package mba

import (
	"fmt"
	"strings"
)

type z3LValue struct {
	kind string // var, mem
	name string
	cell string
}

type z3Stmt struct {
	kind  string // assign, if, block, while, do, for, switch, break
	lhs   z3LValue
	op    string
	expr  *Expr
	cond  *Expr
	then  []z3Stmt
	els   []z3Stmt
	init  []z3Stmt
	post  []z3Stmt
	cases []z3Case
}

type z3Case struct {
	values []*Expr
	def    bool
	body   []z3Stmt
}

type z3Parser struct {
	src  string
	toks []token
	pos  int
}

func parseZ3Program(src string) ([]z3Stmt, error) {
	toks, err := lexC(src)
	if err != nil {
		return nil, err
	}
	p := &z3Parser{src: src, toks: toks}
	stmts, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().text)
	}
	return stmts, nil
}

func (p *z3Parser) peek() token {
	if p.pos >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.pos]
}

func (p *z3Parser) take() token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *z3Parser) accept(text string) bool {
	if p.peek().text == text {
		p.pos++
		return true
	}
	return false
}

func (p *z3Parser) expect(text string) error {
	if !p.accept(text) {
		return fmt.Errorf("expected %q, got %q", text, p.peek().text)
	}
	return nil
}

func (p *z3Parser) parseBlock(expectBrace bool) ([]z3Stmt, error) {
	if expectBrace {
		if err := p.expect("{"); err != nil {
			return nil, err
		}
	}
	var out []z3Stmt
	for {
		t := p.peek()
		if t.kind == tokEOF {
			if expectBrace {
				return nil, fmt.Errorf("unterminated block")
			}
			return out, nil
		}
		if t.text == "}" {
			if expectBrace {
				p.pos++
				return out, nil
			}
			return nil, fmt.Errorf("unexpected }")
		}
		if t.text == ";" {
			p.pos++
			continue
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
}

func (p *z3Parser) parseStmt() (z3Stmt, error) {
	switch p.peek().text {
	case "{":
		block, err := p.parseBlock(true)
		if err != nil {
			return z3Stmt{}, err
		}
		return z3Stmt{kind: "block", then: block}, nil
	case "if":
		return p.parseIf()
	case "while":
		return p.parseWhile()
	case "do":
		return p.parseDo()
	case "for":
		return p.parseFor()
	case "switch":
		return p.parseSwitch()
	case "break":
		p.pos++
		if err := p.expect(";"); err != nil {
			return z3Stmt{}, err
		}
		return z3Stmt{kind: "break"}, nil
	case "continue", "goto", "return":
		return z3Stmt{}, fmt.Errorf("control-flow construct %q requires CFG/invariant support and is not handled in bounded Z3 mode", p.peek().text)
	}
	return p.parseAssignment()
}

func (p *z3Parser) parseIf() (z3Stmt, error) {
	p.pos++ // if
	if err := p.expect("("); err != nil {
		return z3Stmt{}, err
	}
	open := p.toks[p.pos-1]
	depth := 1
	var close token
	for depth > 0 {
		t := p.take()
		if t.kind == tokEOF {
			return z3Stmt{}, fmt.Errorf("unterminated if condition")
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				close = t
			}
		}
	}
	cond, err := ParseExpression(p.src[open.end:close.start])
	if err != nil {
		return z3Stmt{}, err
	}
	then, err := p.parseStmt()
	if err != nil {
		return z3Stmt{}, err
	}
	st := z3Stmt{kind: "if", cond: cond, then: []z3Stmt{then}}
	if p.accept("else") {
		els, err := p.parseStmt()
		if err != nil {
			return z3Stmt{}, err
		}
		st.els = []z3Stmt{els}
	}
	return st, nil
}

func (p *z3Parser) parseParenExpr() (*Expr, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	open := p.toks[p.pos-1]
	depth := 1
	var close token
	for depth > 0 {
		t := p.take()
		if t.kind == tokEOF {
			return nil, fmt.Errorf("unterminated parenthesized expression")
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				close = t
			}
		}
	}
	return ParseExpression(p.src[open.end:close.start])
}

func (p *z3Parser) parseWhile() (z3Stmt, error) {
	p.pos++
	cond, err := p.parseParenExpr()
	if err != nil {
		return z3Stmt{}, err
	}
	body, err := p.parseStmt()
	if err != nil {
		return z3Stmt{}, err
	}
	return z3Stmt{kind: "while", cond: cond, then: []z3Stmt{body}}, nil
}

func (p *z3Parser) parseDo() (z3Stmt, error) {
	p.pos++
	body, err := p.parseStmt()
	if err != nil {
		return z3Stmt{}, err
	}
	if !p.accept("while") {
		return z3Stmt{}, fmt.Errorf("expected while after do body")
	}
	cond, err := p.parseParenExpr()
	if err != nil {
		return z3Stmt{}, err
	}
	if err := p.expect(";"); err != nil {
		return z3Stmt{}, err
	}
	return z3Stmt{kind: "do", cond: cond, then: []z3Stmt{body}}, nil
}

func (p *z3Parser) parseFor() (z3Stmt, error) {
	p.pos++ // for
	if err := p.expect("("); err != nil {
		return z3Stmt{}, err
	}
	initText, err := p.collectHeader(";")
	if err != nil {
		return z3Stmt{}, err
	}
	condText, err := p.collectHeader(";")
	if err != nil {
		return z3Stmt{}, err
	}
	postText, err := p.collectHeader(")")
	if err != nil {
		return z3Stmt{}, err
	}
	init, err := parseZ3Fragment(initText)
	if err != nil {
		return z3Stmt{}, err
	}
	post, err := parseZ3Fragment(postText)
	if err != nil {
		return z3Stmt{}, err
	}
	var cond *Expr
	if strings.TrimSpace(condText) == "" {
		cond = Num(1)
	} else {
		cond, err = ParseExpression(condText)
		if err != nil {
			return z3Stmt{}, err
		}
	}
	body, err := p.parseStmt()
	if err != nil {
		return z3Stmt{}, err
	}
	return z3Stmt{kind: "for", cond: cond, init: init, post: post, then: []z3Stmt{body}}, nil
}

func (p *z3Parser) collectHeader(stop string) (string, error) {
	start := p.peek().start
	depth := 0
	for {
		t := p.take()
		if t.kind == tokEOF {
			return "", fmt.Errorf("unterminated for header")
		}
		switch t.text {
		case "(", "[":
			depth++
		case ")", "]":
			if depth > 0 {
				depth--
			}
		}
		if t.text == stop && depth == 0 {
			return strings.TrimSpace(p.src[start:t.start]), nil
		}
	}
}

func (p *z3Parser) parseSwitch() (z3Stmt, error) {
	p.pos++ // switch
	expr, err := p.parseParenExpr()
	if err != nil {
		return z3Stmt{}, err
	}
	if err := p.expect("{"); err != nil {
		return z3Stmt{}, err
	}
	var cases []z3Case
	for {
		if p.peek().kind == tokEOF {
			return z3Stmt{}, fmt.Errorf("unterminated switch")
		}
		if p.accept("}") {
			break
		}
		if p.accept("case") {
			start := p.peek().start
			var colon token
			for {
				t := p.take()
				if t.kind == tokEOF {
					return z3Stmt{}, fmt.Errorf("unterminated case")
				}
				if t.text == ":" {
					colon = t
					break
				}
			}
			val, err := ParseExpression(p.src[start:colon.start])
			if err != nil {
				return z3Stmt{}, err
			}
			body, err := p.parseCaseBody()
			if err != nil {
				return z3Stmt{}, err
			}
			cases = append(cases, z3Case{values: []*Expr{val}, body: body})
			continue
		}
		if p.accept("default") {
			if err := p.expect(":"); err != nil {
				return z3Stmt{}, err
			}
			body, err := p.parseCaseBody()
			if err != nil {
				return z3Stmt{}, err
			}
			cases = append(cases, z3Case{def: true, body: body})
			continue
		}
		return z3Stmt{}, fmt.Errorf("expected case/default/}, got %q", p.peek().text)
	}
	return z3Stmt{kind: "switch", expr: expr, cases: cases}, nil
}

func (p *z3Parser) parseCaseBody() ([]z3Stmt, error) {
	var out []z3Stmt
	for {
		t := p.peek()
		if t.kind == tokEOF || t.text == "}" || t.text == "case" || t.text == "default" {
			return out, nil
		}
		if t.text == ";" {
			p.pos++
			continue
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
}

func parseZ3Fragment(text string) ([]z3Stmt, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if strings.HasSuffix(text, "++") || strings.HasSuffix(text, "--") {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "+"), "+"))
		if strings.HasSuffix(text, "--") {
			name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "-"), "-"))
		}
		if name == "" || strings.ContainsAny(name, " \t()[].") {
			return nil, fmt.Errorf("unsupported for increment %q", text)
		}
		op := "+"
		if strings.HasSuffix(text, "--") {
			op = "-"
		}
		return []z3Stmt{{
			kind: "assign",
			lhs:  z3LValue{kind: "var", name: name},
			op:   "=",
			expr: Binary(op, Var(name), Num(1)),
		}}, nil
	}
	at := findAssignment(text)
	if at == nil {
		return nil, fmt.Errorf("unsupported for header %q", text)
	}
	lhs, err := parseZ3LValue(strings.TrimSpace(text[:at.start]))
	if err != nil {
		return nil, err
	}
	rhs, err := ParseExpression(strings.TrimSpace(text[at.end:]))
	if err != nil {
		return nil, err
	}
	return []z3Stmt{{kind: "assign", lhs: lhs, op: at.op, expr: rhs}}, nil
}

func (p *z3Parser) parseAssignment() (z3Stmt, error) {
	start := p.peek().start
	depth := 0
	var semi token
	for {
		t := p.take()
		if t.kind == tokEOF {
			return z3Stmt{}, fmt.Errorf("missing ;")
		}
		switch t.text {
		case "(", "[":
			depth++
		case ")", "]":
			if depth > 0 {
				depth--
			}
		case ";":
			if depth == 0 {
				semi = t
			}
		}
		if semi.text == ";" {
			break
		}
		if t.text == "}" {
			return z3Stmt{}, fmt.Errorf("unexpected } in statement")
		}
	}
	text := strings.TrimSpace(p.src[start:semi.start])
	at := findAssignment(text)
	if at == nil {
		return z3Stmt{}, fmt.Errorf("unsupported statement %q", text)
	}
	lhs, err := parseZ3LValue(strings.TrimSpace(text[:at.start]))
	if err != nil {
		return z3Stmt{}, err
	}
	rhs, err := ParseExpression(strings.TrimSpace(text[at.end:]))
	if err != nil {
		return z3Stmt{}, err
	}
	return z3Stmt{kind: "assign", lhs: lhs, op: at.op, expr: rhs}, nil
}

func parseZ3LValue(prefix string) (z3LValue, error) {
	if name, ok := simpleLHSVar(prefix); ok {
		return z3LValue{kind: "var", name: name}, nil
	}
	if strings.HasPrefix(strings.TrimSpace(prefix), "*") {
		if root := rootIdent(prefix); root != "" {
			return z3LValue{kind: "mem", cell: "root:" + root, name: root}, nil
		}
	}
	e, err := ParseExpression(prefix)
	if err != nil {
		return z3LValue{}, fmt.Errorf("unsupported lvalue %q", prefix)
	}
	switch e.Kind {
	case KVar:
		return z3LValue{kind: "var", name: e.Name}, nil
	case KMember:
		return z3LValue{kind: "mem", cell: "member:" + e.Key(), name: e.Format(32)}, nil
	case KUnary:
		if e.Op == "*" && len(e.Kids) == 1 && e.Kids[0].Kind == KVar {
			n := e.Kids[0].Name
			return z3LValue{kind: "mem", cell: "root:" + n, name: n}, nil
		}
	}
	return z3LValue{}, fmt.Errorf("unsupported lvalue %q", prefix)
}

func (b *smtBuilder) execZ3(st *smtState, stmts []z3Stmt) error {
	for _, s := range stmts {
		switch s.kind {
		case "block":
			if err := b.execZ3(st, s.then); err != nil {
				return err
			}
		case "assign":
			rhs, err := b.expr(st, s.expr)
			if err != nil {
				return err
			}
			var old string
			if s.lhs.kind == "var" {
				old = b.varValue(st, s.lhs.name)
			} else {
				old = b.cellValue(st, s.lhs.cell)
			}
			next, err := b.apply(s.op, old, rhs)
			if err != nil {
				return err
			}
			if s.lhs.kind == "var" {
				st.vars[s.lhs.name] = next
			} else {
				st.mem[s.lhs.cell] = next
			}
		case "if":
			cond, err := b.expr(st, s.cond)
			if err != nil {
				return err
			}
			a := st.clone()
			c := st.clone()
			if err := b.execZ3(a, s.then); err != nil {
				return err
			}
			if err := b.execZ3(c, s.els); err != nil {
				return err
			}
			merged := b.mergeZ3(cond, a, c)
			*st = *merged
		case "while":
			if containsBreak(s.then) {
				return fmt.Errorf("break/continue inside while requires CFG support")
			}
			if err := b.execLoop(st, s.cond, s.then, nil, z3LoopBound); err != nil {
				return err
			}
		case "do":
			if containsBreak(s.then) {
				return fmt.Errorf("break/continue inside do requires CFG support")
			}
			if err := b.execZ3(st, s.then); err != nil {
				return err
			}
			if err := b.execLoop(st, s.cond, s.then, nil, z3LoopBound-1); err != nil {
				return err
			}
		case "for":
			if containsBreak(s.then) {
				return fmt.Errorf("break/continue inside for requires CFG support")
			}
			if err := b.execZ3(st, s.init); err != nil {
				return err
			}
			if err := b.execLoop(st, s.cond, s.then, s.post, z3LoopBound); err != nil {
				return err
			}
		case "switch":
			if err := b.execSwitch(st, s); err != nil {
				return err
			}
		case "break":
			return fmt.Errorf("break outside a supported switch")
		default:
			return fmt.Errorf("unsupported Z3 statement")
		}
	}
	return nil
}

func containsBreak(stmts []z3Stmt) bool {
	for _, s := range stmts {
		if s.kind == "break" {
			return true
		}
		if s.kind == "switch" {
			// A break inside a switch belongs to the switch, not an enclosing loop.
			continue
		}
		if containsBreak(s.then) || containsBreak(s.els) || containsBreak(s.init) || containsBreak(s.post) {
			return true
		}
	}
	return false
}

func (b *smtBuilder) execLoop(st *smtState, cond *Expr, body, post []z3Stmt, limit int) error {
	b.bounded = true
	for i := 0; i < limit; i++ {
		c, err := b.expr(st, cond)
		if err != nil {
			return err
		}
		next := st.clone()
		if err := b.execZ3(next, body); err != nil {
			return err
		}
		if len(post) > 0 {
			if err := b.execZ3(next, post); err != nil {
				return err
			}
		}
		merged := b.mergeZ3(c, next, st)
		*st = *merged
	}
	return nil
}

func (b *smtBuilder) execSwitch(st *smtState, s z3Stmt) error {
	swExpr, err := b.expr(st, s.expr)
	if err != nil {
		return err
	}
	var defBody []z3Stmt
	var cases []z3Case
	for _, c := range s.cases {
		if c.def {
			defBody = c.body
			continue
		}
		cases = append(cases, c)
	}
	// Default arm is the fallback state.
	current := st.clone()
	if len(defBody) > 0 {
		var err error
		defBody, err = stripTrailingBreak(defBody)
		if err != nil {
			return err
		}
		if err := b.execZ3(current, defBody); err != nil {
			return err
		}
	}
	for i := len(cases) - 1; i >= 0; i-- {
		body, err := stripTrailingBreak(cases[i].body)
		if err != nil {
			return err
		}
		then := st.clone()
		if err := b.execZ3(then, body); err != nil {
			return err
		}
		var cond string
		for _, v := range cases[i].values {
			ve, err := b.expr(st, v)
			if err != nil {
				return err
			}
			eq := b.boolToBV(fmt.Sprintf("(= %s %s)", swExpr, ve))
			if cond == "" {
				cond = eq
			} else {
				cond = b.boolToBV(fmt.Sprintf("(or %s %s)", b.nonzero(cond), b.nonzero(eq)))
			}
		}
		current = b.mergeZ3(cond, then, current)
	}
	*st = *current
	return nil
}

func stripTrailingBreak(stmts []z3Stmt) ([]z3Stmt, error) {
	if len(stmts) == 0 {
		return stmts, nil
	}
	if stmts[len(stmts)-1].kind == "break" {
		return stmts[:len(stmts)-1], nil
	}
	for _, s := range stmts {
		if containsBreak([]z3Stmt{s}) {
			return nil, fmt.Errorf("break inside a switch case must be the final statement")
		}
	}
	return nil, fmt.Errorf("switch fallthrough requires CFG support; add a break at the end of each case")
}

func (s *smtState) clone() *smtState {
	out := &smtState{vars: map[string]string{}, mem: map[string]string{}}
	for k, v := range s.vars {
		out.vars[k] = v
	}
	for k, v := range s.mem {
		out.mem[k] = v
	}
	return out
}

func (b *smtBuilder) mergeZ3(cond string, a, c *smtState) *smtState {
	out := &smtState{vars: map[string]string{}, mem: map[string]string{}}
	keys := map[string]bool{}
	for k := range a.vars {
		keys["v:"+k] = true
	}
	for k := range c.vars {
		keys["v:"+k] = true
	}
	for k := range a.mem {
		keys["m:"+k] = true
	}
	for k := range c.mem {
		keys["m:"+k] = true
	}
	condNZ := b.nonzero(cond)
	for key := range keys {
		if strings.HasPrefix(key, "v:") {
			k := key[2:]
			av := a.vars[k]
			if av == "" {
				av = b.varSymbol(k)
			}
			cv := c.vars[k]
			if cv == "" {
				cv = b.varSymbol(k)
			}
			out.vars[k] = fmt.Sprintf("(ite %s %s %s)", condNZ, av, cv)
		} else {
			k := key[2:]
			av := a.mem[k]
			if av == "" {
				av = b.cellSymbol(k)
			}
			cv := c.mem[k]
			if cv == "" {
				cv = b.cellSymbol(k)
			}
			out.mem[k] = fmt.Sprintf("(ite %s %s %s)", condNZ, av, cv)
		}
	}
	return out
}

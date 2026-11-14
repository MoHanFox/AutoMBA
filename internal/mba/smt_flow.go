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
	kind string // assign, if, block
	lhs  z3LValue
	op   string
	expr *Expr
	cond *Expr
	then []z3Stmt
	els  []z3Stmt
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
	case "while", "for", "do", "switch", "goto", "return", "break", "continue", "case", "default":
		return z3Stmt{}, fmt.Errorf("control-flow construct %q is not supported by Z3 mode", p.peek().text)
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
		default:
			return fmt.Errorf("unsupported Z3 statement")
		}
	}
	return nil
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

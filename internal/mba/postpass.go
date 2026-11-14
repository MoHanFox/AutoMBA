package mba

import (
	"fmt"
	"sort"
	"strings"
)

type postStmt struct {
	chunk  string
	lead   string
	body   string
	prefix string
	op     string
	rhs    *Expr
	name   string
	simple bool
	mem    bool
	semi   bool
}

func parsePostStmt(chunk string) (postStmt, bool) {
	ps := postStmt{chunk: chunk}
	trimmedLeft := strings.TrimLeft(chunk, " \t\r\n")
	ps.lead = chunk[:len(chunk)-len(trimmedLeft)]
	if i := strings.LastIndexByte(ps.lead, '\n'); i >= 0 {
		ps.lead = ps.lead[i+1:]
	}
	body := strings.TrimRight(trimmedLeft, " \t\r\n")
	if body == "" {
		return ps, false
	}
	if strings.HasSuffix(body, ";") {
		ps.semi = true
		body = strings.TrimRight(body[:len(body)-1], " \t\r\n")
	}
	ps.body = body
	at := findAssignment(body)
	if at == nil {
		return ps, false
	}
	ps.prefix = strings.TrimRight(body[:at.start], " \t\r\n")
	rhsText := strings.TrimSpace(body[at.end:])
	e, err := ParseExpression(rhsText)
	if err != nil {
		return ps, false
	}
	ps.op = at.op
	ps.rhs = e
	if name, ok := simpleLHSVar(ps.prefix); ok {
		ps.name = name
		ps.simple = true
	} else if strings.HasPrefix(strings.TrimSpace(ps.prefix), "*") {
		ps.mem = true
	}
	return ps, true
}

func varsInExpr(e *Expr) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(*Expr)
	walk = func(n *Expr) {
		if n == nil {
			return
		}
		if n.Kind == KVar && !seen[n.Name] {
			seen[n.Name] = true
			out = append(out, n.Name)
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(e)
	return out
}

func rawVars(body string) []string {
	toks, err := lexC(body)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range toks {
		if t.kind == tokIdent && !typeWords[t.text] && !seen[t.text] {
			seen[t.text] = true
			out = append(out, t.text)
		}
	}
	return out
}

// RemoveDeadCode removes pure scalar assignments whose value is never used
// later. Calls and memory reads are kept because they may have side effects.
func RemoveDeadCode(code string, width uint) string {
	chunks := splitStatements(code)
	stmts := make([]postStmt, len(chunks))
	for i, ch := range chunks {
		ps, _ := parsePostStmt(ch)
		if ps.chunk == "" {
			ps.chunk = ch
		}
		stmts[i] = ps
	}
	used := map[string]bool{}
	keep := make([]bool, len(stmts))
	for i := len(stmts) - 1; i >= 0; i-- {
		ps := stmts[i]
		if ps.simple && ps.op == "=" && !used[ps.name] && isPureExpr(ps.rhs) {
			keep[i] = false
			continue
		}
		keep[i] = true
		if ps.rhs != nil {
			for _, v := range varsInExpr(ps.rhs) {
				used[v] = true
			}
		} else {
			for _, v := range rawVars(ps.body) {
				used[v] = true
			}
		}
		if ps.simple {
			if ps.op == "=" {
				delete(used, ps.name)
			} else {
				used[ps.name] = true
			}
		}
	}
	var b strings.Builder
	for i, ps := range stmts {
		if keep[i] {
			b.WriteString(ps.chunk)
		}
	}
	return b.String()
}

type cseCand struct {
	key   string
	expr  *Expr
	count int
	first int
	last  int
	vars  map[string]bool
}

// EliminateCSE introduces temporaries for large pure subexpressions that occur
// in multiple statements and are stable between their first and last use.
func EliminateCSE(code string, width uint, signed bool) string {
	chunks := splitStatements(code)
	stmts := make([]postStmt, len(chunks))
	for i, ch := range chunks {
		ps, ok := parsePostStmt(ch)
		if !ok {
			ps = postStmt{chunk: ch, body: ch}
		}
		stmts[i] = ps
	}
	cands := map[string]*cseCand{}
	for i, ps := range stmts {
		if ps.rhs == nil {
			continue
		}
		var walk func(*Expr, bool)
		walk = func(e *Expr, isRoot bool) {
			if e == nil {
				return
			}
			if !isRoot && e.Cost() >= 3 && isPureExpr(e) && e.Kind != KVar && e.Kind != KNum {
				key := e.Key()
				c := cands[key]
				if c == nil {
					c = &cseCand{key: key, expr: e, first: i, last: i, vars: map[string]bool{}}
					for _, v := range varsInExpr(e) {
						c.vars[v] = true
					}
					cands[key] = c
				}
				if c.first > i {
					c.first = i
				}
				if c.last < i {
					c.last = i
				}
				c.count++
			}
			for _, k := range e.Kids {
				walk(k, false)
			}
		}
		walk(ps.rhs, true)
	}
	var list []*cseCand
	for _, c := range cands {
		if c.count >= 2 && c.first < c.last {
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		ci := list[i].count * list[i].expr.Cost()
		cj := list[j].count * list[j].expr.Cost()
		if ci != cj {
			return ci > cj
		}
		return list[i].key < list[j].key
	})
	if len(list) > 6 {
		list = list[:6]
	}

	type replacement struct {
		temp string
		expr *Expr
		at   int
	}
	var reps []replacement
	for _, c := range list {
		safe := true
		for i := c.first + 1; i <= c.last && safe; i++ {
			ps := stmts[i]
			if ps.simple {
				if c.vars[ps.name] {
					safe = false
				}
			}
			if ps.mem {
				root := rootIdent(ps.prefix)
				if c.vars[root] {
					safe = false
				}
			}
		}
		if safe {
			reps = append(reps, replacement{
				temp: fmt.Sprintf("__cse%d", len(reps)+1),
				expr: c.expr,
				at:   c.first,
			})
		}
	}
	if len(reps) == 0 {
		return code
	}

	// Replace occurrences in statement ASTs.
	for i := 0; i < len(stmts); i++ {
		ps := &stmts[i]
		if ps.rhs == nil {
			continue
		}
		for _, r := range reps {
			if i < r.at {
				continue
			}
			ps.rhs = replaceSubexpr(ps.rhs, r.expr.Key(), Var(r.temp))
		}
	}

	var b strings.Builder
	for i, ps := range stmts {
		for _, r := range reps {
			if r.at == i {
				b.WriteString(ps.lead)
				b.WriteString(cseType(width, signed) + " " + r.temp + " = " + r.expr.Format(width) + ";\n")
			}
		}
		if ps.rhs != nil {
			line := ps.lead + ps.prefix + " " + ps.op + " " + ps.rhs.Format(width) + semiIf(ps.semi)
			b.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteByte('\n')
			}
		} else {
			b.WriteString(ps.chunk)
			if !strings.HasSuffix(ps.chunk, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func cseType(width uint, signed bool) string {
	if width >= 64 {
		if signed {
			return "__int64"
		}
		return "unsigned __int64"
	}
	if width == 16 {
		if signed {
			return "short"
		}
		return "unsigned short"
	}
	if width == 8 {
		if signed {
			return "char"
		}
		return "unsigned char"
	}
	if signed {
		return "int"
	}
	return "unsigned int"
}

func replaceSubexpr(e *Expr, key string, repl *Expr) *Expr {
	if e == nil {
		return nil
	}
	if e.Key() == key {
		return repl.Clone()
	}
	out := e.Clone()
	for i, k := range e.Kids {
		out.Kids[i] = replaceSubexpr(k, key, repl)
	}
	return out
}

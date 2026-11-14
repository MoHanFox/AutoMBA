package mba

import (
	"strings"
)

type deepState struct {
	defs     map[string]*Expr
	maxNodes int
}

func newDeepState() *deepState {
	return &deepState{defs: map[string]*Expr{}, maxNodes: 6000}
}

// substitute replaces pure scalar variables by their known definitions. It
// bails out (returns nil) if the expansion would become too large.
func (d *deepState) invalidateDependents(name string) {
	for k, def := range d.defs {
		if hasVar(def, name) {
			delete(d.defs, k)
		}
	}
}

func hasVar(e *Expr, name string) bool {
	if e == nil {
		return false
	}
	if e.Kind == KVar && e.Name == name {
		return true
	}
	for _, k := range e.Kids {
		if hasVar(k, name) {
			return true
		}
	}
	return false
}

func (d *deepState) substitute(e *Expr) *Expr {
	if e == nil || len(d.defs) == 0 {
		return e.Clone()
	}
	count := 0
	stack := map[string]bool{}
	var rec func(*Expr, int) *Expr
	rec = func(n *Expr, depth int) *Expr {
		if n == nil || depth > 16 {
			return nil
		}
		if n.Kind == KVar {
			if def, ok := d.defs[n.Name]; ok && !stack[n.Name] {
				count += def.Cost()
				if count > d.maxNodes {
					return nil
				}
				stack[n.Name] = true
				r := rec(def.Clone(), depth+1)
				delete(stack, n.Name)
				if r == nil {
					return nil
				}
				return r
			}
			return n.Clone()
		}
		out := n.Clone()
		for i, k := range n.Kids {
			r := rec(k, depth+1)
			if r == nil {
				return nil
			}
			out.Kids[i] = r
		}
		return out
	}
	return rec(e, 0)
}

// ProcessDeep performs a dataflow-aware simplification pass: pure scalar
// definitions are substituted into later expressions when that makes the
// expression smaller. It is used by the "以结果再简化" button.
func ProcessDeep(code string, width uint) ProcessResult {
	res := ProcessResult{}
	if width != 8 && width != 16 && width != 32 && width != 64 {
		width = 32
	}
	code = strings.TrimPrefix(code, "\ufeff")
	code = strings.ReplaceAll(code, "\r\n", "\n")
	code = strings.ReplaceAll(code, "\r", "\n")
	if strings.TrimSpace(code) == "" {
		res.Error = "请输入要简化的 C 表达式或代码"
		return res
	}
	if len(code) > maxInputBytes {
		res.Error = "输入过大（上限 512 KB）"
		return res
	}

	// A bare expression has no dataflow to exploit.
	if e, err := ParseExpression(strings.TrimSpace(code)); err == nil {
		out := New(width).Simplify(e).Format(width)
		res.Output = out
		res.Changed = out != code
		res.Statements = 1
		res.Simplified = 1
		res.Passes = 1
		return res
	}

	if !hasTopLevelSemicolon(code) {
		if findAssignment(strings.TrimSpace(code)) == nil {
			return Process(code, width, false)
		}
	}

	// Substitution is only sound for straight-line code. Fall back to the
	// ordinary per-expression pass when control flow is present.
	if hasControlFlow(code) {
		res := Process(code, width, false)
		res.Warnings = append(res.Warnings, "检测到控制流，已退化为逐表达式化简（不做跨语句传播）")
		return res
	}

	cur := code
	passes := 0
	totalStmts, totalSimpl := 0, 0
	var warnings []string
	for pass := 0; pass < 2; pass++ {
		s := New(width)
		ds := newDeepState()
		chunks := splitStatements(cur)
		var b strings.Builder
		stmts, simpl := 0, 0
		for _, chunk := range chunks {
			out, st, sp, warns := processChunk(chunk, s, ds)
			b.WriteString(out)
			stmts += st
			simpl += sp
			warnings = append(warnings, warns...)
		}
		next := b.String()
		passes++
		totalStmts, totalSimpl = stmts, simpl
		if next == cur {
			cur = next
			break
		}
		cur = next
	}
	res.Output = cur
	res.Changed = cur != code
	res.Statements = totalStmts
	res.Simplified = totalSimpl
	res.Passes = passes
	res.Warnings = dedupeWarnings(warnings)
	return res
}

func hasControlFlow(code string) bool {
	toks, err := lexC(code)
	if err != nil {
		return true
	}
	for _, t := range toks {
		if t.kind != tokIdent {
			continue
		}
		switch t.text {
		case "if", "else", "for", "while", "do", "switch", "case", "default", "goto":
			return true
		}
	}
	return false
}

func isPureExpr(e *Expr) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case KNum, KVar:
		return true
	case KUnary:
		switch e.Op {
		case "~", "-", "+", "!":
			return isPureExpr(e.Kids[0])
		default:
			return false
		}
	case KBinary:
		return isPureExpr(e.Kids[0]) && isPureExpr(e.Kids[1])
	case KCast:
		return isPureExpr(e.Kids[0])
	case KTernary:
		return isPureExpr(e.Kids[0]) && isPureExpr(e.Kids[1]) && isPureExpr(e.Kids[2])
	default:
		return false
	}
}

func simpleLHSVar(prefix string) (string, bool) {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return "", false
	}
	for _, bad := range []string{"*", "[", "]", "(", ")", ".", "->", "?", ":"} {
		if strings.Contains(p, bad) {
			return "", false
		}
	}
	end := len(p)
	start := end
	for start > 0 && isIdentPart(p[start-1]) {
		start--
	}
	if start == end || !isIdentStart(p[start]) {
		return "", false
	}
	name := p[start:end]
	if typeWords[name] {
		return "", false
	}
	return name, true
}

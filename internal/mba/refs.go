package mba

import (
	"fmt"
	"strings"
)

// ProcessRefs performs aggressive symbolic inlining while keeping only
// statements whose root reference is listed in keepRefs (for example
// "a1;a2;a3"). Pure scalar temporaries are eliminated; opaque calls and loads
// are inlined as atoms.
func ProcessRefs(code string, width uint, keepRefs string) ProcessResult {
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
	keep := parseKeepRefs(keepRefs)
	if len(keep) == 0 {
		return Process(code, width, false)
	}
	if hasControlFlow(code) {
		out := Process(code, width, false)
		out.Warnings = append(out.Warnings, "检测到控制流，已退化为逐表达式化简（未做引用合并）")
		return out
	}

	rp := &refsProcessor{
		s:        New(width),
		width:    width,
		keep:     keep,
		defs:     map[string]*defEntry{},
		maxNodes: 40000,
	}
	for _, chunk := range splitStatements(code) {
		rp.processChunk(chunk)
	}

	res.Output = rp.output()
	res.Changed = res.Output != code
	res.Statements = rp.emitted
	res.Simplified = rp.eliminated
	res.Merged = rp.eliminated
	res.Passes = 1
	res.Warnings = dedupeWarnings(rp.warnings)
	if strings.TrimSpace(res.Output) == "" {
		res.Error = "没有与引用列表匹配的语句"
	}
	return res
}

type defEntry struct {
	name string
	expr *Expr
	pure bool
	used int
	line string
}

type refOut struct {
	text string
	def  *defEntry
}

type refsProcessor struct {
	s          *Simplifier
	width      uint
	keep       map[string]bool
	defs       map[string]*defEntry
	out        []refOut
	warnings   []string
	maxNodes   int
	emitted    int
	eliminated int
}

func parseKeepRefs(s string) map[string]bool {
	res := map[string]bool{}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	for _, f := range fields {
		f = strings.TrimSpace(f)
		f = strings.TrimLeft(f, "*&")
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var b strings.Builder
		for i := 0; i < len(f); i++ {
			c := f[i]
			if isIdentPart(c) {
				b.WriteByte(c)
				continue
			}
			break
		}
		if b.Len() > 0 {
			res[b.String()] = true
		}
	}
	return res
}

func (rp *refsProcessor) output() string {
	var lines []string
	for _, o := range rp.out {
		if o.text != "" {
			lines = append(lines, o.text)
			continue
		}
		if o.def == nil {
			continue
		}
		if o.def.used == 0 && !o.def.pure {
			// Preserve the side effect of a call/load whose value was never
			// consumed.
			lines = append(lines, o.def.line+o.def.expr.Format(rp.width)+";")
		}
	}
	return strings.Join(lines, "\n")
}

func (rp *refsProcessor) processChunk(chunk string) {
	trimmedLeft := strings.TrimLeft(chunk, " \t\r\n")
	leading := chunk[:len(chunk)-len(trimmedLeft)]
	if i := strings.LastIndexByte(leading, '\n'); i >= 0 {
		leading = leading[i+1:]
	}
	body := strings.TrimRight(trimmedLeft, " \t\r\n")
	if body == "" {
		return
	}
	hasSemi := false
	if strings.HasSuffix(body, ";") {
		hasSemi = true
		body = strings.TrimRight(body[:len(body)-1], " \t\r\n")
	}
	if body == "" {
		return
	}
	at := findAssignment(body)
	if at == nil {
		if rest, ok := stripReturn(body); ok {
			e, err := ParseExpression(strings.TrimSpace(rest))
			if err == nil {
				e = rp.subst(e)
				out := rp.s.Simplify(e).Format(rp.width)
				prefix := body[:len(body)-len(rest)]
				if prefix != "" && !strings.HasSuffix(prefix, " ") {
					prefix += " "
				}
				rp.emit(leading + prefix + out + semiIf(hasSemi))
				return
			}
		}
		if e, err := ParseExpression(strings.TrimSpace(body)); err == nil {
			e = rp.subst(e)
			out := rp.s.Simplify(e).Format(rp.width)
			rp.emit(leading + out + semiIf(hasSemi))
			return
		}
		// Keep labels, braces and anything else that is not an expression.
		rp.emit(leading + body + semiIf(hasSemi))
		return
	}

	prefix := strings.TrimRight(body[:at.start], " \t\r\n")
	rhs := strings.TrimSpace(body[at.end:])
	if prefix == "" || rhs == "" {
		rp.emit(leading + body + semiIf(hasSemi))
		return
	}
	e, err := ParseExpression(rhs)
	if err != nil {
		rp.emit(leading + body + semiIf(hasSemi))
		rp.warnings = append(rp.warnings, fmt.Sprintf("无法解析表达式 %q，已原样保留", truncate(rhs, 70)))
		return
	}
	e = rp.subst(e)
	simplified := rp.s.Simplify(e)

	name, isSimple := simpleLHSVar(prefix)
	root := rootIdent(prefix)
	kept := rp.keep[root]

	if isSimple && root != "" && !kept && at.op == "=" {
		rp.invalidate(name)
		def := &defEntry{name: name, expr: simplified, pure: isPureExpr(simplified), line: leading}
		rp.defs[name] = def
		rp.out = append(rp.out, refOut{def: def})
		rp.eliminated++
		return
	}
	if isSimple && root != "" && !kept && at.op != "=" && strings.HasSuffix(at.op, "=") {
		// v += e  =>  v = v <op> e, then keep it as a definition.
		baseOp := strings.TrimSuffix(at.op, "=")
		combined := Binary(baseOp, Var(name), e)
		combined = rp.s.Simplify(rp.subst(combined))
		rp.invalidate(name)
		def := &defEntry{name: name, expr: combined, pure: isPureExpr(combined), line: leading}
		rp.defs[name] = def
		rp.out = append(rp.out, refOut{def: def})
		rp.eliminated++
		return
	}

	line := prefix + " " + at.op + " " + simplified.Format(rp.width) + semiIf(hasSemi)
	rp.emit(leading + line)
	if isSimple {
		rp.invalidate(name)
		if kept {
			delete(rp.defs, name)
		}
	}
}

func semiIf(has bool) string {
	if has {
		return ";"
	}
	return ""
}

func (rp *refsProcessor) emit(line string) {
	rp.out = append(rp.out, refOut{text: line})
	rp.emitted++
}

func (rp *refsProcessor) invalidate(name string) {
	for k, def := range rp.defs {
		if hasVar(def.expr, name) {
			delete(rp.defs, k)
		}
	}
}

func (rp *refsProcessor) subst(e *Expr) *Expr {
	if e == nil || len(rp.defs) == 0 {
		return e
	}
	count := 0
	stack := map[string]bool{}
	var rec func(*Expr, int) *Expr
	rec = func(n *Expr, depth int) *Expr {
		if n == nil || depth > 100 {
			return n
		}
		if n.Kind == KVar {
			if def, ok := rp.defs[n.Name]; ok && !stack[n.Name] {
				def.used++
				count += def.expr.Cost()
				if count > rp.maxNodes {
					rp.warnings = append(rp.warnings, "表达式展开过大，已停止继续内联部分变量")
					return n.Clone()
				}
				stack[n.Name] = true
				r := rec(def.expr.Clone(), depth+1)
				delete(stack, n.Name)
				return r
			}
			return n.Clone()
		}
		out := n.Clone()
		for i, k := range n.Kids {
			out.Kids[i] = rec(k, depth+1)
		}
		return out
	}
	return rec(e, 0)
}

// rootIdent extracts the base identifier of an lvalue: *a1 -> a1,
// a1->field -> a1, arr[i] -> arr, "unsigned int v5" -> v5.
func rootIdent(prefix string) string {
	toks, err := lexC(prefix)
	if err != nil {
		return ""
	}
	for _, t := range toks {
		if t.kind != tokIdent || typeWords[t.text] {
			continue
		}
		return t.text
	}
	return ""
}

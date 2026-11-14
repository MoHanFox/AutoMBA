package mba

import (
	"fmt"
	"strings"
)

// ProcessResult is the result of processing a C source fragment.
type ProcessResult struct {
	Output     string   `json:"output"`
	Changed    bool     `json:"changed"`
	Statements int      `json:"statements"`
	Simplified int      `json:"simplified"`
	Merged     int      `json:"merged"`
	Passes     int      `json:"passes"`
	Warnings   []string `json:"warnings,omitempty"`
	Error      string   `json:"error,omitempty"`
}

const maxInputBytes = 512 * 1024

// Process simplifies all assignments/expressions in a C fragment. If iterate
// is true, it keeps re-simplifying its own output until a fixed point.
func Process(code string, width uint, iterate bool) ProcessResult {
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

	maxPasses := 1
	if iterate {
		maxPasses = 6
	}
	cur := code
	passes := 0
	totalStatements := 0
	totalSimplified := 0
	var warnings []string
	for pass := 0; pass < maxPasses; pass++ {
		out, stmts, simpl, warns, err := processOnce(cur, width)
		passes++
		if err != nil {
			res.Error = err.Error()
			res.Output = cur
			res.Passes = passes
			return res
		}
		totalStatements = stmts
		totalSimplified = simpl
		warnings = append(warnings, warns...)
		if out == cur {
			cur = out
			break
		}
		cur = out
		if !iterate {
			break
		}
	}
	res.Output = cur
	res.Changed = cur != code
	res.Statements = totalStatements
	res.Simplified = totalSimplified
	res.Passes = passes
	res.Warnings = dedupeWarnings(warnings)
	return res
}

func dedupeWarnings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range in {
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func processOnce(code string, width uint) (string, int, int, []string, error) {
	s := New(width)
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return code, 0, 0, nil, nil
	}

	// A single expression (possibly spread over several lines).
	if e, err := ParseExpression(trimmed); err == nil {
		out := s.Simplify(e).Format(width)
		return out, 1, 1, nil, nil
	}

	// No statement separators: treat the text as assignment/return, or, as a
	// last resort, as one expression per line.
	if !hasTopLevelSemicolon(code) {
		if at := findAssignment(trimmed); at != nil {
			out, stmt, simplified, warns := processChunk(trimmed, s, nil)
			_ = stmt
			return out, 1, simplified, warns, nil
		}
		if out, ok := processExpressionLines(code, s, width); ok {
			return out, countNonEmptyLines(code), countNonEmptyLines(code), nil, nil
		}
		return code, 0, 0, []string{"无法解析输入，已原样保留"}, nil
	}

	chunks := splitStatements(code)
	var b strings.Builder
	stmts := 0
	simplified := 0
	var warnings []string
	for _, chunk := range chunks {
		out, stmt, simpl, warns := processChunk(chunk, s, nil)
		b.WriteString(out)
		stmts += stmt
		simplified += simpl
		warnings = append(warnings, warns...)
	}
	return b.String(), stmts, simplified, warnings, nil
}

func hasTopLevelSemicolon(code string) bool {
	for _, ch := range splitStatements(code) {
		if strings.Contains(ch, ";") {
			return true
		}
	}
	return false
}

// splitStatements splits at semicolons that are not inside parentheses,
// brackets, strings, or comments.
func splitStatements(src string) []string {
	var out []string
	start := 0
	paren, bracket := 0, 0
	inString, inChar := false, false
	lineComment, blockComment := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inString || inChar {
			if c == '\\' {
				i++
				continue
			}
			if inString && c == '"' {
				inString = false
			}
			if inChar && c == '\'' {
				inChar = false
			}
			continue
		}
		if c == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				lineComment = true
				i++
				continue
			}
			if src[i+1] == '*' {
				blockComment = true
				i++
				continue
			}
		}
		switch c {
		case '"':
			inString = true
		case '\'':
			inChar = true
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '[':
			bracket++
		case ']':
			if bracket > 0 {
				bracket--
			}
		case ';':
			if paren == 0 && bracket == 0 {
				out = append(out, src[start:i+1])
				start = i + 1
			}
		}
	}
	if start < len(src) {
		out = append(out, src[start:])
	}
	return out
}

type assignAt struct {
	start, end int
	op         string
}

func findAssignment(body string) *assignAt {
	toks, err := lexC(body)
	if err != nil {
		return nil
	}
	paren, bracket := 0, 0
	for i, t := range toks {
		switch t.text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		}
		if paren != 0 || bracket != 0 {
			continue
		}
		switch t.text {
		case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
			// Avoid matching an operator that is part of a longer token (the
			// lexer already emits maximal operators, so this is safe).
			_ = i
			return &assignAt{start: t.start, end: t.end, op: t.text}
		}
	}
	return nil
}

func processChunk(chunk string, s *Simplifier, ds *deepState) (string, int, int, []string) {
	orig := chunk
	trimmedLeft := strings.TrimLeft(chunk, " \t\r\n")
	leading := chunk[:len(chunk)-len(trimmedLeft)]
	body := strings.TrimRight(trimmedLeft, " \t\r\n")
	if body == "" {
		return orig, 0, 0, nil
	}

	// Separate a trailing semicolon.
	hasSemi := false
	if strings.HasSuffix(body, ";") {
		hasSemi = true
		body = strings.TrimRight(body[:len(body)-1], " \t\r\n")
	}
	if body == "" {
		return orig, 0, 0, nil
	}

	at := findAssignment(body)
	if at == nil {
		if rest, ok := stripReturn(body); ok {
			e, err := ParseExpression(strings.TrimSpace(rest))
			if err != nil {
				return orig, 1, 0, []string{"无法解析 return 表达式，已原样保留"}
			}
			out := s.Simplify(e).Format(s.Width)
			prefix := body[:len(body)-len(rest)]
			if prefix != "" && !strings.HasSuffix(prefix, " ") {
				prefix += " "
			}
			newBody := prefix + out
			if hasSemi {
				newBody += ";"
			}
			if strings.TrimSpace(newBody) == strings.TrimSpace(body) {
				return leading + newBody, 1, 0, nil
			}
			return leading + newBody, 1, 1, nil
		}
		// A bare expression statement, e.g. "x + x;" or a final expression
		// without a semicolon.
		if e, err := ParseExpression(strings.TrimSpace(body)); err == nil {
			out := s.Simplify(e).Format(s.Width)
			newBody := out
			if hasSemi {
				newBody += ";"
			}
			changed := 0
			if newBody != strings.TrimSpace(body) {
				changed = 1
			}
			return leading + newBody, 1, changed, nil
		}
		return orig, 0, 0, nil
	}

	prefix := strings.TrimRight(body[:at.start], " \t\r\n")
	rhs := strings.TrimSpace(body[at.end:])
	if prefix == "" || rhs == "" {
		return orig, 1, 0, nil
	}
	e, err := ParseExpression(rhs)
	if err != nil {
		return orig, 1, 0, []string{fmt.Sprintf("无法解析表达式 %q，已原样保留", truncate(rhs, 80))}
	}
	chosen := s.Simplify(e)
	if ds != nil {
		if sub := ds.substitute(e); sub != nil {
			deep := s.Simplify(sub)
			if deep.Cost() < chosen.Cost() {
				chosen = deep
			}
		}
		if name, ok := simpleLHSVar(prefix); ok {
			// Any assignment invalidates definitions that depend on the value.
			ds.invalidateDependents(name)
			if at.op == "=" && isPureExpr(chosen) {
				ds.defs[name] = chosen.Clone()
			} else {
				delete(ds.defs, name)
			}
		}
	}
	out := chosen.Format(s.Width)
	newBody := prefix + " " + at.op + " " + out
	if hasSemi {
		newBody += ";"
	}
	changed := 0
	if newBody != body {
		changed = 1
	}
	return leading + newBody, 1, changed, nil
}

func stripReturn(body string) (string, bool) {
	if !strings.HasPrefix(body, "return") {
		return "", false
	}
	rest := body[len("return"):]
	if rest != "" && !isSpaceByte(rest[0]) {
		return "", false
	}
	return rest, true
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func processExpressionLines(code string, s *Simplifier, width uint) (string, bool) {
	lines := strings.Split(code, "\n")
	var b strings.Builder
	okCount := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			b.WriteString(line)
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
			continue
		}
		e, err := ParseExpression(trimmed)
		if err != nil {
			return "", false
		}
		okCount++
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		b.WriteString(indent)
		b.WriteString(s.Simplify(e).Format(width))
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), okCount > 0
}

func countNonEmptyLines(code string) int {
	n := 0
	for _, l := range strings.Split(code, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

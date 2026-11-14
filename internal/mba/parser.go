package mba

import (
	"fmt"
	"strconv"
	"strings"
)

type parser struct {
	toks []token
	pos  int
	src  string
}

// ParseExpression parses a single C expression. It accepts the C subset that
// appears in decompiler output and keeps unsupported constructs as atoms.
func ParseExpression(src string) (*Expr, error) {
	toks, err := lexC(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, src: src}
	e, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("表达式末尾有多余内容 %q", p.peek().text)
	}
	return e, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}
func (p *parser) accept(text string) bool {
	if p.peek().text == text {
		p.pos++
		return true
	}
	return false
}
func (p *parser) expect(text string) error {
	if !p.accept(text) {
		return fmt.Errorf("期望 %q，实际是 %q", text, p.peek().text)
	}
	return nil
}

func (p *parser) parseExpr(minPrec int) (*Expr, error) {
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	left, err = p.parsePostfix(left)
	if err != nil {
		return nil, err
	}

	for {
		t := p.peek()
		// Ternary has the lowest precedence and is right associative.
		if t.text == "?" && minPrec <= 1 {
			p.pos++
			a, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			b, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			left = Ternary(left, a, b)
			continue
		}
		prec, ok := binaryPrecedence[t.text]
		if !ok || prec < minPrec {
			break
		}
		p.pos++
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = Binary(t.text, left, right)
	}
	return left, nil
}

var binaryPrecedence = map[string]int{
	"||": 2,
	"&&": 3,
	"|":  4,
	"^":  5,
	"&":  6,
	"==": 7,
	"!=": 7,
	"<":  8,
	"<=": 8,
	">":  8,
	">=": 8,
	"<<": 9,
	">>": 9,
	"+":  10,
	"-":  10,
	"*":  11,
	"/":  11,
	"%":  11,
}

func (p *parser) parsePrefix() (*Expr, error) {
	t := p.peek()
	switch {
	case t.kind == tokNumber:
		p.pos++
		v, err := parseCInteger(t.text)
		if err != nil {
			return nil, err
		}
		return Num(v), nil
	case t.kind == tokString || t.kind == tokChar:
		p.pos++
		return Opaque(t.text), nil
	case t.kind == tokIdent:
		if t.text == "sizeof" {
			return p.parseSizeof()
		}
		p.pos++
		return Var(t.text), nil
	case t.text == "(":
		if c, ok := p.tryParseCast(); ok {
			return c, nil
		}
		p.pos++
		e, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return e, nil
	case t.text == "+" || t.text == "-" || t.text == "~" || t.text == "!" ||
		t.text == "*" || t.text == "&" || t.text == "++" || t.text == "--":
		p.pos++
		x, err := p.parseUnaryOperand()
		if err != nil {
			return nil, err
		}
		return Unary(t.text, x), nil
	}
	return nil, fmt.Errorf("无法解析表达式，遇到 %q", t.text)
}

func (p *parser) parsePostfix(e *Expr) (*Expr, error) {
	for {
		t := p.peek()
		switch t.text {
		case "(":
			p.pos++
			var args []*Expr
			if !p.accept(")") {
				for {
					a, err := p.parseExpr(1)
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.accept(")") {
						break
					}
					if err := p.expect(","); err != nil {
						return nil, err
					}
				}
			}
			e = Call(e, args...)
		case "[":
			p.pos++
			idx, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			e = Index(e, idx)
		case ".", "->":
			p.pos++
			id := p.peek()
			if id.kind != tokIdent {
				return nil, fmt.Errorf("成员访问后期望标识符，实际是 %q", id.text)
			}
			p.pos++
			e = Member(e, t.text, id.text)
		case "++", "--":
			p.pos++
			e = Unary("post"+t.text, e)
		default:
			return e, nil
		}
	}
}

func (p *parser) parseSizeof() (*Expr, error) {
	start := p.pos
	p.pos++ // sizeof
	if p.peek().text != "(" {
		x, err := p.parsePrefix()
		if err != nil {
			return nil, err
		}
		return Unary("sizeof", x), nil
	}
	depth := 0
	for {
		t := p.peek()
		if t.kind == tokEOF {
			return nil, fmt.Errorf("sizeof 括号没有闭合")
		}
		if t.text == "(" {
			depth++
		} else if t.text == ")" {
			depth--
			if depth == 0 {
				p.pos++
				raw := p.src[p.toks[start].start:p.toks[p.pos-1].end]
				return Opaque(raw), nil
			}
		}
		p.pos++
	}
}

func (p *parser) tryParseCast() (*Expr, bool) {
	save := p.pos
	open := p.peek()
	p.pos++ // (
	_, ok := p.parseTypeName()
	if !ok || p.peek().text != ")" {
		p.pos = save
		return nil, false
	}
	p.pos++ // )
	if !startsUnary(p.peek()) {
		p.pos = save
		return nil, false
	}
	// The textual type is taken from the source to preserve spelling.
	closeTok := p.toks[p.pos-1]
	raw := strings.TrimSpace(p.src[open.end:closeTok.start])
	x, err := p.parsePrefix()
	if err != nil {
		p.pos = save
		return nil, false
	}
	x, err = p.parsePostfix(x)
	if err != nil {
		p.pos = save
		return nil, false
	}
	return Cast(raw, x), true
}

func (p *parser) parseUnaryOperand() (*Expr, error) {
	x, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	return p.parsePostfix(x)
}

var typeWords = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true,
	"float": true, "double": true, "signed": true, "unsigned": true,
	"bool": true, "_Bool": true, "const": true, "volatile": true,
	"__int8": true, "__int16": true, "__int32": true, "__int64": true,
	"__int128": true, "size_t": true, "ssize_t": true,
	"int8_t": true, "int16_t": true, "int32_t": true, "int64_t": true,
	"uint8_t": true, "uint16_t": true, "uint32_t": true, "uint64_t": true,
	"uintptr_t": true, "intptr_t": true, "wchar_t": true,
	"DWORD": true, "WORD": true, "BYTE": true, "QWORD": true, "OWORD": true,
	"ULONG": true, "LONG": true, "UINT": true, "INT": true, "BOOL": true,
	"PVOID": true, "HANDLE": true, "LPSTR": true, "LPCSTR": true, "LPVOID": true,
	"_DWORD": true, "_QWORD": true, "_BYTE": true, "_WORD": true, "_OWORD": true,
	"uint": true, "uchar": true, "ushort": true, "ulong": true, "byte": true,
	"struct": true, "union": true, "enum": true, "typedef": true,
}

func (p *parser) parseTypeName() (string, bool) {
	if p.peek().kind != tokIdent {
		return "", false
	}
	first := p.peek()
	if !typeWords[first.text] && !looksLikeTypeName(first.text) {
		return "", false
	}
	start := first.start
	consumed := false
	for {
		t := p.peek()
		if t.kind == tokIdent && (typeWords[t.text] || (consumed && looksLikeTypeName(t.text))) {
			p.pos++
			consumed = true
			continue
		}
		if t.text == "*" {
			p.pos++
			consumed = true
			continue
		}
		break
	}
	if !consumed {
		return "", false
	}
	end := p.toks[p.pos-1].end
	return strings.TrimSpace(p.src[start:end]), true
}

func looksLikeTypeName(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	if !(c >= 'A' && c <= 'Z') && c != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentPart(s[i]) {
			return false
		}
	}
	return true
}

func startsUnary(t token) bool {
	switch t.kind {
	case tokNumber, tokIdent, tokString, tokChar:
		return true
	}
	switch t.text {
	case "(", "~", "!", "-", "+", "*", "&", "++", "--":
		return true
	}
	return false
}

func parseCInteger(s string) (uint64, error) {
	orig := s
	s = strings.ReplaceAll(s, "'", "")
	lower := strings.ToLower(s)
	for _, suffix := range []string{"ui64", "i64", "ull", "llu", "ul", "lu", "ll", "u", "l"} {
		if strings.HasSuffix(lower, suffix) {
			s = s[:len(s)-len(suffix)]
			break
		}
	}
	if s == "" {
		return 0, fmt.Errorf("无效整数 %q", orig)
	}
	base := 10
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		base = 16
		s = s[2:]
	case strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B"):
		base = 2
		s = s[2:]
	case len(s) > 1 && s[0] == '0':
		base = 8
		s = s[1:]
	default:
		base = 10
	}
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析整数 %q", orig)
	}
	return v, nil
}

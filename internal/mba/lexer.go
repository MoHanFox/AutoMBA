package mba

import "fmt"

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokChar
	tokOperator
)

type token struct {
	kind       tokenKind
	text       string
	start, end int
}

func (t token) String() string { return t.text }

type lexer struct {
	src  string
	pos  int
	toks []token
}

func lexC(src string) ([]token, error) {
	l := &lexer{src: src}
	for {
		if err := l.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		if l.pos >= len(l.src) {
			break
		}
		start := l.pos
		c := l.src[l.pos]
		switch {
		case isIdentStart(c):
			l.pos++
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			l.toks = append(l.toks, token{kind: tokIdent, text: l.src[start:l.pos], start: start, end: l.pos})
		case c >= '0' && c <= '9':
			l.lexNumber()
			t := l.toks[len(l.toks)-1]
			if t.kind == tokOperator {
				// A float or otherwise unsupported number.
				return nil, fmt.Errorf("不支持的数值字面量 %q", t.text)
			}
		case c == '"' || c == '\'':
			if err := l.lexQuoted(c); err != nil {
				return nil, err
			}
		default:
			op := l.longestOperator()
			if op == "" {
				return nil, fmt.Errorf("无法识别的字符 %q（位置 %d）", string(c), start)
			}
			l.pos += len(op)
			l.toks = append(l.toks, token{kind: tokOperator, text: op, start: start, end: l.pos})
		}
	}
	l.toks = append(l.toks, token{kind: tokEOF, start: len(l.src), end: len(l.src)})
	return l.toks, nil
}

func (l *lexer) skipSpaceAndComments() error {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v' {
			l.pos++
			continue
		}
		if c == '/' && l.pos+1 < len(l.src) {
			if l.src[l.pos+1] == '/' {
				l.pos += 2
				for l.pos < len(l.src) && l.src[l.pos] != '\n' {
					l.pos++
				}
				continue
			}
			if l.src[l.pos+1] == '*' {
				end := l.pos + 2
				closed := false
				for end+1 < len(l.src) {
					if l.src[end] == '*' && l.src[end+1] == '/' {
						end += 2
						closed = true
						break
					}
					end++
				}
				if !closed {
					return fmt.Errorf("块注释没有闭合")
				}
				l.pos = end
				continue
			}
		}
		break
	}
	return nil
}

func (l *lexer) lexNumber() {
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			c == 'x' || c == 'X' || c == '_' {
			l.pos++
			continue
		}
		if c == '\'' {
			// C23 digit separator.
			l.pos++
			continue
		}
		break
	}
	text := l.src[start:l.pos]
	kind := tokNumber
	if containsFloatSyntax(text) {
		kind = tokOperator
	}
	l.toks = append(l.toks, token{kind: kind, text: text, start: start, end: l.pos})
}

func containsFloatSyntax(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
		if (s[i] == 'e' || s[i] == 'E') && !hasHexPrefix(s) {
			return true
		}
	}
	return false
}

func hasHexPrefix(s string) bool {
	return len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
}

func (l *lexer) lexQuoted(quote byte) error {
	start := l.pos
	l.pos++
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\\' {
			l.pos += 2
			continue
		}
		l.pos++
		if c == quote {
			kind := tokString
			if quote == '\'' {
				kind = tokChar
			}
			l.toks = append(l.toks, token{kind: kind, text: l.src[start:l.pos], start: start, end: l.pos})
			return nil
		}
	}
	return fmt.Errorf("字符串或字符字面量没有闭合")
}

var operators = []string{
	"<<=", ">>=", "...", "->", "++", "--", "&&", "||", "==", "!=", "<=", ">=",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "::",
	"+", "-", "*", "/", "%", "&", "|", "^", "~", "!", "<", ">", "=", "?", ":", ";",
	",", ".", "(", ")", "[", "]", "{", "}",
}

func (l *lexer) longestOperator() string {
	for _, op := range operators {
		if len(l.src)-l.pos >= len(op) && l.src[l.pos:l.pos+len(op)] == op {
			return op
		}
	}
	return ""
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

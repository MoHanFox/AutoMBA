package mba

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is the kind of an expression node.
type Kind uint8

const (
	KNum Kind = iota
	KVar
	KUnary
	KBinary
	KCall
	KIndex
	KMember
	KCast
	KTernary
	KOpaque
)

// Expr is a small C expression AST. It is deliberately permissive: anything
// that is not needed for MBA reasoning is kept as an opaque atom.
type Expr struct {
	Kind Kind
	Op   string
	Name string
	Num  uint64
	Kids []*Expr
}

func Num(v uint64) *Expr             { return &Expr{Kind: KNum, Num: v} }
func Var(name string) *Expr          { return &Expr{Kind: KVar, Name: name} }
func Unary(op string, x *Expr) *Expr { return &Expr{Kind: KUnary, Op: op, Kids: []*Expr{x}} }
func Binary(op string, a, b *Expr) *Expr {
	return &Expr{Kind: KBinary, Op: op, Kids: []*Expr{a, b}}
}
func Call(callee *Expr, args ...*Expr) *Expr {
	return &Expr{Kind: KCall, Kids: append([]*Expr{callee}, args...)}
}
func Index(base, idx *Expr) *Expr { return &Expr{Kind: KIndex, Kids: []*Expr{base, idx}} }
func Member(base *Expr, op, name string) *Expr {
	return &Expr{Kind: KMember, Op: op, Name: name, Kids: []*Expr{base}}
}
func Cast(name string, x *Expr) *Expr { return &Expr{Kind: KCast, Name: name, Kids: []*Expr{x}} }
func Ternary(cond, a, b *Expr) *Expr {
	return &Expr{Kind: KTernary, Op: "?:", Kids: []*Expr{cond, a, b}}
}
func Opaque(text string) *Expr { return &Expr{Kind: KOpaque, Name: text} }

// Clone returns a deep copy.
func (e *Expr) Clone() *Expr {
	if e == nil {
		return nil
	}
	n := &Expr{Kind: e.Kind, Op: e.Op, Name: e.Name, Num: e.Num}
	if len(e.Kids) > 0 {
		n.Kids = make([]*Expr, len(e.Kids))
		for i, k := range e.Kids {
			n.Kids[i] = k.Clone()
		}
	}
	return n
}

// Key is a structural fingerprint. It intentionally ignores whitespace.
func (e *Expr) Key() string {
	if e == nil {
		return "nil"
	}
	var b strings.Builder
	e.keyInto(&b)
	return b.String()
}

func (e *Expr) keyInto(b *strings.Builder) {
	switch e.Kind {
	case KNum:
		b.WriteString("n")
		b.WriteString(strconv.FormatUint(e.Num, 16))
	case KVar:
		b.WriteString("v:")
		b.WriteString(e.Name)
	case KUnary:
		b.WriteString("u(")
		b.WriteString(e.Op)
		b.WriteByte(',')
		e.Kids[0].keyInto(b)
		b.WriteByte(')')
	case KBinary:
		b.WriteString("b(")
		b.WriteString(e.Op)
		b.WriteByte(',')
		e.Kids[0].keyInto(b)
		b.WriteByte(',')
		e.Kids[1].keyInto(b)
		b.WriteByte(')')
	case KCall:
		b.WriteString("c(")
		for i, k := range e.Kids {
			if i > 0 {
				b.WriteByte(',')
			}
			k.keyInto(b)
		}
		b.WriteByte(')')
	case KIndex:
		b.WriteString("i(")
		e.Kids[0].keyInto(b)
		b.WriteByte(',')
		e.Kids[1].keyInto(b)
		b.WriteByte(')')
	case KMember:
		b.WriteString("m(")
		b.WriteString(e.Op)
		b.WriteString(e.Name)
		b.WriteByte(',')
		e.Kids[0].keyInto(b)
		b.WriteByte(')')
	case KCast:
		b.WriteString("t(")
		b.WriteString(e.Name)
		b.WriteByte(',')
		e.Kids[0].keyInto(b)
		b.WriteByte(')')
	case KTernary:
		b.WriteString("q(")
		for i, k := range e.Kids {
			if i > 0 {
				b.WriteByte(',')
			}
			k.keyInto(b)
		}
		b.WriteByte(')')
	case KOpaque:
		b.WriteString("o:")
		b.WriteString(e.Name)
	}
}

// Cost is a rough expression-size metric used to choose between equivalent
// candidate forms. Constants and variables count as one node.
func (e *Expr) Cost() int {
	if e == nil {
		return 0
	}
	c := 1
	for _, k := range e.Kids {
		c += k.Cost()
	}
	return c
}

func (e *Expr) CountNodes() int {
	if e == nil {
		return 0
	}
	n := 1
	for _, k := range e.Kids {
		n += k.CountNodes()
	}
	return n
}

func (e *Expr) HasOp(op string) bool {
	if e == nil {
		return false
	}
	if (e.Kind == KUnary || e.Kind == KBinary || e.Kind == KTernary) && e.Op == op {
		return true
	}
	for _, k := range e.Kids {
		if k.HasOp(op) {
			return true
		}
	}
	return false
}

func (e *Expr) CollectVars(dst *[]string, seen map[string]bool) {
	if e == nil {
		return
	}
	if e.Kind == KVar {
		if !seen[e.Name] {
			seen[e.Name] = true
			*dst = append(*dst, e.Name)
		}
	}
	for _, k := range e.Kids {
		k.CollectVars(dst, seen)
	}
}

func maskFor(width uint) uint64 {
	if width == 0 || width >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << width) - 1
}

func allOnes(width uint) uint64 { return maskFor(width) }

func (e *Expr) IsConst() bool { return e != nil && e.Kind == KNum }

func (e *Expr) IsZero() bool { return e != nil && e.Kind == KNum && e.Num == 0 }

func (e *Expr) IsAllOnes(width uint) bool {
	return e != nil && e.Kind == KNum && e.Num&maskFor(width) == maskFor(width)
}

// isBitwiseExpr reports whether e is built exclusively from bitwise operations,
// variables and constants.
func isBitwiseExpr(e *Expr) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case KNum, KVar:
		return true
	case KUnary:
		return e.Op == "~" && isBitwiseExpr(e.Kids[0])
	case KBinary:
		if e.Op != "&" && e.Op != "|" && e.Op != "^" {
			return false
		}
		return isBitwiseExpr(e.Kids[0]) && isBitwiseExpr(e.Kids[1])
	default:
		return false
	}
}

func isBitwiseRoot(e *Expr) bool {
	if e == nil {
		return false
	}
	if e.Kind == KUnary {
		return e.Op == "~"
	}
	if e.Kind == KBinary {
		return e.Op == "&" || e.Op == "|" || e.Op == "^"
	}
	return false
}

// precedence follows C's operator precedence. Assignment is handled outside
// the expression parser.
func precedence(e *Expr) int {
	if e == nil {
		return 0
	}
	switch e.Kind {
	case KTernary:
		return 1
	case KBinary:
		switch e.Op {
		case "||":
			return 2
		case "&&":
			return 3
		case "|":
			return 4
		case "^":
			return 5
		case "&":
			return 6
		case "==", "!=":
			return 7
		case "<", "<=", ">", ">=":
			return 8
		case "<<", ">>":
			return 9
		case "+", "-":
			return 10
		case "*", "/", "%":
			return 11
		}
	case KUnary:
		return 12
	case KCast:
		return 12
	case KCall, KIndex, KMember:
		return 13
	}
	return 14
}

// Format renders the expression as C source using the given bit width for
// constant normalization.
func (e *Expr) Format(width uint) string {
	var b strings.Builder
	e.formatInto(&b, 0, width, false)
	return b.String()
}

func (e *Expr) formatInto(b *strings.Builder, parentPrec int, width uint, rightChild bool) {
	if e == nil {
		b.WriteString("0")
		return
	}
	myPrec := precedence(e)
	needParen := myPrec < parentPrec
	if needParen {
		b.WriteByte('(')
	}
	switch e.Kind {
	case KNum:
		b.WriteString(FormatConst(e.Num, width))
	case KVar:
		b.WriteString(e.Name)
	case KOpaque:
		b.WriteString(e.Name)
	case KUnary:
		if e.Op == "sizeof" {
			b.WriteString("sizeof(")
			e.Kids[0].formatInto(b, 0, width, false)
			b.WriteByte(')')
			break
		}
		b.WriteString(e.Op)
		// Avoid producing --x or ++x accidentally.
		if e.Op == "-" || e.Op == "+" {
			if e.Kids[0].Kind == KUnary && (e.Kids[0].Op == "-" || e.Kids[0].Op == "+" || e.Kids[0].Op == "--" || e.Kids[0].Op == "++") {
				b.WriteByte('(')
				e.Kids[0].formatInto(b, 0, width, false)
				b.WriteByte(')')
				break
			}
		}
		e.Kids[0].formatInto(b, myPrec, width, false)
	case KCast:
		b.WriteByte('(')
		b.WriteString(e.Name)
		b.WriteByte(')')
		e.Kids[0].formatInto(b, myPrec, width, false)
	case KCall:
		callee := e.Kids[0]
		if callee.Kind == KVar || callee.Kind == KMember || callee.Kind == KIndex || callee.Kind == KCall {
			callee.formatInto(b, myPrec, width, false)
		} else {
			b.WriteByte('(')
			callee.formatInto(b, 0, width, false)
			b.WriteByte(')')
		}
		b.WriteByte('(')
		for i, a := range e.Kids[1:] {
			if i > 0 {
				b.WriteString(", ")
			}
			a.formatInto(b, 0, width, false)
		}
		b.WriteByte(')')
	case KIndex:
		e.Kids[0].formatInto(b, myPrec, width, false)
		b.WriteByte('[')
		e.Kids[1].formatInto(b, 0, width, false)
		b.WriteByte(']')
	case KMember:
		e.Kids[0].formatInto(b, myPrec, width, false)
		b.WriteString(e.Op)
		b.WriteString(e.Name)
	case KTernary:
		e.Kids[0].formatInto(b, 2, width, false)
		b.WriteString(" ? ")
		e.Kids[1].formatInto(b, 2, width, false)
		b.WriteString(" : ")
		e.Kids[2].formatInto(b, 1, width, true)
	case KBinary:
		e.Kids[0].formatInto(b, myPrec, width, false)
		b.WriteByte(' ')
		b.WriteString(e.Op)
		b.WriteByte(' ')
		rightPrec := myPrec
		// For non-associative operators a same-precedence right child needs
		// parentheses to preserve the tree.
		if rightChild {
			_ = rightChild
		}
		if e.Op == "-" || e.Op == "/" || e.Op == "%" || e.Op == "<<" || e.Op == ">>" {
			rightPrec++
		}
		e.Kids[1].formatInto(b, rightPrec, width, true)
	}
	if needParen {
		b.WriteByte(')')
	}
}

// FormatConst renders a width-bit constant in a compact C form.
func FormatConst(v uint64, width uint) string {
	m := maskFor(width)
	v &= m
	if v == 0 {
		return "0"
	}
	if v == m {
		return "-1"
	}
	// Choose a signed representation when it is visibly shorter.
	if width > 0 && width < 64 {
		signBit := uint64(1) << (width - 1)
		if v&signBit != 0 {
			signed := int64(v | ^m)
			if signed < 0 && -signed <= 0x1000 {
				return strconv.FormatInt(signed, 10)
			}
		}
	} else if width >= 64 && v&(1<<63) != 0 {
		signed := int64(v)
		if signed < 0 && -signed <= 0x1000 {
			return strconv.FormatInt(signed, 10)
		}
	}
	if v <= 9 {
		return strconv.FormatUint(v, 10)
	}
	s := "0x" + strings.ToUpper(strconv.FormatUint(v, 16))
	if width >= 64 && v > 0xFFFFFFFF {
		s += "ULL"
	}
	return s
}

// Eval evaluates an expression for concrete 64-bit values. Atoms (variables,
// calls, loads, casts, ...) are looked up by Key in env. The result is reduced
// modulo 2^width.
func Eval(e *Expr, width uint, env map[string]uint64) (uint64, bool) {
	if e == nil {
		return 0, false
	}
	m := maskFor(width)
	switch e.Kind {
	case KNum:
		return e.Num & m, true
	case KVar:
		v, ok := env[e.Key()]
		return v & m, ok
	case KUnary:
		x, ok := Eval(e.Kids[0], width, env)
		if !ok {
			return 0, false
		}
		switch e.Op {
		case "~":
			return (^x) & m, true
		case "-":
			return (-x) & m, true
		case "+":
			return x, true
		case "!":
			if x == 0 {
				return 1, true
			}
			return 0, true
		}
		return 0, false
	case KBinary:
		a, ok1 := Eval(e.Kids[0], width, env)
		b, ok2 := Eval(e.Kids[1], width, env)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch e.Op {
		case "+":
			return (a + b) & m, true
		case "-":
			return (a - b) & m, true
		case "*":
			return (a * b) & m, true
		case "/":
			if b == 0 {
				return 0, false
			}
			return (a / b) & m, true
		case "%":
			if b == 0 {
				return 0, false
			}
			return (a % b) & m, true
		case "&":
			return a & b, true
		case "|":
			return a | b, true
		case "^":
			return a ^ b, true
		case "<<":
			if b >= uint64(width) {
				return 0, true
			}
			return (a << b) & m, true
		case ">>":
			if b >= uint64(width) {
				return 0, true
			}
			return (a >> b) & m, true
		case "==":
			return boolWord(a == b), true
		case "!=":
			return boolWord(a != b), true
		case "<":
			return boolWord(a < b), true
		case "<=":
			return boolWord(a <= b), true
		case ">":
			return boolWord(a > b), true
		case ">=":
			return boolWord(a >= b), true
		case "&&":
			return boolWord(a != 0 && b != 0), true
		case "||":
			return boolWord(a != 0 || b != 0), true
		}
		return 0, false
	case KTernary:
		c, ok := Eval(e.Kids[0], width, env)
		if !ok {
			return 0, false
		}
		if c != 0 {
			return Eval(e.Kids[1], width, env)
		}
		return Eval(e.Kids[2], width, env)
	case KCall, KIndex, KMember, KCast, KOpaque:
		v, ok := env[e.Key()]
		return v & m, ok
	}
	return 0, false
}

func boolWord(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// replaceAtoms replaces atom expressions by other expressions, keyed by Key.
func replaceAtoms(e *Expr, repl map[string]*Expr) *Expr {
	if e == nil {
		return nil
	}
	if r, ok := repl[e.Key()]; ok {
		return r.Clone()
	}
	n := e.Clone()
	for i := range n.Kids {
		n.Kids[i] = replaceAtoms(n.Kids[i], repl)
	}
	return n
}

// atomEnv maps each atom expression to a value, keyed by Key.
func atomEnv(atoms []*Expr, vals []uint64) map[string]uint64 {
	env := make(map[string]uint64, len(atoms))
	for i, a := range atoms {
		env[a.Key()] = vals[i]
	}
	return env
}

func (e *Expr) String() string {
	return fmt.Sprintf("%s", e.Format(64))
}

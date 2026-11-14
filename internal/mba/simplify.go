package mba

import (
	"fmt"
	"sort"
	"strings"
)

// Simplifier performs width-aware MBA simplification.
type Simplifier struct {
	Width           uint
	MaxBitwiseAtoms int
	MaxPasses       int

	boolTables map[int][]*Expr
}

func New(width uint) *Simplifier {
	if width == 0 || width > 64 {
		width = 32
	}
	return &Simplifier{
		Width:           width,
		MaxBitwiseAtoms: 6,
		MaxPasses:       8,
	}
}

// Simplify rewrites an expression until it reaches a fixed point (bounded).
func (s *Simplifier) Simplify(e *Expr) *Expr {
	if e == nil {
		return e
	}
	cur := e
	for i := 0; i < s.MaxPasses; i++ {
		next := s.rewrite(cur, 0)
		if next == nil || next.Key() == cur.Key() {
			return next
		}
		cur = next
	}
	return cur
}

func (s *Simplifier) rewrite(e *Expr, depth int) *Expr {
	if e == nil || depth > 400 {
		return e
	}
	if isBitwiseRoot(e) {
		return s.rewriteBitwise(e, depth+1)
	}
	switch e.Kind {
	case KNum:
		return Num(e.Num & maskFor(s.Width))
	case KVar:
		return e
	case KUnary:
		child := s.rewrite(e.Kids[0], depth+1)
		switch e.Op {
		case "+":
			return child
		case "-":
			if child.Kind == KNum {
				return Num((^child.Num + 1) & maskFor(s.Width))
			}
			if child.Kind == KUnary && child.Op == "-" {
				return child.Kids[0]
			}
			return Unary("-", child)
		case "~":
			if child.Kind == KNum {
				return Num((^child.Num) & maskFor(s.Width))
			}
			if child.Kind == KUnary && child.Op == "~" {
				return child.Kids[0]
			}
			return Unary("~", child)
		case "!":
			if child.Kind == KNum {
				if child.Num == 0 {
					return Num(1)
				}
				return Num(0)
			}
			if child.Kind == KUnary && child.Op == "!" {
				return Unary("!", Unary("!", child))
			}
			return Unary("!", child)
		default:
			return Unary(e.Op, child)
		}
	case KBinary:
		switch e.Op {
		case "+", "-", "*":
			return s.rewriteArithmetic(e, depth+1)
		default:
			left := s.rewrite(e.Kids[0], depth+1)
			right := s.rewrite(e.Kids[1], depth+1)
			if left.Kind == KNum && right.Kind == KNum {
				if v, ok := foldBinaryConst(e.Op, left.Num, right.Num, s.Width); ok {
					return Num(v)
				}
			}
			switch e.Op {
			case "<<":
				if right.Kind == KNum && right.Num == 0 {
					return left
				}
			case ">>":
				if right.Kind == KNum && right.Num == 0 {
					return left
				}
			case "&", "|", "^":
				// Should have been caught by isBitwiseRoot, but keep a
				// safety net for nodes produced by other rewrites.
				return s.rewriteBitwise(e, depth+1)
			}
			return Binary(e.Op, left, right)
		}
	case KCall:
		n := e.Clone()
		for i := range n.Kids {
			n.Kids[i] = s.rewrite(n.Kids[i], depth+1)
		}
		return n
	case KIndex:
		return Index(s.rewrite(e.Kids[0], depth+1), s.rewrite(e.Kids[1], depth+1))
	case KMember:
		return Member(s.rewrite(e.Kids[0], depth+1), e.Op, e.Name)
	case KCast:
		return Cast(e.Name, s.rewrite(e.Kids[0], depth+1))
	case KTernary:
		return Ternary(s.rewrite(e.Kids[0], depth+1), s.rewrite(e.Kids[1], depth+1), s.rewrite(e.Kids[2], depth+1))
	case KOpaque:
		return e
	}
	return e
}

func foldBinaryConst(op string, a, b uint64, width uint) (uint64, bool) {
	m := maskFor(width)
	a &= m
	b &= m
	switch op {
	case "+":
		return (a + b) & m, true
	case "-":
		return (a - b) & m, true
	case "*":
		return (a * b) & m, true
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
}

// rewriteBitwise chooses the cheapest of a structural rewrite and an exact
// bit-level ANF form.
func (s *Simplifier) rewriteBitwise(e *Expr, depth int) *Expr {
	structural := s.rewriteBitwiseStructural(e, depth+1)
	anf := s.anfSimplify(e, depth+1)
	if anf == nil {
		return structural
	}
	if structural == nil {
		return anf
	}
	// Prefer the structural form on equal cost: it usually stays closer to the
	// original shape and is easier to read.
	if anf.Cost() < structural.Cost() {
		return anf
	}
	return structural
}

func (s *Simplifier) rewriteBitwiseStructural(e *Expr, depth int) *Expr {
	if e == nil || depth > 400 {
		return e
	}
	switch e.Kind {
	case KNum:
		return Num(e.Num & maskFor(s.Width))
	case KVar:
		return e
	case KUnary:
		if e.Op != "~" {
			return s.rewrite(e, depth+1)
		}
		child := s.rewriteBitwiseStructural(e.Kids[0], depth+1)
		if child.Kind == KNum {
			return Num((^child.Num) & maskFor(s.Width))
		}
		if child.Kind == KUnary && child.Op == "~" {
			return child.Kids[0]
		}
		return Unary("~", child)
	case KBinary:
		if e.Op != "&" && e.Op != "|" && e.Op != "^" {
			return s.rewrite(e, depth+1)
		}
		ops := s.flattenBitwise(e, e.Op, depth+1)
		switch e.Op {
		case "&":
			return s.buildAnd(ops, depth+1)
		case "|":
			return s.buildOr(ops, depth+1)
		case "^":
			return s.buildXor(ops, depth+1)
		}
	}
	return s.rewrite(e, depth+1)
}

func (s *Simplifier) flattenBitwise(e *Expr, op string, depth int) []*Expr {
	var out []*Expr
	var walk func(*Expr)
	walk = func(n *Expr) {
		if n.Kind == KBinary && n.Op == op {
			walk(n.Kids[0])
			walk(n.Kids[1])
			return
		}
		if isBitwiseRoot(n) {
			out = append(out, s.rewriteBitwiseStructural(n, depth+1))
		} else {
			out = append(out, s.rewrite(n, depth+1))
		}
	}
	walk(e)
	return out
}

func (s *Simplifier) buildAnd(ops []*Expr, depth int) *Expr {
	m := maskFor(s.Width)
	constVal := uint64(m)
	hasConst := false
	var terms []*Expr
	seen := map[string]bool{}
	for _, op := range ops {
		op = s.rewriteBitwiseStructural(op, depth+1)
		if op.Kind == KNum {
			constVal &= op.Num
			hasConst = true
			if constVal == 0 {
				return Num(0)
			}
			continue
		}
		if seen[op.Key()] {
			continue
		}
		seen[op.Key()] = true
		terms = append(terms, op)
	}
	// x & ~x == 0
	for _, t := range terms {
		if t.Kind == KUnary && t.Op == "~" && seen[t.Kids[0].Key()] {
			return Num(0)
		}
	}
	// Absorption: x & (x | y) == x.  Drop the disjunction, not x.
	var kept []*Expr
	for i, u := range terms {
		absorbed := false
		if u.Kind == KBinary && u.Op == "|" {
			for j, t := range terms {
				if i != j && hasDirectOperand(u, "|", t) {
					absorbed = true
					break
				}
			}
		}
		if !absorbed {
			kept = append(kept, u)
		}
	}
	terms = kept
	if hasConst && constVal != m {
		terms = append(terms, Num(constVal))
	}
	if len(terms) == 0 {
		if hasConst {
			return Num(constVal)
		}
		return Num(m)
	}
	res := terms[0]
	for _, t := range terms[1:] {
		res = Binary("&", res, t)
	}
	return s.factorCommon(res, depth+1)
}

func (s *Simplifier) buildOr(ops []*Expr, depth int) *Expr {
	m := maskFor(s.Width)
	constVal := uint64(0)
	hasConst := false
	var terms []*Expr
	seen := map[string]bool{}
	for _, op := range ops {
		op = s.rewriteBitwiseStructural(op, depth+1)
		if op.Kind == KNum {
			constVal |= op.Num
			hasConst = true
			if constVal == m {
				return Num(m)
			}
			continue
		}
		if seen[op.Key()] {
			continue
		}
		seen[op.Key()] = true
		terms = append(terms, op)
	}
	// x | ~x == all ones
	for _, t := range terms {
		if t.Kind == KUnary && t.Op == "~" && seen[t.Kids[0].Key()] {
			return Num(m)
		}
	}
	// Absorption: x | (x & y) == x.  Drop the conjunction, not x.
	var kept []*Expr
	for i, u := range terms {
		absorbed := false
		if u.Kind == KBinary && u.Op == "&" {
			for j, t := range terms {
				if i != j && hasDirectOperand(u, "&", t) {
					absorbed = true
					break
				}
			}
		}
		if !absorbed {
			kept = append(kept, u)
		}
	}
	terms = kept
	if hasConst && constVal != 0 {
		terms = append(terms, Num(constVal))
	}
	if len(terms) == 0 {
		if hasConst {
			return Num(constVal)
		}
		return Num(0)
	}
	res := terms[0]
	for _, t := range terms[1:] {
		res = Binary("|", res, t)
	}
	res = s.simplifyMuxOr(res, depth+1)
	return s.factorCommon(res, depth+1)
}

// simplifyMuxOr recognises (B|C)&m | B&~m == B | (C&m) and its symmetric
// form. It is common after bit-slice synthesis, where masks split the word.
func (s *Simplifier) simplifyMuxOr(e *Expr, depth int) *Expr {
	if e == nil || e.Kind != KBinary || e.Op != "|" {
		return e
	}
	all := maskFor(s.Width)
	for iter := 0; iter < 4; iter++ {
		terms := flattenSameOp(e, "|")
		changed := false
		for i := 0; i < len(terms) && !changed; i++ {
			ei, mi, oki := maskedTerm(terms[i])
			if !oki {
				continue
			}
			for j := i + 1; j < len(terms); j++ {
				ej, mj, okj := maskedTerm(terms[j])
				if !okj {
					continue
				}
				if mi&mj != 0 || mi|mj != all {
					continue
				}
				var replacement *Expr
				if c, ok := removeOrOperand(ei, ej); ok {
					replacement = Binary("|", ej.Clone(), Binary("&", c, Num(mi)))
				} else if c, ok := removeOrOperand(ej, ei); ok {
					replacement = Binary("|", ei.Clone(), Binary("&", c, Num(mj)))
				}
				if replacement == nil {
					continue
				}
				var rest []*Expr
				for k, t := range terms {
					if k == i || k == j {
						continue
					}
					rest = append(rest, t)
				}
				res := replacement
				for _, t := range rest {
					res = Binary("|", res, t)
				}
				e = res
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	return e
}

func maskedTerm(e *Expr) (*Expr, uint64, bool) {
	if e == nil || e.Kind != KBinary || e.Op != "&" {
		return nil, 0, false
	}
	if e.Kids[0].Kind == KNum {
		return e.Kids[1], e.Kids[0].Num, true
	}
	if e.Kids[1].Kind == KNum {
		return e.Kids[0], e.Kids[1].Num, true
	}
	return nil, 0, false
}

// removeOrOperand returns the disjunction remainder of container after removing
// one occurrence of target.
func removeOrOperand(container, target *Expr) (*Expr, bool) {
	if container == nil || target == nil {
		return nil, false
	}
	parts := flattenSameOp(container, "|")
	removed := false
	var rest []*Expr
	for _, p := range parts {
		if !removed && p.Key() == target.Key() {
			removed = true
			continue
		}
		rest = append(rest, p)
	}
	if !removed {
		return nil, false
	}
	if len(rest) == 0 {
		return Num(0), true
	}
	res := rest[0]
	for _, p := range rest[1:] {
		res = Binary("|", res, p)
	}
	return res, true
}

func (s *Simplifier) buildXor(ops []*Expr, depth int) *Expr {
	m := maskFor(s.Width)
	constVal := uint64(0)
	var terms []*Expr
	counts := map[string]int{}
	byKey := map[string]*Expr{}
	var order []string
	for _, op := range ops {
		op = s.rewriteBitwiseStructural(op, depth+1)
		if op.Kind == KNum {
			constVal ^= op.Num
			continue
		}
		k := op.Key()
		if counts[k] == 0 {
			order = append(order, k)
			byKey[k] = op
		}
		counts[k]++
	}
	for _, k := range order {
		if counts[k]%2 == 1 {
			terms = append(terms, byKey[k])
		}
	}
	var res *Expr
	for _, t := range terms {
		if res == nil {
			res = t
		} else {
			res = Binary("^", res, t)
		}
	}
	constVal &= m
	if constVal == m {
		if res == nil {
			return Num(m)
		}
		return Unary("~", res)
	}
	if constVal != 0 {
		if res == nil {
			return Num(constVal)
		}
		res = Binary("^", res, Num(constVal))
	}
	if res == nil {
		return Num(0)
	}
	return s.factorCommon(res, depth+1)
}

// hasDirectOperand reports whether target is a direct operand of an
// associative op-chain. It must not recurse into operands, otherwise
// x & (x | (y & x)) would be incorrectly reduced to y.
func hasDirectOperand(e *Expr, op string, target *Expr) bool {
	if e == nil || target == nil || e.Kind != KBinary || e.Op != op {
		return false
	}
	for _, part := range flattenSameOp(e, op) {
		if part.Key() == target.Key() {
			return true
		}
	}
	return false
}

// factorCommon implements (f&a)|(f&b) -> f&(a|b), plus the dual and XOR
// variants. It is deliberately one-level and conservative.
func (s *Simplifier) factorCommon(e *Expr, depth int) *Expr {
	if e == nil || e.Kind != KBinary {
		return e
	}
	outer := e.Op
	if outer != "|" && outer != "&" && outer != "^" {
		return e
	}
	terms := flattenSameOp(e, outer)
	if len(terms) < 2 {
		return e
	}
	inner := "&"
	if outer == "&" {
		inner = "|"
	}
	for _, t := range terms {
		if t.Kind != KBinary || t.Op != inner {
			return e
		}
	}
	common := map[string]*Expr{}
	for _, t := range terms {
		seen := map[string]*Expr{}
		for _, f := range flattenSameOp(t, inner) {
			seen[f.Key()] = f
		}
		if len(common) == 0 {
			for k, v := range seen {
				common[k] = v
			}
		} else {
			for k := range common {
				if _, ok := seen[k]; !ok {
					delete(common, k)
				}
			}
		}
		if len(common) == 0 {
			return e
		}
	}
	var commonKeys []string
	for k := range common {
		commonKeys = append(commonKeys, k)
	}
	sort.Strings(commonKeys)
	factor := common[commonKeys[0]]
	var remainders []*Expr
	for _, t := range terms {
		parts := flattenSameOp(t, inner)
		var rem []*Expr
		removed := false
		for _, p := range parts {
			if p.Key() == factor.Key() && !removed {
				removed = true
				continue
			}
			rem = append(rem, p)
		}
		var r *Expr
		for _, p := range rem {
			if r == nil {
				r = p
			} else {
				r = Binary(inner, r, p)
			}
		}
		if r == nil {
			// This can only happen for a one-factor term; keep the original.
			return e
		}
		remainders = append(remainders, r)
	}
	var combined *Expr
	for _, r := range remainders {
		if combined == nil {
			combined = r
		} else {
			combined = Binary(outer, combined, r)
		}
	}
	candidate := Binary(inner, factor, combined)
	// Let the caller's fixed-point loop clean this up, but avoid infinite
	// factoring by only returning it when it is no larger.
	if candidate.Cost() <= e.Cost() {
		return candidate
	}
	return e
}

func flattenSameOp(e *Expr, op string) []*Expr {
	if e.Kind == KBinary && e.Op == op {
		return append(flattenSameOp(e.Kids[0], op), flattenSameOp(e.Kids[1], op)...)
	}
	return []*Expr{e}
}

type addTerm struct {
	coeff uint64
	base  *Expr
}

func (s *Simplifier) rewriteArithmetic(e *Expr, depth int) *Expr {
	left := s.rewrite(e.Kids[0], depth+1)
	right := s.rewrite(e.Kids[1], depth+1)

	switch e.Op {
	case "*":
		if left.Kind == KNum && right.Kind == KNum {
			return Num((left.Num * right.Num) & maskFor(s.Width))
		}
		if left.Kind == KNum {
			return s.multiplyConst(left.Num, right, depth+1)
		}
		if right.Kind == KNum {
			return s.multiplyConst(right.Num, left, depth+1)
		}
		// Combine (c*x)*(d*y) into (c*d)*(x*y) when possible.
		if left.Kind == KBinary && left.Op == "*" && left.Kids[0].Kind == KNum &&
			right.Kind == KBinary && right.Op == "*" && right.Kids[0].Kind == KNum {
			c := (left.Kids[0].Num * right.Kids[0].Num) & maskFor(s.Width)
			return s.multiplyConst(c, Binary("*", left.Kids[1], right.Kids[1]), depth+1)
		}
		return Binary("*", left, right)
	case "+", "-":
		return s.combineAdditive(Binary(e.Op, left, right))
	}
	return Binary(e.Op, left, right)
}

func (s *Simplifier) multiplyConst(c uint64, e *Expr, depth int) *Expr {
	m := maskFor(s.Width)
	c &= m
	if c == 0 {
		return Num(0)
	}
	if c == 1 {
		return e
	}
	if e.Kind == KNum {
		return Num((c * e.Num) & m)
	}
	if c == m {
		if e.Kind == KUnary && e.Op == "-" {
			return e.Kids[0]
		}
		return Unary("-", e)
	}
	if e.Kind == KBinary && e.Op == "*" && e.Kids[0].Kind == KNum {
		return s.multiplyConst((c*e.Kids[0].Num)&m, e.Kids[1], depth+1)
	}
	if e.Kind == KUnary && e.Op == "-" {
		return Unary("-", s.multiplyConst(c, e.Kids[0], depth+1))
	}
	return Binary("*", Num(c), e)
}

func (s *Simplifier) combineAdditive(e *Expr) *Expr {
	m := maskFor(s.Width)
	var terms []addTerm
	constant := uint64(0)
	hasConstant := false
	var collect func(*Expr, uint64)
	collect = func(n *Expr, sign uint64) {
		switch {
		case n.Kind == KBinary && n.Op == "+":
			collect(n.Kids[0], sign)
			collect(n.Kids[1], sign)
		case n.Kind == KBinary && n.Op == "-":
			collect(n.Kids[0], sign)
			collect(n.Kids[1], (^sign+1)&m)
		case n.Kind == KUnary && n.Op == "-":
			collect(n.Kids[0], (^sign+1)&m)
		case n.Kind == KBinary && n.Op == "*":
			if n.Kids[0].Kind == KNum {
				collect(n.Kids[1], (sign*n.Kids[0].Num)&m)
			} else if n.Kids[1].Kind == KNum {
				collect(n.Kids[0], (sign*n.Kids[1].Num)&m)
			} else {
				terms = append(terms, addTerm{coeff: sign, base: n})
			}
		case n.Kind == KNum:
			constant = (constant + sign*n.Num) & m
			hasConstant = true
		default:
			terms = append(terms, addTerm{coeff: sign, base: n})
		}
	}
	collect(e, 1)

	coeffs := map[string]uint64{}
	bases := map[string]*Expr{}
	var order []string
	for _, t := range terms {
		k := t.base.Key()
		if _, ok := bases[k]; !ok {
			bases[k] = t.base
			order = append(order, k)
		}
		coeffs[k] = (coeffs[k] + t.coeff) & m
	}

	var positive []*Expr
	var negative []*Expr
	for _, k := range order {
		c := coeffs[k]
		if c == 0 {
			continue
		}
		if c&(1<<(s.Width-1)) != 0 && s.Width > 0 {
			abs := (^c + 1) & m
			negative = append(negative, s.multiplyConst(abs, bases[k], 0))
		} else {
			positive = append(positive, s.multiplyConst(c, bases[k], 0))
		}
	}
	var res *Expr
	for _, t := range positive {
		if res == nil {
			res = t
		} else {
			res = Binary("+", res, t)
		}
	}
	for _, t := range negative {
		if res == nil {
			res = Unary("-", t)
		} else {
			res = Binary("-", res, t)
		}
	}
	if hasConstant && constant != 0 {
		if constant&(1<<(s.Width-1)) != 0 && s.Width > 0 && constant != m {
			neg := (^constant + 1) & m
			if res == nil {
				res = Num(neg)
				res = Unary("-", res)
			} else {
				res = Binary("-", res, Num(neg))
			}
		} else if res == nil {
			res = Num(constant)
		} else {
			res = Binary("+", res, Num(constant))
		}
	}
	if res == nil {
		return Num(0)
	}
	return res
}

// formatCost is used when comparing candidates.
func formatCost(e *Expr) int {
	if e == nil {
		return 1 << 30
	}
	return e.Cost()
}

func indentLines(s, indent string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func debugExpr(e *Expr) string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s [cost=%d]", e.Format(32), e.Cost())
}

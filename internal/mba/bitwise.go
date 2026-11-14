package mba

import (
	"fmt"
	"math/rand"
	"sort"
)

// anfCollector splits a bitwise expression into variables (atoms) and constants.
type anfCollector struct {
	s     *Simplifier
	atoms []*Expr
	keys  []string
	seen  map[string]int
}

func (c *anfCollector) add(e *Expr) {
	k := e.Key()
	if _, ok := c.seen[k]; ok {
		return
	}
	c.seen[k] = len(c.atoms)
	c.atoms = append(c.atoms, e)
	c.keys = append(c.keys, k)
}

func (c *anfCollector) walk(e *Expr, depth int) {
	if depth > 400 {
		return
	}
	switch e.Kind {
	case KNum:
		return
	case KVar:
		c.add(e)
	case KUnary:
		if e.Op == "~" {
			c.walk(e.Kids[0], depth+1)
			return
		}
		c.addSimplified(e)
	case KBinary:
		if e.Op == "&" || e.Op == "|" || e.Op == "^" {
			c.walk(e.Kids[0], depth+1)
			c.walk(e.Kids[1], depth+1)
			return
		}
		c.addSimplified(e)
	default:
		c.addSimplified(e)
	}
}

func (c *anfCollector) addSimplified(e *Expr) {
	sim := c.s.Simplify(e)
	if sim == nil || sim.Kind == KNum {
		return
	}
	c.add(sim)
}

// anfSimplify computes the exact bit-level algebraic normal form of a bitwise
// expression. Arbitrary constants are preserved bit-for-bit, unlike a 0/1 truth
// table over whole words.
func (s *Simplifier) anfSimplify(e *Expr, depth int) *Expr {
	col := &anfCollector{s: s, seen: map[string]int{}}
	col.walk(e, depth+1)
	t := len(col.atoms)
	if t > s.MaxBitwiseAtoms {
		return nil
	}
	if t == 0 {
		v, ok := Eval(e, s.Width, nil)
		if !ok {
			return nil
		}
		return Num(v)
	}
	n := 1 << t
	abstraction := abstractAtoms(e, col.atoms)
	if abstraction == nil {
		return nil
	}
	vals := make([]uint64, n)
	for mask := 0; mask < n; mask++ {
		env := make(map[string]uint64, t)
		for i := range col.atoms {
			k := Var(fmt.Sprintf("__a%d", i)).Key()
			if mask&(1<<i) != 0 {
				env[k] = allOnes(s.Width)
			} else {
				env[k] = 0
			}
		}
		v, ok := Eval(abstraction, s.Width, env)
		if !ok {
			return nil
		}
		vals[mask] = v
	}

	// Möbius transform over GF(2): mask[S] = XOR_{T subset S} vals[T].
	masks := append([]uint64(nil), vals...)
	for bit := 0; bit < t; bit++ {
		for mask := 0; mask < n; mask++ {
			if mask&(1<<bit) != 0 {
				masks[mask] ^= masks[mask^(1<<bit)]
			}
		}
	}

	var candidates []*Expr
	consider := func(cand *Expr) {
		if cand == nil {
			return
		}
		cand = s.rewriteBitwiseStructural(cand, depth+1)
		for _, old := range candidates {
			if old.Key() == cand.Key() {
				return
			}
		}
		if len(candidates) < 512 {
			candidates = append(candidates, cand)
		}
	}

	// Simple XOR of masked monomials.
	consider(s.buildANFMonomials(masks, col.atoms))

	// Per-bit truth-table grouping with optimal small Boolean formulas.
	consider(s.bitSliceFromVals(vals, col.atoms))

	// Try variable orders for both a XOR factor tree and an OR mux tree.
	// Exhaustive permutations are only cheap for up to four variables.
	order := make([]int, t)
	for i := range order {
		order[i] = i
	}
	tryOrder := func(vars []int) {
		local := reorderMasks(masks, vars)
		copyOrder := append([]int(nil), vars...)
		consider(s.buildXorFactor(local, copyOrder, col.atoms, depth+1))
		consider(s.buildORMux(local, copyOrder, col.atoms, depth+1))
	}
	if t <= 4 {
		var perm func(int)
		perm = func(k int) {
			if k == t {
				tryOrder(order)
				return
			}
			for i := k; i < t; i++ {
				order[k], order[i] = order[i], order[k]
				perm(k + 1)
				order[k], order[i] = order[i], order[k]
			}
		}
		perm(0)
	} else {
		tryOrder(order)
		rev := append([]int(nil), order...)
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		tryOrder(rev)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Cost() < candidates[j].Cost()
	})
	for _, cand := range candidates {
		if s.verifyBitwise(e, cand, col.atoms) {
			return cand
		}
	}
	return nil
}

func reorderMasks(masks []uint64, order []int) []uint64 {
	t := len(order)
	n := 1 << t
	out := make([]uint64, n)
	for local := 0; local < n; local++ {
		orig := 0
		for j := 0; j < t; j++ {
			if local&(1<<j) != 0 {
				orig |= 1 << order[j]
			}
		}
		out[local] = masks[orig]
	}
	return out
}

func (s *Simplifier) buildANFMonomials(masks []uint64, atoms []*Expr) *Expr {
	m := maskFor(s.Width)
	var terms []*Expr
	for subset, cm := range masks {
		cm &= m
		if cm == 0 {
			continue
		}
		var conj *Expr
		for i := range atoms {
			if subset&(1<<i) == 0 {
				continue
			}
			if conj == nil {
				conj = atoms[i].Clone()
			} else {
				conj = Binary("&", conj, atoms[i].Clone())
			}
		}
		if conj == nil {
			terms = append(terms, Num(cm))
			continue
		}
		if cm != m {
			conj = Binary("&", conj, Num(cm))
		}
		terms = append(terms, conj)
	}
	if len(terms) == 0 {
		return Num(0)
	}
	res := terms[0]
	for _, t := range terms[1:] {
		res = Binary("^", res, t)
	}
	return res
}

// buildXorFactor builds f = A ^ (x & B), recursively.
func (s *Simplifier) buildXorFactor(masks []uint64, vars []int, atoms []*Expr, depth int) *Expr {
	if len(vars) == 0 {
		return Num(masks[0] & maskFor(s.Width))
	}
	allZero := true
	for _, v := range masks {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return Num(0)
	}
	n := len(vars)
	half := 1 << (n - 1)
	a := make([]uint64, half)
	b := make([]uint64, half)
	for r := 0; r < half; r++ {
		a[r] = masks[r<<1]
		b[r] = masks[r<<1] ^ masks[(r<<1)|1]
	}
	x := atoms[vars[0]].Clone()
	ae := s.buildXorFactor(a, vars[1:], atoms, depth+1)
	be := s.buildXorFactor(b, vars[1:], atoms, depth+1)
	if be.IsZero() {
		return ae
	}
	if ae.IsZero() {
		if be.IsAllOnes(s.Width) {
			return x
		}
		return Binary("&", x, be)
	}
	if be.IsAllOnes(s.Width) {
		return Binary("^", ae, x)
	}
	return Binary("^", ae, Binary("&", x, be))
}

// buildORMux builds the Shannon mux (~x & f0) | (x & f1), recursively.
func (s *Simplifier) buildORMux(masks []uint64, vars []int, atoms []*Expr, depth int) *Expr {
	if len(vars) == 0 {
		return Num(masks[0] & maskFor(s.Width))
	}
	allZero := true
	for _, v := range masks {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return Num(0)
	}
	n := len(vars)
	half := 1 << (n - 1)
	a := make([]uint64, half)
	b := make([]uint64, half)
	for r := 0; r < half; r++ {
		a[r] = masks[r<<1]
		b[r] = masks[(r<<1)|1]
	}
	x := atoms[vars[0]].Clone()
	ae := s.buildORMux(a, vars[1:], atoms, depth+1)
	be := s.buildORMux(b, vars[1:], atoms, depth+1)
	if ae.Key() == be.Key() {
		return ae
	}
	if ae.IsZero() {
		if be.IsAllOnes(s.Width) {
			return x
		}
		return Binary("&", x, be)
	}
	if be.IsZero() {
		if ae.IsAllOnes(s.Width) {
			return Unary("~", x)
		}
		return Binary("&", Unary("~", x), ae)
	}
	if ae.IsAllOnes(s.Width) {
		return Binary("|", Unary("~", x), be)
	}
	if be.IsAllOnes(s.Width) {
		return Binary("|", x, ae)
	}
	return Binary("|", Binary("&", Unary("~", x), ae), Binary("&", x, be))
}

func (s *Simplifier) verifyBitwise(orig, cand *Expr, atoms []*Expr) bool {
	if len(atoms) == 0 {
		return true
	}
	oa := abstractAtoms(orig, atoms)
	ca := abstractAtoms(cand, atoms)
	if oa == nil || ca == nil {
		return false
	}
	rng := rand.New(rand.NewSource(0x4d4241))
	trials := 96
	for trial := 0; trial < trials; trial++ {
		env := make(map[string]uint64, len(atoms))
		for i := range atoms {
			var v uint64
			switch trial {
			case 0:
				v = 0
			case 1:
				v = allOnes(s.Width)
			default:
				v = rng.Uint64() & maskFor(s.Width)
			}
			env[Var(fmt.Sprintf("__a%d", i)).Key()] = v
		}
		x, ok1 := Eval(oa, s.Width, env)
		y, ok2 := Eval(ca, s.Width, env)
		if !ok1 || !ok2 || x != y {
			return false
		}
	}
	return true
}

// abstractAtoms replaces each atom by a fresh variable so that opaque
// subexpressions (e.g. an addition used as a bitwise operand) are not evaluated
// internally.
func abstractAtoms(e *Expr, atoms []*Expr) *Expr {
	repl := make(map[string]*Expr, len(atoms))
	for i, a := range atoms {
		repl[a.Key()] = Var(fmt.Sprintf("__a%d", i))
	}
	return replaceAtoms(e, repl)
}

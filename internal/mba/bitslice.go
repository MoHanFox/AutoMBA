package mba

import (
	"fmt"
	"sort"
)

// boolTable returns optimal (by node count) bitwise formulas for every Boolean
// function of t variables, for t <= 3. Formulas use template variables named
// "__b0", "__b1", ...
func (s *Simplifier) boolTable(t int) []*Expr {
	if t < 0 || t > 3 {
		return nil
	}
	if s.boolTables == nil {
		s.boolTables = map[int][]*Expr{}
	}
	if tab, ok := s.boolTables[t]; ok {
		return tab
	}
	n := 1 << t
	size := 1 << n
	cost := make([]int, size)
	expr := make([]*Expr, size)
	for i := range cost {
		cost[i] = 1 << 30
	}
	set := func(key int, c int, e *Expr) {
		if c < cost[key] {
			cost[key] = c
			expr[key] = e
		}
	}
	all := maskFor(s.Width)
	set(0, 1, Num(0))
	set(size-1, 1, Num(all))
	for v := 0; v < t; v++ {
		key := 0
		for a := 0; a < n; a++ {
			if a&(1<<v) != 0 {
				key |= 1 << a
			}
		}
		set(key, 1, Var(fmt.Sprintf("__b%d", v)))
	}

	changed := true
	for iter := 0; changed && iter < 32; iter++ {
		changed = false
		for a := 0; a < size; a++ {
			if expr[a] == nil {
				continue
			}
			// Negation.
			na := a ^ (size - 1)
			if cost[a]+1 < cost[na] {
				set(na, cost[a]+1, Unary("~", expr[a]))
				changed = true
			}
			for b := a; b < size; b++ {
				if expr[b] == nil {
					continue
				}
				c := cost[a] + cost[b] + 1
				if c >= cost[0] {
					// Still may improve specific keys; only skip if no key can
					// improve is hard, so keep it simple.
				}
				k := a & b
				if c < cost[k] {
					set(k, c, Binary("&", expr[a], expr[b]))
					changed = true
				}
				k = a | b
				if c < cost[k] {
					set(k, c, Binary("|", expr[a], expr[b]))
					changed = true
				}
				k = a ^ b
				if c < cost[k] {
					set(k, c, Binary("^", expr[a], expr[b]))
					changed = true
				}
			}
		}
	}
	s.boolTables[t] = expr
	return expr
}

// instantiateBool replaces template variables with actual atom expressions.
func instantiateBool(template *Expr, atoms []*Expr) *Expr {
	if template == nil {
		return nil
	}
	if template.Kind == KVar {
		if len(template.Name) == 4 && template.Name[:3] == "__b" {
			idx := int(template.Name[3] - '0')
			if idx >= 0 && idx < len(atoms) {
				return atoms[idx].Clone()
			}
		}
		return template.Clone()
	}
	n := template.Clone()
	for i := range n.Kids {
		n.Kids[i] = instantiateBool(n.Kids[i], atoms)
	}
	return n
}

// bitSliceFromVals groups bit positions by their Boolean truth function and
// synthesises one small formula per group. This is exact for bitwise
// expressions with arbitrary constants and often recovers OR/AND forms that a
// single ANF cannot express compactly.
func (s *Simplifier) bitSliceFromVals(vals []uint64, atoms []*Expr) *Expr {
	t := len(atoms)
	if t == 0 || t > s.MaxBitwiseAtoms || t > 3 {
		return nil
	}
	n := 1 << t
	groups := map[uint64]uint64{}
	for bit := uint(0); bit < s.Width; bit++ {
		var key uint64
		for a := 0; a < n; a++ {
			if (vals[a]>>bit)&1 != 0 {
				key |= 1 << a
			}
		}
		groups[key] |= 1 << bit
	}
	table := s.boolTable(t)
	if table == nil {
		return nil
	}
	all := maskFor(s.Width)
	keys := make([]uint64, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var terms []*Expr
	for _, key := range keys {
		bm := groups[key]
		if bm == 0 {
			continue
		}
		if int(key) >= len(table) || table[key] == nil {
			return nil
		}
		f := instantiateBool(table[key], atoms)
		if bm != all {
			f = Binary("&", f, Num(bm))
		}
		terms = append(terms, f)
	}
	if len(terms) == 0 {
		return Num(0)
	}
	res := terms[0]
	for _, t := range terms[1:] {
		res = Binary("|", res, t)
	}
	return res
}

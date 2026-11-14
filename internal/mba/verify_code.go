package mba

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
)

// Verification is the result of a sampled differential check between the
// original and simplified code.
type Verification struct {
	Status  string `json:"status"` // passed, failed, skipped
	Cases   int    `json:"cases"`
	Message string `json:"message,omitempty"`
}

type vStmt struct {
	root  string
	mem   bool
	op    string
	expr  *Expr
	isVar bool
}

type vProgram struct {
	stmts []vStmt
	roots []string
	vars  []string
}

func parseVProgram(code string, width uint) (vProgram, bool) {
	var p vProgram
	rootSet := map[string]bool{}
	varSet := map[string]bool{}
	for _, chunk := range splitStatements(code) {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(chunk), ";"))
		if body == "" {
			continue
		}
		at := findAssignment(body)
		if at == nil {
			// Braces / labels are harmless; anything else (a call statement,
			// return, control flow) cannot be modelled by this verifier.
			if body == "{" || body == "}" || strings.HasSuffix(body, "{") || strings.HasSuffix(body, "}") {
				continue
			}
			return p, false
		}
		prefix := strings.TrimSpace(body[:at.start])
		rhs := strings.TrimSpace(body[at.end:])
		e, err := ParseExpression(rhs)
		if err != nil {
			return p, false
		}
		stmt := vStmt{op: at.op, expr: e}
		if name, ok := simpleLHSVar(prefix); ok {
			stmt.root = name
			stmt.isVar = true
			if !varSet[name] {
				varSet[name] = true
				p.vars = append(p.vars, name)
			}
		} else if strings.HasPrefix(prefix, "*") {
			root := rootIdent(prefix)
			if root == "" {
				return p, false
			}
			stmt.root = root
			stmt.mem = true
			if !rootSet[root] {
				rootSet[root] = true
				p.roots = append(p.roots, root)
			}
		} else {
			return p, false
		}
		p.stmts = append(p.stmts, stmt)
	}
	return p, len(p.stmts) > 0
}

type vExec struct {
	width  uint
	mem    map[string]uint64
	vars   map[string]uint64
	trial  uint64
	opaque int
}

func newVExec(width uint, roots []string, trial uint64) *vExec {
	x := &vExec{
		width: width,
		mem:   map[string]uint64{},
		vars:  map[string]uint64{},
		trial: trial,
	}
	for _, r := range roots {
		x.mem[r] = x.hashValue("init:" + r)
	}
	return x
}

func (x *vExec) hashValue(key string) uint64 {
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], x.trial)
	h.Write(b[:])
	h.Write([]byte(key))
	return h.Sum64() & maskFor(x.width)
}

func (x *vExec) opaqueValue(key string) uint64 {
	x.opaque++
	return x.hashValue(key)
}

// loadKey canonicalises common decompiler load shapes such as
// *(_DWORD *)&array[4 * v] so that a load is keyed by its array and evaluated
// index rather than by the spelling of the intermediate variable.
func (x *vExec) loadKey(e *Expr) string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case KCast:
		return x.loadKey(e.Kids[0])
	case KUnary:
		if e.Op == "&" {
			return x.addressKey(e.Kids[0])
		}
	case KIndex:
		return x.addressKey(e)
	}
	return ""
}

func (x *vExec) addressKey(e *Expr) string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case KIndex:
		idx, ok := x.eval(e.Kids[1])
		if !ok {
			return ""
		}
		return fmt.Sprintf("%s[%x]", e.Kids[0].Format(x.width), idx)
	case KVar:
		return e.Name
	case KMember:
		return e.Format(x.width)
	case KCast:
		return x.addressKey(e.Kids[0])
	}
	return ""
}

func (x *vExec) eval(e *Expr) (uint64, bool) {
	if e == nil {
		return 0, false
	}
	m := maskFor(x.width)
	switch e.Kind {
	case KNum:
		return e.Num & m, true
	case KVar:
		return x.vars[e.Name], true
	case KUnary:
		switch e.Op {
		case "*":
			if len(e.Kids) == 1 && e.Kids[0].Kind == KVar {
				if v, ok := x.mem[e.Kids[0].Name]; ok {
					x.opaque++
					return v, true
				}
			}
			if key := x.loadKey(e.Kids[0]); key != "" {
				return x.opaqueValue("load:" + key), true
			}
			return x.opaqueValue("load:" + e.Format(x.width)), true
		case "&":
			return x.opaqueValue("addr:" + e.Format(x.width)), true
		case "~", "-", "+", "!":
			v, ok := x.eval(e.Kids[0])
			if !ok {
				return 0, false
			}
			switch e.Op {
			case "~":
				return (^v) & m, true
			case "-":
				return (-v) & m, true
			case "+":
				return v, true
			default:
				if v == 0 {
					return 1, true
				}
				return 0, true
			}
		}
		return 0, false
	case KBinary:
		a, ok1 := x.eval(e.Kids[0])
		b, ok2 := x.eval(e.Kids[1])
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
			if b >= uint64(x.width) {
				return 0, true
			}
			return (a << b) & m, true
		case ">>":
			if b >= uint64(x.width) {
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
		c, ok := x.eval(e.Kids[0])
		if !ok {
			return 0, false
		}
		if c != 0 {
			return x.eval(e.Kids[1])
		}
		return x.eval(e.Kids[2])
	case KCast:
		return x.eval(e.Kids[0])
	case KCall:
		var vals []string
		for _, a := range e.Kids[1:] {
			v, ok := x.eval(a)
			if !ok {
				return 0, false
			}
			vals = append(vals, fmt.Sprintf("%x", v))
		}
		return x.opaqueValue("call:" + e.Kids[0].Format(x.width) + ":" + strings.Join(vals, ",")), true
	case KIndex:
		idx, ok := x.eval(e.Kids[1])
		if !ok {
			return 0, false
		}
		return x.opaqueValue(fmt.Sprintf("index:%s:%x", e.Kids[0].Format(x.width), idx)), true
	case KMember:
		return x.opaqueValue("member:" + e.Format(x.width)), true
	case KOpaque:
		return x.opaqueValue("opaque:" + e.Name), true
	}
	return 0, false
}

func applyOp(op string, old, rhs uint64, width uint) (uint64, bool) {
	m := maskFor(width)
	switch op {
	case "=":
		return rhs & m, true
	case "+=":
		return (old + rhs) & m, true
	case "-=":
		return (old - rhs) & m, true
	case "*=":
		return (old * rhs) & m, true
	case "/=":
		if rhs == 0 {
			return 0, false
		}
		return (old / rhs) & m, true
	case "%=":
		if rhs == 0 {
			return 0, false
		}
		return (old % rhs) & m, true
	case "&=":
		return old & rhs, true
	case "|=":
		return old | rhs, true
	case "^=":
		return old ^ rhs, true
	case "<<=":
		if rhs >= uint64(width) {
			return 0, true
		}
		return (old << rhs) & m, true
	case ">>=":
		if rhs >= uint64(width) {
			return 0, true
		}
		return (old >> rhs) & m, true
	}
	return 0, false
}

func (x *vExec) run(p vProgram) bool {
	for _, s := range p.stmts {
		v, ok := x.eval(s.expr)
		if !ok {
			return false
		}
		if s.isVar {
			old := x.vars[s.root]
			nv, ok := applyOp(s.op, old, v, x.width)
			if !ok {
				return false
			}
			x.vars[s.root] = nv
		} else {
			old := x.mem[s.root]
			nv, ok := applyOp(s.op, old, v, x.width)
			if !ok {
				return false
			}
			x.mem[s.root] = nv
		}
	}
	return true
}

// VerifyCode performs a deterministic sampled differential check. Calls and
// memory reads are modeled as deterministic opaque values so the two programs
// see the same external inputs. It is a strong sanity check, not a formal proof.
func VerifyCode(original, simplified string, width uint) Verification {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		width = 32
	}
	if strings.TrimSpace(original) == strings.TrimSpace(simplified) {
		return Verification{Status: "passed", Cases: 0, Message: "Input and output are identical"}
	}
	po, ok := parseVProgram(original, width)
	if !ok {
		return Verification{Status: "skipped", Message: "Verification supports straight-line assignments only"}
	}
	ps, ok := parseVProgram(simplified, width)
	if !ok {
		return Verification{Status: "skipped", Message: "Verification supports straight-line assignments only"}
	}

	const trials = 256
	if len(po.roots) == 0 {
		// Compare scalar variables only if both programs define all of them.
		have := map[string]bool{}
		for _, s := range ps.stmts {
			if s.isVar {
				have[s.root] = true
			}
		}
		for _, v := range po.vars {
			if !have[v] {
				return Verification{Status: "skipped", Message: "No common memory references to verify"}
			}
		}
	}

	for trial := 0; trial < trials; trial++ {
		xo := newVExec(width, po.roots, uint64(trial))
		xs := newVExec(width, ps.roots, uint64(trial))
		if !xo.run(po) || !xs.run(ps) {
			return Verification{Status: "skipped", Cases: trial, Message: "Could not evaluate one of the programs"}
		}
		for _, root := range po.roots {
			if xo.mem[root] != xs.mem[root] {
				return Verification{
					Status: "failed",
					Cases:  trial + 1,
					Message: fmt.Sprintf("memory *%s differs in sample %d: original %#x, simplified %#x",
						root, trial+1, xo.mem[root], xs.mem[root]),
				}
			}
		}
		if len(po.roots) == 0 {
			for _, v := range po.vars {
				if xo.vars[v] != xs.vars[v] {
					return Verification{
						Status: "failed",
						Cases:  trial + 1,
						Message: fmt.Sprintf("%s differs in sample %d: original %#x, simplified %#x",
							v, trial+1, xo.vars[v], xs.vars[v]),
					}
				}
			}
		}
	}
	return Verification{Status: "passed", Cases: trials, Message: fmt.Sprintf("passed %d sampled differential checks", trials)}
}

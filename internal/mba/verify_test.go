package mba

import (
	"math/rand"
	"strings"
	"testing"
)

type simpleAssignment struct {
	name string
	expr *Expr
}

func trackedVar(name string) bool {
	switch name {
	case "v6", "v7", "v8", "v9", "v10", "v11", "v12", "v13", "v14":
		return true
	}
	return false
}

func extractAssignments(code string) ([]simpleAssignment, *Expr, error) {
	var out []simpleAssignment
	var final *Expr
	for _, chunk := range splitStatements(code) {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(chunk), ";"))
		if body == "" {
			continue
		}
		at := findAssignment(body)
		if at == nil {
			continue
		}
		prefix := strings.TrimSpace(body[:at.start])
		rhs := strings.TrimSpace(body[at.end:])
		e, err := ParseExpression(rhs)
		if err != nil {
			continue
		}
		if at.op == "=" {
			if name, ok := simpleLHSVar(prefix); ok && trackedVar(name) {
				out = append(out, simpleAssignment{name: name, expr: e})
			}
		} else if at.op == "+=" && strings.Contains(prefix, "*a2") {
			final = e
		}
	}
	return out, final, nil
}

func evalAssignments(t *testing.T, code string, seed int64, trials int) (map[string][]uint64, []uint64) {
	t.Helper()
	as, final, err := extractAssignments(code)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	tracked := []string{"v6", "v7", "v8", "v9", "v10", "v11", "v12", "v13", "v14"}
	results := map[string][]uint64{}
	for _, n := range tracked {
		results[n] = nil
	}
	var finalVals []uint64
	for trial := 0; trial < trials; trial++ {
		env := map[string]uint64{
			Var("v5").Key():  rng.Uint64() & 0xFFFFFFFF,
			Var("v15").Key(): rng.Uint64() & 0xFFFFFFFF,
		}
		for _, a := range as {
			v, ok := Eval(a.expr, 32, env)
			if !ok {
				t.Fatalf("cannot eval %s in trial %d", a.name, trial)
			}
			env[Var(a.name).Key()] = v
			if _, want := results[a.name]; want {
				results[a.name] = append(results[a.name], v)
			}
		}
		if final != nil {
			v, ok := Eval(final, 32, env)
			if !ok {
				t.Fatal("cannot eval final += expression")
			}
			finalVals = append(finalVals, v)
		}
	}
	return results, finalVals
}

func TestSimplificationEquivalence(t *testing.T) {
	outputs := map[string]string{}
	outputs["normal"] = Process(sampleCode, 32, false).Output
	outputs["deep"] = ProcessDeep(sampleCode, 32).Output
	origVals, origFinal := evalAssignments(t, sampleCode, 1234, 200)
	for mode, out := range outputs {
		outVals, outFinal := evalAssignments(t, out, 1234, 200)
		for name, want := range origVals {
			got := outVals[name]
			if len(got) != len(want) {
				t.Errorf("%s: %s length mismatch %d != %d", mode, name, len(got), len(want))
				continue
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s trial %d: %s = %#x, want %#x", mode, i, name, got[i], want[i])
					break
				}
			}
		}
		if len(outFinal) != len(origFinal) {
			t.Errorf("%s: final length mismatch", mode)
		} else {
			for i := range origFinal {
				if outFinal[i] != origFinal[i] {
					t.Errorf("%s trial %d: final = %#x, want %#x", mode, i, outFinal[i], origFinal[i])
					break
				}
			}
		}
	}
}

func TestExpressionEquivalenceRandom(t *testing.T) {
	exprs := []string{
		"(x & 0x0F) | (x & 0xF0)",
		"(x & 0x80275917 | ~(x & 0x862F5D3F) & 0x708A428) ^ 0xF8F75BD7",
		"~(~(x & 0x63E364E2 ^ ~x & 0x9C1C9B1D ^ 0xE5CC39DD) | ~(x & 0x63E364E2 ^ ~x & 0x9C1C9B1D ^ 0x63E364E2) & 0x862F5D3F)",
		"(~x & y) | (x & ~y)",
		"x + x",
		"x - x + y",
	}
	s := New(32)
	rng := rand.New(rand.NewSource(99))
	for _, src := range exprs {
		e, err := ParseExpression(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		out := s.Simplify(e)
		for i := 0; i < 500; i++ {
			env := map[string]uint64{
				Var("x").Key(): rng.Uint64() & 0xFFFFFFFF,
				Var("y").Key(): rng.Uint64() & 0xFFFFFFFF,
			}
			a, ok1 := Eval(e, 32, env)
			b, ok2 := Eval(out, 32, env)
			if !ok1 || !ok2 || a != b {
				t.Fatalf("%s -> %s mismatch: %#x vs %#x", src, out.Format(32), a, b)
			}
		}
		t.Logf("%s => %s", src, out.Format(32))
	}
}

func randomBitwise(rng *rand.Rand, depth int) *Expr {
	if depth <= 0 || rng.Intn(4) == 0 {
		switch rng.Intn(4) {
		case 0:
			return Num(rng.Uint64() & 0xFFFFFFFF)
		case 1:
			return Var("x")
		case 2:
			return Var("y")
		default:
			return Var("z")
		}
	}
	switch rng.Intn(4) {
	case 0:
		return Unary("~", randomBitwise(rng, depth-1))
	case 1:
		return Binary("&", randomBitwise(rng, depth-1), randomBitwise(rng, depth-1))
	case 2:
		return Binary("|", randomBitwise(rng, depth-1), randomBitwise(rng, depth-1))
	default:
		return Binary("^", randomBitwise(rng, depth-1), randomBitwise(rng, depth-1))
	}
}

func TestRandomBitwiseEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(2026))
	s := New(32)
	for i := 0; i < 500; i++ {
		e := randomBitwise(rng, 4)
		out := s.Simplify(e)
		formatted := out.Format(32)
		reparsed, err := ParseExpression(formatted)
		if err != nil {
			t.Fatalf("case %d: formatted output does not parse: %s: %v", i, formatted, err)
		}
		for trial := 0; trial < 32; trial++ {
			env := map[string]uint64{
				Var("x").Key(): rng.Uint64() & 0xFFFFFFFF,
				Var("y").Key(): rng.Uint64() & 0xFFFFFFFF,
				Var("z").Key(): rng.Uint64() & 0xFFFFFFFF,
			}
			a, ok1 := Eval(e, 32, env)
			b, ok2 := Eval(reparsed, 32, env)
			if !ok1 || !ok2 || a != b {
				t.Fatalf("case %d: %s => %s mismatch: %#x vs %#x", i, e.Format(32), formatted, a, b)
			}
		}
	}
}

func TestBoolTableComplete(t *testing.T) {
	s := New(32)
	for vars := 0; vars <= 3; vars++ {
		tab := s.boolTable(vars)
		if tab == nil {
			t.Fatalf("nil table for %d vars", vars)
		}
		for i, e := range tab {
			if e == nil {
				t.Fatalf("missing formula for %d vars, key %d", vars, i)
			}
		}
	}
}

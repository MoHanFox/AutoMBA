package mba

import (
	"strings"
	"testing"
)

func TestSignedSemantics(t *testing.T) {
	e, err := ParseExpression("0xFFFFFFFF >> 1")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := Eval(e, 32, nil)
	s, _ := EvalSigned(e, 32, true, nil)
	if u != 0x7FFFFFFF {
		t.Errorf("unsigned >> = %#x", u)
	}
	if s != 0xFFFFFFFF {
		t.Errorf("signed >> = %#x", s)
	}

	div, _ := ParseExpression("0xFFFFFFF8 / 2") // -8 / 2
	ds, _ := EvalSigned(div, 32, true, nil)
	if ds != 0xFFFFFFFC {
		t.Errorf("signed division = %#x", ds)
	}
	cmp, _ := ParseExpression("0xFFFFFFFF < 1")
	cu, _ := Eval(cmp, 32, nil)
	cs, _ := EvalSigned(cmp, 32, true, nil)
	if cu != 0 || cs != 1 {
		t.Errorf("comparison unsigned=%d signed=%d", cu, cs)
	}
}

func TestDeadCodeElimination(t *testing.T) {
	src := "x = a + b;\ny = a + b;\n*a1 += y;"
	out := RemoveDeadCode(src, 32)
	if strings.Contains(out, "x =") {
		t.Errorf("dead x not removed:\n%s", out)
	}
	if !strings.Contains(out, "*a1 += y;") {
		t.Errorf("live memory update removed:\n%s", out)
	}
}

func TestCSE(t *testing.T) {
	src := "v1 = (a + b) * (a + b);\nv2 = (a + b) * (a + b);\n*a1 += v1 + v2;"
	out := EliminateCSE(src, 32, true)
	if !strings.Contains(out, "__cse1") {
		t.Errorf("expected CSE temporary:\n%s", out)
	}
	if strings.Count(out, "a + b") != 1 {
		t.Errorf("expected one materialized a + b:\n%s", out)
	}
}

func TestImpureCallAnnotation(t *testing.T) {
	src := "v1 = sub_140006120(*a1);\nv2 = v1 + 1;\n*a2 += v2;"
	opt := DefaultOptions()
	opt.ImpureCalls = []string{"sub_140006120"}
	out := ProcessRefsWithOptions(src, 32, "a1;a2", false, opt).Output
	if !strings.Contains(out, "v1 = sub_140006120") {
		t.Errorf("non-pure call was inlined:\n%s", out)
	}
	if strings.Contains(out, "v2 =") {
		t.Errorf("pure v2 should be merged:\n%s", out)
	}
}

func TestDefinitionsAndSteps(t *testing.T) {
	out := Process(sampleCode, 32, false).Output
	defs, steps := AnalyzeDefinitions(sampleCode, out, 32)
	if len(defs) == 0 || len(steps) == 0 {
		t.Fatalf("expected definitions and steps, got %d/%d", len(defs), len(steps))
	}
	found := false
	for _, d := range defs {
		if d.Name == "v18" && strings.Contains(d.Source, "sub_140006120") {
			found = true
		}
	}
	if !found {
		t.Errorf("missing v18 definition")
	}
}

func TestExtendedTernary(t *testing.T) {
	src := "x = c ? a + b : a - b;\n*a1 += x;"
	out := Process(src, 32, false).Output
	if !strings.Contains(out, "?") {
		t.Errorf("ternary lost:\n%s", out)
	}
}

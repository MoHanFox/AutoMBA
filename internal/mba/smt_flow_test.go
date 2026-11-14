package mba

import "testing"

func requireZ3(t *testing.T) {
	t.Helper()
	if _, err := findZ3(); err == nil {
		return
	}
	if _, _, err := findPythonZ3(); err == nil {
		return
	}
	t.Skip("z3 backend not installed")
}

func TestZ3ControlFlow(t *testing.T) {
	requireZ3(t)
	code := "if (c) { *a1 += 1; } else { *a1 += 2; }"
	v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
	if v.Status != "passed" {
		t.Fatalf("control flow proof failed: %s: %s", v.Status, v.Message)
	}
	bad := "*a1 += 3;"
	v = VerifyZ3WithOptions(code, bad, 32, DefaultOptions())
	if v.Status != "failed" {
		t.Fatalf("expected counterexample, got %s: %s", v.Status, v.Message)
	}
}

func TestZ3MemberAccess(t *testing.T) {
	requireZ3(t)
	code := "s.x += 1;\n*a1 += s.x;"
	v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
	if v.Status != "passed" {
		t.Fatalf("member proof failed: %s: %s", v.Status, v.Message)
	}
	v = VerifyZ3WithOptions(code, "*a1 += 2;", 32, DefaultOptions())
	if v.Status != "failed" {
		t.Fatalf("expected member counterexample, got %s: %s", v.Status, v.Message)
	}
}

func TestZ3BoundedLoops(t *testing.T) {
	requireZ3(t)
	cases := []string{
		"while (c) { *a1 += 1; }",
		"for (i = 0; i < 4; i++) { *a1 += i; }",
		"do { *a1 += 1; } while (c);",
	}
	for _, code := range cases {
		v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
		if v.Status != "bounded" {
			t.Errorf("%q: expected bounded, got %s: %s", code, v.Status, v.Message)
		}
	}
	v := VerifyZ3WithOptions("while (c) { *a1 += 1; }", "while (c) { *a1 += 2; }", 32, DefaultOptions())
	if v.Status != "failed" {
		t.Fatalf("expected loop counterexample, got %s: %s", v.Status, v.Message)
	}
}

func TestZ3Switch(t *testing.T) {
	requireZ3(t)
	code := `switch (x) {
  case 1: *a1 += 1; break;
  case 2: *a1 += 2; break;
  default: *a1 += 3; break;
}`
	v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
	if v.Status != "passed" {
		t.Fatalf("switch proof failed: %s: %s", v.Status, v.Message)
	}
	v = VerifyZ3WithOptions(code, "*a1 += 9;", 32, DefaultOptions())
	if v.Status != "failed" {
		t.Fatalf("expected switch counterexample, got %s: %s", v.Status, v.Message)
	}
}

func TestZ3UnsupportedFlowStaysSkipped(t *testing.T) {
	requireZ3(t)
	cases := []string{
		"while (c) { if (d) break; }",
		"goto done;\n*a1 += 1;\ndone: *a1 += 2;",
		"return 0;",
	}
	for _, code := range cases {
		v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
		if v.Status != "skipped" {
			t.Errorf("%q: expected skipped, got %s: %s", code, v.Status, v.Message)
		}
	}
}

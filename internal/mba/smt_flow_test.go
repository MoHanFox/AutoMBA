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

func TestZ3LoopsStaySkipped(t *testing.T) {
	requireZ3(t)
	code := "while (c) { *a1 += 1; }"
	v := VerifyZ3WithOptions(code, code, 32, DefaultOptions())
	if v.Status != "skipped" {
		t.Fatalf("expected skipped loop, got %s: %s", v.Status, v.Message)
	}
}

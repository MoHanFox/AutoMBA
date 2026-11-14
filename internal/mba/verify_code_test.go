package mba

import "testing"

func TestVerifyCodeSample(t *testing.T) {
	normal := Process(sampleCode, 32, false).Output
	deep := ProcessDeep(sampleCode, 32).Output
	refs := ProcessRefs(sampleCode, 32, "a1;a2;a3", false).Output
	preserved := ProcessRefs(sampleCode, 32, "a1;a2;a3", true).Output

	for name, out := range map[string]string{
		"normal":    normal,
		"deep":      deep,
		"refs":      refs,
		"preserved": preserved,
	} {
		v := VerifyCode(sampleCode, out, 32)
		t.Logf("%s: %s (%d) %s", name, v.Status, v.Cases, v.Message)
		if v.Status != "passed" {
			t.Errorf("%s verification failed: %s", name, v.Message)
		}
	}
}

func TestVerifyCodeDetectsMismatch(t *testing.T) {
	original := "*a1 += *a2 ^ *a3;"
	bad := "*a1 += *a2 | *a3;"
	v := VerifyCode(original, bad, 32)
	if v.Status != "failed" {
		t.Fatalf("expected failed, got %s: %s", v.Status, v.Message)
	}
}

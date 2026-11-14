package mba

import (
	"testing"
)

func simp(t *testing.T, src string, width uint) string {
	t.Helper()
	e, err := ParseExpression(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	out := New(width).Simplify(e).Format(width)
	env := map[string]uint64{}
	_ = env
	return out
}

func TestBasicSimplifications(t *testing.T) {
	cases := []string{
		"x + x",
		"x - x",
		"x & 0x0F",
		"(x & 0x0F) | (x & 0xF0)",
		"~(~x & ~y)",
		"(~x & y) | (x & ~y)",
		"x ^ x",
		"~(x ^ x)",
		"(x & 0x80275917 | ~(x & 0x862F5D3F) & 0x708A428) ^ 0xF8F75BD7",
		"~(x | y) & ~(x | y)",
		"(x | y) & (x | y)",
		"x & (x | y)",
		"x | (x & y)",
		"(x & 0x30) | (x & 0xC0)",
	}
	for _, src := range cases {
		t.Logf("%-80s => %s", src, simp(t, src, 32))
	}
}

func TestParseDecompilerShapes(t *testing.T) {
	cases := []string{
		"sub_140006120((unsigned int)*a2)",
		"*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17",
		"(unsigned int)i",
		"v18\n & ((((*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17) ^ 0xEDACA0BC) & 0x707E4CB1 | (*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17) & 0x8F81B34E ^ 0x2011342) ^ v18 ^ 0x622D13F2) ^ (*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17) & ~v18",
	}
	for _, src := range cases {
		if _, err := ParseExpression(src); err != nil {
			t.Errorf("parse %q: %v", src, err)
		}
	}
}

package mba

import (
	"strings"
	"testing"
)

const sampleCode = `
    v18 = sub_140006120((unsigned int)*a2);
    v17 = *a3;
    v4 = sub_140006140(*a3, (unsigned int)i);
    *a1 += v18
         & ((((*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17) ^ 0xEDACA0BC) & 0x707E4CB1
           | (*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17) & 0x8F81B34E ^ 0x2011342)
          ^ v18
          ^ 0x622D13F2)
         ^ (*(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17)
         & ~v18;
    *a3 += 2135587861;
    v15 = sub_140006120((unsigned int)*a1);
    v16 = *a3;
    v5 = v16 + *(_DWORD *)&aYcsvllo3xM9zAa[4 * (unsigned int)sub_140006160(*a3, i)];
    v6 = ~(~(v5 & 0x63E364E2 ^ ~v5 & 0x9C1C9B1D ^ 0xE5CC39DD)
         | ~(v5 & 0x63E364E2 ^ ~v5 & 0x9C1C9B1D ^ 0x63E364E2) & 0x862F5D3F);
    v7 = (v15 & 0x80275917 | ~(v15 & 0x862F5D3F) & 0x708A428) ^ 0xF8F75BD7;
    v8 = ~((v15 ^ 0x79D0A2C0) & ~v15 | 0x4B820ECE) & 0xEAD124A6;
    v9 = ~(v7 ^ (v15 ^ 0x79D0A2C0) & ~v15) | v7 & ~((v15 ^ 0x79D0A2C0) & ~v15);
    v10 = ~(~(v5 & 0x862F5D3F | v6) | v5 & 0x862F5D3F ^ 0xC6AB228 ^ v6 ^ 0xC6AB228) | v5 & 0x862F5D3F ^ v6;
    v11 = ~(v10 | v10 ^ 0xB3673F0F);
    v12 = v10 & 0xB3673F0F;
    v13 = ~((~(v9
             | ~((v15 & 0x2000C0C | ~(v15 & 0x2020C0E) & 0x898781E3)
               ^ (v7 & 0x34787010 | ~(v7 & 0xB47DF131) & 0x898781E3)
               ^ (~(~((v15 ^ 0x79D0A2C0) & ~v15 & 0x4B820ECE) | (v15 ^ 0x79D0A2C0) & ~v15 | 0x4B820ECE)
                | ((v15 ^ 0x79D0A2C0) & ~v15 & 0x4A800486 | ~((v15 ^ 0x79D0A2C0) & ~v15 & 0x4B820ECE) & 0x152EDB59)
                ^ (v8 | (v15 ^ 0x79D0A2C0) & ~v15 & 0x142CD111 | 0x1020A48))))
           | v9
           ^ 0x516953A5
           ^ (v15 & 0x2000C0C | ~(v15 & 0x2020C0E) & 0x898781E3)
           ^ (v7 & 0x34787010 | ~(v7 & 0xB47DF131) & 0x898781E3)
           ^ (~(~((v15 ^ 0x79D0A2C0) & ~v15 & 0x4B820ECE) | (v15 ^ 0x79D0A2C0) & ~v15 | 0x4B820ECE)
            | ((v15 ^ 0x79D0A2C0) & ~v15 & 0x4A800486 | ~((v15 ^ 0x79D0A2C0) & ~v15 & 0x4B820ECE) & 0x152EDB59)
            ^ (v8 | (v15 ^ 0x79D0A2C0) & ~v15 & 0x142CD111 | 0x1020A48))
           ^ 0xAE96AC5A)
          ^ 0xB3673F0F);
    v14 = ~(~v12 | ~v11) | v12 ^ 0xBC734574 ^ v11 ^ 0xBC734574;
    *a2 += ~v14 & v13 | ~v13 & v14;
`

func TestProcessSampleDeep(t *testing.T) {
	res := ProcessDeep(sampleCode, 32)
	if res.Error != "" {
		t.Fatalf("error: %s", res.Error)
	}
	t.Logf("passes=%d statements=%d simplified=%d", res.Passes, res.Statements, res.Simplified)
	t.Logf("output:\n%s", res.Output)
}

func TestProcessSample(t *testing.T) {
	for _, iterate := range []bool{false, true} {
		res := Process(sampleCode, 32, iterate)
		if res.Error != "" {
			t.Fatalf("error: %s", res.Error)
		}
		t.Logf("iterate=%v passes=%d statements=%d simplified=%d", iterate, res.Passes, res.Statements, res.Simplified)
		t.Logf("output:\n%s", res.Output)
		if len(res.Output) >= len(sampleCode) {
			t.Errorf("output was not shorter: %d >= %d", len(res.Output), len(sampleCode))
		}
		if !strings.Contains(res.Output, "v18") {
			t.Errorf("output lost v18")
		}
	}
}

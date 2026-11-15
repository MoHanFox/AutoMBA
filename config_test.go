package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigOmitsEmptyOptionalFields(t *testing.T) {
	data, err := json.Marshal(normalizeConfig(defaultAppConfig()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, key := range []string{"roots", "impureCalls", "z3Path"} {
		if strings.Contains(text, `"`+key+`"`) {
			t.Errorf("empty %s should be omitted: %s", key, text)
		}
	}
}

func TestConfigKeepsNonEmptyOptionalFields(t *testing.T) {
	cfg := defaultAppConfig()
	cfg.Roots = "a1;a2"
	cfg.ImpureCalls = "sub_1"
	cfg.Z3Path = `C:\Python313`
	data, err := json.Marshal(normalizeConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, fragment := range []string{`"roots":"a1;a2"`, `"impureCalls":"sub_1"`, `"z3Path":"C:\\Python313"`} {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing %s in %s", fragment, text)
		}
	}
}

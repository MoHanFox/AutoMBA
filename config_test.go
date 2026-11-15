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
	for _, key := range []string{"z3Path", "roots", "impureCalls"} {
		if strings.Contains(text, `"`+key+`"`) {
			t.Errorf("unexpected %s in config: %s", key, text)
		}
	}
}

func TestConfigKeepsNonEmptyZ3Path(t *testing.T) {
	cfg := defaultAppConfig()
	cfg.Z3Path = `C:\Python313`
	data, err := json.Marshal(normalizeConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"z3Path":"C:\\Python313"`) {
		t.Errorf("missing z3Path in %s", text)
	}
	if strings.Contains(text, `"roots"`) || strings.Contains(text, `"impureCalls"`) {
		t.Errorf("roots/impureCalls must never be persisted: %s", text)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintEnvMaskingAndExport(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret-1234567890",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"anth"}

	// masked: API_KEY redacted, MODEL shown.
	var masked bytes.Buffer
	if err := printEnv(&masked, env, chain, false, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if !strings.Contains(masked.String(), "len=24") {
		t.Errorf("masked output missing redaction:\n%s", masked.String())
	}
	if !strings.Contains(masked.String(), "ANTHROPIC_MODEL=claude-sonnet-5") {
		t.Errorf("masked output missing plaintext model:\n%s", masked.String())
	}

	// export: `export ` prefix, full value.
	var exp bytes.Buffer
	if err := printEnv(&exp, env, chain, true, true); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(exp.String()), "export ") {
		t.Errorf("export output missing `export ` prefix:\n%s", exp.String())
	}
	if !strings.Contains(exp.String(), "sk-ant-secret-1234567890") {
		t.Errorf("export output missing unmasked value:\n%s", exp.String())
	}
}

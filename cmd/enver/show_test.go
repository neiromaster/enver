package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintEnvMasking(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret-1234567890",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"anth"}

	// masked: API_KEY redacted, MODEL shown.
	var masked bytes.Buffer
	if err := printEnv(&masked, env, chain, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if !strings.Contains(masked.String(), "len=24") {
		t.Errorf("masked output missing redaction:\n%s", masked.String())
	}
	if !strings.Contains(masked.String(), "ANTHROPIC_MODEL=claude-sonnet-5") {
		t.Errorf("masked output missing plaintext model:\n%s", masked.String())
	}
}

func TestPrintExportBash(t *testing.T) {
	env := map[string]string{
		"API_KEY": "sk-ant-secret-1234567890",
		"QUOTE":   "it's",
	}
	var exp bytes.Buffer
	if err := printExport(&exp, env, "bash"); err != nil {
		t.Fatalf("printExport: %v", err)
	}
	out := exp.String()
	if !strings.HasPrefix(strings.TrimSpace(out), "export ") {
		t.Errorf("bash export missing `export ` prefix:\n%s", out)
	}
	if !strings.Contains(out, "sk-ant-secret-1234567890") {
		t.Errorf("bash export missing unmasked value:\n%s", out)
	}
	if !strings.Contains(out, "export QUOTE='it'\\''s'") {
		t.Errorf("bash export missing shell-quoted value:\n%s", out)
	}
}

func TestPrintExportPowerShell(t *testing.T) {
	env := map[string]string{
		"API_KEY": "sk-ant-secret-1234567890",
		"QUOTE":   "it's",
	}
	var exp bytes.Buffer
	if err := printExport(&exp, env, "powershell"); err != nil {
		t.Fatalf("printExport: %v", err)
	}
	out := exp.String()
	if !strings.Contains(out, "$env:API_KEY = 'sk-ant-secret-1234567890'") {
		t.Errorf("powershell export missing $env: assignment:\n%s", out)
	}
	if !strings.Contains(out, "$env:QUOTE = 'it''s'") {
		t.Errorf("powershell export missing doubled-quote escaping:\n%s", out)
	}
}

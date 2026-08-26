package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestPrintEnvMasking(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret-1234567890",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"anth"}

	// masked: API_KEY redacted, MODEL shown.
	var masked bytes.Buffer
	if err := printEnv(&masked, env, chain, nil, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if !strings.Contains(masked.String(), "len=24") {
		t.Errorf("masked output missing redaction:\n%s", masked.String())
	}
	if !strings.Contains(masked.String(), "ANTHROPIC_MODEL=claude-sonnet-5") {
		t.Errorf("masked output missing plaintext model:\n%s", masked.String())
	}
}

// TestPrintEnvJSON pins the JSON contract: machine-readable output is always
// unmasked. Text masking is for human eyes; a consumer piping JSON to a tool
// needs the real values, and --no-mask stays a text-output concern.
func TestPrintEnvJSON(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret-1234567890",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"anth"}

	var out bytes.Buffer
	if err := printEnvJSON(&out, "anth", chain, env, nil); err != nil {
		t.Fatalf("printEnvJSON: %v", err)
	}
	var got showJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if got.Profile != "anth" {
		t.Errorf("profile = %q, want anth", got.Profile)
	}
	if !reflect.DeepEqual(got.Chain, chain) {
		t.Errorf("chain = %v, want %v", got.Chain, chain)
	}
	if got.Env["ANTHROPIC_MODEL"] != "claude-sonnet-5" {
		t.Errorf("plain value = %q, want claude-sonnet-5", got.Env["ANTHROPIC_MODEL"])
	}
	if got.Env["API_KEY"] != "sk-ant-secret-1234567890" {
		t.Error("JSON must always carry the full secret value")
	}
	if strings.Contains(out.String(), "len=") {
		t.Errorf("JSON must not contain masking hints:\n%s", out.String())
	}
}

// TestPrintEnvProvenance pins the per-key source annotation: text output marks
// the defining profile and layer, and JSON carries a structured sources map.
func TestPrintEnvProvenance(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"dev", "anth"}
	sources := map[string]config.Source{
		"API_KEY":         {Profile: "dev", Layer: "local"},
		"ANTHROPIC_MODEL": {Profile: "anth", Layer: "global"},
	}

	var out bytes.Buffer
	if err := printEnv(&out, env, chain, sources, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "ANTHROPIC_MODEL=claude-sonnet-5  # from anth (global)") {
		t.Errorf("text output missing global-source annotation:\n%s", s)
	}
	if !strings.Contains(s, "# from dev (local)") {
		t.Errorf("text output missing local-source annotation:\n%s", s)
	}
	if !strings.Contains(s, "# profile: dev → anth") {
		t.Errorf("chain header lost:\n%s", s)
	}

	var jout bytes.Buffer
	if err := printEnvJSON(&jout, "dev", chain, env, sources); err != nil {
		t.Fatalf("printEnvJSON: %v", err)
	}
	var got showJSON
	if err := json.Unmarshal(jout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jout.String())
	}
	if got.Sources["API_KEY"] != sources["API_KEY"] {
		t.Errorf("JSON source API_KEY = %+v, want %+v", got.Sources["API_KEY"], sources["API_KEY"])
	}
	if got.Sources["ANTHROPIC_MODEL"].Layer != "global" {
		t.Errorf("JSON source ANTHROPIC_MODEL = %+v, want global", got.Sources["ANTHROPIC_MODEL"])
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

func TestPrintExportFish(t *testing.T) {
	env := map[string]string{
		"API_KEY": "sk-ant-secret-1234567890",
		"QUOTE":   "it's",
		"WINPATH": `C:\Users\gavro`,
	}
	var exp bytes.Buffer
	if err := printExport(&exp, env, "fish"); err != nil {
		t.Fatalf("printExport: %v", err)
	}
	out := exp.String()
	if !strings.Contains(out, "set -gx API_KEY 'sk-ant-secret-1234567890'") {
		t.Errorf("fish export missing set -gx assignment:\n%s", out)
	}
	if !strings.Contains(out, "set -gx QUOTE 'it\\'s'") {
		t.Errorf("fish export missing backslash-quote escaping:\n%s", out)
	}
	if !strings.Contains(out, `set -gx WINPATH 'C:\\Users\\gavro'`) {
		t.Errorf("fish export must not mangle backslashes:\n%s", out)
	}
}

// TestPrintExportOmitsUnsetKeys pins the pass-through contract: unset keys are
// simply absent from env, so eval of an export leaves any shell-exported value
// untouched — no strip lines are emitted.
func TestPrintExportOmitsUnsetKeys(t *testing.T) {
	env := map[string]string{"API_KEY": "sk-ant-secret"}
	var out bytes.Buffer
	if err := printExport(&out, env, "bash"); err != nil {
		t.Fatalf("printExport: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "unset ") {
		t.Errorf("bash export must not emit strip lines:\n%s", s)
	}
	if !strings.Contains(s, "export API_KEY='sk-ant-secret'") {
		t.Errorf("bash export lost the assignment:\n%s", s)
	}
}

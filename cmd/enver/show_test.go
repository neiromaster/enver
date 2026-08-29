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
	if err := printEnv(&masked, env, chain, nil, nil, false, false); err != nil {
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
	if err := printEnvJSON(&out, "anth", chain, env, nil, nil); err != nil {
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
	if err := printEnv(&out, env, chain, sources, nil, false, false); err != nil {
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
	if err := printEnvJSON(&jout, "dev", chain, env, sources, nil); err != nil {
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

// TestPrintEnvUnsets pins the tombstone rows: text output interleaves a
// comment line naming the unsetting profile, and JSON carries a structured
// unsets map mirroring sources.
func TestPrintEnvUnsets(t *testing.T) {
	env := map[string]string{"API_KEY": "sk-ant-secret", "PORT": "8080"}
	chain := []string{"prod", "base"}
	sources := map[string]config.Source{"PORT": {Profile: "base", Layer: "global"}}
	unsets := map[string]config.Source{
		"TRACE": {Profile: "prod", Layer: "local"},
	}

	var out bytes.Buffer
	if err := printEnv(&out, env, chain, sources, unsets, false, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "# TRACE — unset by \"prod\"") {
		t.Errorf("text output missing TRACE tombstone row:\n%s", s)
	}
	if !strings.Contains(s, "API_KEY=sk-a") {
		t.Errorf("text output lost the masked live key:\n%s", s)
	}
	if !strings.Contains(s, "PORT=8080") {
		t.Errorf("text output lost the live key:\n%s", s)
	}

	var jout bytes.Buffer
	if err := printEnvJSON(&jout, "prod", chain, env, sources, unsets); err != nil {
		t.Fatalf("printEnvJSON: %v", err)
	}
	var got showJSON
	if err := json.Unmarshal(jout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jout.String())
	}
	if got.Unsets["API_KEY"] != unsets["API_KEY"] {
		t.Errorf("JSON unsets API_KEY = %+v, want %+v", got.Unsets["API_KEY"], unsets["API_KEY"])
	}
	if got.Unsets["TRACE"].Profile != "prod" {
		t.Errorf("JSON unsets TRACE = %+v, want prod", got.Unsets["TRACE"])
	}
}

// TestPrintEnvJSONOmitsEmptyUnsets pins the additive contract: a profile with
// no unsets produces no "unsets" key at all, so existing JSON consumers see
// byte-identical output.
func TestPrintEnvJSONOmitsEmptyUnsets(t *testing.T) {
	var out bytes.Buffer
	if err := printEnvJSON(&out, "p", []string{"p"}, map[string]string{"A": "1"}, nil, nil); err != nil {
		t.Fatalf("printEnvJSON: %v", err)
	}
	if strings.Contains(out.String(), "unsets") {
		t.Errorf("empty unsets must not appear in JSON:\n%s", out.String())
	}
}

// TestPrintEnvLiveBeatsTombstone pins the overlap contract: a key both live
// and unset (a Resolved invariant violation) renders exactly one row — the
// live one — instead of hiding the value behind a tombstone.
func TestPrintEnvLiveBeatsTombstone(t *testing.T) {
	env := map[string]string{"SECRET": "real-value"}
	unsets := map[string]config.Source{"SECRET": {Profile: "prod", Layer: "global"}}
	var out bytes.Buffer
	if err := printEnv(&out, env, []string{"p"}, nil, unsets, true, false); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	if n := strings.Count(s, "SECRET"); n != 1 {
		t.Errorf("SECRET rendered %d times, want 1:\n%s", n, s)
	}
	if !strings.Contains(s, "SECRET=real-value") {
		t.Errorf("live value hidden by tombstone:\n%s", s)
	}
	if strings.Contains(s, "unset by") {
		t.Errorf("tombstone survived a live key:\n%s", s)
	}
}

// TestPrintEnvColor pins the styling contract of the colored text output:
// keys are bold, every comment (header, provenance suffix, tombstone rows)
// is dim, and values stay plain so they read and copy cleanly.
func TestPrintEnvColor(t *testing.T) {
	const (
		ansiBold  = "\x1b[1m"
		ansiDim   = "\x1b[2m"
		ansiReset = "\x1b[m"
	)
	env := map[string]string{"API_KEY": "sk-ant-secret"}
	chain := []string{"dev", "anth"}
	sources := map[string]config.Source{"API_KEY": {Profile: "dev", Layer: "local"}}
	unsets := map[string]config.Source{"LEGACY_URL": {Profile: "anth", Layer: "local"}}

	var out bytes.Buffer
	if err := printEnv(&out, env, chain, sources, unsets, true, true); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	for name, want := range map[string]string{
		"header":    ansiDim + "# profile: dev → anth" + ansiReset,
		"live row":  ansiBold + "API_KEY" + ansiReset + "=sk-ant-secret  " + ansiDim + "# from dev (local)" + ansiReset,
		"tombstone": ansiDim + `# LEGACY_URL — unset by "anth"` + ansiReset,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("colored output missing %s row:\nwant %q\ngot  %q", name, want, s)
		}
	}
}

// TestColorEnabled pins the color gate: styling is emitted only onto a
// terminal, and never under NO_COLOR (https://no-color.org, empty string
// counts as absent).
func TestColorEnabled(t *testing.T) {
	orig := stdoutIsTTY
	t.Cleanup(func() { stdoutIsTTY = orig })

	t.Setenv("NO_COLOR", "")
	stdoutIsTTY = func() bool { return true }
	if !colorEnabled() {
		t.Error("empty NO_COLOR on a TTY must keep color enabled")
	}

	t.Setenv("NO_COLOR", "1")
	if colorEnabled() {
		t.Error("NO_COLOR set but colorEnabled() = true")
	}

	stdoutIsTTY = func() bool { return false }
	if colorEnabled() {
		t.Error("stdout is not a TTY but colorEnabled() = true")
	}
}

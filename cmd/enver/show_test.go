package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
)

func TestPrintEnvMasking(t *testing.T) {
	env := map[string]string{
		"API_KEY":         "sk-ant-secret-1234567890",
		"ANTHROPIC_MODEL": "claude-sonnet-5",
	}
	chain := []string{"anth"}

	// masked: API_KEY redacted, MODEL shown.
	var masked bytes.Buffer
	if err := printEnv(&masked, env, chain, nil, nil, envRender{}); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if !strings.Contains(masked.String(), "len=24") {
		t.Errorf("masked output missing redaction:\n%s", masked.String())
	}
	if !strings.Contains(masked.String(), "ANTHROPIC_MODEL=claude-sonnet-5") {
		t.Errorf("masked output missing plaintext model:\n%s", masked.String())
	}
}

// TestPrintEnvMarkedAnnotation pins the secret-mark rendering: a marked value
// masks whatever its name looks like, and a marked row still holding plaintext
// carries a note saying so. Once the value is ciphertext at rest the note goes
// away — the promise is kept and there is nothing to flag.
func TestPrintEnvMarkedAnnotation(t *testing.T) {
	env := map[string]string{"PROJECT_PATH": "/home/anna"}
	chain := []string{"dev"}
	sources := map[string]config.Source{"PROJECT_PATH": {Profile: "dev", Layer: "local"}}
	marked := map[string]bool{"PROJECT_PATH": true}

	var out bytes.Buffer
	if err := printEnv(&out, env, chain, sources, nil, envRender{marked: marked}); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "/home/anna") {
		t.Errorf("marked value leaked:\n%s", s)
	}
	if !strings.Contains(s, "# from dev (local) ~ marked, not encrypted") {
		t.Errorf("plaintext secret missing its annotation:\n%s", s)
	}

	var enc bytes.Buffer
	if err := printEnv(&enc, env, chain, sources, nil, envRender{marked: marked, encrypted: map[string]bool{"PROJECT_PATH": true}}); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	es := enc.String()
	if strings.Contains(es, "/home/anna") {
		t.Errorf("marked encrypted value leaked:\n%s", es)
	}
	if strings.Contains(es, "marked, not encrypted") {
		t.Errorf("encrypted secret kept the plaintext annotation:\n%s", es)
	}

	// Without provenance the note rides the plain line, so the flag never
	// depends on the per-key source being known.
	var bare bytes.Buffer
	if err := printEnv(&bare, env, chain, nil, nil, envRender{marked: marked}); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	if bs := bare.String(); !strings.Contains(bs, "~ marked, not encrypted") {
		t.Errorf("sourceless plaintext secret missing its annotation:\n%s", bs)
	}
}

// TestShowWiresSecretMarks pins the mark's trip from the YAML key to the text
// renderer: show resolves the profile itself, so the annotation only appears if
// the resolved marks reach printEnv.
func TestShowWiresSecretMarks(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	writeDoc(t, globalPath, "profiles:\n  dev:\n    env:\n      ~PROJECT_PATH: /home/anna\n")
	out, err := runCommand(t, "--config", globalPath, "--chdir", dir, "show", "dev")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(out, "/home/anna") {
		t.Fatalf("show leaked the marked value: %q", out)
	}
	if !strings.Contains(out, "~ marked, not encrypted") {
		t.Fatalf("show lost the plaintext annotation: %q", out)
	}
}

// ciphertextV3 encrypts value under a throwaway key so a fixture can carry an
// enc:v3 value no test key can read.
func ciphertextV3(t *testing.T, value string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := crypto.DeriveKey("hunter2", salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	enc, err := crypto.EncryptValueWithParams(value, key, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return enc
}

// TestShowDegradesWithoutKey pins the display/execution split end to end: text
// show names the key it cannot read and still exits 0, while the JSON contract
// keeps failing loudly rather than handing machines ciphertext.
func TestShowDegradesWithoutKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ENVER_KEY", "")
	prevInteractive := app.Interactive
	app.Interactive = func() bool { return false }
	t.Cleanup(func() { app.Interactive = prevInteractive })
	savedFormat, savedNoMask := showFormat, showNoMask
	t.Cleanup(func() { showFormat, showNoMask = savedFormat, savedNoMask })

	cfgPath := filepath.Join(dir, "config.yaml")
	writeDoc(t, cfgPath, fmt.Sprintf("profiles:\n  work:\n    env:\n      TOKEN: %s\n      PLAIN: kept\n", ciphertextV3(t, "secret")))

	out, err := runCommand(t, "--config", cfgPath, "--chdir", dir, "show", "work")
	if err != nil {
		t.Fatalf("text show must degrade instead of failing: %v", err)
	}
	if !strings.Contains(out, "TOKEN=(cannot decrypt: no key — run enver keygen)") {
		t.Fatalf("degraded note missing:\n%s", out)
	}
	if strings.Contains(out, "enc:v3:") {
		t.Fatalf("ciphertext reached the display:\n%s", out)
	}
	if !strings.Contains(out, "PLAIN=kept") {
		t.Fatalf("plaintext row lost:\n%s", out)
	}

	if _, err := runCommand(t, "--config", cfgPath, "--chdir", dir, "show", "--format", "json", "work"); err == nil {
		t.Fatal("JSON show must fail loudly when no key can decrypt")
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
	if err := printEnv(&out, env, chain, sources, nil, envRender{}); err != nil {
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
	if err := printEnv(&out, env, chain, sources, unsets, envRender{}); err != nil {
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
	if err := printEnv(&out, env, []string{"p"}, nil, unsets, envRender{unmasked: true}); err != nil {
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
// keys are bold, every comment (header, provenance suffix, tombstone rows, the
// marked-but-plaintext note) is dim, and values stay plain so they read and
// copy cleanly.
func TestPrintEnvColor(t *testing.T) {
	const (
		ansiBold  = "\x1b[1m"
		ansiDim   = "\x1b[2m"
		ansiReset = "\x1b[m"
	)
	env := map[string]string{"API_KEY": "sk-ant-secret", "PROJECT_PATH": "/home/anna"}
	chain := []string{"dev", "anth"}
	sources := map[string]config.Source{
		"API_KEY":      {Profile: "dev", Layer: "local"},
		"PROJECT_PATH": {Profile: "dev", Layer: "local"},
	}
	unsets := map[string]config.Source{"LEGACY_URL": {Profile: "anth", Layer: "local"}}

	var out bytes.Buffer
	if err := printEnv(&out, env, chain, sources, unsets, envRender{
		unmasked: true,
		color:    true,
		marked:   map[string]bool{"PROJECT_PATH": true},
	}); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	s := out.String()
	for name, want := range map[string]string{
		"header":      ansiDim + "# profile: dev → anth" + ansiReset,
		"live row":    ansiBold + "API_KEY" + ansiReset + "=sk-ant-secret  " + ansiDim + "# from dev (local)" + ansiReset,
		"tombstone":   ansiDim + `# LEGACY_URL — unset by "anth"` + ansiReset,
		"marked note": ansiBold + "PROJECT_PATH" + ansiReset + "=/home/anna  " + ansiDim + "# from dev (local) ~ marked, not encrypted" + ansiReset,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("colored output missing %s row:\nwant %q\ngot  %q", name, want, s)
		}
	}
}

// TestShowInjectedWriterStaysEscapeFree pins the gate side end to end: the
// color decision follows the writer the command actually writes to. With
// cobra's output redirected, the buffer stays escape-free even though the
// seam treats every *os.File as a terminal — the situation a pty run
// exposed when the gate probed the process stdout instead.
func TestShowInjectedWriterStaysEscapeFree(t *testing.T) {
	orig := writerIsTTY
	t.Cleanup(func() { writerIsTTY = orig })
	writerIsTTY = func(w io.Writer) bool {
		_, isFile := w.(*os.File)
		return isFile
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	writeDoc(t, globalPath, "profiles:\n  p:\n    env:\n      A: '1'\n")
	out, err := runCommand(t, "--config", globalPath, "--chdir", dir, "show", "p")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("redirected show output carries ANSI escapes: %q", out)
	}
	if !strings.Contains(out, "A=1") {
		t.Fatalf("show lost the value: %q", out)
	}
}

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

func TestImportMergeCreate(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	envBytes := []byte("# db\nDB_HOST=h\nDB_URL=$DB_HOST/x\nNEW=1\n")
	summary, err := runImport(bytes.NewReader(envBytes), cfgPath, "prod", false, false, "", nil, nil)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(summary, "imported 3") || !strings.Contains(summary, "created") {
		t.Errorf("summary: %q", summary)
	}
	prof, _, ok, err := config.ReadProfile(cfgPath, "prod")
	if err != nil || !ok {
		t.Fatalf("profile not written: %v", err)
	}
	if prof.Env["DB_URL"] != "$DB_HOST/x" || prof.Env["DB_HOST"] != "h" {
		t.Errorf("raw values not stored: %+v", prof.Env)
	}
}

func TestImportMergeKeepsExisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1", "SHARED": "old"}}, false, false)
	summary, err := runImport(bytes.NewReader([]byte("SHARED=new\nNEW=2\n")), cfgPath, "p", false, false, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if prof.Env["OLD"] != "1" {
		t.Errorf("merge should keep OLD: %+v", prof.Env)
	}
	if prof.Env["SHARED"] != "new" {
		t.Errorf("merge should override SHARED: %+v", prof.Env)
	}
	if !strings.Contains(summary, "merge") {
		t.Errorf("summary should say merge: %q", summary)
	}
}

func TestImportReplaceRemovesAbsent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false)
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Errorf("replace should remove OLD: %+v", prof.Env)
	}
	if prof.Env["NEW"] != "2" {
		t.Errorf("replace should add NEW: %+v", prof.Env)
	}
}

// TestImportReplaceClearsUnset pins the --replace contract: like the profile's
// own env, the unset list is wiped, so an imported key the old profile fenced
// survives the import instead of being silently stripped.
func TestImportReplaceClearsUnset(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"K": "old"}, Unset: config.Unsets{"A"}}, false, false)
	if _, err := runImport(bytes.NewReader([]byte("A=1\n")), cfgPath, "p", true, true, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "unset") {
		t.Errorf("replace should drop the unset field:\n%s", data)
	}
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	prof := cfg.Profiles["p"]
	if prof.Env["A"] != "1" {
		t.Errorf("replace should import A unfenced: %+v", prof.Env)
	}
	if len(prof.Unset) != 0 {
		t.Errorf("replace should clear unset, got %v", prof.Unset)
	}
}

func TestImportReplaceKeepsDefault(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, true, false)
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, _ := config.ReadProfile(cfgPath, "p")
	if !isDefault {
		t.Errorf("replace should keep default pointer; isDefault=%v", isDefault)
	}
	if prof.Env["NEW"] != "2" {
		t.Errorf("replace should add NEW: %+v", prof.Env)
	}
}

func TestCompleteImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, p := range []string{"prod", "dev", "stage"} {
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("global", false, "")
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("global", "true")

	// arg 0 is a file path: defer to the shell, no suggestions.
	got, dir := completeImport(cmd, nil, "")
	if dir != cobra.ShellCompDirectiveDefault || len(got) != 0 {
		t.Errorf("arg0: got %v (dir=%v), want Default with no suggestions", got, dir)
	}

	// arg 1 is a profile name: list profiles, no file completion.
	got, dir = completeImport(cmd, []string{"file.env"}, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("arg1: dir=%v, want NoFileComp", dir)
	}
	if len(got) != 3 {
		t.Errorf("arg1: got %v, want 3 profiles", got)
	}

	// prefix filtering: "d" matches only dev.
	got, _ = completeImport(cmd, []string{"file.env"}, "d")
	if len(got) != 1 || got[0] != "dev" {
		t.Errorf("arg1 prefix \"d\": got %v, want [dev]", got)
	}
}

func TestImportExtendsCreate(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"ROOT": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	summary, err := runImport(bytes.NewReader([]byte("OWN=2\n")), cfgPath, "child", false, false, "base", nil, nil)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(summary, "extends:") || !strings.Contains(summary, "→ base") {
		t.Errorf("create with --extends should report the extends change: %q", summary)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "child")
	if !prof.Extends.Has("base") {
		t.Errorf("child.Extends = %q, want base", prof.Extends)
	}
	if prof.Env["OWN"] != "2" {
		t.Errorf("child.Env = %+v, want OWN=2", prof.Env)
	}
}

// TestImportExtendsMissingParent runs at the command level: parents are
// validated against the merged view in RunE, not inside runImport.
func TestImportExtendsMissingParent(t *testing.T) {
	global := importFixtureLayers(t)
	globalFlags.configPath = global
	globalFlags.global = true
	globalFlags.noLocal = true

	err := runImportCmd(t, "A=1\n", "p", "--extends", "ghost")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing-parent error, got: %v", err)
	}
}

func TestImportExtendsMergePreserved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"Y": "2"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("Z=3\n")), cfgPath, "p", false, false, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if !prof.Extends.Has("base") {
		t.Errorf("merge without --extends should preserve base; got %q", prof.Extends)
	}
}

func TestImportExtendsReplacePreserved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OLD": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if !prof.Extends.Has("base") {
		t.Errorf("replace without --extends should preserve base; got %q", prof.Extends)
	}
	if _, ok := prof.Env["OLD"]; ok {
		t.Errorf("replace should drop OLD: %+v", prof.Env)
	}
	if prof.Env["NEW"] != "2" {
		t.Errorf("replace should add NEW: %+v", prof.Env)
	}
}

func TestImportEmptyErrors(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_, err := runImport(bytes.NewReader([]byte("")), cfgPath, "p", false, false, "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no variables to import") {
		t.Fatalf("expected no-variables error, got: %v", err)
	}
	if _, _, ok, _ := config.ReadProfile(cfgPath, "p"); ok {
		t.Error("empty import must not create a profile")
	}
}

func TestImportEmptyWithExtendsOK(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("")), cfgPath, "child", false, false, "base", nil, nil); err != nil {
		t.Fatalf("empty import with --extends should succeed: %v", err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "child")
	if !prof.Extends.Has("base") {
		t.Errorf("child.Extends = %q, want base", prof.Extends)
	}
}

func TestImportDiffMerge(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"A": "1", "B": "old"}}, false, false)
	summary, err := runImport(bytes.NewReader([]byte("B=new\nC=2\n")), cfgPath, "p", false, false, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "— merge") || !strings.Contains(summary, "~ B = new") || !strings.Contains(summary, "+ C = 2") {
		t.Errorf("merge diff missing lines:\n%s", summary)
	}
	if strings.Contains(summary, "+ A") {
		t.Errorf("unchanged/inherited A should not appear:\n%s", summary)
	}
}

// TestImportDiffCreateShowsValues pins the diff contract: the summary echoes the
// imported values verbatim. The data came straight from the user's own .env
// file, so masking would force an unmask dance just to verify what changed.
func TestImportDiffCreateShowsValues(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	summary, err := runImport(bytes.NewReader([]byte("API_TOKEN=sk-live-secret\nA=1\n")), cfgPath, "p", false, false, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "+ API_TOKEN = sk-live-secret") {
		t.Errorf("secret value must be shown in full:\n%s", summary)
	}
	if !strings.Contains(summary, "+ A = 1") {
		t.Errorf("non-secret value should show in full:\n%s", summary)
	}
}

func TestImportDiffReplaceRemoves(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"A": "1", "B": "2"}}, false, false)
	summary, err := runImport(bytes.NewReader([]byte("C=3\n")), cfgPath, "p", true, true, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- A = 1", "- B = 2", "+ C = 3", "— replaced"} {
		if !strings.Contains(summary, want) {
			t.Errorf("missing %q in:\n%s", want, summary)
		}
	}
}

func TestImportReplaceConfirmDeclined(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false)
	refuse := func(string, bool) (bool, error) { return false, nil }
	summary, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", refuse, nil)
	if err != nil {
		t.Fatalf("declined confirm should not error: %v", err)
	}
	if summary != "" {
		t.Errorf("declined confirm should print nothing, got: %q", summary)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; !ok {
		t.Error("declined confirm must not remove OLD")
	}
	if _, ok := prof.Env["NEW"]; ok {
		t.Error("declined confirm must not add NEW")
	}
}

func TestImportReplaceConfirmAccepted(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false)
	accept := func(msg string, _ bool) (bool, error) {
		if !strings.Contains(msg, "remove 1 key") || !strings.Contains(msg, "OLD") {
			t.Errorf("confirm prompt missing count/key: %q", msg)
		}
		return true, nil
	}
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", accept, nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Error("accepted confirm should remove OLD")
	}
	if prof.Env["NEW"] != "2" {
		t.Error("accepted confirm should add NEW")
	}
}

func TestImportReplaceConfirmNonInteractive(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false)
	broken := func(string, bool) (bool, error) { return false, io.EOF }
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", broken, nil)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force hint on failed confirm, got: %v", err)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; !ok {
		t.Error("failed confirm must not remove OLD")
	}
}

func TestImportReplaceForceSkipsConfirm(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false)
	called := false
	never := func(string, bool) (bool, error) { called = true; return false, nil }
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", never, nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("confirm must not be called under --force")
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Error("--force should still remove OLD")
	}
}

func TestImportExtendsList(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(cfgPath, "a", config.Profile{Env: map[string]string{"K": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "b", config.Profile{Env: map[string]string{"K": "2"}}, false, false); err != nil {
		t.Fatal(err)
	}
	envBytes := []byte("X=y\n")
	summary, err := runImport(bytes.NewReader(envBytes), cfgPath, "p", false, false, "a,b", nil, nil)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(summary, "extends:") || !strings.Contains(summary, "→ a, b") {
		t.Errorf("create with --extends a,b should report multi-parent extends: %q", summary)
	}
	prof, _, ok, err := config.ReadProfile(cfgPath, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile p: ok=%v err=%v", ok, err)
	}
	if !prof.Extends.Has("a") || !prof.Extends.Has("b") || len(prof.Extends) != 2 {
		t.Fatalf("p.Extends = %v, want [a b]", prof.Extends)
	}
}

func TestImportReplaceConfirmsUnsetClearing(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"FOO": "1"}, Unset: []string{"SECRET"}}, false, false)
	prompted := false
	accept := func(msg string, _ bool) (bool, error) {
		prompted = true
		if !strings.Contains(msg, "SECRET") {
			t.Errorf("confirm prompt must name the unset entries being cleared: %q", msg)
		}
		return true, nil
	}
	if _, err := runImport(bytes.NewReader([]byte("FOO=1\n")), cfgPath, "p", true, false, "", accept, nil); err != nil {
		t.Fatal(err)
	}
	if !prompted {
		t.Fatal("identical env keys must still confirm when the unset list would be cleared")
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if len(prof.Unset) != 0 {
		t.Fatalf("unset = %v, want cleared after an accepted confirm", prof.Unset)
	}
}

func TestImportReplaceDeclinedUnsetConfirmPreservesFence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"FOO": "1"}, Unset: []string{"SECRET"}}, false, false)
	refuse := func(string, bool) (bool, error) { return false, nil }
	summary, err := runImport(bytes.NewReader([]byte("FOO=1\n")), cfgPath, "p", true, false, "", refuse, nil)
	if err != nil {
		t.Fatalf("declined confirm should not error: %v", err)
	}
	if summary != "" {
		t.Errorf("declined confirm should print nothing, got: %q", summary)
	}
	prof, _, _, _ := config.ReadProfile(cfgPath, "p")
	if len(prof.Unset) != 1 {
		t.Fatal("declined confirm must keep the fence intact")
	}
}

// TestImportMergeReportsFencedKeys resolves against the real file the import
// just wrote, so the fence comes from the profile's own unset list — the same
// resolution show and export apply.
func TestImportMergeReportsFencedKeys(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Unset: []string{"A"}}, false, false)
	resolve := func(name string) (config.Resolved, error) {
		cfg, err := config.LoadFile(cfgPath)
		if err != nil {
			return config.Resolved{}, err
		}
		return cfg.ResolveProfile(name)
	}
	summary, err := runImport(bytes.NewReader([]byte("A=1\nB=2\n")), cfgPath, "p", false, false, "", nil, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "! A = 1") || !strings.Contains(summary, "fenced") {
		t.Errorf("a fenced key must be reported as dead on arrival, got:\n%s", summary)
	}
	if !strings.Contains(summary, "+ B = 2") {
		t.Errorf("an unfenced key still reports as added, got:\n%s", summary)
	}
}

// runImportCmd executes the import command against a fixture directory with
// the given flags, writing env into a local .env file. dir is the chdir root
// holding global.yaml; the caller has already wired globalFlags.
func runImportCmd(t *testing.T, envBody string, args ...string) error {
	t.Helper()
	envFile := filepath.Join(t.TempDir(), "vars.env")
	if err := os.WriteFile(envFile, []byte(envBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return importCmd.RunE(&cobra.Command{}, append([]string{envFile}, args...))
}

func importFixtureLayers(t *testing.T) string {
	t.Helper()
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })

	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "global.yaml")
}

// TestImportExtendsSelfCycleRefused pins the cycle guard: import must refuse
// an extends value that loops, like add and edit do, instead of writing a
// profile that fails resolution later.
func TestImportExtendsSelfCycleRefused(t *testing.T) {
	global := importFixtureLayers(t)
	if err := config.UpsertProfile(global, "a", config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	globalFlags.configPath = global
	globalFlags.global = true
	globalFlags.noLocal = true

	err := runImportCmd(t, "OWN=2\n", "a", "--extends", "a")
	if err == nil || !strings.Contains(err.Error(), "would create a cycle") {
		t.Fatalf("err=%v, want cycle refusal", err)
	}
	prof, _, _, _ := config.ReadProfile(global, "a")
	if prof.Env["OWN"] != "" {
		t.Fatalf("profile was written despite cycle refusal: %+v", prof)
	}
}

// TestImportExtendsParentFromOtherLayerAccepted pins the merged-view rule:
// a parent living only in the other layer is a valid extends target for
// import, exactly as it is for add and edit.
func TestImportExtendsParentFromOtherLayerAccepted(t *testing.T) {
	global := importFixtureLayers(t)
	if err := config.UpsertProfile(global, "gparent", config.Profile{Env: map[string]string{"ROOT": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	globalFlags.configPath = global
	globalFlags.global = false
	globalFlags.noLocal = false

	if err := runImportCmd(t, "OWN=2\n", "child", "--extends", "gparent"); err != nil {
		t.Fatalf("merged-view parent rejected: %v", err)
	}
	local := config.LocalPath()
	prof, _, _, _ := config.ReadProfile(local, "child")
	if !prof.Extends.Has("gparent") {
		t.Fatalf("child.Extends = %q, want gparent", prof.Extends)
	}
}

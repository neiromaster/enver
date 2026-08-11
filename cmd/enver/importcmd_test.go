package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

func TestImportMergeCreate(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	envBytes := []byte("# db\nDB_HOST=h\nDB_URL=$DB_HOST/x\nNEW=1\n")
	summary, err := runImport(bytes.NewReader(envBytes), cfgPath, "prod", false, false, "", nil)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(summary, "imported 3") || !strings.Contains(summary, "created") {
		t.Errorf("summary: %q", summary)
	}
	prof, _, _, ok, err := config.ReadProfile(cfgPath, "prod")
	if err != nil || !ok {
		t.Fatalf("profile not written: %v", err)
	}
	if prof.Env["DB_URL"] != "$DB_HOST/x" || prof.Env["DB_HOST"] != "h" {
		t.Errorf("raw values not stored: %+v", prof.Env)
	}
}

func TestImportMergeKeepsExisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1", "SHARED": "old"}}, false, false, nil)
	summary, err := runImport(bytes.NewReader([]byte("SHARED=new\nNEW=2\n")), cfgPath, "p", false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
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
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false, nil)
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Errorf("replace should remove OLD: %+v", prof.Env)
	}
	if prof.Env["NEW"] != "2" {
		t.Errorf("replace should add NEW: %+v", prof.Env)
	}
}

func TestImportReplaceKeepsDefault(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, true, false, nil)
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	prof, _, isDefault, _, _ := config.ReadProfile(cfgPath, "p")
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
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("no-local", "true")

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
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"ROOT": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	summary, err := runImport(bytes.NewReader([]byte("OWN=2\n")), cfgPath, "child", false, false, "base", nil)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(summary, "extends:") || !strings.Contains(summary, "→ base") {
		t.Errorf("create with --extends should report the extends change: %q", summary)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "child")
	if !prof.Extends.Has("base") {
		t.Errorf("child.Extends = %q, want base", prof.Extends)
	}
	if prof.Env["OWN"] != "2" {
		t.Errorf("child.Env = %+v, want OWN=2", prof.Env)
	}
}

func TestImportExtendsMissingParent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_, err := runImport(bytes.NewReader([]byte("A=1\n")), cfgPath, "p", false, false, "ghost", nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing-parent error, got: %v", err)
	}
}

func TestImportExtendsMergePreserved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"Y": "2"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("Z=3\n")), cfgPath, "p", false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if !prof.Extends.Has("base") {
		t.Errorf("merge without --extends should preserve base; got %q", prof.Extends)
	}
}

func TestImportExtendsReplacePreserved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OLD": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
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
	_, err := runImport(bytes.NewReader([]byte("")), cfgPath, "p", false, false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "no variables to import") {
		t.Fatalf("expected no-variables error, got: %v", err)
	}
	if _, _, _, ok, _ := config.ReadProfile(cfgPath, "p"); ok {
		t.Error("empty import must not create a profile")
	}
}

func TestImportEmptyWithExtendsOK(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("")), cfgPath, "child", false, false, "base", nil); err != nil {
		t.Fatalf("empty import with --extends should succeed: %v", err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "child")
	if !prof.Extends.Has("base") {
		t.Errorf("child.Extends = %q, want base", prof.Extends)
	}
}

func TestImportDiffMerge(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"A": "1", "B": "old"}}, false, false, nil)
	summary, err := runImport(bytes.NewReader([]byte("B=new\nC=2\n")), cfgPath, "p", false, false, "", nil)
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

func TestImportDiffCreateMasks(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	summary, err := runImport(bytes.NewReader([]byte("API_TOKEN=sk-live-secret\nA=1\n")), cfgPath, "p", false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, "sk-live-secret") {
		t.Errorf("secret value should be masked:\n%s", summary)
	}
	if !strings.Contains(summary, "+ A = 1") {
		t.Errorf("non-secret value should show in full:\n%s", summary)
	}
	if !strings.Contains(summary, "+ API_TOKEN = ") {
		t.Errorf("secret key line missing:\n%s", summary)
	}
}

func TestImportDiffReplaceRemoves(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"A": "1", "B": "2"}}, false, false, nil)
	summary, err := runImport(bytes.NewReader([]byte("C=3\n")), cfgPath, "p", true, true, "", nil)
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
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false, nil)
	refuse := func(string, bool) (bool, error) { return false, nil }
	summary, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", refuse)
	if err != nil {
		t.Fatalf("declined confirm should not error: %v", err)
	}
	if summary != "" {
		t.Errorf("declined confirm should print nothing, got: %q", summary)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; !ok {
		t.Error("declined confirm must not remove OLD")
	}
	if _, ok := prof.Env["NEW"]; ok {
		t.Error("declined confirm must not add NEW")
	}
}

func TestImportReplaceConfirmAccepted(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false, nil)
	accept := func(msg string, _ bool) (bool, error) {
		if !strings.Contains(msg, "remove 1 key") || !strings.Contains(msg, "OLD") {
			t.Errorf("confirm prompt missing count/key: %q", msg)
		}
		return true, nil
	}
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", accept); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Error("accepted confirm should remove OLD")
	}
	if prof.Env["NEW"] != "2" {
		t.Error("accepted confirm should add NEW")
	}
}

func TestImportReplaceConfirmNonInteractive(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false, nil)
	broken := func(string, bool) (bool, error) { return false, io.EOF }
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, false, "", broken)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force hint on failed confirm, got: %v", err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; !ok {
		t.Error("failed confirm must not remove OLD")
	}
}

func TestImportReplaceForceSkipsConfirm(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, false, nil)
	called := false
	never := func(string, bool) (bool, error) { called = true; return false, nil }
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", never); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("confirm must not be called under --force")
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if _, ok := prof.Env["OLD"]; ok {
		t.Error("--force should still remove OLD")
	}
}

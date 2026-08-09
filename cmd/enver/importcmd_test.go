package main

import (
	"bytes"
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
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1", "SHARED": "old"}}, false, nil)
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
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, false, nil)
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
	_ = config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"OLD": "1"}}, true, nil)
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
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, nil); err != nil {
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
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"ROOT": "1"}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("OWN=2\n")), cfgPath, "child", false, false, "base", nil); err != nil {
		t.Fatalf("runImport: %v", err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "child")
	if prof.Extends != "base" {
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
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: "base", Env: map[string]string{"Y": "2"}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("Z=3\n")), cfgPath, "p", false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if prof.Extends != "base" {
		t.Errorf("merge without --extends should preserve base; got %q", prof.Extends)
	}
}

func TestImportExtendsReplacePreserved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "base", config.Profile{Env: map[string]string{"X": "1"}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Extends: "base", Env: map[string]string{"OLD": "1"}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true, true, "", nil); err != nil {
		t.Fatal(err)
	}
	prof, _, _, _, _ := config.ReadProfile(cfgPath, "p")
	if prof.Extends != "base" {
		t.Errorf("replace without --extends should preserve base; got %q", prof.Extends)
	}
	if _, ok := prof.Env["OLD"]; ok {
		t.Errorf("replace should drop OLD: %+v", prof.Env)
	}
	if prof.Env["NEW"] != "2" {
		t.Errorf("replace should add NEW: %+v", prof.Env)
	}
}

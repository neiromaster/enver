package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestImportMergeCreate(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	envBytes := []byte("# db\nDB_HOST=h\nDB_URL=$DB_HOST/x\nNEW=1\n")
	summary, err := runImport(bytes.NewReader(envBytes), cfgPath, "prod", false)
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
	summary, err := runImport(bytes.NewReader([]byte("SHARED=new\nNEW=2\n")), cfgPath, "p", false)
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
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true)
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
	_, err := runImport(bytes.NewReader([]byte("NEW=2\n")), cfgPath, "p", true)
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

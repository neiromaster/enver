package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/spf13/cobra"
)

// Write-target integration tests: each mutator writes to the file writeTarget()
// picks (local by default, global under --global) and never leaks to the other
// layer. add/edit are interactive-only (see noninteractive_test.go).

// chdirTemp chdir to a temp dir, resolving the macOS /var symlink so LocalPath() holds.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	return dir
}

// saveGlobalFlags snapshots the package globalFlags and restores it on cleanup.
func saveGlobalFlags(t *testing.T) {
	t.Helper()
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })
}

// twoLayers builds distinct global and local files, each with profile "p" (A=g
// in global, A=l in local). noLocal makes the merged read deterministic; the
// caller picks the write target via globalFlags.global.
func twoLayers(t *testing.T) (globalPath, localPath string) {
	t.Helper()
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalPath = filepath.Join(dir, "global.yaml")
	localPath = config.LocalPath()
	if err := config.UpsertProfile(globalPath, "p", config.Profile{Env: map[string]string{"A": "g"}}, false, false); err != nil {
		t.Fatalf("global upsert: %v", err)
	}
	if err := config.UpsertProfile(localPath, "p", config.Profile{Env: map[string]string{"A": "l"}}, false, false); err != nil {
		t.Fatalf("local upsert: %v", err)
	}
	globalFlags.configPath = globalPath
	globalFlags.noLocal = true
	return globalPath, localPath
}

func profileExists(path, name string) bool {
	_, _, ok, _ := config.ReadProfile(path, name)
	return ok
}

func profileDefault(path, name string) bool {
	_, isDefault, _, _ := config.ReadProfile(path, name)
	return isDefault
}

func profileEnv(path, name, key string) string {
	prof, _, _, _ := config.ReadProfile(path, name)
	return prof.Env[key]
}

// targetAndOther returns the write target (the file the command should mutate)
// and the other layer (which must stay unchanged), given the --global choice.
func targetAndOther(global bool, globalPath, localPath string) (target, other string) {
	if global {
		return globalPath, localPath
	}
	return localPath, globalPath
}

func TestDuplicateWriteTarget(t *testing.T) {
	setNonInteractive(t)
	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			gPath, lPath := twoLayers(t)
			globalFlags.global = global
			if err := duplicateCmd.RunE(&cobra.Command{}, []string{"p", "q"}); err != nil {
				t.Fatalf("duplicate: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if !profileExists(target, "q") {
				t.Fatalf("q should be in %s", target)
			}
			if profileExists(other, "q") {
				t.Fatalf("q must not leak into %s", other)
			}
		})
	}
}

func TestRenameWriteTarget(t *testing.T) {
	setNonInteractive(t)
	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			gPath, lPath := twoLayers(t)
			globalFlags.global = global
			if err := renameCmd.RunE(&cobra.Command{}, []string{"p", "q"}); err != nil {
				t.Fatalf("rename: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if !profileExists(target, "q") || profileExists(target, "p") {
				t.Fatalf("target should have q (not p): target=%s", target)
			}
			if !profileExists(other, "p") || profileExists(other, "q") {
				t.Fatalf("other layer must keep p (not q): other=%s", other)
			}
		})
	}
}

func TestRemoveWriteTarget(t *testing.T) {
	setNonInteractive(t)
	prev := removeYes
	removeYes = true
	t.Cleanup(func() { removeYes = prev })
	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			gPath, lPath := twoLayers(t)
			globalFlags.global = global
			if err := removeCmd.RunE(&cobra.Command{}, []string{"p"}); err != nil {
				t.Fatalf("remove: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if profileExists(target, "p") {
				t.Fatalf("p should be removed from %s", target)
			}
			if !profileExists(other, "p") {
				t.Fatalf("p must remain in the other layer %s", other)
			}
		})
	}
}

func TestDefaultWriteTarget(t *testing.T) {
	setNonInteractive(t)
	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			gPath, lPath := twoLayers(t)
			globalFlags.global = global
			if err := defaultCmd.RunE(&cobra.Command{}, []string{"p"}); err != nil {
				t.Fatalf("default: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if !profileDefault(target, "p") {
				t.Fatalf("p should be the default in %s", target)
			}
			if profileDefault(other, "p") {
				t.Fatalf("default must not flip in the other layer %s", other)
			}
		})
	}
}

func TestImportWriteTarget(t *testing.T) {
	setNonInteractive(t)
	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			gPath, lPath := twoLayers(t)
			globalFlags.global = global
			if err := importCmd.RunE(&cobra.Command{}, []string{writeEnvFile(t), "newprof"}); err != nil {
				t.Fatalf("import: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if profileEnv(target, "newprof", "X") != "1" {
				t.Fatalf("newprof should be imported into %s", target)
			}
			if profileExists(other, "newprof") {
				t.Fatalf("newprof must not leak into %s", other)
			}
		})
	}
}

func TestEncryptWriteTarget(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	keyPath := filepath.Join(dir, "key")
	if err := crypto.GenerateKey(keyPath, true); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	globalFlags.keyPath = keyPath

	gPath := filepath.Join(dir, "global.yaml")
	lPath := config.LocalPath()
	if err := config.UpsertProfile(gPath, "p", config.Profile{Env: map[string]string{"A": "g"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(lPath, "p", config.Profile{Env: map[string]string{"A": "l"}}, false, false); err != nil {
		t.Fatal(err)
	}
	globalFlags.configPath = gPath
	globalFlags.noLocal = true

	for _, global := range []bool{false, true} {
		name := "local"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			// Re-create both layers so each subtest starts from plaintext.
			_ = config.UpsertProfile(gPath, "p", config.Profile{Env: map[string]string{"A": "g"}}, false, true)
			_ = config.UpsertProfile(lPath, "p", config.Profile{Env: map[string]string{"A": "l"}}, false, true)
			globalFlags.global = global
			cmd := &cobra.Command{}
			cmd.Flags().Bool("all", false, "")
			_ = cmd.Flags().Set("all", "true")
			if err := encryptCmd.RunE(cmd, nil); err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			target, other := targetAndOther(global, gPath, lPath)
			if !strings.HasPrefix(profileEnv(target, "p", "A"), "enc:") {
				t.Fatalf("target %s value should be encrypted: %q", target, profileEnv(target, "p", "A"))
			}
			if strings.HasPrefix(profileEnv(other, "p", "A"), "enc:") {
				t.Fatalf("other layer %s must stay plaintext: %q", other, profileEnv(other, "p", "A"))
			}
		})
	}
}

// TestCompleteProfileMergedForReads pins #3: read commands get merged completion,
// so global-only profiles are suggested even with no local .enver.yaml.
func TestCompleteProfileMergedForReads(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	gPath := filepath.Join(dir, "global.yaml")
	if err := config.UpsertProfile(gPath, "dev", config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	// No local .enver.yaml in dir -- a global-only setup, the default layout.
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")
	_ = cmd.Flags().Set("config", gPath)

	got, directive := completeProfile(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive=%v, want NoFileComp", directive)
	}
	if len(got) != 1 || got[0] != "dev" {
		t.Fatalf("completeProfile = %v, want [dev] from the merged view", got)
	}
}

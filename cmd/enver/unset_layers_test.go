package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// writeDoc drops a YAML document at path for a dispatch test.
func writeDoc(t *testing.T, path, doc string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCommand drives the real cobra dispatch: persistent flags parsed,
// PersistentPreRunE applies --chdir, stdout captured. State touched along
// the way is restored through t.Cleanup.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	var out bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err = rootCmd.Execute()
	return strings.TrimSuffix(out.String(), "\n"), err
}

// TestUnsetLayersShowMatrix pins the composed pipeline the in-memory merge
// tests never traverse: both YAML files read off disk, layered by LoadMerged,
// rendered exactly as show prints it.
func TestUnsetLayersShowMatrix(t *testing.T) {
	fencedGlobal := "profiles:\n" +
		"  anth:\n" +
		"    env:\n      EDITOR: vim\n      THEME: ocean\n" +
		"  bare:\n" +
		"    extends: anth\n" +
		"    unset: [EDITOR]\n"
	selfFenceGlobal := "profiles:\n" +
		"  anth:\n" +
		"    env:\n      THEME: forest\n" +
		"  bare:\n" +
		"    extends: anth\n" +
		"    env:\n      THEME: dusk\n" +
		"    unset: [THEME]\n"

	for _, tc := range []struct {
		name    string
		global  string
		local   string // "" leaves the cwd layer absent
		profile string
		want    []string
	}{
		{
			name:    "local define beats a global-layer unset",
			global:  "profiles:\n  dev:\n    unset: MODE\n",
			local:   "profiles:\n  dev:\n    env:\n      MODE: fast\n",
			profile: "dev",
			want:    []string{"# profile: dev", "MODE=fast  # from dev (local)"},
		},
		{
			name:    "carried chain fence survives an unrelated local override",
			global:  fencedGlobal,
			local:   "profiles:\n  bare:\n    env:\n      THEME: sunset\n",
			profile: "bare",
			want: []string{
				"# profile: bare → anth",
				"# EDITOR — unset by \"bare\"",
				"THEME=sunset  # from bare (local)",
			},
		},
		{
			name:    "later-era refill through the ancestor outruns the earlier fence",
			global:  fencedGlobal,
			local:   "profiles:\n  anth:\n    env:\n      EDITOR: nano\n",
			profile: "bare",
			want: []string{
				"# profile: bare → anth",
				"EDITOR=nano  # from anth (local)",
				"THEME=ocean  # from anth (global)",
			},
		},
		{
			name:    "self-fence stays stripped under an unrelated local file",
			global:  selfFenceGlobal,
			local:   "profiles:\n  foo:\n    env:\n      X: '1'\n",
			profile: "bare",
			want: []string{
				"# profile: bare → anth",
				"# THEME — unset by \"bare\"",
			},
		},
		{
			name:    "unmerged single-file self-fence matches the merged view",
			global:  selfFenceGlobal,
			local:   "",
			profile: "bare",
			want: []string{
				"# profile: bare → anth",
				"# THEME — unset by \"bare\"",
			},
		},
		{
			name: "sibling parent supplies fresh values past the other branch fence",
			global: "profiles:\n" +
				"  p1:\n    env:\n      LANG: ru\n" +
				"  p2:\n    extends: p1\n    unset: [LANG]\n",
			local:   "profiles:\n  c:\n    extends: [p1, p2]\n",
			profile: "c",
			want:    []string{"# profile: c → p1 → p2", "LANG=ru  # from p1 (global)"},
		},
		{
			name: "the same composition resists parent reordering",
			global: "profiles:\n" +
				"  p1:\n    env:\n      LANG: ru\n" +
				"  p2:\n    extends: p1\n    unset: [LANG]\n",
			local:   "profiles:\n  c:\n    extends: [p2, p1]\n",
			profile: "c",
			want:    []string{"# profile: c → p2 → p1", "LANG=ru  # from p1 (global)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			globalPath := filepath.Join(dir, "global.yaml")
			writeDoc(t, globalPath, tc.global)
			if tc.local != "" {
				writeDoc(t, filepath.Join(dir, config.LocalFilename), tc.local)
			}
			out, err := runCommand(t, "--config", globalPath, "--chdir", dir, "show", tc.profile)
			if err != nil {
				t.Fatalf("show %s: %v", tc.profile, err)
			}
			if got := strings.Join(tc.want, "\n"); out != got {
				t.Fatalf("show output:\n%s\nwant:\n%s", out, got)
			}
		})
	}
}

// xHelperDir marks a test-binary re-execution that runs the x command in
// place of the parent: unix x delivers the child via syscall.Exec, which
// replaces the calling process, so the command must run in a sacrificial
// process or it would take the test binary with it.
const xHelperDir = "ENVER_TEST_X_HELPER_DIR"

// TestXRealChildPassesShellThroughFence is the only member that forks an
// actual child: the fenced key rides in nowhere from the overlay, so the
// shell's live value reaches the process untouched, while the overlay
// injects its own key beside it.
func TestXRealChildPassesShellThroughFence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawns /bin/sh; Windows delivery is pinned at MergedEnv level")
	}
	if dir := os.Getenv(xHelperDir); dir != "" {
		xHelperExec(t, dir)
		return
	}

	dir := t.TempDir()
	workDir := filepath.Join(dir, "wd")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(dir, "global.yaml")
	writeDoc(t, globalPath, "profiles:\n"+
		"  anth:\n"+
		"    env:\n      THEME: ocean\n"+
		"  bare:\n"+
		"    extends: anth\n"+
		"    unset: [EDITOR]\n")

	childEnv := filepath.Join(dir, "child-env.txt")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(exe, "-test.run=^"+t.Name()+"$")
	helper.Env = append(os.Environ(), xHelperDir+"="+dir)
	if out, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("x helper: %v\n%s", err, out)
	}
	got, err := os.ReadFile(childEnv)
	if err != nil {
		t.Fatal(err)
	}
	if want := "EDITOR=shell-live\nTHEME=ocean\n"; string(got) != want {
		t.Fatalf("child env:\n%s\nwant:\n%s", got, want)
	}
}

// xHelperExec runs inside the re-executed binary: it wires the fixture under
// dir and lets x deliver the child. Without GOCOVERDIR the exec replaces this
// process with the shell and never returns, so reaching the end of the
// function means the child exited without replacing us; under a coverage
// build (GOCOVERDIR set — the same signal execChild keys on) fork+wait is the
// expected delivery, and surviving is the pass.
func xHelperExec(t *testing.T, dir string) {
	t.Setenv("EDITOR", "shell-live")
	workDir := filepath.Join(dir, "wd")
	globalPath := filepath.Join(dir, "global.yaml")
	childEnv := filepath.Join(dir, "child-env.txt")
	script := fmt.Sprintf("printf 'EDITOR=%%s\\nTHEME=%%s\\n' \"$EDITOR\" \"$THEME\" > %q", childEnv)

	if _, err := runCommand(t, "--config", globalPath, "--chdir", workDir, "x", "bare", "--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("x bare: %v", err)
	}
	// Coverage builds lose syscall.Exec to the waitChild fallback, so only a
	// plain run must prove the process replacement.
	if os.Getenv("GOCOVERDIR") == "" {
		t.Fatal("x must replace the helper process via exec")
	}
}

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPowerShellCompletionServesProfiles pins the shipped completer against a
// real shell: the script enver itself generates is dot-sourced, then every Tab
// press a user would make is replayed through TabExpansion2, the engine
// function the interactive host calls. The whole path is covered at once — the
// generated script, the PATH lookup of the binary and the __complete protocol.
func TestPowerShellCompletionServesProfiles(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		home := filepath.Join(dir, "home")
		cfgHome := filepath.Join(home, ".config")
		writePSCompletionFixture(t, filepath.Join(cfgHome, "enver", "config.yaml"),
			"profiles:\n  alpha:\n    env:\n      A: \"1\"\n  beta:\n    env:\n      B: \"2\"\n")
		project := filepath.Join(dir, "project")
		if err := os.Mkdir(project, 0o755); err != nil {
			t.Fatal(err)
		}
		writePSCompletionFixture(t, filepath.Join(project, "cfg.yaml"),
			"profiles:\n  own1:\n    env:\n      A: \"1\"\n  own2:\n    env:\n      B: \"2\"\n"+
				"remotes:\n  r1:\n    url: https://example.invalid/r1.yaml\n  r2:\n    url: https://example.invalid/r2.yaml\n")

		generated, err := exec.Command(enverBin, "completion", "powershell").Output()
		if err != nil {
			t.Fatalf("enver completion powershell: %v", err)
		}
		if len(generated) == 0 {
			t.Fatal("enver completion powershell produced an empty script")
		}
		completer := filepath.Join(dir, "enver.ps1")
		writePSCompletionFixture(t, completer, string(generated))

		env := map[string]string{
			"XDG_CONFIG_HOME": cfgHome,
			"HOME":            home,
			"USERPROFILE":     home,
			pathEnvKey():      filepath.Dir(enverBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		}

		cases := []struct {
			line string
			want []string
		}{
			{"enver show ", []string{"alpha", "beta"}},
			{"enver show --file cfg.yaml ", []string{"own1", "own2"}},
			{"enver pull --file cfg.yaml ", []string{"r1", "r2"}},
			{"enver edit --file cfg.yaml ", []string{"own1", "own2"}},
		}

		// The --file cases are relative, so the child must sit in the fixture
		// project before completing; the harness has no cwd seam, and moving the
		// engine is the same thing a user does before pressing Tab.
		var snippet strings.Builder
		fmt.Fprintf(&snippet, ". %s\n", psQuote(completer))
		fmt.Fprintf(&snippet, "Set-Location -LiteralPath %s\n", psQuote(project))
		for i, c := range cases {
			fmt.Fprintf(&snippet, "'---case %d---'\n", i)
			fmt.Fprintf(&snippet, "$r = TabExpansion2 -inputScript %s -cursorColumn %d\n", psQuote(c.line), len(c.line))
			snippet.WriteString("$r.CompletionMatches | ForEach-Object { $_.CompletionText }\n")
		}

		stdout, err := runPowerShell(t, engine, "", snippet.String(), env)
		if err != nil {
			t.Fatalf("driving %s: %v", filepath.Base(engine), err)
		}
		got := splitPSCompletionCases(t, stdout, len(cases))
		for i, c := range cases {
			t.Run(strings.TrimSpace(c.line), func(t *testing.T) {
				slices.Sort(got[i])
				want := slices.Clone(c.want)
				slices.Sort(want)
				if !slices.Equal(got[i], want) {
					t.Fatalf("completion of %q = %q, want %q (raw engine output below)\n%s",
						c.line, got[i], want, stdout)
				}
			})
		}
	})
}

// writePSCompletionFixture writes a file and any missing parents.
func writePSCompletionFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pathEnvKey returns the spelling the parent environment uses for PATH, so the
// child replaces that entry instead of carrying a second case variant that
// Windows would resolve inconsistently.
func pathEnvKey() string {
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			return k
		}
	}
	return "PATH"
}

// splitPSCompletionCases cuts the engine output at the case markers and returns
// one slice of completion lines per case, in order. Engine noise before the
// first marker is a harness bug, not completion output.
func splitPSCompletionCases(t *testing.T, stdout string, n int) [][]string {
	t.Helper()
	got := make([][]string, n)
	current := -1
	for _, line := range strings.Split(stdout, "\n") {
		// In its Complete mode the script pads a short candidate with spaces so
		// the descriptions line up. That is display alignment, not the value, and
		// no profile or record name ends in whitespace.
		line = strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(line, "---case ") && strings.HasSuffix(line, "---") {
			var i int
			if _, err := fmt.Sscanf(line, "---case %d---", &i); err != nil || i < 0 || i >= n {
				t.Fatalf("unparsable case marker %q in output:\n%s", line, stdout)
			}
			current = i
			continue
		}
		if line == "" {
			continue
		}
		if current < 0 {
			t.Fatalf("output before the first case marker: %q\n%s", line, stdout)
		}
		got[current] = append(got[current], line)
	}
	return got
}

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/dotenv"
)

// TestImportEnvMergeAndReplaceSummary pins the app-level import seam: merge
// overrides same-named keys and reports the mode, replace wipes the profile's
// own env first and reports the removals.
func TestImportEnvMergeAndReplaceSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  a:\n    env:\n      A: old\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := strings.NewReader("A=new\nB=2\n")
	summary, err := ImportEnv(r, path, "a", "", ImportOptions{})
	if err != nil {
		t.Fatalf("ImportEnv: %v", err)
	}
	if !strings.Contains(summary, `imported 2 vars into "a" — merge`) {
		t.Fatalf("summary = %q", summary)
	}
	got, err := config.LoadFile(path)
	if err != nil || got.Profiles["a"].Env["A"] != "new" || got.Profiles["a"].Env["B"] != "2" {
		t.Fatalf("post-merge env = %v, %v", got.Profiles["a"].Env, err)
	}

	r = strings.NewReader("C=3\n")
	summary, err = ImportEnv(r, path, "a", "", ImportOptions{Replace: true, Force: true})
	if err != nil {
		t.Fatalf("ImportEnv replace: %v", err)
	}
	if !strings.Contains(summary, "replaced") {
		t.Fatalf("summary = %q", summary)
	}
}

// globalParentOnly marks GH_TOKEN in a parent no target profile extends.
const globalParentOnly = "profiles:\n  base:\n    env:\n      ~GH_TOKEN: parent\n"

// globalWithParent also gives work the extends only the merged view can see, so
// the target file on its own knows nothing of the marking parent.
const globalWithParent = globalParentOnly + "  work:\n    extends: base\n"

// TestImportEnvKeepsInheritedMark pins the cross-layer case: the parent that
// marks GH_TOKEN lives in the other layer, so the writer rewriting this file
// cannot see it. The merged view decides the mark — inherited where the profile
// defines no entry of its own, defeated by an own unmarked entry, and still
// inherited by --replace when the profile keeps the extends.
func TestImportEnvKeepsInheritedMark(t *testing.T) {
	tests := []struct {
		name       string
		global     string
		target     string
		replace    bool
		wantMarked bool
	}{
		{
			name:       "inherited mark",
			global:     globalWithParent,
			target:     "profiles:\n  work:\n    env:\n      OTHER: x\n",
			wantMarked: true,
		},
		{
			name:       "own unmarked entry wins",
			global:     globalWithParent,
			target:     "profiles:\n  work:\n    env:\n      GH_TOKEN: old\n",
			wantMarked: false,
		},
		{
			name:       "replace keeps an extends the target declares",
			global:     globalParentOnly,
			target:     "profiles:\n  work:\n    extends: base\n    env:\n      OTHER: x\n",
			replace:    true,
			wantMarked: true,
		},
		{
			name:       "replace clears an extends the target does not declare",
			global:     globalWithParent,
			target:     "profiles:\n  work:\n    env:\n      OTHER: x\n",
			replace:    true,
			wantMarked: false,
		},
		{
			// The replace wipes the fence, so the key comes back into the
			// resolved env and with it the parent's mark.
			name:       "replace clears a fence over the inherited key",
			global:     globalWithParent,
			target:     "profiles:\n  work:\n    extends: base\n    unset: [GH_TOKEN]\n    env:\n      OTHER: x\n",
			replace:    true,
			wantMarked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			globalPath := filepath.Join(dir, "global.yaml")
			targetPath := filepath.Join(dir, ".enver.yaml")
			writeFile(t, globalPath, tt.global)
			writeFile(t, targetPath, tt.target)

			globalCfg, err := config.LoadFile(globalPath)
			if err != nil {
				t.Fatal(err)
			}
			targetCfg, err := config.LoadFile(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			merged := config.Merge(globalCfg, targetCfg)
			if _, err := ImportEnv(strings.NewReader("GH_TOKEN=new\n"), targetPath, "work", "", ImportOptions{
				Replace: tt.replace,
				Force:   true,
				Config:  &merged,
			}); err != nil {
				t.Fatalf("ImportEnv: %v", err)
			}

			got, err := config.LoadFile(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			if v := got.Profiles["work"].Env["GH_TOKEN"]; v != "new" {
				t.Fatalf("GH_TOKEN = %q, want new", v)
			}
			if marked := got.Profiles["work"].Marked["GH_TOKEN"]; marked != tt.wantMarked {
				t.Fatalf("GH_TOKEN marked = %t, want %t (%#v)", marked, tt.wantMarked, got.Profiles["work"].Marked)
			}
			data, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			if wroteMarked := strings.Contains(string(data), "~GH_TOKEN"); wroteMarked != tt.wantMarked {
				t.Fatalf("file writes the mark = %t, want %t:\n%s", wroteMarked, tt.wantMarked, data)
			}
		})
	}
}

// TestImportEnvNilConfigKeepsWriterFallback pins the nil-Config contract: a
// caller without a merged view hands the writer no marks at all, so the
// writer's own in-file chain still supplies an inherited mark — unchanged
// behavior for every caller that has no view to offer.
func TestImportEnvNilConfigKeepsWriterFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "profiles:\n  base:\n    env:\n      ~GH_TOKEN: parent\n  work:\n    extends: base\n")
	if _, err := ImportEnv(strings.NewReader("GH_TOKEN=new\n"), path, "work", "", ImportOptions{}); err != nil {
		t.Fatalf("ImportEnv: %v", err)
	}
	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Profiles["work"].Marked["GH_TOKEN"] {
		t.Fatalf("the writer's in-file chain must still mark the key: %#v", got.Profiles["work"])
	}
}

// writeFile seeds a config file a test reads back.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSkippedLineNote pins the import appendix: the singular noun at one
// skip, the full line list at two, and the cap-at-3 fold with a remaining
// count beyond that.
func TestSkippedLineNote(t *testing.T) {
	skips := func(n int) []dotenv.Skip {
		out := make([]dotenv.Skip, n)
		for i := range out {
			out[i] = dotenv.Skip{Line: i + 1, Reason: "test"}
		}
		return out
	}
	tests := []struct {
		name string
		n    int
		want string // exact note; empty when only the fold is pinned
		fold string // remaining-count marker; "" when nothing may fold
	}{
		{name: "one skip is singular", n: 1, want: "\nskipped 1 line: line 1 (test)\n"},
		{name: "two skips list both", n: 2, want: "\nskipped 2 lines: line 1 (test), line 2 (test)\n"},
		{name: "four skips fold one", n: 4, fold: "… 1 more"},
		{name: "five skips fold two", n: 5, fold: "… 2 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skippedLineNote(skips(tt.n))
			if tt.want != "" && got != tt.want {
				t.Fatalf("skippedLineNote(%d skips) = %q, want %q", tt.n, got, tt.want)
			}
			prefix := fmt.Sprintf("\nskipped %d line", tt.n)
			if tt.n != 1 {
				prefix += "s"
			}
			if !strings.HasPrefix(got, prefix+": ") {
				t.Fatalf("skippedLineNote(%d skips) = %q, want prefix %q", tt.n, got, prefix+": ")
			}
			if hasFold := strings.Contains(got, "… "); hasFold != (tt.fold != "") {
				t.Fatalf("skippedLineNote(%d skips) = %q, fold marker present = %t", tt.n, got, hasFold)
			}
			if tt.fold != "" {
				if !strings.Contains(got, tt.fold) {
					t.Fatalf("skippedLineNote(%d skips) = %q, want fold marker %q", tt.n, got, tt.fold)
				}
				if strings.Contains(got, fmt.Sprintf("line %d (test)", tt.n)) {
					t.Fatalf("skippedLineNote(%d skips) = %q, want the last line folded away", tt.n, got)
				}
			}
		})
	}
}

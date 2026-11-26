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

// TestImportSummaryPlaintextHint pins the summary's closing hint: an import
// never seals, so it must say the values landed as plaintext and name both
// ways to protect them — marking the keys, or sealing the whole profile.
func TestImportSummaryPlaintextHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "profiles:\n  p:\n    env:\n      A: old\n")
	summary, err := ImportEnv(strings.NewReader("A=1\n"), path, "p", "", ImportOptions{})
	if err != nil {
		t.Fatalf("ImportEnv: %v", err)
	}
	for _, want := range []string{
		"Imported values are stored in plaintext",
		"`enver edit p` Secrets menu",
		"`~KEY` in the config file",
		"`enver encrypt p --all`",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary must mention %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Run `enver encrypt p` to encrypt secrets.") {
		t.Fatalf("summary must not send the reader to a command that seals nothing:\n%s", summary)
	}
}

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
			// The target file declares no extends of its own, so nothing in it
			// would keep the parent alive: the mark survives only because the
			// other layer still declares that parent after the rewrite.
			name:       "replace keeps the extends only the other layer declares",
			global:     globalWithParent,
			target:     "profiles:\n  work:\n    env:\n      OTHER: x\n",
			replace:    true,
			wantMarked: true,
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
			if _, err := ImportEnv(strings.NewReader("GH_TOKEN=new\n"), targetPath, "work", "", ImportOptions{
				Replace: tt.replace,
				Force:   true,
				Under:   []config.Layer{{Label: config.LayerGlobal, Config: globalCfg}},
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
			// The imported line carries the value new, so this pins the spelling
			// of the written entry rather than some parent's mark of its own.
			if wroteMarked := strings.Contains(string(data), "~GH_TOKEN: new"); wroteMarked != tt.wantMarked {
				t.Fatalf("file writes the mark = %t, want %t:\n%s", wroteMarked, tt.wantMarked, data)
			}
		})
	}
}

// TestImportEnvFoldsUnderStack pins the fold direction of a write into the global
// layer: the stack the target sits on folds first, deepest layer first, and the
// target folds over it. Extends concatenate base-first, so a nearer layer wins a
// shared key's value and mark — and a fence keeps the era it was declared in,
// whichever layer that is.
func TestImportEnvFoldsUnderStack(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		under      string
		wantMarked bool
	}{
		{
			name:       "a remote parent supplies the mark",
			target:     "profiles:\n  work:\n    extends: [marked]\n",
			under:      "profiles:\n  marked:\n    env:\n      ~GH_TOKEN: m\n",
			wantMarked: true,
		},
		{
			name:       "the target's own entry outranks the remote parent",
			target:     "profiles:\n  marked:\n    env:\n      GH_TOKEN: plain\n  work:\n    extends: [marked]\n",
			under:      "profiles:\n  marked:\n    env:\n      ~GH_TOKEN: m\n",
			wantMarked: false,
		},
		{
			name:       "a remote fence strips the key it fences",
			target:     "profiles:\n  work:\n    extends: [fenced]\n",
			under:      "profiles:\n  fenced:\n    unset: [GH_TOKEN]\n    env:\n      ~GH_TOKEN: m\n",
			wantMarked: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			targetPath := filepath.Join(dir, "global.yaml")
			underPath := filepath.Join(dir, "remote.yaml")
			writeFile(t, targetPath, tt.target)
			writeFile(t, underPath, tt.under)

			underCfg, err := config.LoadFile(underPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ImportEnv(strings.NewReader("GH_TOKEN=new\n"), targetPath, "work", "", ImportOptions{
				Force:          true,
				Under:          []config.Layer{{Label: config.RemoteLayer("llm"), Config: underCfg}},
				TargetIsGlobal: true,
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
			// The imported line carries the value new, so this pins the spelling
			// of the written entry rather than some parent's mark of its own.
			if wroteMarked := strings.Contains(string(data), "~GH_TOKEN: new"); wroteMarked != tt.wantMarked {
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

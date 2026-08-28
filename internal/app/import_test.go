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

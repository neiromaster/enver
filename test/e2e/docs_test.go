package e2e

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsReconciled pins the statements the docs make about the unset menus,
// completion and the default check, and keeps their cross-links resolving so a
// moved section cannot leave a pointer rotting.
func TestDocsReconciled(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repoRoot(), "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	if got := read("config.md"); strings.Contains(got, "menus do not manage the list") {
		t.Errorf("docs/config.md still says the menus do not manage the unset list")
	}
	for _, want := range []string{"enver completion", "default file completion"} {
		if got := read("cli.md"); !strings.Contains(got, want) {
			t.Errorf("docs/cli.md lacks %q", want)
		}
	}
	if want := "default naming no profile"; !strings.Contains(read("profiles.md"), want) {
		t.Errorf("docs/profiles.md lacks %q", want)
	}

	for _, name := range []string{"config.md", "cli.md", "profiles.md"} {
		checkDocLinks(t, name)
	}
}

// docLinkRe matches a markdown inline link target. Only relative .md targets
// are checked: external URLs, in-page anchors and non-markdown files resolve
// elsewhere and are not this test's business.
var docLinkRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)

func checkDocLinks(t *testing.T, name string) {
	t.Helper()
	// Rooting the check at docs/ keeps a target from resolving outside it.
	docsFS := os.DirFS(filepath.Join(repoRoot(), "docs"))
	data, err := fs.ReadFile(docsFS, name)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, m := range docLinkRe.FindAllStringSubmatch(string(data), -1) {
		target := m[1]
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		if target == "" || path.IsAbs(target) ||
			!strings.HasSuffix(target, ".md") || strings.Contains(target, "://") {
			continue
		}
		checked++
		if _, err := fs.Stat(docsFS, path.Clean(target)); err != nil {
			t.Errorf("docs/%s: link %q does not resolve: %v", name, m[1], err)
		}
	}
	if checked == 0 {
		t.Fatalf("docs/%s: no relative markdown links found, so the check proves nothing", name)
	}
}

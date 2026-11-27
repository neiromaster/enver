package config

import (
	"slices"
	"strings"
	"testing"
)

func TestInspectFile(t *testing.T) {
	for _, c := range []struct {
		in      string
		kind    FileKind
		unknown []string
	}{
		{"publish: true\nprofiles: {}\n", FilePublish, nil},
		{"publish: true\n", FilePublish, nil},
		{"publish: false\nprofiles: {}\n", FileLocal, nil},
		{"profiles:\n  base:\n    env:\n      K: v\n", FileLocal, nil},
		{"remotes:\n  r:\n    url: https://example.invalid/r.yaml\n", FileLocal, nil},
		{"default: base\n", FileLocal, nil},
		{"name: ci\non: [push]\n", FileNotEnver, nil},
		{"", FileNotEnver, nil},
		{"- a\n- b\n", FileNotEnver, nil},
		{"x-notes: keep\nprofiles: {}\n", FileLocal, []string{"x-notes"}},
		{"profiles: &base\n  p:\n    env:\n      K: v\n<<: *base\n", FileLocal, []string{"p"}},
		// The mark and the section say what the file is even when they arrive
		// through a top-level merge key.
		{"shared: &s\n  profiles:\n    p:\n      env:\n        K: v\n<<: *s\n", FileLocal, []string{"shared"}},
		{"shared: &s\n  publish: true\n<<: *s\n", FilePublish, []string{"shared"}},
		{"shared: &s\n  default: p\n<<: *s\n", FileLocal, []string{"shared"}},
		{"shared: &s\n  profiles:\n    p: {}\n  fallback: &f\n    default: p\n<<: [*s, *f]\n", FileLocal, []string{"shared", "fallback"}},
	} {
		kind, unknown, err := InspectFile([]byte(c.in))
		if err != nil || kind != c.kind || !slices.Equal(unknown, c.unknown) {
			t.Errorf("InspectFile(%q) = %v, %q, %v; want %v, %q", c.in, kind, unknown, err, c.kind, c.unknown)
		}
	}
}

func TestInspectFileRefusesBadMark(t *testing.T) {
	for _, in := range []string{"publish: yes\nprofiles: {}\n", "publish: \"true\"\nprofiles: {}\n", "publish: 1\nprofiles: {}\n", "publish: maybe\nprofiles: {}\n"} {
		if _, _, err := InspectFile([]byte(in)); err == nil || !strings.Contains(err.Error(), "publish") {
			t.Errorf("InspectFile(%q) = %v, want an error naming publish", in, err)
		}
	}
	if _, _, err := InspectFile([]byte("profiles: [\n")); err == nil {
		t.Error("a document that is not YAML must be an error")
	}
}

// An aliased mark resolves to the boolean it names before the tag test, so the
// alias spelling of publish: true is a mark, not a bad one.
func TestInspectFileAcceptsAliasedMark(t *testing.T) {
	kind, unknown, err := InspectFile([]byte("mark: &t true\npublish: *t\n"))
	if err != nil || kind != FilePublish || !slices.Equal(unknown, []string{"mark"}) {
		t.Fatalf("aliased mark = %v, %q, %v; want FilePublish with the anchor key unknown", kind, unknown, err)
	}
}

// The mark spelled inside a profile is ignored by the decoder, so a file whose
// author plainly meant to publish would be judged local; the inspector refuses
// it instead, naming where the mark belongs.
func TestInspectFileRefusesPublishInsideProfile(t *testing.T) {
	const in = "profiles:\n  a:\n    publish: true\n    env:\n      K: v\n"
	kind, _, err := InspectFile([]byte(in))
	if err == nil || !strings.Contains(err.Error(), "publish is not a profile field; the mark belongs at the top level of the file") {
		t.Fatalf("InspectFile(%q) = %v, %v; want the misplaced-mark error", in, kind, err)
	}
	if _, _, err := InspectFile([]byte("publish: true\nprofiles:\n  a:\n    env:\n      K: v\n")); err != nil {
		t.Fatalf("a mark at the top level with no profile mark = %v", err)
	}
}

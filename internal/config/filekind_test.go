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
		{"profiles: &base\n  p:\n    env:\n      K: v\n<<: *base\n", FileLocal, nil},
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

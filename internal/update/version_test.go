package update

import "testing"

func TestCanonicalVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.9.1", "v0.9.1"},
		{"v0.9.1", "v0.9.1"},
		{"0.9.1-g1234567", "v0.9.1"},
		{"v0.9.1-g1234567abc", "v0.9.1"},
		{"dev", "v0.0.0"},
		{"(devel)", "v0.0.0"},
		{"", "v0.0.0"},
	}
	for _, c := range cases {
		if got := canonicalVersion(c.in); got != c.want {
			t.Errorf("canonicalVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLatestOrEqual(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.9.1", "0.9.1", true},
		{"0.9.2", "0.9.1", true},
		{"0.8.4", "0.9.1", false},
		{"0.9.1-g1234567", "0.9.1", true},
		{"dev", "0.9.1", false},
	}
	for _, c := range cases {
		if got := latestOrEqual(c.current, c.latest); got != c.want {
			t.Errorf("latestOrEqual(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

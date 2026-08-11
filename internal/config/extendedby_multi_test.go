package config

import (
	"testing"
)

func TestExtendedByMultiParent(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"base":   {},
			"child1": {Extends: Extends{"base"}},
			"child2": {Extends: Extends{"base"}},
			"multi":  {Extends: Extends{"child1", "child2", "base"}},
		},
	}

	got := cfg.ExtendedBy("base")
	want := []string{"child1", "child2", "multi"}
	if !sliceEq(got, want) {
		t.Fatalf("ExtendedBy(base) = %v, want %v", got, want)
	}

	got = cfg.ExtendedBy("child1")
	want = []string{"multi"}
	if !sliceEq(got, want) {
		t.Fatalf("ExtendedBy(child1) = %v, want %v", got, want)
	}

	got = cfg.ExtendedBy("child2")
	want = []string{"multi"}
	if !sliceEq(got, want) {
		t.Fatalf("ExtendedBy(child2) = %v, want %v", got, want)
	}
}

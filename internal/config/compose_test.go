package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// cfgOf builds a Config from (name, Profile) pairs so composition tests read
// as the YAML they encode.
func cfgOf(t *testing.T, pairs ...any) Config {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("cfgOf needs name/Profile pairs, got %d args", len(pairs))
	}
	cfg := Config{Profiles: map[string]Profile{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("cfgOf: name %v is not a string", pairs[i])
		}
		prof, ok := pairs[i+1].(Profile)
		if !ok {
			t.Fatalf("cfgOf: profile %v is not a Profile", pairs[i+1])
		}
		cfg.Profiles[name] = prof
	}
	return cfg
}

func TestResolveProfilesFoldsLeftToRight(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Env: map[string]string{"SHARED": "from-a", "ONLY_A": "a"}},
		"b": {Env: map[string]string{"SHARED": "from-b", "ONLY_B": "b"}},
	}}

	r, err := cfg.ResolveProfiles([]string{"a", "b"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if got := r.Env["SHARED"]; got != "from-b" {
		t.Fatalf("SHARED = %q, want the later profile's value (left-to-right fold)", got)
	}
	if got := r.Env["ONLY_A"]; got != "a" {
		t.Fatalf("ONLY_A = %q, want %q", got, "a")
	}
	if got := r.Env["ONLY_B"]; got != "b" {
		t.Fatalf("ONLY_B = %q, want %q", got, "b")
	}
	if got := r.Chain; !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Chain = %v, want [a b]", got)
	}
}

func TestResolveProfilesSiblingUnsetNeverStripsSiblingDefine(t *testing.T) {
	cfg := cfgOf(t,
		"fenced", Profile{Unset: Unsets{"X"}},
		"definer", Profile{Env: map[string]string{"X": "live"}},
	)
	for _, names := range [][]string{{"fenced", "definer"}, {"definer", "fenced"}} {
		r, err := cfg.ResolveProfiles(names)
		if err != nil {
			t.Fatalf("ResolveProfiles(%v): %v", names, err)
		}
		if got := r.Env["X"]; got != "live" {
			t.Fatalf("%v: X = %q, want live (a sibling unset cannot strip a sibling define)", names, got)
		}
		if _, tomb := r.Unsets["X"]; tomb {
			t.Fatalf("%v: X carries a tombstone, want none (a key back in play holds no fence)", names)
		}
	}
}

func TestResolveProfilesUnsetWithinOwnChainStillFences(t *testing.T) {
	cfg := cfgOf(t,
		"base", Profile{Env: map[string]string{"X": "from-base"}},
		"leaf", Profile{Extends: Extends{"base"}, Unset: Unsets{"X"}},
	)
	r, err := cfg.ResolveProfiles([]string{"leaf"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if _, live := r.Env["X"]; live {
		t.Fatal("X is live, want fenced by leaf's own unset")
	}
	src, tomb := r.Unsets["X"]
	if !tomb {
		t.Fatal("X has no tombstone, want the reported fence")
	}
	if src.Profile != "leaf" {
		t.Fatalf("tombstone attributed to %q, want leaf", src.Profile)
	}
}

func TestResolveProfilesMarksMoveWithValues(t *testing.T) {
	cfg := cfgOf(t,
		"a", Profile{Env: map[string]string{"K": "secret"}, Marked: map[string]bool{"K": true}},
		"b", Profile{Env: map[string]string{"K": "plain"}},
	)
	r, err := cfg.ResolveProfiles([]string{"a", "b"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if r.Marked["K"] {
		t.Fatal("K is marked, want the later unmarked entry to clear it")
	}
	if r.Env["K"] != "plain" {
		t.Fatalf("K = %q, want plain", r.Env["K"])
	}

	r, err = cfg.ResolveProfiles([]string{"b", "a"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if !r.Marked["K"] {
		t.Fatal("K is unmarked, want the later marked entry to carry the mark")
	}
}

func TestResolveProfilesSourcesAndCommentsFollowWinner(t *testing.T) {
	cfg := cfgOf(t,
		"a", Profile{
			Env:      map[string]string{"K": "from-a"},
			Comments: map[string]string{"K": "note a"},
		},
		"b", Profile{
			Env:      map[string]string{"K": "from-b"},
			Comments: map[string]string{"K": "note b"},
		},
	)
	r, err := cfg.ResolveProfiles([]string{"a", "b"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if src := r.Sources["K"]; src.Profile != "b" {
		t.Fatalf("source profile = %q, want b", src.Profile)
	}
	if got := r.Comments["K"]; got != "note b" {
		t.Fatalf("comment = %q, want the winner's note", got)
	}
}

func TestResolveProfilesDuplicateNamesAreIdempotent(t *testing.T) {
	cfg := cfgOf(t,
		"a", Profile{Env: map[string]string{"K": "v"}},
	)
	once, err := cfg.ResolveProfiles([]string{"a"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	twice, err := cfg.ResolveProfiles([]string{"a", "a"})
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if once.Env["K"] != twice.Env["K"] || !slices.Equal(once.Chain, twice.Chain) {
		t.Fatalf("duplicate members must be idempotent: env %q/%q chain %v/%v", once.Env["K"], twice.Env["K"], once.Chain, twice.Chain)
	}
}

func TestResolveProfilesErrorsLikeASingleResolve(t *testing.T) {
	t.Run("cycle inside a member's own chain", func(t *testing.T) {
		cfg := cfgOf(t,
			"loop", Profile{Extends: Extends{"loop"}},
			"b", Profile{Env: map[string]string{"K": "v"}},
		)
		if _, err := cfg.ResolveProfiles([]string{"loop", "b"}); !errors.Is(err, ErrExtendsCycle) {
			t.Fatalf("err = %v, want ErrExtendsCycle", err)
		}
	})

	t.Run("unknown member name", func(t *testing.T) {
		cfg := cfgOf(t, "a", Profile{})
		_, err := cfg.ResolveProfiles([]string{"a", "ghost"})
		if err == nil || !strings.Contains(err.Error(), `profile "ghost" not found`) {
			t.Fatalf("err = %v, want the not-found message for ghost", err)
		}
	})
}

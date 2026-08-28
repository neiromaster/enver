package envname

import (
	"runtime"
	"testing"
)

// TestEqual pins the platform split at the comparison level: case variants
// denote one variable on Windows and are distinct names on POSIX.
func TestEqual(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !Equal("Path", "PATH") || !Equal("token", "TOKEN") {
			t.Fatal("windows Equal must fold case")
		}
		if Equal("Path", "Prefix") {
			t.Fatal("windows Equal matched different names")
		}
		return
	}
	if Equal("Path", "PATH") {
		t.Fatal("posix Equal must be byte-exact")
	}
	if !Equal("PATH", "PATH") {
		t.Fatal("posix Equal must match identical names")
	}
}

// TestHas pins presence lookup by Equal semantics against a map whose only
// entry is a case variant of the key.
func TestHas(t *testing.T) {
	m := map[string]int{"Token": 1}
	if runtime.GOOS == "windows" {
		if !Has(m, "TOKEN") {
			t.Fatal("windows Has must find the case variant")
		}
		if Has(m, "PREFIX") {
			t.Fatal("windows Has matched an unrelated name")
		}
		return
	}
	if Has(m, "TOKEN") {
		t.Fatal("posix Has must not fold case")
	}
	if !Has(m, "Token") {
		t.Fatal("posix Has must find the exact key")
	}
}

// TestMatchesAny pins fence-list matching: the list entries are the unset
// spellings and the key an env name, matched by Equal in both directions.
func TestMatchesAny(t *testing.T) {
	list := []string{"Token", "OTHER"}
	if runtime.GOOS == "windows" {
		if !MatchesAny(list, "TOKEN") {
			t.Fatal("windows MatchesAny must fold case in the entry")
		}
		if !MatchesAny([]string{"token"}, "TOKEN") {
			t.Fatal("windows MatchesAny must fold case in the key")
		}
		if MatchesAny(list, "PREFIX") {
			t.Fatal("windows MatchesAny matched an unrelated name")
		}
		return
	}
	if MatchesAny(list, "TOKEN") {
		t.Fatal("posix MatchesAny must be byte-exact")
	}
	if !MatchesAny(list, "Token") {
		t.Fatal("posix MatchesAny must match the exact entry")
	}
	if MatchesAny(nil, "Token") {
		t.Fatal("an empty fence matches nothing")
	}
}

// TestValid pins the identifier rule shared by hand-authored YAML and
// imported .env files: [A-Za-z_][A-Za-z0-9_]*.
func TestValid(t *testing.T) {
	for _, k := range []string{"A", "_X1", "aB_c9"} {
		if !Valid(k) {
			t.Fatalf("Valid(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"1A", "A-B", "", "a.b", "has space"} {
		if Valid(k) {
			t.Fatalf("Valid(%q) = true, want false", k)
		}
	}
}

// TestGetPlatformSemantics pins the case-variant scan: on Windows Token and
// TOKEN denote one variable, on POSIX they are distinct names.
func TestGetPlatformSemantics(t *testing.T) {
	m := map[string]string{"Token": "local"}
	got, found := Get(m, "TOKEN")
	if runtime.GOOS == "windows" {
		if !found || got != "local" {
			t.Fatalf("windows Get = %q, %v; want local, true", got, found)
		}
		return
	}
	if found {
		t.Fatalf("posix Get = %q, %v; want zero, false", got, found)
	}
}

// TestSetAndDeleteAreShapeGeneric pins both properties at once: Set and Delete
// work on non-string maps, and on Windows they collapse case variants exactly
// as they do for string maps. POSIX keeps both spellings — there PATH and Path
// are distinct variables — so Delete by one spelling must not touch the other.
func TestSetAndDeleteAreShapeGeneric(t *testing.T) {
	sources := map[string]source{"Path": {Layer: "global"}}
	Set(sources, "PATH", source{Layer: "local"})
	if runtime.GOOS == "windows" {
		if len(sources) != 1 || sources["PATH"].Layer != "local" {
			t.Fatalf("Set on a non-string map = %v", sources)
		}
		Delete(sources, "path")
		if len(sources) != 0 {
			t.Fatalf("Delete on a non-string map left %v", sources)
		}
		return
	}
	if len(sources) != 2 || sources["PATH"].Layer != "local" || sources["Path"].Layer != "global" {
		t.Fatalf("Set on a non-string map = %v, want both spellings on POSIX", sources)
	}
	Delete(sources, "path")
	if len(sources) != 2 || sources["PATH"].Layer != "local" {
		t.Fatalf("Delete on a non-string map removed a case variant: %v", sources)
	}
}

type source struct{ Layer string }

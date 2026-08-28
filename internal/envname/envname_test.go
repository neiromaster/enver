package envname

import (
	"runtime"
	"testing"
)

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

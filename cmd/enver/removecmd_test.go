package main

import (
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestGuardRemovable(t *testing.T) {
	cfg := config.Config{
		Default: "anth",
		Profiles: map[string]config.Profile{
			"anth":  {},
			"local": {Extends: config.Extends{"anth"}},
		},
	}
	if err := guardRemovable(cfg, "missing"); err == nil {
		t.Fatal("missing profile should be refused")
	}
	if err := guardRemovable(cfg, "anth"); err == nil || !strings.Contains(err.Error(), "extended by") {
		t.Fatalf("default+extended profile should be refused with dependents: %v", err)
	}
	if err := guardRemovable(cfg, "local"); err != nil {
		t.Fatalf("removable profile refused: %v", err)
	}
}

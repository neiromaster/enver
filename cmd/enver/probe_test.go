package main

import (
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestExtendsCycles(t *testing.T) {
	// dev -> base -> dev is a cycle; dev -> ci is not.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Extends: config.Extends{"dev"}, Env: map[string]string{"A": "1"}},
		"ci":   {Env: map[string]string{"B": "2"}},
	}}
	if !extendsCycles(cfg, "dev", config.Extends{"base"}) {
		t.Fatal("dev extending base, where base extends dev, must report a cycle")
	}
	if extendsCycles(cfg, "dev", config.Extends{"ci"}) {
		t.Fatal("dev extending ci is acyclic")
	}
	if extendsCycles(cfg, "dev", nil) {
		t.Fatal("no extends cannot cycle")
	}
	// A dangling parent fails resolution but is not a cycle — the pick stays
	// allowed and shows up as an external row instead.
	if extendsCycles(cfg, "dev", config.Extends{"ghost"}) {
		t.Fatal("dangling parent must not be reported as a cycle")
	}
}

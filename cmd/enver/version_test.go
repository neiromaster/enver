package main

import (
	"runtime/debug"
	"testing"
)

func TestFormatVersion(t *testing.T) {
	cases := []struct {
		version, commit, date, want string
	}{
		{"0.1.1", "", "", "0.1.1"},
		{"0.1.1", "abc1234", "", "0.1.1 (abc1234)"},
		{"0.1.1", "", "2026-08-04", "0.1.1 (2026-08-04)"},
		{"0.1.1", "abc1234", "2026-08-04", "0.1.1 (abc1234, 2026-08-04)"},
		{"v0.2.0", "deadbeef", "2026-08-04T12:00:00Z", "v0.2.0 (deadbeef, 2026-08-04T12:00:00Z)"},
		{"dev", "", "", "dev"},
	}
	for _, c := range cases {
		got := formatVersion(c.version, c.commit, c.date)
		if got != c.want {
			t.Errorf("formatVersion(%q, %q, %q) = %q, want %q",
				c.version, c.commit, c.date, got, c.want)
		}
	}
}

func TestResolveFromBuildInfo(t *testing.T) {
	cases := []struct {
		name                string
		bi                  *debug.BuildInfo
		wantV, wantC, wantD string
	}{
		{
			"go install from tagged module (no vcs in proxy build)",
			&debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}},
			"v1.2.3", "", "",
		},
		{
			"local go build at a tag",
			&debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.time", Value: "2026-08-04T12:00:00Z"},
				},
			},
			"v1.2.3", "abc123", "2026-08-04T12:00:00Z",
		},
		{
			"local dev build between tags ((devel))",
			&debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.time", Value: "2026-08-04T12:00:00Z"},
				},
			},
			"", "abc123", "2026-08-04T12:00:00Z",
		},
		{"nil build info", nil, "", "", ""},
	}
	for _, c := range cases {
		gotV, gotC, gotD := resolveFromBuildInfo(c.bi)
		if gotV != c.wantV || gotC != c.wantC || gotD != c.wantD {
			t.Errorf("%s: resolveFromBuildInfo = (%q, %q, %q), want (%q, %q, %q)",
				c.name, gotV, gotC, gotD, c.wantV, c.wantC, c.wantD)
		}
	}
}

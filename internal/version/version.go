// Package version holds build metadata injected via ldflags, with a fallback
// to debug.BuildInfo for `go install` and development builds.
package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String renders Version with optional commit/date metadata. When Version is
// "dev", it falls back to the build's VCS info.
func String() string {
	v, c, d := Version, Commit, Date
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bv, bc, bd := resolveFromBuildInfo(bi); bv != "" {
				v, c, d = bv, bc, bd
			}
		}
	}
	return formatVersion(v, c, d)
}

func formatVersion(version, commit, date string) string {
	meta := make([]string, 0, 2)
	if commit != "" {
		meta = append(meta, commit)
	}
	if date != "" {
		meta = append(meta, date)
	}
	if len(meta) == 0 {
		return version
	}
	return version + " (" + strings.Join(meta, ", ") + ")"
}

func buildSetting(bi *debug.BuildInfo, key string) string {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

func resolveFromBuildInfo(bi *debug.BuildInfo) (string, string, string) {
	if bi == nil {
		return "", "", ""
	}
	v := ""
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		v = mv
	}
	return v, buildSetting(bi, "vcs.revision"), buildSetting(bi, "vcs.time")
}

package main

import (
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestDedupIssuesSortedByFileProfileKind(t *testing.T) {
	out := dedupIssues([]config.Issue{
		{File: "b.yaml", Profile: "a", Kind: config.KindCycle},
		{File: "c.yaml", Profile: "b", Kind: config.KindDanglingExtends},
		{File: "a.yaml", Profile: "a", Kind: config.KindDanglingExtends},
		{File: "a.yaml", Profile: "b", Kind: config.KindCycle},
	})
	// Current sort order: by File, then Profile, then Kind
	// "cycle" < "dangling-extends" alphabetically
	want := []struct {
		file, profile string
		kind          config.IssueKind
	}{
		{"a.yaml", "a", config.KindDanglingExtends},
		{"a.yaml", "b", config.KindCycle},
		{"b.yaml", "a", config.KindCycle},
		{"c.yaml", "b", config.KindDanglingExtends},
	}
	for i, w := range want {
		if out[i].File != w.file || out[i].Profile != w.profile || out[i].Kind != w.kind {
			t.Fatalf("out[%d] = %s/%s/%s, want %s/%s/%s", i, out[i].File, out[i].Profile, out[i].Kind, w.file, w.profile, w.kind)
		}
	}
}

package app

import (
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// projectedRemote lands one record's content in a temporary remotes directory and
// returns its projected layer, labelled remote:<name>.
func projectedRemote(t *testing.T, name, content string) config.Layer {
	t.Helper()
	dir := t.TempDir()
	prev := config.RemotesDir
	config.RemotesDir = func() string { return dir }
	t.Cleanup(func() { config.RemotesDir = prev })

	if err := config.WriteRemoteContent(name, []byte(content)); err != nil {
		t.Fatalf("write remote content: %v", err)
	}
	layers, warnings, err := config.RemoteLayers(map[string]config.RemoteRecord{
		name: {URL: "https://example.com/enver.yaml"},
	})
	if err != nil {
		t.Fatalf("remote layers: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("fixture raised warnings: %v", warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %d, want one", len(layers))
	}
	if !config.IsRemoteLayer(layers[0].Label) {
		t.Fatalf("label = %q, want a remote layer", layers[0].Label)
	}
	return layers[0]
}

// TestWriteMarksRemoteUnderStack pins the write-side mark rule across three
// layers: a mark a remote's content supplies reaches a patch written into the
// user layer, the same-name copy of a remote profile keeps it, and a nearer
// unmarked mention still clears it — nearest mention wins, not "remote always
// loses" and not "any mention marks".
func TestWriteMarksRemoteUnderStack(t *testing.T) {
	remote := projectedRemote(t, "llm", "profiles:\n  base:\n    env:\n      ~GH_TOKEN: remote-secret\n      PLAIN: remote\n")
	global := config.Layer{Label: config.LayerGlobal, Config: config.Config{}}
	keys := []string{"GH_TOKEN", "PLAIN"}

	query := WriteQuery{
		Under:   []config.Layer{global, remote},
		Target:  config.Config{},
		Name:    "work",
		Extends: config.Extends{"llm/base"},
		Keys:    keys,
	}
	marks := WriteMarks(query)
	if !marks["GH_TOKEN"] {
		t.Fatalf("a mark inherited from remote content must reach the write: %#v", marks)
	}
	if marks["PLAIN"] {
		t.Fatalf("a key nothing marks must stay plain: %#v", marks)
	}

	// The patch bootstrap case: the user's copy of a remote profile declares the
	// key, and the remote's own copy is what marks it.
	query.Name = "llm/base"
	query.Extends = nil
	if marks := WriteMarks(query); !marks["GH_TOKEN"] {
		t.Fatalf("a patch over a marked remote profile must keep the mark: %#v", marks)
	}

	// A global copy of the same profile folds over the remote one, so its plain
	// entry is the nearer mention and clears the mark the remote set.
	query.Under = []config.Layer{
		{Label: config.LayerGlobal, Config: config.Config{Profiles: map[string]config.Profile{
			"llm/base": {Env: map[string]string{"GH_TOKEN": "user"}},
		}}},
		remote,
	}
	if marks := WriteMarks(query); marks["GH_TOKEN"] {
		t.Fatalf("a nearer unmarked mention must clear the remote's mark: %#v", marks)
	}

	// Nothing under the target means no view: the writer keeps its own in-file
	// fallback rather than treating an unknown chain as unmarked.
	if marks := WriteMarks(WriteQuery{Target: config.Config{}, Name: "work", Keys: keys}); marks != nil {
		t.Fatalf("an empty stack must answer nil, got %#v", marks)
	}
}

// TestWriteMarksRemoteFenceEra pins that each stacked layer keeps the era it was
// declared in: a fence a remote declared rides along as a carried fence stamped
// with the remote layer, so it bites remote-era values and goes silent under the
// user's own closer mention — an era-less fold would misfire in both directions.
func TestWriteMarksRemoteFenceEra(t *testing.T) {
	remote := projectedRemote(t, "llm", "profiles:\n  base:\n    unset: [GH_TOKEN]\n    env:\n      ~GH_TOKEN: remote-secret\n")
	global := config.Layer{Label: config.LayerGlobal, Config: config.Config{}}

	// The remote copy unsets the key it also defines: the fence wins in its own
	// era, so the key resolves unset and nothing is marked.
	query := WriteQuery{
		Under:   []config.Layer{global, remote},
		Target:  config.Config{},
		Name:    "work",
		Extends: config.Extends{"llm/base"},
		Keys:    []string{"GH_TOKEN"},
	}
	if marks := WriteMarks(query); marks["GH_TOKEN"] {
		t.Fatalf("a remote fence must strip the key it fences: %#v", marks)
	}

	// The same stack with the remote's own env left untouched but the fence
	// declared in the remote layer under a profile the write extends: the
	// global layer's plain mention outranks it, exactly as a global fence gates
	// a local redefinition.
	remote = projectedRemote(t, "llm", "profiles:\n  base:\n    unset: [GH_TOKEN]\n")
	query.Under = []config.Layer{
		{Label: config.LayerGlobal, Config: config.Config{Profiles: map[string]config.Profile{
			"llm/base": {Env: map[string]string{"GH_TOKEN": "user"}, Marked: map[string]bool{"GH_TOKEN": true}},
		}}},
		remote,
	}
	if marks := WriteMarks(query); !marks["GH_TOKEN"] {
		t.Fatalf("a user-layer mention must outrank a remote fence: %#v", marks)
	}
}

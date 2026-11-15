package source

import "testing"

// The zero sourceKind is no form, so a source that was never parsed cannot
// resolve into a github URL.
func TestUnparsedSourceDoesNotResolve(t *testing.T) {
	var zero source
	url, ref, err := zero.resolve("")
	if err == nil {
		t.Fatalf("zero source resolved to (%q, %q), want an error", url, ref)
	}
}

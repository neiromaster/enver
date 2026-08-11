package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameProfileUpdatesMultiParentExtends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlContent := `profiles:
  base:
    env:
      K: v
  child1:
    env:
      A: 1
  child2:
    env:
      B: 2
  multi:
    extends: [base, child1, child2]
    env:
      C: 3
`
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rename child1 to child1_renamed
	if err := RenameProfile(path, "child1", "child1_renamed"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Check that multi's extends was updated
	if !strings.Contains(content, "child1_renamed") {
		t.Fatalf("multi's extends not updated:\n%s", content)
	}

	if strings.Contains(content, "extends: [base, child1, child2]") {
		t.Fatalf("extends not updated, still has old child1:\n%s", content)
	}
}

package engdocs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRootFindsModuleRoot(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("RepoRoot() = %q without go.mod: %v", root, err)
	}
	if name := filepath.Base(root); name != "ragflow-x" {
		t.Fatalf("RepoRoot() = %q, want the ragflow-x module root", root)
	}
}

func TestPresentTracksEngineeringDocTree(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	if !Present(root) {
		t.Skipf("engineering doc tree absent in this checkout: %s", SkipReason)
	}
	// A directory without doc/develop must never be reported as present, so the
	// gates cannot pass by accident on a stripped checkout.
	if Present(t.TempDir()) {
		t.Fatal("Present() = true for a directory without doc/develop")
	}
}

func TestSkipReasonNamesTheBoundary(t *testing.T) {
	if SkipReason == "" {
		t.Fatal("SkipReason must explain the doc git boundary")
	}
}

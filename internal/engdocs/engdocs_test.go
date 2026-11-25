package engdocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wantModulePath is what the repository's go.mod declares. It is the property the
// root is supposed to have; the directory *name* is not.
const wantModulePath = "github.com/ragflow-x/ragflow-x"

func TestRepoRootFindsModuleRoot(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("RepoRoot() = %q without go.mod: %v", root, err)
	}
	// This used to assert filepath.Base(root) == "ragflow-x". That pinned the developer's
	// habit of naming the clone directory, not a property of the tree: a git worktree at any
	// other path, a CI cache directory, a copy under /tmp/verify — all of them are the same
	// repository, and all of them went red here for a reason no diff could explain (doc/304
	// §4 E115, where a worktree at E:/tmpgo/s21wt turned s07.go-test-all red). Which go.mod
	// this is, is the part that matters: a nested or vendored module must not be mistaken for
	// the repository root.
	if got := modulePath(t, filepath.Join(root, "go.mod")); got != wantModulePath {
		t.Fatalf("RepoRoot() = %q declares module %q, want %q", root, got, wantModulePath)
	}
}

// modulePath is the `module` directive of a go.mod, which is what identifies the repository
// independently of where it happens to be checked out.
func modulePath(t *testing.T, gomod string) string {
	t.Helper()
	data, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatalf("read %s: %v", gomod, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"")
		}
	}
	t.Fatalf("no module directive in %s", gomod)
	return ""
}

// The assertion above has to be able to refuse something. A tree whose go.mod declares a
// different module is *reachable* by RepoRoot() (it walks up to the nearest go.mod), and this
// case pins that it is not therefore this repository's root — i.e. the check discriminates on
// the module path and not on the shape of the path or its last segment.
func TestAForeignModuleIsNotThisRoot(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gomod, []byte("module example.com/not-ragflow-x\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", gomod, err)
	}
	t.Chdir(dir)

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	if got := modulePath(t, filepath.Join(root, "go.mod")); got == wantModulePath {
		t.Fatalf("RepoRoot() = %q reported this repository's module for a foreign tree", root)
	}
	// And the same tree, if someone had renamed the directory, is still not this root: the
	// name proves nothing either way.
	if filepath.Base(root) == "ragflow-x" {
		t.Fatalf("test setup is worthless: the temporary tree is already named ragflow-x (%q)", root)
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

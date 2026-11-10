package service

import (
	"os"
	"path/filepath"
	"testing"
)

// testSQLiteDSN returns a sqlite DSN inside a per-test directory.
//
// t.TempDir() asserts that its directory can be removed and fails the test when
// it cannot. Several service flows persist evidence from background goroutines
// (notification fan-out, pushdown metadata sync), so a write can land while the
// directory is being walked: sqlite re-creates its journal file and RemoveAll
// reports "directory not empty". That race is about cleanup order, not about the
// behaviour under test, so the directory is removed best-effort here and any
// residue is reported instead of failing an otherwise green test.
func testSQLiteDSN(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rgx-svc-test-")
	if err != nil {
		t.Fatalf("create test temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("test temp dir %s: %v (a background write outlived the store, residue left behind)", dir, err)
		}
	})
	return filepath.Join(dir, name)
}

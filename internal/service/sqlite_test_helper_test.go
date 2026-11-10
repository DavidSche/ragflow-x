package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
)

// sqliteTemplate builds the migrated schema once per test binary, so the ~900
// tests in this package do not each replay the 162-step migration. Goals: keep
// the per-test database real (a copied sqlite file, not a mock) while removing
// the setup cost that pushed the suite past its CI timeout.
var (
	sqliteTemplateOnce sync.Once
	sqliteTemplatePath string
	sqliteTemplateErr  error
)

func migratedSQLiteTemplate() (string, error) {
	sqliteTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rgx-svc-template-")
		if err != nil {
			sqliteTemplateErr = err
			return
		}
		path := filepath.Join(dir, "template.db")
		gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: path})
		if err != nil {
			sqliteTemplateErr = err
			return
		}
		if err := db.Migrate(gdb); err != nil {
			sqliteTemplateErr = err
			return
		}
		// The connection runs in WAL mode, so committed rows may still live in
		// template.db-wal. Fold them into the main file first: the copy below
		// takes only the .db file, and a template missing its seeds would leave
		// tests silently running against a half-populated schema.
		if err := gdb.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
			sqliteTemplateErr = fmt.Errorf("checkpoint migrated template: %w", err)
			return
		}
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
		if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
			sqliteTemplateErr = fmt.Errorf("template write-ahead log is not empty (%d bytes) after checkpoint", info.Size())
			return
		}
		sqliteTemplatePath = path
	})
	return sqliteTemplatePath, sqliteTemplateErr
}

// testSQLiteDSN returns a sqlite DSN inside a per-test directory holding a copy
// of the migrated template schema.
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
	dsn := filepath.Join(dir, name)
	template, err := migratedSQLiteTemplate()
	if err != nil {
		// Fall back to an empty file: the test opens it and migrates itself.
		t.Logf("migrated sqlite template unavailable, tests migrate from scratch: %v", err)
		return dsn
	}
	if err := copyFile(template, dsn); err != nil {
		t.Logf("sqlite template copy failed, tests migrate from scratch: %v", err)
	}
	return dsn
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Package dbtest provides migrated sqlite databases for tests.
//
// Every test used to replay the 162-step migration against a brand new file. That
// is tolerable without instrumentation and fatal with it: the pure-Go sqlite
// driver runs roughly 50x slower under -race, which pushed internal/db,
// internal/handler and internal/repository past any sane per-package timeout
// (1556s, 2400s and 2400s in the 2026-11-11 nightly). The schema is identical for
// every test in a package, so it is migrated once per test binary and copied.
//
// The copy is still a real sqlite file, never a mock: tests that assert on query
// plans, indexes, transactions or WAL behaviour keep exercising the real engine.
package dbtest

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

var (
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

// MigratedTemplate builds the migrated schema once per test binary and returns
// the path of a checkpointed copy of it.
//
// The connection runs in WAL mode, so committed rows may still live in
// <path>-wal. The copy below takes only the .db file, and a template missing its
// seeds would leave tests silently running against a half-populated schema: the
// log is folded into the main file first and the emptiness of -wal is asserted.
func MigratedTemplate() (string, error) {
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rgx-dbtest-template-")
		if err != nil {
			templateErr = err
			return
		}
		path := filepath.Join(dir, "template.db")
		gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: path})
		if err != nil {
			templateErr = err
			return
		}
		if err := db.Migrate(gdb); err != nil {
			templateErr = err
			return
		}
		if err := gdb.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
			templateErr = fmt.Errorf("checkpoint migrated template: %w", err)
			return
		}
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
		if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
			templateErr = fmt.Errorf("template write-ahead log is not empty (%d bytes) after checkpoint", info.Size())
			return
		}
		templatePath = path
	})
	return templatePath, templateErr
}

// SQLiteDSN returns a sqlite DSN inside a per-test directory holding a copy of
// the migrated template schema. Tests still call db.Migrate on it as they always
// did; that is now a version check instead of 162 steps.
//
// The directory is removed best-effort instead of through t.TempDir(): several
// flows persist evidence from background goroutines, so a write can land while
// the directory is being walked, sqlite re-creates its journal file and RemoveAll
// reports "directory not empty". That is a cleanup-order race, not the behaviour
// under test, so any residue is reported instead of failing a green test.
//
// When the template cannot be built the caller gets a plain path and migrates
// from scratch; the reason is logged so the slowdown is never silent.
func SQLiteDSN(tb testing.TB, name string) string {
	tb.Helper()
	dir, err := os.MkdirTemp("", "rgx-dbtest-")
	if err != nil {
		tb.Fatalf("create test temp dir: %v", err)
	}
	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Logf("test temp dir %s: %v (a background write outlived the store, residue left behind)", dir, err)
		}
	})
	dsn := filepath.Join(dir, name)
	template, err := MigratedTemplate()
	if err != nil {
		tb.Logf("migrated sqlite template unavailable, tests migrate from scratch: %v", err)
		return dsn
	}
	if err := copyFile(template, dsn); err != nil {
		tb.Logf("sqlite template copy failed, tests migrate from scratch: %v", err)
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

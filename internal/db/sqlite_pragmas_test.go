package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// The package comment advertised WAL while the pragma list did not contain it,
// so every sqlite commit paid for a rollback-journal fsync (~200ms per write on
// the Windows CI runner). This guard keeps "documented" and "applied" equal.
func TestSQLiteDefaultsUseWAL(t *testing.T) {
	dsn, err := withSQLitePragmas(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
	})
	var mode string
	if err := gdb.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// Caller-supplied pragmas win, so a deployment can still pin a different
// journal mode without the defaults fighting it.
func TestSQLiteExplicitJournalModeWins(t *testing.T) {
	dsn, err := withSQLitePragmas(filepath.Join(t.TempDir(), "del.db") + "?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
	})
	var mode string
	if err := gdb.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "delete" {
		t.Fatalf("journal_mode = %q, want delete (explicit pragma must win)", mode)
	}
}

// In-memory databases cannot use WAL; asking for it would fail every
// connection open, so the default is skipped there.
func TestSQLiteMemoryDSNSkipsWAL(t *testing.T) {
	dsn, err := withSQLitePragmas("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() {
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
	})
	if err := gdb.Exec("CREATE TABLE wal_probe (id TEXT)").Error; err != nil {
		t.Fatalf("in-memory sqlite unusable: %v", err)
	}
	var mode string
	if err := gdb.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode == "wal" {
		t.Fatalf("journal_mode = %q, in-memory databases must not claim WAL", mode)
	}
}

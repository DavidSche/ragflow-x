package db

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// The schema in these tests is the same for every case, and replaying all 162
// migration steps per test costs 26 minutes once the pure-Go sqlite driver runs
// under -race (nightly 2026-11-11: internal/db took 1556s). Build it once per
// test binary and hand out copies instead.
//
// A package-local helper (rather than internal/db/dbtest) is required: dbtest
// imports this package, so using it from package db's own tests would be an
// import cycle.
var (
	testTemplateOnce sync.Once
	testTemplatePath string
	testTemplateErr  error
)

func migratedSQLiteTemplate() (string, error) {
	testTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rgx-db-template-")
		if err != nil {
			testTemplateErr = err
			return
		}
		path := filepath.Join(dir, "template.db")
		gdb, err := Open(config.Database{Driver: "sqlite", DSN: path})
		if err != nil {
			testTemplateErr = err
			return
		}
		if err := Migrate(gdb); err != nil {
			testTemplateErr = err
			return
		}
		// The connection runs in WAL mode, so committed rows may still live in
		// <path>-wal. The copy takes only the .db file, and a template missing
		// its seeds would leave tests running against a half-populated schema.
		if err := gdb.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
			testTemplateErr = fmt.Errorf("checkpoint migrated template: %w", err)
			return
		}
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
		if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
			testTemplateErr = fmt.Errorf("template write-ahead log is not empty (%d bytes) after checkpoint", info.Size())
			return
		}
		testTemplatePath = path
	})
	return testTemplatePath, testTemplateErr
}

// migratedTestDSN returns a per-test sqlite DSN holding a copy of the migrated
// schema. Callers still run Migrate on it as before; that is a version check now.
//
// Bound: a test that must observe a migration *happening* cannot start from the
// migrated schema. Three shapes need a fresh file (keep
// filepath.Join(t.TempDir(), ...) there): seeding rows that an early step
// backfills, seeding rows that a later step's constraint would reject, and
// MigrateTo targets below LatestMigrationVersion. Such tests fail loudly, not
// silently, when templated — that is how the template rollout found them.
//
// The directory is removed best-effort instead of through t.TempDir(), because a
// late write from a store finaliser can recreate the journal file mid-removal.
// When the template cannot be built the caller migrates from scratch, and the
// reason is logged so the slowdown is never silent.
func migratedTestDSN(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rgx-db-test-")
	if err != nil {
		t.Fatalf("create test temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("test temp dir %s: %v (residue left behind)", dir, err)
		}
	})
	dsn := filepath.Join(dir, name)
	template, err := migratedSQLiteTemplate()
	if err != nil {
		t.Logf("migrated sqlite template unavailable, test migrates from scratch: %v", err)
		return dsn
	}
	if err := copyTemplateFile(template, dsn); err != nil {
		t.Logf("sqlite template copy failed, test migrates from scratch: %v", err)
	}
	return dsn
}

func copyTemplateFile(src, dst string) error {
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

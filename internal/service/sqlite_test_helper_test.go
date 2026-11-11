package service

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
)

// testSQLiteDSN returns a sqlite DSN inside a per-test directory holding a copy
// of the migrated schema, so the ~900 tests in this package do not each replay
// the 162-step migration. The template lives in internal/db/dbtest because
// internal/handler, internal/repository and internal/router need exactly the
// same thing, and three copies of "migrate once, copy per test" would drift.
//
// The copy is still a real sqlite file, never a mock, and the directory is
// removed best-effort: several service flows persist evidence from background
// goroutines, so a write can land while the directory is walked and sqlite
// re-creates its journal file. That is cleanup order, not behaviour.
func testSQLiteDSN(t *testing.T, name string) string {
	t.Helper()
	return dbtest.SQLiteDSN(t, name)
}

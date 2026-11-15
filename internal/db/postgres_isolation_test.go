package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

// isolatedPostgresDB opens a fresh, per-test PostgreSQL database and returns a
// migrated-ready gorm handle pointed at it, registering cleanup so the database
// is dropped when the test finishes.
//
// Why per-test isolation: several PostgreSQL contract tests used to connect to
// the shared RGX_TEST_POSTGRES_DSN base database directly and mutate it
// (Migrate + seeded business rows). Under `go test -shuffle=on` the leftover
// schema/data from one test surfaced in another — e.g. the release-candidate
// pagination contract failed in 0.01s because the shared base had no/lost
// schema, and migration-invariant tests hit UNIQUE collisions against rows a
// sibling left behind. Each test now owns its database, so no test can observe
// another's mutation regardless of visit order (doc/283 §2).
//
// The handle is returned open; callers must still run Migrate/MigrateTo as they
// did before. Cleanup order: database drop happens via t.Cleanup (LIFO), after
// the gorm handle is closed.
func isolatedPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	baseDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if strings.TrimSpace(baseDSN) == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL contract")
	}
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping postgres admin: %v", err)
	}
	name := strings.ToLower(fmt.Sprintf("rgx_db_contract_%x", time.Now().UnixNano()))
	if err := createPostgresDatabase(admin, name, ""); err != nil {
		t.Fatalf("create isolated postgres database %s: %v", name, err)
	}
	gdb, err := openPostgresContractDB(swapPostgresDatabase(baseDSN, name))
	if err != nil {
		_ = dropPostgresDatabase(admin, name)
		t.Fatalf("open isolated postgres database %s: %v", name, err)
	}
	t.Cleanup(func() { _ = closePostgresContractDB(gdb) })
	t.Cleanup(func() { _ = dropPostgresDatabase(admin, name) })
	return gdb
}

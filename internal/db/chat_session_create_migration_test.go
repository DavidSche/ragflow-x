package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Migration 191 moves "start a chat thread" off `chat:manage` and onto its own
// action (doc/304 §4 E155). The upgrade path is the part worth testing: the
// built-in rows are reconciled from the canonical matrix, so a store that was
// already migrated by older code only gains the grant if a later migration runs
// again — a code-only change would leave every deployed database without it, and
// the failure would surface as a 403 at runtime.
//
// Two properties, and the second one is the reason this test is not just
// "the grant exists":
//
//   - the five conversing built-in roles gain `session:create` on `chat` when the
//     migration runs — a code-only change would leave every deployed database
//     without it, and the failure would look like a 403 at runtime;
//   - `viewer` does not. A read-only role must not silently acquire the ability
//     to write a new thread; that is exactly the boundary §4 E155 claims.
func TestMigrationV191GrantsChatSessionCreate(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/chat-session-create.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := MigrateTo(gdb, 190); err != nil {
		t.Fatalf("migrate to 190: %v", err)
	}

	// A deployed store at 190 was reconciled by the code that shipped then, i.e.
	// before this grant existed. MigrateTo runs the current code, so the state has
	// to be modelled explicitly: drop the rows, then require 191 to put them back.
	// Without this step the assertion below would be about the Go matrix, not about
	// the migration.
	if err := gdb.Where("action = ? AND resource = ?", "session:create", "chat").
		Delete(&model.Permission{}).Error; err != nil {
		t.Fatalf("seed pre-191 store: %v", err)
	}

	// The row must not be there at 190, or this test asserts a state the migration
	// did not produce.
	var before int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleBusinessUser, "session:create", "chat").
		Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("business_user still has chat session:create at 190 (%d rows): the seed did not take", before)
	}

	if err := MigrateTo(gdb, 191); err != nil {
		t.Fatalf("migrate to 191: %v", err)
	}
	if LatestMigrationVersion() < 191 {
		t.Fatalf("latest migration = %d, want at least 191", LatestMigrationVersion())
	}

	for _, role := range []string{
		model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator,
		model.RoleBusinessUser, model.RoleTeamAdmin,
	} {
		var count int64
		if err := gdb.Model(&model.Permission{}).
			Where("role_id = ? AND action = ? AND resource = ?", role, "session:create", "chat").
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s has %d rows for chat session:create, want 1", role, count)
		}
	}

	var viewer int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleViewer, "session:create", "chat").
		Count(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if viewer != 0 {
		t.Fatalf("viewer gained chat session:create (%d rows): the read-only role must stay read-only", viewer)
	}
}

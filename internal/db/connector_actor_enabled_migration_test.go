package db

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// Milestone 225 follow-up: migration 155 must preserve legacy operator intent
// when splitting the overloaded status column into enabled + runtime status.
func TestConnectorActorEnabledMigration(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "connector-actor.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := MigrateTo(gdb, 154); err != nil {
		t.Fatalf("migrate to 154: %v", err)
	}
	insert := func(id, status string, enabled bool) {
		t.Helper()
		if err := gdb.Exec(`INSERT INTO rgx_connector
			(id, tenant_id, name, provider, target_dataset_id, spec_json, credential_crypted,
			 has_credential, cursor_json, schedule_kind, schedule_value, schedule_tz_offset_min,
			 sensitive, enabled, status, last_run_status, created_by)
			VALUES (?, 'tenant-a', ?, 'local_folder', 'ds-1', '{}', '', 0, '{}',
			        'manual', 0, 480, 0, ?, ?, '', 'legacy')`,
			id, id, enabled, status).Error; err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	insert("legacy-disabled", model.ConnectorStatusDisabled, false)
	insert("legacy-degraded", model.ConnectorStatusDegraded, false)

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	for id, want := range map[string]bool{
		"legacy-disabled": false,
		"legacy-degraded": true,
	} {
		var got model.Connector
		if err := gdb.First(&got, "id = ?", id).Error; err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got.Enabled != want {
			t.Fatalf("%s enabled = %v, want %v", id, got.Enabled, want)
		}
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 155 must be idempotent: %v", err)
	}
}

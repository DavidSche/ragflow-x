package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

func TestBuiltinRBACReconciliationMigrationBackfillsExistingDatabase(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rbac.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	if err := gdb.AutoMigrate(&model.Tenant{}, &model.Role{}, &model.Permission{}, &model.UserRole{}, &model.SchemaVersion{}); err != nil {
		t.Fatalf("create pre-upgrade tables: %v", err)
	}
	tenant := &model.Tenant{ID: id.New(), Name: "Existing Tenant", Type: model.TenantTypeWorkspace}
	if err := gdb.Create(tenant).Error; err != nil {
		t.Fatalf("create existing tenant: %v", err)
	}
	if err := gdb.Create(&model.SchemaVersion{Version: 127, AppliedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("mark pre-upgrade schema: %v", err)
	}
	stalePermission := &model.Permission{ID: id.New(), RoleID: model.RoleTenantAdmin, Action: "read", Resource: "legacy-resource", Effect: model.PermissionEffectAllow}
	if err := gdb.Create(stalePermission).Error; err != nil {
		t.Fatalf("create stale grant: %v", err)
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var grantCount int64
	if err := gdb.Model(&model.Permission{}).Count(&grantCount).Error; err != nil {
		t.Fatalf("count canonical grants: %v", err)
	}
	unique := map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		unique[grant.Role+"|"+grant.Action+"|"+grant.Resource] = true
	}
	if want := int64(len(unique)); grantCount != want {
		t.Fatalf("canonical grants = %d, want %d", grantCount, want)
	}
	for _, grant := range BuiltinPermissionMatrix() {
		var count int64
		if err := gdb.Model(&model.Permission{}).
			Where("role_id = ? AND resource = ? AND action = ? AND effect = ?",
				grant.Role, grant.Resource, grant.Action, model.PermissionEffectAllow).
			Count(&count).Error; err != nil {
			t.Fatalf("count grant %s %s %s: %v", grant.Role, grant.Resource, grant.Action, err)
		}
		if count != 1 {
			t.Fatalf("grant %s %s %s count = %d, want 1", grant.Role, grant.Resource, grant.Action, count)
		}
	}
	var staleCount int64
	if err := gdb.Model(&model.Permission{}).
		Where("resource = ?", stalePermission.Resource).
		Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale grants: %v", err)
	}
	if staleCount != 0 {
		t.Fatalf("stale grant count = %d, want 0", staleCount)
	}
}

// TestLatestSchemaStartupReconcilesBuiltinRBAC guards the “startup
// reconciliation” contract: databases that are already at the latest schema
// version must still have the built-in permission matrix replaced wholesale on
// every boot, even when no migration callback fires. migrateTo reconciles RBAC
// unconditionally at the end of the real startup path (Migrate) so grants
// added to BuiltinPermissionMatrix() are applied immediately.
func TestLatestSchemaStartupReconcilesBuiltinRBAC(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rbac-latest.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	if err := gdb.AutoMigrate(&model.Tenant{}, &model.Role{}, &model.Permission{}, &model.UserRole{}, &model.Team{}, &model.SchemaVersion{}); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	// First boot applies every migration; afterwards the schema is fully at
	// the latest version (no pending migration records).
	if err := Migrate(gdb); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Tamper with the canonical matrix: drop one built-in grant and plant a
	// stale one, then boot again. With no pending migrations only the
	// unconditional startup reconciliation (migrateTo tail) can restore both.
	victim := &model.Permission{}
	if err := gdb.Where("role_id = ? AND resource = ? AND action = ?",
		model.RolePlatformAdmin, "system", "read").First(victim).Error; err != nil {
		t.Fatalf("locate canonical grant: %v", err)
	}
	if err := gdb.Delete(victim).Error; err != nil {
		t.Fatalf("delete canonical grant: %v", err)
	}
	stale := &model.Permission{ID: id.New(), RoleID: model.RoleTenantAdmin, Action: "read", Resource: "legacy-resource", Effect: model.PermissionEffectAllow}
	if err := gdb.Create(stale).Error; err != nil {
		t.Fatalf("create stale grant: %v", err)
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("second migrate on latest schema: %v", err)
	}

	// The deleted canonical grant must be restored.
	var restored int64
	if err := gdb.Model(&model.Permission{}).Where("role_id = ? AND resource = ? AND action = ? AND effect = ?",
		model.RolePlatformAdmin, "system", "read", model.PermissionEffectAllow).Count(&restored).Error; err != nil {
		t.Fatalf("count restored grant: %v", err)
	}
	if restored != 1 {
		t.Fatalf("canonical grant not restored by startup reconcile: count = %d, want 1", restored)
	}
	// And the stale grant must be gone (wholesale replace, idempotent).
	var staleCount int64
	if err := gdb.Model(&model.Permission{}).Where("resource = ?", stale.Resource).Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale grants: %v", err)
	}
	if staleCount != 0 {
		t.Fatalf("stale grant survived startup reconcile: count = %d, want 0", staleCount)
	}
}

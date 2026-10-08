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

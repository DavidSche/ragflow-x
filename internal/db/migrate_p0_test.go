package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-DBMIG-001
func TestP0_DBMIG_001_MigrationAndBuiltinRoleReconciliationAreIdempotent(t *testing.T) {
	gdb, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "idempotent-migration.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := Migrate(gdb); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	var initialSchema []model.SchemaVersion
	if err := gdb.Order("version").Find(&initialSchema).Error; err != nil {
		t.Fatal(err)
	}
	var initialRoles, initialPermissions, initialPlatformTenants int64
	if err := gdb.Model(&model.Role{}).Count(&initialRoles).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Permission{}).Count(&initialPermissions).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Tenant{}).Where("type = ?", model.TenantTypePlatform).Count(&initialPlatformTenants).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var repeatSchema []model.SchemaVersion
	if err := gdb.Order("version").Find(&repeatSchema).Error; err != nil {
		t.Fatal(err)
	}
	var repeatRoles, repeatPermissions, repeatPlatformTenants int64
	if err := gdb.Model(&model.Role{}).Count(&repeatRoles).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Permission{}).Count(&repeatPermissions).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Tenant{}).Where("type = ?", model.TenantTypePlatform).Count(&repeatPlatformTenants).Error; err != nil {
		t.Fatal(err)
	}

	if len(repeatSchema) != len(migrations) || len(initialSchema) != len(repeatSchema) {
		t.Fatalf("expected one schema version per migration, initial=%d repeat=%d migrations=%d", len(initialSchema), len(repeatSchema), len(migrations))
	}
	seen := make(map[int64]struct{}, len(repeatSchema))
	for _, version := range repeatSchema {
		if _, exists := seen[version.Version]; exists {
			t.Fatalf("duplicate schema version %d", version.Version)
		}
		seen[version.Version] = struct{}{}
	}
	if initialRoles != repeatRoles || initialPermissions != repeatPermissions {
		t.Fatalf("seed reconciliation duplicated state: roles %d -> %d, permissions %d -> %d", initialRoles, repeatRoles, initialPermissions, repeatPermissions)
	}
	if initialPlatformTenants != 1 || repeatPlatformTenants != 1 {
		t.Fatalf("expected one platform tenant, initial=%d repeat=%d", initialPlatformTenants, repeatPlatformTenants)
	}
}

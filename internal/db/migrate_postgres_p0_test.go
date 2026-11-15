package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-PG-001
func TestP0_PG_001_PostgresMigrationIsIdempotentUnderContract(t *testing.T) {
	gdb := isolatedPostgresDB(t)

	if err := Migrate(gdb); err != nil {
		t.Fatalf("initial postgres migration: %v", err)
	}
	var initialVersions []model.SchemaVersion
	if err := gdb.Order("version").Find(&initialVersions).Error; err != nil {
		t.Fatal(err)
	}
	var initialRoles, initialPermissions int64
	if err := gdb.Model(&model.Role{}).Count(&initialRoles).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Permission{}).Count(&initialPermissions).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("repeat postgres migration: %v", err)
	}
	var repeatVersions []model.SchemaVersion
	if err := gdb.Order("version").Find(&repeatVersions).Error; err != nil {
		t.Fatal(err)
	}
	var repeatRoles, repeatPermissions int64
	if err := gdb.Model(&model.Role{}).Count(&repeatRoles).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Permission{}).Count(&repeatPermissions).Error; err != nil {
		t.Fatal(err)
	}

	if len(initialVersions) != len(migrations) || len(repeatVersions) != len(migrations) {
		t.Fatalf("migration versions changed: initial=%d repeat=%d expected=%d", len(initialVersions), len(repeatVersions), len(migrations))
	}
	if initialRoles != repeatRoles || initialPermissions != repeatPermissions {
		t.Fatalf("postgres reconciliation duplicated state: roles %d -> %d, permissions %d -> %d", initialRoles, repeatRoles, initialPermissions, repeatPermissions)
	}
}

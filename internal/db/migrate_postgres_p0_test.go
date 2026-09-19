package db

import (
	"os"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ScenarioID: SC-PG-001
func TestP0_PG_001_PostgresMigrationIsIdempotentUnderContract(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL migration contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

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

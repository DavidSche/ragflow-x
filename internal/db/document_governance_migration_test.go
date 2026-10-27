package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestDocumentGovernanceMigrationCreatesTables(t *testing.T) {
	if LatestMigrationVersion() < 148 {
		t.Fatalf("latest migration = %d, want at least 148", LatestMigrationVersion())
	}
	if LatestMigrationVersion() < 149 {
		t.Fatalf("latest migration = %d, want at least 149 for permission matrix", LatestMigrationVersion())
	}
	if LatestMigrationVersion() < 150 {
		t.Fatalf("latest migration = %d, want at least 150 for dictionary/schema", LatestMigrationVersion())
	}
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gov-migrate.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !gdb.Migrator().HasTable(&model.DocumentGovernance{}) {
		t.Fatal("rgx_document_governance table missing after migrate")
	}
	if !gdb.Migrator().HasTable(&model.UserClearance{}) {
		t.Fatal("rgx_user_clearance table missing after migrate")
	}
	if !gdb.Migrator().HasTable(&model.GovernanceDictionaryEntry{}) {
		t.Fatal("rgx_governance_dictionary table missing after migrate")
	}
	if !gdb.Migrator().HasTable(&model.GovernanceSchema{}) {
		t.Fatal("rgx_governance_schema table missing after migrate")
	}

	// Platform dictionary terms must be seeded by migration 150 (five terms).
	var dictCount int64
	if err := gdb.Model(&model.GovernanceDictionaryEntry{}).
		Where("origin = ?", model.DictOriginPlatform).Count(&dictCount).Error; err != nil {
		t.Fatal(err)
	}
	if dictCount != 5 {
		t.Fatalf("seeded platform dictionary terms = %d, want 5", dictCount)
	}

	// Permission matrix must reconcile the new resources for governor roles.
	ctx := context.Background()
	var count int64
	if err := gdb.WithContext(ctx).Model(&model.Permission{}).
		Where("role_id = ? AND resource IN ? AND action = 'manage'", model.RoleTenantAdmin,
			[]string{"document-governance", "user-clearance"}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("tenant_admin must hold manage on both new resources, got %d", count)
	}
	var readTenantAdmin int64
	if err := gdb.WithContext(ctx).Model(&model.Permission{}).
		Where("role_id = ? AND resource = ? AND action IN ?", model.RoleTeamAdmin,
			"document-governance", []string{"read"}).
		Count(&readTenantAdmin).Error; err != nil {
		t.Fatal(err)
	}
	if readTenantAdmin == 0 {
		t.Fatal("team_admin must at least read document-governance")
	}
	var memberManageGrant int64
	if err := gdb.WithContext(ctx).Model(&model.Permission{}).
		Where("role_id = ? AND resource IN ? AND action = 'manage'", model.RoleTeamAdmin,
			[]string{"document-governance", "user-clearance"}).Count(&memberManageGrant).Error; err != nil {
		t.Fatal(err)
	}
	if memberManageGrant != 0 {
		t.Fatalf("team_admin must NOT hold manage on governance resources, got %d", memberManageGrant)
	}
}

package db

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

func TestDedupeUniqueNames(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "names.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := gdb.AutoMigrate(&model.Tenant{}, &model.Project{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.Tenant{
		{ID: "tenant-1", Name: "Demo", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive},
		{ID: "tenant-2", Name: "demo", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive},
	}
	if err := gdb.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	projects := []model.Project{
		{ID: "project-1", TenantID: "tenant-1", Name: "Ops"},
		{ID: "project-2", TenantID: "tenant-1", Name: "ops"},
		{ID: "project-3", TenantID: "tenant-2", Name: "ops"},
	}
	if err := gdb.Create(&projects).Error; err != nil {
		t.Fatal(err)
	}

	if err := dedupeUniqueNames(gdb, "rgx_tenant", "id", "", "name"); err != nil {
		t.Fatal(err)
	}
	if err := dedupeUniqueNames(gdb, "rgx_project", "id", "tenant_id", "name"); err != nil {
		t.Fatal(err)
	}

	var renamed model.Tenant
	if err := gdb.First(&renamed, "id = ?", "tenant-2").Error; err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "demo (2)" {
		t.Fatalf("expected renamed workspace, got %q", renamed.Name)
	}
	var count int64
	if err := gdb.Model(&model.Project{}).Where("tenant_id = ? AND LOWER(name) = ?", "tenant-1", "ops").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one scoped project name, got %d", count)
	}
}

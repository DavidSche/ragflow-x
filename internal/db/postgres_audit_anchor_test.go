package db

import (
	"os"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_PostgresAuditAnchorImmutability(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL baseline")
	}
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	defer sqlDB.Close()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	anchor := model.AuditAnchor{
		ID: id.New(), TenantID: "pg-anchor-" + id.New()[:8], LastSeq: 1,
		LastHash: id.New(), Algorithm: model.AuditAnchorAlgorithm,
	}
	if err := gdb.Create(&anchor).Error; err != nil {
		t.Fatalf("create anchor: %v", err)
	}
	if err := gdb.Model(&model.AuditAnchor{}).Where("id = ?", anchor.ID).
		Update("last_hash", "tampered").Error; err == nil {
		t.Fatal("expected PostgreSQL update to fail")
	}
	if err := gdb.Delete(&model.AuditAnchor{}, "id = ?", anchor.ID).Error; err == nil {
		t.Fatal("expected PostgreSQL delete to fail")
	}
	var count int64
	if err := gdb.Model(&model.AuditAnchor{}).Where("id = ?", anchor.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("anchor count = %d, want 1", count)
	}
}

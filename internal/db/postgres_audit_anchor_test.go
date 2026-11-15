package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_PostgresAuditAnchorImmutability(t *testing.T) {
	gdb := isolatedPostgresDB(t)
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

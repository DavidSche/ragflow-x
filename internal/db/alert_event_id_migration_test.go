package db

import (
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Alerts persisted by the reconcilers arrived without an ID and therefore
// landed on the empty primary key: the worklist could not claim them
// (/alerts//claimed) and every later anonymous alert was dropped by
// ON CONFLICT (id) DO NOTHING. Migration 189 gives the surviving row an ID so
// the existing buttons start working again.
func TestMigrationV189RepairsAlertEventIDs(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/alert-ids.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := MigrateTo(gdb, 188); err != nil {
		t.Fatalf("migrate to 188: %v", err)
	}

	now := time.Now().UTC()
	alert := &model.AlertEvent{
		ID: "", TenantID: "tenant-1", Title: "Knowledge revision requires evaluation review",
		Severity: "warn", Type: "knowledge.incremental.impact", Status: model.AlertStatusOpen,
		OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(alert).Error; err != nil {
		t.Fatalf("seed unreachable alert: %v", err)
	}
	delivery := &model.AlertDelivery{
		AlertEventID: "", Channel: "webhook", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusPending, LastAttemptAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(delivery).Error; err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	if err := MigrateTo(gdb, 189); err != nil {
		t.Fatalf("migrate to 189: %v", err)
	}

	var events []model.AlertEvent
	if err := gdb.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one alert, got %d", len(events))
	}
	if events[0].ID == "" {
		t.Fatal("migration left the alert on the empty primary key")
	}
	var repaired model.AlertDelivery
	if err := gdb.Where("channel = ?", "webhook").First(&repaired).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.AlertEventID != events[0].ID {
		t.Fatalf("delivery still points at %q, want %q", repaired.AlertEventID, events[0].ID)
	}
}

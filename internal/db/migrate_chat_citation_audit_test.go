package db

// Migration 187 (doc/301 T19).
//
// The sample writes two tables and four ledger columns, and the idempotency guard is
// a *database* constraint, not a process lock: a replayed weekly job must not be able
// to sample the same week twice. A constraint that only exists in a comment is not a
// guard, so it is asserted against a real schema here.

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func chatCitationAuditFixture(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "citation-audit.db")})
	if err != nil {
		t.Fatal(err)
	}
	// Windows will not delete a temp dir whose sqlite file is still open.
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	return gdb
}

func TestChatCitationAuditMigrationCreatesTables(t *testing.T) {
	gdb := chatCitationAuditFixture(t)
	for _, table := range []string{"rgx_chat_citation_audit_run", "rgx_chat_citation_audit_finding"} {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("%s was not created", table)
		}
	}
	// The four ledger columns the write-back needs. Without them the audit would have
	// to write a second table to hold a disposition the queue cannot filter on.
	for _, column := range []string{
		"citation_audit_status", "citation_audit_reason",
		"citation_audit_run_id", "citation_audited_at",
	} {
		if !gdb.Migrator().HasColumn(&model.KnowledgeOpsEvent{}, column) {
			t.Fatalf("rgx_knowledge_ops_event.%s was not created", column)
		}
	}
}

func TestChatCitationAuditRunIsUniquePerTenantAndPeriod(t *testing.T) {
	gdb := chatCitationAuditFixture(t)
	first := model.ChatCitationAuditRun{
		ID: "run-1", TenantID: "t1", PeriodStart: "2026-02-23",
		MethodVersion: "qa2.v1", IdempotencyKey: "t1:2026-02-23",
		Status: model.ChatCitationAuditRunRunning,
	}
	if err := gdb.Create(&first).Error; err != nil {
		t.Fatalf("insert first run: %v", err)
	}
	// A different idempotency key must not open a second run for the same week: the
	// guard has to be the period, not a value the caller chooses.
	second := model.ChatCitationAuditRun{
		ID: "run-2", TenantID: "t1", PeriodStart: "2026-02-23",
		MethodVersion: "qa2.v1", IdempotencyKey: "t1:2026-02-23:retry",
		Status: model.ChatCitationAuditRunRunning,
	}
	if err := gdb.Create(&second).Error; err == nil {
		t.Fatal("a second run for the same tenant and week must be refused by the schema")
	}
	// Another tenant's week is a different row: the sample is per tenant.
	other := model.ChatCitationAuditRun{
		ID: "run-3", TenantID: "t2", PeriodStart: "2026-02-23",
		MethodVersion: "qa2.v1", IdempotencyKey: "t2:2026-02-23",
		Status: model.ChatCitationAuditRunRunning,
	}
	if err := gdb.Create(&other).Error; err != nil {
		t.Fatalf("insert other tenant's run: %v", err)
	}
}

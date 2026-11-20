package db

// Migration 188 (doc/301 §5.10 GW-3 / T21).
//
// The daily reconciliation writes two tables and no columns anywhere else — the check
// must not be able to edit the books it audits, so it gets its own ledger and nothing
// more. The idempotency guard is a *database* constraint: `(tenant_id, day)` unique,
// which is what makes a replayed daily job unable to double-count one day's findings.

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func gatewayBillingFixture(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gw-billing-migrate.db")})
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

func TestGatewayBillingReconcileMigrationCreatesTables(t *testing.T) {
	gdb := gatewayBillingFixture(t)
	for _, table := range []string{
		"rgx_gateway_billing_reconcile_run",
		"rgx_gateway_billing_reconcile_finding",
	} {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("%s was not created", table)
		}
	}
	// The counts must be columns, not JSON: the drill queries "which kind fired on
	// which day" and a text blob would make that a scan of everything.
	for _, column := range []string{
		"checked", "findings", "missing_meter", "missing_cost", "charged_after_failure",
		"duplicate_request", "stale_reservation", "quota_drift_buckets", "usage_drift",
		"quota_expected_tokens", "quota_actual_tokens",
		"usage_metered_requests", "usage_billed_requests", "failure_reason",
	} {
		if !gdb.Migrator().HasColumn(&model.GatewayBillingReconcileRun{}, column) {
			t.Fatalf("rgx_gateway_billing_reconcile_run.%s was not created", column)
		}
	}
}

func TestGatewayBillingReconcileRefusesSecondRunForSameDay(t *testing.T) {
	gdb := gatewayBillingFixture(t)
	now := time.Now().UTC()
	first := &model.GatewayBillingReconcileRun{
		ID: "run-1", TenantID: "t1", Day: "2026-08-20",
		Status: model.GatewayBillingReconcileRunning, StartedAt: now, CreatedAt: now,
	}
	if err := gdb.Create(first).Error; err != nil {
		t.Fatal(err)
	}

	// Same tenant, same day, different id: the unique index is the guard, so the
	// database — not the service — must refuse it.
	duplicate := &model.GatewayBillingReconcileRun{
		ID: "run-2", TenantID: "t1", Day: "2026-08-20",
		Status: model.GatewayBillingReconcileRunning, StartedAt: now, CreatedAt: now,
	}
	if err := gdb.Create(duplicate).Error; err == nil {
		t.Fatal("a second run for the same tenant-day was accepted")
	}

	// Another tenant's same day is a different reconciliation and must insert.
	other := &model.GatewayBillingReconcileRun{
		ID: "run-3", TenantID: "t2", Day: "2026-08-20",
		Status: model.GatewayBillingReconcileRunning, StartedAt: now, CreatedAt: now,
	}
	if err := gdb.Create(other).Error; err != nil {
		t.Fatalf("another tenant's run was refused: %v", err)
	}
}

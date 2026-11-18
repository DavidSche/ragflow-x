package service

// A47 (P0-B, closing the "半交付" note in doc/288 v2.8) — the soft-delete recovery
// window is only a promise if something can act on it. This file pins the three
// answers the restore surface owes a user:
//
//   - inside the window: the report comes back, with its revisions and artifacts, and
//     the recovery marker is gone (so a second restore is not a second delete);
//   - outside the window (or `force=true`): 409/44104, never a silent no-op — a
//     restore that answers 200 for a report that stays invisible is the failure mode
//     that makes users delete and recreate a report;
//   - retention already ended: 409/44104 as well, because reviving a row the janitor
//     is about to purge produces a report that disappears again minutes later.
//
// The window is compared against `cleanup_hold_until`, which is written at delete
// time from the tenant overlay (T20b) and never extended afterwards: A47 says a soft
// delete does not lengthen any TTL, and these tests keep that true by using the real
// delete path rather than writing the column by hand.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// deleteReportForTest drives the real soft-delete path so the recovery window comes
// from the same place production gets it.
func deleteReportForTest(t *testing.T, fx *reportFixture, reportID string, force bool) {
	t.Helper()
	if err := fx.svc.SoftDeleteReport(context.Background(), fx.owner.ID, fx.tenantID, reportID, "test", force); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
}

func TestRestoreReportReopensInsideTheRecoveryWindow(t *testing.T) {
	fx := newReportFixture(t, "restore-ok")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-restore-0001")

	deleteReportForTest(t, fx, created.Report.ID, false)
	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("a deleted report must be invisible to detail before restore")
	}

	detail, err := fx.svc.RestoreReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("restore inside the window must succeed: %v", err)
	}
	if detail.Report.ID != created.Report.ID || detail.Report.CurrentRevisionID != created.Revision.ID {
		t.Fatalf("restore must answer the report it re-opened: %+v", detail.Report)
	}
	if detail.Report.DeletedAt != nil {
		t.Fatal("restored report must not keep deleted_at")
	}
	if detail.Report.CleanupHoldUntil != nil {
		// A leftover hold on a live row is a report the janitor may purge while the
		// user is reading it.
		t.Fatalf("restore must clear the recovery window, got %v", detail.Report.CleanupHoldUntil)
	}
	// The report's own clock is untouched: the hold is a grace period on a deleted
	// row, never a TTL extension (§7.6).
	if !detail.Report.ExpiresAt.Equal(created.Report.ExpiresAt) {
		t.Fatalf("restore must not move expires_at: %v vs %v",
			detail.Report.ExpiresAt, created.Report.ExpiresAt)
	}

	// Restorable is a one-shot state: a second restore must say "not deleted" rather
	// than answer 200 twice.
	if _, err := fx.svc.RestoreReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("second restore must be refused")
	} else if code := reportErrorCode(err); code != codeReportInvalidTransition {
		t.Fatalf("second restore must be 44104, got %d", code)
	}

	// And the audit trail says who did it (A47: "写审计").
	var logs []model.AuditLog
	if err := fx.gdb.Where("tenant_id = ? AND action = ?", fx.tenantID, "report.restored").
		Find(&logs).Error; err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(logs) != 1 || logs[0].ResourceID != created.Report.ID || logs[0].UserID != fx.owner.ID {
		t.Fatalf("restore must be audited once for this report: %+v", logs)
	}
}

func TestRestoreReportRefusesAfterTheWindowClosed(t *testing.T) {
	fx := newReportFixture(t, "restore-closed")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-restore-0002")
	deleteReportForTest(t, fx, created.Report.ID, false)

	// Move the window into the past rather than waiting seven days. This is the one
	// place a test writes `cleanup_hold_until` directly, and it simulates time — it
	// does not simulate the delete.
	past := time.Now().UTC().Add(-time.Hour)
	if err := fx.gdb.Model(&model.Report{}).Where("id = ?", created.Report.ID).
		Update("cleanup_hold_until", past).Error; err != nil {
		t.Fatalf("close the window: %v", err)
	}
	if _, err := fx.svc.RestoreReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("restore after the window must be refused")
	} else if code := reportErrorCode(err); code != codeReportInvalidTransition {
		t.Fatalf("closed window must be 44104, got %d", code)
	}
	// Still gone.
	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("a refused restore must not resurrect the row")
	}
}

func TestRestoreReportRefusesAForceDeletedReport(t *testing.T) {
	fx := newReportFixture(t, "restore-force")
	ctx := context.Background()
	// `force` needs `manage:report`, so the report has to belong to somebody who holds
	// it: a tenant admin deleting their own report is the only shape that reaches the
	// zero-length window (a colleague's report is refused earlier, as 44109).
	admin, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-admin-restore", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := fx.svc.CreateReport(ctx, admin.ID, fx.tenantID, "req-restore-0003",
		reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	if err := fx.svc.SoftDeleteReport(ctx, admin.ID, fx.tenantID, created.Report.ID, "归档", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}

	// `force=true` means "do not keep it". The window is zero, so the restore must
	// refuse even though the row is still on disk waiting for its purge.
	if _, err := fx.svc.RestoreReport(ctx, admin.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("a force-deleted report must not be restorable")
	} else if code := reportErrorCode(err); code != codeReportInvalidTransition {
		t.Fatalf("force delete must answer 44104 on restore, got %d", code)
	}
}

func TestRestoreReportRefusesAfterRetentionEnded(t *testing.T) {
	fx := newReportFixture(t, "restore-expired")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-restore-0004")
	deleteReportForTest(t, fx, created.Report.ID, false)

	// Retention is over while the recovery window is still open. Both clocks exist
	// and the shorter one wins: the user may not recover a report that is due for
	// purge, or the next janitor pass silently undoes the restore.
	past := time.Now().UTC().Add(-time.Hour)
	if err := fx.gdb.Model(&model.Report{}).Where("id = ?", created.Report.ID).
		Update("expires_at", past).Error; err != nil {
		t.Fatalf("age the report: %v", err)
	}
	if _, err := fx.svc.RestoreReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("restore after retention ended must be refused")
	} else if code := reportErrorCode(err); code != codeReportInvalidTransition {
		t.Fatalf("expired retention must be 44104, got %d", code)
	}
}

// A colleague in the same tenant must not learn that a report exists by trying to
// restore it: the answer is the same 44109 as every other object read (§7.6).
func TestRestoreReportHidesAColleaguesReport(t *testing.T) {
	fx := newReportFixture(t, "restore-tenant")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-restore-0005")
	deleteReportForTest(t, fx, created.Report.ID, false)

	colleague, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-colleague-restore", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.RestoreReport(ctx, colleague.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("a colleague must not restore somebody else's report")
	} else if code := reportErrorCode(err); code != codeReportAccessDenied {
		t.Fatalf("cross-owner restore must be 44109, got %d", code)
	}
	// And the row is still deleted afterwards.
	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err == nil {
		t.Fatal("a refused restore must leave the row deleted")
	}
}

// `force=true` is a stronger delete, so it needs a stronger permission: an operator
// who may delete their own report must not be able to skip the recovery window.
func TestForceDeleteRequiresManage(t *testing.T) {
	fx := newReportFixture(t, "force-perm")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-restore-0006")

	if err := fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, "", true); err == nil {
		t.Fatal("force delete must require manage:report")
	}
	// The refused call must not have deleted anything: a partial force delete would be
	// the worst of both answers (gone, but with a window nobody can see).
	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err != nil {
		t.Fatalf("a refused force delete must leave the report live: %v", err)
	}
}

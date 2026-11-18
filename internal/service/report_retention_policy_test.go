package service

// Tenant-level report retention overlay (doc/288 §7.6 / T20b, A47).
//
// These tests pin the three properties that make the overlay safe:
//
//  1. **Inheritance**: a tenant that never overrode a clock follows the system value,
//     including changes made after the overlay row exists. (A copied default would
//     freeze it, which is the bug this design avoids.)
//  2. **`max` survives**: a tenant shortening `report_days` widens the evidence clock
//     instead of letting evidence expire before the report that cites it.
//  3. **No retroactivity**: the overlay only affects rows created afterwards.
//     Existing `expires_at` values are never recomputed, and a purged row is not
//     revived by a longer policy (A47).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// setSystemReportRetention installs the system policy a tenant overlay inherits.
// retentionDays is a local pointer helper (intPtr already exists in another file of
// this package).
func retentionDays(value int) *int { return &value }

func setSystemReportRetention(t *testing.T, svc *Service, report, evidence, artifact, event, hold int) {
	t.Helper()
	svc.SetRetentionPolicy(config.Retention{
		Enabled: true, AuditDays: 180, UsageDays: 90, FeedbackDays: 180, JobDays: 30,
		ReportDays: report, ReportEvidenceDays: evidence,
		ReportArtifactDays: artifact, ReportEventDays: event, ReportCleanupHoldDays: hold,
	})
}

// retentionAdmin creates a tenant administrator, who is the only role that may write
// the overlay: retention decides when evidence disappears, so `execute` is not
// enough (doc/288 §11.4).
func retentionAdmin(t *testing.T, fx *reportFixture, name string) *model.User {
	t.Helper()
	admin, err := fx.svc.CreateUser(context.Background(), fx.tenantID, "", CreateUserRequest{
		Username: "report-retention-admin-" + name, Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

// createReportRow creates a report through the real service path and returns the
// stored row, so the assertion sees the frozen column rather than a computed value.
func createReportRow(t *testing.T, fx *reportFixture, key string) *model.Report {
	t.Helper()
	ctx := context.Background()
	result, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, key, reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, result.Report.ID)
	if err != nil || report == nil {
		t.Fatalf("read created report: %v", err)
	}
	return report
}

func TestReportRetentionOverlayInheritsAndOverrides(t *testing.T) {
	fx := newReportFixture(t, "tenantpolicy")
	ctx := context.Background()
	admin := retentionAdmin(t, fx, "tenantpolicy")
	setSystemReportRetention(t, fx.svc, 90, 90, 30, 7, 7)

	// No overlay row: every clock is the system value.
	view, err := fx.svc.ReportRetention(ctx, fx.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if view.ReportDays != 90 || view.ReportEvidenceDays != 90 || view.ReportArtifactDays != 30 ||
		view.ReportEventDays != 7 || view.ReportCleanupHoldDays != 7 || len(view.Overridden) != 0 {
		t.Fatalf("an untouched tenant must inherit every value: %+v", view)
	}

	// `execute` (the report owner) is not enough to move compliance clocks.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, fx.owner.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(30)}); err == nil {
		t.Fatal("an operator must not be able to write the retention overlay")
	}

	// Override two clocks; the other three keep inheriting.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(30), ReportArtifactDays: retentionDays(3)}); err != nil {
		t.Fatalf("a tenant admin must be able to manage retention: %v", err)
	}
	view, err = fx.svc.ReportRetention(ctx, fx.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if view.ReportDays != 30 || view.ReportArtifactDays != 3 ||
		view.ReportEventDays != 7 || view.ReportCleanupHoldDays != 7 {
		t.Fatalf("only the overridden clocks may change: %+v", view)
	}
	// The evidence clock is max(effective report, evidence). The tenant only
	// shortened the report to 30, so the inherited 90 keeps the evidence clock at
	// 90 — shortening the report never shrinks the evidence that outlives it.
	if view.ReportEvidenceDays != 90 {
		t.Fatalf("evidence must be max(effective report 30, inherited evidence 90) = 90, got %d",
			view.ReportEvidenceDays)
	}
	if joined := joinForTest(view.Overridden); joined != "report_days,report_artifact_days" {
		t.Fatalf("the view must name what the tenant pins: %v", view.Overridden)
	}

	// An operational change to the system value still reaches the inherited clocks.
	setSystemReportRetention(t, fx.svc, 90, 90, 30, 3, 5)
	view, err = fx.svc.ReportRetention(ctx, fx.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if view.ReportEventDays != 3 || view.ReportCleanupHoldDays != 5 {
		t.Fatalf("inheritance must keep following the system value: %+v", view)
	}
	if view.ReportDays != 30 || view.ReportArtifactDays != 3 {
		t.Fatalf("an override must survive a system change: %+v", view)
	}
	if view.SystemReportDays != 90 {
		t.Fatalf("the view must expose the inherited value too: %+v", view)
	}
}

// TestReportRetentionEvidenceMaxSurvivesATenantShortcut is §7.6 invariant ② under the
// overlay: a tenant may run a 30-day report clock, but its evidence still has to
// outlive the report that cites it.
func TestReportRetentionEvidenceMaxSurvivesATenantShortcut(t *testing.T) {
	fx := newReportFixture(t, "tenantmax")
	ctx := context.Background()
	admin := retentionAdmin(t, fx, "tenantmax")
	setSystemReportRetention(t, fx.svc, 90, 90, 30, 7, 7)
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(30), ReportEvidenceDays: retentionDays(7)}); err != nil {
		t.Fatal(err)
	}
	policy := fx.svc.ReportRetentionFor(ctx, fx.tenantID)
	if policy.ReportDays != 30 {
		t.Fatalf("the tenant report clock must win, got %d", policy.ReportDays)
	}
	if effective := policy.EffectiveReportEvidenceTTL(); effective != 30 {
		t.Fatalf("evidence must be widened to the effective report clock, got %d", effective)
	}

	// A tenant may also keep evidence *longer* than the report.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(30), ReportEvidenceDays: retentionDays(400)}); err != nil {
		t.Fatal(err)
	}
	if effective := fx.svc.ReportRetentionFor(ctx, fx.tenantID).EffectiveReportEvidenceTTL(); effective != 400 {
		t.Fatalf("a longer tenant evidence clock must be honoured, got %d", effective)
	}
}

// TestReportRetentionOverlayOnlyAffectsNewRows is A47's "改参数不追溯" in the tenant
// dimension: freezing happens at creation, so shortening the policy afterwards must
// leave every existing `expires_at` exactly where it was.
func TestReportRetentionOverlayOnlyAffectsNewRows(t *testing.T) {
	fx := newReportFixture(t, "tenantfreeze")
	ctx := context.Background()
	admin := retentionAdmin(t, fx, "tenantfreeze")
	setSystemReportRetention(t, fx.svc, 90, 90, 30, 7, 7)
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(30)}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().AddDate(0, 0, 30)
	report := createReportRow(t, fx, "req-tenantfreeze-0001")
	if report.ExpiresAt.Before(before.Add(-2*time.Minute)) || report.ExpiresAt.After(before.Add(2*time.Minute)) {
		t.Fatalf("the row clock must come from the tenant overlay (30d), got %s", report.ExpiresAt)
	}
	frozen := report.ExpiresAt

	// Shorten the policy to 5 days: the existing row keeps its 30-day clock.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportDays: retentionDays(5)}); err != nil {
		t.Fatal(err)
	}
	if got := reportAfter(fx, t, report.ID).ExpiresAt; !got.Equal(frozen) {
		t.Fatalf("a policy change must not recompute existing rows: %s -> %s", frozen, got)
	}
	// A row created now gets the new clock.
	fresh := createReportRow(t, fx, "req-tenantfreeze-0002")
	if !fresh.ExpiresAt.Before(frozen.Add(-24 * time.Hour)) {
		t.Fatalf("a new row must use the new policy: %s vs %s", fresh.ExpiresAt, frozen)
	}

	// The audit row records which fields the tenant pins and that nothing was
	// recomputed — the fact an auditor would otherwise assume the opposite of.
	if got := countAuditAction(t, fx.gdb, fx.tenantID, "report.retention.tenant_overridden"); got != 2 {
		t.Fatalf("each overlay write must be audited, got %d rows", got)
	}
	var latest model.AuditLog
	if err := fx.gdb.Where("tenant_id = ? AND action = ?", fx.tenantID,
		"report.retention.tenant_overridden").Order("seq DESC").Take(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.UserID != admin.ID || latest.ResourceID != fx.tenantID {
		t.Fatalf("the audit must name the actor and the tenant: %+v", latest)
	}
	if !containsEvery(latest.DetailJSON, `"report_days"`, `"existing_rows_recomputed":false`) {
		t.Fatalf("the overlay audit must record the pin and the no-retroactivity note: %s", latest.DetailJSON)
	}
}

// TestSetTenantReportPolicyRejectsOutOfRange keeps a tenant from reaching a state the
// system surface would refuse: 0 days would purge every report at the next sweep.
//
// The answer is pinned as `400` / `44105`, not just "some error". Two codes are
// deliberately *not* used and the assertions say why: `44114` belongs to
// `Idempotency-Key` (§8.7 rule ⑤ forbids borrowing it), and an outside-family code
// such as `40098` is already mapped to one memory-specific sentence in the web error
// table — a bad retention window would render "请选择嵌入模型".
func TestSetTenantReportPolicyRejectsOutOfRange(t *testing.T) {
	fx := newReportFixture(t, "tenantvalidate")
	ctx := context.Background()
	admin := retentionAdmin(t, fx, "tenantvalidate")
	for _, request := range []TenantReportPolicyRequest{
		{ReportDays: retentionDays(0)},
		{ReportDays: retentionDays(4000)},
		{ReportEvidenceDays: retentionDays(-1)},
		{ReportCleanupHoldDays: retentionDays(366)},
	} {
		_, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, request)
		if err == nil {
			t.Fatalf("out-of-range retention must be rejected: %+v", request)
		}
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) {
			t.Fatalf("rejection must be an http error, got %T: %v", err, err)
		}
		if httpErr.Status != 400 || httpErr.Code != codeReportOutlineInvalid {
			t.Fatalf("out-of-range retention = %d/%d, want 400/%d",
				httpErr.Status, httpErr.Code, codeReportOutlineInvalid)
		}
		if httpErr.Code == codeReportIdempotencyKeyRequired {
			t.Fatal("44114 is the Idempotency-Key code and must not answer a range check")
		}
	}
	// A zero recovery window is legal: it means "no grace period".
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID,
		TenantReportPolicyRequest{ReportCleanupHoldDays: retentionDays(0)}); err != nil {
		t.Fatalf("cleanup hold 0 is a legal value: %v", err)
	}
}

// TestReportRetentionOverlayIsNotCrossTenant is the isolation rule applied to the
// policy row: another workspace's overlay must never resolve for this tenant.
func TestReportRetentionOverlayIsNotCrossTenant(t *testing.T) {
	fx := newReportFixture(t, "tenantisolation")
	ctx := context.Background()
	setSystemReportRetention(t, fx.svc, 90, 90, 30, 7, 7)
	other, err := fx.svc.CreateTenant(ctx, "隔壁工作区 tenantisolation")
	if err != nil {
		t.Fatal(err)
	}
	// Written straight through the store: this test is about resolution, not about
	// who may write (that is covered above).
	if err := fx.svc.Store.UpsertTenantReportPolicy(ctx, &model.TenantReportPolicy{
		TenantID: other.ID, ReportDays: retentionDays(5),
	}); err != nil {
		t.Fatal(err)
	}
	if got := fx.svc.ReportRetentionFor(ctx, fx.tenantID); got.ReportDays != 90 {
		t.Fatalf("another tenant's overlay must not leak: %d", got.ReportDays)
	}
	if got := fx.svc.ReportRetentionFor(ctx, other.ID); got.ReportDays != 5 {
		t.Fatalf("the owning tenant must see its own overlay: %d", got.ReportDays)
	}
}

func reportAfter(fx *reportFixture, t *testing.T, reportID string) *model.Report {
	t.Helper()
	report, err := fx.svc.Store.GetReport(context.Background(), fx.tenantID, reportID)
	if err != nil || report == nil {
		t.Fatalf("read report %s: %v", reportID, err)
	}
	return report
}

func joinForTest(values []string) string {
	out := ""
	for index, value := range values {
		if index > 0 {
			out += ","
		}
		out += value
	}
	return out
}

func containsEvery(haystack string, needles ...string) bool {
	for _, needle := range needles {
		found := false
		for index := 0; index+len(needle) <= len(haystack); index++ {
			if haystack[index:index+len(needle)] == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

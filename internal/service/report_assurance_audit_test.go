package service

// The assurance surface's audit trail (doc/301 §7.4 / T15).
//
// Three actions, one property, and the property is not "a row was written": it is
// **which chain the row landed on**. `recordReportAudit` takes `(actorID, tenantID)`,
// two adjacent non-empty strings, so a swapped call compiles, inserts, and produces a
// row the tenant cannot read — an audit trail that looks complete in the table and is
// empty in every query the product runs. That exact swap shipped twice in this slice
// and was caught only because the export gate test asserted the *tenant* chain rather
// than the row count. This file is the pin for all three actions.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// reportAuditRowDetail reads one audit row's detail payload (reportAuditDetail in
// report_audit_actions_test.go takes the raw string; a row carries its own action and
// that action belongs in the failure message).
func reportAuditRowDetail(t *testing.T, row model.AuditLog) map[string]any {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal([]byte(row.DetailJSON), &detail); err != nil {
		t.Fatalf("audit %s detail is not readable: %v", row.Action, err)
	}
	return detail
}

// requireAuditOnTheTenantChain asserts the row exists for *this workspace* and names
// *this* actor. Both halves matter: the chain is selected by `tenant_id`, and the
// `user_id` is what makes the row attributable rather than anonymous.
func requireAuditOnTheTenantChain(t *testing.T, fx *reportFixture, action, actorID, resourceID string) model.AuditLog {
	t.Helper()
	audits, err := fx.svc.Store.ListAuditsAll(context.Background(), fx.tenantID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	for _, row := range audits {
		if row.Action != action {
			continue
		}
		if row.UserID != actorID {
			t.Fatalf("%s: actor recorded as %q, want %q (swapped arguments land the "+
				"row on a user-keyed chain and mislabel the actor)", action, row.UserID, actorID)
		}
		if row.ResourceID != resourceID {
			t.Fatalf("%s: resource %q, want %q", action, row.ResourceID, resourceID)
		}
		return row
	}
	// Say what *was* there: an empty list and a row on the wrong chain need different
	// fixes, and the failure message is the only place that distinction survives.
	var seen []string
	for _, row := range audits {
		seen = append(seen, row.Action+"@"+row.UserID)
	}
	t.Fatalf("%s: no audit row on tenant %s (saw %v)", action, fx.tenantID, seen)
	return model.AuditLog{}
}

func TestTriggeringARunIsAuditedOnTheOwningTenant(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "audit-trigger")
	ctx := context.Background()
	enableAssuranceForScopeTest(t, fx.svc)

	view, err := fx.svc.TriggerReportAssurance(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1,
		"req-audit-trigger", ReportAssuranceTriggerInput{Mode: model.ReportAssuranceModeFull})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	row := requireAuditOnTheTenantChain(t, fx.reportFixture, "report.assurance_started", fx.owner.ID, fx.reportID)
	detail := reportAuditRowDetail(t, row)
	if detail["run_id"] != view.ID {
		t.Fatalf("audit names run %v, want the one it created (%s)", detail["run_id"], view.ID)
	}
	// The tier is `pending` here, and the audit must say so: recording the policy the
	// caller asked for would make an audit row indistinguishable from a measurement.
	if detail["release_eligibility"] != model.ReportAssuranceEligibilityPending {
		t.Fatalf("fresh run audited as %v, want pending", detail["release_eligibility"])
	}
}

func TestAdjudicatingAFindingIsAuditedOnTheOwningTenant(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "audit-finding")
	ctx := context.Background()

	if _, err := fx.svc.UpdateReviewFinding(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1,
		fx.findingID, model.ReportFindingStatusAcknowledged, "按来源核对后确认", ""); err != nil {
		t.Fatalf("adjudicate: %v", err)
	}

	row := requireAuditOnTheTenantChain(t, fx.reportFixture, "report.finding_reviewed", fx.owner.ID, fx.reportID)
	detail := reportAuditRowDetail(t, row)
	if detail["finding_id"] != fx.findingID {
		t.Fatalf("audit names finding %v, want %s", detail["finding_id"], fx.findingID)
	}
	// `open → acknowledged` is the only first move the transition table allows; a
	// test that jumped straight to `resolved` would be asserting a shape the product
	// refuses (409), and the audit row it pins would never exist.
	if detail["to"] != model.ReportFindingStatusAcknowledged {
		t.Fatalf("audit records transition to %v, want %s", detail["to"], model.ReportFindingStatusAcknowledged)
	}
	if detail["from"] != model.ReportFindingStatusOpen {
		t.Fatalf("audit records transition from %v, want %s", detail["from"], model.ReportFindingStatusOpen)
	}
}

func TestTheAckAndTheRunShareOneAuditChain(t *testing.T) {
	// One chain, two actions, in the order the product performs them: a run is
	// triggered, conflicts are found, and an export acknowledges them. Asserting both
	// rows in one test is what makes "the trail is complete" checkable — each action
	// alone can be right while the pair is written to different tenants.
	fx := newAssuranceScopeFixture(t, "audit-chain")
	ctx := context.Background()
	enableAssuranceForScopeTest(t, fx.svc)

	view, err := fx.svc.TriggerReportAssurance(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1,
		"req-audit-chain", ReportAssuranceTriggerInput{Mode: model.ReportAssuranceModeFull})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	stampAssurance(t, fx.reportFixture, &model.ReportRevision{ID: fx.revisionID}, model.ReportAssurancePolicyAdvisory, "succeeded", 1, 0)
	// The production shape: a finished run projects its id onto the revision, and the
	// ack reads it from there rather than from a body field. Without it the ack would
	// record `run_id: ""` — an acknowledgement of nothing in particular.
	if err := fx.gdb.Model(&model.ReportRevision{}).Where("id = ?", fx.revisionID).
		Update("assurance_run_id", view.ID).Error; err != nil {
		t.Fatalf("project run id: %v", err)
	}
	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: fx.reportID, Revision: 1, Format: model.ExportFormatMarkdown,
		AcknowledgeAssurance: true,
	}); err != nil {
		t.Fatalf("acknowledged export: %v", err)
	}

	started := requireAuditOnTheTenantChain(t, fx.reportFixture, "report.assurance_started", fx.owner.ID, fx.reportID)
	ack := requireAuditOnTheTenantChain(t, fx.reportFixture, "report.assurance_ack", fx.owner.ID, fx.reportID)
	if reportAuditRowDetail(t, ack)["run_id"] != view.ID {
		t.Fatalf("the ack names a different run than the one it acknowledges")
	}
	// Chain order, not wall-clock: `seq` is gapless per tenant, so "the ack came after
	// the run" is a fact even when two rows share a timestamp.
	if ack.Seq <= started.Seq {
		t.Fatalf("ack seq %d is not after the run's %d", ack.Seq, started.Seq)
	}
}

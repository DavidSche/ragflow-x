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
	"strings"
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
	// The audit row must also answer *how* the review will be asked and, when it is
	// not the direct route, *why* (doc/304 E129). This fixture tenant configures no
	// review model, so the honest pair is `engine_chat` + a reason that says which
	// input was missing — a blank reason would send the operator back to the logs.
	// The pair has to agree with the run row: a run that configured no reviewer asked
	// no model (`none`), and a run that did configures one must name the route it
	// would take. Either way the audit row states it — silence is the defect.
	transport, _ := detail["review_transport"].(string)
	if view.ReviewModel == "" {
		if transport != assuranceTransportNone {
			t.Fatalf("run has no review model but the audit says transport=%q, want %q", transport, assuranceTransportNone)
		}
	} else if transport != assuranceTransportEngine && transport != assuranceTransportProviderDirect {
		t.Fatalf("run reviews with %q but the audit says transport=%q", view.ReviewModel, transport)
	}
	reason, _ := detail["review_transport_reason"].(string)
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("audited transport has no reason (detail=%v): a fallback nobody can explain is a ticket", detail)
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

// TestTheAuditRowNamesTheReviewRouteItWillTake covers the other half of the pair: a run
// whose tenant *did* configure a reviewer. Without it the `engine_chat` + reason branch
// of the trigger is only covered by the pure resolver, and deleting the assignment that
// copies the reason into the frozen policy would go unnoticed (doc/304 E129).
func TestTheAuditRowNamesTheReviewRouteItWillTake(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "audit-route")
	ctx := context.Background()
	enableAssuranceForScopeTest(t, fx.svc)

	// A reference that names no registered instance: the resolver must land on the
	// engine route and say which gate refused.
	current, err := fx.svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatalf("current settings revision: %v", err)
	}
	expected := int64(0)
	if current != nil {
		expected = current.Revision
	}
	if _, err := fx.svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: expected,
		Changes: map[string]interface{}{
			"report.assurance_review_model": "Qwen/Qwen3.8-27B-FP8@no-such-instance@ainet",
		},
		Note:      "review route audit test",
		Confirmed: true,
	}); err != nil {
		t.Fatalf("set review model: %v", err)
	}

	view, err := fx.svc.TriggerReportAssurance(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1,
		"req-audit-route", ReportAssuranceTriggerInput{Mode: model.ReportAssuranceModeFull})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	row := requireAuditOnTheTenantChain(t, fx.reportFixture, "report.assurance_started", fx.owner.ID, fx.reportID)
	detail := reportAuditRowDetail(t, row)
	if detail["review_transport"] != assuranceTransportEngine {
		t.Fatalf("audited transport = %v, want %q — the reference names no instance",
			detail["review_transport"], assuranceTransportEngine)
	}
	reason, _ := detail["review_transport_reason"].(string)
	if !strings.Contains(reason, "no-such-instance") {
		t.Fatalf("reason = %q, want it to name the instance the reference addresses", reason)
	}
	if view.Independence != model.ReportIndependenceUnknown {
		t.Fatalf("independence = %q, want %q on the engine route", view.Independence, model.ReportIndependenceUnknown)
	}
}

package service

// doc/301 §7.2 / T09 — the assurance gate on export, and the ack it writes.
//
// Two refusals, tested here because their *order* and their *side effects* are the
// product:
//
//   - 44122 (policy requires assurance, run not terminal) must win over 44121 even
//     when conflicts > 0: "wait" and "decide" are different instructions, and asking
//     for consent to conflicts that have not been found yet is asking the wrong
//     question;
//   - 44121 must be refused *without* writing anything, and an acknowledged export
//     must write both the revision's ack payload and the `report.assurance_ack`
//     audit row *before* rendering, so a failed render cannot erase the fact that
//     the caller was shown the conflicts and said yes.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// stampAssurance writes the frozen gate inputs onto a revision, exactly as
// `ProjectAssuranceOnRevision` would after a run: the summary is the *source* of the
// gate decision, so the test must set it rather than fake a live finding count.
func stampAssurance(t *testing.T, fx *reportFixture, revision *model.ReportRevision, policy, status string, conflicts, high int) {
	t.Helper()
	summary, err := json.Marshal(map[string]any{
		"requested_policy": policy,
		"conflicts":        conflicts,
		"high_findings":    high,
	})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("id = ?", revision.ID).
		Updates(map[string]any{
			"assurance_status":       status,
			"assurance_summary_json": string(summary),
		}).Error; err != nil {
		t.Fatalf("stamp assurance: %v", err)
	}
	revision.AssuranceStatus = status
	revision.AssuranceSummaryJSON = string(summary)
}

func assuranceAckAudits(t *testing.T, fx *reportFixture) []model.AuditLog {
	t.Helper()
	audits, err := fx.svc.Store.ListAuditsAll(context.Background(), fx.tenantID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var acks []model.AuditLog
	for index := range audits {
		if audits[index].Action == "report.assurance_ack" {
			acks = append(acks, audits[index])
		}
	}
	return acks
}

func TestExportGateWaitsForAnUnfinishedRunBeforeAskingForAnAck(t *testing.T) {
	fx := newReportFixture(t, "gate-order")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}

	// Required policy, run still running, and two conflicts already recorded: the
	// caller must be told to wait, not to acknowledge.
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyRequired, "running", 2, 1)
	_, err = fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
		AcknowledgeAssurance: true,
	})
	business, ok := err.(*httperr.Error)
	if !ok || business.Code != 44122 {
		t.Fatalf("unfinished required run: got %v, want 44122 (wait), not 44121", err)
	}
	if acks := assuranceAckAudits(t, fx); len(acks) != 0 {
		t.Fatalf("an unfinished run must not record an ack, found %d", len(acks))
	}
}

func TestExportGateRefusesUnresolvedConflictsWithoutWriting(t *testing.T) {
	fx := newReportFixture(t, "gate-refuse")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "succeeded", 2, 1)

	_, err = fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	business, ok := err.(*httperr.Error)
	if !ok || business.Code != 44121 {
		t.Fatalf("unacknowledged conflicts: got %v, want 44121", err)
	}
	if !strings.Contains(business.Message, "2") {
		t.Fatalf("44121 must name how many conflicts are open, got %q", business.Message)
	}

	// Refused means refused: no ack payload, no ack audit, no artifact.
	stored := fx.reportRevision(t, revision.ID)
	if stored.AssuranceAckJSON != "" {
		t.Fatalf("a refusal wrote an ack payload: %q", stored.AssuranceAckJSON)
	}
	if acks := assuranceAckAudits(t, fx); len(acks) != 0 {
		t.Fatalf("a refusal recorded an ack audit: %+v", acks[0])
	}
	entries, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused export produced %d artifact(s)", len(entries))
	}
}

func TestAcknowledgedExportRecordsTheCountItShowed(t *testing.T) {
	fx := newReportFixture(t, "gate-ack")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "succeeded", 2, 1)

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
		AcknowledgeAssurance: true,
	}); err != nil {
		t.Fatalf("acknowledged export: %v", err)
	}

	stored := fx.reportRevision(t, revision.ID)
	if stored.AssuranceAckJSON == "" {
		t.Fatal("acknowledged export left no ack payload on the revision")
	}
	var ack struct {
		Acknowledged int    `json:"conflicts"`
		ActorID      string `json:"acknowledged_by"`
	}
	if err := json.Unmarshal([]byte(stored.AssuranceAckJSON), &ack); err != nil {
		t.Fatalf("ack payload is not readable: %v", err)
	}
	if ack.Acknowledged != 2 {
		t.Fatalf("ack recorded %d conflicts, want the 2 the caller was shown", ack.Acknowledged)
	}
	if ack.ActorID != fx.owner.ID {
		t.Fatalf("ack recorded actor %q, want %q", ack.ActorID, fx.owner.ID)
	}

	acks := assuranceAckAudits(t, fx)
	if len(acks) != 1 {
		t.Fatalf("ack audits = %d, want exactly 1", len(acks))
	}
	if acks[0].UserID != fx.owner.ID || acks[0].ResourceID != report.ID {
		t.Fatalf("ack audit identifies the wrong subject: %+v", acks[0])
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(acks[0].DetailJSON), &detail); err != nil {
		t.Fatalf("ack audit detail: %v", err)
	}
	if got, _ := detail["acknowledged"].(float64); int(got) != 2 {
		t.Fatalf("audit recorded acknowledged=%v, want 2", detail["acknowledged"])
	}
	if detail["format"] != model.ExportFormatMarkdown {
		t.Fatalf("audit recorded format=%v, want %s", detail["format"], model.ExportFormatMarkdown)
	}
}

func TestExportNeedsNoAckWhenNothingIsInConflict(t *testing.T) {
	fx := newReportFixture(t, "gate-clean")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "succeeded", 0, 0)

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	}); err != nil {
		t.Fatalf("clean export must not require an ack: %v", err)
	}
	if stored := fx.reportRevision(t, revision.ID); stored.AssuranceAckJSON != "" {
		t.Fatalf("clean export wrote an ack it never asked for: %q", stored.AssuranceAckJSON)
	}
	if acks := assuranceAckAudits(t, fx); len(acks) != 0 {
		t.Fatalf("clean export recorded an ack audit")
	}
}

// stampEligibility writes only `release_eligibility` onto an existing summary, the
// way `reportAssuranceSummary` does after a run: the gate reads that frozen value, so
// "the semantic layer did not run" must be expressed on the row rather than by
// configuring anything at export time.
func stampEligibility(t *testing.T, fx *reportFixture, revision *model.ReportRevision, eligibility string) {
	t.Helper()
	var summary map[string]any
	if err := json.Unmarshal([]byte(revision.AssuranceSummaryJSON), &summary); err != nil {
		t.Fatalf("existing summary is not readable: %v", err)
	}
	summary["release_eligibility"] = eligibility
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("id = ?", revision.ID).
		Update("assurance_summary_json", string(encoded)).Error; err != nil {
		t.Fatalf("stamp eligibility: %v", err)
	}
	revision.AssuranceSummaryJSON = string(encoded)
}

// doc/301 D6/J6 + T18 (2026-11-26 decision): v0.7.0 ships *advisory + ack*. An
// uncalibrated model must not block delivery, and it must not pass silently either —
// so a `tier_0` run requires the same explicit acknowledgement a conflict set does,
// and the acknowledgement records what was shipped.
func TestUncalibratedReviewNeedsAnAckInsteadOfBlockingDelivery(t *testing.T) {
	fx := newReportFixture(t, "gate-tier0")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	// Zero conflicts and zero high findings: the *only* reason to ask for anything is
	// that the run itself is not a release input.
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "degraded", 0, 0)
	stampEligibility(t, fx, revision, model.ReportAssuranceEligibilityTier0)

	_, err = fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	business, ok := err.(*httperr.Error)
	if !ok || business.Code != 44121 {
		t.Fatalf("unacknowledged tier_0 review: got %v, want 44121 (ack), not a refusal to deliver", err)
	}
	if !strings.Contains(business.Message, "tier_0") {
		t.Fatalf("44121 must name the eligibility it is asking about, got %q", business.Message)
	}
	data, _ := business.Data.(map[string]any)
	if data["reason"] != "release_eligibility" || data["release_eligibility"] != "tier_0" {
		t.Fatalf("44121 data names the wrong reason: %+v", business.Data)
	}
	if stored := fx.reportRevision(t, revision.ID); stored.AssuranceAckJSON != "" {
		t.Fatalf("a refusal wrote an ack payload: %q", stored.AssuranceAckJSON)
	}

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
		AcknowledgeAssurance: true,
	}); err != nil {
		t.Fatalf("acknowledged tier_0 export must be allowed (advisory, not blocking): %v", err)
	}

	stored := fx.reportRevision(t, revision.ID)
	var ack struct {
		Conflicts   int    `json:"conflicts"`
		Eligibility string `json:"release_eligibility"`
		ActorID     string `json:"acknowledged_by"`
	}
	if err := json.Unmarshal([]byte(stored.AssuranceAckJSON), &ack); err != nil {
		t.Fatalf("ack payload is not readable: %v", err)
	}
	if ack.Conflicts != 0 || ack.Eligibility != model.ReportAssuranceEligibilityTier0 {
		t.Fatalf("ack recorded %+v, want 0 conflicts + tier_0", ack)
	}
	if ack.ActorID != fx.owner.ID {
		t.Fatalf("ack recorded actor %q, want %q", ack.ActorID, fx.owner.ID)
	}

	acks := assuranceAckAudits(t, fx)
	if len(acks) != 1 {
		t.Fatalf("ack audits = %d, want exactly 1", len(acks))
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(acks[0].DetailJSON), &detail); err != nil {
		t.Fatalf("ack audit detail: %v", err)
	}
	if detail["ack_reason"] != "release_eligibility" ||
		detail["release_eligibility"] != model.ReportAssuranceEligibilityTier0 {
		t.Fatalf("ack audit cannot explain what was accepted: %+v", detail)
	}
}

func TestBlockedReviewNeedsAnAckAndStaysDeliverable(t *testing.T) {
	fx := newReportFixture(t, "gate-blocked")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "succeeded", 0, 0)
	stampEligibility(t, fx, revision, model.ReportAssuranceEligibilityBlocked)

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	}); err == nil {
		t.Fatal("a blocked review must not export without an acknowledgement")
	}
	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
		AcknowledgeAssurance: true,
	}); err != nil {
		t.Fatalf("advisory policy must keep a blocked review deliverable after the ack: %v", err)
	}
}

// The counter-test: a usable run asks for nothing. Without this, "ask for an ack"
// could be implemented as "always ask", which would train reviewers to click through
// the one dialog that matters.
func TestTierOneReviewNeedsNoAck(t *testing.T) {
	fx := newReportFixture(t, "gate-tier1")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	stampAssurance(t, fx, revision, model.ReportAssurancePolicyAdvisory, "succeeded", 0, 0)
	stampEligibility(t, fx, revision, model.ReportAssuranceEligibilityTier1)

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	}); err != nil {
		t.Fatalf("tier_1 export must not require an ack: %v", err)
	}
	if acks := assuranceAckAudits(t, fx); len(acks) != 0 {
		t.Fatalf("tier_1 export recorded an ack audit")
	}
}

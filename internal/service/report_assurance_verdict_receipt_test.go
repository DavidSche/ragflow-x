package service

// A verdict row must name its judge (doc/304 E141).
//
// The row has always been created with `method=deterministic`; when the semantic
// layer answered, nothing overwrote that column, so a model verdict was stored as
// a rule verdict with an empty `model_id` and an empty `prompt_hash`. The run's
// `used_tokens` then contradicted its own rows: money was spent on a model the
// table says was never asked. That is the receipt half of §7 item 8 — before the
// drill can ask "did the named model answer?", the row has to be able to say which
// model and which prompt template produced it.
//
// The test drives the pipeline with a hand-built evaluator (:E141's seam) because
// the property is about the *wiring*, not about the model: an evaluator that says
// `semantic`/`stub-model` must leave those exact values on the row and on any
// finding derived from it, and the rule layer must keep saying `deterministic`
// rather than inheriting a neighbour's stamp.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// receiptEvaluator is a judge with no provider behind it: it exists to be named.
type receiptEvaluator struct {
	verdict string
}

func (receiptEvaluator) ModelID() string      { return "stub-model" }
func (receiptEvaluator) PromptHash() string   { return "stub-prompt-hash" }
func (receiptEvaluator) Method() string       { return model.ReportEvaluationMethodSemantic }
func (receiptEvaluator) Independence() string { return model.ReportIndependenceSeparateProvider }

func (e receiptEvaluator) Evaluate(
	context.Context, reportAssuranceEvalInput,
) (reportAssuranceEvalOutcome, error) {
	return reportAssuranceEvalOutcome{
		Verdict: e.verdict, Relevance: "on_topic", Rationale: "stub judge",
		TokensIn: 10, TokensOut: 5,
	}, nil
}

// verdictReceiptFixture is one published revision with a single checkable claim,
// one citation and one frozen snapshot — the smallest plan the four layers accept.
type verdictReceiptFixture struct {
	*reportFixture
	reportID   string
	revisionID string
	claim      *model.ReportClaim
}

func newVerdictReceiptFixture(t *testing.T, name string) *verdictReceiptFixture {
	t.Helper()
	fx := newReportFixture(t, name)
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-receipt-"+name,
		reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	revision, err := fx.svc.Store.GetReportRevision(ctx, fx.tenantID, created.Report.ID, 1)
	if err != nil || revision == nil {
		t.Fatalf("load created revision: %v", err)
	}
	now := time.Now().UTC()
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("id = ?", revision.ID).
		Updates(map[string]interface{}{"status": model.ReportRevisionPublished, "published_at": &now}).Error; err != nil {
		t.Fatalf("publish revision: %v", err)
	}

	section := &model.ReportSection{
		ID: "sec-receipt-" + name, TenantID: fx.tenantID, RevisionID: revision.ID,
		Ord: 1, SectionKey: "refund", SectionVersion: 1, Title: "退款政策",
		RequiresEvidence: true,
	}
	claim := &model.ReportClaim{
		ID: "claim-receipt-" + name, TenantID: fx.tenantID, RevisionID: revision.ID,
		SectionID: section.ID, SectionOrd: section.Ord, SectionVersion: section.SectionVersion,
		Ord: 1, ClaimText: "退款须在收货后 7 天内申请。", ClaimType: "fact",
	}
	snapshot := &model.ReportEvidenceSnapshot{
		ID: "snap-receipt-" + name, TenantID: fx.tenantID, ChunkID: "chunk-receipt-" + name,
		SourceContentHash: "contenthash-" + name, Content: "客户须在收货后 3 天内申请退款。",
		ContentHash: "hash-" + name, ContentSize: 30, CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}
	citation := &model.ReportCitation{
		ID: "cite-receipt-" + name, TenantID: fx.tenantID, RevisionID: revision.ID,
		SectionID: section.ID, SectionOrd: section.Ord, SectionVersion: section.SectionVersion,
		Ord: 1, ClaimID: claim.ID, EvidenceSnapshotID: snapshot.ID,
		Snippet: "3 天内申请退款", ReferencedAt: now,
	}
	for _, row := range []interface{}{section, claim, snapshot, citation} {
		if err := fx.gdb.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	return &verdictReceiptFixture{
		reportFixture: fx, reportID: created.Report.ID, revisionID: revision.ID, claim: claim,
	}
}

// runWith executes the pipeline for a fresh run of this revision using the given
// judge, and returns the run id so the caller can read the rows it wrote.
func (fx *verdictReceiptFixture) runWith(
	t *testing.T, runID string, evaluator reportAssuranceEvaluator,
) *model.ReportAssuranceRun {
	t.Helper()
	ctx := context.Background()
	run := &model.ReportAssuranceRun{
		ID: runID, TenantID: fx.tenantID, ReportID: fx.reportID, RevisionID: fx.revisionID,
		Revision: 1, Status: model.ReportAssuranceStatusQueued,
		Mode: model.ReportAssuranceModeFull, RequestedPolicy: model.ReportAssurancePolicyAdvisory,
		ReleaseEligibility: model.ReportAssuranceEligibilityPending,
		MethodVersion:      reportAssuranceMethodVersion,
		BudgetTokens:       80000, TriggeredBy: fx.owner.ID, CreatedAt: time.Now().UTC(),
	}
	if err := fx.gdb.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	plan, err := fx.svc.planReportAssurance(ctx, run)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Claims) != 1 {
		t.Fatalf("plan has %d claims, want the one seeded claim", len(plan.Claims))
	}
	if err := fx.svc.executeReportAssuranceWith(ctx, run, plan, evaluator); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return run
}

func TestVerdictRowsNameTheirJudge(t *testing.T) {
	fx := newVerdictReceiptFixture(t, "judge")
	run := fx.runWith(t, "run-receipt-judge", receiptEvaluator{verdict: model.ReportVerdictUnsupported})

	var rows []model.ReportClaimEvaluation
	if err := fx.gdb.Where("run_id = ?", run.ID).Find(&rows).Error; err != nil {
		t.Fatalf("load evaluations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("evaluation rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Method != model.ReportEvaluationMethodSemantic {
		t.Fatalf("method = %q, want %q: a model verdict stored as a rule verdict is a "+
			"receipt that contradicts its own run's token count",
			row.Method, model.ReportEvaluationMethodSemantic)
	}
	if row.ModelID != "stub-model" {
		t.Fatalf("model id = %q, want the judge that answered", row.ModelID)
	}
	if row.PromptHash != "stub-prompt-hash" {
		t.Fatalf("prompt hash = %q, want the template hash of the judge", row.PromptHash)
	}

	// A finding derived from a model verdict must name the same judge. The source
	// column is what a reviewer filters on; `rule` there sends them to a layer that
	// never saw the claim.
	var findings []model.ReportReviewFinding
	if err := fx.gdb.Where("run_id = ?", run.ID).Find(&findings).Error; err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("finding rows = %d, want 1 for an unsupported verdict", len(findings))
	}
	if findings[0].Source != model.ReportEvaluationMethodSemantic {
		t.Fatalf("finding source = %q, want %q", findings[0].Source, model.ReportEvaluationMethodSemantic)
	}
	if findings[0].ModelID != "stub-model" || findings[0].PromptHash != "stub-prompt-hash" {
		t.Fatalf("finding does not name its judge: model=%q prompt=%q",
			findings[0].ModelID, findings[0].PromptHash)
	}
}

// The rule layer has no model to name. If the stamping above were applied to the
// evaluation's *default* rather than to the evaluator, this case would still pass —
// so it is the half that proves the value comes from the judge and not from a
// constant added to the struct literal.
func TestRuleLayerKeepsTheDeterministicMethod(t *testing.T) {
	fx := newVerdictReceiptFixture(t, "rule")
	run := fx.runWith(t, "run-receipt-rule", reportAssuranceRuleEvaluator{})

	var rows []model.ReportClaimEvaluation
	if err := fx.gdb.Where("run_id = ?", run.ID).Find(&rows).Error; err != nil {
		t.Fatalf("load evaluations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("evaluation rows = %d, want 1", len(rows))
	}
	if rows[0].Method != model.ReportEvaluationMethodRule {
		t.Fatalf("method = %q, want %q", rows[0].Method, model.ReportEvaluationMethodRule)
	}
	if rows[0].ModelID != "" || rows[0].PromptHash != "" {
		t.Fatalf("rule layer named a model it never called: model=%q prompt=%q",
			rows[0].ModelID, rows[0].PromptHash)
	}
}

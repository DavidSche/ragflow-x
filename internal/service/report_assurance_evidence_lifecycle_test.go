package service

// doc/319 R-01 + R-02, the two defects this batch closes.
//
// **R-01** — the evidence clock and the citation outlived each other. Retention
// hard-deleted the snapshot row, `reportAssuranceQuoteOf` turned "no row" into
// `not_provided`, and the run reported *the author cited nothing* plus a
// `no_quote` abstention while paying a model to judge an empty 【证据原文】. In the
// same breath `evidence_expired` sat in `ReportInsufficientReasons()` with zero
// producers anywhere in the codebase.
//
// **R-02** — `reportClampVerdict` rewrote every `requires_evidence` claim into
// `abstained / no_quote` whenever the quote was `not_provided`, regardless of what
// the evaluator had answered. A model-judged `unsupported` therefore vanished from
// both the verdict and the finding list, and the run still said `succeeded`.
//
// The two are tested apart on purpose. A single test asserting "the pipeline ended
// in a sensible place" would pass with either defect present, because both end in a
// plausible-looking `abstained` row.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// countingEvaluator records whether the semantic layer was reached at all. The
// question "did we pay for a judge on evidence nobody could read?" is answered by
// the call count, not by the verdict it would have produced.
type countingEvaluator struct {
	verdict string
	calls   int
}

func (e *countingEvaluator) ModelID() string      { return "stub-model" }
func (e *countingEvaluator) PromptHash() string   { return "stub-prompt-hash" }
func (e *countingEvaluator) Method() string       { return model.ReportEvaluationMethodSemantic }
func (e *countingEvaluator) Independence() string { return model.ReportIndependenceSeparateProvider }

func (e *countingEvaluator) Evaluate(
	context.Context, reportAssuranceEvalInput,
) (reportAssuranceEvalOutcome, error) {
	e.calls++
	return reportAssuranceEvalOutcome{
		Verdict: e.verdict, Relevance: "on_topic", Rationale: "stub judge",
		TokensIn: 10, TokensOut: 5,
	}, nil
}

// evidenceState is the lifecycle fact the purge would have produced in production.
type evidenceState string

const (
	// evidenceLive is the control: the snapshot exists and is inside its window.
	evidenceLive evidenceState = "live"
	// evidenceExpired is what a tombstone looks like: the row stays, the text is gone.
	evidenceExpired evidenceState = "expired"
	// evidenceMissing is a citation pointing at an id that was never there — the
	// shape a hard delete by an older release left behind.
	evidenceMissing evidenceState = "missing"
	// evidenceNoQuote has a live snapshot but an empty excerpt, which is the only
	// path that legitimately produces `not_provided` for an existing citation.
	evidenceNoQuote evidenceState = "no_quote"
	// evidenceDue is past its `expires_at` with the janitor not yet run: the content
	// is still there, so the citation is still checkable. It exists to pin the rule
	// that `evidence_expired` is a fact of the purge, never a recomputation from the
	// clock or from the retention setting currently in force (doc/319 invariant ①).
	evidenceDue evidenceState = "due"
)

func (fx *evidenceLifecycleFixture) apply(t *testing.T, state evidenceState) {
	t.Helper()
	now := time.Now().UTC()
	switch state {
	case evidenceLive:
		return
	case evidenceExpired:
		if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).
			Where("id = ?", fx.snapshotID).
			Updates(map[string]interface{}{
				"content": "", "content_size": int64(0),
				"expires_at": now.Add(-24 * time.Hour), "purged_at": now,
				"purged_reason": model.ReportEvidencePurgeRetentionExpired,
			}).Error; err != nil {
			t.Fatalf("tombstone snapshot: %v", err)
		}
	case evidenceMissing:
		if err := fx.gdb.Where("id = ?", fx.snapshotID).
			Delete(&model.ReportEvidenceSnapshot{}).Error; err != nil {
			t.Fatalf("delete snapshot: %v", err)
		}
	case evidenceDue:
		if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).
			Where("id = ?", fx.snapshotID).
			Update("expires_at", now.Add(-24*time.Hour)).Error; err != nil {
			t.Fatalf("age the snapshot: %v", err)
		}
	case evidenceNoQuote:
		if err := fx.gdb.Model(&model.ReportCitation{}).
			Where("id = ?", fx.citationID).
			Update("snippet", "").Error; err != nil {
			t.Fatalf("blank snippet: %v", err)
		}
	default:
		t.Fatalf("unknown evidence state %q", state)
	}
}

type evidenceLifecycleFixture struct {
	*verdictReceiptFixture
	snapshotID string
	citationID string
}

// newEvidenceLifecycleFixture reuses the receipt fixture — one published revision,
// one claim, one citation, one snapshot — because the defects are about the *wiring*
// between the purge, the quote layer and the clamp, not about report assembly.
func newEvidenceLifecycleFixture(t *testing.T, name string) *evidenceLifecycleFixture {
	t.Helper()
	fx := newVerdictReceiptFixture(t, name)
	return &evidenceLifecycleFixture{
		verdictReceiptFixture: fx,
		snapshotID:            "snap-receipt-" + name,
		citationID:            "cite-receipt-" + name,
	}
}

func (fx *evidenceLifecycleFixture) onlyEvaluation(t *testing.T, runID string) model.ReportClaimEvaluation {
	t.Helper()
	var rows []model.ReportClaimEvaluation
	if err := fx.gdb.Where("run_id = ?", runID).Find(&rows).Error; err != nil {
		t.Fatalf("load evaluations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("evaluation rows = %d, want 1", len(rows))
	}
	return rows[0]
}

func (fx *evidenceLifecycleFixture) findings(t *testing.T, runID string) []model.ReportReviewFinding {
	t.Helper()
	var rows []model.ReportReviewFinding
	if err := fx.gdb.Where("run_id = ?", runID).Find(&rows).Error; err != nil {
		t.Fatalf("load findings: %v", err)
	}
	return rows
}

// TestUnreadableEvidenceDoesNotAccuseTheAuthor is R-01.
//
// The two states are asserted together because they share the symptom the defect
// produced (`not_provided`) and differ in the answer they must give. A test that only
// checked "not not_provided" would pass with one of them silently degraded.
func TestUnreadableEvidenceDoesNotAccuseTheAuthor(t *testing.T) {
	cases := []struct {
		state        evidenceState
		wantStatus   string
		wantReason   string
		wantSeverity string
	}{
		{evidenceExpired, model.ReportQuoteStatusEvidenceExpired, model.ReportInsufficientExpired, model.ReportSeverityMedium},
		{evidenceMissing, model.ReportQuoteStatusEvidenceMissing, model.ReportInsufficientEvidenceMissing, model.ReportSeverityHigh},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.state), func(t *testing.T) {
			fx := newEvidenceLifecycleFixture(t, string(testCase.state))
			fx.apply(t, testCase.state)
			judge := &countingEvaluator{verdict: model.ReportVerdictSupported}
			run := fx.runWith(t, "run-lifecycle-"+string(testCase.state), judge)

			row := fx.onlyEvaluation(t, run.ID)
			if row.QuoteStatus != testCase.wantStatus {
				t.Fatalf("quote_status = %q, want %q: reporting %q here tells the author "+
					"they cited nothing when the product is what removed the evidence",
					row.QuoteStatus, testCase.wantStatus, row.QuoteStatus)
			}
			if row.SupportVerdict != model.ReportVerdictInsufficient {
				t.Fatalf("support_verdict = %q, want %q", row.SupportVerdict, model.ReportVerdictInsufficient)
			}
			if row.InsufficientReason != testCase.wantReason {
				t.Fatalf("insufficient_reason = %q, want %q", row.InsufficientReason, testCase.wantReason)
			}
			if row.Method != model.ReportEvaluationMethodRule || row.ModelID != "" {
				t.Fatalf("method=%q model_id=%q: no judge may be named for a claim nobody judged",
					row.Method, row.ModelID)
			}
			if judge.calls != 0 {
				t.Fatalf("the semantic layer ran %d time(s) on unreadable evidence: an empty "+
					"【证据原文】 buys a verdict that cannot be reproduced or blamed", judge.calls)
			}
			if row.TokensIn != 0 || row.TokensOut != 0 {
				t.Fatalf("tokens in/out = %d/%d, want 0/0 with no model call", row.TokensIn, row.TokensOut)
			}
			if run.UsedTokens != 0 {
				t.Fatalf("run used_tokens = %d, want 0", run.UsedTokens)
			}

			rows := fx.findings(t, run.ID)
			if len(rows) != 1 {
				t.Fatalf("findings = %d, want 1: an unreadable citation must stay actionable", len(rows))
			}
			if rows[0].Category != model.ReportFindingTraceability {
				t.Fatalf("finding category = %q, want %q: nothing was judged, so the finding "+
					"must be about traceability and not imply a verdict",
					rows[0].Category, model.ReportFindingTraceability)
			}
			if rows[0].Severity != testCase.wantSeverity {
				t.Fatalf("finding severity = %q, want %q", rows[0].Severity, testCase.wantSeverity)
			}
			if rows[0].Source != "rule" {
				t.Fatalf("finding source = %q, want %q: no model answered, and a finding "+
					"that names one sends the reviewer to a layer that never saw the claim",
					rows[0].Source, "rule")
			}
		})
	}
}

// TestLiveEvidenceStillReachesTheJudge is the control for the test above: the
// blocking branch must be about the *absence* of readable evidence, so a live
// snapshot has to keep spending the model call. Without this, "no model call" could
// be satisfied by a pipeline that never calls the model at all.
func TestLiveEvidenceStillReachesTheJudge(t *testing.T) {
	fx := newEvidenceLifecycleFixture(t, "live")
	judge := &countingEvaluator{verdict: model.ReportVerdictSupported}
	run := fx.runWith(t, "run-lifecycle-live", judge)

	row := fx.onlyEvaluation(t, run.ID)
	if row.QuoteStatus != model.ReportQuoteStatusVerified {
		t.Fatalf("quote_status = %q, want %q", row.QuoteStatus, model.ReportQuoteStatusVerified)
	}
	if judge.calls != 1 {
		t.Fatalf("the semantic layer ran %d time(s), want 1", judge.calls)
	}
	if row.SupportVerdict != model.ReportVerdictSupported {
		t.Fatalf("support_verdict = %q, want %q", row.SupportVerdict, model.ReportVerdictSupported)
	}
	if row.Method != model.ReportEvaluationMethodSemantic {
		t.Fatalf("method = %q, want %q", row.Method, model.ReportEvaluationMethodSemantic)
	}
}

// TestExpiredMeansPurgedNotPastDue pins invariant ① of batch 1: the classification
// is read from the tombstone, so a row whose `expires_at` has passed but which the
// janitor has not reached yet stays reviewable — and a run already stored as
// `not_provided` is never re-derived after an operator changes the setting.
//
// The two ways to get this wrong are both attractive: comparing `expires_at` with
// `now` at review time (then lowering `report.evidence_retention_days` rewrites the
// meaning of every historical run) and re-running the classifier over old rows. The
// first is caught here; the second has no code path to catch, because the status is
// written once per evaluation and nothing recomputes it.
func TestExpiredMeansPurgedNotPastDue(t *testing.T) {
	fx := newEvidenceLifecycleFixture(t, "due")
	fx.apply(t, evidenceDue)
	judge := &countingEvaluator{verdict: model.ReportVerdictSupported}
	run := fx.runWith(t, "run-lifecycle-due", judge)

	row := fx.onlyEvaluation(t, run.ID)
	if row.QuoteStatus != model.ReportQuoteStatusVerified {
		t.Fatalf("quote_status = %q, want %q: the content is still readable, so nothing was "+
			"cleared and the claim is still checkable — only the purge turns a snapshot into "+
			"`evidence_expired`", row.QuoteStatus, model.ReportQuoteStatusVerified)
	}
	if judge.calls != 1 {
		t.Fatalf("the semantic layer ran %d time(s), want 1: a past due date is not the same "+
			"fact as cleared content", judge.calls)
	}
	if row.SupportVerdict != model.ReportVerdictSupported {
		t.Fatalf("support_verdict = %q, want %q", row.SupportVerdict, model.ReportVerdictSupported)
	}
}

// TestNegativeVerdictSurvivesAMissingQuote is R-02, and it is the case the 144-cell
// sweep could not see: the sweep ranked `abstained` *below* `unsupported` on a single
// support axis, so washing a rejection into an abstention read as a legal descent.
func TestNegativeVerdictSurvivesAMissingQuote(t *testing.T) {
	fx := newEvidenceLifecycleFixture(t, "noquote")
	fx.apply(t, evidenceNoQuote)
	judge := &countingEvaluator{verdict: model.ReportVerdictUnsupported}
	run := fx.runWith(t, "run-lifecycle-noquote", judge)

	// The quote state is the one the defect keyed on: a live snapshot, an empty
	// excerpt, `fact` + `requires_evidence`.
	row := fx.onlyEvaluation(t, run.ID)
	if row.QuoteStatus != model.ReportQuoteStatusNotProvided {
		t.Fatalf("quote_status = %q, want %q (fixture precondition)",
			row.QuoteStatus, model.ReportQuoteStatusNotProvided)
	}
	if row.SupportVerdict != model.ReportVerdictUnsupported {
		t.Fatalf("support_verdict = %q, want %q: a missing quote may stop a claim being "+
			"certified, never stop it being rejected",
			row.SupportVerdict, model.ReportVerdictUnsupported)
	}
	if row.AbstainReason != "" {
		t.Fatalf("abstain_reason = %q, want empty: the evaluator did not abstain", row.AbstainReason)
	}
	rows := fx.findings(t, run.ID)
	if len(rows) != 1 {
		t.Fatalf("findings = %d, want 1: erasing the verdict also erased the finding, which "+
			"is how a rejected claim disappeared from the review workspace", len(rows))
	}
	if rows[0].Category != model.ReportFindingUnsupported {
		t.Fatalf("finding category = %q, want %q", rows[0].Category, model.ReportFindingUnsupported)
	}
	if rows[0].ModelID != "stub-model" {
		t.Fatalf("finding model_id = %q, want the judge that answered", rows[0].ModelID)
	}
}

// TestClampOnlyRestrictsPositiveVerdicts pins the rule at the seam itself, one
// verdict and one quote state at a time, so the pipeline test above cannot be the
// only thing holding it.
func TestClampOnlyRestrictsPositiveVerdicts(t *testing.T) {
	negatives := []string{
		model.ReportVerdictUnsupported, model.ReportVerdictConflicting,
		model.ReportVerdictOutdated, model.ReportVerdictInsufficient,
	}
	states := []struct {
		status string
		check  reportQuoteCheck
	}{
		{reportQuoteNotProvided, reportQuoteCheck{Status: reportQuoteNotProvided, SpanMap: reportSpanMapNone}},
		{reportQuoteNotFound, reportQuoteCheck{Status: reportQuoteNotFound, SpanMap: reportSpanMapNone}},
		{reportQuoteEvidenceExpired, reportQuoteCheck{Status: reportQuoteEvidenceExpired, SpanMap: reportSpanMapNone}},
		{reportQuoteEvidenceMissing, reportQuoteCheck{Status: reportQuoteEvidenceMissing, SpanMap: reportSpanMapNone}},
	}
	for _, state := range states {
		for _, verdict := range negatives {
			got, reason := reportClampVerdict(verdict, state.check, "fact", true)
			if got != verdict || reason != "" {
				t.Fatalf("clamp(%s, %s) = %q/%q, want %q with no abstain reason: a negative "+
					"verdict must survive every quote problem", state.status, verdict, got, reason, verdict)
			}
		}
		// And the positive side must still be restricted — otherwise "negative
		// survives" would be satisfiable by deleting the clamp altogether.
		for _, verdict := range []string{model.ReportVerdictSupported, model.ReportVerdictPartiallySupported} {
			got, _ := reportClampVerdict(verdict, state.check, "fact", true)
			if got == model.ReportVerdictSupported {
				t.Fatalf("clamp(%s, %s) kept %q: a claim that cannot be verified must not stay "+
					"certified", state.status, verdict, model.ReportVerdictSupported)
			}
		}
	}
}

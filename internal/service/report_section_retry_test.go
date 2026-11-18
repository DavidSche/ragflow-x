package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// doc/288 §7.9: `rgx_report_section.attempt` counts one section's model calls and
// allows ≤ 2 of them *inside the same slice*. The live environment is why this
// acceptance exists: one 60 s read timeout on the chat route closed the section on
// attempt 1, which skipped every section depending on it, and published a 3/5
// report — two sections lost to a single transient stall.
func TestReportSectionRetriesATransientModelFailure(t *testing.T) {
	fx, engine := newReportRunFixture(t, "section-retry-ok")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-section-retry-ok", "req-section-retry-ok-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	engine.chatErrs = []error{
		&ragflow.Error{
			Type: ragflow.ErrorTypeTimeout, Message: "context deadline exceeded",
		},
		nil, // the second call answers normally
	}
	engine.replies = []string{
		`{"answer":"依据《差旅管理办法》第 12 条，住宿标准为每晚上限 600 元 [1]。",` +
			`"usage":{"prompt_tokens":100,"completion_tokens":50}}`,
	}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if sections[0].Status != model.ReportSectionSucceeded {
		t.Fatalf("the retried section must succeed, got %q", sections[0].Status)
	}
	if sections[0].Attempt != 2 {
		t.Fatalf("attempt must count both calls (2), got %d", sections[0].Attempt)
	}
	// The retry is one section's second attempt, not a second section: a dependent
	// section must still be written instead of cascading into a skipped gap.
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded {
			t.Fatalf("section %s must be written, got %q", section.SectionKey, section.Status)
		}
	}

	// Each call is its own event, otherwise the timeline would claim one call.
	started := fx.reportEventsOf(t, created.Job.ID, reportEventSectionStarted)
	attempts := make([]int, 0, len(started))
	for _, event := range started {
		var payload struct {
			Attempt   int    `json:"attempt"`
			SectionID string `json:"section_id"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("event payload: %v (%s)", err, event.PayloadJSON)
		}
		if payload.SectionID != sections[0].ID {
			continue
		}
		attempts = append(attempts, payload.Attempt)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("expected attempts [1 2] for the retried section, got %v", attempts)
	}

	// Billing: the unanswered call is an orphan (we do not know what it cost), the
	// answered one is settled. Two rows, two ledger keys — a retry that reused the
	// key would be silently unrecorded (A36).
	var charges []model.ReportCharge
	if err := fx.gdb.Where("report_job_id = ? AND section_id = ?", created.Job.ID, sections[0].ID).
		Order("call_seq asc").Find(&charges).Error; err != nil {
		t.Fatalf("load charges: %v", err)
	}
	if len(charges) != 2 {
		t.Fatalf("expected one ledger row per call (2), got %d", len(charges))
	}
	if charges[0].State != model.ReportChargeOrphan {
		t.Fatalf("the timed-out call must be orphaned, got %q", charges[0].State)
	}
	if charges[1].State != model.ReportChargeSettled {
		t.Fatalf("the answered call must be settled, got %q", charges[1].State)
	}
	if charges[0].LedgerKey == charges[1].LedgerKey {
		t.Fatal("both calls share one ledger key: the second call was never recorded")
	}
}

// A business refusal is the provider answering "no": retrying it would only burn
// budget on the same refusal. `attempt` must stay 1 and the section must be closed
// after exactly one call.
func TestReportSectionDoesNotRetryABusinessRefusal(t *testing.T) {
	fx, engine := newReportRunFixture(t, "section-retry-refused")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-section-retry-refused", "req-section-retry-ref-1")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	engine.chatErrs = []error{&ragflow.Error{
		Type: ragflow.ErrorTypeBusiness, HTTPStatus: 400, Code: 1001,
		Message: "model does not exist",
	}}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if sections[0].Status != model.ReportSectionFailed {
		t.Fatalf("a refused section must be failed, got %q", sections[0].Status)
	}
	if sections[0].Attempt != 1 {
		t.Fatalf("a business refusal must not be retried, attempt=%d", sections[0].Attempt)
	}
}

// The retry is bounded by `reportSectionMaxAttempts`: a provider that never
// answers must not turn one section into an unbounded call loop.
func TestReportSectionStopsRetryingAtTheAttemptCeiling(t *testing.T) {
	fx, engine := newReportRunFixture(t, "section-retry-ceiling")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-section-retry-ceiling", "req-section-retry-max-1")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	// A single-error slice repeats forever in this mock, so a missing ceiling would
	// spin until the test times out.
	engine.chatErrs = []error{&ragflow.Error{
		Type: ragflow.ErrorTypeTimeout, Message: "context deadline exceeded",
	}}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if sections[0].Attempt != reportSectionMaxAttempts {
		t.Fatalf("attempt must stop at the ceiling %d, got %d",
			reportSectionMaxAttempts, sections[0].Attempt)
	}
	if sections[0].Status != model.ReportSectionFailed {
		t.Fatalf("a section out of attempts must be failed, got %q", sections[0].Status)
	}
}

package service

// doc/296 §2.2 / A71: the detail read publishes the live run's spend so the
// extension card can quote the server's envelope *and its bound*. Two facts this
// test pins, both of which a UI would otherwise have to guess:
//
//   - `max_extension_tokens` is `reportBudgetExtensionMaxFactor` of the envelope,
//     computed here and not in TypeScript (one governance rule, one place);
//   - a report with no *live* job publishes nothing, because an extension on a
//     finalized run is refused (44104) and offering the form would be a dead end.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportDetailPublishesTheLiveRunBudget(t *testing.T) {
	fx := newReportFixture(t, "detail-budget")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-detail-budget-0001")
	fx.setEnvelope(t, created.Job.ID, 5000)

	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if detail.Budget == nil {
		t.Fatal("a job that has not finalized must publish its spend")
	}
	if detail.Budget.BudgetTokens != 5000 || detail.Budget.SpentTokens != 0 {
		t.Fatalf("unexpected envelope: budget=%d spent=%d",
			detail.Budget.BudgetTokens, detail.Budget.SpentTokens)
	}
	// 5000 * 1: one request may add the envelope again, never more.
	if detail.Budget.MaxExtensionTokens != 5000 {
		t.Fatalf("max extension must be the envelope itself, got %d",
			detail.Budget.MaxExtensionTokens)
	}
	// The job froze `enforce` at creation; the detail reports the frozen value, not
	// whatever the tenant switch says now.
	if detail.Budget.BudgetPolicy != model.ReportBudgetEnforce || detail.Budget.Observing {
		t.Fatalf("unexpected policy: %q observing=%v",
			detail.Budget.BudgetPolicy, detail.Budget.Observing)
	}

	// Spend part of it: the published numbers move with the ledger, and nothing
	// about the envelope does.
	if _, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 1200), time.Now().UTC()); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	detail, err = fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("re-read detail: %v", err)
	}
	if detail.Budget.AvailableTokens != 3800 {
		t.Fatalf("available must be envelope - held, got %d", detail.Budget.AvailableTokens)
	}

	// A finalized run publishes nothing: it cannot be extended, so the card must say
	// "no live run" instead of offering a request the service would reject.
	finalized := time.Now().UTC()
	if err := fx.gdb.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", fx.tenantID, created.Job.ID).
		Update("finalized_at", finalized).Error; err != nil {
		t.Fatalf("finalize job: %v", err)
	}
	detail, err = fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("read detail after finalize: %v", err)
	}
	if detail.Budget != nil {
		t.Fatalf("a finalized job must not publish a budget block, got %+v", detail.Budget)
	}
}

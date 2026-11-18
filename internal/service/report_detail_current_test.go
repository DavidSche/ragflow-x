package service

// A32 / doc/288 §7.10「前端语义」: 「报告详情页永远展示 current_revision_id 的内容」。
//
// Live full7 caught the read aggregate answering the *newest* revision instead:
// after regenerating v3 and cancelling it, `GET /reports/:id` returned
// `revision=3 cancelled`, so the user's published v2 disappeared from the page
// (and the artifact list came from the revision that has none). The pointer is
// only moved by the publish transaction, which makes the read a one-line rule:
// resolve `current_revision_id`, never `ORDER BY revision DESC`.

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportDetailShowsTheCurrentRevisionNotTheLatestAttempt(t *testing.T) {
	fx := newReportFixture(t, "detail-current")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}

	// A published report is its own current revision.
	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if detail.Revision.ID != base.ID {
		t.Fatalf("detail must show the current revision %d, got %d",
			base.Revision, detail.Revision.Revision)
	}

	// Start a regeneration and cancel it: the attempt must not become the detail.
	regenerated, err := regenerateReport(t, fx, report.ID, "req-detail-current-0001")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if regenerated.Revision.Revision != base.Revision+1 {
		t.Fatalf("regeneration must mint revision %d", base.Revision+1)
	}
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("id = ?", regenerated.Revision.ID).
		Update("status", model.ReportRevisionCancelled).Error; err != nil {
		t.Fatalf("cancel the attempt: %v", err)
	}

	current, err := fx.svc.Store.GetReport(ctx, fx.tenantID, report.ID)
	if err != nil || current == nil {
		t.Fatalf("reload report: %v", err)
	}
	if current.CurrentRevisionID != base.ID {
		t.Fatalf("the publish pointer must not move without a publish: got %q", current.CurrentRevisionID)
	}
	detail, err = fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("re-read detail: %v", err)
	}
	if detail.Revision.ID != base.ID || detail.Revision.Revision != base.Revision {
		t.Fatalf("detail must still show revision %d, got %d/%q",
			base.Revision, detail.Revision.Revision, detail.Revision.Status)
	}
	if detail.Revision.Status != model.ReportRevisionPublished {
		t.Fatalf("the published base must stay published, got %q", detail.Revision.Status)
	}
}

// The fallback is not decoration: a row whose pointer is empty must still resolve
// something, otherwise the detail page 404s on a report that exists.
func TestReportDetailFallsBackToTheNewestRevisionWithoutAPointer(t *testing.T) {
	fx := newReportFixture(t, "detail-pointerless")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-detail-pointerless-0001")
	if err := fx.gdb.Model(&model.Report{}).
		Where("id = ?", created.Report.ID).
		Update("current_revision_id", "").Error; err != nil {
		t.Fatalf("clear pointer: %v", err)
	}

	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if detail.Revision == nil || detail.Revision.ID != created.Revision.ID {
		t.Fatalf("the only revision must be resolved, got %+v", detail.Revision)
	}
}

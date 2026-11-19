package service

// 「列表的状态列必须说交付状态，不能说生命周期标记」。
//
// `report.status` 只有一个取值 `active`（未删除），从创建到删除都不变。列表页把
// 它渲染成「进行中」，于是一份上周就交付完成的报告，在列表里一直写着「仍然在
// 生成」——这既是错的，也是最容易让用户怀疑「是不是卡住了」的一句话。真实环境
// 2026-11-18 的验证过程里直接看到了这个现象（doc/44）。
//
// 交付状态属于**当前 revision**，所以列表行必须带上它；测试钉两件事：
//  ① 已发布报告的行上，`revision_status=published` 且 `revision`/`completeness`
//     与详情读到的一致；
//  ② 列表行的生命周期标记仍是 `active`（别把两件事揉成一个字段），并且一轮再生
//     成把指针留在已发布基上时，列表跟着指针走，不跟着「最新 attempt」走。

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportListCarriesTheCurrentRevisionDeliveryState(t *testing.T) {
	fx := newReportFixture(t, "list_status")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())

	rows, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, "")
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("expected exactly one row, got total=%d rows=%d", total, len(rows))
	}
	row := rows[0]

	// ① Delivery state rides the row, and it agrees with the detail read.
	if row.RevisionStatus != model.ReportRevisionPublished {
		t.Fatalf("list row must carry the current revision status, got %q", row.RevisionStatus)
	}
	if row.Revision != revision.Revision {
		t.Fatalf("list row revision=%d, want %d", row.Revision, revision.Revision)
	}
	if row.Completeness != revision.Completeness {
		t.Fatalf("list row completeness=%q, want %q", row.Completeness, revision.Completeness)
	}
	if row.PublishedAt == nil || revision.PublishedAt == nil || !row.PublishedAt.Equal(*revision.PublishedAt) {
		t.Fatalf("list row published_at=%v, want %v", row.PublishedAt, revision.PublishedAt)
	}

	// ② The lifecycle flag stays itself: `active` means "not deleted", and a list
	// that overwrote it with a delivery state would lose the only fact it holds.
	if row.Status != model.ReportStatusActive {
		t.Fatalf("lifecycle status must stay %q, got %q", model.ReportStatusActive, row.Status)
	}
}

// A regenerate that fails leaves the pointer on the published base (A32). The list
// must follow the pointer: a row that reported the failed attempt's status would
// tell the reader their delivered report is broken.
func TestReportListFollowsThePointerNotTheNewestAttempt(t *testing.T) {
	fx := newReportFixture(t, "list_status_pointer")
	ctx := context.Background()
	published := publishReport(t, fx, successSeeds())

	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, published.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}
	// A second revision that dies before publishing: the pointer never moves off
	// the published base.
	failed := &model.ReportRevision{
		ID:                  "rev-list-failed-0001",
		ReportID:            report.ID,
		TenantID:            fx.tenantID,
		Revision:            published.Revision + 1,
		TemplateKey:         published.TemplateKey,
		TemplateVersion:     published.TemplateVersion,
		OutlineConfirmation: "required",
		Status:              model.ReportRevisionFailed,
		CreatedBy:           fx.owner.ID,
	}
	if err := fx.svc.Store.CreateReportRevision(ctx, failed); err != nil {
		t.Fatalf("create failed revision: %v", err)
	}

	rows, _, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, "")
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if rows[0].RevisionStatus != model.ReportRevisionPublished {
		t.Fatalf("row must report the published base (%s), got %q",
			model.ReportRevisionPublished, rows[0].RevisionStatus)
	}
	if rows[0].Revision != published.Revision {
		t.Fatalf("row revision=%d, want the published %d", rows[0].Revision, published.Revision)
	}
}

package service

// A18 (doc/288 §8.6 / §14.3) — "同一章节重复提交不产生重复版本、不重复扣费
// （同 ledger_key 二次提交只结算一次）".
//
// A18 is about the two replay paths a worker can take after a crash or a duplicated
// runtime message:
//
//   - content: the runner commits the same section row twice. The second commit must
//     be a no-op — no second row, no second version, no overwrite of the content that
//     already justified `succeeded`, and no second `progress_done` increment.
//   - money: the runner settles the same `ledger_key` twice (a retried HTTP call, a
//     re-delivered job). Only the first transition from `reserved` may apply.
//
// `TestSettleReportChargeRejectsDoubleSettle` covers the second half in isolation; the
// test below drives both halves over one section, which is the shape a real replay has
// (the charge and the content arrive from the same retried step).

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestDuplicateSectionCommitWritesOneVersionAndSettlesOneCharge(t *testing.T) {
	fx := newReportFixture(t, "a18")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-a18-0001")
	now := time.Now().UTC()

	// One pending section row, the state `CreateReport` leaves behind.
	section := &model.ReportSection{
		ID: id.New(), TenantID: fx.tenantID, RevisionID: created.Revision.ID,
		Ord: 1, SectionKey: "background", SectionVersion: 1,
		Title: "背景与范围", RequiresEvidence: false,
		Status: model.ReportSectionPending,
	}
	if err := fx.gdb.Create(section).Error; err != nil {
		t.Fatal(err)
	}
	if applied, err := fx.svc.Store.BeginReportSection(ctx, repository.ReportSectionStart{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionID: section.ID, PreviousAttempt: 0, Now: now,
	}); err != nil || !applied {
		t.Fatalf("begin section: applied=%v err=%v", applied, err)
	}

	commit := func(content, hash string) bool {
		t.Helper()
		written := *section
		written.Attempt = 1
		written.ContentMD = content
		written.ContentHash = hash
		written.TokensIn = 120
		written.TokensOut = 340
		written.QualityStatus = model.ReportQualityPassed
		written.EvidenceStatus = model.ReportEvidenceSupported
		applied, err := fx.svc.Store.CommitReportSection(ctx, repository.ReportSectionCommit{
			TenantID: fx.tenantID, JobID: created.Job.ID, Now: now, Section: written,
		})
		if err != nil {
			t.Fatalf("commit section: %v", err)
		}
		return applied
	}

	if !commit("# 背景与范围\n\n第一版正文。", "hash-a18-v1") {
		t.Fatal("the first commit must apply")
	}
	// A re-delivered message carrying *different* text must still be a no-op: the row
	// already left `writing`, and the version that justified it is frozen.
	if commit("# 背景与范围\n\n重复投递的正文。", "hash-a18-v2") {
		t.Fatal("a duplicate commit must not apply")
	}

	// Content: one live version, still the first text.
	var rows []model.ReportSection
	if err := fx.gdb.Where("tenant_id = ? AND revision_id = ? AND ord = ?", fx.tenantID, created.Revision.ID, 1).
		Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("a duplicate commit must not add a version, got %d rows", len(rows))
	}
	if rows[0].ContentHash != "hash-a18-v1" || rows[0].SectionVersion != 1 ||
		rows[0].Status != model.ReportSectionSucceeded || rows[0].Attempt != 1 {
		t.Fatalf("the duplicate must leave the original content in place, got %+v", rows[0])
	}

	// Progress: one section counted once. A double increment would make progress_done
	// overshoot progress_total and stall the completion gate.
	job := fx.reportJob(t, created.Job.ID)
	if job.ProgressDone != 1 {
		t.Fatalf("progress_done must count the section once, got %d/%d",
			job.ProgressDone, job.ProgressTotal)
	}

	// Money: one reservation, one settle, and a replay of the same ledger key refused.
	reserved, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 900), now)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, reserved.LedgerKey, 420, now); err != nil || !applied {
		t.Fatalf("first settle: applied=%v err=%v", applied, err)
	}
	// A replay is refused *loudly* (44112): silently ignoring it would leave the
	// second caller believing it billed 420 more tokens.
	applied, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, reserved.LedgerKey, 420, now)
	if applied || err == nil {
		t.Fatalf("a replayed settle must be refused, got applied=%v err=%v", applied, err)
	}
	if code := reportErrorCode(err); code != codeReportLedgerInconsistent {
		t.Fatalf("a double settle is 44112, got %d (%v)", code, err)
	}
	charge := fx.chargeRow(t, reserved.LedgerKey)
	if charge.State != model.ReportChargeSettled || charge.ActualTokens != 420 {
		t.Fatalf("the ledger must keep the first settle, got %+v", charge)
	}
	var chargeRows int64
	if err := fx.gdb.Model(&model.ReportCharge{}).
		Where("tenant_id = ? AND report_job_id = ?", fx.tenantID, created.Job.ID).
		Count(&chargeRows).Error; err != nil {
		t.Fatal(err)
	}
	if chargeRows != 1 {
		t.Fatalf("a replayed settle must not mint a second ledger row, got %d", chargeRows)
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Actual != 420 || sums.Charged != 420 || sums.Held != 0 {
		t.Fatalf("a replayed settle must not move the sums, got %+v", sums)
	}
}

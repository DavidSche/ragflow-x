package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// retentionStore reuses the busy-timeout SQLite builder from audit_test.go.
func retentionStore(t *testing.T) *store {
	t.Helper()
	st := newAuditStore(t).(*store)
	return st
}

// TestPurgeAuditReanchorsChain builds a real hash chain, purges the oldest
// rows, re-anchors the survivors and appends another record; the resulting
// chain must still be gapless and hash-valid (VerifyAuditChain semantics).
func TestPurgeAuditReanchorsChain(t *testing.T) {
	st := retentionStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := st.CreateAudit(ctx, &model.AuditLog{TenantID: "t1", UserID: "u", Action: "x", Resource: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	rows := []model.AuditLog{}
	if err := st.DB.Where("tenant_id = ?", "t1").Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 seeded audit rows, got %d", len(rows))
	}
	old := time.Now().UTC().AddDate(0, 0, -400)
	for i := 0; i < 3; i++ {
		if err := st.DB.Model(&model.AuditLog{}).Where("id = ?", rows[i].ID).Update("at", old).Error; err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := st.PurgeAuditBefore(ctx, time.Now().UTC().AddDate(0, 0, -365))
	if err != nil {
		t.Fatal(err)
	}
	if deleted["t1"] != 3 {
		t.Fatalf("expected 3 deleted audit rows for t1, got %d", deleted["t1"])
	}
	if err := st.ReanchorAuditChains(ctx, []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAudit(ctx, &model.AuditLog{TenantID: "t1", UserID: "u", Action: "after", Resource: "r"}); err != nil {
		t.Fatal(err)
	}
	// survivors 2 + the appended record = 3, chain must verify.
	verifyTenantChain(t, st, "t1", 3)
}

// TestReanchorSkipsEmptyAndUnknownTenant ensures the re-anchor helper tolerates
// an empty/unknown tenant without corrupting unaffected chains.
func TestReanchorSkipsEmptyAndUnknownTenant(t *testing.T) {
	st := retentionStore(t)
	ctx := context.Background()
	if err := st.CreateAudit(ctx, &model.AuditLog{TenantID: "t2", UserID: "u", Action: "x", Resource: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReanchorAuditChains(ctx, []string{"", "no-such-tenant"}); err != nil {
		t.Fatal(err)
	}
	verifyTenantChain(t, st, "t2", 1)
}

// TestPurgeUsageFeedbackAndMeter verifies per-tenant deleted counts and that
// young rows survive each usage/feedback purge.
func TestPurgeUsageFeedbackAndMeter(t *testing.T) {
	st := retentionStore(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -200)
	recent := time.Now().UTC().Add(-time.Hour)
	before := time.Now().UTC().AddDate(0, 0, -180)

	seed := []*model.CostMetric{
		{ID: "cm-id-1", RequestID: "cm-old-1", TenantID: "t1", UserID: "u", Date: "2026-01-01", CreatedAt: old},
		{ID: "cm-id-2", RequestID: "cm-old-2", TenantID: "t1", UserID: "u", Date: "2026-01-02", CreatedAt: old},
		{ID: "cm-id-3", RequestID: "cm-new", TenantID: "t1", UserID: "u", Date: "2026-08-28", CreatedAt: recent},
	}
	for _, m := range seed {
		if err := st.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	cm, err := st.PurgeCostMetricBefore(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if cm["t1"] != 2 {
		t.Fatalf("cost_metric purge: expected 2, got %d", cm["t1"])
	}

	quota := []*model.QuotaUsage{
		{ID: "q-old-1", TenantID: "t1", UserID: "u1", KeyID: "k", Date: "2026-01-01", CreatedAt: old},
		{ID: "q-new", TenantID: "t1", UserID: "u1", KeyID: "k", Date: "2026-08-28", CreatedAt: recent},
	}
	for _, u := range quota {
		if err := st.DB.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	qu, err := st.PurgeQuotaUsageBefore(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if qu["t1"] != 1 {
		t.Fatalf("quota_usage purge: expected 1, got %d", qu["t1"])
	}

	meter := []*model.MeterRequest{
		{RequestID: "mr-old-1", TenantID: "t1", UserID: "u", KeyID: "k", Date: "2026-01-01", CreatedAt: old},
		{RequestID: "mr-new", TenantID: "t1", UserID: "u", KeyID: "k", Date: "2026-08-28", CreatedAt: recent},
	}
	for _, m := range meter {
		if err := st.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	mr, err := st.PurgeMeterRequestBefore(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if mr["t1"] != 1 {
		t.Fatalf("meter_request purge: expected 1, got %d", mr["t1"])
	}

	fb := []*model.MessageFeedback{
		{ID: "fb-old-1", TenantID: "t1", ChatID: "c", SessionID: "s", MessageID: "m-old-1", UserID: "u", Rating: "positive", CreatedAt: old},
		{ID: "fb-new", TenantID: "t1", ChatID: "c", SessionID: "s", MessageID: "m-new", UserID: "u", Rating: "positive", CreatedAt: recent},
	}
	for _, f := range fb {
		if err := st.DB.Create(f).Error; err != nil {
			t.Fatal(err)
		}
	}
	del, err := st.PurgeMessageFeedbackBefore(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if del["t1"] != 1 {
		t.Fatalf("message_feedback purge: expected 1, got %d", del["t1"])
	}

	// young rows must remain across all tables
	assertCount(t, st, &model.CostMetric{}, 1)
	assertCount(t, st, &model.QuotaUsage{}, 1)
	assertCount(t, st, &model.MeterRequest{}, 1)
	assertCount(t, st, &model.MessageFeedback{}, 1)
}

// TestPurgeTerminalJobsTasksOnly ensures retention only removes terminal job /
// task rows and never touches active (queued/running) ones.
func TestPurgeTerminalJobsTasksOnly(t *testing.T) {
	st := retentionStore(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -200)
	before := time.Now().UTC().AddDate(0, 0, -90)

	jobs := []*model.Job{
		{ID: "j-succ", Kind: "k", Key: "job-succ", TenantID: "t1", Status: model.JobStatusSucceeded, CreatedAt: old, UpdatedAt: old, RunAfter: old},
		{ID: "j-fail", Kind: "k", Key: "job-fail", TenantID: "t1", Status: model.JobStatusFailed, CreatedAt: old, UpdatedAt: old, RunAfter: old},
		{ID: "j-cancel", Kind: "k", Key: "job-cancel", TenantID: "t1", Status: model.JobStatusCanceled, CreatedAt: old, UpdatedAt: old, RunAfter: old},
		{ID: "j-queued", Kind: "k", Key: "job-queued", TenantID: "t1", Status: model.JobStatusQueued, CreatedAt: old, UpdatedAt: old, RunAfter: old},
		{ID: "j-running", Kind: "k", Key: "job-running", TenantID: "t1", Status: model.JobStatusRunning, CreatedAt: old, UpdatedAt: old, RunAfter: old},
	}
	for _, j := range jobs {
		if err := st.DB.Create(j).Error; err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.PurgeTerminalJobsBefore(ctx, before); err != nil {
		t.Fatal(err)
	} else if got["t1"] != 3 {
		t.Fatalf("terminal job purge: expected 3 (succ+fail+cancel), got %d", got["t1"])
	}

	tasks := []*model.Task{
		{ID: "t-done", TenantID: "t1", TaskType: "parse", Status: model.TaskStatusDone, CreatedAt: old, UpdatedAt: old},
		{ID: "t-fail", TenantID: "t1", TaskType: "parse", Status: model.TaskStatusFailed, CreatedAt: old, UpdatedAt: old},
		{ID: "t-stopped", TenantID: "t1", TaskType: "parse", Status: model.TaskStatusStopped, CreatedAt: old, UpdatedAt: old},
		{ID: "t-queued", TenantID: "t1", TaskType: "parse", Status: model.TaskStatusQueued, CreatedAt: old, UpdatedAt: old},
		{ID: "t-running", TenantID: "t1", TaskType: "parse", Status: model.TaskStatusRunning, CreatedAt: old, UpdatedAt: old},
	}
	for _, tsk := range tasks {
		if err := st.DB.Create(tsk).Error; err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.PurgeTerminalTasksBefore(ctx, before); err != nil {
		t.Fatal(err)
	} else if got["t1"] != 3 {
		t.Fatalf("terminal task purge: expected 3 (done+fail+stopped), got %d", got["t1"])
	}

	// active rows survive, terminal removed.
	assertCount(t, st, &model.Job{}, 2)
	assertCount(t, st, &model.Task{}, 2)
}

func assertCount(t *testing.T, st *store, model interface{}, want int64) {
	t.Helper()
	var n int64
	if err := st.DB.Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("expected %d rows for %T, got %d", want, model, n)
	}
}

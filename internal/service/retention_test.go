package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// newRetentionSvc builds a Service over a SQLite store with a busy timeout,
// so the running async worker and the retention purge do not trip SQLITE_BUSY
// in tests (serialized writes instead of immediate lock errors).
func newRetentionSvc(t *testing.T) *Service {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "retention.db") + "?_pragma=busy_timeout(10000)"
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

// seedRetentionRows writes one old and one fresh audit/cost/feedback row per
// tenant so every retention class has data to purge.
func seedRetentionRows(t *testing.T, svc *Service, tenantID string) {
	t.Helper()
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -400)
	recent := time.Now().UTC().Add(-time.Hour)

	if err := svc.Store.CreateAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: "u", Action: "seed.old", Resource: "r", At: old}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: "u", Action: "seed.new", Resource: "r", At: recent}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.RecordCostMetric(ctx, &model.CostMetric{RequestID: "svc-cm-old-" + tenantID, TenantID: tenantID, UserID: "u", Date: "2026-01-01", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.RecordCostMetric(ctx, &model.CostMetric{RequestID: "svc-cm-new-" + tenantID, TenantID: tenantID, UserID: "u", Date: "2026-08-28", CreatedAt: recent}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertMessageFeedback(ctx, &model.MessageFeedback{TenantID: tenantID, ChatID: "c", SessionID: "s", MessageID: "svc-fb-old-" + tenantID, UserID: "u", Rating: model.FeedbackPositive, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertMessageFeedback(ctx, &model.MessageFeedback{TenantID: tenantID, ChatID: "c", SessionID: "s", MessageID: "svc-fb-new-" + tenantID, UserID: "u", Rating: model.FeedbackPositive, CreatedAt: recent}); err != nil {
		t.Fatal(err)
	}
}

// TestRunRetentionDisabledNoOp verifies the master switch: disabled retention
// performs no deletion and the summary reports it as disabled.
func TestRunRetentionDisabledNoOp(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	ta, err := svc.CreateTenant(ctx, "TenantDisable")
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionRows(t, svc, ta.ID)

	sum, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Enabled {
		t.Fatal("retention must be disabled by default")
	}
	// nothing must have been deleted
	deleted, err := auditCounts(ctx, svc, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("disabled retention must not delete audit rows, got %d remaining (want 2)", deleted)
	}
}

// TestRunRetentionPurgeAllClasses verifies a full purge cycle: old rows are
// removed per class, the audit chain stays verifiable after re-anchoring, and
// a retention.purge audit record with the deleted counts is written.
func TestRunRetentionPurgeAllClasses(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	ta, err := svc.CreateTenant(ctx, "TenantRet")
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionRows(t, svc, ta.ID)

	svc.SetRetentionPolicy(config.Retention{
		Enabled: true, AuditDays: 1, UsageDays: 1, FeedbackDays: 1, JobDays: 1, IntervalSec: 60,
	})
	sum, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Enabled {
		t.Fatal("expected retention enabled")
	}
	if sum.Audit[ta.ID] != 1 {
		t.Fatalf("expected 1 audit row purged, got %d", sum.Audit[ta.ID])
	}
	if sum.Usage[ta.ID] < 1 {
		t.Fatalf("expected >=1 usage row purged, got %d", sum.Usage[ta.ID])
	}
	if sum.Feedback[ta.ID] != 1 {
		t.Fatalf("expected 1 feedback row purged, got %d", sum.Feedback[ta.ID])
	}

	// one fresh audit row must remain, and the chain must verify.
	deleted, err := auditCounts(ctx, svc, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 surviving audit rows (1 fresh + 1 purge record), got %d", deleted)
	}
	if ok, err := svc.VerifyAuditChain(ctx, ta.ID); err != nil || !ok {
		t.Fatalf("audit chain must verify after retention purge: ok=%v err=%v", ok, err)
	}

	// the purge must be audited with per-class deleted counts.
	rows, err := svc.Store.ListAuditsAll(ctx, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Action == "retention.purge" {
			found = true
			if !strings.Contains(r.DetailJSON, `"audit":1`) {
				t.Fatalf("purge audit detail missing audit count: %s", r.DetailJSON)
			}
		}
	}
	if !found {
		t.Fatal("no retention.purge audit record written")
	}
}

// TestRunRetentionSkipsDisabledClasses verifies per-class day=0 disables that
// class while others still purge.
func TestRunRetentionSkipsDisabledClasses(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	ta, err := svc.CreateTenant(ctx, "TenantPartial")
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionRows(t, svc, ta.ID)

	// only audit retention enabled; feedback/usage/job disabled -> only audit purged.
	svc.SetRetentionPolicy(config.Retention{
		Enabled: true, AuditDays: 1, UsageDays: 0, FeedbackDays: 0, JobDays: 0, IntervalSec: 60,
	})
	sum, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Usage[ta.ID] != 0 || sum.Feedback[ta.ID] != 0 {
		t.Fatalf("disabled classes must not purge: usage=%v feedback=%v", sum.Usage, sum.Feedback)
	}
	if sum.Audit[ta.ID] != 1 {
		t.Fatalf("expected 1 audit row purged, got %d", sum.Audit[ta.ID])
	}
}

// TestRetentionWorkerSelfSchedules runs the A5 janitor through the async
// runner: the recurring job purges old rows, self-schedules its next run, and
// writes the purge audit record (crash-safe recurring execution via A2 worker).
func TestRetentionWorkerSelfSchedules(t *testing.T) {
	svc := newRetentionSvc(t)
	ctx := context.Background()
	ta, err := svc.CreateTenant(ctx, "TenantWorker")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -400)
	for i := 0; i < 3; i++ {
		if err := svc.Store.CreateAudit(ctx, &model.AuditLog{TenantID: ta.ID, UserID: "u", Action: "seed", Resource: "r", At: old}); err != nil {
			t.Fatal(err)
		}
	}

	cfg := fastWorkerConfig()
	svc.SetupWorker(cfg)
	svc.SetupRetention(config.Retention{
		Enabled: true, AuditDays: 1, UsageDays: 1, FeedbackDays: 1, JobDays: 1, IntervalSec: 1,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Runner.Start(runCtx)
	defer func() { _ = svc.Runner.Stop(context.Background()) }()

	if inserted, err := svc.ScheduleRetention(ctx, ""); err != nil || !inserted {
		t.Fatalf("schedule retention: inserted=%v err=%v", inserted, err)
	}

	// The janitor runs (poll + run_after 1s), succeeds, and self-schedules.
	job := waitJobStatus(t, svc.Store, SystemTenantID, model.JobKindDataRetention, model.JobStatusSucceeded, 8*time.Second)
	if job.ID == "" {
		t.Fatal("no retention job id")
	}

	// All 3 old audit rows purged; the fresh retention.purge record remains.
	rows, err := svc.Store.ListAuditsAll(ctx, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "retention.purge" {
		t.Fatalf("expected exactly one retention.purge audit after worker run, got %+v", rows)
	}
	if ok, err := svc.VerifyAuditChain(ctx, ta.ID); err != nil || !ok {
		t.Fatalf("chain must verify after worker purge: ok=%v err=%v", ok, err)
	}

	// A next run must already be armed (queued) via the durable job table.
	active, err := svc.Store.CountActiveJobs(ctx, model.JobKindDataRetention, "")
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("expected exactly 1 queued next retention run, got %d", active)
	}
}

func auditCounts(ctx context.Context, svc *Service, tenantID string) (int, error) {
	rows, err := svc.Store.ListAuditsAll(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

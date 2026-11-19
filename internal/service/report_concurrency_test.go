package service

// Report-level admission control (doc/297 §3). The rule is "a workspace may have at
// most `ReportMaxConcurrentRuns` report slices executing", and the only interesting
// question is what happens to the next request. The answer, pinned here, is that
// nothing bad happens to it:
//
//   - the request is accepted, the report and revision exist, the token envelope is
//     frozen, the audit row is written;
//   - the job stays `queued` with no runtime pointer, and the answer carries the
//     concurrency snapshot so the UI can say "waiting behind N runs";
//   - the sweep arms it as soon as a slot frees — no second click, no re-submit;
//   - and the limit never touches the delivery path: an archived or published report
//     reads and exports while the workspace is saturated.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// reportSaturateWorkspace fills every execution slot of the fixture's workspace and
// returns the reports it created to do so.
//
// It counts first instead of assuming the workspace is empty: a fixture that published
// a report earlier still holds that report's first slice (the `report:<id>:1:1` job is
// never claimed by the test harness), and a helper that assumed zero would overflow its
// own limit on the third call and look like a bug in the limit.
func reportSaturateWorkspace(t *testing.T, fx *reportFixture) []CreateReportResult {
	t.Helper()
	ctx := context.Background()
	active, err := fx.svc.Store.CountActiveJobsExcept(ctx, model.JobKindReportGenerate, fx.tenantID, "")
	if err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if active >= int64(ReportMaxConcurrentRuns) {
		return nil
	}
	var created []CreateReportResult
	for index := active; index < int64(ReportMaxConcurrentRuns); index++ {
		result := fx.createReport(t, fx.datasetLink, "req-cc-slot-"+strconv.FormatInt(index, 10))
		if result.Concurrency != nil {
			t.Fatalf("create %d was deferred while slots were free: %+v", index, result.Concurrency)
		}
		if result.Job.Status != model.ReportJobRunning || result.Job.CurrentRuntimeJobID == "" {
			t.Fatalf("create %d did not start: status=%s pointer=%q",
				index, result.Job.Status, result.Job.CurrentRuntimeJobID)
		}
		created = append(created, result)
	}
	full, err := fx.svc.Store.CountActiveJobsExcept(ctx, model.JobKindReportGenerate, fx.tenantID, "")
	if err != nil || full != int64(ReportMaxConcurrentRuns) {
		t.Fatalf("workspace should be full: active=%d err=%v", full, err)
	}
	return created
}

func TestReportConcurrencyLimitDefersInsteadOfFailing(t *testing.T) {
	fx := newReportFixture(t, "concurrency_defer")
	ctx := context.Background()
	reportSaturateWorkspace(t, fx)

	// ① The over-limit request is accepted, not refused: a 429 here would throw away
	// a report the user asked for because somebody else's run was in flight.
	deferred := fx.createReport(t, fx.datasetLink, "req-cc-overflow")
	if deferred.Report == nil || deferred.Revision == nil || deferred.Job == nil {
		t.Fatalf("deferred create must still return the report: %+v", deferred)
	}
	if deferred.Job.Status != model.ReportJobQueued || deferred.Job.CurrentRuntimeJobID != "" {
		t.Fatalf("deferred job must stay queued and unarmed: status=%s pointer=%q",
			deferred.Job.Status, deferred.Job.CurrentRuntimeJobID)
	}
	// The envelope was frozen before admission: a waiting report has already been
	// costed, so "waiting" cannot quietly change into "different budget".
	if deferred.Job.ReservedTokens <= 0 {
		t.Fatalf("deferred job must carry its reserved envelope, got %d", deferred.Job.ReservedTokens)
	}

	// ② The answer says why, and with two facts rather than a queue position.
	if deferred.Concurrency == nil {
		t.Fatalf("deferred create must carry the concurrency snapshot")
	}
	if deferred.Concurrency.Limit != ReportMaxConcurrentRuns {
		t.Fatalf("limit=%d, want %d", deferred.Concurrency.Limit, ReportMaxConcurrentRuns)
	}
	if deferred.Concurrency.Active != int64(ReportMaxConcurrentRuns) {
		t.Fatalf("active=%d, want %d", deferred.Concurrency.Active, ReportMaxConcurrentRuns)
	}

	// ③ The audit row keeps the reason after the 202 is gone.
	audits := fx.auditRows(t, "report.job.created")
	var found string
	for _, row := range audits {
		if strings.Contains(row.DetailJSON, deferred.Report.ID) {
			found = row.DetailJSON
		}
	}
	if !strings.Contains(found, `"deferred":true`) {
		t.Fatalf("audit must record the deferral, got %q", found)
	}

	// ④ A replayed create is the same report and the same waiting job: deferral is a
	// fact about the job, not a new attempt.
	replay, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-cc-overflow",
		reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Report.ID != deferred.Report.ID {
		t.Fatalf("replay minted a second report: created=%v id=%s", replay.Created, replay.Report.ID)
	}

	// ⑤ Freeing one slot lets the sweep arm the waiting job — no new request, no
	// second click. This is the whole reason the deferral is not an error.
	var occupied []model.Job
	if err := fx.gdb.Where("kind = ? AND tenant_id = ? AND status IN ?",
		model.JobKindReportGenerate, fx.tenantID,
		[]string{model.JobStatusQueued, model.JobStatusRunning}).Find(&occupied).Error; err != nil {
		t.Fatalf("load runtime jobs: %v", err)
	}
	if len(occupied) != ReportMaxConcurrentRuns {
		t.Fatalf("expected %d occupied slots, got %d", ReportMaxConcurrentRuns, len(occupied))
	}
	if err := fx.gdb.Model(&model.Job{}).Where("id = ?", occupied[0].ID).
		Update("status", model.JobStatusSucceeded).Error; err != nil {
		t.Fatalf("free a slot: %v", err)
	}
	armed, err := fx.svc.armUnarmedReportJobs(ctx, time.Now().UTC().Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if armed != 1 {
		t.Fatalf("sweep armed %d jobs, want 1", armed)
	}
	if job := fx.reportJob(t, deferred.Job.ID); job.Status != model.ReportJobRunning ||
		job.CurrentRuntimeJobID == "" {
		t.Fatalf("waiting job must start once a slot frees: status=%s pointer=%q",
			job.Status, job.CurrentRuntimeJobID)
	}
}

func TestReportConcurrencyLimitDoesNotBlockDelivery(t *testing.T) {
	fx := newReportFixture(t, "concurrency_delivery")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	reportID := revision.ReportID
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, reportID, ""); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// Saturate the workspace with other people's work after the report was archived:
	// the limit is about *producing*, and reading or forwarding a delivered report
	// must not queue behind it (doc/297 §3.1).
	reportSaturateWorkspace(t, fx)

	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil {
		t.Fatalf("detail while saturated: %v", err)
	}
	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: reportID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	}); err != nil {
		t.Fatalf("export while saturated: %v", err)
	}

	// And the saturation itself is honest: a new request is deferred, not refused.
	overflow := fx.createReport(t, fx.datasetLink, "req-cc-delivery-overflow")
	if overflow.Concurrency == nil || overflow.Job.Status != model.ReportJobQueued {
		t.Fatalf("over-limit create must be deferred: %+v", overflow.Concurrency)
	}
}

func TestReportConcurrencyLimitIsPerWorkspace(t *testing.T) {
	fx := newReportFixture(t, "concurrency_tenant")
	ctx := context.Background()
	reportSaturateWorkspace(t, fx)

	// A second workspace with its own database rows must be admitted: the count is
	// scoped by `tenant_id` in the single query that makes the decision.
	other, err := fx.svc.CreateTenant(ctx, "报告工作区 concurrency-other")
	if err != nil {
		t.Fatalf("second tenant: %v", err)
	}
	owner, err := fx.svc.CreateUser(ctx, other.ID, "", CreateUserRequest{
		Username: "report-owner-concurrency-other", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("second owner: %v", err)
	}
	seedReportDataset(t, ctx, fx.svc, fx.engine, other.ID, "policy-concurrency-other")
	created, err := fx.svc.CreateReport(ctx, owner.ID, other.ID, "req-cc-other",
		reportCreateRequest("link-policy-concurrency-other"))
	if err != nil {
		t.Fatalf("create in the second workspace: %v", err)
	}
	if created.Concurrency != nil {
		t.Fatalf("the other workspace must not be deferred: %+v", created.Concurrency)
	}
	if created.Job.Status != model.ReportJobRunning {
		t.Fatalf("other workspace job status=%s, want running", created.Job.Status)
	}
}

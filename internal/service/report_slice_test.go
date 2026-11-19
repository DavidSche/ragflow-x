package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Report slices and cancellation (doc/288 §7.9 / §7.10 / §8.4 / A34/I1).
//
// The slice chain and the cancel path are two halves of the same question: "who is
// allowed to move this business job, and under which condition?". Both are written
// as conditional single statements, so the tests drive the real schema and assert
// on rows — a CAS that silently applied twice would not show up in a returned
// value.

// reportErrorCode returns the business code of a typed error, or 0.
func reportErrorCode(err error) int {
	var typed *httperr.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return 0
}

func (fx *reportFixture) runtimeJobRow(t *testing.T, runtimeJobID string) *model.Job {
	t.Helper()
	var runtimeJob model.Job
	if err := fx.gdb.Where("id = ?", runtimeJobID).First(&runtimeJob).Error; err != nil {
		t.Fatalf("load runtime job %s: %v", runtimeJobID, err)
	}
	return &runtimeJob
}

// finalizeNow drives the real two-trigger path: Begin writes `finalizing` plus the
// outbox row, the outbox consumer runs the terminal write. Tests must not call
// FinalizeReportJob on a queued job — Gate 1 refuses it on purpose.
func (fx *reportFixture) finalizeNow(t *testing.T, jobID, runtimeJobID string) {
	t.Helper()
	ctx := context.Background()
	// A real slice settles (succeeded/failed) before it asks for the terminal write;
	// a slice that never ran would trip Gate 2 and defer forever.
	if runtimeJobID != "" {
		if err := fx.gdb.Model(&model.Job{}).Where("id = ?", runtimeJobID).
			Update("status", model.JobStatusSucceeded).Error; err != nil {
			t.Fatal(err)
		}
	}
	job := fx.reportJob(t, jobID)
	applied, err := fx.svc.BeginReportFinalization(
		ctx, fx.tenantID, jobID, job.AttemptSeq, job.SliceSeq, runtimeJobID)
	if err != nil || !applied {
		t.Fatalf("begin finalization: applied=%v err=%v", applied, err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("dispatch outbox: %v", err)
	}
}

// armedSlice returns the runtime slice the job is armed with.
//
// Creation arms slice 1 (`armReportJob`), so a fixture never dispatches by hand:
// a test that dispatched again would only prove the second dispatch is a no-op.
func (fx *reportFixture) armedSlice(t *testing.T, jobID string) string {
	t.Helper()
	job := fx.reportJob(t, jobID)
	if job.CurrentRuntimeJobID == "" {
		t.Fatalf("job %s was never armed: %+v", jobID, job)
	}
	return job.CurrentRuntimeJobID
}

// unarmReportJob rewinds a freshly created job to its pre-dispatch state, so the
// dispatch transition itself can be exercised from its real precondition.
func (fx *reportFixture) unarmReportJob(t *testing.T, jobID string) {
	t.Helper()
	job := fx.reportJob(t, jobID)
	if job.CurrentRuntimeJobID == "" {
		t.Fatalf("job %s is already unarmed", jobID)
	}
	if err := fx.gdb.Where("report_job_id = ?", jobID).Delete(&model.ReportJobAttempt{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Where("id = ?", job.CurrentRuntimeJobID).Delete(&model.Job{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", jobID).
		Updates(map[string]interface{}{
			"status":                 model.ReportJobQueued,
			"current_runtime_job_id": "",
			"started_at":             nil,
		}).Error; err != nil {
		t.Fatal(err)
	}
}

func (fx *reportFixture) countSlices(t *testing.T, reportJobID string) int64 {
	t.Helper()
	var count int64
	if err := fx.gdb.Model(&model.ReportJobAttempt{}).
		Where("report_job_id = ?", reportJobID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// TestDispatchReportJobArmsFirstSlice is §7.9's first transition: the pointer, the
// slice row and the attempt mapping appear together, and the slice carries the
// business identity rather than a clock.
func TestDispatchReportJobArmsFirstSlice(t *testing.T) {
	fx := newReportFixture(t, "dispatch")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-dispatch", "req-dispatch-0001")
	// The transition under test starts from `queued`, which is what the fixture
	// rewinds to: creation itself already armed the slice.
	fx.unarmReportJob(t, created.Job.ID)

	runtimeJobID, applied, err := fx.svc.DispatchReportJob(ctx, fx.tenantID, created.Job.ID)
	if err != nil || !applied {
		t.Fatalf("dispatch: applied=%v err=%v", applied, err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning {
		t.Fatalf("dispatch must move queued → running, got %s", job.Status)
	}
	if job.CurrentRuntimeJobID != runtimeJobID {
		t.Fatalf("pointer must name the armed slice: %q vs %q", job.CurrentRuntimeJobID, runtimeJobID)
	}
	if job.Stage != model.ReportStageOutlining || job.StartedAt == nil {
		t.Fatalf("dispatch must set the first stage and the start clock: %+v", job)
	}
	runtimeJob := fx.runtimeJobRow(t, runtimeJobID)
	if runtimeJob.Kind != model.JobKindReportGenerate {
		t.Fatalf("slice kind must be report_generate, got %s", runtimeJob.Kind)
	}
	if want := reportSliceKey(created.Job.ID, 1, 1); runtimeJob.Key != want {
		t.Fatalf("slice key must be the business identity %q, got %q", want, runtimeJob.Key)
	}
	payload, err := parseReportSlicePayload(runtimeJob.Payload)
	if err != nil {
		t.Fatalf("slice payload must parse: %v", err)
	}
	if payload.ReportJobID != created.Job.ID || payload.SliceSeq != 1 ||
		payload.AttemptSeq != 1 || payload.StartOrd != 1 || payload.RevisionID != created.Revision.ID {
		t.Fatalf("slice payload must carry the business identity: %+v", payload)
	}
	if count := fx.countSlices(t, created.Job.ID); count != 1 {
		t.Fatalf("dispatch must write exactly one attempt mapping, got %d", count)
	}

	// A second dispatch is a no-op: dispatch is a transition, not an insert.
	secondID, applied, err := fx.svc.DispatchReportJob(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applied || secondID != "" {
		t.Fatalf("a second dispatch must not arm another slice: applied=%v id=%q", applied, secondID)
	}
	if count := fx.countSlices(t, created.Job.ID); count != 1 {
		t.Fatalf("a second dispatch must not write a second mapping, got %d", count)
	}
}

// TestCreateReportArmsTheFirstSlice is the same transition reached through the API:
// a created report must already own a runnable slice.
//
// This is the live defect the 151 dry run found: creation committed the aggregate
// and stopped, so every report sat at `queued`/`outlining` forever — visible in the
// UI, never started, and nothing in the response or the log said so.
func TestCreateReportArmsTheFirstSlice(t *testing.T) {
	fx := newReportFixture(t, "createarms")
	created := fx.createReport(t, "link-policy-createarms", "req-createarms-0001")

	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning || job.CurrentRuntimeJobID == "" {
		t.Fatalf("creation must arm slice 1, got %+v", job)
	}
	if created.Job.CurrentRuntimeJobID != job.CurrentRuntimeJobID {
		t.Fatalf("the 202 body must describe the armed run: %q vs %q",
			created.Job.CurrentRuntimeJobID, job.CurrentRuntimeJobID)
	}
	slice := fx.runtimeJobRow(t, job.CurrentRuntimeJobID)
	if slice.Kind != model.JobKindReportGenerate {
		t.Fatalf("armed slice kind must be report_generate, got %s", slice.Kind)
	}
	if want := reportSliceKey(created.Job.ID, 1, 1); slice.Key != want {
		t.Fatalf("armed slice key must be the business identity %q, got %q", want, slice.Key)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("arming must write exactly one attempt mapping, got %d", n)
	}
}

// TestReconcilerArmsAJobWhoseDispatchWasLost covers the crash window the request
// path cannot close: the aggregate transaction committed, the process died before
// the dispatch. The sweep arms it instead of leaving a visible report that never
// runs, and it leaves a job that is already armed alone.
func TestReconcilerArmsAJobWhoseDispatchWasLost(t *testing.T) {
	fx := newReportFixture(t, "armlost")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-armlost", "req-armlost-0001")
	fx.unarmReportJob(t, created.Job.ID)
	// Age the row past the grace window so the sweep may touch it.
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Update("updated_at", time.Now().UTC().Add(-2*reportFinalizeReconcileStaleAfter)).Error; err != nil {
		t.Fatal(err)
	}

	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning || job.CurrentRuntimeJobID == "" {
		t.Fatalf("the sweep must arm the lost dispatch, got %+v", job)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("the sweep must arm exactly one slice, got %d mappings", n)
	}

	// A second pass is a no-op: the job is no longer `queued` with an empty pointer.
	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("a repeated sweep must not arm a second slice, got %d mappings", n)
	}
}

// TestDispatchReportJobRefusesFinishedAndCancelledJobs protects the queue from
// arming work nobody wants: a terminal job and a cancel-requested job must both
// refuse the transition rather than resurrect it.
func TestDispatchReportJobRefusesFinishedAndCancelledJobs(t *testing.T) {
	fx := newReportFixture(t, "dispatchguard")
	ctx := context.Background()

	finished := fx.createReport(t, "link-policy-dispatchguard", "req-dispatchguard-0001")
	// Terminal writes only fire from `running`/`cancel_requested` (§8.2), so the
	// fixture dispatches before it finishes — the same order production uses.
	fx.unarmReportJob(t, finished.Job.ID)
	finishedRuntimeID, applied, err := fx.svc.DispatchReportJob(ctx, fx.tenantID, finished.Job.ID)
	if err != nil || !applied || finishedRuntimeID == "" {
		t.Fatalf("dispatch: id=%q err=%v", finishedRuntimeID, err)
	}
	fx.seedSections(t, finished.Revision.ID, successSeeds())
	fx.finalizeNow(t, finished.Job.ID, finishedRuntimeID)
	if _, applied, err := fx.svc.DispatchReportJob(ctx, fx.tenantID, finished.Job.ID); err != nil || applied {
		t.Fatalf("a finished job must not be dispatched: applied=%v err=%v", applied, err)
	}

	cancelled := fx.createReport(t, "link-policy-dispatchguard", "req-dispatchguard-0002")
	if _, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, cancelled.Report.ID); err != nil {
		t.Fatal(err)
	}
	if _, applied, err := fx.svc.DispatchReportJob(ctx, fx.tenantID, cancelled.Job.ID); err != nil || applied {
		t.Fatalf("a cancel_requested job must not be dispatched: applied=%v err=%v", applied, err)
	}
}

// TestYieldReportSliceAdvancesPointerAndAnnouncesCheckpoint is §8.2's yield: one
// statement moves the numbering, the pointer and the progress counter, and the
// `job.progress` checkpoint is written in that same transaction.
func TestYieldReportSliceAdvancesPointerAndAnnouncesCheckpoint(t *testing.T) {
	fx := newReportFixture(t, "yield")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-yield", "req-yield-0001")
	firstRuntimeID := fx.armedSlice(t, created.Job.ID)
	// Two sections finished, the third failed: the next slice resumes at ord 2 and
	// its counter must not claim more than it wrote.
	fx.seedSections(t, created.Revision.ID, []reportSectionSeed{
		{Key: "background", Status: model.ReportSectionSucceeded},
		{Key: "applicable_rules", Status: model.ReportSectionSucceeded},
		{Key: "compliance", Status: model.ReportSectionFailed},
	})

	applied, err := fx.svc.YieldReportSlice(
		ctx, fx.tenantID, created.Job.ID, 1, 1, firstRuntimeID, 2)
	if err != nil || !applied {
		t.Fatalf("yield: applied=%v err=%v", applied, err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.SliceSeq != 2 || job.Status != model.ReportJobRunning {
		t.Fatalf("yield must bump the slice and keep running: %+v", job)
	}
	if job.CurrentRuntimeJobID == firstRuntimeID || job.CurrentRuntimeJobID == "" {
		t.Fatalf("yield must re-point the job at the new slice, got %q", job.CurrentRuntimeJobID)
	}
	if job.ProgressDone != 2 || job.LastCheckpointAt == nil {
		t.Fatalf("yield must record progress and the checkpoint clock: %+v", job)
	}
	if job.Stage != model.ReportStageSectionWriting {
		t.Fatalf("a yield resumes in section_writing, got %s", job.Stage)
	}
	next := fx.runtimeJobRow(t, job.CurrentRuntimeJobID)
	if want := reportSliceKey(created.Job.ID, 1, 2); next.Key != want {
		t.Fatalf("next slice key must be %q, got %q", want, next.Key)
	}
	payload, err := parseReportSlicePayload(next.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if payload.StartOrd != 3 {
		t.Fatalf("the resumed slice must start at the first unfinished ord (3), got %d", payload.StartOrd)
	}
	if count := fx.countSlices(t, created.Job.ID); count != 2 {
		t.Fatalf("yield must map the new slice too, got %d mappings", count)
	}

	events, err := fx.svc.Store.ListReportEvents(ctx, fx.tenantID, created.Job.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var progress *model.ReportJobEvent
	for i := range events {
		if events[i].Type == reportEventJobProgress {
			progress = &events[i]
		}
	}
	if progress == nil {
		t.Fatal("the checkpoint must be announced as job.progress")
	}
	var payloadFields map[string]interface{}
	if err := json.Unmarshal([]byte(progress.PayloadJSON), &payloadFields); err != nil {
		t.Fatal(err)
	}
	if payloadFields["slice_seq"] != float64(2) {
		t.Fatalf("job.progress must name the new slice: %s", progress.PayloadJSON)
	}
}

// TestYieldReportSliceIgnoresSupersededSlice is the stale-slice guard: a late
// checkpoint from a slice that no longer owns the job must move nothing.
func TestYieldReportSliceIgnoresSupersededSlice(t *testing.T) {
	fx := newReportFixture(t, "yieldstale")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-yieldstale", "req-yieldstale-0001")
	firstRuntimeID := fx.armedSlice(t, created.Job.ID)
	if applied, err := fx.svc.YieldReportSlice(
		ctx, fx.tenantID, created.Job.ID, 1, 1, firstRuntimeID, 1,
	); err != nil || !applied {
		t.Fatalf("first yield: applied=%v err=%v", applied, err)
	}
	before := fx.reportJob(t, created.Job.ID)

	applied, err := fx.svc.YieldReportSlice(
		ctx, fx.tenantID, created.Job.ID, 1, 1, firstRuntimeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("a superseded slice must not advance the job again")
	}
	after := fx.reportJob(t, created.Job.ID)
	if after.SliceSeq != before.SliceSeq || after.CurrentRuntimeJobID != before.CurrentRuntimeJobID {
		t.Fatalf("a stale yield must not mutate the pointer: %+v → %+v", before, after)
	}
	if count := fx.countSlices(t, created.Job.ID); count != 2 {
		t.Fatalf("a stale yield must not write a third slice, got %d", count)
	}
}

// TestCancelReportConvergesWithoutSlice is the dead end §8.4 creates for itself:
// a job cancelled before its first dispatch has no checkpoint to read the flag, so
// the cancel request must ask for the terminal write itself.
func TestCancelReportConvergesWithoutSlice(t *testing.T) {
	fx := newReportFixture(t, "cancelnoslice")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancelnoslice", "req-cancelnoslice-0001")
	// The dead end only exists when no slice was ever armed: rewind the arming
	// creation now performs, so the cancel has no checkpoint to reach.
	fx.unarmReportJob(t, created.Job.ID)

	result, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if result.Status != model.ReportJobCancelRequested || result.JobID != created.Job.ID {
		t.Fatalf("cancel must report the recorded intent: %+v", result)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing {
		t.Fatalf("a slice-less cancel must ask for the terminal write, got %s", job.Status)
	}
	if job.CancelRequestedAt == nil {
		t.Fatal("the cancel clock must be frozen at request time")
	}
	if delivered := fx.countOutbox(t, model.EventTypeReportJobFinalize); delivered != 1 {
		t.Fatalf("the request must hand one terminal write to the outbox, got %d", delivered)
	}

	// The outbox consumer is the trigger; finalize stays the only writer.
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("dispatch outbox: %v", err)
	}
	job = fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobCancelled || job.FinalizedAt == nil {
		t.Fatalf("the cancel must reach cancelled: %+v", job)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionCancelled {
		t.Fatalf("the revision must be cancelled too, got %s", revision.Status)
	}
}

// TestCancelReportIsIdempotent keeps a double click from reading as two state
// changes: the second call reports the intent that already exists.
func TestCancelReportIsIdempotent(t *testing.T) {
	fx := newReportFixture(t, "cancelidem")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancelidem", "req-cancelidem-0001")
	// A slice-less cancel is the case where the request itself must hand out the
	// terminal write; creation's auto-armed slice would otherwise answer instead.
	fx.unarmReportJob(t, created.Job.ID)

	if _, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	firstClock := *job.CancelRequestedAt

	second, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("a repeated cancel must be accepted: %v", err)
	}
	if !second.AlreadyRequested {
		t.Fatal("a repeated cancel must say the intent was already recorded")
	}
	if !second.CancelRequestedAt.Equal(firstClock) {
		t.Fatalf("a repeated cancel must not move the frozen clock: %s vs %s",
			second.CancelRequestedAt, firstClock)
	}
	if delivered := fx.countOutbox(t, model.EventTypeReportJobFinalize); delivered != 1 {
		t.Fatalf("a repeated cancel must not hand out a second terminal write, got %d", delivered)
	}
}

// TestCancelReportOnFinishedJobIsRefused is 44104: the job exists and the caller may
// act on it, but "already finished" must not read as "cancel accepted".
func TestCancelReportOnFinishedJobIsRefused(t *testing.T) {
	fx := newReportFixture(t, "cancelfinished")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancelfinished", "req-cancelfinished-0001")
	runtimeJobID := fx.armedSlice(t, created.Job.ID)
	fx.seedSections(t, created.Revision.ID, successSeeds())
	fx.finalizeNow(t, created.Job.ID, runtimeJobID)
	if job := fx.reportJob(t, created.Job.ID); job.Status != model.ReportJobSucceeded {
		t.Fatalf("fixture must finish the job before the cancel: %+v", job)
	}
	_, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if code := reportErrorCode(err); code != codeReportInvalidTransition {
		t.Fatalf("cancelling a finished job must answer 44104, got %v (%d)", err, code)
	}
}

// TestCancelReportRequiresOwnership is 44109: a cancel is an operation on the
// caller's own deliverable, so a colleague in the same workspace must not be able to
// stop someone else's run — and must not learn that it exists either.
func TestCancelReportRequiresOwnership(t *testing.T) {
	fx := newReportFixture(t, "cancelowner")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancelowner", "req-cancelowner-0001")
	colleague, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-colleague-cancelowner", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fx.svc.CancelReport(ctx, colleague.ID, fx.tenantID, created.Report.ID)
	if code := reportErrorCode(err); code != codeReportAccessDenied {
		t.Fatalf("a non-owner cancel must answer 44109, got %v (%d)", err, code)
	}
	if job := fx.reportJob(t, created.Job.ID); job.Status != model.ReportJobRunning {
		t.Fatalf("a refused cancel must not move the job, got %s", job.Status)
	}
}

// TestReconcilerConvergesStaleActiveJob is the second half of the sweep: a job left
// `cancel_requested` by a crash (no outbox row, no live slice) is converged by the
// reconcile pass instead of hanging forever.
func TestReconcilerConvergesStaleActiveJob(t *testing.T) {
	fx := newReportFixture(t, "converge")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-converge", "req-converge-0001")
	// The crash class under test has no live slice (nothing to checkpoint), so the
	// fixture rewinds the slice creation armed.
	fx.unarmReportJob(t, created.Job.ID)
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Updates(map[string]interface{}{
			"status":              model.ReportJobCancelRequested,
			"cancel_requested_at": time.Now().UTC(),
			"updated_at":          time.Now().UTC().Add(-2 * reportFinalizeReconcileStaleAfter),
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing {
		t.Fatalf("the sweep must ask for the terminal write, got %s", job.Status)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if job = fx.reportJob(t, created.Job.ID); job.Status != model.ReportJobCancelled {
		t.Fatalf("the converged job must reach cancelled, got %s", job.Status)
	}
}

// TestReconcilerLeavesLiveSliceAlone is the boundary of the sweep: a job whose slice
// is still running belongs to that slice's checkpoint, and forcing `finalizing` under
// it is exactly the window A34 closes.
func TestReconcilerLeavesLiveSliceAlone(t *testing.T) {
	fx := newReportFixture(t, "convergelive")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-convergelive", "req-convergelive-0001")
	fx.unarmReportJob(t, created.Job.ID)
	liveRuntimeID := fx.reportRuntimeJob(t, model.JobStatusRunning)
	fx.claimReportJob(t, created.Job.ID, liveRuntimeID)
	fx.attemptRow(t, created.Job.ID, liveRuntimeID)
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Update("updated_at", time.Now().UTC().Add(-2*reportFinalizeReconcileStaleAfter)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning {
		t.Fatalf("a live slice must keep its job, got %s", job.Status)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("a live slice with its mapping in place must not be rebuilt, got %d rows", n)
	}
	if delivered := fx.countOutbox(t, model.EventTypeReportJobFinalize); delivered != 0 {
		t.Fatalf("a live slice must not be handed a terminal write, got %d", delivered)
	}
}

// TestReconcilerRebuildsMissingAttemptMapping is doc/288 §7.9 class 3: the
// business job declares a live runtime slice (pointer + running status) but the
// `rgx_report_job_attempt` mapping is gone — the crash window between the
// pointer write and the mapping insert. The sweep restores exactly one mapping
// row keyed on the triple, leaves the terminal write to the live slice, and is
// idempotent across passes.
func TestReconcilerRebuildsMissingAttemptMapping(t *testing.T) {
	fx := newReportFixture(t, "reconcileclass3")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-class3-0001")
	// Class 3 starts from "pointer written, mapping lost": rewind creation's arm
	// (which wrote both) and let the fixture write only the pointer.
	fx.unarmReportJob(t, created.Job.ID)
	liveRuntimeID := fx.reportRuntimeJob(t, model.JobStatusRunning)
	fx.claimReportJob(t, created.Job.ID, liveRuntimeID)
	if n := fx.countAttempts(t, created.Job.ID); n != 0 {
		t.Fatalf("fixture must start with no mapping row, got %d", n)
	}
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Update("updated_at", time.Now().UTC().Add(-2*reportFinalizeReconcileStaleAfter)).Error; err != nil {
		t.Fatal(err)
	}

	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning {
		t.Fatalf("a live slice must keep its job, got %s", job.Status)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("class 3 must rebuild exactly one mapping row, got %d", n)
	}
	attempt := &model.ReportJobAttempt{}
	if err := fx.gdb.Where("report_job_id = ?", created.Job.ID).First(attempt).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.RuntimeJobID != liveRuntimeID || attempt.AttemptSeq != 1 || attempt.SliceSeq != 1 {
		t.Fatalf("rebuilt mapping must mirror the declared triple: %+v", attempt)
	}
	if delivered := fx.countOutbox(t, model.EventTypeReportJobFinalize); delivered != 0 {
		t.Fatalf("a live slice must not be handed a terminal write, got %d", delivered)
	}

	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile second pass: %v", err)
	}
	if n := fx.countAttempts(t, created.Job.ID); n != 1 {
		t.Fatalf("second pass must not duplicate the mapping, got %d rows", n)
	}
}

// TestParseReportSlicePayloadRejectsUnaddressablePayload keeps a malformed payload
// from being repaired into a default slice: an unparsable payload is a programming
// error and must surface as one.
func TestParseReportSlicePayloadRejectsUnaddressablePayload(t *testing.T) {
	if _, err := parseReportSlicePayload(""); err == nil {
		t.Fatal("an empty payload must be rejected")
	}
	if _, err := parseReportSlicePayload(`{"report_job_id":"j"}`); err == nil {
		t.Fatal("a payload without attempt/slice seq must be rejected")
	}
	payload, err := parseReportSlicePayload(
		`{"report_job_id":"j","attempt_seq":1,"slice_seq":3,"start_ord":2}`)
	if err != nil {
		t.Fatalf("a complete payload must parse: %v", err)
	}
	if payload.SliceSeq != 3 || !strings.HasPrefix(reportSliceKey("j", 1, 3), "report:j:1:3") {
		t.Fatalf("slice identity must round-trip: %+v", payload)
	}
}

// TestReportSliceOwnershipRequiresCurrentPointer pins the three-part guard: business
// id, attempt/slice seq, and the pointer itself.
func TestReportSliceOwnershipRequiresCurrentPointer(t *testing.T) {
	job := &model.ReportJob{ID: "job-1", AttemptSeq: 1, SliceSeq: 2, CurrentRuntimeJobID: "rt-2"}
	payload := ReportSlicePayload{ReportJobID: "job-1", AttemptSeq: 1, SliceSeq: 2}
	if !payload.owns(job, "rt-2") {
		t.Fatal("the current pointer must own the job")
	}
	if payload.owns(job, "rt-1") {
		t.Fatal("a superseded runtime id must not own the job")
	}
	job.SliceSeq = 3
	if payload.owns(job, "rt-2") {
		t.Fatal("a bumped slice seq must not own the job")
	}
}

// TestReportNextStartOrdCountsFromOne pins the resume pointer against the same
// 1-based `ord` the rows carry.
//
// The fallback used to be `len(sections)`, which is only the resume point when the
// first ordinal is 0 — i.e. in the old fixture, never in the database.
func TestReportNextStartOrdCountsFromOne(t *testing.T) {
	cases := []struct {
		name     string
		sections []model.ReportSection
		want     int
	}{
		{"no sections yet", nil, 1},
		{"all succeeded", []model.ReportSection{
			{Ord: 1, Status: model.ReportSectionSucceeded},
			{Ord: 2, Status: model.ReportSectionSucceeded},
		}, 3},
		{"first unfinished wins", []model.ReportSection{
			{Ord: 1, Status: model.ReportSectionSucceeded},
			{Ord: 2, Status: model.ReportSectionFailed},
			{Ord: 3, Status: model.ReportSectionSucceeded},
		}, 2},
		{"superseded history is skipped but still counted", []model.ReportSection{
			{Ord: 1, Status: model.ReportSectionSuperseded},
			{Ord: 2, Status: model.ReportSectionSucceeded},
		}, 3},
	}
	for _, test := range cases {
		if got := reportNextStartOrd(test.sections); got != test.want {
			t.Fatalf("%s: resume ord is %d, want %d", test.name, got, test.want)
		}
	}
}

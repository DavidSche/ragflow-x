package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

// Report finalize (doc/288 §8.2 / §7.10 / A34–A46). These tests drive the real
// service against a real migrated schema because every invariant here is a
// cross-table fact: "the terminal state exists" only means something together with
// "exactly one published revision", "exactly one settlement", "exactly one
// terminal event" and "an outbox row that is not published yet".

type reportSectionSeed struct {
	Key            string
	Status         string
	RequiresEvi    bool
	CitationCount  int
	EvidenceStatus string
	QualityStatus  string
	// GapReason mirrors what the runner stored in `gap_reason` when it closed the
	// section. Empty means "written before the column existed".
	GapReason string
}

func (fx *reportFixture) seedSections(t *testing.T, revisionID string, seeds []reportSectionSeed) {
	t.Helper()
	// `ord` is 1-based on disk (`report_generate_prompt.go` writes `index+1`). The
	// fixture used to write 0..n, which made every renderer's `ord+1` look correct
	// in tests while the delivered files were numbered from 2 in production.
	for ord, seed := range seeds {
		required := seed.RequiresEvi
		quality := seed.QualityStatus
		if quality == "" {
			quality = model.ReportQualityNotChecked
		}
		row := &model.ReportSection{
			ID: id.New(), TenantID: fx.tenantID, RevisionID: revisionID, Ord: ord + 1,
			SectionKey: seed.Key, SectionVersion: 1, Title: seed.Key,
			RequiresEvidence: required, Status: seed.Status, QualityStatus: quality,
			EvidenceStatus: seed.EvidenceStatus, CitationCount: seed.CitationCount,
			GapReason: seed.GapReason,
			ContentMD: "内容：" + seed.Key, ContentHash: "hash-" + seed.Key,
		}
		if err := fx.gdb.Create(row).Error; err != nil {
			t.Fatalf("seed section %s: %v", seed.Key, err)
		}
	}
}

// reportRuntimeJob creates the `rgx_job` row that represents one runtime slice and
// drives it to the requested status.
func (fx *reportFixture) reportRuntimeJob(t *testing.T, status string) string {
	t.Helper()
	runtimeJob := &model.Job{
		Kind: "report_generate", Key: "runtime-" + id.New()[:8], TenantID: fx.tenantID,
		Status: model.JobStatusQueued, RunAfter: time.Now().UTC(),
	}
	if _, err := fx.svc.Store.EnqueueJob(context.Background(), runtimeJob); err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.Job{}).Where("id = ?", runtimeJob.ID).
		Updates(map[string]interface{}{"status": status}).Error; err != nil {
		t.Fatal(err)
	}
	return runtimeJob.ID
}

// claimReportJob points the business job at a runtime slice and marks it running,
// which is the state a slice's Run() sees before it settles.
func (fx *reportFixture) claimReportJob(t *testing.T, jobID, runtimeJobID string) {
	t.Helper()
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", jobID).
		Updates(map[string]interface{}{
			"status": model.ReportJobRunning, "current_runtime_job_id": runtimeJobID,
			"attempt_seq": 1, "slice_seq": 1, "stage": model.ReportStageAssembling,
		}).Error; err != nil {
		t.Fatal(err)
	}
}

// attemptRow mirrors the `rgx_report_job_attempt` row the dispatch transaction
// would have written alongside the pointer + runtime slice (doc/288 §7.9).
func (fx *reportFixture) attemptRow(t *testing.T, jobID, runtimeJobID string) {
	t.Helper()
	if err := fx.gdb.Create(&model.ReportJobAttempt{
		ID: id.New(), TenantID: fx.tenantID, ReportJobID: jobID,
		AttemptSeq: 1, SliceSeq: 1, RuntimeJobID: runtimeJobID,
		RuntimeJobKey: "report:" + jobID + ":1:1", Status: model.JobStatusQueued,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// countAttempts is the class-3 ledger probe: exactly one mapping row per triple.
func (fx *reportFixture) countAttempts(t *testing.T, jobID string) int {
	t.Helper()
	var n int64
	if err := fx.gdb.Model(&model.ReportJobAttempt{}).
		Where("report_job_id = ?", jobID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func (fx *reportFixture) createReport(t *testing.T, datasetID, key string) CreateReportResult {
	t.Helper()
	result, err := fx.svc.CreateReport(context.Background(), fx.owner.ID, fx.tenantID, key,
		reportCreateRequest(datasetID))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	return *result
}

func (fx *reportFixture) reportJob(t *testing.T, jobID string) *model.ReportJob {
	t.Helper()
	job, err := fx.svc.Store.GetReportJob(context.Background(), fx.tenantID, jobID)
	if err != nil || job == nil {
		t.Fatalf("load job %s: %v", jobID, err)
	}
	return job
}

func (fx *reportFixture) reportRevision(t *testing.T, revisionID string) *model.ReportRevision {
	t.Helper()
	revision, err := fx.svc.Store.GetReportRevisionByID(context.Background(), fx.tenantID, revisionID)
	if err != nil || revision == nil {
		t.Fatalf("load revision %s: %v", revisionID, err)
	}
	return revision
}

func (fx *reportFixture) countOutbox(t *testing.T, eventType string) int64 {
	t.Helper()
	var count int64
	if err := fx.gdb.Model(&model.OutboxEvent{}).Where("event_type = ?", eventType).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func (fx *reportFixture) terminalEvents(t *testing.T, jobID string) []model.ReportJobEvent {
	t.Helper()
	events, err := fx.svc.Store.ListReportEvents(context.Background(), fx.tenantID, jobID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	terminal := make([]model.ReportJobEvent, 0, 2)
	for _, event := range events {
		if isReportTerminalEvent(event.Type) {
			terminal = append(terminal, event)
		}
	}
	return terminal
}

// successSeeds is the happy path: five sections, four of which need evidence, all
// written and all backed by citations.
func successSeeds() []reportSectionSeed {
	return []reportSectionSeed{
		{Key: "background", Status: model.ReportSectionSucceeded},
		{Key: "applicable_rules", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 2, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "compliance", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 3, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		// `partially_supported` is `needs_review`, not `passed`: the section cites
		// evidence but fewer chunks than the template's min_evidence, and the fixture
		// has to say what the runner would say.
		{Key: "gaps", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidencePartiallySupported, QualityStatus: model.ReportQualityNeedsReview},
		{Key: "remediation", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
	}
}

// TestBeginReportFinalizationWritesFinalizingAndOutboxTogether is A35 ①:
// "there is a `finalizing` row" must imply "there is an undelivered row that says
// so", otherwise a crash in between leaves a report nothing can finish.
func TestBeginReportFinalizationWritesFinalizingAndOutboxTogether(t *testing.T) {
	fx := newReportFixture(t, "begin")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-begin", "req-begin-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)

	applied, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID)
	if err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	if !applied {
		t.Fatal("begin must apply for the current slice")
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing || job.FinalizedAt != nil {
		t.Fatalf("job must be finalizing with finalized_at NULL: %+v", job)
	}
	if got := fx.countOutbox(t, model.EventTypeReportJobFinalize); got != 1 {
		t.Fatalf("finalizing must write exactly one outbox row, got %d", got)
	}
	events, err := fx.svc.Store.ListReportEvents(ctx, fx.tenantID, created.Job.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != reportEventJobFinalizing {
		t.Fatalf("finalizing must broadcast job.finalizing, got %q", last.Type)
	}

	// A superseded slice (same job, different runtime id) must not close it again.
	applied, err = fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 2, "someone-else")
	if err != nil {
		t.Fatalf("stale begin: %v", err)
	}
	if applied {
		t.Fatal("a slice that no longer owns the job must not begin finalization")
	}
	if got := fx.countOutbox(t, model.EventTypeReportJobFinalize); got != 1 {
		t.Fatalf("stale begin must not write a second outbox row, got %d", got)
	}
}

// TestFinalizeReportJobPublishesOnceAndSettlesOnce is A45: repeated delivery (and
// double triggering) must not produce a second revision, event or settlement.
func TestFinalizeReportJobPublishesOnceAndSettlesOnce(t *testing.T) {
	fx := newReportFixture(t, "idem")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-idem", "req-idem-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	// One reserved charge, already settled at the same amount: the replay below
	// must not touch the totals.
	if err := fx.gdb.Create(&model.ReportCharge{
		ID: id.New(), TenantID: fx.tenantID, ReportJobID: created.Job.ID,
		AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-compliance", SectionAttempt: 1, CallSeq: 1,
		LedgerKey: "report:" + created.Job.ID + ":1:1:sec-compliance:1:1",
		State:     model.ReportChargeSettled, ReservedTokens: 100, ActualTokens: 100, ChargedTokens: 100,
	}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	outcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, now)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if outcome != ReportFinalizeApplied {
		t.Fatalf("first finalize must apply, got %q", outcome)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobSucceeded || job.FinalizedAt == nil {
		t.Fatalf("job must be succeeded + finalized: %+v", job)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished || revision.PublishedAt == nil {
		t.Fatalf("revision must be published: %+v", revision)
	}
	if revision.Completeness != model.ReportCompletenessFull || revision.GapJSON != "[]" {
		t.Fatalf("complete run must publish full with an empty gap list: %q / %q",
			revision.Completeness, revision.GapJSON)
	}
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}
	if report.CurrentRevisionID != created.Revision.ID {
		t.Fatalf("current_revision_id must move to the published revision")
	}
	if terminal := fx.terminalEvents(t, created.Job.ID); len(terminal) != 1 ||
		terminal[0].Type != reportEventJobSucceeded {
		t.Fatalf("exactly one job.succeeded expected, got %+v", terminal)
	}

	// Replay three more times, mirroring a re-delivered outbox row.
	for attempt := 0; attempt < 3; attempt++ {
		outcome, err = fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, now)
		if err != nil {
			t.Fatalf("replay %d: %v", attempt, err)
		}
		if outcome != ReportFinalizeAlreadyDone {
			t.Fatalf("replay %d must report already_finalized, got %q", attempt, outcome)
		}
	}
	if terminal := fx.terminalEvents(t, created.Job.ID); len(terminal) != 1 {
		t.Fatalf("replays must not add terminal events, got %d", len(terminal))
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Charged != 100 || sums.Actual != 100 {
		t.Fatalf("replays must not change the ledger, got %+v", sums)
	}
	var revisions int64
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("report_id = ? AND status = ?", created.Report.ID, model.ReportRevisionPublished).
		Count(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Fatalf("exactly one published revision expected, got %d", revisions)
	}
}

// TestFinalizeReportJobWaitsForRuntimeSlice is A34/A44 ②: a terminal state may not
// be written while the runtime slice is still running, and the request must stay
// retryable rather than be silently dropped.
func TestFinalizeReportJobWaitsForRuntimeSlice(t *testing.T) {
	fx := newReportFixture(t, "gates")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-gates", "req-gates-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusRunning)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}

	outcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("finalize while running: %v", err)
	}
	if outcome != ReportFinalizeDeferred {
		t.Fatalf("a running slice must defer, got %q", outcome)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing || job.FinalizedAt != nil {
		t.Fatalf("deferred finalize must not write the terminal state: %+v", job)
	}
	if revision := fx.reportRevision(t, created.Revision.ID); revision.Status == model.ReportRevisionPublished {
		t.Fatal("deferred finalize must not publish")
	}

	// The outbox consumer turns the deferral into a retry, not an ack (A44 ②).
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("outbox round while deferred: %v", err)
	}
	var events []model.OutboxEvent
	if err := fx.gdb.Where("event_type = ?", model.EventTypeReportJobFinalize).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one outbox row, got %d", len(events))
	}
	if events[0].PublishedAt != nil {
		t.Fatal("a deferred finalize must not mark the outbox row published")
	}
	if events[0].Attempts != 1 || events[0].NextRetryAt == nil {
		t.Fatalf("a deferred finalize must schedule a retry: %+v", events[0])
	}

	// A stale slice is dropped without touching anything.
	staleOutcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, "other-slice", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if staleOutcome != ReportFinalizeStale {
		t.Fatalf("a superseded slice must be stale, got %q", staleOutcome)
	}

	// Once the slice settles, the same call finishes the job and publishes. The
	// retry backoff is real (2s for the first attempt), so it is fast-forwarded
	// instead of slept through: the assertion is "the retry works", not "2s passed".
	if err := fx.gdb.Model(&model.Job{}).Where("id = ?", runtimeID).
		Updates(map[string]interface{}{"status": model.JobStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.OutboxEvent{}).Where("id = ?", events[0].ID).
		Updates(map[string]interface{}{"next_retry_at": time.Now().UTC().Add(-time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("outbox round after settle: %v", err)
	}
	job = fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobSucceeded || job.FinalizedAt == nil {
		t.Fatalf("settled slice must finalize: %+v", job)
	}
	if revision := fx.reportRevision(t, created.Revision.ID); revision.Status != model.ReportRevisionPublished {
		t.Fatalf("settled slice must publish, got %q", revision.Status)
	}
}

// TestFinalizeReportJobClosesASectionNoSliceWillWrite replaces the old third gate.
//
// The old rule deferred whenever a `writing` row survived a slice that reported
// success. Gate 2 above has already proved that slice settled, and a settled slice
// cannot call the model again — so there was nothing to wait for, and `finalizing`
// became a state with no exit: the reconciler re-ran finalize every minute and
// deferred every minute, forever (observed live on 2026-11-18, doc/44 §6).
//
// The rule now: the row is an interrupted write, so finalize closes it and the
// report reaches a terminal state with the hole disclosed. "Disclosed" is the part
// that must not regress — silence would be a silent failure.
func TestFinalizeReportJobClosesASectionNoSliceWillWrite(t *testing.T) {
	fx := newReportFixture(t, "inflight")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-inflight", "req-inflight-0001")
	seeds := successSeeds()
	seeds[2].Status = model.ReportSectionWriting
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	outcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if outcome != ReportFinalizeApplied {
		t.Fatalf("a section no slice will write must not defer forever, got %q", outcome)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.FinalizedAt == nil {
		t.Fatal("finalize must write finalized_at")
	}
	if job.Status != model.ReportJobPartial {
		t.Fatalf("one missing section among written ones is `partial`, got %q", job.Status)
	}
	// The row itself is closed, so a reader never sees a `writing` section inside a
	// published revision.
	for _, row := range fx.reportSections(t, created.Revision.ID) {
		if row.Status == model.ReportSectionWriting || row.Status == model.ReportSectionPending {
			t.Fatalf("finalize must close the interrupted row: %+v", row)
		}
	}
	// ... and the hole is named in `gap_json`, not left for the reader to notice.
	revision := fx.reportRevision(t, created.Revision.ID)
	var gap []reportGapEntry
	if err := json.Unmarshal([]byte(revision.GapJSON), &gap); err != nil {
		t.Fatalf("gap json: %v (%s)", err, revision.GapJSON)
	}
	if len(gap) != 1 || gap[0].Reason == "" {
		t.Fatalf("the closed section must be disclosed with a reason: %+v", gap)
	}
}

// TestFinalizeReportJobRejectsMissingSection is §14.2 structure gate: a declared
// `section_key` with no row fails the revision, and the failure names the keys
// instead of silently patching them in.
func TestFinalizeReportJobRejectsMissingSection(t *testing.T) {
	fx := newReportFixture(t, "structure")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-structure", "req-structure-0001")
	seeds := successSeeds()[:4] // drops `remediation`
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFailed || job.ErrorCode != "44105" {
		t.Fatalf("missing section must fail with 44105: %+v", job)
	}
	if revision := fx.reportRevision(t, created.Revision.ID); revision.Status != model.ReportRevisionFailed {
		t.Fatalf("revision must not publish, got %q", revision.Status)
	}
	terminal := fx.terminalEvents(t, created.Job.ID)
	if len(terminal) != 1 || terminal[0].Type != reportEventJobFailed {
		t.Fatalf("expected one job.failed, got %+v", terminal)
	}
	if !strings.Contains(terminal[0].PayloadJSON, "remediation") {
		t.Fatalf("job.failed must name the missing section_key, got %s", terminal[0].PayloadJSON)
	}
	if revision := fx.reportRevision(t, created.Revision.ID); !strings.Contains(revision.MetricsJSON, `"structure_completeness":0.8`) {
		t.Fatalf("structural ratio must be recorded even on failure, got %s", revision.MetricsJSON)
	}
}

// TestFinalizeReportJobPublishesPartialWithGaps is A39: structure complete, one
// section failed → publishable `partial`, with a persisted disclosure that names
// the section, the reason and the impact.
func TestFinalizeReportJobPublishesPartialWithGaps(t *testing.T) {
	fx := newReportFixture(t, "partial")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-partial", "req-partial-0001")
	seeds := successSeeds()
	seeds[3].Status = model.ReportSectionFailed
	seeds[3].CitationCount = 0
	seeds[3].EvidenceStatus = model.ReportEvidenceUnsupported
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobPartial {
		t.Fatalf("job must be partial, got %q", job.Status)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished || revision.Completeness != model.ReportCompletenessPartial {
		t.Fatalf("partial must still publish: %+v", revision)
	}
	var gaps []reportGapEntry
	if err := json.Unmarshal([]byte(revision.GapJSON), &gaps); err != nil {
		t.Fatalf("gap_json must be a JSON array: %v (%s)", err, revision.GapJSON)
	}
	if len(gaps) != 1 || gaps[0].SectionKey != "gaps" || gaps[0].Reason != model.ReportGapFailed ||
		gaps[0].Impact != model.ReportGapImpactSectionMissing {
		t.Fatalf("gap disclosure wrong: %+v", gaps)
	}
	if !strings.Contains(revision.MetricsJSON, `"quality_flag":"degraded"`) {
		t.Fatalf("citation coverage below the floor must degrade the flag: %s", revision.MetricsJSON)
	}
	terminal := fx.terminalEvents(t, created.Job.ID)
	if len(terminal) != 1 || terminal[0].Type != reportEventJobPartial {
		t.Fatalf("partial must broadcast job.partial (A39): %+v", terminal)
	}
	if !strings.Contains(terminal[0].PayloadJSON, "budget_exhausted") &&
		!strings.Contains(terminal[0].PayloadJSON, "gap_json") {
		t.Fatalf("job.partial must carry the gap list, got %s", terminal[0].PayloadJSON)
	}
}

// TestFinalizeReportJobFailsWithoutBorrowingAnOutlineCode pins what a run where
// every section failed is allowed to say about itself.
//
// The failure is real (`failed`, nothing published) but it is not an invalid
// outline: 44105's single meaning on this surface is "outline / request payload
// invalid" (doc/288 §8.7 ⑤), and a client that reads it goes looking for a broken
// template. The branch used to stamp it, which is exactly the "one code, two
// meanings" the family rule forbids — the live v1 report failed this way and
// reported 44105 while the actual cause was a dead upstream chat route.
//
// So the assertions are: the job fails, nothing publishes, every section is
// disclosed as a gap with its own reason, and the job-level code stays empty
// rather than naming a cause that never happened.
func TestFinalizeReportJobFailsWithoutBorrowingAnOutlineCode(t *testing.T) {
	fx := newReportFixture(t, "all-failed")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-all-failed", "req-allfailed-0001")
	seeds := successSeeds()
	for index := range seeds {
		seeds[index].Status = model.ReportSectionFailed
		seeds[index].CitationCount = 0
		seeds[index].EvidenceStatus = model.ReportEvidenceUnsupported
	}
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFailed || job.FinalizedAt == nil {
		t.Fatalf("0 succeeded sections must fail terminally: %+v", job)
	}
	if job.ErrorCode == "44105" {
		t.Fatal("a model failure must not borrow the outline/payload code 44105")
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionFailed {
		t.Fatalf("a report with no written section must not publish, got %q", revision.Status)
	}
	var gaps []reportGapEntry
	if err := json.Unmarshal([]byte(revision.GapJSON), &gaps); err != nil {
		t.Fatalf("gap_json must still disclose every failed section: %v (%s)", err, revision.GapJSON)
	}
	if len(gaps) != len(seeds) {
		t.Fatalf("every failed section must be disclosed, got %d gaps for %d sections", len(gaps), len(seeds))
	}
	for _, gap := range gaps {
		if gap.Reason == "" {
			t.Fatalf("gap disclosure must carry a reason: %+v", gap)
		}
	}
	terminal := fx.terminalEvents(t, created.Job.ID)
	if len(terminal) != 1 || terminal[0].Type != reportEventJobFailed {
		t.Fatalf("expected one job.failed, got %+v", terminal)
	}
}

// TestFinalizeReportJobSettlesLedgerAgainstBudget is A31/A9: the ledger is the truth
// for what was spent, the job's envelope is the truth for what was allowed, and the
// difference comes back as `released_tokens` (written down as an audit row, because
// "we returned 9110 tokens" is a billing fact, not a log line).
func TestFinalizeReportJobSettlesLedgerAgainstBudget(t *testing.T) {
	fx := newReportFixture(t, "ledger")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-ledger", "req-ledger-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	// 60 reserved and still open, 40 settled below its reservation, and one row
	// that already charged more than it reserved.
	charges := []model.ReportCharge{
		{ID: id.New(), TenantID: fx.tenantID, ReportJobID: created.Job.ID,
			AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-compliance", SectionAttempt: 1, CallSeq: 1,
			LedgerKey: "report:" + created.Job.ID + ":1:1:sec-compliance:1:1",
			State:     model.ReportChargeReserved, ReservedTokens: 60},
		{ID: id.New(), TenantID: fx.tenantID, ReportJobID: created.Job.ID,
			AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-gaps", SectionAttempt: 1, CallSeq: 1,
			LedgerKey: "report:" + created.Job.ID + ":1:1:sec-gaps:1:1",
			State:     model.ReportChargeSettled, ReservedTokens: 100, ActualTokens: 90, ChargedTokens: 90},
	}
	for index := range charges {
		if err := fx.gdb.Create(&charges[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The open reservation is released in full: at terminal time nothing is in
	// flight, so an unreleased reservation would be a leak, not pending cost.
	if sums.Reserved != 160 || sums.Released != 60 || sums.Charged != 90 ||
		sums.Actual != 90 || sums.Held != 0 {
		t.Fatalf("ledger settlement wrong: %+v", sums)
	}
	var freed model.ReportCharge
	if err := fx.gdb.Where("ledger_key = ?", charges[0].LedgerKey).Take(&freed).Error; err != nil {
		t.Fatal(err)
	}
	if freed.ReconcileOutcome != model.ReportReconcileOutcomeReleased || freed.SettledAt == nil {
		t.Fatalf("a terminal release must say why it was released: %+v", freed)
	}
	job := fx.reportJob(t, created.Job.ID)
	// The envelope is five sections at 1600 tokens plus the 1200 outline reserve,
	// frozen at creation; the ledger says 90 tokens were actually spent.
	if job.ReservedTokens != 9200 {
		t.Fatalf("the envelope must come from the template, got %d", job.ReservedTokens)
	}
	if job.ActualTokens != sums.Actual || job.ChargedTokens != sums.Charged {
		t.Fatalf("ledger columns must be rewritten from the ledger: job=%+v sums=%+v", job, sums)
	}
	if job.ReleasedTokens != 9200-sums.Charged || job.OverageTokens != 0 {
		t.Fatalf("unspent envelope must be released, got job=%+v", job)
	}
	var releasedAudits []model.AuditLog
	if err := fx.gdb.Where("action = ? AND tenant_id = ?", "report.charge.released", fx.tenantID).
		Find(&releasedAudits).Error; err != nil {
		t.Fatal(err)
	}
	if len(releasedAudits) != 1 {
		t.Fatalf("releasing unspent budget must be written down once, got %d", len(releasedAudits))
	}
}

// TestFinalizeReportJobAuditsOverage keeps the overage branch honest: an overage is
// allowed (A9 removed `charged <= reserved`), but it must produce an audit row
// instead of being absorbed silently.
func TestFinalizeReportJobAuditsOverage(t *testing.T) {
	fx := newReportFixture(t, "overage")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-overage", "req-overage-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	// An envelope of 100 tokens (a small template) against a call that really cost
	// 140: the overage is billable, must not block publishing, and must be recorded.
	if err := fx.gdb.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", fx.tenantID, created.Job.ID).
		Update("reserved_tokens", 100).Error; err != nil {
		t.Fatal(err)
	}
	charge := &model.ReportCharge{
		ID: id.New(), TenantID: fx.tenantID, ReportJobID: created.Job.ID,
		AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-compliance", SectionAttempt: 1, CallSeq: 1,
		LedgerKey: "report:" + created.Job.ID + ":1:1:sec-compliance:1:1",
		State:     model.ReportChargeSettled, ReservedTokens: 100, ActualTokens: 140, ChargedTokens: 140,
	}
	if err := fx.gdb.Create(charge).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobSucceeded {
		t.Fatalf("an overage must not block publishing, got %q", job.Status)
	}
	if job.OverageTokens != 40 {
		t.Fatalf("overage must be recorded on the job, got %d", job.OverageTokens)
	}
	var audits []model.AuditLog
	if err := fx.gdb.Where("action = ? AND tenant_id = ?", "report.charge.overage", fx.tenantID).
		Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("overage must write exactly one audit row, got %d", len(audits))
	}
	if audits[0].At.IsZero() || audits[0].Result != "SUCCESS" {
		t.Fatalf("audit row must carry a timestamp and a result: %+v", audits[0])
	}
}

// TestReportFinalizeReconcilerFinishesStrandedJob is A35 ④: the reconcile sweep is
// the second trigger, and it must finish a job whose outbox row was published while
// the terminal write never landed.
func TestReportFinalizeReconcilerFinishesStrandedJob(t *testing.T) {
	fx := newReportFixture(t, "reconcile")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-reconcile", "req-reconcile-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash between "outbox row published" and "terminal write": the row
	// is marked published, the job is still finalizing, and the row is old.
	var outboxRows []model.OutboxEvent
	if err := fx.gdb.Where("event_type = ?", model.EventTypeReportJobFinalize).
		Find(&outboxRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(outboxRows) != 1 {
		t.Fatalf("expected one finalize outbox row, got %d", len(outboxRows))
	}
	publishedAt := time.Now().UTC()
	if err := fx.gdb.Model(&model.OutboxEvent{}).Where("id = ?", outboxRows[0].ID).
		Updates(map[string]interface{}{"published_at": publishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-5 * time.Minute)
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Updates(map[string]interface{}{"updated_at": old}).Error; err != nil {
		t.Fatal(err)
	}
	// A fresh finalizing job must not be swept: the window is what keeps the sweep
	// from racing the outbox consumer.
	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobSucceeded || job.FinalizedAt == nil {
		t.Fatalf("the sweep must finish the stranded job: %+v", job)
	}
	if revision := fx.reportRevision(t, created.Revision.ID); revision.Status != model.ReportRevisionPublished {
		t.Fatalf("the sweep must publish, got %q", revision.Status)
	}
	// And a second sweep is a no-op: the job is no longer finalizing.
	if err := fx.svc.ProcessReportFinalizeReconciler(ctx, 20); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if terminal := fx.terminalEvents(t, created.Job.ID); len(terminal) != 1 {
		t.Fatalf("the second sweep must not add a terminal event, got %d", len(terminal))
	}
}

// TestMustHaveReportOutboxHandlersRequiresFinalize is A46: a build without the
// finalize consumer must refuse to start instead of stranding every report.
func TestMustHaveReportOutboxHandlersRequiresFinalize(t *testing.T) {
	fx := newReportFixture(t, "handlers")
	if err := fx.svc.MustHaveReportOutboxHandlers(); err != nil {
		t.Fatalf("all required consumers must be registered: %v", err)
	}
	original := reportOutboxHandlers
	reportOutboxHandlers = map[string]reportOutboxHandler{}
	t.Cleanup(func() { reportOutboxHandlers = original })
	if err := fx.svc.MustHaveReportOutboxHandlers(); err == nil ||
		!strings.Contains(err.Error(), model.EventTypeReportJobFinalize) {
		t.Fatalf("a missing finalize consumer must name the event type, got %v", err)
	}
}

// TestProcessOutboxEventsKeepsUnknownTypesRetryable is the second half of A46: the
// registry path must not turn the "unknown event type" branch into a silent ack.
func TestProcessOutboxEventsKeepsUnknownTypesRetryable(t *testing.T) {
	fx := newReportFixture(t, "unknown")
	ctx := context.Background()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: fx.tenantID, EventType: "report.not_a_real_event",
		AggregateType: model.AggregateTypeReportJob, AggregateID: "nobody",
		Payload: "{}", OccurredAt: time.Now().UTC(),
	}
	if err := fx.gdb.Create(event).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err == nil {
		t.Fatal("an unknown event type must surface an error, not be acked")
	}
	var stored model.OutboxEvent
	if err := fx.gdb.Where("id = ?", event.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PublishedAt != nil || stored.Attempts == 0 {
		t.Fatalf("an unknown event type must be retried, not published: %+v", stored)
	}
}

// TestReportFinalizeOutboxPayloadCarriesSliceIdentity pins the payload contract: the
// consumer needs the slice identity to detect a stale delivery (A44 ③).
func TestReportFinalizeOutboxPayloadCarriesSliceIdentity(t *testing.T) {
	fx := newReportFixture(t, "payload")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-payload", "req-payload-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	// The slice identity in the request must match the row: a mismatch is the
	// "superseded slice" case, which is covered by its own assertions.
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Updates(map[string]interface{}{"attempt_seq": 2, "slice_seq": 3}).Error; err != nil {
		t.Fatal(err)
	}
	applied, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 2, 3, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("begin must apply for the owning slice")
	}
	var stored model.OutboxEvent
	if err := fx.gdb.Where("event_type = ?", model.EventTypeReportJobFinalize).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var payload reportFinalizePayload
	if err := json.Unmarshal([]byte(stored.Payload), &payload); err != nil {
		t.Fatalf("payload must be JSON: %v", err)
	}
	if payload.ReportJobID != created.Job.ID || payload.RuntimeJobID != runtimeID ||
		payload.AttemptSeq != 2 || payload.SliceSeq != 3 {
		t.Fatalf("payload must carry the slice identity: %+v", payload)
	}
	if stored.AggregateType != model.AggregateTypeReportJob || stored.AggregateID != created.Job.ID {
		t.Fatalf("aggregate identity wrong: %+v", stored)
	}
	if _, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID); err != nil {
		t.Fatalf("ledger must be readable: %v", err)
	}
}

// TestFinalizeReportJobCancelWaitsForSliceThenKeepsFinishedWork is the cancel-vs-
// yield race (§7.10): a cancel request must not force a terminal state while the
// slice is still running, and when the work is in fact complete the finished
// content is published rather than thrown away — the job keeps the honest
// `cancelled` status either way.
func TestFinalizeReportJobCancelWaitsForSliceThenKeepsFinishedWork(t *testing.T) {
	fx := newReportFixture(t, "cancel")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancel", "req-cancel-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusRunning)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	cancelledAt := time.Now().UTC()
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Updates(map[string]interface{}{"cancel_requested_at": cancelledAt}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}

	// Cancel semantics never override gate 2: the slice is still running, so the
	// request is kept (not applied) and the finish stays retryable.
	outcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("cancel finalize while running: %v", err)
	}
	if outcome != ReportFinalizeDeferred {
		t.Fatalf("cancel must yield to a running slice, got %q", outcome)
	}
	if job := fx.reportJob(t, created.Job.ID); job.Status != model.ReportJobFinalizing || job.FinalizedAt != nil {
		t.Fatalf("cancel must not write the terminal state early: %+v", job)
	}

	// The slice ends up finishing the work anyway; the cancel request stands, so
	// the job is `cancelled` but the complete content is published.
	if err := fx.gdb.Model(&model.Job{}).Where("id = ?", runtimeID).
		Updates(map[string]interface{}{"status": model.JobStatusCanceled}).Error; err != nil {
		t.Fatal(err)
	}
	outcome, err = fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("finalize cancelled: %v", err)
	}
	if outcome != ReportFinalizeApplied {
		t.Fatalf("settled cancel must apply, got %q", outcome)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobCancelled {
		t.Fatalf("job must stay cancelled, got %q", job.Status)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished || revision.Completeness != model.ReportCompletenessFull {
		t.Fatalf("finished work must be published despite the cancel: %+v", revision)
	}
	terminal := fx.terminalEvents(t, created.Job.ID)
	if len(terminal) != 1 || terminal[0].Type != reportEventJobCancelled {
		t.Fatalf("expected one job.cancelled, got %+v", terminal)
	}
	// The disclosure must be the real list, not `null`: §8.8 makes job.cancelled
	// carry what the cancel kept, and a nil slice would render as "nothing".
	var cancelledPayload struct {
		CompletedOrds []int `json:"completed_ords"`
	}
	if err := json.Unmarshal([]byte(terminal[0].PayloadJSON), &cancelledPayload); err != nil {
		t.Fatalf("job.cancelled payload must be JSON: %v", err)
	}
	if len(cancelledPayload.CompletedOrds) != len(successSeeds()) {
		t.Fatalf("job.cancelled must report all %d completed sections, got %s",
			len(successSeeds()), terminal[0].PayloadJSON)
	}
}

// TestFinalizeReportJobCancelWithHolesPublishesNothing is the other half of §7.10:
// a cancel on an incomplete report publishes nothing and leaves the previous
// revision in place. A broken version must never replace a usable one.
func TestFinalizeReportJobCancelWithHolesPublishesNothing(t *testing.T) {
	fx := newReportFixture(t, "cancelholes")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-cancelholes", "req-cancelholes-0001")
	seeds := successSeeds()
	seeds[4].Status = model.ReportSectionPending
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusCanceled)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", created.Job.ID).
		Updates(map[string]interface{}{"cancel_requested_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobCancelled {
		t.Fatalf("job must be cancelled, got %q", job.Status)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionCancelled || revision.PublishedAt != nil {
		t.Fatalf("an incomplete cancel must not publish: %+v", revision)
	}
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}
	// The creation-time pointer still names revision 1 (the only revision), but the
	// revision is no longer `published`, which is exactly the "nothing usable was
	// produced" signal a reader checks.
	if report.CurrentRevisionID != created.Revision.ID {
		t.Fatalf("cancel must not move current_revision_id")
	}
	if revision.MetricsJSON == "" {
		t.Fatal("even a cancelled non-publishing run must record why (metrics_json)")
	}
}

// gapByKey indexes a decoded `gap_json` array, so a test can state what it means
// about one section without depending on the order the derivation emitted them.
func gapByKey(t *testing.T, revision *model.ReportRevision) map[string]reportGapEntry {
	t.Helper()
	var gaps []reportGapEntry
	if err := json.Unmarshal([]byte(revision.GapJSON), &gaps); err != nil {
		t.Fatalf("gap_json must be a JSON array: %v (%s)", err, revision.GapJSON)
	}
	byKey := make(map[string]reportGapEntry, len(gaps))
	for _, gap := range gaps {
		byKey[gap.SectionKey] = gap
	}
	return byKey
}

func countAuditAction(t *testing.T, gdb *gorm.DB, tenantID, action string) int {
	t.Helper()
	var n int64
	if err := gdb.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ?", tenantID, action).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// TestFinalizeReportJobReportsTheReasonTheRunnerRecorded is the disclosure
// invariant behind `gap_json.reason`: a reader who sees "budget_exhausted" buys
// tokens, and a reader who sees "failed" fixes the source. Neither status nor the
// quality flag separates them (a skip the runner and finalize write is
// `not_checked`, a model failure is `failed`), so the reason has to travel on the
// row itself.
func TestFinalizeReportJobReportsTheReasonTheRunnerRecorded(t *testing.T) {
	fx := newReportFixture(t, "gapreason")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-gapreason", "req-gapreason-0001")
	seeds := successSeeds()
	// A real model failure: the runner recorded nothing beyond the status.
	seeds[1].CitationCount = 0
	seeds[1].Status, seeds[1].EvidenceStatus = model.ReportSectionFailed, model.ReportEvidenceUnsupported
	seeds[1].QualityStatus = model.ReportQualityFailed
	// A dependency skip: skipped + failed, but not because the budget ran out.
	seeds[2].CitationCount = 0
	seeds[2].Status, seeds[2].EvidenceStatus = model.ReportSectionSkipped, model.ReportEvidenceNotRequired
	seeds[2].QualityStatus, seeds[2].GapReason = model.ReportQualityFailed, model.ReportGapSkipped
	// The budget bail-out, which is the only reason that may name 44111.
	seeds[3].CitationCount = 0
	seeds[3].Status, seeds[3].EvidenceStatus = model.ReportSectionSkipped, model.ReportEvidenceNotRequired
	seeds[3].QualityStatus, seeds[3].GapReason = model.ReportQualityFailed, model.ReportGapBudgetExhausted
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	gaps := gapByKey(t, fx.reportRevision(t, created.Revision.ID))
	if got := gaps["applicable_rules"]; got.Reason != model.ReportGapFailed || got.ErrorCode != "" {
		t.Fatalf("a model failure is not a budget problem: %+v", got)
	}
	if got := gaps["compliance"]; got.Reason != model.ReportGapSkipped || got.ErrorCode != "" {
		t.Fatalf("a dependency skip must not be relabelled budget_exhausted: %+v", got)
	}
	if got := gaps["gaps"]; got.Reason != model.ReportGapBudgetExhausted ||
		got.ErrorCode != strconv.Itoa(codeReportBudgetExhausted) {
		t.Fatalf("the budget bail-out must name 44111: %+v", got)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobPartial {
		t.Fatalf("holes must publish as partial, got %q", job.Status)
	}
	if job.ErrorCode != strconv.Itoa(codeReportBudgetExhausted) {
		t.Fatalf("a budget gap must raise 44111 on the job, got %q", job.ErrorCode)
	}
}

// TestFinalizeReportJobPartialWithoutBudgetCarriesNoCode keeps §8.7 ⑥ honest: 44111
// is the *budget* code. Stamping it on every partial tells a user whose model calls
// failed to go buy tokens, and makes the one code mean two things.
func TestFinalizeReportJobPartialWithoutBudgetCarriesNoCode(t *testing.T) {
	fx := newReportFixture(t, "nocode")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-nocode", "req-nocode-0001")
	seeds := successSeeds()
	seeds[3].Status = model.ReportSectionFailed
	seeds[3].CitationCount = 0
	seeds[3].EvidenceStatus = model.ReportEvidenceUnsupported
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobPartial || job.ErrorCode != "" {
		t.Fatalf("a non-budget partial must publish without an error code: %+v", job)
	}
	// A finalized job must not still claim it is writing sections: live full6
	// published `partial` with `stage=section_writing` because the stage only ever
	// moved when a slice wrote one.
	if job.Stage != model.ReportStageAssembling {
		t.Fatalf("a terminal job must sit in %q, got %q",
			model.ReportStageAssembling, job.Stage)
	}
}

// TestFinalizeReportJobFinishesWhenTheRuntimeRowIsGone is A46's "拒绝静默失效": a
// purged `rgx_job` row can only happen after it was terminal, so gate 2 has nothing
// left to wait for. Refusing would leave the job `finalizing` forever (the outbox
// has no attempt ceiling) and the report would never reach a terminal state — the
// exact dead end the gate exists to avoid.
func TestFinalizeReportJobFinishesWhenTheRuntimeRowIsGone(t *testing.T) {
	fx := newReportFixture(t, "runtimemissing")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-runtimemissing", "req-runtimemissing-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Where("id = ?", runtimeID).Delete(&model.Job{}).Error; err != nil {
		t.Fatal(err)
	}
	outcome, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("a missing runtime row must not dead-end finalization: %v", err)
	}
	if outcome != ReportFinalizeApplied {
		t.Fatalf("finalization must apply, got %q", outcome)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobSucceeded || job.FinalizedAt == nil {
		t.Fatalf("every section was written, so the report must succeed: %+v", job)
	}
}

// TestFinalizeReportJobWritesDownLedgerDrift is §8.6's reconciliation duty: the
// ledger wins over the job's cached columns, but a disagreement means a writer
// crashed between the two, and a number that changes after the fact must leave a
// trace. The mirror case (columns already agree) must stay silent.
func TestFinalizeReportJobWritesDownLedgerDrift(t *testing.T) {
	// Agreement: the totals were refreshed after the settle, so nothing is rewritten
	// and no reconcile row is written.
	synced := newReportFixture(t, "driftsync")
	ctx := context.Background()
	createdSynced := synced.createReport(t, "link-policy-driftsync", "req-driftsync-0001")
	synced.seedSections(t, createdSynced.Revision.ID, successSeeds())
	runtimeSynced := synced.reportRuntimeJob(t, model.JobStatusSucceeded)
	synced.claimReportJob(t, createdSynced.Job.ID, runtimeSynced)
	if err := synced.gdb.Create(&model.ReportCharge{
		ID: id.New(), TenantID: synced.tenantID, ReportJobID: createdSynced.Job.ID,
		AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-gaps", SectionAttempt: 1, CallSeq: 1,
		LedgerKey: "report:" + createdSynced.Job.ID + ":1:1:sec-gaps:1:1",
		State:     model.ReportChargeSettled, ReservedTokens: 100, ActualTokens: 90, ChargedTokens: 90,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := synced.svc.Store.RefreshReportChargeTotals(ctx, synced.tenantID, createdSynced.Job.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := synced.svc.BeginReportFinalization(ctx, synced.tenantID, createdSynced.Job.ID, 1, 1, runtimeSynced); err != nil {
		t.Fatal(err)
	}
	if _, err := synced.svc.FinalizeReportJob(ctx, synced.tenantID, createdSynced.Job.ID, runtimeSynced, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := countAuditAction(t, synced.gdb, synced.tenantID, "report.charge.reconciled"); got != 0 {
		t.Fatalf("agreeing columns must not be reported as drift, got %d rows", got)
	}

	// Disagreement: the ledger holds a settled charge the summary columns never saw.
	fx := newReportFixture(t, "drift")
	createdDrift := fx.createReport(t, "link-policy-drift", "req-drift-0001")
	fx.seedSections(t, createdDrift.Revision.ID, successSeeds())
	runtimeDrift := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, createdDrift.Job.ID, runtimeDrift)
	if err := fx.gdb.Create(&model.ReportCharge{
		ID: id.New(), TenantID: fx.tenantID, ReportJobID: createdDrift.Job.ID,
		AttemptSeq: 1, SliceSeq: 1, SectionID: "sec-gaps", SectionAttempt: 1, CallSeq: 1,
		LedgerKey: "report:" + createdDrift.Job.ID + ":1:1:sec-gaps:1:1",
		State:     model.ReportChargeSettled, ReservedTokens: 100, ActualTokens: 90, ChargedTokens: 90,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, createdDrift.Job.ID, 1, 1, runtimeDrift); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, createdDrift.Job.ID, runtimeDrift, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := countAuditAction(t, fx.gdb, fx.tenantID, "report.charge.reconciled"); got != 1 {
		t.Fatalf("ledger drift must be written down exactly once, got %d", got)
	}
	job := fx.reportJob(t, createdDrift.Job.ID)
	if job.ChargedTokens != 90 || job.ActualTokens != 90 {
		t.Fatalf("the ledger must win over the cached columns: %+v", job)
	}
	var audit model.AuditLog
	if err := fx.gdb.Where("action = ? AND tenant_id = ?", "report.charge.reconciled", fx.tenantID).
		Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit.DetailJSON, "ledger_sum_differs_from_job_summary") ||
		!strings.Contains(audit.DetailJSON, "before") || !strings.Contains(audit.DetailJSON, "after") {
		t.Fatalf("the reconcile audit must carry both sides: %s", audit.DetailJSON)
	}
}

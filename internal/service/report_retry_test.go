package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// retrySeed is a directly-seeded published-but-partial report: one generate job,
// already terminal, whose revision carries a succeeded section (ord 1) and a
// failed section (ord 2). Seeding rows through the store (not the engine)
// isolates RetryReportSection from the generation pipeline, which is a different
// feature's concerns.
type retrySeed struct {
	report   *model.Report
	revision *model.ReportRevision
	job      *model.ReportJob
	sections []*model.ReportSection
}

// seedRetryReport returns the minimal (unflushed) state RetryReportSection reads.
// Tests mutate pointers before `flush` so each plants the exact gate it exercises.
func seedRetryReport(t *testing.T, fx *reportFixture) *retrySeed {
	t.Helper()
	now := time.Now().UTC()
	revisionID := id.New()
	report := &model.Report{
		ID: id.New(), TenantID: fx.tenantID, Title: "合规测试报告",
		OwnerUserID: fx.owner.ID, TemplateKey: reportComplianceTemplateKey,
		CurrentRevisionID: revisionID, AccessPolicy: model.ReportAccessOwnerOnly,
		Status: model.ReportStatusActive, ExpiresAt: now.Add(90 * 24 * time.Hour),
		CreatedAt: now, UpdatedAt: now,
	}
	revision := &model.ReportRevision{
		ID: revisionID, ReportID: report.ID, TenantID: fx.tenantID, Revision: 1,
		OutlineConfirmation: model.ReportOutlineRequired,
		DatasetsJSON:        `[]`, TemplateKey: reportComplianceTemplateKey, TemplateVersion: 1,
		Status: model.ReportRevisionPublished, Completeness: model.ReportCompletenessPartial,
		CreatedBy: fx.owner.ID, CreatedAt: now, PublishedAt: &now,
	}
	job := &model.ReportJob{
		ID: id.New(), TenantID: fx.tenantID, ReportID: report.ID, RevisionID: revisionID,
		JobKind: model.ReportJobKindGenerate, Status: model.ReportJobPartial,
		ProgressTotal: 2, UpdatedAt: now,
		FinalizedAt: &now, FinishedAt: &now, StartedAt: &now,
	}
	sections := []*model.ReportSection{
		{
			ID: id.New(), TenantID: fx.tenantID, RevisionID: revisionID,
			Ord: 1, SectionKey: "background", SectionVersion: 1,
			Title: "背景与范围", RequiresEvidence: false,
			Status: model.ReportSectionSucceeded, Attempt: 1,
			ContentMD: "# 背景", ContentHash: "h1", FinishedAt: &now,
		},
		{
			ID: id.New(), TenantID: fx.tenantID, RevisionID: revisionID,
			Ord: 2, SectionKey: "applicable_rules", SectionVersion: 1,
			Title: "适用制度清单", RequiresEvidence: true,
			DependsOnSectionsJSON: `[]`, Status: model.ReportSectionFailed, Attempt: 2,
			FinishedAt: &now,
		},
	}
	return &retrySeed{report: report, revision: revision, job: job, sections: sections}
}

// flush persists the seeded rows.
func (s *retrySeed) flush(t *testing.T, fx *reportFixture) {
	t.Helper()
	ctx := context.Background()
	for _, op := range []func() error{
		func() error { return fx.svc.Store.CreateReport(ctx, s.report) },
		func() error { return fx.svc.Store.CreateReportRevision(ctx, s.revision) },
		func() error { return fx.svc.Store.CreateReportJob(ctx, s.job) },
	} {
		if err := op(); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, section := range s.sections {
		if err := fx.svc.Store.CreateReportSection(ctx, section); err != nil {
			t.Fatalf("seed section: %v", err)
		}
	}
}

// activeJob plants a second, non-terminal job on the same report (used to pin the
// "one running job per report" gate).
func (s *retrySeed) activeJob(t *testing.T, fx *reportFixture) *model.ReportJob {
	t.Helper()
	now := time.Now().UTC()
	active := &model.ReportJob{
		ID: id.New(), TenantID: fx.tenantID, ReportID: s.report.ID, RevisionID: s.revision.ID,
		JobKind: model.ReportJobKindGenerate, Status: model.ReportJobRunning,
		StartedAt: &now, UpdatedAt: now,
	}
	if err := fx.svc.Store.CreateReportJob(context.Background(), active); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	return active
}

func assertRetryCode(t *testing.T, err error, code, status int) {
	t.Helper()
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != code || httpErr.Status != status {
		t.Fatalf("want %d/%d, got %v", status, code, err)
	}
}

// TestRetryReportSectionMintsRetryJob is the happy path: one failed section of a
// published revision is rewritten behind a `section_retry` job, the old row is
// superseded, and a fresh pending row (version+1, based_on, rewrite_reason=retry)
// is inserted and armed as slice 1.
func TestRetryReportSectionMintsRetryJob(t *testing.T) {
	fx := newReportFixture(t, "retry-basic")
	seed := seedRetryReport(t, fx)
	seed.flush(t, fx)

	result, err := fx.svc.RetryReportSection(context.Background(), fx.owner.ID, fx.tenantID, seed.job.ID, 2)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	old := seed.sections[1]
	if result.SupersededID != old.ID || result.SupersededVer != 1 {
		t.Fatalf("superseded provenance wrong: %+v", result)
	}
	if result.Section.Ord != 2 || result.Section.SectionKey != "applicable_rules" ||
		result.Section.SectionVersion != 2 || result.Section.BasedOnSectionVersion != 1 {
		t.Fatalf("new section must advance the version chain, got %+v", result.Section)
	}
	if result.Section.RewriteReason != model.ReportRewriteReasonRetry {
		t.Fatalf("rewrite_reason must be retry, got %q", result.Section.RewriteReason)
	}
	if result.Section.Status != model.ReportSectionPending || result.Section.Attempt != 0 {
		t.Fatalf("retried section must start pending/attempt 0, got %+v", result.Section)
	}
	if result.Section.RequiresEvidence != old.RequiresEvidence ||
		result.Section.Title != old.Title ||
		result.Section.DependsOnSectionsJSON != old.DependsOnSectionsJSON {
		t.Fatalf("new section must copy the editable envelope of the failed row, got %+v", result.Section)
	}
	if result.Job.JobKind != model.ReportJobKindSectionRetry ||
		result.Job.AttemptSeq != 1 || result.Job.SliceSeq != 1 || result.Job.ProgressTotal != 1 {
		t.Fatalf("retry job must be one-attempt/one-slice with job_kind=section_retry, got %+v", result.Job)
	}
	if result.Job.Status != model.ReportJobQueued {
		t.Fatalf("created job must start queued before dispatch, got %+v", result.Job)
	}

	// The old failed row is superseded, never deleted.
	var storedOld, storedNew model.ReportSection
	if err := fx.gdb.First(&storedOld, "id = ?", old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedOld.Status != model.ReportSectionSuperseded || storedOld.SectionVersion != 1 {
		t.Fatalf("old row must be superseded history, got %+v", storedOld)
	}
	if err := fx.gdb.First(&storedNew, "id = ?", result.Section.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedNew.Status != model.ReportSectionPending || storedNew.SectionVersion != 2 {
		t.Fatalf("stored new row wrong, got %+v", storedNew)
	}
}

// TestRetryReportSectionDispatchesFirstSlice pins the §7.9 slice arming: the
// retry job's first slice is `report:<job>:1:1` in rgx_job (the idempotency fence
// against double dispatch), and a second call to RetryReportSection sees the job
// as active and answers 44110 instead of minting a second slice.
func TestRetryReportSectionDispatchesFirstSlice(t *testing.T) {
	fx := newReportFixture(t, "retry-dispatch")
	seed := seedRetryReport(t, fx)
	seed.flush(t, fx)

	result, err := fx.svc.RetryReportSection(context.Background(), fx.owner.ID, fx.tenantID, seed.job.ID, 2)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	var slice model.Job
	if err := fx.gdb.First(&slice, "key = ?", reportSliceKey(result.Job.ID, 1, 1)).Error; err != nil {
		t.Fatalf("slice key report:<job>:1:1 must exist: %v", err)
	}
	if slice.Kind != model.JobKindReportGenerate {
		t.Fatalf("slice kind wrong, got %q", slice.Kind)
	}

	// A replay of the retry is a business conflict: the new section is now pending
	// and the retry job is active, so the gate answers 44110 (active job) — the
	// same serialization regenerate enjoys — never a second superseded write.
	_, err = fx.svc.RetryReportSection(context.Background(), fx.owner.ID, fx.tenantID, seed.job.ID, 2)
	assertRetryCode(t, err, codeReportActiveJobConflict, 409)
}

// TestRetryReportSectionChainedRetries tracks §7.10 across two repairs: the first
// retry writes v2, the second writes v3 *based on v2*, and only the newest row is
// ever live — v1 is superseded by the first retry, v2 by the second. Each retry
// mints its own job (a fresh attempt sequence of its own), so no two slices can
// ever share a key.
func TestRetryReportSectionChainedRetries(t *testing.T) {
	fx := newReportFixture(t, "retry-chain")
	seed := seedRetryReport(t, fx)
	seed.flush(t, fx)
	now := time.Now().UTC()

	first, err := fx.svc.RetryReportSection(context.Background(), fx.owner.ID, fx.tenantID, seed.job.ID, 2)
	if err != nil {
		t.Fatalf("first retry: %v", err)
	}
	jobID1 := first.Job.ID
	sectionID2 := first.Section.ID
	// Let the first retry "run" and fail again, mirroring a slice whose rewrite
	// also ends in failure: the new row goes failed and job1 becomes terminal.
	if err := fx.gdb.Model(&model.ReportSection{}).Where("id = ?", sectionID2).
		Updates(map[string]interface{}{"status": model.ReportSectionFailed, "finished_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportJob{}).Where("id = ?", jobID1).
		Updates(map[string]interface{}{
			"status": model.ReportJobPartial, "finalized_at": now,
		}).Error; err != nil {
		t.Fatal(err)
	}

	second, err := fx.svc.RetryReportSection(context.Background(), fx.owner.ID, fx.tenantID, jobID1, 2)
	if err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if second.Job.ID == jobID1 {
		t.Fatal("each retry mints its own job")
	}
	if second.Section.SectionVersion != 3 || second.Section.BasedOnSectionVersion != 2 {
		t.Fatalf("second retry must be v3 based on v2, got %+v", second.Section)
	}
	var v1, v2 model.ReportSection
	if err := fx.gdb.First(&v1, "id = ?", seed.sections[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.First(&v2, "id = ?", sectionID2).Error; err != nil {
		t.Fatal(err)
	}
	if v1.Status != model.ReportSectionSuperseded || v2.Status != model.ReportSectionSuperseded {
		t.Fatalf("both older rows must be superseded history, v1=%s v2=%s", v1.Status, v2.Status)
	}
}

// TestRetryReportSectionGates exercises the refusal answers in service order.
func TestRetryReportSectionGates(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown job id", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-job")
		_, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, "nope", 2)
		assertRetryCode(t, err, codeReportAccessDenied, 404)
	})

	t.Run("active job blocks retry", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-active")
		seed := seedRetryReport(t, fx)
		seed.flush(t, fx)
		seed.activeJob(t, fx)
		_, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, seed.job.ID, 2)
		assertRetryCode(t, err, codeReportActiveJobConflict, 409)
	})

	t.Run("stale revision is not retryable in place", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-stale")
		seed := seedRetryReport(t, fx)
		seed.flush(t, fx)
		// A newer revision is now the report's current content.
		newerID := id.New()
		newer := &model.ReportRevision{
			ID: newerID, ReportID: seed.report.ID, TenantID: fx.tenantID, Revision: 2,
			OutlineConfirmation: model.ReportOutlineRequired,
			DatasetsJSON:        `[]`, TemplateKey: reportComplianceTemplateKey, TemplateVersion: 1,
			Status: model.ReportRevisionPublished, Completeness: model.ReportCompletenessFull,
			CreatedBy: fx.owner.ID, CreatedAt: time.Now().UTC(),
		}
		if err := fx.svc.Store.CreateReportRevision(ctx, newer); err != nil {
			t.Fatal(err)
		}
		if err := fx.gdb.Model(&model.Report{}).Where("id = ?", seed.report.ID).
			Update("current_revision_id", newerID).Error; err != nil {
			t.Fatal(err)
		}
		_, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, seed.job.ID, 2)
		assertRetryCode(t, err, codeReportInvalidTransition, 409)
	})

	t.Run("non-failed section is not retryable", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-success")
		seed := seedRetryReport(t, fx)
		seed.sections[1].Status = model.ReportSectionSucceeded
		seed.flush(t, fx)
		_, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, seed.job.ID, 2)
		assertRetryCode(t, err, codeReportInvalidTransition, 409)
	})

	t.Run("unknown ord is 44107", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-ord")
		seed := seedRetryReport(t, fx)
		seed.flush(t, fx)
		_, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, seed.job.ID, 99)
		assertRetryCode(t, err, codeReportSectionNotFound, 404)
	})

	t.Run("non-owner is answered 44109", func(t *testing.T) {
		fx := newReportFixture(t, "retry-gate-owner")
		seed := seedRetryReport(t, fx)
		seed.flush(t, fx)
		intruder, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
			Username: "snooper-" + idShort(t), Password: "secret123", Role: model.RoleOperator,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = fx.svc.RetryReportSection(ctx, intruder.ID, fx.tenantID, seed.job.ID, 2)
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
			t.Fatalf("want the owner_only 404/44109, got %v", err)
		}
	})
}

// TestDeriveReportFinalDecisionIgnoresSuperseded proves the §7.10 invariant that
// a `superseded` (old, replaced) section row is invisible to the finalize
// derivation: a retry-finalized revision must produce exactly the decision the
// live rows alone would produce. It is deliberately a pure derivation test — no
// store, no engine — because the derivation is the pure function whose input a
// retry changes (it adds a superseded row to an otherwise settled revision).
func TestDeriveReportFinalDecisionIgnoresSuperseded(t *testing.T) {
	sec := func(key, status string, version int) model.ReportSection {
		return model.ReportSection{
			ID: id.New(), Ord: ordOf(key), SectionKey: key, SectionVersion: version,
			Title: key, Status: status, QualityStatus: model.ReportQualityNotChecked,
		}
	}
	// A rev whose job has an active-outline template keys "intro/findings" but
	// not an evidence map, so `deriveReportQualityMetrics` has nothing to vary.
	facts := reportFinalizeFacts{
		Job:           &model.ReportJob{},
		RuntimeStatus: model.JobStatusSucceeded,
		TemplateKeys:  []string{"intro", "findings"},
		EvidenceKeys:  map[string]bool{},
	}

	// Live-only baseline: both sections succeeded.
	live := facts
	live.Sections = []model.ReportSection{
		sec("intro", model.ReportSectionSucceeded, 1),
		sec("findings", model.ReportSectionSucceeded, 1),
	}
	want := deriveReportFinalDecision(live)

	// After a retry, the same revision reads: findings v1 failed→superseded,
	// findings v2 succeeded, intro untouched. The superseded row must not shadow
	// the live row in `present` nor appear in any ordinal tally.
	after := facts
	after.Sections = []model.ReportSection{
		sec("findings", model.ReportSectionSuperseded, 1),
		sec("intro", model.ReportSectionSucceeded, 1),
		sec("findings", model.ReportSectionSucceeded, 2),
	}
	got := deriveReportFinalDecision(after)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superseded row changed the decision\n  want %+v\n  got  %+v", want, got)
	}

	// A *live* failed row is disclosed exactly once even when its old superseded
	// sibling sits right next to it in the same ord.
	partial := facts
	partial.Sections = []model.ReportSection{
		sec("findings", model.ReportSectionSuperseded, 1),
		sec("intro", model.ReportSectionSucceeded, 1),
		sec("findings", model.ReportSectionFailed, 2),
	}
	d := deriveReportFinalDecision(partial)
	if d.Status != model.ReportJobPartial || d.Publish != true ||
		d.Completeness != model.ReportCompletenessPartial {
		t.Fatalf("expected a publishable partial, got %+v", d)
	}
	if len(d.Gap) != 1 || d.Gap[0].SectionKey != "findings" || d.Gap[0].Ord != ordOf("findings") {
		t.Fatalf("expected exactly one gap for the live findings row, got %+v", d.Gap)
	}
	if len(d.FailedOrds) != 1 || d.FailedOrds[0] != ordOf("findings") {
		t.Fatalf("expected exactly one failed ord, got %+v", d.FailedOrds)
	}
}

// ordOf keeps the pure test's ord ↔ key mapping in one place: "intro" is ord 1,
// "findings" is ord 2, matching the template keys the facts declare.
func ordOf(key string) int {
	switch key {
	case "intro":
		return 1
	case "findings":
		return 2
	}
	return 0
}

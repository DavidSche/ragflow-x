package service

// Generate worker (T4) contract tests. These run the real worker against a real
// migrated schema, because every invariant they pin is a cross-table fact:
//
//   - the outline step freezes the snapshot exactly once and writes one section row
//     per declared section, without calling the model;
//   - confirmation opens attempt 2 / slice 1 and leaves the first attempt's slice
//     key behind, so replay protection and the reviewer's approval cannot collide;
//   - a written section stores its body, its hash, its citations and the evidence
//     the citations point at, and settles its charge;
//   - a refused provider refunds the reservation while an unknown outcome orphans
//     it, and the section is closed either way (never left `writing`);
//   - the worker never writes a terminal status — it asks finalize to.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// reportRunEngine is the report-capable mock with the two engine entry points the
// generate worker uses under test control: the raw model call and retrieval.
type reportRunEngine struct {
	*reportTestEngine
	mu sync.Mutex
	// replies are consumed in order; the last one repeats.
	replies     []string
	chatErrs    []error
	prompts     []string
	retrievals  []ragflow.RetrieveDatasetsRequest
	chunks      *ragflow.RetrieveDatasetsResult
	retrieveErr error
}

func (e *reportRunEngine) ChatToModel(
	ctx context.Context, providerName, instanceName, modelName, message string, stream, thinking bool,
) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.prompts = append(e.prompts, message)
	if len(e.chatErrs) > 0 {
		err := e.chatErrs[0]
		if len(e.chatErrs) > 1 {
			e.chatErrs = e.chatErrs[1:]
		}
		if err != nil {
			return "", err
		}
	}
	if len(e.replies) == 0 {
		return `{"answer":"ok","usage":{"prompt_tokens":10,"completion_tokens":20}}`, nil
	}
	reply := e.replies[0]
	if len(e.replies) > 1 {
		e.replies = e.replies[1:]
	}
	return reply, nil
}

func (e *reportRunEngine) RetrieveDatasets(
	ctx context.Context, req ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retrievals = append(e.retrievals, req)
	if e.retrieveErr != nil {
		return nil, e.retrieveErr
	}
	if e.chunks != nil {
		return e.chunks, nil
	}
	return &ragflow.RetrieveDatasetsResult{
		Chunks: []map[string]interface{}{
			{
				"chunk_id": "chunk-a", "document_id": "doc-a", "document_name": "制度汇编.md",
				"dataset_id": req.DatasetIDs[0], "content": "差旅住宿标准为每晚上限 600 元。",
				"similarity": 0.83,
			},
			{
				"chunk_id": "chunk-b", "document_id": "doc-a", "document_name": "制度汇编.md",
				"dataset_id": req.DatasetIDs[0], "content": "超标部分需部门负责人书面审批。",
				"similarity": 0.71,
			},
		},
	}, nil
}

func (e *reportRunEngine) retrievalCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.retrievals)
}

func (e *reportRunEngine) promptCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.prompts)
}

func (e *reportRunEngine) lastPrompt() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.prompts) == 0 {
		return ""
	}
	return e.prompts[len(e.prompts)-1]
}

// newReportRunFixture builds the standard fixture and replaces the engine with the
// controllable one, plus a default chat model so the writer resolves.
func newReportRunFixture(t *testing.T, name string) (*reportFixture, *reportRunEngine) {
	t.Helper()
	fx := newReportFixture(t, name)
	engine := &reportRunEngine{reportTestEngine: fx.engine}
	fx.svc.RAGFlow = engine
	if err := engine.SetDefaultModel(context.Background(), ragflow.SetDefaultModelRequest{
		ModelType: "chat", ModelID: "mock-chat-model", ModelName: "mock-chat-model",
	}); err != nil {
		t.Fatal(err)
	}
	return fx, engine
}

// runSlice executes the slice the fixture's job is currently armed with, the way
// the runner would: load the runtime row, parse its payload, run the worker.
func (fx *reportFixture) runSlice(t *testing.T, jobID string) {
	t.Helper()
	job := fx.reportJob(t, jobID)
	if job.CurrentRuntimeJobID == "" {
		t.Fatalf("job %s has no armed slice", jobID)
	}
	payload, err := parseReportSlicePayload(fx.runtimeJobRow(t, job.CurrentRuntimeJobID).Payload)
	if err != nil {
		t.Fatalf("parse slice payload: %v", err)
	}
	if err := fx.svc.runReportSlice(context.Background(), fx.runtimeJobRow(t, job.CurrentRuntimeJobID), payload); err != nil {
		t.Fatalf("run slice: %v", err)
	}
}

func (fx *reportFixture) outlineJob(t *testing.T, datasetID, key string) CreateReportResult {
	t.Helper()
	created := fx.createReport(t, datasetID, key)
	// CreateReport arms slice 1; the helper only proves it happened.
	if id := fx.armedSlice(t, created.Job.ID); id == "" {
		t.Fatalf("report job %s was never armed", created.Job.ID)
	}
	return created
}

func (fx *reportFixture) reportSections(t *testing.T, revisionID string) []model.ReportSection {
	t.Helper()
	rows, err := fx.svc.Store.ListReportSections(context.Background(), fx.tenantID, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (fx *reportFixture) reportEventsOf(t *testing.T, jobID, eventType string) []model.ReportJobEvent {
	t.Helper()
	events, err := fx.svc.Store.ListReportEvents(context.Background(), fx.tenantID, jobID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]model.ReportJobEvent, 0, len(events))
	for _, event := range events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

// TestReportGenerateOutlineFreezesRunOnce is §7.3: the snapshot is written before
// the first model call, exactly once, and the sections come from the template — not
// from a model that could invent a shape the reviewer cannot predict.
func TestReportGenerateOutlineFreezesRunOnce(t *testing.T) {
	fx, engine := newReportRunFixture(t, "generate-outline")
	created := fx.outlineJob(t, "link-policy-generate-outline", "req-outline-0001")

	fx.runSlice(t, created.Job.ID)

	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobAwaitingOutline {
		t.Fatalf("outline step must park the job in awaiting_outline, got %q", job.Status)
	}
	if job.CurrentRuntimeJobID != "" {
		t.Fatalf("outline step must release the slice pointer, got %q", job.CurrentRuntimeJobID)
	}
	if engine.promptCount() != 0 {
		t.Fatalf("the outline must not call the model, got %d calls", engine.promptCount())
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionAwaitingOutline {
		t.Fatalf("revision must follow the job into awaiting_outline, got %q", revision.Status)
	}
	if revision.ExecutionSnapshotJSON == "" || revision.ExecutionSnapshotHash == "" {
		t.Fatal("the outline step must freeze the execution snapshot and its hash")
	}
	if revision.EngineVersion != "1.0.0" {
		t.Fatalf("snapshot must record the probed engine version, got %q", revision.EngineVersion)
	}
	var snapshot reportExecutionSnapshot
	if err := json.Unmarshal([]byte(revision.ExecutionSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("snapshot must be readable JSON: %v", err)
	}
	if snapshot.Model.Provider != "Mock" || snapshot.Model.Instance != "mock-instance" ||
		snapshot.Model.Name != "mock-chat-model" {
		t.Fatalf("snapshot must resolve the writer triple, got %+v", snapshot.Model)
	}
	if snapshot.Retrieval.SimilarityThreshold != nil {
		t.Fatalf("P0-A must send no absolute cutoff, got %v", *snapshot.Retrieval.SimilarityThreshold)
	}
	if snapshot.Retrieval.ThresholdSource != "engine_default" ||
		snapshot.Retrieval.ThresholdPolicy != "dataset_calibrated" {
		t.Fatalf("snapshot must disclose the threshold policy and its source: %+v", snapshot.Retrieval)
	}
	if len(snapshot.Datasets) != 1 || snapshot.Datasets[0].EngineDatasetID == "" {
		t.Fatalf("snapshot must freeze the engine dataset id: %+v", snapshot.Datasets)
	}
	if snapshot.PromptHashes["system"] == "" || snapshot.PromptHashes["instruction:compliance"] == "" {
		t.Fatalf("snapshot must hash the prompt skeleton: %+v", snapshot.PromptHashes)
	}

	var outline reportOutline
	if err := json.Unmarshal([]byte(revision.OutlineJSON), &outline); err != nil {
		t.Fatalf("outline_json must be readable: %v", err)
	}
	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) != len(outline.Sections) || len(sections) == 0 {
		t.Fatalf("outline and section rows must agree: %d vs %d", len(sections), len(outline.Sections))
	}
	for _, section := range sections {
		if section.Status != model.ReportSectionPending {
			t.Fatalf("fresh sections must be pending, got %q", section.Status)
		}
		if section.SectionVersion != 1 || section.Ord == 0 || section.Query == "" {
			t.Fatalf("section row incomplete: %+v", section)
		}
	}
	if events := fx.reportEventsOf(t, created.Job.ID, reportEventOutlineReady); len(events) != 1 {
		t.Fatalf("outline step must write exactly one outline.ready event, got %d", len(events))
	}

	// The hash describes the *conditions*, not the run: two reports over the same
	// template, model and datasets must hash equal, which is what makes "the
	// conditions changed" detectable at all.
	created2 := fx.createReport(t, "link-policy-generate-outline", "req-outline-0002")
	fx.armedSlice(t, created2.Job.ID)
	fx.runSlice(t, created2.Job.ID)
	second := fx.reportRevision(t, created2.Revision.ID)
	if second.ExecutionSnapshotHash == "" {
		t.Fatal("the second run must freeze its own snapshot")
	}
	if second.ExecutionSnapshotHash != revision.ExecutionSnapshotHash {
		t.Fatalf("identical conditions must hash equal: "+
			"first=%s second=%s",
			revision.ExecutionSnapshotJSON, second.ExecutionSnapshotJSON)
	}
	if second.ID == revision.ID {
		t.Fatal("two reports must not share a revision")
	}
}

// TestConfirmReportOutlineArmsSecondAttempt pins the transition the reviewer's
// approval performs: titles land, attempt 2 / slice 1 is armed, in one transaction.
func TestConfirmReportOutlineArmsSecondAttempt(t *testing.T) {
	fx, _ := newReportRunFixture(t, "generate-confirm")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-generate-confirm", "req-confirm-0001")
	fx.runSlice(t, created.Job.ID)

	// A stale revision is a 404, not "confirm the latest instead".
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 2, ConfirmReportOutlineRequest{}); err == nil {
		t.Fatal("confirming a revision that does not exist must fail")
	} else if code := reportErrorCode(err); code != 44108 {
		t.Fatalf("missing revision must answer 44108, got %d", code)
	}
	// An unknown section key is a client bug, not a silent no-op.
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{
			Sections: []ConfirmReportOutlineSection{{SectionKey: "nope", Title: "x"}},
		}); err == nil {
		t.Fatal("confirming an unknown section_key must fail")
	} else if code := reportErrorCode(err); code != 44105 {
		t.Fatalf("unknown section key must answer 44105, got %d", code)
	}

	firstRuntime := fx.reportRuntimeJobIDs(t, created.Job.ID)[0]
	result, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{
			Sections: []ConfirmReportOutlineSection{
				{SectionKey: "gaps", Title: "差距、风险与影响面"},
			},
		})
	if err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	if result.AttemptSeq != 2 || result.SliceSeq != 1 {
		t.Fatalf("confirmation must arm attempt 2 slice 1, got %d/%d", result.AttemptSeq, result.SliceSeq)
	}
	if result.EditedCount != 1 {
		t.Fatalf("confirmation must count the edit, got %d", result.EditedCount)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobRunning || job.AttemptSeq != 2 || job.SliceSeq != 1 {
		t.Fatalf("job must be running attempt 2 slice 1: %+v", job)
	}
	if job.CurrentRuntimeJobID != result.RuntimeJob {
		t.Fatalf("pointer must name the new slice: %q vs %q", job.CurrentRuntimeJobID, result.RuntimeJob)
	}
	if firstRuntime == result.RuntimeJob {
		t.Fatal("confirmation must arm a new runtime job, not reuse the outline slice")
	}
	var renamed bool
	for _, section := range fx.reportSections(t, created.Revision.ID) {
		if section.SectionKey == "gaps" && section.Title == "差距、风险与影响面" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("the confirmed title must be written to the section row")
	}
	if events := fx.reportEventsOf(t, created.Job.ID, reportEventOutlineConfirmed); len(events) != 1 {
		t.Fatalf("confirmation must write one outline.confirmed event, got %d", len(events))
	} else if !strings.Contains(strings.ToLower(events[0].PayloadJSON), `"edited":true`) {
		t.Fatalf("confirmation event must disclose the edit: %s", events[0].PayloadJSON)
	}
	if count := fx.countSlices(t, created.Job.ID); count != 2 {
		t.Fatalf("both attempts must be mapped, got %d", count)
	}
	// A second confirmation is a transition that is no longer available.
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err == nil {
		t.Fatal("confirming twice must fail")
	} else if code := reportErrorCode(err); code != 44104 {
		t.Fatalf("double confirmation must answer 44104, got %d", code)
	}
}

// TestReportGenerateWritesSectionsWithEvidence is the happy path end to end: every
// section is written, cited and settled, and the terminal state is finalize's.
func TestReportGenerateWritesSectionsWithEvidence(t *testing.T) {
	fx, engine := newReportRunFixture(t, "generate-sections")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-generate-sections", "req-sections-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	// Every section answers with a citation of the first evidence item, except the
	// no-evidence section which answers without one.
	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"适用制度为差旅制度第三章。[1]","usage":{"prompt_tokens":50,"completion_tokens":12}}`,
		`{"answer":"逐条判定：住宿标准符合。[1][2]","usage":{"prompt_tokens":60,"completion_tokens":20}}`,
		`{"answer":"差距：超标审批留痕缺失。[2]","usage":{"prompt_tokens":70,"completion_tokens":18}}`,
		`{"answer":"整改：补齐审批留痕。[2]","usage":{"prompt_tokens":80,"completion_tokens":16}}`,
	}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing {
		t.Fatalf("worker must ask for finalization, got %q", job.Status)
	}
	if job.FinalizedAt != nil {
		t.Fatal("the worker must never write a terminal status itself")
	}
	if job.ProgressDone != len(fx.reportSections(t, created.Revision.ID)) {
		t.Fatalf("progress_done must reach progress_total: %d/%d", job.ProgressDone, job.ProgressTotal)
	}
	sections := fx.reportSections(t, created.Revision.ID)
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded {
			t.Fatalf("section %s must be succeeded, got %q", section.SectionKey, section.Status)
		}
		if section.ContentMD == "" || section.ContentHash == "" || section.PromptHash == "" {
			t.Fatalf("section %s must record body, hash and prompt hash: %+v", section.SectionKey, section)
		}
		if section.Attempt != 1 {
			t.Fatalf("a section written once must be at attempt 1, got %d", section.Attempt)
		}
		if section.RequiresEvidence && section.CitationCount == 0 {
			t.Fatalf("section %s requires evidence and cited it, but recorded 0 citations", section.SectionKey)
		}
		if !section.RequiresEvidence && section.EvidenceStatus != model.ReportEvidenceNotRequired {
			t.Fatalf("section %s needs no evidence, got %q", section.SectionKey, section.EvidenceStatus)
		}
	}
	var citations int64
	if err := fx.gdb.Model(&model.ReportCitation{}).Where("revision_id = ?", created.Revision.ID).Count(&citations).Error; err != nil {
		t.Fatal(err)
	}
	if citations == 0 {
		t.Fatal("cited sections must write citation rows")
	}
	var evidence int64
	if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).Where("tenant_id = ?", fx.tenantID).Count(&evidence).Error; err != nil {
		t.Fatal(err)
	}
	if evidence == 0 {
		t.Fatal("citations must point at stored evidence snapshots")
	}
	// Charges: one settled row per section, nothing left reserved.
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Held != 0 || sums.Orphan != 0 || sums.Actual <= 0 {
		t.Fatalf("every reservation must be settled, none held or orphaned: %+v", sums)
	}
	if sums.Charged != sums.Actual {
		t.Fatalf("charged must equal actual once nothing is held: %+v", sums)
	}
	if events := fx.reportEventsOf(t, created.Job.ID, reportEventSectionSucceeded); len(events) != len(sections) {
		t.Fatalf("one section.succeeded per section, got %d for %d", len(events), len(sections))
	}
	// Retrieval used the frozen binding and the template's top_k, and sent no cutoff.
	// Two retrievals per section, both deliberate: one at outline time for the
	// evidence preview (§9.6) and one when the section is written. Pinning the count
	// is what stops the preview from silently becoming a second write-path retrieval
	// (or from disappearing when the outline step is refactored).
	if len(engine.retrievals) != 2*len(sections) {
		t.Fatalf("preview + write must be one retrieval each per section (%d), got %d",
			2*len(sections), len(engine.retrievals))
	}
	for _, request := range engine.retrievals {
		if request.SimilarityThreshold != nil {
			t.Fatalf("retrieval must not send an absolute cutoff: %v", *request.SimilarityThreshold)
		}
		if request.PageSize != 8 || request.TopK != 8 {
			t.Fatalf("retrieval must use the template's top_k, got %+v", request)
		}
	}
	if !strings.Contains(strings.ToLower(engine.lastPrompt()), "[1] source:") {
		t.Fatal("the prompt must number the evidence")
	}

	// The terminal write belongs to finalize; drive it the way production does.
	if err := fx.gdb.Model(&model.Job{}).
		Where("id = ?", fx.reportJob(t, created.Job.ID).CurrentRuntimeJobID).
		Update("status", model.JobStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("finalize via outbox: %v", err)
	}
	final := fx.reportJob(t, created.Job.ID)
	if final.Status != model.ReportJobSucceeded {
		t.Fatalf("all sections written must finalize succeeded, got %q", final.Status)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Completeness != model.ReportCompletenessFull {
		t.Fatalf("a complete report must be full, got %q", revision.Completeness)
	}
	// Finalize settles the envelope: every reserved token ends up charged,
	// released or disclosed as overage — never unaccounted for.
	settled, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Released != 0 || settled.Orphan != 0 {
		t.Fatalf("a run inside its envelope must release and orphan nothing: %+v", settled)
	}
	if settled.Charged != settled.Actual {
		t.Fatalf("charged must equal actual after finalize: %+v", settled)
	}
}

// TestReportGenerateZeroCitationsIsDisclosed is §9.3: a section that needed evidence
// and found none is still published, and says so. Dropping the body would discard
// the only honest statement it made.
func TestReportGenerateZeroCitationsIsDisclosed(t *testing.T) {
	fx, engine := newReportRunFixture(t, "generate-unsupported")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-generate-unsupported", "req-unsupported-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.chunks = &ragflow.RetrieveDatasetsResult{}
	engine.replies = []string{
		`{"answer":"知识库未覆盖该问题。","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}

	fx.runSlice(t, created.Job.ID)

	for _, section := range fx.reportSections(t, created.Revision.ID) {
		if !section.RequiresEvidence {
			continue
		}
		if section.Status != model.ReportSectionSucceeded {
			t.Fatalf("an evidence-less section must still commit, got %q", section.Status)
		}
		if section.EvidenceStatus != model.ReportEvidenceUnsupported {
			t.Fatalf("no evidence must be disclosed as unsupported, got %q", section.EvidenceStatus)
		}
		if section.QualityStatus != model.ReportQualityNeedsReview {
			t.Fatalf("an unsupported section needs review, got %q", section.QualityStatus)
		}
	}
}

// TestReportGenerateSettlesBillingByFailureKind is §8.6: a business refusal was not
// billed and is refunded; a transport failure may have been billed and is orphaned
// for reconciliation. Either way the section must reach a terminal state.
func TestReportGenerateSettlesBillingByFailureKind(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantState  string
		wantReason string
	}{
		{
			name: "business refusal is refunded",
			err: &ragflow.Error{
				Type: ragflow.ErrorTypeBusiness, HTTPStatus: 400, Code: 1001,
				Message: "model does not exist",
			},
			wantState: model.ReportChargeReleased,
		},
		{
			name: "transport failure is orphaned",
			err: &ragflow.Error{
				Type: ragflow.ErrorTypeTransport, Message: "connection reset by peer",
			},
			wantState:  model.ReportChargeOrphan,
			wantReason: model.ReportReconcileProviderUnconfirmed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, engine := newReportRunFixture(t, "generate-billing-"+tc.name[:4])
			ctx := context.Background()
			created := fx.outlineJob(t, "link-policy-generate-billing-"+tc.name[:4],
				"req-billing-"+tc.name[:4]+"-0001")
			fx.runSlice(t, created.Job.ID)
			if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
				created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
				t.Fatalf("confirm outline: %v", err)
			}
			engine.mu.Lock()
			engine.chatErrs = []error{tc.err}
			engine.mu.Unlock()

			fx.runSlice(t, created.Job.ID)

			var charge model.ReportCharge
			if err := fx.gdb.Where("report_job_id = ?", created.Job.ID).
				Order("created_at asc").First(&charge).Error; err != nil {
				t.Fatalf("load charge: %v", err)
			}
			if charge.State != tc.wantState {
				t.Fatalf("charge must be %q, got %q", tc.wantState, charge.State)
			}
			if tc.wantReason != "" && charge.ReconcileReason != tc.wantReason {
				t.Fatalf("charge reason must be %q, got %q", tc.wantReason, charge.ReconcileReason)
			}
			// The failed section is terminal; the run continues with the next one.
			sections := fx.reportSections(t, created.Revision.ID)
			if sections[0].Status != model.ReportSectionFailed {
				t.Fatalf("first section must be failed, got %q", sections[0].Status)
			}
			if sections[0].Status == model.ReportSectionWriting {
				t.Fatal("a section must never be left writing")
			}
			if sections[1].Status == model.ReportSectionWriting {
				t.Fatal("the next section must not be abandoned mid-flight")
			}
		})
	}
}

// TestReportGenerateExhaustedBudgetDisclosesGap is §7.10: the frozen envelope is a
// promise, and when it runs out the remaining sections are disclosed as
// `budget_exhausted` gaps rather than silently missing.
func TestReportGenerateExhaustedBudgetDisclosesGap(t *testing.T) {
	fx, engine := newReportRunFixture(t, "generate-budget")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-generate-budget", "req-budget-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	// The envelope covers the first reservation (1600) but not what the first call
	// actually consumes, so section 1 is written and the rest run out of budget.
	fx.setEnvelope(t, created.Job.ID, 1700)
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程。","usage":{"prompt_tokens":1600,"completion_tokens":500}}`,
	}

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if sections[0].Status != model.ReportSectionSucceeded {
		t.Fatalf("the section the budget covered must be written, got %q", sections[0].Status)
	}
	for _, section := range sections[1:] {
		if section.Status != model.ReportSectionSkipped {
			t.Fatalf("budget exhaustion must skip the rest, got %q", section.Status)
		}
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing {
		t.Fatalf("an exhausted run still finalizes, got %q", job.Status)
	}
	if err := fx.gdb.Model(&model.Job{}).
		Where("id = ?", job.CurrentRuntimeJobID).
		Update("status", model.JobStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("finalize via outbox: %v", err)
	}
	final := fx.reportJob(t, created.Job.ID)
	if final.Status != model.ReportJobPartial {
		t.Fatalf("a report with skipped sections must be partial, got %q", final.Status)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Completeness != model.ReportCompletenessPartial {
		t.Fatalf("completeness must be partial, got %q", revision.Completeness)
	}
	var gaps []reportGapEntry
	if err := json.Unmarshal([]byte(revision.GapJSON), &gaps); err != nil {
		t.Fatalf("gap_json must be readable: %v", err)
	}
	if len(gaps) == 0 {
		t.Fatal("a partial report must disclose its gaps")
	}
	for _, gap := range gaps {
		if gap.Reason != model.ReportGapBudgetExhausted {
			t.Fatalf("gap reason must be budget_exhausted, got %q", gap.Reason)
		}
		if gap.Impact != model.ReportGapImpactSectionMissing {
			t.Fatalf("gap impact must be section_missing, got %q", gap.Impact)
		}
	}
}

// TestReportGenerateCancelBetweenSectionsConverges is §8.8: a cancel that arrives
// mid-run stops the loop and hands the job to finalize, which derives `cancelled`.
func TestReportGenerateCancelBetweenSectionsConverges(t *testing.T) {
	fx, _ := newReportRunFixture(t, "generate-cancel")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-generate-cancel", "req-cancel-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	if _, err := fx.svc.CancelReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	fx.runSlice(t, created.Job.ID)

	job := fx.reportJob(t, created.Job.ID)
	if job.Status != model.ReportJobFinalizing {
		t.Fatalf("a cancelled slice must hand the job to finalize, got %q", job.Status)
	}
	if err := fx.gdb.Model(&model.Job{}).
		Where("id = ?", job.CurrentRuntimeJobID).
		Update("status", model.JobStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("finalize via outbox: %v", err)
	}
	if final := fx.reportJob(t, created.Job.ID); final.Status != model.ReportJobCancelled {
		t.Fatalf("cancel must finalize as cancelled, got %q", final.Status)
	}
}

// TestReportGenerateRejectsUnknownWriterModel pins the resolution failure: a
// workspace without a usable default chat model must stop the run with the reason
// recorded, not generate sections with a guessed model.
func TestReportGenerateRejectsUnknownWriterModel(t *testing.T) {
	fx, engine := newReportRunFixture(t, "generate-nomodel")
	ctx := context.Background()
	if err := engine.SetDefaultModel(ctx, ragflow.SetDefaultModelRequest{
		ModelType: "chat", ModelID: "",
	}); err != nil {
		t.Fatal(err)
	}
	created := fx.outlineJob(t, "link-policy-generate-nomodel", "req-nomodel-0001")

	err := func() error {
		job := fx.reportJob(t, created.Job.ID)
		payload, parseErr := parseReportSlicePayload(fx.runtimeJobRow(t, job.CurrentRuntimeJobID).Payload)
		if parseErr != nil {
			return parseErr
		}
		return fx.svc.runReportSlice(ctx, fx.runtimeJobRow(t, job.CurrentRuntimeJobID), payload)
	}()
	if err == nil {
		t.Fatal("a run without a default chat model must fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "report writer model") {
		t.Fatalf("failure must name the writer model: %v", err)
	}
	if len(fx.reportSections(t, created.Revision.ID)) != 0 {
		t.Fatal("a run that cannot resolve its model must not write sections")
	}
	if job := fx.reportJob(t, created.Job.ID); job.Status != model.ReportJobFinalizing {
		t.Fatalf("the failure must converge to finalize, got %q", job.Status)
	}
}

// reportRuntimeJobIDs lists the armed runtime jobs of one report, oldest first.
func (fx *reportFixture) reportRuntimeJobIDs(t *testing.T, jobID string) []string {
	t.Helper()
	var rows []model.ReportJobAttempt
	if err := fx.gdb.Where("report_job_id = ?", jobID).
		Order("attempt_seq asc, slice_seq asc").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RuntimeJobID)
	}
	return out
}

// TestReportSectionWritersKeepOneOwner pins the two guards that decide who may
// touch a section row: `writing` has a live owner and must not be skipped out from
// under it, while a section that failed before it started must still be closable —
// otherwise it stays `pending` and every later slice picks the same doomed section.
func TestReportSectionWritersKeepOneOwner(t *testing.T) {
	fx, _ := newReportRunFixture(t, "section-guards")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-section-guards", "req-guards-0001")
	fx.runSlice(t, created.Job.ID)
	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) < 3 {
		t.Fatalf("fixture needs at least three sections, got %d", len(sections))
	}
	writing, failedEarly := sections[0], sections[1]
	now := time.Now().UTC()
	if err := fx.gdb.Model(&model.ReportSection{}).Where("id = ?", writing.ID).
		Updates(map[string]interface{}{"status": model.ReportSectionWriting, "attempt": 1}).Error; err != nil {
		t.Fatal(err)
	}

	// The live owner is asked to be skipped too, so the guard is what excludes it.
	ids := []string{writing.ID}
	for _, section := range sections[2:] {
		ids = append(ids, section.ID)
	}
	skipped, err := fx.svc.Store.SkipReportSections(ctx, repository.ReportSectionSkip{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionIDs: ids, QualityStatus: model.ReportQualityFailed,
		EvidenceStatus: model.ReportEvidenceUnsupported, Now: now, Stage: model.ReportStageSectionWriting,
	})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if skipped != int64(len(ids)-1) {
		t.Fatalf("only the sections without a live owner may be skipped, got %d of %d",
			skipped, len(ids))
	}
	if row := fx.reportSection(t, writing.ID); row.Status != model.ReportSectionWriting {
		t.Fatalf("the section being written must keep its owner, got %q", row.Status)
	}

	// A failure that lands after the row was claimed closes a `writing` section.
	applied, err := fx.svc.Store.CloseReportSection(ctx, repository.ReportSectionClose{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionID: writing.ID, Attempt: 1, Status: model.ReportSectionFailed,
		QualityStatus: model.ReportQualityFailed, EvidenceStatus: model.ReportEvidenceUnsupported,
		Now: now, Stage: model.ReportStageSectionWriting,
	})
	if err != nil || !applied {
		t.Fatalf("close writing section: applied=%v err=%v", applied, err)
	}
	// A failure that lands before the claim closes a `pending` section — attempt 0
	// is the row's attempt because no call ever started.
	applied, err = fx.svc.Store.CloseReportSection(ctx, repository.ReportSectionClose{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionID: failedEarly.ID, Attempt: failedEarly.Attempt, Status: model.ReportSectionFailed,
		QualityStatus: model.ReportQualityFailed, EvidenceStatus: model.ReportEvidenceUnsupported,
		Now: now, Stage: model.ReportStageSectionWriting,
	})
	if err != nil || !applied {
		t.Fatalf("close a section that never started: applied=%v err=%v", applied, err)
	}
	if row := fx.reportSection(t, failedEarly.ID); row.Status != model.ReportSectionFailed ||
		row.FinishedAt == nil {
		t.Fatalf("a failed section must be terminal and time-stamped: %+v", row)
	}
	// A close is a transition too: the same attempt cannot be closed twice.
	if again, err := fx.svc.Store.CloseReportSection(ctx, repository.ReportSectionClose{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionID: failedEarly.ID, Attempt: failedEarly.Attempt, Status: model.ReportSectionSkipped,
		QualityStatus: model.ReportQualityFailed, EvidenceStatus: model.ReportEvidenceUnsupported,
		Now: now, Stage: model.ReportStageSectionWriting,
	}); err != nil || again {
		t.Fatalf("a closed section must not be closed again: applied=%v err=%v", again, err)
	}
	if again, err := fx.svc.Store.SkipReportSections(ctx, repository.ReportSectionSkip{
		TenantID: fx.tenantID, JobID: created.Job.ID, RevisionID: created.Revision.ID,
		SectionIDs: []string{sections[2].ID}, QualityStatus: model.ReportQualityFailed,
		EvidenceStatus: model.ReportEvidenceUnsupported, Now: now, Stage: model.ReportStageSectionWriting,
	}); err != nil || again != 0 {
		t.Fatalf("a skipped section must not be skipped again: applied=%d err=%v", again, err)
	}
}

// reportSection loads one section row by id.
func (fx *reportFixture) reportSection(t *testing.T, sectionID string) model.ReportSection {
	t.Helper()
	var row model.ReportSection
	if err := fx.gdb.Where("id = ?", sectionID).First(&row).Error; err != nil {
		t.Fatalf("load section %s: %v", sectionID, err)
	}
	return row
}

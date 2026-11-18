package service

// Live-shape generation (doc/288 §7.5, doc/44 R09–R13).
//
// Three defects were found only against the real RAGFlow engine and PostgreSQL,
// and each of them is pinned here because SQLite cannot see the first one and the
// mock could not see the second:
//
//  1. `reportRowID` produced 33 characters for the `evidence` prefix while both
//     snapshot tables declare `varchar(32)`. PostgreSQL rejects the insert
//     (`22001`), the section's commit transaction aborts, and the row stays
//     `writing` — which reads as work in flight forever.
//  2. `/retrieval` on RAGFlow v1.0.0 names the chunk `id` (16 hex), not
//     `chunk_id`. Reading only `chunk_id` stored evidence whose chunk could not be
//     named, i.e. citations that point at nothing.
//  3. a `writing` row whose owning slice died was claimed as `skipped` in memory
//     by `skipRemaining` while the store (correctly) refused to touch it, so
//     `allTerminal()` lied and finalize deferred forever.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestReportRowIDsFitTheirColumns pins the length budget, not the digest: any
// truncation works as long as the id stays under the column width and remains
// deterministic, so a retried write collides instead of duplicating.
func TestReportRowIDsFitTheirColumns(t *testing.T) {
	evidence := reportRowID("evidence", "tenant-a", "1f2e3d4c5b6a7988", "sha256-of-content")
	if len(evidence) > 32 {
		t.Fatalf("evidence id must fit varchar(32): %d chars %q", len(evidence), evidence)
	}
	if again := reportRowID("evidence", "tenant-a", "1f2e3d4c5b6a7988", "sha256-of-content"); again != evidence {
		t.Fatalf("ids must be deterministic: %q != %q", again, evidence)
	}
	if other := reportRowID("evidence", "tenant-a", "1f2e3d4c5b6a7988", "other-content"); other == evidence {
		t.Fatal("a different chunk must not collide with the same id")
	}
	citation := reportRowID("citation", "section-a", "0")
	if len(citation) > 32 {
		t.Fatalf("citation id must fit varchar(32): %d chars %q", len(citation), citation)
	}
	if citation == reportRowID("citation", "section-a", "1") {
		t.Fatal("two cited chunks must not share a citation id")
	}
}

// TestEvidenceSnapshotNamesTheEngineChunk drives generation with the chunk shape
// the live engine answers, and asserts the snapshot and the citation carry that id
// through to the read side.
func TestEvidenceSnapshotNamesTheEngineChunk(t *testing.T) {
	fx, engine := newReportRunFixture(t, "chunk-id")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-chunk-id", "req-chunk-id-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	const engineChunkID = "1f2e3d4c5b6a7988"
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			// No `chunk_id`: this is what the live engine sends.
			"id": engineChunkID, "document_id": "doc-live",
			"document_name": "差旅制度.md", "dataset_id": fx.datasetLink,
			"content": "差旅住宿标准为每晚上限 600 元。", "similarity": 0.88,
		},
	}}
	engine.replies = []string{
		`{"answer":"总体结论：合规。[1]","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`{"answer":"适用制度为差旅制度。[1]","usage":{"prompt_tokens":12,"completion_tokens":6}}`,
		`{"answer":"住宿标准符合规定。[1]","usage":{"prompt_tokens":14,"completion_tokens":7}}`,
		`{"answer":"差距：审批留痕缺失。[1]","usage":{"prompt_tokens":16,"completion_tokens":8}}`,
		`{"answer":"整改：补齐审批留痕。[1]","usage":{"prompt_tokens":18,"completion_tokens":9}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	var snapshots []model.ReportEvidenceSnapshot
	if err := fx.gdb.Where("tenant_id = ?", fx.tenantID).Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if len(snapshots) == 0 {
		t.Fatal("a section that cites its evidence must store a snapshot")
	}
	for _, snapshot := range snapshots {
		if snapshot.ChunkID != engineChunkID {
			t.Fatalf("the snapshot must name the engine chunk %q, got %q",
				engineChunkID, snapshot.ChunkID)
		}
		if len(snapshot.ID) > 32 {
			t.Fatalf("snapshot id must fit varchar(32): %q", snapshot.ID)
		}
	}
	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(citations) == 0 {
		t.Fatal("generation must write citations")
	}
	for _, citation := range citations {
		if citation.SourceChunkID != engineChunkID {
			t.Fatalf("the citation must name the engine chunk %q, got %q",
				engineChunkID, citation.SourceChunkID)
		}
	}
}

// TestSkipRemainingDoesNotClaimASectionItDidNotClose is the regression guard for
// the lie: the store closes `pending` rows only, so the runner must not mark a
// `writing` row `skipped` in memory — doing so made `allTerminal()` true and the
// slice asked for finalization while a section was still in flight.
func TestSkipRemainingDoesNotClaimASectionItDidNotClose(t *testing.T) {
	fx := newReportFixture(t, "skip-truth")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-skip-truth", "req-skip-truth-0001")
	seeds := successSeeds()
	seeds[2].Status = model.ReportSectionWriting
	seeds[4].Status = model.ReportSectionPending
	fx.seedSections(t, created.Revision.ID, seeds)

	run := reportSectionRun{
		svc: fx.svc, ctx: ctx, tenantID: fx.tenantID,
		business: fx.reportJob(t, created.Job.ID), revision: fx.reportRevision(t, created.Revision.ID),
		sections: fx.reportSections(t, created.Revision.ID),
	}
	closed, err := run.skipRemaining(model.ReportGapSkipped, model.ReportQualityNotChecked)
	if err != nil {
		t.Fatalf("skip remaining: %v", err)
	}
	if closed != 1 {
		t.Fatalf("only the never-started section may be closed, got %d", closed)
	}
	if run.allTerminal() {
		t.Fatal("the slice must not report every section closed while one is still writing")
	}
	writing := 0
	for index := range run.sections {
		if run.sections[index].Status == model.ReportSectionWriting {
			writing++
		}
	}
	if writing != 1 {
		t.Fatalf("the in-flight section must stay writing in memory, got %d", writing)
	}
	storedWriting := 0
	for _, row := range fx.reportSections(t, created.Revision.ID) {
		if row.Status == model.ReportSectionWriting {
			storedWriting++
		}
	}
	if storedWriting != 1 {
		t.Fatalf("the store must still hold the in-flight section, got %d", storedWriting)
	}
	// The closed row was never attempted, so the quality gate never judged it:
	// `failed` here would claim a verdict nobody reached (doc/288 §11 disclosure).
	for _, row := range fx.reportSections(t, created.Revision.ID) {
		if row.Status != model.ReportSectionSkipped {
			continue
		}
		if row.QualityStatus != model.ReportQualityNotChecked {
			t.Fatalf("a never-started section must be not_checked, got %q", row.QualityStatus)
		}
		if row.GapReason != model.ReportGapSkipped {
			t.Fatalf("a never-started section must disclose the gap, got %q", row.GapReason)
		}
	}
}

// TestSliceClosesASectionItFoundInterrupted is the other half of the same bug: a
// slice that finds a `writing` row from a dead attempt ends it instead of spinning
// on it, so the report can reach a terminal state with the hole disclosed.
func TestSliceClosesASectionItFoundInterrupted(t *testing.T) {
	fx, engine := newReportRunFixture(t, "interrupted")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-interrupted", "req-interrupted-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	// The wreck a crashed slice leaves: owned by nobody, still `writing`.
	sections := fx.reportSections(t, created.Revision.ID)
	broken := sections[0]
	if err := fx.gdb.Model(&model.ReportSection{}).Where("id = ?", broken.ID).
		Updates(map[string]interface{}{
			"status": model.ReportSectionWriting, "attempt": broken.Attempt + 1,
			"started_at": time.Now().UTC(),
		}).Error; err != nil {
		t.Fatal(err)
	}

	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"总体结论：合规。","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`{"answer":"适用制度为差旅制度。","usage":{"prompt_tokens":12,"completion_tokens":6}}`,
		`{"answer":"住宿标准符合规定。","usage":{"prompt_tokens":14,"completion_tokens":7}}`,
		`{"answer":"差距：审批留痕缺失。","usage":{"prompt_tokens":16,"completion_tokens":8}}`,
		`{"answer":"整改：补齐审批留痕。","usage":{"prompt_tokens":18,"completion_tokens":9}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	closed := fx.reportSections(t, created.Revision.ID)[0]
	if closed.Status != model.ReportSectionFailed {
		t.Fatalf("an interrupted write must be closed as failed, got %q", closed.Status)
	}
	if closed.GapReason != model.ReportGapFailed {
		t.Fatalf("the closure must keep the `failed` wording, got %q", closed.GapReason)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.Status == model.ReportJobRunning {
		t.Fatal("the slice must not keep a job running on a section it already closed")
	}
}

package service

// Historical evidence readability (doc/288 §7.5 / A20).
//
// A20 has one job: a document that is re-parsed, re-chunked, re-embedded or deleted
// tomorrow must not change what a report said about it today. The mechanism is the
// evidence snapshot — a copy of the chunk content taken at citation time — and this
// test pins the read side of it: the appendix reader resolves content from that copy
// and never from the retrieval source, so a re-parse cannot rewrite history.
//
// What must not be "fixed" later: making the appendix reader fall back to a live
// retrieval when a snapshot looks stale. That would make the appendix agree with
// whatever the documents say *now*, which is the opposite of evidence.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestHistoricalEvidenceSurvivesSourceReparse(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-reparse")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-evidence-reparse", "req-evidence-reparse-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	// A chunk larger than the snapshot budget, so the frozen copy is an excerpt and
	// the truncation flags are exercised rather than trivially "none".
	source := longReportChunk()
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "chunk-original", "document_id": "doc-original",
			"document_name": "差旅制度.md", "dataset_id": fx.datasetLink,
			"content": source, "similarity": 0.9,
		},
	}}
	engine.replies = []string{
		`{"answer":"总体结论：制度执行基本合规。","usage":{"prompt_tokens":30,"completion_tokens":10}}`,
		`{"answer":"适用制度为差旅制度。[1]","usage":{"prompt_tokens":40,"completion_tokens":12}}`,
		`{"answer":"住宿标准符合规定。[1]","usage":{"prompt_tokens":50,"completion_tokens":20}}`,
		`{"answer":"差距：超标审批留痕缺失。[1]","usage":{"prompt_tokens":60,"completion_tokens":18}}`,
		`{"answer":"整改：补齐审批留痕。[1]","usage":{"prompt_tokens":70,"completion_tokens":16}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
	if err != nil || len(citations) == 0 {
		t.Fatalf("generation must write citations: %v (%d)", err, len(citations))
	}
	for _, citation := range citations {
		if citation.EvidenceSnapshotID == "" {
			t.Fatalf("citation %s has no evidence snapshot", citation.ID)
		}
	}
	sections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendix, verifiable, err := fx.svc.buildReportEvidenceAppendix(
		ctx, fx.tenantID, citations, sectionTitles(sections))
	if err != nil {
		t.Fatalf("appendix must render: %v", err)
	}
	if !verifiable || len(appendix) == 0 {
		t.Fatalf("fresh snapshots must verify: verifiable=%v entries=%d", verifiable, len(appendix))
	}
	frozen := appendix[0].Content
	if !appendix[0].Truncated ||
		appendix[0].TruncationStrategy != model.ReportEvidenceTruncationExcerptWindow {
		t.Fatalf("an over-budget chunk must be disclosed as an excerpt: %+v", appendix[0])
	}
	if !strings.Contains(frozen, appendix[0].Snippet) {
		t.Fatal("the frozen excerpt must contain the snippet it is cited for")
	}

	// The document is re-parsed: new chunk ids, new text, new embeddings, and the
	// retrieval source is momentarily unavailable (the parse is in flight). None of
	// that may reach a report that already cited the old text.
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "chunk-reparsed", "document_id": "doc-reparsed",
			"document_name": "差旅制度.md", "dataset_id": fx.datasetLink,
			"content":    "（重新解析后的新文本）住宿标准调整为每晚上限 800 元。",
			"similarity": 0.95,
		},
	}}
	engine.retrieveErr = errors.New("document is being re-parsed")
	engine.mu.Unlock()

	again, stillVerifiable, err := fx.svc.buildReportEvidenceAppendix(
		ctx, fx.tenantID, citations, sectionTitles(sections))
	if err != nil {
		t.Fatalf("a re-parse must not break the historical appendix: %v", err)
	}
	if !stillVerifiable {
		t.Fatal("a re-parse is not evidence expiry: citations must stay verifiable")
	}
	if again[0].Content != frozen || again[0].ContentHash != appendix[0].ContentHash {
		t.Fatal("the appendix must re-read the frozen copy, not the re-parsed source")
	}
	if again[0].ChunkID != "chunk-original" || again[0].DocumentID != "doc-original" {
		t.Fatalf("the appendix must keep naming the source it was written from: %+v", again[0])
	}
	if again[0].Truncated != appendix[0].Truncated ||
		again[0].TruncationStrategy != appendix[0].TruncationStrategy {
		t.Fatal("the truncation disclosure must not change after a re-parse")
	}
	if strings.Contains(again[0].Content, "800") {
		t.Fatal("the appendix leaked re-parsed content into a historical report")
	}
}

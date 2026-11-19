package service

// Outline evidence preview (doc/288 §9.6).
//
// The confirmation screen is the only human decision point in the whole run, so it
// is also the only place where "the knowledge base has nothing for this section" can
// still be acted on cheaply. These tests pin the three properties that make the
// preview trustworthy rather than decorative: it is frozen from a real retrieval, a
// retrieval that did not happen stays *unjudged* (never "plenty"), and a preview
// failure never blocks the run.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestOutlineFreezesAnEvidencePreviewPerSection(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-0001")
	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("the outline must materialise sections")
	}
	// One retrieval per section: the preview describes *this* section's query, so a
	// single shared search would report the report's opening hits for every section.
	if got := engine.retrievalCount(); got != len(sections) {
		t.Fatalf("%d sections must make %d preview retrievals, got %d", len(sections), len(sections), got)
	}
	for _, section := range sections {
		preview := reportSectionEvidencePreview(section.EvidencePreviewJSON)
		if preview == nil {
			t.Fatalf("section %s has no frozen preview", section.SectionKey)
		}
		if preview.HitChunks != 2 {
			t.Fatalf("section %s must report the 2 chunks the fixture engine returned, got %d",
				section.SectionKey, preview.HitChunks)
		}
		if len(preview.TopSources) != 1 || preview.TopSources[0] != "制度汇编.md" {
			t.Fatalf("section %s must name the source document, got %v",
				section.SectionKey, preview.TopSources)
		}
		if preview.Insufficient == nil {
			t.Fatalf("section %s retrieved successfully, so the shortage must be judged", section.SectionKey)
		}
		if preview.Note != "" {
			t.Fatalf("a successful preview carries no note, got %q", preview.Note)
		}
	}

	// The read surface hands the same preview to the confirmation screen.
	snapshot, err := fx.svc.GetReportJobSnapshot(context.Background(), fx.owner.ID, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sections) != len(sections) {
		t.Fatalf("snapshot must carry every section, got %d of %d", len(snapshot.Sections), len(sections))
	}
	for _, section := range snapshot.Sections {
		if section.EvidencePreview == nil {
			t.Fatalf("snapshot section %s lost its preview", section.SectionKey)
		}
		if section.EvidencePreview.HitChunks != 2 {
			t.Fatalf("snapshot preview disagrees with the row: %d", section.EvidencePreview.HitChunks)
		}
	}
}

// TestAnUnavailablePreviewIsUnjudgedAndDoesNotBlockTheOutline: the engine is down,
// which is a normal morning. The preview must say "did not look" — a `false`
// here would tell the reviewer the section is well supported by evidence nobody
// fetched, and an error would cancel a run that only wanted to be helpful.
func TestAnUnavailablePreviewIsUnjudgedAndDoesNotBlockTheOutline(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-down")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-down")
	engine.mu.Lock()
	engine.retrieveErr = errors.New("engine is unreachable")
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	revision, err := fx.svc.Store.GetReportRevision(context.Background(), fx.tenantID, created.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != model.ReportRevisionAwaitingOutline {
		t.Fatalf("a preview failure must not stop the outline, revision is %q", revision.Status)
	}
	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("the outline must still materialise its sections")
	}
	for _, section := range sections {
		preview := reportSectionEvidencePreview(section.EvidencePreviewJSON)
		if preview == nil {
			t.Fatalf("section %s must still carry a preview payload", section.SectionKey)
		}
		if preview.Insufficient != nil {
			t.Fatalf("section %s was never looked at, yet the preview judged it %v",
				section.SectionKey, *preview.Insufficient)
		}
		if preview.Note != "retrieval_unavailable" {
			t.Fatalf("section %s must say why it is unjudged, got %q", section.SectionKey, preview.Note)
		}
		if preview.TopSources == nil {
			t.Fatalf("section %s must serialise an empty source list, not null", section.SectionKey)
		}
	}
}

// TestEvidencePreviewShortageIsMeasuredAgainstMinEvidence is the one judgement in
// the preview, so it is pinned per case rather than by example.
func TestEvidencePreviewShortageIsMeasuredAgainstMinEvidence(t *testing.T) {
	cases := []struct {
		name     string
		requires bool
		hits     int
		min      int
		want     bool
	}{
		{"nothing found where two were required", true, 0, 2, true},
		{"exactly the minimum", true, 2, 2, false},
		{"more than the minimum", true, 5, 2, false},
		{"one short", true, 1, 2, true},
		{"an optional-evidence section has no shortage", false, 0, 2, false},
		// `min_evidence = 0` is a standard of zero, not "unset" (§7.8): there is no
		// scarcity to report, and calling it insufficient would invent a rule the
		// template never declared.
		{"no declared minimum", true, 0, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := reportEvidencePreviewInsufficient(testCase.requires, testCase.hits, testCase.min)
			if got == nil {
				t.Fatal("a computed preview must judge the section")
			}
			if *got != testCase.want {
				t.Fatalf("insufficient = %v, want %v", *got, testCase.want)
			}
		})
	}
}

// TestEvidencePreviewCapsItsSourceList: a preview is a summary. Five chunks from two
// documents are five hits but two sources, and a section that hits ten documents
// names three — the list is for orientation, not for reading.
func TestEvidencePreviewCapsItsSourceList(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-cap")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-cap")
	chunks := make([]map[string]interface{}, 0, 6)
	for index, name := range []string{"甲.md", "甲.md", "乙.md", "丙.md", "丁.md", "甲.md"} {
		chunks = append(chunks, map[string]interface{}{
			"chunk_id":    "chunk-" + name + string(rune('0'+index)),
			"document_id": "doc-" + name, "document_name": name,
			"dataset_id": "engine-ds", "content": "内容 " + name,
		})
	}
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: chunks}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	preview := reportSectionEvidencePreview(sections[0].EvidencePreviewJSON)
	if preview == nil {
		t.Fatal("no preview")
	}
	if preview.HitChunks != 6 {
		t.Fatalf("hits count chunks, got %d", preview.HitChunks)
	}
	want := []string{"甲.md", "乙.md", "丙.md"}
	if len(preview.TopSources) != len(want) {
		t.Fatalf("top sources must be capped at %d distinct documents, got %v",
			reportEvidencePreviewLimit, preview.TopSources)
	}
	for index, name := range want {
		if preview.TopSources[index] != name {
			t.Fatalf("top sources must keep first-seen order, got %v", preview.TopSources)
		}
	}
}

// TestARowWithoutAPreviewReadsAsUnjudged, not as "supported": rows written before
// v175 (and unreadable payloads) have nothing frozen, so the read surface must hand
// back nil.
func TestARowWithoutAPreviewReadsAsUnjudged(t *testing.T) {
	if got := reportSectionEvidencePreview(""); got != nil {
		t.Fatalf("an empty column means nothing was frozen, got %+v", got)
	}
	if got := reportSectionEvidencePreview("{not json"); got != nil {
		t.Fatalf("an unreadable preview must not be guessed at, got %+v", got)
	}
	decoded := reportSectionEvidencePreview(`{"hit_chunks":3,"top_sources":[],"insufficient":null}`)
	if decoded == nil || decoded.TopSources == nil {
		t.Fatalf("a null source list must decode to an empty list, got %+v", decoded)
	}
	if decoded.Insufficient != nil {
		t.Fatalf("null insufficient must stay unjudged, got %v", *decoded.Insufficient)
	}
}

// TestThePreviewNamesTheFileNotTheId pins the shape RAGFlow v1.0.0 actually answers.
//
// `/retrieval` chunks carry `document_id` + `document_keyword` (the file name); the
// `document_name`/`doc_name` keys the preview preferred are simply absent, so the
// live screen read "命中 8 段 · 725ea28d… 9a9f2f77…" — hits counted, sources
// unreadable (4.151, 2026-11-18). The id is still the last resort, but it must not
// win over a name that is right there.
func TestThePreviewNamesTheFileNotTheId(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-keyword")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-keyword")
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "c1", "document_id": "725ea28d000000000000000000000001",
			"document_keyword": "差旅与费用报销制度.md", "dataset_id": "engine-ds",
			"content": "差旅住宿标准为每晚上限 600 元。", "similarity": 0.8,
		},
		{
			"chunk_id": "c2", "document_id": "725ea28d000000000000000000000001",
			"document_keyword": "差旅与费用报销制度.md", "dataset_id": "engine-ds",
			"content": "超标部分需部门负责人书面审批。", "similarity": 0.7,
		},
	}}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	preview := reportSectionEvidencePreview(sections[0].EvidencePreviewJSON)
	if preview == nil {
		t.Fatal("no preview")
	}
	if len(preview.TopSources) != 1 || preview.TopSources[0] != "差旅与费用报销制度.md" {
		t.Fatalf("top sources must name the file (document_keyword), got %v", preview.TopSources)
	}
}

// TestTheCitationLedgerNamesTheFileNotTheId: the same retrieval feeds the evidence
// rows a reader clicks in the PDF, so the label there must be the same name.
func TestTheCitationLedgerNamesTheFileNotTheId(t *testing.T) {
	if got := reportChunkDocumentLabel(map[string]interface{}{
		"document_id":      "725ea28d000000000000000000000001",
		"document_keyword": "差旅与费用报销制度.md",
	}, "725ea28d000000000000000000000001"); got != "差旅与费用报销制度.md" {
		t.Fatalf("document_keyword must win over the id, got %q", got)
	}
	// An explicit name beats the keyword (older engine builds).
	if got := reportChunkDocumentLabel(map[string]interface{}{
		"document_name": "制度汇编.md", "document_keyword": "制度汇编.md (1)",
	}, ""); got != "制度汇编.md" {
		t.Fatalf("document_name must stay the first choice, got %q", got)
	}
	// Nothing readable: the id is traceable, a blank label is not.
	if got := reportChunkDocumentLabel(map[string]interface{}{"document_id": "doc-9"}, "doc-9"); got != "doc-9" {
		t.Fatalf("the id is the last resort, got %q", got)
	}
	if got := reportChunkDocumentLabel(map[string]interface{}{}, ""); got != "" {
		t.Fatalf("a chunk with no identifiers labels nothing, got %q", got)
	}
}

// TestThePreviewShowsTheTopPassage is R13's cheap half: the reviewer decides whether
// to spend the run, and "命中 3 段" alone does not say what the section would be
// written from. The excerpt is clipped and flattened because the dialog renders it in
// one row.
func TestThePreviewShowsTheTopPassage(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-excerpt")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-excerpt")
	long := "差旅住宿标准为每晚上限 600 元。\n\n超标部分需部门负责人书面审批，并在报销单上注明事由与金额。" +
		"本段故意写得很长，用来证明预览只保留开头，而不是把整段检索结果塞进确认对话框。"
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "c1", "document_id": "doc-1", "document_keyword": "差旅制度.md",
			"dataset_id": "engine-ds", "content": "第一段排序在前的命中内容。", "similarity": 0.9,
		},
		{
			"chunk_id": "c2", "document_id": "doc-2", "document_keyword": "费用制度.md",
			"dataset_id": "engine-ds", "content": long, "similarity": 0.5,
		},
	}}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	preview := reportSectionEvidencePreview(sections[0].EvidencePreviewJSON)
	if preview == nil {
		t.Fatal("no preview")
	}
	// The engine answers in rank order, so the first chunk is the passage the writer
	// would lean on most — not the longest one.
	if preview.Excerpt != "第一段排序在前的命中内容。" {
		t.Fatalf("the excerpt must be the best-ranked passage, got %q", preview.Excerpt)
	}
	// Same thing, built long enough to be clipped: the limit is a limit, and the
	// ellipsis is the only thing that grows it.
	oversized := strings.Repeat("住宿标准为每晚 600 元。", reportEvidencePreviewExcerptRunes)
	clipped := reportPreviewExcerpt(oversized)
	if got := len([]rune(clipped)); got != reportEvidencePreviewExcerptRunes+1 {
		t.Fatalf("a long passage is clipped to %d runes plus the ellipsis, got %d: %q",
			reportEvidencePreviewExcerptRunes, got, clipped)
	}
	if !strings.HasSuffix(clipped, "…") {
		t.Fatalf("a clipped passage must say so: %q", clipped)
	}
	if got := reportPreviewExcerpt("短句。"); got != "短句。" {
		t.Fatalf("a short passage is left alone, got %q", got)
	}
	if got := reportPreviewExcerpt("甲\n\n乙"); got != "甲 乙" {
		t.Fatalf("newlines are flattened, got %q", got)
	}
}

// TestThePreviewWarnsWhenItsSourcesDisagree is R13's P1 half (§9.6「P1 加支持度与
// 冲突提示」): the same detector the written section uses, run early enough that the
// reviewer can unbind a dataset before paying for the run.
func TestThePreviewWarnsWhenItsSourcesDisagree(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-conflict")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-conflict")
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "c1", "document_id": "doc-1", "document_keyword": "差旅制度.md",
			"dataset_id": "engine-ds", "content": "住宿标准为每晚 600 元。", "similarity": 0.9,
		},
		{
			"chunk_id": "c2", "document_id": "doc-2", "document_keyword": "费用制度.md",
			"dataset_id": "engine-ds", "content": "住宿标准为每晚 800 元。", "similarity": 0.8,
		},
	}}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	preview := reportSectionEvidencePreview(sections[0].EvidencePreviewJSON)
	if preview == nil {
		t.Fatal("no preview")
	}
	if preview.ConflictCount != 1 || len(preview.ConflictLabels) != 1 {
		t.Fatalf("the disagreeing sources must be disclosed, got %+v", preview)
	}
	if !strings.Contains(preview.ConflictLabels[0], "600") ||
		!strings.Contains(preview.ConflictLabels[0], "800") {
		t.Fatalf("the conflict line must name both values: %q", preview.ConflictLabels[0])
	}
	// It is a hint, not a verdict: the shortage judgement still comes from the hit
	// count, because the writer may never cite either chunk.
	if preview.Insufficient == nil {
		t.Fatal("a successful preview must still judge shortage")
	}
}

func TestPreviewConflictsKeepOnlyTheFirstFewAndIgnoreStaleness(t *testing.T) {
	items := []reportEvidenceItem{
		{Index: 1, DocumentLabel: "A.md", Content: "住宿标准为每晚 600 元。"},
		{Index: 2, DocumentLabel: "B.md", Content: "住宿标准为每晚 800 元。"},
	}
	count, labels := reportPreviewConflicts(items)
	if count != 1 || len(labels) != 1 {
		t.Fatalf("want one conflict, got %d / %v", count, labels)
	}
	// One document holding two values is a stale copy of itself — the preview cannot
	// tell that from a chunk quoting a superseded number, and a warning that cannot
	// be acted on trains the reviewer to ignore the dialog.
	stale := []reportEvidenceItem{
		{Index: 1, DocumentLabel: "A.md", Content: "住宿标准为每晚 600 元。"},
		{Index: 2, DocumentLabel: "A.md", Content: "住宿标准为每晚 800 元。"},
	}
	if count, labels := reportPreviewConflicts(stale); count != 0 || labels != nil {
		t.Fatalf("staleness is not a preview conflict, got %d / %v", count, labels)
	}
	if count, labels := reportPreviewConflicts(items[:1]); count != 0 || labels != nil {
		t.Fatalf("one source cannot disagree with itself, got %d / %v", count, labels)
	}
}

package service

// Evidence snapshot truncation (doc/288 §7.5 / A33).
//
// The promise A33 protects is narrow and checkable: for any chunk, the snapshot
// stored for a citation contains the snippet printed next to that citation. Two
// decisions follow from it, and both are asserted here rather than assumed:
//
//   - the snapshot window is a **prefix** of the chunk, sized to the 8KB budget, so
//     "the snippet is inside it" is structural rather than a search that can fail;
//   - the citation snippet carries **no ellipsis**, because "住宿标准为每晚上…" is not
//     a substring of the source text and a pointer nobody can verify is not evidence.

import (
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// longReportChunk builds a chunk whose UTF-8 size exceeds the snapshot budget while
// every rune is CJK (3 bytes), so a byte-budget cut and a rune-budget cut disagree —
// which is the case where splitting a rune would corrupt the stored JSON.
func longReportChunk() string {
	return strings.Repeat("住宿标准为每晚上限六百元人民币。", 500)
}

func TestReportEvidenceSnapshotStaysWithinBudgetAndKeepsTheSnippet(t *testing.T) {
	content := longReportChunk()
	if len(content) <= reportEvidenceSnapshotMaxBytes {
		t.Fatalf("fixture must exceed the budget: %d bytes", len(content))
	}

	window, strategy, ok := reportEvidenceWindow(content)
	if !ok {
		t.Fatal("a CJK chunk must always yield a window")
	}
	if strategy != model.ReportEvidenceTruncationExcerptWindow {
		t.Fatalf("an over-budget chunk must be marked excerpt_window, got %q", strategy)
	}
	if len(window) > reportEvidenceSnapshotMaxBytes {
		t.Fatalf("the window must respect the byte budget: %d bytes", len(window))
	}
	if !strings.HasPrefix(content, window) {
		t.Fatal("the window must be a prefix of the chunk")
	}
	if !utf8Valid(window) {
		t.Fatal("the window must not split a rune: JSON round-trips would corrupt it")
	}

	// A33's substring assertion, on the exact string the citation prints.
	snippet := reportEvidenceSnippet(content)
	if !strings.Contains(window, snippet) {
		t.Fatalf("the snapshot must contain its citation snippet verbatim: %q", snippet)
	}
	if strings.Contains(snippet, "...") {
		t.Fatal("the citation snippet must be a literal prefix, not an elided one")
	}

	// A short chunk is stored verbatim: truncation is a budget mechanism, not a
	// default, otherwise every appendix would claim to be an excerpt.
	short := "住宿标准为每晚上限六百元人民币。"
	verbatim, shortStrategy, ok := reportEvidenceWindow(short)
	if !ok || verbatim != short || shortStrategy != model.ReportEvidenceTruncationNone {
		t.Fatalf("a small chunk must be stored verbatim: %q / %q", verbatim, shortStrategy)
	}
}

func TestReportEvidenceRowsRecordTheTruncationStrategy(t *testing.T) {
	now := time.Now().UTC()
	content := longReportChunk()
	items := []reportEvidenceItem{
		{Index: 0, ChunkID: "chunk-big", DatasetID: "ds-1", DocumentID: "doc-1", Content: content, ContentHash: "hash-big"},
		{Index: 1, ChunkID: "chunk-small", DatasetID: "ds-1", DocumentID: "doc-1", Content: "短片段。", ContentHash: "hash-small"},
		{Index: 2, ChunkID: "chunk-uncited", DatasetID: "ds-1", DocumentID: "doc-1", Content: content, ContentHash: "hash-uncited"},
	}
	rows, err := reportEvidenceRows(items, map[int]bool{0: true, 1: true}, "t-a33", now.AddDate(0, 0, 90), now)
	if err != nil {
		t.Fatalf("cited chunks must snapshot: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("only cited chunks are snapshotted, got %d rows", len(rows))
	}
	big := rows[0]
	if !big.Truncated || big.TruncationStrategy != model.ReportEvidenceTruncationExcerptWindow {
		t.Fatalf("the oversized chunk must be flagged as truncated: %+v", big)
	}
	// `source_content_size` is the chunk as retrieved; `content_size` is the copy we
	// stored. Keeping them distinct is what lets an auditor notice the trim.
	if big.SourceContentSize <= big.ContentSize {
		t.Fatalf("source size %d must exceed stored size %d", big.SourceContentSize, big.ContentSize)
	}
	if big.ContentSize != int64(len([]rune(big.Content))) {
		t.Fatal("content_size must count the stored copy, not the source chunk")
	}
	if !strings.Contains(big.Content, reportEvidenceSnippet(content)) {
		t.Fatal("the stored snapshot lost the snippet it is cited for")
	}
	if big.ContentHash != hashReportContent(big.Content) {
		t.Fatal("the snapshot hash must describe the stored copy")
	}
	if rows[1].Truncated || rows[1].TruncationStrategy != model.ReportEvidenceTruncationNone {
		t.Fatalf("a small chunk must not claim truncation: %+v", rows[1])
	}
	if rows[1].Content != "短片段。" {
		t.Fatalf("a small chunk must be stored verbatim, got %q", rows[1].Content)
	}
}

// TestReportEvidenceSnapshotFreezesTheSourceNotThePromptView pins the split that
// makes `truncated` mean something: the prompt block is clipped to the section's
// 1200-rune budget, the snapshot is capped by the 8KB budget. Snapshotting the
// clipped text would produce a 1200-rune copy labelled `truncated=false` — an
// appendix that claims completeness it does not have (A20/A33).
func TestReportEvidenceSnapshotFreezesTheSourceNotThePromptView(t *testing.T) {
	now := time.Now().UTC()
	source := longReportChunk()
	item := reportEvidenceItem{
		Index: 0, ChunkID: "chunk-big", DatasetID: "ds-1", DocumentID: "doc-1",
		Content:       string([]rune(source)[:reportEvidenceSnippetRunes]),
		SourceContent: source,
		ContentHash:   hashReportContent(strings.TrimSpace(source)),
	}
	if len(item.Content) >= len(source) {
		t.Fatal("the fixture must make the prompt view shorter than the chunk")
	}
	rows, err := reportEvidenceRows([]reportEvidenceItem{item}, map[int]bool{0: true},
		"t-a20", now.AddDate(0, 0, 90), now)
	if err != nil || len(rows) != 1 {
		t.Fatalf("snapshot: %v (%d)", err, len(rows))
	}
	row := rows[0]
	if row.Content == item.Content {
		t.Fatal("the snapshot must not be the prompt excerpt")
	}
	if !strings.HasPrefix(source, row.Content) {
		t.Fatal("the snapshot must stay a prefix of the chunk")
	}
	if len(row.Content) > reportEvidenceSnapshotMaxBytes {
		t.Fatalf("the snapshot must respect the 8KB budget: %d bytes", len(row.Content))
	}
	if !row.Truncated || row.TruncationStrategy != model.ReportEvidenceTruncationExcerptWindow {
		t.Fatalf("an 8KB-capped copy of a larger chunk must disclose the cut: %+v", row)
	}
	if row.SourceContentSize != int64(len([]rune(source))) {
		t.Fatalf("source_content_size must describe the chunk, not the copy: %d", row.SourceContentSize)
	}
	if row.ContentSize >= row.SourceContentSize {
		t.Fatalf("content_size %d must be smaller than the source it was cut from %d",
			row.ContentSize, row.SourceContentSize)
	}
	// A33 still holds on the new split: the snippet printed next to the citation is
	// inside the frozen copy.
	if !strings.Contains(row.Content, reportEvidenceSnippet(item.Content)) {
		t.Fatal("the frozen copy must contain its citation snippet")
	}
}

// utf8Valid keeps the encoding assertion readable at the call site.
func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "\uFFFD") == value
}

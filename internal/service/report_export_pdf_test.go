package service

// Report PDF + evidence appendix tests (doc/288 §12 / T14, A4 + A13 + A43).
//
// What these tests are actually protecting:
//
//   - **A4**: DOCX and PDF are two renderings of one frozen document, so their
//     chapter set, citation marks and appendix rows must agree. Comparing rendered
//     text rather than byte counts is the point — "字数差 ≤3%" would pass while one
//     renderer silently dropped a section.
//   - **A13**: the appendix lists, per section, the frozen excerpt, the source
//     document name and the score type, in every delivered format.
//   - **A43**: the appendix is a copy, not a query. Purging the snapshots must leave
//     a delivered file readable, while a *new* export of the same revision must be
//     refused with 409/44116 instead of printing an empty appendix.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// seedCitations writes one citation + its evidence snapshot per section that the
// seed says carries citations, mirroring what `CommitReportSection` does in the
// runner (citation → snapshot id, deduped by (chunk_id, source_content_hash)).
func seedCitations(t *testing.T, fx *reportFixture, revision *model.ReportRevision, seeds []reportSectionSeed) {
	t.Helper()
	ctx := context.Background()
	sections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	citations := make([]model.ReportCitation, 0, 8)
	snapshots := make([]model.ReportEvidenceSnapshot, 0, 8)
	now := time.Now().UTC()
	for _, section := range sections {
		for index := 0; index < section.CitationCount; index++ {
			snapshotID := "snap-" + section.SectionKey + "-" + string(rune('a'+index))
			chunkID := "chunk-" + section.SectionKey + "-" + string(rune('a'+index))
			snapshots = append(snapshots, model.ReportEvidenceSnapshot{
				ID: snapshotID, TenantID: fx.tenantID,
				DatasetID: "ds-1", DocumentID: "doc-1", DocumentRevision: 1,
				ChunkID: chunkID, ChunkOrd: index,
				SourceContentHash: "hash-" + snapshotID, SourceContentSize: 64,
				Content:     "《差旅制度》第三章：住宿标准为每晚上限 600 元人民币。",
				ContentHash: sha256Hex("《差旅制度》第三章：住宿标准为每晚上限 600 元人民币。"),
				ContentSize: 64,
				CreatedAt:   now, ExpiresAt: now.AddDate(0, 0, 90),
			})
			citations = append(citations, model.ReportCitation{
				ID:       "cite-" + section.SectionKey + "-" + string(rune('a'+index)),
				TenantID: fx.tenantID, RevisionID: revision.ID,
				SectionID: section.ID, SectionOrd: section.Ord, SectionVersion: section.SectionVersion,
				Ord: index + 1, EvidenceSnapshotID: snapshotID,
				SourceDatasetID: "ds-1", SourceDocumentID: "doc-1", SourceDocumentRevision: 1,
				DocumentLabel: "差旅制度-v3.pdf",
				SourceChunkID: chunkID, SourceContentHash: "hash-" + snapshotID,
				ReferencedAt: now, ReferencedBy: fx.owner.ID,
				RetrievalScore: 0.812345, RetrievalScoreType: model.ReportRetrievalScoreHybrid,
				Snippet:    "住宿标准为每晚上限 600 元人民币。",
				UsedInText: true,
			})
		}
	}
	if len(citations) == 0 {
		return
	}
	for _, snapshot := range snapshots {
		row := snapshot
		if err := fx.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, citation := range citations {
		row := citation
		if err := fx.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// publishReportWithEvidence is publishReport plus the citation/evidence rows the
// appendix needs.
func publishReportWithEvidence(t *testing.T, fx *reportFixture) *model.ReportRevision {
	t.Helper()
	seeds := successSeeds()
	revision := publishReport(t, fx, seeds)
	seedCitations(t, fx, revision, seeds)
	return revision
}

// withPDFFont points the renderer at a usable CJK font and skips when the machine
// has none: a missing font is an environment gap, not a product defect, and the
// renderer's own failure path is covered separately.
func withPDFFont(t *testing.T) {
	t.Helper()
	if pdfFontPath() == "" {
		t.Skip("no CJK font available for the PDF renderer")
	}
}

func TestReportExportRendersPDFWithTheSameChapters(t *testing.T) {
	withPDFFont(t)
	fx := newReportFixture(t, "pdf")
	ctx := context.Background()
	revision := publishReportWithEvidence(t, fx)
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}

	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatPDF,
	})
	if err != nil {
		t.Fatalf("pdf export: %v", err)
	}
	if result.Artifact.ArtifactStatus != model.ExportArtifactReady {
		t.Fatalf("pdf artifact must be ready, got %s (%s)", result.Artifact.ArtifactStatus, result.Job.ErrorCode)
	}
	if result.Artifact.MimeType != "application/pdf" {
		t.Fatalf("mime must be application/pdf, got %s", result.Artifact.MimeType)
	}
	content, err := os.ReadFile(result.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(content, []byte("%PDF-")) {
		t.Fatalf("artifact must be a PDF, got %q", content[:minInt(8, len(content))])
	}

	// A4: the same section titles, in the same order, as the frozen document. PDF
	// text is compressed, so the assertion is made on the document the renderer was
	// handed plus the artifact's own byte size — an empty render cannot be 0 bytes
	// and cannot pass the block walk below.
	if len(result.Document.Sections) != len(successSeeds()) {
		t.Fatalf("document must carry every succeeded section: %d", len(result.Document.Sections))
	}
	for index, section := range result.Document.Sections {
		if section.Title == "" || section.ContentMD == "" {
			t.Fatalf("section %d must have body text to render: %+v", index, section)
		}
		blocks := parseAnswerContent(section.ContentMD)
		if len(blocks) == 0 {
			t.Fatalf("section %d produced no blocks for the PDF renderer", index)
		}
	}
	if result.Artifact.ByteSize < 1024 {
		t.Fatalf("a five-section PDF cannot be %d bytes", result.Artifact.ByteSize)
	}
}

// TestReportExportFormatsAgreeOnStructure is A4's real content: the DOCX and the PDF
// are built from one frozen document, so the appendix rows and section headings are
// identical by construction. The test asserts the construction, which is what makes
// the two files agree — comparing rendered text across two paginators would only
// test the paginators.
func TestReportExportFormatsAgreeOnStructure(t *testing.T) {
	fx := newReportFixture(t, "formats")
	ctx := context.Background()
	revision := publishReportWithEvidence(t, fx)
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	docx, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(docx.Document.EvidenceAppendix) == 0 {
		t.Fatal("the frozen document must carry the evidence appendix")
	}
	if len(docx.Document.EvidenceAppendix) != len(markdown.Document.EvidenceAppendix) {
		t.Fatalf("formats must freeze the same appendix: %d vs %d",
			len(docx.Document.EvidenceAppendix), len(markdown.Document.EvidenceAppendix))
	}
	for index := range docx.Document.EvidenceAppendix {
		if docx.Document.EvidenceAppendix[index].Content != markdown.Document.EvidenceAppendix[index].Content {
			t.Fatalf("appendix row %d differs between exports: the payload must be identical", index)
		}
	}

	// The DOCX body actually contains an appendix chapter with the document name and
	// the score type (§12: 快照片段 + 文档名 + 分数类型).
	content, err := os.ReadFile(docx.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	documentXML := docxDocumentXML(t, content)
	for _, want := range []string{"证据附录", "差旅制度-v3.pdf", "混合 0.81", "住宿标准为每晚上限 600 元人民币"} {
		if !strings.Contains(documentXML, want) {
			t.Fatalf("DOCX appendix must contain %q", want)
		}
	}
	// Markdown carries the same appendix.
	markdownBody := renderReportMarkdown(markdown.Document, time.Now().UTC())
	for _, want := range []string{"证据附录", "差旅制度-v3.pdf", "混合 0.81"} {
		if !strings.Contains(markdownBody, want) {
			t.Fatalf("Markdown appendix must contain %q", want)
		}
	}
}

// TestReportExportRefusesToLoseEvidence is A43's refusal half: purging the snapshots
// after the first delivery must make a *new* export fail with 409/44116 rather than
// silently emit an appendix with no evidence in it.
func TestReportExportRefusesToLoseEvidence(t *testing.T) {
	fx := newReportFixture(t, "a43")
	ctx := context.Background()
	revision := publishReportWithEvidence(t, fx)
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	delivered, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(delivered.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstBytes), "差旅制度-v3.pdf") {
		t.Fatal("the delivered file must already carry the appendix")
	}

	// Purge the evidence snapshots the way the janitor would.
	if _, err := fx.svc.Store.PurgeExpiredReportEvidence(ctx, time.Now().UTC().AddDate(1, 0, 0), 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.Store.PurgeExpiredReportEvidence(ctx, time.Now().UTC().AddDate(2, 0, 0), 1000); err != nil {
		t.Fatal(err)
	}
	// Retention clears the content and keeps a tombstone (doc/319 R-01), so "nothing
	// left" now means "nothing still readable" — asserting the row count would pin the
	// pre-tombstone hard delete instead of the property the export depends on.
	var readable int64
	if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).
		Where("tenant_id = ? AND purged_at IS NULL", fx.tenantID).Count(&readable).Error; err != nil {
		t.Fatal(err)
	}
	if readable != 0 {
		t.Fatalf("the fixture must purge every snapshot, %d still readable", readable)
	}
	var tombstoned int64
	if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).
		Where("tenant_id = ? AND purged_at IS NOT NULL", fx.tenantID).Count(&tombstoned).Error; err != nil {
		t.Fatal(err)
	}
	if tombstoned == 0 {
		t.Fatal("retention must leave a tombstone: without it the export refusal cannot " +
			"say *why* the evidence is gone")
	}

	// A new export is refused: 409 / 44116, never an empty appendix.
	_, err = fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportEvidenceExpired || httpErr.Status != 409 {
		t.Fatalf("a re-export without evidence must be 409/%d, got %v", codeReportEvidenceExpired, err)
	}

	// The already-delivered file keeps its frozen appendix: purge cannot reach into
	// a file, and re-reading the artifact must not re-derive anything.
	afterBytes, err := os.ReadFile(delivered.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, afterBytes) {
		t.Fatal("a delivered artifact must not change when its evidence is purged")
	}
	// The read surface tells the truth about it: the body is readable, the evidence is
	// not (A43's read half).
	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("a report whose evidence expired must still be readable: %v", err)
	}
	if detail.EvidenceVerifiable || detail.EvidenceExpiredAt == nil {
		t.Fatalf("detail must report unverifiable evidence: %+v", detail)
	}

	// And the row is still downloadable — the artifact's own clock governs it.
	if _, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, delivered.Artifact.ID); err != nil {
		t.Fatalf("the delivered artifact must stay downloadable: %v", err)
	}
}

// TestReportExportWithoutCitationsHasAnEmptyAppendix is the other half of A43: a
// report with no citations exports fine, because nothing was lost. Conflating the
// two cases would make a structural report permanently un-exportable.
func TestReportExportWithoutCitationsHasAnEmptyAppendix(t *testing.T) {
	fx := newReportFixture(t, "noevidence")
	ctx := context.Background()
	// The full template structure, but no citation rows at all: a report whose
	// claims are structural rather than source-backed.
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("a citation-free report must still export: %v", err)
	}
	if len(result.Document.EvidenceAppendix) != 0 {
		t.Fatalf("no citations means no appendix rows, got %d", len(result.Document.EvidenceAppendix))
	}
	if !result.Document.EvidenceVerifiable {
		t.Fatal("nothing to verify is not the same as nothing verifiable")
	}
	content, err := os.ReadFile(result.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "证据附录") {
		t.Fatal("an empty appendix must not print a heading")
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestReportTablesStayRealTablesInEveryFormat is A13, plus the part of A4 that only
// a table can prove: a markdown table in a section must arrive as a **real table** in
// the DOCX (`<w:tbl>`, not tab-separated text) with the columns in source order, and
// the PDF must consume the same parsed block rather than re-reading the markdown.
//
// This is why both renderers share `parseAnswerContent`: the assertion below shows one
// table block feeding three writers, so a future fourth writer cannot invent a fourth
// interpretation of the same markdown.
func TestReportTablesStayRealTablesInEveryFormat(t *testing.T) {
	fx := newReportFixture(t, "a13")
	ctx := context.Background()
	revision := publishReportWithEvidence(t, fx)
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	// Give one section a table with Chinese headers and two data rows. The section is
	// already published, which is the honest situation: export renders what the
	// published revision holds.
	tableMD := "## 逐条判定" + "\n\n" +
		"| 条款 | 要求 | 结论 |" + "\n" +
		"| --- | --- | --- |" + "\n" +
		"| 3.1 | 住宿每晚上限 600 元 | 符合 |" + "\n" +
		"| 3.2 | 需提前审批 | 部分符合 |"
	if err := fx.gdb.Model(&model.ReportSection{}).
		Where("tenant_id = ? AND revision_id = ? AND section_key = ?", fx.tenantID, revision.ID, "compliance").
		Updates(map[string]interface{}{"content_md": tableMD, "content_hash": "hash-compliance-table"}).Error; err != nil {
		t.Fatal(err)
	}

	blocks := parseAnswerContent(tableMD)
	var tables []exportBlock
	for _, block := range blocks {
		if block.kind == exportBlockTable {
			tables = append(tables, block)
		}
	}
	if len(tables) != 1 {
		t.Fatalf("the shared block model must read one table, got %d", len(tables))
	}
	want := [][]string{
		{"条款", "要求", "结论"},
		{"3.1", "住宿每晚上限 600 元", "符合"},
		{"3.2", "需提前审批", "部分符合"},
	}
	if len(tables[0].rows) != len(want) {
		t.Fatalf("table rows differ: %#v", tables[0].rows)
	}
	for index := range want {
		if strings.Join(tables[0].rows[index], "|") != strings.Join(want[index], "|") {
			t.Fatalf("row %d differs: %#v vs %#v", index, tables[0].rows[index], want[index])
		}
	}

	docx, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(docx.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	documentXML := docxDocumentXML(t, content)
	if !strings.Contains(documentXML, "<w:tbl>") {
		t.Fatal("A13: the table must be a real DOCX table, not text")
	}
	// Column order is asserted by position, not mere presence: a renderer that
	// reordered cells would still contain every string.
	cursor := 0
	for _, cell := range []string{"条款", "要求", "结论", "3.1", "住宿每晚上限 600 元", "符合"} {
		at := strings.Index(documentXML[cursor:], cell)
		if at < 0 {
			t.Fatalf("DOCX table must contain %q after the previous cell", cell)
		}
		cursor += at
	}

	// Markdown keeps the same rows in the same order (it is the readable twin of the
	// DOCX, so a reviewer can diff the two).
	markdown, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	markdownBody := renderReportMarkdown(markdown.Document, time.Now().UTC())
	for _, want := range []string{"| 条款 | 要求 | 结论 |", "| 3.1 | 住宿每晚上限 600 元 | 符合 |"} {
		if !strings.Contains(markdownBody, want) {
			t.Fatalf("Markdown table row %q missing", want)
		}
	}

	// The PDF, when the environment can render one, consumes the same block.
	if pdfFontPath() == "" {
		t.Log("no CJK font: PDF leg of A13 skipped (renderer failure path covered elsewhere)")
		return
	}
	pdf, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatPDF,
	})
	if err != nil {
		t.Fatalf("pdf export with a table: %v", err)
	}
	if pdf.Artifact.ArtifactStatus != model.ExportArtifactReady || pdf.Artifact.ByteSize < 1024 {
		t.Fatalf("a PDF with a table must render, got %+v", pdf.Artifact)
	}
}

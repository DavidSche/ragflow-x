package service

// Cover, table of contents and figure/table numbering (doc/288 §12, doc/44 「好用」
// gap #5).
//
// The delivered file is the artefact a customer forwards, so the front matter is
// part of the contract, not decoration:
//
//   - page 1 is the cover; it states title, version, generated time and completeness,
//     and it carries no running header or page number;
//   - page 2 is the contents, and every row's number must equal the page that heading
//     actually landed on. That is the assertion that bites: the numbers come from a
//     first layout pass, and a renderer that hard-codes "body starts on page 3" starts
//     printing wrong numbers the moment the TOC spills onto a second page;
//   - figures and tables are numbered across the whole document and the three formats
//     agree, because two files that claim to be the same report must not label the
//     same table differently (A4).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// pdfFontBytesForTest loads the same font the renderer uses, or skips: the font is a
// deployment requirement (Dockerfiles bake it), not a property of this machine.
func pdfFontBytesForTest(t *testing.T) []byte {
	t.Helper()
	path := pdfFontPath()
	if path == "" {
		t.Skip("no CJK font available: PDF layout assertions need one")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pdf font: %v", err)
	}
	trimmed, err := sfntFontBytes(raw)
	if err != nil {
		t.Fatalf("normalize pdf font: %v", err)
	}
	return trimmed
}

func tocTestDocument(sections int) *reportDocument {
	document := &reportDocument{
		Title: "动火作业风险分析", Question: "动火作业有哪些风险？",
		Revision: 2, TemplateKey: "analysis", TemplateVersion: 1,
		Completeness: model.ReportCompletenessFull,
	}
	for index := 0; index < sections; index++ {
		document.Sections = append(document.Sections, reportDocumentSection{
			Ord: index, Title: fmt.Sprintf("第 %d 章", index+1),
			ContentMD: "本节正文，用于占据版面。",
		})
	}
	return document
}

func TestReportPdfCoverAndContentsPointAtRealPages(t *testing.T) {
	font := pdfFontBytesForTest(t)
	document := tocTestDocument(3)
	now := time.Now().UTC()

	pageOf, _, err := renderReportPdfPass(document, now, font, nil)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// Cover = 1, contents = 2, body starts on 3. These numbers are discovered, so a
	// renderer that forgot the contents page would report 2 and fail here.
	for ord := 0; ord < 3; ord++ {
		if pageOf[pdfSectionAnchorKey(ord)] != 3 {
			t.Fatalf("section %d must start on page 3, got %d (map %v)",
				ord, pageOf[pdfSectionAnchorKey(ord)], pageOf)
		}
	}

	// The printed rows must carry the measured pages, entry by entry: this is the
	// assertion a hard-coded "3" would fail as soon as the contents spills.
	for index, row := range reportPdfContentsRows(document, pageOf) {
		if row.Page != pageOf[row.Key] || row.Page != 3 {
			t.Fatalf("contents row %d prints page %d, heading landed on %d",
				index, row.Page, pageOf[row.Key])
		}
	}

	secondPass, output, err := renderReportPdfPass(document, now, font, pageOf)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	// Printing the numbers must not shift the pagination they describe.
	for key, page := range pageOf {
		if secondPass[key] != page {
			t.Fatalf("%s moved from page %d to %d once numbers were printed",
				key, page, secondPass[key])
		}
	}
	if pages := bytes.Count(output, []byte("/Type /Page\n")); pages < 3 {
		t.Fatalf("cover + contents + body need at least 3 pages, got %d", pages)
	}
	// The cover must be its own page: fpdf starts numbering at 1, and the body page
	// recorded above is 3, so nothing collapsed into the cover.
	if pageOf[pdfSectionAnchorKey(0)] == 1 {
		t.Fatal("the body must not begin on the cover page")
	}
}

func TestReportPdfContentsSurvivesASpillingToc(t *testing.T) {
	font := pdfFontBytesForTest(t)
	// 90 rows do not fit on one page: the contents spills, every section shifts by a
	// page, and a hard-coded offset would print numbers that are off by one.
	document := tocTestDocument(90)
	now := time.Now().UTC()

	pageOf, _, err := renderReportPdfPass(document, now, font, nil)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := pageOf[pdfSectionAnchorKey(0)]
	last := pageOf[pdfSectionAnchorKey(89)]
	if first == 3 {
		t.Fatalf("a 90-row contents cannot fit on one page: first section on page %d", first)
	}
	// The printed numbers follow the spill rather than a fixed offset.
	for index, row := range reportPdfContentsRows(document, pageOf) {
		if row.Page != pageOf[row.Key] || row.Page == 3 && index > 40 {
			t.Fatalf("contents row %d printed page %d for a heading on %d",
				index, row.Page, pageOf[row.Key])
		}
	}
	if last < first || last-first > 12 {
		t.Fatalf("sections must advance monotonically over a few pages: first %d last %d", first, last)
	}
	// The body is contiguous: 90 short sections cannot skip a page between neighbours
	// by more than one (each section is a heading + one line).
	previous := first
	for ord := 1; ord < 90; ord++ {
		page := pageOf[pdfSectionAnchorKey(ord)]
		if page < previous || page > previous+1 {
			t.Fatalf("section %d jumped from page %d to %d", ord, previous, page)
		}
		previous = page
	}
}

func TestReportPdfAndDocxNumberTablesAndFiguresTheSameWay(t *testing.T) {
	// One document, two tables in the first section and one figure in the second.
	document := &reportDocument{
		Title: "编号测试", Revision: 1, TemplateKey: "analysis", TemplateVersion: 1,
		Completeness: model.ReportCompletenessFull,
		Sections: []reportDocumentSection{
			{Ord: 0, Title: "上", ContentMD: "| a | b |\n| --- | --- |\n| 1 | 2 |\n\n| c | d |\n| --- | --- |\n| 3 | 4 |"},
			{Ord: 1, Title: "下", ContentMD: "![图注](img://chunk-1)"},
		},
	}

	// DOCX: the captions are XML paragraphs, so they can be asserted directly.
	docx, err := renderReportDocx(document, time.Now().UTC())
	if err != nil {
		t.Fatalf("docx: %v", err)
	}
	documentXML := docxDocumentXML(t, docx)
	// The cover/contents front matter exists and lists the chapters.
	if !strings.Contains(documentXML, "目录") {
		t.Fatal("DOCX must carry a 目录 (doc/288 §12)")
	}
	if !strings.Contains(documentXML, "1. 上") || !strings.Contains(documentXML, "2. 下") {
		t.Fatal("the DOCX contents must list every chapter")
	}
	for _, caption := range []string{"表 1", "表 2", "图 1"} {
		if !strings.Contains(documentXML, caption) {
			t.Fatalf("DOCX must number tables and figures (%q missing)", caption)
		}
	}
	// Numbering is document-wide: the figure in §2 is 图 1, not 图 3.
	cursor := strings.Index(documentXML, "表 2")
	if at := strings.Index(documentXML[cursor:], "图 1"); at < 0 {
		t.Fatal("table and figure counters must be independent and document-wide")
	}

	// PDF: same counters, asserted on the live renderer paths.
	font := pdfFontBytesForTest(t)
	pdf := newPdfForTest(t, font)
	numbers := &pdfCaptionNumbers{}
	for _, section := range document.Sections {
		for _, block := range parseAnswerContent(section.ContentMD) {
			pdfReportBlock(pdf, block, numbers)
		}
	}
	if numbers.table != 2 || numbers.image != 1 {
		t.Fatalf("PDF numbering must match DOCX (2 tables, 1 figure), got %d/%d",
			numbers.table, numbers.image)
	}
	if err := pdf.Error(); err != nil {
		t.Fatalf("numbering must not break the pdf writer: %v", err)
	}

	// Markdown carries the contents too. It has no page numbers, and it says so by
	// simply not printing any (a made-up number would be worse than none).
	markdown := renderReportMarkdown(document, time.Now().UTC())
	if !strings.Contains(markdown, "## 目录") {
		t.Fatal("Markdown must carry a 目录")
	}
	if !strings.Contains(markdown, "1. 上\n2. 下") {
		t.Fatalf("markdown contents must list the chapters in order:\n%s", markdown)
	}
	if strings.Contains(markdown, "### 目录") {
		t.Fatal("the contents is a document-level heading")
	}
}

func TestReportPdfContentsListsTheEvidenceAppendix(t *testing.T) {
	font := pdfFontBytesForTest(t)
	document := tocTestDocument(2)
	document.EvidenceAppendix = []reportEvidenceEntry{{
		SectionOrd: 0, SectionTitle: "第 1 章", Ord: 0,
		DocumentName: "制度汇编.md", Content: "住宿标准为每晚上限 600 元。",
		RetrievalScore: 0.82, RetrievalScoreType: "similarity",
	}}

	pageOf, _, err := renderReportPdfPass(document, time.Now().UTC(), font, nil)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	appendixPage, ok := pageOf[pdfAppendixAnchorKey]
	if !ok {
		t.Fatal("the appendix must be a contents entry: a reader needs to find it")
	}
	if appendixPage <= pageOf[pdfSectionAnchorKey(1)] {
		t.Fatalf("the appendix follows the body: appendix %d, last section %d",
			appendixPage, pageOf[pdfSectionAnchorKey(1)])
	}
}

// TestReportExportFrontMatterIsFrozenWithTheDocument pins the wiring, not the
// formatting: the front matter is rendered from the frozen payload at export time, so
// the delivered file can be reproduced from `rgx_export_artifact.document_json` alone.
func TestReportExportFrontMatterIsFrozenWithTheDocument(t *testing.T) {
	fx := newReportFixture(t, "front-matter")
	revision := publishReport(t, fx, successSeeds())

	exported, err := fx.svc.CreateReportExport(context.Background(), fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: revision.ReportID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if strings.TrimSpace(exported.Document.Title) == "" {
		t.Fatalf("the frozen payload must carry the title: %+v", exported.Document)
	}
	if len(exported.Document.Sections) == 0 {
		t.Fatal("the frozen payload must carry the sections the contents lists")
	}
	// The markdown body is regenerated from the frozen payload, which is what makes a
	// re-export byte-identical (idempotency) and what lets the TOC be rebuilt later.
	body := renderReportMarkdown(exported.Document, time.Now().UTC())
	if !strings.Contains(body, "## 目录") || !strings.Contains(body, exported.Document.Sections[0].Title) {
		t.Fatalf("front matter must come from the frozen document:\n%s", body)
	}
}

// newPdfForTest builds the same writer the renderer builds, so the numbering
// assertions exercise the real block path rather than a stand-in.
func newPdfForTest(t *testing.T, font []byte) *fpdf.Fpdf {
	t.Helper()
	writer := fpdf.New("P", "mm", "A4", "")
	writer.SetAutoPageBreak(true, 18)
	writer.AddUTF8FontFromBytes("cjk", "", font)
	writer.SetFont("cjk", "", 10)
	writer.AddPage()
	if err := writer.Error(); err != nil {
		t.Fatalf("pdf writer: %v", err)
	}
	return writer
}

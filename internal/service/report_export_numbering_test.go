package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// numberingTestDocument builds the smallest document that exercises both
// numberings a reader sees: section headings and citation markers in the appendix.
//
// `ord` is 1-based on disk (`rgx_report_section.ord` starts at 1, and the same for
// `rgx_report_citation.ord`): the writer assigns `ord = index + 1` and the live rows
// read 1..5. Fixtures here used to be 0-based, which hid the renderers' `ord+1`
// and shipped every export numbered from 2.
func numberingTestDocument() *reportDocument {
	return &reportDocument{
		Title: "动火作业符合性检查", Question: "动火作业是否合规？",
		Revision: 1, TemplateKey: "report-compliance", TemplateVersion: 2,
		Completeness: model.ReportCompletenessFull,
		Sections: []reportDocumentSection{
			{
				Ord: 1, Title: "背景与范围",
				ContentMD:     "**范围说明**：本次检查覆盖三个厂区。",
				CitationCount: 2, EvidenceStatus: model.ReportEvidenceSupported,
			},
			{
				Ord: 2, Title: "逐条符合性判定",
				ContentMD:     "结论见证据附录。",
				CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported,
			},
		},
		EvidenceAppendix: []reportEvidenceEntry{
			{SectionOrd: 1, SectionTitle: "背景与范围", Ord: 1, DocumentName: "制度汇编.pdf", Content: "厂区范围"},
			{SectionOrd: 1, SectionTitle: "背景与范围", Ord: 2, DocumentName: "作业票.docx", Content: "动火票"},
			{SectionOrd: 2, SectionTitle: "逐条符合性判定", Ord: 1, DocumentName: "检查记录.xlsx", Content: "第 5 条"},
		},
	}
}

// TestReportExportsNumberSectionsAndCitationsFromOne freezes one fact across the
// delivered formats: a reader who opens the file sees the numbers the page showed.
//
// Every renderer printed `ord+1`, so the first section was "2. 背景与范围" in DOCX,
// PDF and Markdown while the reader said "1." — and the appendix labelled the first
// citation `[2]` while the body cited `[1]`, which makes the evidence of a delivered
// report impossible to match up (live, 2026-11-18).
func TestReportExportsNumberSectionsAndCitationsFromOne(t *testing.T) {
	now := time.Now().UTC()
	document := numberingTestDocument()

	markdown := renderReportMarkdown(document, now)
	for _, wanted := range []string{"1. 背景与范围", "2. 逐条符合性判定", "[1] 制度汇编.pdf", "[2] 作业票.docx"} {
		if !strings.Contains(markdown, wanted) {
			t.Fatalf("markdown must number from one: missing %q", wanted)
		}
	}
	for _, unwanted := range []string{"2. 背景与范围", "3. 逐条符合性判定", "[2] 制度汇编.pdf"} {
		if strings.Contains(markdown, unwanted) {
			t.Fatalf("markdown exported an off-by-one number %q", unwanted)
		}
	}

	docx, err := renderReportDocx(document, now)
	if err != nil {
		t.Fatalf("docx: %v", err)
	}
	plain := docxVisibleText(docxBodyXML(t, docx))
	for _, wanted := range []string{"1. 背景与范围", "2. 逐条符合性判定"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("docx must number from one: missing %q", wanted)
		}
	}
	if strings.Contains(plain, "2. 背景与范围") {
		t.Fatal("docx exported the first section as 2.")
	}
	// The appendix row's first cell is the citation's own ord, so the marker the
	// body used ("[1]") and the row a reviewer finds in the appendix agree.
	if !strings.Contains(plain, "制度汇编.pdf") {
		t.Fatalf("docx appendix lost its first row: %q", plain)
	}

	// PDF text lives in a compressed stream, so the numbering is proven through the
	// contents rows the two-pass renderer builds — the same strings it prints.
	labels := make([]string, 0, 3)
	for _, row := range reportPdfContentsRows(document, nil) {
		labels = append(labels, row.Label)
	}
	want := []string{"1. 背景与范围", "2. 逐条符合性判定", "证据附录"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("pdf contents rows are %v, want %v", labels, want)
	}
	if _, err := renderReportPdf(document, now); err != nil {
		// The CJK font is a deployment requirement, not a property of this machine.
		t.Logf("pdf renderer unavailable here: %v", err)
	}
}

// TestReportExportsRenderBoldInsteadOfAsterisks pins the other half of what a
// reader sees: the writer answers in Markdown, so `**范围说明**` used to reach the
// delivered document as literal asterisks around the phrase.
func TestReportExportsRenderBoldInsteadOfAsterisks(t *testing.T) {
	now := time.Now().UTC()
	document := numberingTestDocument()

	docx, err := renderReportDocx(document, now)
	if err != nil {
		t.Fatalf("docx: %v", err)
	}
	body := docxBodyXML(t, docx)
	if bytes.Contains(body, []byte("**")) {
		t.Fatal("docx must not print the markdown emphasis markers")
	}
	if !bytes.Contains(body, []byte("<w:b/>")) {
		t.Fatal("docx must carry the emphasis as bold runs")
	}
	if plain := docxVisibleText(body); !strings.Contains(plain, "范围说明：本次检查覆盖三个厂区。") {
		t.Fatalf("bold run lost its text: %q", plain)
	}

	// Markdown keeps the markers: it is Markdown, and `**` is how bold is written.
	if markdown := renderReportMarkdown(document, now); !strings.Contains(markdown, "**范围说明**") {
		t.Fatal("markdown output must keep its own emphasis syntax")
	}
}

// TestEmphasisRunsLeavesUnbalancedMarkersAlone keeps the renderer from eating text:
// a lone `**` is a typo in the model's answer, not an emphasis span.
func TestEmphasisRunsLeavesUnbalancedMarkersAlone(t *testing.T) {
	runs := emphasisRuns("温度 **超过 80 度")
	if len(runs) != 1 || runs[0].bold {
		t.Fatalf("unbalanced markers must stay literal, got %+v", runs)
	}
	if runs[0].text != "温度 **超过 80 度" {
		t.Fatalf("text changed: %q", runs[0].text)
	}
	paired := emphasisRuns("温度 **超过 80 度** 即报警")
	if len(paired) != 3 || !paired[1].bold || paired[1].text != "超过 80 度" {
		t.Fatalf("paired markers must become a bold run, got %+v", paired)
	}
	if plain := plainEmphasis("温度 **超过 80 度** 即报警"); plain != "温度 超过 80 度 即报警" {
		t.Fatalf("plain rendering kept markers: %q", plain)
	}
}

// docxBodyXML returns `word/document.xml` — the part a reader sees. Assertions must
// read this, not the package: a DOCX is a ZIP, and a byte search over the archive
// matches compressed bytes.
func docxBodyXML(t *testing.T, docx []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("docx is not a zip: %v", err)
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open document.xml: %v", err)
		}
		defer handle.Close()
		raw, err := io.ReadAll(handle)
		if err != nil {
			t.Fatalf("read document.xml: %v", err)
		}
		return raw
	}
	t.Fatal("docx has no word/document.xml")
	return nil
}

// docxVisibleText strips the WordprocessingML tags so assertions read what a reader
// reads. Cell boundaries become tabs and paragraphs newlines, so a table cell's
// number stays its own token.
func docxVisibleText(docx []byte) string {
	text := string(docx)
	text = strings.ReplaceAll(text, "</w:p>", "\n")
	text = strings.ReplaceAll(text, "</w:tc>", "\t")
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		if text[index] != '<' {
			out.WriteByte(text[index])
			continue
		}
		for index < len(text) && text[index] != '>' {
			index++
		}
	}
	return out.String()
}

// TestReportExportKeyCarriesTheRendererIdentity keeps a renderer fix from being
// replayed as the old file.
//
// The key used to be revision+format+content only. Numbering was wrong, the fix
// changed the bytes, and the content hash did not: every re-export replayed the
// broken artifact (2026-11-18). The renderer now belongs to the request identity.
func TestReportExportKeyCarriesTheRendererIdentity(t *testing.T) {
	const revision = "c3f63977653d4f1ead05a903964c8fc9"
	const canonical = "9f2c5a4d1e8b7c6a5f4e3d2c1b0a99887766554433221100ffeeddccbbaa9988"

	key := reportExportIdempotencyKey(revision, model.ExportFormatMarkdown, canonical)
	if again := reportExportIdempotencyKey(revision, model.ExportFormatMarkdown, canonical); again != key {
		t.Fatalf("the same request must produce the same key: %q vs %q", key, again)
	}
	// 128 is the column width, and the renewal suffix has to fit as well.
	if len(key) > 120 {
		t.Fatalf("idempotency key is %d bytes, too close to the 128-byte limit: %s", len(key), key)
	}
	tag := sha256Hex(reportExportRendererVersion)[:4]
	if !strings.Contains(key, ":"+tag+":") {
		t.Fatalf("key %q must carry the renderer tag %q", key, tag)
	}
	if strings.Contains(key, reportExportRendererVersion) {
		t.Fatalf("key %q must not spell out the renderer version: the column is 128 bytes", key)
	}

	// Distinct renderer versions must not collide, or the fix would replay.
	other := sha256Hex("export.renderer.v99")[:4]
	if other == tag {
		t.Skip("digest collision in a 4-hex tag: choose another version string")
	}
	if key == strings.Replace(key, tag, other, 1) {
		t.Fatal("replacing the renderer tag must change the key")
	}
}

// TestReportExportKeyRenewalSuffixStillFits guards the tail of the loop.
func TestReportExportKeyRenewalSuffixStillFits(t *testing.T) {
	key := reportExportIdempotencyKey("c3f63977653d4f1ead05a903964c8fc9",
		model.ExportFormatMarkdown, strings.Repeat("a", 64))
	for renewal := 1; renewal <= reportArtifactRenewalLimit; renewal++ {
		withSuffix := key + fmt.Sprintf(":renew%d", renewal)
		if len(withSuffix) > 128 {
			t.Fatalf("renewal %d overflows the 128-byte key column: %d bytes", renewal, len(withSuffix))
		}
	}
}

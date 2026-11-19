package service

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The XLSX delivery format (queue item 4).
//
// The format's whole value is that a reviewer can *sort* it, so these tests check
// the three things that decide whether the file is a workbook or a wall of text:
//
//  1. **It is a valid package.** Every part is well-formed XML (decoded, not
//     regex-matched), the workbook names its sheets, and each sheet part exists.
//     Excel refuses the whole file on one malformed part, so "it opened for me" is
//     not a test — this is.
//  2. **Numbers are numbers.** `ord` and 引用数 travel as numeric cells, because a
//     column of text that looks numeric sorts as text ("10" before "9").
//  3. **Untrusted text cannot break the file.** Evidence excerpts are arbitrary
//     source bytes: an XML-illegal control character or a 40000-character chunk has
//     to be neutralised here, or the workbook is unopenable with no error message
//     that points at the cause.

func xlsxTestDocument() *reportDocument {
	rate := 1.0 / 3.0
	return &reportDocument{
		Schema: reportDocumentSchema, ReportID: "rep-1", Title: "差旅合规检查 <2026>",
		Question:   "差旅报销是否符合管理办法？",
		RevisionID: "rev-1", Revision: 2, TemplateKey: "report-compliance", TemplateVersion: 1,
		Completeness: model.ReportCompletenessFull,
		Sections: []reportDocumentSection{
			{Ord: 1, SectionKey: "scope", SectionVersion: 2, Title: "范围与依据",
				ContentMD:      "## 适用范围\n\n| 项目 | 标准 |\n|---|---|\n| 住宿 | 600 |\n",
				EvidenceStatus: model.ReportEvidenceSupported,
				QualityStatus:  model.ReportQualityPassed, CitationCount: 2,
				LocationHints: []string{"第 2 页 表 1"}},
			{Ord: 2, SectionKey: "findings", SectionVersion: 1, Title: "主要发现",
				ContentMD:      "- 住宿超标 3 笔\n- 审批缺失 1 笔",
				EvidenceStatus: model.ReportEvidencePartiallySupported,
				QualityStatus:  model.ReportQualityNeedsReview, CitationCount: 1},
		},
		EvidenceAppendix: []reportEvidenceEntry{{
			CitationID: "cit-1", SectionOrd: 1, SectionTitle: "范围与依据", Ord: 1,
			DocumentName: "差旅报销制度.md", ChunkID: "chunk-1",
			Snippet: "住宿标准为每晚 600 元。", Content: "第 6 条 住宿标准为每晚 600 元。\x0b\x1f",
			RetrievalScore: 0.87, RetrievalScoreType: "vector", Truncated: true,
		}},
		Claims: []reportClaimEntry{
			{SectionOrd: 1, SectionTitle: "范围与依据", Ord: 1, Text: "住宿标准为每晚 600 元。",
				Type: model.ReportClaimTypeFact, Support: model.ReportClaimSupportSupported,
				Markers: []int{1}},
			{SectionOrd: 2, SectionTitle: "主要发现", Ord: 1, Text: "所有发票均已归档。",
				Type: model.ReportClaimTypeFact, Support: model.ReportClaimSupportUnsupported},
		},
		ClaimScored: 2, ClaimSupportRate: &rate,
		EvidenceVerifiable: true,
	}
}

// xlsxParts opens the workbook and returns its parts by name, failing on a part
// that is not well-formed XML.
func xlsxParts(t *testing.T, content []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("the artifact is not a zip package: %v", err)
	}
	parts := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open part %s: %v", file.Name, err)
		}
		body, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatalf("read part %s: %v", file.Name, err)
		}
		parts[file.Name] = string(body)
		if !strings.HasSuffix(file.Name, ".xml") && !strings.HasSuffix(file.Name, ".rels") {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := decoder.Token(); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("part %s is not well-formed XML: %v", file.Name, err)
			}
		}
	}
	return parts
}

func TestRenderReportXlsxIsAValidWorkbook(t *testing.T) {
	content, err := renderReportXlsx(xlsxTestDocument(), time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render xlsx: %v", err)
	}
	if !bytes.HasPrefix(content, []byte("PK\x03\x04")) {
		t.Fatal("an xlsx artifact must be a zip package")
	}
	parts := xlsxParts(t, content)
	for _, required := range []string{
		"[Content_Types].xml", "_rels/.rels", "docProps/core.xml",
		"xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml",
		"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml",
		"xl/worksheets/sheet3.xml", "xl/worksheets/sheet4.xml",
	} {
		if _, ok := parts[required]; !ok {
			t.Fatalf("the package is missing %s; Excel refuses the whole file", required)
		}
	}
	workbook := parts["xl/workbook.xml"]
	for _, name := range []string{"概览", "章节", "结论", "证据"} {
		if !strings.Contains(workbook, `name="`+name+`"`) {
			t.Fatalf("the workbook must expose the %s sheet: %s", name, workbook)
		}
	}
	if strings.Contains(workbook, "缺口") {
		t.Fatal("a complete report has no gap sheet to show")
	}
	// Every sheet declared in the workbook must have a relationship and a part
	// (`sheetId` and `rId` are the two numbering spaces that must line up).
	for index := 1; index <= 4; index++ {
		if !strings.Contains(parts["xl/_rels/workbook.xml.rels"],
			`worksheets/sheet`+strconv.Itoa(index)+`.xml`) {
			t.Fatalf("sheet %d has no relationship", index)
		}
	}
	// The styles part must carry the `gray125` fill: Excel treats its absence as a
	// corrupt workbook, which is the classic hand-rolled-xlsx failure.
	if !strings.Contains(parts["xl/styles.xml"], `patternType="gray125"`) {
		t.Fatal("styles.xml must declare the gray125 fill")
	}
	if !strings.Contains(parts["[Content_Types].xml"],
		`application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml`) {
		t.Fatal("the workbook part needs its content type or the file is not a workbook")
	}
}

func TestRenderReportXlsxKeepsNumbersNumeric(t *testing.T) {
	content, err := renderReportXlsx(xlsxTestDocument(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parts := xlsxParts(t, content)
	sections := parts["xl/worksheets/sheet2.xml"]

	// Row 1 is the header, row 2 is the first section: A2 = 序号 (1), F2 = 引用数 (2).
	if !strings.Contains(sections, `<c r="A2"><v>1</v></c>`) {
		t.Fatalf("ord must be a numeric cell (sorts as a number): %s", sections)
	}
	if !strings.Contains(sections, `<c r="F2"><v>2</v></c>`) {
		t.Fatalf("引用数 must be a numeric cell: %s", sections)
	}
	if strings.Contains(sections, `<c r="A2" t="inlineStr">`) {
		t.Fatal("writing 序号 as text makes the column look sortable and not be")
	}
	// The two status dimensions stay separate columns (§13 forbids folding them) and
	// they print reader-facing labels, never the closed-set words.
	if !strings.Contains(sections, ">有据<") || !strings.Contains(sections, ">需复核<") {
		t.Fatalf("evidence/quality labels must be the reader's words: %s", sections)
	}
	for _, enum := range []string{"partially_supported", "needs_review", "not_required"} {
		if strings.Contains(contentString(t, parts), enum) {
			t.Fatalf("the closed-set word %q reached the workbook", enum)
		}
	}
	if !strings.Contains(sections, "住宿 | 600") {
		t.Fatal("a table row must keep its cells visible as `a | b`, not collapse into prose")
	}
	if !strings.Contains(sections, "• 住宿超标 3 笔") {
		t.Fatal("bullets keep their marker so the reviewer can see the structure")
	}
}

// contentString joins every part: some assertions are about the whole workbook, and
// a value can legitimately live on any sheet.
func contentString(t *testing.T, parts map[string]string) string {
	t.Helper()
	var builder strings.Builder
	for _, name := range []string{"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml",
		"xl/worksheets/sheet3.xml", "xl/worksheets/sheet4.xml", "xl/worksheets/sheet5.xml"} {
		builder.WriteString(parts[name])
	}
	return builder.String()
}

func TestRenderReportXlsxNeutralisesUntrustedText(t *testing.T) {
	content, err := renderReportXlsx(xlsxTestDocument(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parts := xlsxParts(t, content)
	evidence := parts["xl/worksheets/sheet4.xml"]
	if strings.ContainsRune(evidence, '\x0b') || strings.ContainsRune(evidence, '\x1f') {
		t.Fatal("an XML-illegal control byte must be stripped, not written into the part")
	}
	if !strings.Contains(evidence, "第 6 条 住宿标准为每晚 600 元。") {
		t.Fatal("stripping controls must not eat the words around them")
	}
	if !strings.Contains(evidence, "&lt;2026&gt;") && !strings.Contains(parts["docProps/core.xml"], "&lt;2026&gt;") {
		t.Fatal("the title is escaped exactly once")
	}
	if !strings.Contains(evidence, ">是<") {
		t.Fatal("the 已截断 column says 是/否, not true/false")
	}

	// The cell ceiling: a longer payload is clipped with a marker that says so, and
	// the clip is by runes (a half-cut multi-byte rune would be invalid UTF-8).
	long := xlsxTestDocument()
	long.EvidenceAppendix[0].Content = strings.Repeat("证据", xlsxCellLimit)
	content, err = renderReportXlsx(long, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parts = xlsxParts(t, content)
	evidence = parts["xl/worksheets/sheet4.xml"]
	if !strings.Contains(evidence, xlsxOverflow) {
		t.Fatal("a clipped cell must say it was clipped; a silent cut is a lie about the source")
	}
	if len(evidence) > 4*xlsxCellLimit {
		t.Fatalf("the cell ceiling was not applied: %d bytes", len(evidence))
	}
}

func TestRenderReportXlsxDisclosesGaps(t *testing.T) {
	document := xlsxTestDocument()
	document.Completeness = model.ReportCompletenessPartial
	document.Gaps = []reportGapEntry{{
		SectionKey: "controls", Ord: 3, Title: "控制措施", Reason: "证据不足",
		Impact: model.ReportGapImpactSectionMissing, ErrorCode: "44105",
	}}
	content, err := renderReportXlsx(document, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parts := xlsxParts(t, content)
	if _, ok := parts["xl/worksheets/sheet5.xml"]; !ok {
		t.Fatal("a partial report needs the 缺口 sheet; hiding it is the spreadsheet lie")
	}
	if !strings.Contains(parts["xl/workbook.xml"], `name="缺口"`) {
		t.Fatal("the 缺口 sheet must be reachable from the workbook")
	}
	overview := parts["xl/worksheets/sheet1.xml"]
	if !strings.Contains(overview, "部分完成") {
		t.Fatal("the 概览 sheet states the completeness in words")
	}
	if !strings.Contains(overview, "第 3 节「控制措施」：证据不足") {
		t.Fatal("the 概览 sheet names the chapters that never ran")
	}
}

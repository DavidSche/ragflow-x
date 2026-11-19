package service

import (
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The HTML delivery format (queue item 4).
//
// These tests pin the three promises the format makes, in the order they matter:
//
//  1. **It is self-contained and inert** — no stylesheet/font/script/image is
//     fetched, and a `javascript:` link in the body is neutralised. A delivered
//     report is opened on a machine that may well be offline and is definitely not
//     trusted to run what the writer's markdown happened to contain.
//  2. **The disclosure survives the format change** — banner, evidence appendix,
//     claim table and consistency block are read from the frozen payload, exactly
//     like the Markdown/DOCX/PDF renderers.
//  3. **It agrees with them about numbering** — `ord` is 1-based on disk, so the
//     first chapter is "1." and the first citation is "[1]" (A4: two files claiming
//     to be the same document must not disagree).

func htmlTestDocument() *reportDocument {
	rate := 1.0 / 3.0
	return &reportDocument{
		Schema: reportDocumentSchema, ReportID: "rep-1", Title: "差旅合规检查 <2026>",
		Question:   "差旅报销是否符合管理办法？",
		RevisionID: "rev-1", Revision: 2, TemplateKey: "report-compliance", TemplateVersion: 1,
		Completeness: model.ReportCompletenessPartial,
		Gaps: []reportGapEntry{{
			SectionKey: "controls", Ord: 3, Title: "控制措施",
			Reason: "证据不足", Impact: model.ReportGapImpactSectionMissing,
			ErrorCode: "44105",
		}},
		Sections: []reportDocumentSection{
			{Ord: 1, SectionKey: "scope", SectionVersion: 2, Title: "范围与依据",
				ContentMD:      "## 适用范围\n\n本报告覆盖 **2026 年度** 差旅报销。\n\n- 住宿标准 600 元\n\n| 项目 | 标准 |\n|---|---|\n| 住宿 | 600 |\n\n`inline` 与 [制度原文](https://example.com/policy)。",
				EvidenceStatus: model.ReportEvidenceSupported, CitationCount: 1,
				LocationHints: []string{"第 2 页 表 1"}},
			{Ord: 2, SectionKey: "findings", SectionVersion: 1, Title: "主要发现",
				ContentMD:      "发现 [钓鱼](javascript:alert(1)) 与 [内网](http://10.0.0.5/x)。",
				EvidenceStatus: model.ReportEvidencePartiallySupported, CitationCount: 1},
		},
		EvidenceAppendix: []reportEvidenceEntry{{
			CitationID: "cit-1", SectionOrd: 1, SectionTitle: "范围与依据", Ord: 1,
			DocumentName: "差旅报销制度.md", ChunkID: "chunk-1",
			Content: "第 6 条 住宿标准为每晚 600 元。", RetrievalScore: 0.87,
		}},
		Claims: []reportClaimEntry{
			{SectionOrd: 1, SectionTitle: "范围与依据", Ord: 1, Text: "住宿标准为每晚 600 元。",
				Type: model.ReportClaimTypeFact, Support: model.ReportClaimSupportSupported,
				Markers: []int{1}},
			{SectionOrd: 1, SectionTitle: "范围与依据", Ord: 2, Text: "执行记录缺失。",
				Type: model.ReportClaimTypeLimitation, Support: model.ReportClaimSupportNotRequired},
			{SectionOrd: 2, SectionTitle: "主要发现", Ord: 1, Text: "建议每季度复核一次。",
				Type:    model.ReportClaimTypeRecommendation,
				Support: model.ReportClaimSupportNotRequired},
			{SectionOrd: 2, SectionTitle: "主要发现", Ord: 2, Text: "住宿标准可能已上调。",
				Type:    model.ReportClaimTypeInference,
				Support: model.ReportClaimSupportPartiallySupported, Markers: []int{1}},
			{SectionOrd: 2, SectionTitle: "主要发现", Ord: 3, Text: "所有发票均已归档。",
				Type: model.ReportClaimTypeFact, Support: model.ReportClaimSupportUnsupported},
		},
		ClaimScored: 3, ClaimNotRequired: 1, ClaimSupportRate: &rate,
		ConsistencyIssues: []reportConsistencyIssue{
			{Label: "住宿标准", Ords: []int{1, 2}, Values: []string{"600 元", "900 元"}},
		},
		EvidenceVerifiable: true,
	}
}

func TestRenderReportHTMLIsSelfContainedAndInert(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	page := renderReportHTML(htmlTestDocument(), now)

	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Fatalf("an HTML delivery must declare its doctype: %q", page[:40])
	}
	if !strings.Contains(page, `<meta charset="utf-8">`) {
		t.Fatal("the file must declare its own encoding or a Chinese report opens as mojibake")
	}
	// Self-contained: the only permitted external reference is a link the report
	// body itself cites, never a resource the page loads.
	for _, forbidden := range []string{"<script", "<link ", "<iframe", "<object", "<embed"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("a delivered report must not load anything: found %q", forbidden)
		}
	}
	if strings.Contains(page, "<img") {
		t.Fatal("an <img> in a delivered file fetches from wherever the report came from")
	}
	if strings.Contains(page, `href="javascript:`) {
		t.Fatal("a javascript: target must be rendered as text, never as a link")
	}
	if !strings.Contains(page, "钓鱼（javascript:alert(1）") {
		t.Fatalf("an unsupported scheme keeps its words: %q", page)
	}
	if !strings.Contains(page, `<a href="https://example.com/policy" rel="noopener noreferrer">制度原文</a>`) {
		t.Fatal("an https citation must still be a link")
	}
	if !strings.Contains(page, `<a href="http://10.0.0.5/x" rel="noopener noreferrer">内网</a>`) {
		t.Fatal("http is a legitimate citation scheme")
	}
	if !strings.Contains(page, "差旅合规检查 &lt;2026&gt;") {
		t.Fatal("the title is escaped once, in the text, not interpreted as markup")
	}
	if !strings.Contains(page, "<strong>2026 年度</strong>") {
		t.Fatal("the writer's bold must be styled, not printed with asterisks")
	}
	if !strings.Contains(page, "<code>inline</code>") {
		t.Fatal("inline code must survive as <code>")
	}
	if !strings.Contains(page, "<table>") || !strings.Contains(page, "<th>项目</th>") {
		t.Fatal("a markdown table must become a real table")
	}
}

func TestRenderReportHTMLCarriesTheDisclosure(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	page := renderReportHTML(htmlTestDocument(), now)

	for _, want := range []string{
		`class="disclosure"`,
		"本报告为部分完成。",
		"第 3 节「控制措施」未生成：证据不足（影响：section_missing）",
		"44105",
		"证据附录",
		"第 6 条 住宿标准为每晚 600 元。",
		"结论清单",
		reportClaimAppendixPreamble,
		"可核实的结论句 1 / 3（33%），另有 1 句无需核实。",
		"跨章节一致性提示",
		"「住宿标准」：第 1 节、第 2 节 分别为 600 元、900 元",
		"定位线索（非证据）：第 2 页 表 1",
		"r2", "report-compliance v1",
		reportExportRendererVersion,
		reportExportLayoutID("report-compliance", model.ExportFormatHTML),
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the delivered file lost %q", want)
		}
	}
	// Numbering: first chapter is "1.", first citation is "[1]" — never "2." (A4).
	if !strings.Contains(page, "<h2>1. 范围与依据</h2>") || !strings.Contains(page, "<h2>2. 主要发现</h2>") {
		t.Fatal("sections must be numbered from ord, which is 1-based on disk")
	}
	if !strings.Contains(page, "<h4>[1] 差旅报销制度.md</h4>") {
		t.Fatalf("citations must be numbered from ord: %q", page)
	}
	// The claim table prints all three claims, including the two that never claimed
	// support: dropping them would make the appendix shorter than the report.
	claims := page[strings.Index(page, `class="claims"`):strings.Index(page, `class="consistency"`)]
	if got := strings.Count(claims, "<tr><td>"); got != 5 {
		t.Fatalf("want 5 claim rows (every sentence, not a sample), got %d", got)
	}
	if !strings.Contains(page, "未引用证据") {
		t.Fatal("a scorable claim without markers says so instead of leaving a blank")
	}
}

func TestRenderReportHTMLOmitsTheBannerForAFullReport(t *testing.T) {
	document := htmlTestDocument()
	document.Completeness = model.ReportCompletenessFull
	document.Gaps = nil
	document.Claims = nil
	document.ConsistencyIssues = nil
	document.EvidenceAppendix = nil
	page := renderReportHTML(document, time.Now().UTC())

	if strings.Contains(page, `class="disclosure"`) || strings.Contains(page, "本报告为部分完成") {
		t.Fatal("a full report must not carry the partial disclosure")
	}
	for _, absent := range []string{"结论清单", "证据附录", "跨章节一致性提示"} {
		if strings.Contains(page, absent) {
			t.Fatalf("an empty %q section must not be printed as an empty heading", absent)
		}
	}
}

func TestRenderReportHTMLLeavesTheEnumOutOfTheFile(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	page := renderReportHTML(htmlTestDocument(), now)
	// `partially_supported` and `not_required` are machine words; the reader gets the
	// same labels the panel prints.
	for _, enum := range []string{"partially_supported", "not_required", "needs_review"} {
		if strings.Contains(page, enum) {
			t.Fatalf("the closed-set word %q reached the document", enum)
		}
	}
	if !strings.Contains(page, "部分可核实") || !strings.Contains(page, "无需核实") {
		t.Fatal("the reader-facing claim labels must be printed")
	}
}

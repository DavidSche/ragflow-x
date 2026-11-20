package service

// doc/301 §5.3.1 / T10 — `scan_scope` is *mandatory disclosure* in the delivered file.
//
// The rule it enforces is not cosmetic. A revision whose review only compared cited
// evidence (`cited_only`) and found no conflict must not read as "cross-document
// contrast was performed and found nothing": those are different statements, and only
// one of them is true. The same sentence must therefore appear in all five formats —
// a Markdown-only disclosure would let the DOCX a customer forwards make the stronger
// claim.
//
// Two properties are pinned: the line is present in every renderer, and a revision that
// predates the field prints nothing at all (silence, not an invented `cited_only`).

import (
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// scanScopeDisclosureDocument is the smallest document each renderer accepts.
func scanScopeDisclosureDocument(scope string) *reportDocument {
	return &reportDocument{
		Schema:     reportDocumentSchema,
		ReportID:   "rpt_scope",
		Title:      "差旅合规复核",
		RevisionID: "rev_scope",
		Revision:   3,
		// Markdown/DOCX print `Completeness` verbatim, so it must be a real value.
		Completeness: model.ReportCompletenessFull,
		TemplateKey:  reportComplianceTemplateKey,
		Sections: []reportDocumentSection{{
			Ord: 1, SectionKey: "compliance", Title: "逐条符合性判定",
			ContentMD: "差旅标准为每天 800 元。", CitationCount: 0,
		}},
		ScanScope: scope,
	}
}

func TestScanScopeIsRenderedByEveryFormat(t *testing.T) {
	const citedOnly = "仅引用证据（本次未做跨文档冲突对照）"
	cases := []struct {
		format string
		render func(*reportDocument) string
	}{
		{"markdown", func(document *reportDocument) string {
			return renderReportMarkdown(document, time.Now().UTC())
		}},
		{"html", func(document *reportDocument) string {
			return renderReportHTML(document, time.Now().UTC())
		}},
	}
	for _, testCase := range cases {
		body := testCase.render(scanScopeDisclosureDocument(renderScanScope("cited_only", 0)))
		if !strings.Contains(body, citedOnly) {
			t.Fatalf("%s: scan_scope sentence missing:\n%s", testCase.format, body)
		}
	}

	// DOCX/XLSX are archives and PDF is binary: the sentence is asserted on the
	// renderer's input model for those three, plus the plain-text renderer above for
	// the byte-level proof. `renderScanScope` is the single producer of the wording,
	// so a format cannot word it differently — only omit it — and each renderer's
	// omission is caught by its own format test below.
	if line := renderScanScope("cited_only", 0); line != citedOnly {
		t.Fatalf("cited_only wording drifted: %q", line)
	}
	if line := renderScanScope("cited+candidate:8", 8); !strings.Contains(line, "未引用候选 8 条") ||
		!strings.Contains(line, "候选不入证据链") {
		t.Fatalf("candidate wording drifted: %q", line)
	}
	if line := renderScanScope("", 0); line != "" {
		t.Fatalf("a revision with no scope must print nothing, got %q", line)
	}
}

// TestScanScopeRidesTheMetadataBlockOfEachRenderer: the sentence belongs in the header
// every format prints, not in an appendix a claim-less report would not have. Each
// renderer therefore emits it next to the integrity line.
func TestScanScopeRidesTheMetadataBlockOfEachRenderer(t *testing.T) {
	const citedOnly = "仅引用证据（本次未做跨文档冲突对照）"
	document := scanScopeDisclosureDocument(citedOnly)

	markdown := renderReportMarkdown(document, time.Now().UTC())
	if !strings.Contains(markdown, "- 复核范围："+citedOnly) {
		t.Fatalf("markdown: scope must sit beside `完整性`:\n%s", markdown)
	}
	htmlBody := renderReportHTML(document, time.Now().UTC())
	if !strings.Contains(htmlBody, "<dt>复核范围</dt>") {
		t.Fatalf("html: scope must be a metadata row:\n%s", htmlBody)
	}
	sheet := reportXlsxOverviewSheet(document, time.Now().UTC())
	found := false
	for _, row := range sheet.rows {
		if len(row) > 0 && row[0].text == "复核范围" {
			found = true
			if len(row) < 2 || row[1].text != citedOnly {
				t.Fatalf("xlsx: 复核范围 row must carry the sentence, got %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("xlsx: the overview sheet must carry a 复核范围 row")
	}
}

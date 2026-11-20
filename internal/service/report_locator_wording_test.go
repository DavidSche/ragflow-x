package service

// doc/301 §5.8 / T10 ③ — 「定位线索（非证据）」 and the research-degradation note are
// *disclosures with one owner each*, and this test pins the last property that the
// single-producer design can still lose: that every delivered format actually prints
// the line.
//
// The failure this guards against is not a typo. A locator that reaches a reader
// without its label reads as evidence — the section then carries a page number that
// looks citable and is not (A54). A degraded research plan that stays in the JSON blob
// reads as "we searched the corpus thoroughly" when the planner call was refused for
// budget. Both are the same bug shape: a fact the server knows, a file the customer
// forwards, and a sentence in between that nobody wrote.
//
// Two techniques are used on purpose:
//
//   - text-bearing formats (Markdown / HTML / DOCX / XLSX) are asserted on the rendered
//     bytes, and the label is counted rather than merely found, so a renderer that
//     prints its own copy of the label is caught;
//   - the PDF's text streams are compressed, so the PDF leg is a *difference* test:
//     the same document renders to different bytes with the disclosure and without it.
//     A renderer that dropped the line would produce identical bytes and fail.

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// locatorWordingDocument holds one hinted section and one that has nothing to say, so
// every format has to disclose once and stay silent once.
func locatorWordingDocument(plan *reportDocumentResearch) *reportDocument {
	return &reportDocument{
		Schema:       reportDocumentSchema,
		ReportID:     "rpt_locator_wording",
		Title:        "差旅合规复核",
		RevisionID:   "rev_locator_wording",
		Revision:     2,
		Completeness: model.ReportCompletenessFull,
		TemplateKey:  reportComplianceTemplateKey,
		Sections: []reportDocumentSection{
			{
				Ord: 1, SectionKey: "compliance", Title: "逐条符合性判定",
				ContentMD:     "住宿标准为每晚上限 600 元人民币。",
				CitationCount: 0,
				LocationHints: []string{"第 2 页 表 1"},
				ResearchPlan:  plan,
			},
			{
				Ord: 2, SectionKey: "risks", Title: "风险提示",
				ContentMD:     "未见其他风险。",
				CitationCount: 0,
			},
		},
	}
}

const locatorLabel = "定位线索（非证据）："

// renderAllFormats returns the raw bytes of each of the five delivered formats.
func renderAllFormats(t *testing.T, document *reportDocument) map[string][]byte {
	t.Helper()
	now := time.Now().UTC()
	out := map[string][]byte{}
	for _, format := range []string{
		model.ExportFormatMarkdown, model.ExportFormatHTML,
		model.ExportFormatDOCX, model.ExportFormatXLSX, model.ExportFormatPDF,
	} {
		if format == model.ExportFormatPDF {
			withPDFFont(t)
		}
		content, err := renderReportExport(document, format, now)
		if err != nil {
			t.Fatalf("%s render failed: %v", format, err)
		}
		if len(content) == 0 {
			t.Fatalf("%s rendered to nothing", format)
		}
		out[format] = content
	}
	return out
}

// formatText flattens a format to the text a reader can be shown: archives are read
// part by part, plain text is returned as-is, and the PDF returns "" because its
// streams are compressed (the PDF legs below assert a byte *difference* instead).
func formatText(t *testing.T, format string, content []byte) string {
	t.Helper()
	switch format {
	case model.ExportFormatMarkdown, model.ExportFormatHTML:
		return string(content)
	case model.ExportFormatDOCX, model.ExportFormatXLSX:
		reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			t.Fatalf("%s must be a valid archive: %v", format, err)
		}
		return readAllZipEntries(t, reader)
	}
	return ""
}

func TestEveryFormatPrintsTheLocatorLabelOnce(t *testing.T) {
	rendered := renderAllFormats(t, locatorWordingDocument(nil))
	for format, content := range rendered {
		if format == model.ExportFormatPDF {
			continue // asserted by TestPDFDisclosesLocatorsAndDegradation
		}
		text := formatText(t, format, content)
		if got := strings.Count(text, locatorLabel); got != 1 {
			t.Fatalf("%s prints %q %d time(s), want exactly 1 (one hinted section):\n%s",
				format, locatorLabel, got, text)
		}
		if !strings.Contains(text, "第 2 页 表 1") {
			t.Fatalf("%s dropped the hint text itself", format)
		}
	}
}

// TestNoFormatPrintsALocatorWithoutItsLabel is the other half: the hint text may only
// ever appear on the labelled line. A renderer that printed `第 2 页 表 1` next to the
// citation count, without the 「非证据」 word, is exactly the confusion A54 forbids.
func TestNoFormatPrintsALocatorWithoutItsLabel(t *testing.T) {
	if line := reportLocationHintsLine([]string{"第 2 页 表 1"}); !strings.HasPrefix(line, locatorLabel) {
		t.Fatalf("the single producer must own the label, got %q", line)
	}
	// An empty hint list must produce no line at all: a section that has no locator
	// must not print an empty label, which would look like a rendering defect.
	if line := reportLocationHintsLine(nil); line != "" {
		t.Fatalf("no hints must print nothing, got %q", line)
	}
	if line := reportLocationHintsLine([]string{"  ", ""}); line != "" {
		t.Fatalf("blank hints must print nothing, got %q", line)
	}
}

func TestEveryFormatPrintsTheResearchDegradation(t *testing.T) {
	plan := &reportDocumentResearch{Depth: "deep", Planning: reportResearchPlanningBudget}
	rendered := renderAllFormats(t, locatorWordingDocument(plan))
	// The exporter's wording, which is the renderer's own (`reportResearchPlanLine`),
	// not the workbench dictionary's "深研未启用：预算不足": the two surfaces are allowed
	// different sentences, and pinning the wrong one here would pin a string that no
	// renderer can ever produce.
	const want = "深研状态：深研降级（预算不足）"
	for format, content := range rendered {
		if format == model.ExportFormatPDF {
			continue
		}
		text := formatText(t, format, content)
		if !strings.Contains(text, want) {
			t.Fatalf("%s does not disclose the degraded plan (%q):\n%s", format, want, text)
		}
	}
	// A standard-depth section stays silent — printing 「深研状态：标准检索」 on every
	// chapter of a run that never used deep research is noise that trains readers to
	// skip the line.
	if line := reportResearchPlanLine(&reportDocumentResearch{
		Depth: "standard", Planning: reportResearchPlanningStandard,
	}); line == "" {
		t.Fatal("a standard section still states its depth; only a section with no plan is silent")
	}
	if line := reportResearchPlanLine(nil); line != "" {
		t.Fatalf("a section with no plan must print nothing, got %q", line)
	}
}

// TestPDFDisclosesLocatorsAndDegradation uses a difference test because fpdf
// compresses its text streams: if the PDF renderer stopped printing either line, the
// two renders would be byte-identical.
func TestPDFDisclosesLocatorsAndDegradation(t *testing.T) {
	withPDFFont(t)
	now := time.Now().UTC()

	plain := locatorWordingDocument(nil)
	// The baseline has no locator and no plan: same title, same bodies, same metadata.
	baseline := locatorWordingDocument(nil)
	baseline.Sections[0].LocationHints = nil

	degraded := locatorWordingDocument(&reportDocumentResearch{
		Depth: "deep", Planning: reportResearchPlanningBudget,
	})

	render := func(document *reportDocument) []byte {
		content, err := renderReportExport(document, model.ExportFormatPDF, now)
		if err != nil {
			t.Fatalf("pdf render: %v", err)
		}
		return content
	}
	baselineBytes := render(baseline)
	hintedBytes := render(plain)
	if bytes.Equal(baselineBytes, hintedBytes) {
		t.Fatal("the PDF is byte-identical with and without a locator: the line is not rendered")
	}
	plannedBytes := render(degraded)
	if bytes.Equal(hintedBytes, plannedBytes) {
		t.Fatal("the PDF is byte-identical with and without a degraded plan: the line is not rendered")
	}
}

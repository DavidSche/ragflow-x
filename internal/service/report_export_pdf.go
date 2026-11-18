package service

// Report PDF renderer (doc/288 §12 / T14, A4/A13).
//
// PDF is the second delivery format of the same frozen document, so it must not be a
// second interpretation of the report. Two rules make that structural rather than
// aspirational:
//
//  1. **The same block model.** Every section's Markdown goes through
//     `parseAnswerContent` and the shared `pdfBlock`/`pdfTable` writers that the
//     answer export uses, so a table or a code block means the same thing in a
//     report PDF, a report DOCX and an answer export (A4: the two formats are
//     compared block-by-block by the tests).
//  2. **The appendix comes from the payload, not from the database.** The evidence
//     appendix is rendered from `document.EvidenceAppendix` — the copy frozen at
//     export time. That is what makes A43 true: purging the snapshots afterwards
//     cannot change a delivered file, and a re-export is refused upstream (44116)
//     instead of printing an empty appendix.
//
// The renderer is honest about one limit: fpdf needs a CJK font to lay out Chinese
// text. Without `RGX_EXPORT_PDF_FONT` (or a probed system font) the export fails
// rather than emitting boxes, and the failure lands as a `failed` artifact with
// `export_render_failed` — the same honest path a broken DOCX takes (A40).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// pdfSectionAnchorKey names a TOC destination: one per delivered section plus the
// evidence appendix, because those are the parts a reader jumps between.
func pdfSectionAnchorKey(ord int) string { return fmt.Sprintf("sec:%d", ord) }

const pdfAppendixAnchorKey = "appendix"

// renderReportPdf renders the frozen document to PDF.
//
// It renders twice on purpose. A table of contents needs the page number of every
// entry, and fpdf only knows a page number once that page has been laid out, so the
// first pass lays the whole document out to learn the numbers and the second prints
// them. The two passes must produce identical pagination or the numbers lie, which
// holds here because a TOC row is a fixed-height two-cell line: replacing a number
// with an empty string never reflows anything, and the cover/metadata block does not
// depend on the TOC at all.
//
// The cover and the TOC are why this file does the layout itself instead of using a
// header-only shell: a compliance report that is forwarded needs to say what it is
// (title, version, generated time, completeness) before it says anything else, and a
// 60-page report needs to be navigable without scrolling (doc/288 §12 封面/目录).
func renderReportPdf(document *reportDocument, now time.Time) ([]byte, error) {
	fontPath := pdfFontPath()
	if fontPath == "" {
		return nil, fmt.Errorf("pdf renderer requires RGX_EXPORT_PDF_FONT or a system CJK font")
	}
	// Read the bytes ourselves: fpdf joins the name with its own font dir, which
	// breaks absolute Unix paths (see the long note in answer_export.go).
	fontBytes, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, fmt.Errorf("load pdf font %s: %w", fontPath, err)
	}
	if fontBytes, err = sfntFontBytes(fontBytes); err != nil {
		return nil, fmt.Errorf("load pdf font %s: %w", fontPath, err)
	}
	// Pass 1: lay it out with no page numbers to learn where each entry lands.
	pageOf, _, err := renderReportPdfPass(document, now, fontBytes, nil)
	if err != nil {
		return nil, err
	}
	// Pass 2: same layout, real numbers, real internal links.
	_, output, err := renderReportPdfPass(document, now, fontBytes, pageOf)
	if err != nil {
		return nil, err
	}
	return output, nil
}

// pdfContentsRow is one printed contents line. The page is `0` during the measuring
// pass (the number is blank) and the measured page during the printing pass, so the
// two passes are byte-for-byte the same layout: a contents row is a fixed-height line
// and an empty number cell does not reflow.
type pdfContentsRow struct {
	Key   string
	Label string
	Page  int
}

// reportPdfContentsRows builds the contents from the document and the measured pages.
// It is a separate function so the printed number and the page a heading landed on can
// be asserted to be the same value — a renderer that printed a constant would satisfy
// any test that only counted pages.
func reportPdfContentsRows(document *reportDocument, pageOf map[string]int) []pdfContentsRow {
	rows := make([]pdfContentsRow, 0, len(document.Sections)+1)
	for _, section := range document.Sections {
		key := pdfSectionAnchorKey(section.Ord)
		rows = append(rows, pdfContentsRow{
			Key:   key,
			Label: fmt.Sprintf("%d. %s", section.Ord+1, section.Title),
			Page:  pageOf[key],
		})
	}
	if len(document.EvidenceAppendix) > 0 {
		rows = append(rows, pdfContentsRow{
			Key: pdfAppendixAnchorKey, Label: "证据附录", Page: pageOf[pdfAppendixAnchorKey],
		})
	}
	return rows
}

// renderReportPdfPass renders one pass and reports the page number each TOC entry
// landed on. `pageOf` is nil for the measuring pass.
func renderReportPdfPass(
	document *reportDocument, now time.Time, fontBytes []byte, pageOf map[string]int,
) (map[string]int, []byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddUTF8FontFromBytes("cjk", "", fontBytes)
	pdf.SetFont("cjk", "", 10)
	if err := pdf.Error(); err != nil {
		return nil, nil, fmt.Errorf("load pdf font: %w", err)
	}

	// §12: 页眉 = 报告名 + 版本; 页脚 = 页码. The cover carries the identity block
	// itself, so neither runs on page 1 (a header above a title page reads as a
	// mistake, and a page number on the cover is noise).
	version := fmt.Sprintf("r%d · %s v%d", document.Revision, document.TemplateKey, document.TemplateVersion)
	pdf.SetHeaderFunc(func() {
		if pdf.PageNo() <= 1 {
			return
		}
		pdf.SetFont("cjk", "", 8)
		pdf.SetY(8)
		pdf.CellFormat(0, 5, document.Title+" | "+version, "0", 1, "L", false, 0, "")
		pdf.Ln(3)
	})
	pdf.SetFooterFunc(func() {
		if pdf.PageNo() <= 1 {
			return
		}
		pdf.SetY(-15)
		pdf.SetFont("cjk", "", 8)
		pdf.CellFormat(0, 8, fmt.Sprintf("%d", pdf.PageNo()), "0", 0, "C", false, 0, "")
	})

	// ---- cover -------------------------------------------------------------
	pdf.AddPage()
	pdf.SetY(70)
	pdf.SetFont("cjk", "", 24)
	pdf.MultiCell(0, 12, document.Title, "", "L", false)
	pdf.Ln(6)
	pdf.SetFont("cjk", "", 11)
	pdf.MultiCell(0, 7, fmt.Sprintf("报告版本：%s", version), "", "L", false)
	pdf.MultiCell(0, 7, fmt.Sprintf("生成时间：%s", now.UTC().Format("2006-01-02T15:04:05Z")), "", "L", false)
	pdf.MultiCell(0, 7, fmt.Sprintf("完整性：%s", document.Completeness), "", "L", false)
	if document.Question != "" {
		pdf.Ln(4)
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 6, "原始问题："+document.Question, "", "L", false)
	}

	// The disclosure banner is rendered from the frozen payload, exactly like the
	// Markdown and DOCX banners, so the three files cannot disagree (A41). It stays
	// on the cover: a partial report must say so before its table of contents.
	if document.Completeness == model.ReportCompletenessPartial || len(document.Gaps) > 0 {
		pdf.Ln(8)
		pdf.SetFont("cjk", "", 12)
		pdf.MultiCell(0, 7, "完整性声明", "", "L", false)
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, "本报告为部分完成，以下节未生成：", "", "L", false)
		for _, gap := range document.Gaps {
			line := fmt.Sprintf("· 第 %d 节「%s」未生成：%s（影响：%s）",
				gap.Ord+1, gap.Title, gap.Reason, gap.Impact)
			if gap.ErrorCode != "" {
				line += fmt.Sprintf("；错误码 %s", gap.ErrorCode)
			}
			pdf.MultiCell(0, 5, line, "", "L", false)
		}
	}

	// ---- table of contents -------------------------------------------------
	pdf.AddPage()
	pdf.SetFont("cjk", "", 16)
	pdf.MultiCell(0, 9, "目录", "", "L", false)
	pdf.Ln(2)
	links := map[string]int{}
	pageWidth, _ := pdf.GetPageSize()
	usable := pageWidth - pdf.GetX()*2
	for _, row := range reportPdfContentsRows(document, pageOf) {
		// The link is registered now and pointed at the heading later: fpdf writes
		// annotations when the page is closed, so a later SetLink still lands.
		link := pdf.AddLink()
		links[row.Key] = link
		number := ""
		if row.Page > 0 {
			number = fmt.Sprintf("%d", row.Page)
		}
		pdf.SetFont("cjk", "", 10)
		pdf.CellFormat(usable-14, 6, row.Label, "", 0, "L", false, link, "")
		pdf.CellFormat(14, 6, number, "", 1, "R", false, link, "")
	}

	// ---- body --------------------------------------------------------------
	pdf.AddPage()
	numbers := &pdfCaptionNumbers{}
	// `landed` is what pass 1 exists for: the page each heading was written on.
	landed := map[string]int{}
	for index, section := range document.Sections {
		if index > 0 {
			pdf.Ln(2)
		}
		key := pdfSectionAnchorKey(section.Ord)
		landed[key] = pdf.PageNo()
		if link, ok := links[key]; ok && pageOf != nil {
			pdf.SetLink(link, pdf.GetY()-2, pdf.PageNo())
		}
		pdf.SetFont("cjk", "", 14)
		pdf.MultiCell(0, 7, fmt.Sprintf("%d. %s", section.Ord+1, section.Title), "", "L", false)
		pdf.Ln(1)
		for _, block := range parseAnswerContent(section.ContentMD) {
			pdfReportBlock(pdf, block, numbers)
		}
		if section.EvidenceStatus != "" && section.CitationCount > 0 {
			pdf.SetFont("cjk", "", 9)
			pdf.MultiCell(0, 5, fmt.Sprintf("依据：%d 条引用，证据状态 %s。",
				section.CitationCount, section.EvidenceStatus), "", "L", false)
		}
	}

	if len(document.EvidenceAppendix) > 0 {
		renderReportEvidenceAppendixPdf(pdf, document)
		landed[pdfAppendixAnchorKey] = pdf.PageNo()
	}

	var buffer strings.Builder
	if err := pdf.Output(&pdfStringWriter{&buffer}); err != nil {
		return nil, nil, err
	}
	return landed, []byte(buffer.String()), nil
}

// pdfCaptionNumbers numbers figures and tables through the whole document, so a
// reader can refer to "表 3" across sections instead of "the third table of §4".
type pdfCaptionNumbers struct {
	table int
	image int
}

// pdfBlockKindsCaptioned are the block kinds that get a number. Everything else is
// text and needs no label.
func pdfReportBlock(pdf *fpdf.Fpdf, block exportBlock, numbers *pdfCaptionNumbers) {
	switch block.kind {
	case exportBlockTable:
		numbers.table++
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("表 %d", numbers.table), "", "L", false)
	case exportBlockImage:
		numbers.image++
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("图 %d", numbers.image), "", "L", false)
	}
	pdfBlock(pdf, block)
}

// renderReportEvidenceAppendixPdf prints the frozen appendix. It is shared by the
// PDF and (structurally) mirrored by the DOCX/Markdown renderers, because the same
// evidence has to be readable in every delivered format (A13).
func renderReportEvidenceAppendixPdf(pdf *fpdf.Fpdf, document *reportDocument) {
	if len(document.EvidenceAppendix) == 0 {
		return
	}
	pdf.AddPage()
	pdf.SetFont("cjk", "", 14)
	pdf.MultiCell(0, 7, "证据附录", "", "L", false)
	pdf.SetFont("cjk", "", 9)
	if document.EvidenceVerifiable {
		pdf.MultiCell(0, 5, "以下为生成时冻结的证据副本，可与原始文档逐条核对。", "", "L", false)
	} else {
		pdf.MultiCell(0, 5, "以下为生成时冻结的证据副本；原始快照已过期，当前无法复核。", "", "L", false)
	}
	pdf.Ln(2)
	currentSection := -1
	for _, entry := range document.EvidenceAppendix {
		if entry.SectionOrd != currentSection {
			currentSection = entry.SectionOrd
			pdf.SetFont("cjk", "", 11)
			title := entry.SectionTitle
			if title == "" {
				title = fmt.Sprintf("第 %d 节", entry.SectionOrd+1)
			}
			pdf.MultiCell(0, 6, fmt.Sprintf("%d. %s", entry.SectionOrd+1, title), "", "L", false)
			pdf.SetFont("cjk", "", 9)
		}
		locator := entry.DocumentName
		if locator == "" {
			locator = entry.DocumentID
		}
		if locator == "" {
			locator = entry.ChunkID
		}
		pdf.MultiCell(0, 5, fmt.Sprintf("[%d] %s · %s", entry.Ord+1, locator, scoreLabel(entry)), "", "L", false)
		pdf.SetFont("cjk", "", 8)
		pdf.SetFillColor(245, 245, 245)
		pdf.MultiCell(0, 4.5, entry.Content, "1", "L", true)
		pdf.SetFont("cjk", "", 9)
		pdf.Ln(1)
	}
}

// scoreLabel renders the score with its type, because a bare number is unit-less:
// `similarity 0.82` and `rerank 0.82` mean different things (doc/288 §12).
func scoreLabel(entry reportEvidenceEntry) string {
	kind := entry.RetrievalScoreType
	if kind == "" {
		kind = "score"
	}
	return fmt.Sprintf("%s %.2f", kind, entry.RetrievalScore)
}

// pdfStringWriter adapts a strings.Builder to the io.Writer fpdf expects, so the
// renderer never has to know whether the bytes land in a file or a buffer.
type pdfStringWriter struct{ target *strings.Builder }

func (w *pdfStringWriter) Write(p []byte) (int, error) { return w.target.Write(p) }

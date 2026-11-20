package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report XLSX renderer (doc/288 §12 / queue item 4).
//
// The other formats tell the story; this one is for the reader who wants to *check*
// it. A compliance reviewer does not read a 40-page DOCX line by line — they open a
// sheet, sort by 支持度, and ask "which sentences could we not substantiate, and
// where did each one come from". A prose document cannot answer that; a sheet with
// one row per claim, per section and per citation can, and the answer stays honest
// because every cell is read from the same frozen payload the DOCX was rendered from.
//
// Structure, and why each sheet exists:
//
//	概览   the document's identity + the disclosure counts, one row per fact
//	章节   one row per written section: 版本 / 证据状态 / 质量状态 / 引用数 / 定位线索
//	结论    one row per claim (§9.2): 类型 / 支持度 / 句子 / 引用编号
//	证据    one row per frozen citation excerpt (A43)
//	缺口   only when the report is partial: 哪一节没生成、为什么、影响什么
//
// Three implementation rules matter more than the layout:
//
//  1. **Inline strings, no shared-strings table.** A shared-strings part needs a
//     `uniqueCount` that must match the table exactly or Excel refuses the file, and
//     nothing is gained by it at this size. `t="inlineStr"` with `<is><t>` is the
//     specification's own alternative and needs no cross-part bookkeeping.
//  2. **Cell text is sanitised, not trusted.** Evidence excerpts are arbitrary
//     source bytes; a raw 0x0B or 0x1F in a cell makes the whole workbook
//     unparseable in Excel. `xlsxCellText` strips the XML-illegal controls and caps
//     the length at the format's 32767-character cell ceiling.
//  3. **Numbers that are numbers stay numeric.** `ord`, `引用数`, `检索分` are written
//     as numeric cells so a reviewer can sort them; writing "12" as text is the
//     defect that makes a sheet look sortable and not be.
const (
	// xlsxCellLimit is one cell's character ceiling (Excel refuses more). The
	// overflow marker travels with the text so nobody mistakes a clipped excerpt for
	// the whole source; the DOCX/HTML export carries the full text.
	xlsxCellLimit = 32000
	xlsxOverflow  = "…（单元格已截断，完整内容见 DOCX/HTML 版本）"
	// xlsxInlineStrMin is the first row index Excel treats as data, and is also
	// where the sticky header ends: row 1 is the header on every data sheet.
	xlsxHeaderRow = 1
)

// xlsxSheet is one worksheet: a name (what the tab says) and its rows.
type xlsxSheet struct {
	name   string
	rows   [][]xlsxCell
	widths []int
}

// xlsxCell is one cell: either text or a number. A pointer-free boolean keeps the
// zero value meaningful ("empty cell") without inventing a third kind.
type xlsxCell struct {
	text    string
	number  float64
	numeric bool
	style   int
}

func xlsxTextCell(value string) xlsxCell   { return xlsxCell{text: value} }
func xlsxHeaderCell(value string) xlsxCell { return xlsxCell{text: value, style: 2} }
func xlsxNumberCell(value float64) xlsxCell {
	return xlsxCell{number: value, numeric: true}
}
func xlsxWrapCell(value string) xlsxCell { return xlsxCell{text: value, style: 3} }

func renderReportXlsx(document *reportDocument, now time.Time) ([]byte, error) {
	sheets := []xlsxSheet{
		reportXlsxOverviewSheet(document, now),
		reportXlsxSectionSheet(document),
		reportXlsxClaimSheet(document),
		reportXlsxEvidenceSheet(document),
	}
	if len(document.Gaps) > 0 {
		sheets = append(sheets, reportXlsxGapSheet(document))
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	writer, err := archive.Create("[Content_Types].xml")
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write([]byte(xlsxContentTypes(len(sheets)))); err != nil {
		return nil, err
	}
	parts := []struct {
		name    string
		content string
	}{
		{"_rels/.rels", xlsxRootRels()},
		{"docProps/core.xml", xlsxCoreProps(document, now)},
		{"xl/workbook.xml", xlsxWorkbook(sheets)},
		{"xl/_rels/workbook.xml.rels", xlsxWorkbookRels(len(sheets))},
		{"xl/styles.xml", xlsxStyles()},
	}
	for index, sheet := range sheets {
		parts = append(parts, struct {
			name    string
			content string
		}{fmt.Sprintf("xl/worksheets/sheet%d.xml", index+1), xlsxWorksheet(sheet)})
	}
	for _, part := range parts {
		entry, err := archive.Create(part.name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(part.content)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// reportXlsxOverviewSheet is the "what am I holding" sheet: identity first, then the
// disclosure numbers, then the fixed preamble the claim appendix needs. It is a
// key/value sheet rather than a header row because a reader opens it once.
func reportXlsxOverviewSheet(document *reportDocument, now time.Time) xlsxSheet {
	rates := "未评分"
	if document.ClaimSupportRate != nil {
		rates = fmt.Sprintf("%.0f%%（可核实 %d / %d）",
			*document.ClaimSupportRate*100, claimSupportedCount(document), document.ClaimScored)
	}
	missing := make([]string, 0, len(document.Gaps))
	for _, gap := range document.Gaps {
		missing = append(missing, fmt.Sprintf("第 %d 节「%s」：%s",
			reportSectionNumber(gap.Ord), gap.Title, gap.Reason))
	}
	gapText := "无"
	if len(missing) > 0 {
		gapText = strings.Join(missing, "\n")
	}
	consistency := "无"
	if len(document.ConsistencyIssues) > 0 {
		lines := make([]string, 0, len(document.ConsistencyIssues))
		for _, issue := range document.ConsistencyIssues {
			lines = append(lines, reportConsistencyLine(issue))
		}
		consistency = strings.Join(lines, "\n")
	}
	rows := [][]xlsxCell{
		{xlsxHeaderCell("项目"), xlsxHeaderCell("内容")},
		{xlsxTextCell("报告标题"), xlsxTextCell(document.Title)},
		{xlsxTextCell("研究问题"), xlsxWrapCell(document.Question)},
		{xlsxTextCell("报告版本"), xlsxTextCell("r" + strconv.FormatInt(document.Revision, 10))},
		{xlsxTextCell("模板"), xlsxTextCell(document.TemplateKey + " v" +
			strconv.FormatInt(document.TemplateVersion, 10))},
		{xlsxTextCell("生成时间"), xlsxTextCell(now.UTC().Format("2006-01-02 15:04:05Z"))},
		{xlsxTextCell("完整性"), xlsxTextCell(reportCompletenessLabel(document.Completeness))},
		{xlsxTextCell("复核范围"), xlsxWrapCell(document.ScanScope)},
		{xlsxTextCell("未生成的章节"), xlsxWrapCell(gapText)},
		{xlsxTextCell("结论句总数"), xlsxNumberCell(float64(len(document.Claims)))},
		{xlsxTextCell("可核实比例"), xlsxTextCell(rates)},
		{xlsxTextCell("跨章节一致性提示"), xlsxWrapCell(consistency)},
		{xlsxTextCell("证据可复核"), xlsxTextCell(reportBoolLabel(document.EvidenceVerifiable))},
		{xlsxTextCell("证据条数"), xlsxNumberCell(float64(len(document.EvidenceAppendix)))},
		{xlsxTextCell("结论清单说明"), xlsxWrapCell(reportClaimAppendixPreamble)},
		{xlsxTextCell("渲染器"), xlsxTextCell(reportExportRendererVersion)},
		{xlsxTextCell("版式"), xlsxTextCell(reportExportLayoutID(document.TemplateKey, model.ExportFormatXLSX))},
	}
	return xlsxSheet{name: "概览", rows: rows, widths: []int{22, 90}}
}

func reportXlsxClaimSheet(document *reportDocument) xlsxSheet {
	rows := [][]xlsxCell{{
		xlsxHeaderCell("序号"), xlsxHeaderCell("章节"), xlsxHeaderCell("类型"),
		xlsxHeaderCell("支持度"), xlsxHeaderCell("结论句"), xlsxHeaderCell("引用编号"),
	}}
	for index, claim := range document.Claims {
		markers := make([]string, 0, len(claim.Markers))
		for _, marker := range claim.Markers {
			markers = append(markers, strconv.Itoa(reportSectionNumber(marker)))
		}
		reference := strings.Join(markers, "、")
		if reference == "" && claim.Support != model.ReportClaimSupportNotRequired {
			reference = "未引用证据"
		}
		title := claim.SectionTitle
		if title == "" {
			title = fmt.Sprintf("第 %d 节", reportSectionNumber(claim.SectionOrd))
		}
		rows = append(rows, []xlsxCell{
			xlsxNumberCell(float64(index + 1)),
			xlsxTextCell(fmt.Sprintf("%d. %s", reportSectionNumber(claim.SectionOrd), title)),
			xlsxTextCell(reportClaimTypeLabel(claim.Type)),
			xlsxTextCell(reportClaimSupportLabel(claim.Support)),
			xlsxWrapCell(strings.TrimSpace(claim.Text)),
			xlsxTextCell(reference),
		})
	}
	return xlsxSheet{name: "结论", rows: rows, widths: []int{6, 30, 12, 12, 80, 14}}
}

// reportXlsxEvidenceSheet is the frozen citation copy (A43). The snippet and the
// snapshot body are separate columns for the same reason the appendix prints both:
// "the model saw this" and "the source says this" are different facts, and a sheet
// that merges them cannot answer "was the model working from a fragment".
func reportXlsxEvidenceSheet(document *reportDocument) xlsxSheet {
	rows := [][]xlsxCell{{
		xlsxHeaderCell("序号"), xlsxHeaderCell("章节"), xlsxHeaderCell("来源文档"),
		xlsxHeaderCell("片段"), xlsxHeaderCell("检索分"), xlsxHeaderCell("检索分类型"),
		xlsxHeaderCell("已截断"), xlsxHeaderCell("模型所见摘录"), xlsxHeaderCell("证据原文"),
	}}
	for _, entry := range document.EvidenceAppendix {
		locator := entry.DocumentName
		if locator == "" {
			locator = entry.DocumentID
		}
		if locator == "" {
			locator = entry.ChunkID
		}
		title := entry.SectionTitle
		if title == "" {
			title = fmt.Sprintf("第 %d 节", reportSectionNumber(entry.SectionOrd))
		}
		rows = append(rows, []xlsxCell{
			xlsxNumberCell(float64(reportSectionNumber(entry.Ord))),
			xlsxTextCell(fmt.Sprintf("%d. %s", reportSectionNumber(entry.SectionOrd), title)),
			xlsxTextCell(locator),
			xlsxTextCell(entry.ChunkID),
			xlsxNumberCell(entry.RetrievalScore),
			xlsxTextCell(entry.RetrievalScoreType),
			xlsxTextCell(reportBoolLabel(entry.Truncated)),
			xlsxWrapCell(entry.Snippet),
			xlsxWrapCell(entry.Content),
		})
	}
	return xlsxSheet{name: "证据", rows: rows, widths: []int{6, 30, 34, 24, 10, 14, 10, 60, 80}}
}

// reportXlsxGapSheet exists only for a partial report. A workbook that claims to be
// the delivery of a report while keeping "two chapters never ran" out of every sheet
// is the spreadsheet version of the lie A39 exists to prevent.
func reportXlsxGapSheet(document *reportDocument) xlsxSheet {
	rows := [][]xlsxCell{{
		xlsxHeaderCell("序号"), xlsxHeaderCell("章节"), xlsxHeaderCell("原因"),
		xlsxHeaderCell("影响"), xlsxHeaderCell("错误码"),
	}}
	for _, gap := range document.Gaps {
		rows = append(rows, []xlsxCell{
			xlsxNumberCell(float64(reportSectionNumber(gap.Ord))),
			xlsxTextCell(gap.Title),
			xlsxWrapCell(gap.Reason),
			xlsxWrapCell(gap.Impact),
			xlsxTextCell(gap.ErrorCode),
		})
	}
	return xlsxSheet{name: "缺口", rows: rows, widths: []int{6, 30, 50, 50, 14}}
}

// reportPlainText flattens one section's markdown to text for a spreadsheet cell:
// block structure is lost on purpose (see reportXlsxSectionSheet), but the words are
// not: table rows keep their cells separated by a visible " | " so a table does not
// collapse into a run-on sentence.
func reportPlainText(content string) string {
	var builder strings.Builder
	for _, block := range parseAnswerContent(content) {
		switch block.kind {
		case exportBlockTable:
			for _, row := range block.rows {
				builder.WriteString(strings.Join(row, " | ") + "\n")
			}
			continue
		case exportBlockImage:
			parts := strings.SplitN(block.text, "|", 2)
			builder.WriteString("图片：" + parts[0])
			if len(parts) == 2 {
				builder.WriteString("（来源 " + parts[1] + "）")
			}
		case exportBlockBullet:
			builder.WriteString("• " + reportPlainInline(block.text))
		default:
			builder.WriteString(reportPlainInline(block.text))
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// reportPlainInline drops the inline markers a cell cannot render. Bold asterisks in
// a spreadsheet are noise (`**1. 范围**` was the live defect that started this rule
// for DOCX), so they are removed rather than printed.
func reportPlainInline(text string) string {
	replaced := strings.ReplaceAll(text, "**", "")
	replaced = strings.ReplaceAll(replaced, "`", "")
	return strings.TrimSpace(replaced)
}

// xlsxCellText sanitises one cell value: XML-illegal control bytes removed (an
// evidence excerpt is arbitrary source text), then the 32767-character ceiling.
func xlsxCellText(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			builder.WriteRune(r)
		case r < 0x20:
			// Excel rejects these bytes outright, and stripping them is the only
			// option that does not change what the surrounding words say.
			continue
		case r == 0xfffe || r == 0xffff:
			continue
		default:
			builder.WriteRune(r)
		}
	}
	cleaned := builder.String()
	// Truncate by runes, not bytes: cutting a multi-byte rune in half would produce
	// invalid UTF-8 inside the XML part.
	runes := []rune(cleaned)
	if len(runes) <= xlsxCellLimit {
		return cleaned
	}
	return string(runes[:xlsxCellLimit]) + xlsxOverflow
}

// xlsxColumnName turns a 0-based column index into its A1 letters.
func xlsxColumnName(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

func xlsxWorksheet(sheet xlsxSheet) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	builder.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	// The header row stays visible while the reviewer scrolls: every sheet here is
	// longer than one screen, and a status column whose meaning scrolled away is a
	// column that gets misread.
	builder.WriteString(`<sheetViews><sheetView workbookViewId="0">` +
		`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
		`<selection pane="bottomLeft" activeCell="A2" sqref="A2"/>` +
		`</sheetView></sheetViews>`)
	builder.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)
	if len(sheet.widths) > 0 {
		builder.WriteString("<cols>")
		for index, width := range sheet.widths {
			builder.WriteString(fmt.Sprintf(
				`<col min="%d" max="%d" width="%d" customWidth="1"/>`,
				index+1, index+1, width))
		}
		builder.WriteString("</cols>")
	}
	builder.WriteString("<sheetData>")
	for rowIndex, row := range sheet.rows {
		number := rowIndex + 1
		builder.WriteString(fmt.Sprintf(`<row r="%d">`, number))
		for columnIndex, cell := range row {
			reference := xlsxColumnName(columnIndex) + strconv.Itoa(number)
			if cell.numeric {
				builder.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`,
					reference, xlsxStyleAttr(cell.style),
					strconv.FormatFloat(cell.number, 'f', -1, 64)))
				continue
			}
			if cell.text == "" {
				// An empty string cell is not a cell in xlsx terms; skipping it keeps
				// the file smaller and lets the shared style do the work.
				continue
			}
			builder.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr"%s><is><t xml:space="preserve">%s</t></is></c>`,
				reference, xlsxStyleAttr(cell.style), xmlEscape(xlsxCellText(cell.text))))
		}
		builder.WriteString("</row>")
	}
	builder.WriteString("</sheetData>")
	if len(sheet.rows) > 0 && len(sheet.rows[0]) > 0 {
		builder.WriteString(fmt.Sprintf(`<autoFilter ref="A%d:%s%d"/>`,
			xlsxHeaderRow, xlsxColumnName(len(sheet.rows[0])-1), len(sheet.rows)))
	}
	builder.WriteString(`<pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/>`)
	builder.WriteString("</worksheet>")
	return builder.String()
}

func xlsxStyleAttr(style int) string {
	if style == 0 {
		return ""
	}
	return fmt.Sprintf(` s="%d"`, style)
}

func xlsxContentTypes(sheetCount int) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	builder.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	builder.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	builder.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	builder.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	builder.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	builder.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	for index := 1; index <= sheetCount; index++ {
		builder.WriteString(fmt.Sprintf(
			`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`,
			index))
	}
	builder.WriteString("</Types>")
	return builder.String()
}

func xlsxRootRels() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
		`</Relationships>`
}

// xlsxCoreProps records what the file is and when it was rendered. The content is
// the frozen document; only the timestamp is "now", exactly like the DOCX.
func xlsxCoreProps(document *reportDocument, now time.Time) string {
	stamp := now.UTC().Format("2006-01-02T15:04:05Z")
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + xmlEscape(xlsxCellText(document.Title)) + `</dc:title>` +
		`<dc:creator>RAGFlow-X</dc:creator>` +
		`<cp:lastModifiedBy>RAGFlow-X</cp:lastModifiedBy>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:modified>` +
		`</cp:coreProperties>`
}

func xlsxWorkbook(sheets []xlsxSheet) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	builder.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	builder.WriteString(`<sheets>`)
	for index, sheet := range sheets {
		builder.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`,
			xmlEscape(sheet.name), index+1, index+1))
	}
	builder.WriteString(`</sheets></workbook>`)
	return builder.String()
}

func xlsxWorkbookRels(sheetCount int) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	builder.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for index := 1; index <= sheetCount; index++ {
		builder.WriteString(fmt.Sprintf(
			`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`,
			index, index))
	}
	builder.WriteString(fmt.Sprintf(
		`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`,
		sheetCount+1))
	builder.WriteString(`</Relationships>`)
	return builder.String()
}

// xlsxStyles is the minimum Excel accepts: two fonts, two fills (the second fill must
// be the `gray125` pattern — Excel refuses a workbook without it), one border, and
// four cell formats: default, bold, header (bold on grey), and wrapped body text.
func xlsxStyles() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="3">` +
		`<font><sz val="11"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><color rgb="FF1F2328"/><name val="Calibri"/></font>` +
		`</fonts>` +
		`<fills count="3">` +
		`<fill><patternFill patternType="none"/></fill>` +
		`<fill><patternFill patternType="gray125"/></fill>` +
		`<fill><patternFill patternType="solid"><fgColor rgb="FFF6F8FA"/><bgColor indexed="64"/></patternFill></fill>` +
		`</fills>` +
		`<borders count="2">` +
		`<border><left/><right/><top/><bottom/><diagonal/></border>` +
		`<border><left style="thin"><color rgb="FFD0D7DE"/></left>` +
		`<right style="thin"><color rgb="FFD0D7DE"/></right>` +
		`<top style="thin"><color rgb="FFD0D7DE"/></top>` +
		`<bottom style="thin"><color rgb="FFD0D7DE"/></bottom><diagonal/></border>` +
		`</borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="4">` +
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		`<xf numFmtId="0" fontId="2" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/>` +
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1">` +
		`<alignment vertical="top" wrapText="1"/></xf>` +
		`</cellXfs>` +
		`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
		`</styleSheet>`
}

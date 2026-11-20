package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report DOCX renderer (doc/288 §12 / T11, V10 spike landing).
//
// The spike (tmp/report-spike/v10docx) proved P0-A's DOCX promise with the
// standard library; this is that proof made production: the same 9 parts are
// stitched with archive/zip + text/template, the body reuses the shared
// answer_export block writers (`docxParagraph`/`docxTable`) so a table or a code
// block means the same thing in an answer export and in a report, and the CJK
// text path is pinned by an explicit `w:rFonts w:eastAsia` on every style.
//
// Two P0-A line items move from the spike into their intended shape here:
//
//  1. §12 says 页眉页脚 = 报告名 + 版本 + 生成时间, **无页码域**. The spike's
//     PAGE/NUMPAGES footer is therefore *not* carried over — an operator printing
//     a compliance report does not need Word's page counters for the P0-A
//     contract, and adding a field that is not asked for is scope creep.
//  2. §12 says the answer header (Answer Snapshot / Canonical Hash) leaves the
//     body: the hash moves to the footer in small type and into the docProps, not
//     into the page one reads.
//
// The disclosure banner (completeness + gaps) is rendered from the frozen
// payload, so the DOCX never disagrees with the Markdown or the page (A41).
// docxCaptionNumbers numbers figures and tables through the whole document, so a
// reviewer can cite "表 3" instead of "the third table of §4". The counters are
// per-document on purpose: they must match the PDF's numbering, or two files that
// claim to be the same document disagree (A4).
type docxCaptionNumbers struct {
	table int
	image int
}

// next returns the caption for a numbered block kind, or "" for text.
func (n *docxCaptionNumbers) next(kind exportBlockKind) string {
	switch kind {
	case exportBlockTable:
		n.table++
		return fmt.Sprintf("表 %d", n.table)
	case exportBlockImage:
		n.image++
		return fmt.Sprintf("图 %d", n.image)
	}
	return ""
}

func renderReportDocx(document *reportDocument, now time.Time) ([]byte, error) {
	/// The heading style is reused for every section; heading2/3 collapse to
	/// Heading2 exactly as answer exports render them (docxBlock), so a
	/// reviewer's report and an answer export lay out the same way.
	var body strings.Builder
	body.WriteString(docxParagraph(document.Title, "Title"))
	body.WriteString(docxParagraph(fmt.Sprintf("报告版本：r%d（模板 %s v%d）",
		document.Revision, document.TemplateKey, document.TemplateVersion), ""))
	body.WriteString(docxParagraph("生成时间："+now.UTC().Format("2006-01-02 15:04:05Z"), ""))
	body.WriteString(docxParagraph("完整性："+document.Completeness, ""))
	if document.ScanScope != "" {
		body.WriteString(docxParagraph("复核范围："+document.ScanScope, ""))
	}

	if document.Completeness == model.ReportCompletenessPartial || len(document.Gaps) > 0 {
		body.WriteString(docxParagraph("完整性声明", "Heading1"))
		body.WriteString(docxParagraph("本报告为部分完成，以下节未生成：", ""))
		for _, gap := range document.Gaps {
			line := fmt.Sprintf("第 %d 节「%s」未生成：%s（影响：%s）",
				reportSectionNumber(gap.Ord), gap.Title, gap.Reason, gap.Impact)
			if gap.ErrorCode != "" {
				line += fmt.Sprintf("；错误码 %s", gap.ErrorCode)
			}
			body.WriteString(docxBlock(exportBlock{kind: exportBlockBullet, text: line}))
		}
		body.WriteString(docxParagraph("缺失节的结论强度按 §13 无法给出，请人工复核后决定是否重新生成。", ""))
	}

	// 目录 (§12 封面/目录): the title block above is the cover, this is the contents.
	// Page numbers are deliberately absent: DOCX shows them only after Word updates a
	// TOC field, and printing "1" for every row would be worse than printing none.
	// A numbered title list still navigates (and survives being exported to PDF by
	// the reader's own tool).
	body.WriteString(docxParagraph("目录", "Heading1"))
	for _, section := range document.Sections {
		body.WriteString(docxParagraph(fmt.Sprintf("%d. %s", reportSectionNumber(section.Ord), section.Title), ""))
	}
	if len(document.EvidenceAppendix) > 0 {
		body.WriteString(docxParagraph("证据附录", ""))
	}
	if len(document.Claims) > 0 {
		body.WriteString(docxParagraph("结论清单", ""))
	}
	if len(document.ConsistencyIssues) > 0 {
		body.WriteString(docxParagraph("跨章节一致性提示", ""))
	}

	captions := &docxCaptionNumbers{}
	for index, section := range document.Sections {
		if index > 0 {
			body.WriteString(docxParagraph("", ""))
		}
		body.WriteString(docxParagraph(fmt.Sprintf("%d. %s", reportSectionNumber(section.Ord), section.Title), "Heading1"))
		for _, block := range parseAnswerContent(section.ContentMD) {
			if caption := captions.next(block.kind); caption != "" {
				body.WriteString(docxParagraph(caption, ""))
			}
			body.WriteString(docxBlock(block))
		}
		if line := reportLocationHintsLine(section.LocationHints); line != "" {
			body.WriteString(docxParagraph(line, ""))
		}
		if plan := reportResearchPlanLine(section.ResearchPlan); plan != "" {
			body.WriteString(docxParagraph(plan, ""))
		}
		if section.EvidenceStatus != "" && section.CitationCount > 0 {
			body.WriteString(docxParagraph(fmt.Sprintf("依据：%d 条引用，证据状态 %s。",
				section.CitationCount, reportEvidenceStatusLabel(section.EvidenceStatus)), ""))
		}
	}

	// The evidence appendix is part of the delivered document (§12 / A13), built
	// from the frozen payload so it survives the snapshots it cites (A43).
	if len(document.EvidenceAppendix) > 0 {
		body.WriteString(docxParagraph("证据附录", "Heading1"))
		if document.EvidenceVerifiable {
			body.WriteString(docxParagraph("以下为生成时冻结的证据副本，可与原始文档逐条核对。", ""))
		} else {
			body.WriteString(docxParagraph("以下为生成时冻结的证据副本；原始快照已过期，当前无法复核。", ""))
		}
		currentSection := -1
		for _, entry := range document.EvidenceAppendix {
			if entry.SectionOrd != currentSection {
				currentSection = entry.SectionOrd
				title := entry.SectionTitle
				if title == "" {
					title = fmt.Sprintf("第 %d 节", reportSectionNumber(entry.SectionOrd))
				}
				body.WriteString(docxParagraph(fmt.Sprintf("%d. %s", reportSectionNumber(entry.SectionOrd), title), "Heading2"))
				// The column header repeats per chapter on purpose: a reader who
				// opens the appendix at random should not have to scroll up to know
				// what the columns mean.
				body.WriteString(docxTable([][]string{
					{"#", "来源", "分数", "快照片段"},
				}))
			}
			locator := entry.DocumentName
			if locator == "" {
				locator = entry.DocumentID
			}
			if locator == "" {
				locator = entry.ChunkID
			}
			note := ""
			if entry.Truncated {
				note = "（快照已按策略截断）"
			}
			body.WriteString(docxTable([][]string{{
				fmt.Sprintf("%d", reportSectionNumber(entry.Ord)), locator, scoreLabel(entry), entry.Content + note,
			}}))
		}
	}

	// 结论清单 (R11, doc/288 §9.2): what the report asserts, and what the mechanical
	// support check could say about each assertion. Printed for the same reason as the
	// evidence appendix — a forwarded file must keep saying which of its own sentences
	// it could not substantiate.
	if len(document.Claims) > 0 {
		body.WriteString(docxParagraph("结论清单", "Heading1"))
		body.WriteString(docxParagraph(reportClaimAppendixPreamble, ""))
		body.WriteString(docxParagraph(reportClaimSummary(document), ""))
		for _, group := range reportClaimGroups(document) {
			body.WriteString(docxParagraph(fmt.Sprintf("%d. %s", reportSectionNumber(group.Ord), group.Title), "Heading2"))
			for _, line := range group.Headings {
				body.WriteString(docxParagraph("· "+line, ""))
			}
		}
	}
	if len(document.ConsistencyIssues) > 0 {
		body.WriteString(docxParagraph("跨章节一致性提示", "Heading1"))
		body.WriteString(docxParagraph("以下同一事实在不同章节出现不同说法，请核实后再对外使用。", ""))
		for _, issue := range document.ConsistencyIssues {
			body.WriteString(docxParagraph(reportConsistencyLine(issue), ""))
		}
	}

	data := reportDocxData{
		Title:   document.Title,
		Version: fmt.Sprintf("r%d · 模板 %s v%d", document.Revision, document.TemplateKey, document.TemplateVersion),
		Time:    now.UTC().Format("2006-01-02 15:04:05Z"),
		Body:    body.String(),
		// The footer hash is the same canonical the service froze as its
		// idempotency component: it is the sha256 of the exact payload JSON, so a
		// reader can verify the DOCX against the artifact row.
		Canonical: reportDocxCanonical(document),
		Renderer:  reportExportRendererVersion + " · " + reportExportLayoutID(document.TemplateKey, model.ExportFormatDOCX),
	}
	return writeReportDocx(data, now)
}

// reportDocxCanonical recomputes the payload hash inside the renderer. The
// assemble step produced the identical bytes, but the renderer is only handed the
// struct; marshaling it again must, and does, yield the same JSON (no order or
// timestamp drift in the doc model — that is rule 1 of report_assemble.go).
func reportDocxCanonical(document *reportDocument) string {
	payload, err := json.Marshal(document)
	if err != nil {
		return ""
	}
	return sha256Hex(string(payload))
}

// reportDocxData carries the few templated values. Every string that lands in
// XML goes through xmlEscape before template execution; Body is already escaped
// by docxParagraph's own writer.
type reportDocxData struct {
	Title     string
	Version   string
	Time      string
	Body      string
	Canonical string
	Renderer  string
}

// writeReportDocx stitches the 9 parts of a WordprocessingML package. The XML
// scaffolding is frozen here as constants; only the six documented render points
// carry templated values, so adding a block type never touches the package glue.
func writeReportDocx(data reportDocxData, now time.Time) ([]byte, error) {
	// Escape before templating: the template engine would otherwise treat the
	// report title's `&`/`<` as markup in header/footer/core.
	data.Title = xmlEscape(data.Title)
	data.Version = xmlEscape(data.Version)
	data.Time = xmlEscape(data.Time)
	data.Canonical = xmlEscape(data.Canonical)
	data.Renderer = xmlEscape(data.Renderer)

	coreProps := struct {
		Title string
		Now   string
		Hash  string
	}{data.Title, now.UTC().Format("2006-01-02T15:04:05Z"), data.Canonical}

	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	files := []struct {
		name string
		body string
		data interface{}
	}{
		{"[Content_Types].xml", reportDocxContentTypes, nil},
		{"_rels/.rels", reportDocxRootRels, nil},
		{"word/document.xml", reportDocxDocumentXML, data},
		{"word/_rels/document.xml.rels", reportDocxDocumentRels, nil},
		{"word/styles.xml", reportDocxStylesXML, nil},
		{"word/header1.xml", reportDocxHeaderXML, data},
		{"word/footer1.xml", reportDocxFooterXML, data},
		{"docProps/core.xml", reportDocxCoreXML, coreProps},
		{"docProps/app.xml", reportDocxAppXML, nil},
	}
	for _, file := range files {
		rendered, err := renderDocxPart(file.name, file.body, file.data)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		entry, err := writer.Create(file.name)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		if _, err := entry.Write(rendered); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderDocxPart executes a text/template when the part carries data. The
// templates are compiled once per write and are all static strings, so the parse
// cannot fail; the error path exists to satisfy the template API.
func renderDocxPart(name, body string, data interface{}) ([]byte, error) {
	if data == nil || !strings.Contains(body, "{{") {
		return []byte(body), nil
	}
	tpl, err := template.New(name).Parse(body)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	if err := tpl.Execute(&builder, data); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

const reportDocxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>
<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>
<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>`

const reportDocxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>`

const reportDocxDocumentRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`

// reportDocxStylesXML pins the eastAsia font on every style, so CJK never falls
// back to Word's default theme font. `TableGrid` gives the shared docxTable its
// borders (the shared table block references the style by name); `Code` is the
// monospaced block for code/formula; `Title` mirrors Word's document title.
const reportDocxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimSun" w:cs="Times New Roman"/><w:sz w:val="24"/></w:rPr></w:rPrDefault>
<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:jc w:val="center"/><w:spacing w:before="120" w:after="240"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimHei"/><w:b/><w:sz w:val="40"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="0"/><w:spacing w:before="240" w:after="120"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimHei"/><w:b/><w:sz w:val="32"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="1"/><w:spacing w:before="200" w:after="100"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimHei"/><w:b/><w:sz w:val="28"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="60" w:after="60" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="SimSun"/><w:sz w:val="21"/></w:rPr></w:style>
<w:style w:type="character" w:styleId="Bold"><w:name w:val="Bold"/><w:rPr><w:b/></w:rPr></w:style>
<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:pPr><w:spacing w:after="40" w:line="300" w:lineRule="auto"/></w:pPr><w:tblPr><w:tblBorders>
<w:top w:val="single" w:sz="4" w:space="0" w:color="999999"/><w:left w:val="single" w:sz="4" w:space="0" w:color="999999"/>
<w:bottom w:val="single" w:sz="4" w:space="0" w:color="999999"/><w:right w:val="single" w:sz="4" w:space="0" w:color="999999"/>
<w:insideH w:val="single" w:sz="4" w:space="0" w:color="999999"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="999999"/>
</w:tblBorders></w:tblPr></w:style>
</w:styles>`

// reportDocxHeaderXML is §12's 页眉: 报告名 + 版本 + 生成时间, right-aligned and
// small so it stays a page mark, not competing with the body.
const reportDocxHeaderXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimSun"/><w:sz w:val="18"/><w:color w:val="777777"/></w:rPr><w:t>{{.Title}} · {{.Version}} · {{.Time}}</w:t></w:r></w:p>
</w:hdr>`

// reportDocxFooterXML is §12's 页脚小字: the canonical hash and renderer identity
// that previously lived in the answer document header. Deliberately *no* PAGE
// field — the P0-A contract stops at report name/version/time (§12 无页码域).
const reportDocxFooterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:pPr><w:jc w:val="center"/></w:pPr>
<w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="SimSun"/><w:sz w:val="16"/><w:color w:val="999999"/></w:rPr><w:t>Canonical {{.Canonical}} · Rendered by {{.Renderer}}</w:t></w:r>
</w:p>
</w:ftr>`

// reportDocxCoreXML carries the identity P0-A moved out of the body: the report
// title and its canonical hash now live in the document properties instead of
// page one (doc/288 §12).
const reportDocxCoreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<dc:title>{{.Title}}</dc:title><dc:creator>ragflow-x</dc:creator><cp:lastModifiedBy>ragflow-x</cp:lastModifiedBy>
<dc:description>canonical:{{.Hash}}</dc:description>
<dcterms:created xsi:type="dcterms:W3CDTF">{{.Now}}</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">{{.Now}}</dcterms:modified>
</cp:coreProperties>`

const reportDocxAppXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>ragflow-x report renderer</Application></Properties>`

// reportDocxDocumentXML wraps the assembled body with the header/footer
// references. The body was built by docxParagraph/docxBlock, which already
// XML-escape their content; the data pipeline never injects raw markup here.
const reportDocxDocumentXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>{{.Body}}
<w:sectPr><w:headerReference w:type="default" r:id="rId2" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/>
<w:footerReference w:type="default" r:id="rId3" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/>
<w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1418" w:bottom="1440" w:left="1418" w:header="851" w:footer="992"/></w:sectPr>
</w:body>
</w:document>`

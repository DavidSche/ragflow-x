package service

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report HTML renderer (doc/288 §12 / queue item 4).
//
// Why a report needs HTML at all, given DOCX and PDF already exist: the two
// operator deliverables are files you forward, and forwarding a report is exactly
// the moment its disclosure (完整性声明 / 证据附录 / 结论清单 / 跨章节一致性) must
// survive. A PDF page images that disclosure away from search and copy; an HTML file
// keeps it as text, opens on a phone, prints correctly through `@media print`, and
// carries anchors so a reviewer can send a colleague `report.html#s3` instead of
// "third chapter".
//
// Three constraints shape the file:
//
//  1. **Self-contained.** No stylesheet, font or script is fetched at open time. A
//     delivered report that needs the network to look right is a report that will
//     one day render as an unstyled wall of text in a customer's inbox (and an
//     `<img src>` pointing at an internal host leaks where it came from).
//  2. **The disclosure is read from the frozen payload**, never from the live read
//     API, for the same reason as A39/A41: a forwarded file must keep saying what it
//     could not substantiate.
//  3. **Nothing is executed.** The body is the writer's markdown, converted with
//     escaping first and a deliberately small inline grammar after. Only http(s)
//     and mailto links survive; an `javascript:` target is rendered as text.
const reportHTMLStyle = `:root{color-scheme:light}
*{box-sizing:border-box}
body{margin:0;padding:40px 20px;background:#f7f7f8;color:#1f2328;
font:16px/1.7 -apple-system,"Segoe UI","Noto Sans SC",sans-serif}
main{max-width:860px;margin:0 auto;background:#fff;padding:48px 56px;
border:1px solid #e3e5e8;border-radius:4px}
h1{font-size:28px;margin:0 0 8px;line-height:1.35}
h2{font-size:20px;margin:36px 0 12px;padding-top:8px;border-top:1px solid #ebedf0}
h3{font-size:17px;margin:22px 0 8px}
p{margin:0 0 12px}
dl.meta{margin:12px 0 0;padding:12px 16px;background:#f6f8fa;border-radius:4px;font-size:14px}
dl.meta div{margin:2px 0}
dl.meta dt{display:inline;color:#57606a}
dl.meta dt:after{content:"："}
dl.meta dd{display:inline;margin:0 16px 0 0}
.disclosure{margin:24px 0;padding:16px 20px;border-left:4px solid #bf8700;
background:#fff8e5;border-radius:0 4px 4px 0}
.disclosure h2{margin:0 0 8px;font-size:16px;border:0;padding:0}
.disclosure ul{margin:8px 0 0;padding-left:20px}
.hint{color:#57606a;font-size:14px;font-style:italic;margin:8px 0}
nav.toc{margin:24px 0;padding:16px 20px;border:1px solid #ebedf0;border-radius:4px}
nav.toc h2{margin:0 0 8px;font-size:16px;border:0;padding:0}
nav.toc ol{margin:0;padding-left:22px}
blockquote{margin:8px 0 8px 12px;padding:4px 12px;border-left:3px solid #d0d7de;color:#3d444d}
pre{margin:0 0 12px;padding:12px;background:#f6f8fa;border-radius:4px;overflow:auto;
font:13px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace;white-space:pre-wrap}
code{font:13px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace;
background:rgba(175,184,193,.2);padding:.1em .35em;border-radius:3px}
pre code{background:none;padding:0}
table{border-collapse:collapse;width:100%;margin:0 0 14px;font-size:14px}
th,td{border:1px solid #d0d7de;padding:6px 10px;text-align:left;vertical-align:top}
th{background:#f6f8fa}
article.citation{margin:0 0 18px;padding:12px 16px;border:1px solid #ebedf0;border-radius:4px}
article.citation h4{margin:0 0 6px;font-size:15px}
article.citation cite{display:block;color:#57606a;font-size:13px;font-style:normal;margin:0 0 8px}
footer{margin:40px 0 0;padding-top:12px;border-top:1px solid #ebedf0;color:#57606a;font-size:13px}
@media print{body{background:#fff;padding:0}main{border:0;padding:0;max-width:none}
article.citation,.disclosure,nav.toc{break-inside:avoid}h2{break-after:avoid}}`

// renderReportHTML produces the report as one standalone HTML document.
func renderReportHTML(document *reportDocument, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("<!doctype html>\n<html lang=\"zh-CN\">\n<head>\n")
	builder.WriteString("<meta charset=\"utf-8\">\n")
	builder.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	builder.WriteString("<meta name=\"generator\" content=\"" +
		html.EscapeString(reportExportRendererVersion) + "\">\n")
	builder.WriteString("<title>" + html.EscapeString(document.Title) + "</title>\n")
	builder.WriteString("<style>\n" + reportHTMLStyle + "\n</style>\n</head>\n<body>\n<main>\n")

	builder.WriteString("<h1>" + html.EscapeString(document.Title) + "</h1>\n")
	builder.WriteString("<dl class=\"meta\">\n")
	builder.WriteString("<div><dt>报告版本</dt><dd>r" + fmt.Sprint(document.Revision) + "</dd></div>\n")
	builder.WriteString("<div><dt>模板</dt><dd>" + html.EscapeString(document.TemplateKey) +
		" v" + fmt.Sprint(document.TemplateVersion) + "</dd></div>\n")
	builder.WriteString("<div><dt>生成时间</dt><dd>" +
		html.EscapeString(now.UTC().Format("2006-01-02 15:04:05Z")) + "</dd></div>\n")
	builder.WriteString("<div><dt>完整性</dt><dd>" +
		html.EscapeString(reportCompletenessLabel(document.Completeness)) + "</dd></div>\n")
	if document.ScanScope != "" {
		builder.WriteString("<div><dt>复核范围</dt><dd>" +
			html.EscapeString(document.ScanScope) + "</dd></div>\n")
	}
	builder.WriteString("</dl>\n")

	if document.Question != "" {
		builder.WriteString("<p class=\"hint\">研究问题：" + html.EscapeString(document.Question) + "</p>\n")
	}
	builder.WriteString(reportHTMLDisclosure(document))
	builder.WriteString(reportHTMLTableOfContents(document))

	for _, section := range document.Sections {
		builder.WriteString(fmt.Sprintf("<section id=\"s%d\">\n", reportSectionNumber(section.Ord)))
		builder.WriteString(fmt.Sprintf("<h2>%d. %s</h2>\n",
			reportSectionNumber(section.Ord), html.EscapeString(section.Title)))
		builder.WriteString(reportHTMLBlocks(section.ContentMD))
		if line := reportLocationHintsLine(section.LocationHints); line != "" {
			builder.WriteString("<p class=\"hint\">" + html.EscapeString(line) + "</p>\n")
		}
		if line := reportResearchPlanLine(section.ResearchPlan); line != "" {
			builder.WriteString("<p class=\"hint\">" + html.EscapeString(line) + "</p>\n")
		}
		if section.EvidenceStatus != "" && section.CitationCount > 0 {
			builder.WriteString(fmt.Sprintf("<p class=\"hint\">依据：%d 条引用，证据状态 %s。</p>\n",
				section.CitationCount, html.EscapeString(reportEvidenceStatusLabel(section.EvidenceStatus))))
		}
		builder.WriteString("</section>\n")
	}

	builder.WriteString(reportHTMLEvidenceAppendix(document))
	builder.WriteString(reportHTMLClaimAppendix(document))
	builder.WriteString(reportHTMLConsistency(document))

	builder.WriteString("<footer>本报告由 RAGFlow-X 报告渲染器（" +
		html.EscapeString(reportExportLayoutID(document.TemplateKey, model.ExportFormatHTML)) +
		"，" + html.EscapeString(reportExportRendererVersion) + "）生成。</footer>\n")
	builder.WriteString("</main>\n</body>\n</html>\n")
	return builder.String()
}

// reportHTMLDisclosure prints the completeness banner. Same condition and same
// wording as the Markdown/DOCX renderers: only a partial report (or one with gap
// entries) carries it, and the gap list is the frozen one.
func reportHTMLDisclosure(document *reportDocument) string {
	if document.Completeness != model.ReportCompletenessPartial && len(document.Gaps) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<section class=\"disclosure\" role=\"note\">\n")
	builder.WriteString("<h2>完整性声明</h2>\n<p>本报告为部分完成。</p>\n<ul>\n")
	for _, gap := range document.Gaps {
		builder.WriteString(fmt.Sprintf("<li>第 %d 节「%s」未生成：%s（影响：%s）",
			reportSectionNumber(gap.Ord), html.EscapeString(gap.Title),
			html.EscapeString(gap.Reason), html.EscapeString(gap.Impact)))
		if gap.ErrorCode != "" {
			builder.WriteString("（错误码 " + html.EscapeString(gap.ErrorCode) + "）")
		}
		builder.WriteString("</li>\n")
	}
	builder.WriteString("</ul>\n</section>\n")
	return builder.String()
}

func reportHTMLTableOfContents(document *reportDocument) string {
	if len(document.Sections) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<nav class=\"toc\">\n<h2>目录</h2>\n<ol>\n")
	for _, section := range document.Sections {
		builder.WriteString(fmt.Sprintf("<li><a href=\"#s%d\">%s</a></li>\n",
			reportSectionNumber(section.Ord), html.EscapeString(section.Title)))
	}
	builder.WriteString("</ol>\n</nav>\n")
	return builder.String()
}

// reportHTMLBlocks converts one section's markdown into body blocks using the same
// parser the DOCX/PDF renderers use, so a table or a code block means the same thing
// in every delivered format.
func reportHTMLBlocks(content string) string {
	var builder strings.Builder
	for _, block := range parseAnswerContent(content) {
		switch block.kind {
		case exportBlockHeading1, exportBlockHeading2, exportBlockHeading3:
			builder.WriteString("<h3>" + reportHTMLInline(block.text) + "</h3>\n")
		case exportBlockBullet:
			builder.WriteString("<ul><li>" + reportHTMLInline(block.text) + "</li></ul>\n")
		case exportBlockCode:
			builder.WriteString("<pre><code>" + html.EscapeString(block.text) + "</code></pre>\n")
		case exportBlockFormula:
			builder.WriteString("<pre><code>" + html.EscapeString(block.text) + "</code></pre>\n")
		case exportBlockImage:
			// Deliberately not an `<img>`: a delivered file must not fetch anything
			// when it is opened, so the alt text and the source are printed instead.
			alt, source := block.text, ""
			if parts := strings.SplitN(block.text, "|", 2); len(parts) == 2 {
				alt, source = parts[0], parts[1]
			}
			builder.WriteString("<p class=\"hint\">图片：" + html.EscapeString(alt))
			if source != "" {
				builder.WriteString("（来源 " + html.EscapeString(source) + "）")
			}
			builder.WriteString("</p>\n")
		case exportBlockTable:
			builder.WriteString(reportHTMLTable(block.rows))
		default:
			if strings.TrimSpace(block.text) == "" {
				continue
			}
			builder.WriteString("<p>" + reportHTMLInline(block.text) + "</p>\n")
		}
	}
	return builder.String()
}

func reportHTMLTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<table>\n")
	for index, row := range rows {
		tag := "td"
		if index == 0 {
			builder.WriteString("<thead>\n")
			tag = "th"
		}
		builder.WriteString("<tr>")
		for _, cell := range row {
			builder.WriteString("<" + tag + ">" + reportHTMLInline(cell) + "</" + tag + ">")
		}
		builder.WriteString("</tr>\n")
		if index == 0 {
			builder.WriteString("</thead>\n<tbody>\n")
		}
	}
	if len(rows) > 0 {
		builder.WriteString("</tbody>\n")
	}
	builder.WriteString("</table>\n")
	return builder.String()
}

var (
	reportHTMLBoldPattern = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reportHTMLCodePattern = regexp.MustCompile("`([^`\\n]+)`")
	reportHTMLLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
)

// reportHTMLInline escapes first, then applies the small inline grammar. Order
// matters: converting first and escaping after would turn the markup we just wrote
// into visible `&lt;strong&gt;`.
func reportHTMLInline(text string) string {
	escaped := html.EscapeString(strings.TrimSpace(text))
	escaped = reportHTMLLinkPattern.ReplaceAllStringFunc(escaped, func(match string) string {
		groups := reportHTMLLinkPattern.FindStringSubmatch(match)
		if len(groups) != 3 || !reportHTMLSafeURL(groups[2]) {
			// An unsupported scheme keeps the words and drops the link: a report file
			// that navigates to `javascript:…` when clicked is not a document.
			return groups[1] + "（" + groups[2] + "）"
		}
		return `<a href="` + groups[2] + `" rel="noopener noreferrer">` + groups[1] + `</a>`
	})
	escaped = reportHTMLBoldPattern.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = reportHTMLCodePattern.ReplaceAllString(escaped, "<code>$1</code>")
	return escaped
}

// reportHTMLSafeURL allows the two schemes a report body can legitimately cite.
func reportHTMLSafeURL(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(lower, `"'<>`+"`") {
		return false
	}
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

// reportHTMLEvidenceAppendix prints the frozen evidence copy (A43): the file, not
// the database, is the source.
func reportHTMLEvidenceAppendix(document *reportDocument) string {
	if len(document.EvidenceAppendix) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<section class=\"evidence\">\n<h2>证据附录</h2>\n")
	if document.EvidenceVerifiable {
		builder.WriteString("<p>以下为生成时冻结的证据副本，可与原始文档逐条核对。</p>\n")
	} else {
		builder.WriteString("<p>以下为生成时冻结的证据副本；原始快照已过期，当前无法复核。</p>\n")
	}
	for _, entry := range document.EvidenceAppendix {
		title := entry.SectionTitle
		if title == "" {
			title = fmt.Sprintf("第 %d 节", reportSectionNumber(entry.SectionOrd))
		}
		locator := entry.DocumentName
		if locator == "" {
			locator = entry.DocumentID
		}
		if locator == "" {
			locator = entry.ChunkID
		}
		builder.WriteString("<article class=\"citation\">\n")
		builder.WriteString(fmt.Sprintf("<h4>[%d] %s</h4>\n",
			reportSectionNumber(entry.Ord), html.EscapeString(locator)))
		builder.WriteString("<cite>" + html.EscapeString(fmt.Sprintf("%d. %s · %s",
			reportSectionNumber(entry.SectionOrd), title, scoreLabel(entry))) + "</cite>\n")
		if entry.Truncated {
			builder.WriteString("<p class=\"hint\">快照已按策略截断，仅保留关键片段</p>\n")
		}
		builder.WriteString("<blockquote><pre>" + html.EscapeString(entry.Content) +
			"</pre></blockquote>\n</article>\n")
	}
	builder.WriteString("</section>\n")
	return builder.String()
}

// reportHTMLClaimAppendix prints R11's claim list as a table. A table rather than a
// list because the four columns are what a reviewer scans one at a time (哪一个章节 /
// 哪一类句子 / 可不可核实 / 说了什么), and a table survives copy-paste into a review
// spreadsheet, which a nested list does not.
func reportHTMLClaimAppendix(document *reportDocument) string {
	if len(document.Claims) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<section class=\"claims\">\n<h2>结论清单</h2>\n")
	builder.WriteString("<p>" + html.EscapeString(reportClaimAppendixPreamble) + "</p>\n")
	builder.WriteString("<p>" + html.EscapeString(reportClaimSummary(document)) + "</p>\n")
	builder.WriteString("<table>\n<thead><tr><th>章节</th><th>类型</th><th>支持度</th>" +
		"<th>结论句</th><th>引用</th></tr></thead>\n<tbody>\n")
	for _, claim := range document.Claims {
		markers := make([]string, 0, len(claim.Markers))
		for _, marker := range claim.Markers {
			markers = append(markers, fmt.Sprintf("[%d]", reportSectionNumber(marker)))
		}
		reference := strings.Join(markers, " ")
		if reference == "" && claim.Support != model.ReportClaimSupportNotRequired {
			reference = "未引用证据"
		}
		title := claim.SectionTitle
		if title == "" {
			title = fmt.Sprintf("第 %d 节", reportSectionNumber(claim.SectionOrd))
		}
		builder.WriteString(fmt.Sprintf(
			"<tr><td>%d. %s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			reportSectionNumber(claim.SectionOrd), html.EscapeString(title),
			html.EscapeString(reportClaimTypeLabel(claim.Type)),
			html.EscapeString(reportClaimSupportLabel(claim.Support)),
			html.EscapeString(strings.TrimSpace(claim.Text)),
			html.EscapeString(reference)))
	}
	builder.WriteString("</tbody>\n</table>\n</section>\n")
	return builder.String()
}

// reportHTMLConsistency prints R16's cross-section disagreements.
func reportHTMLConsistency(document *reportDocument) string {
	if len(document.ConsistencyIssues) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<section class=\"consistency\">\n<h2>跨章节一致性提示</h2>\n")
	builder.WriteString("<p>以下同一事实在不同章节出现不同说法，请核实后再对外使用。</p>\n<ul>\n")
	for _, issue := range document.ConsistencyIssues {
		builder.WriteString("<li>" + html.EscapeString(reportConsistencyLine(issue)) + "</li>\n")
	}
	builder.WriteString("</ul>\n</section>\n")
	return builder.String()
}

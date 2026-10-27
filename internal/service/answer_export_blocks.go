package service

import (
	"encoding/json"
	"strings"

	"github.com/go-pdf/fpdf"
)

type exportBlockKind string

const (
	exportBlockParagraph exportBlockKind = "paragraph"
	exportBlockHeading1  exportBlockKind = "heading1"
	exportBlockHeading2  exportBlockKind = "heading2"
	exportBlockHeading3  exportBlockKind = "heading3"
	exportBlockBullet    exportBlockKind = "bullet"
	exportBlockCode      exportBlockKind = "code"
	exportBlockTable     exportBlockKind = "table"
	exportBlockImage     exportBlockKind = "image"
	exportBlockFormula   exportBlockKind = "formula"
)

type exportBlock struct {
	kind exportBlockKind
	text string
	rows [][]string
}

func parseAnswerContent(content string) []exportBlock {
	blocks := make([]exportBlock, 0, 16)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); {
		line := strings.TrimRight(lines[index], " ")
		switch {
		case line == "":
			index++
		case strings.HasPrefix(line, "```"):
			code := make([]string, 0, 8)
			index++
			for index < len(lines) && !strings.HasPrefix(lines[index], "```") {
				code = append(code, lines[index])
				index++
			}
			if index < len(lines) {
				index++
			}
			blocks = append(blocks, exportBlock{kind: exportBlockCode, text: strings.Join(code, "\n")})
		case strings.HasPrefix(line, "![") && strings.Contains(line, "]("):
			alt, source := markdownLink(line[2:])
			blocks = append(blocks, exportBlock{kind: exportBlockImage, text: alt + "|" + source})
			index++
		case strings.HasPrefix(line, "$$"):
			formula := strings.TrimPrefix(line, "$$")
			formula = strings.TrimSuffix(formula, "$$")
			index++
			for index < len(lines) && !strings.HasPrefix(lines[index], "$$") {
				formula += "\n" + lines[index]
				index++
			}
			if index < len(lines) {
				index++
			}
			blocks = append(blocks, exportBlock{kind: exportBlockFormula, text: strings.TrimSpace(formula)})
		case isMarkdownTable(line):
			rows := make([][]string, 0, 4)
			for index < len(lines) && isMarkdownTable(lines[index]) {
				if !isMarkdownTableSeparator(lines[index]) {
					rows = append(rows, markdownTableRow(lines[index]))
				}
				index++
			}
			blocks = append(blocks, exportBlock{kind: exportBlockTable, rows: rows})
		case markdownHeadingLevel(line) > 0:
			level, text := markdownHeading(line)
			switch {
			case level == 1:
				blocks = append(blocks, exportBlock{kind: exportBlockHeading1, text: text})
			case level == 2:
				blocks = append(blocks, exportBlock{kind: exportBlockHeading2, text: text})
			default:
				blocks = append(blocks, exportBlock{kind: exportBlockHeading3, text: text})
			}
			index++
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			blocks = append(blocks, exportBlock{kind: exportBlockBullet, text: strings.TrimSpace(line[2:])})
			index++
		default:
			// A hash-prefixed line that is not a valid heading (e.g. "#######"
			// with seven hashes or "#NoSpace" without a space) starts its own
			// paragraph block. It must be consumed here with an explicit index
			// bump: the aggregation loop below refuses hash lines, so without
			// this branch the parser would spin forever on such input.
			if strings.HasPrefix(line, "#") {
				blocks = append(blocks, exportBlock{kind: exportBlockParagraph, text: strings.TrimSpace(line)})
				index++
				continue
			}
			paragraph := make([]string, 0, 4)
			for index < len(lines) && strings.TrimSpace(lines[index]) != "" &&
				!strings.HasPrefix(lines[index], "```") &&
				!isMarkdownTable(lines[index]) && !strings.HasPrefix(lines[index], "![") &&
				!strings.HasPrefix(lines[index], "- ") && !strings.HasPrefix(lines[index], "* ") &&
				// Any hash-prefixed line starts its own block: valid headings are
				// matched by the heading case above, and malformed ones (e.g.
				// "#######" or "#NoSpace") must not be folded into the previous
				// paragraph or the following heading.
				!strings.HasPrefix(lines[index], "#") {
				paragraph = append(paragraph, lines[index])
				index++
			}
			blocks = append(blocks, exportBlock{kind: exportBlockParagraph, text: strings.TrimSpace(strings.Join(paragraph, "\n"))})
		}
	}
	return blocks
}

func markdownHeadingLevel(line string) int {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0
	}
	if level == len(line) || line[level] == ' ' {
		return level
	}
	return 0
}

func markdownHeading(line string) (int, string) {
	level := markdownHeadingLevel(line)
	return level, strings.TrimSpace(line[level:])
}

func isMarkdownTable(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|") && strings.HasSuffix(strings.TrimSpace(line), "|")
}

func isMarkdownTableSeparator(line string) bool {
	cells := markdownTableRow(line)
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" || strings.Trim(trimmed, ":-") != "" {
			return false
		}
	}
	return len(cells) > 0
}

func markdownTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for index, part := range parts {
		parts[index] = strings.TrimSpace(part)
	}
	return parts
}

func markdownLink(value string) (string, string) {
	end := strings.Index(value, "](")
	if end < 0 || !strings.HasSuffix(value, ")") {
		return strings.TrimSpace(value), ""
	}
	return strings.TrimSpace(value[:end]), strings.TrimSpace(value[end+2 : len(value)-1])
}

func docxBlock(block exportBlock) string {
	switch block.kind {
	case exportBlockHeading1:
		return docxParagraph(block.text, "Heading1")
	case exportBlockHeading2:
		return docxParagraph(block.text, "Heading2")
	case exportBlockHeading3:
		return docxParagraph(block.text, "Heading2")
	case exportBlockBullet:
		return docxParagraph("• "+block.text, "")
	case exportBlockCode:
		return docxParagraph(block.text, "Code")
	case exportBlockImage:
		parts := strings.SplitN(block.text, "|", 2)
		return docxParagraph("Image: "+parts[0]+" Source: "+parts[1], "")
	case exportBlockFormula:
		return docxParagraph("Formula: "+block.text, "Code")
	case exportBlockTable:
		return docxTable(block.rows)
	default:
		return docxParagraph(block.text, "")
	}
}

func docxTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var table strings.Builder
	table.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/></w:tblPr>`)
	for _, row := range rows {
		table.WriteString(`<w:tr>`)
		for _, cell := range row {
			table.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr>` + docxParagraph(cell, "") + `</w:tc>`)
		}
		table.WriteString(`</w:tr>`)
	}
	table.WriteString(`</w:tbl>`)
	return table.String()
}

func pdfBlock(pdf *fpdf.Fpdf, block exportBlock) {
	switch block.kind {
	case exportBlockHeading1:
		pdf.SetFont("cjk", "", 13)
		pdf.MultiCell(0, 7, block.text, "", "L", false)
	case exportBlockHeading2:
		pdf.SetFont("cjk", "", 11)
		pdf.MultiCell(0, 6, block.text, "", "L", false)
	case exportBlockHeading3:
		pdf.SetFont("cjk", "", 10)
		pdf.MultiCell(0, 5, block.text, "", "L", false)
	case exportBlockBullet:
		pdf.SetFont("cjk", "", 10)
		pdf.MultiCell(0, 6, "• "+block.text, "", "L", false)
	case exportBlockCode:
		pdf.SetFillColor(245, 245, 245)
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, block.text, "1", "L", true)
		pdf.Ln(2)
	case exportBlockImage:
		parts := strings.SplitN(block.text, "|", 2)
		pdf.SetFillColor(245, 245, 245)
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, "Image: "+parts[0]+" Source: "+parts[1], "1", "L", true)
		pdf.Ln(2)
	case exportBlockFormula:
		pdf.SetFont("cjk", "", 10)
		pdf.MultiCell(0, 6, "Formula: "+block.text, "1", "L", false)
		pdf.Ln(2)
	case exportBlockTable:
		pdfTable(pdf, block.rows)
	default:
		pdf.SetFont("cjk", "", 10)
		pdf.MultiCell(0, 6, block.text, "", "L", false)
	}
}

func pdfTable(pdf *fpdf.Fpdf, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return
	}
	pageWidth, _ := pdf.GetPageSize()
	width := (pageWidth - pdf.GetX()*2) / float64(columns)
	for index, row := range rows {
		pdf.SetFont("cjk", "", 9)
		pdf.SetFillColor(240, 240, 240)
		for columnIndex := 0; columnIndex < columns; columnIndex++ {
			text := ""
			if columnIndex < len(row) {
				text = row[columnIndex]
			}
			style := "1"
			alignment := "L"
			fill := index == 0
			pdf.CellFormat(width, 6, text, style, 0, alignment, fill, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(2)
}

// executionStep is the sanitized view of one Agent Timeline entry. Only the
// doc/100 "safe summary" fields survive parsing; tool arguments, authorization
// context and any unknown key are dropped before rendering (doc/125 §4.1).
type executionStep struct {
	label      string
	summary    string
	status     string
	durationMs int64
}

const (
	executionStepStatusSuccess = "success"
	executionStepStatusFailed  = "failed"
	executionStepStatusRunning = "running"
)

func executionStepSymbol(status string) string {
	switch status {
	case executionStepStatusSuccess:
		return "✅"
	case executionStepStatusRunning:
		return "⏳"
	default:
		return "❌"
	}
}

func truncateExecutionRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return strings.TrimSpace(value)
	}
	return string(runes[:limit])
}

// parseExecutionSteps normalizes snapshot.ExecutionJSON into timeline steps.
// Malformed payloads degrade to nil so observation can never break delivery:
// the export simply renders without a Timeline section (doc/125 §4.3).
func parseExecutionSteps(raw string) []executionStep {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil
	}
	steps := make([]executionStep, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		label := truncateExecutionRunes(stringFromAny(entry["label"]), 128)
		if label == "" {
			label = truncateExecutionRunes(stringFromAny(entry["name"]), 128)
		}
		if label == "" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(stringFromAny(entry["status"])))
		switch status {
		case executionStepStatusSuccess, executionStepStatusRunning:
		default:
			// Unknown and failed statuses render as failed: conservative by
			// design so a partial run never looks finished.
			status = executionStepStatusFailed
		}
		step := executionStep{label: label, status: status}
		if duration, ok := entry["duration_ms"].(float64); ok && duration >= 0 {
			step.durationMs = int64(duration)
		}
		step.summary = truncateExecutionRunes(stringFromAny(entry["summary"]), 256)
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil
	}
	return steps
}

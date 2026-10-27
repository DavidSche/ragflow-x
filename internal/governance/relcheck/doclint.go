package relcheck

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// LintIssue is one document engineering finding (doc 137 §32 P2-8).
type LintIssue struct {
	Line int    `json:"line"`
	Rule string `json:"rule"`
	What string `json:"what"`
}

// DocLintResult aggregates the lint findings for one document.
type DocLintResult struct {
	Path           string      `json:"path"`
	TableCount     int         `json:"table_count"`
	SectionRefs    int         `json:"section_refs"`
	Anchors        int         `json:"anchors"`
	AnchorsSkipped int         `json:"anchors_skipped"`
	Issues         []LintIssue `json:"issues"`
}

func (r *DocLintResult) add(line int, rule, what string) {
	r.Issues = append(r.Issues, LintIssue{Line: line, Rule: rule, What: what})
}

var (
	sectionRefPattern = regexp.MustCompile(`第 ?(\d+(?:\.\d+)*) ?节|§(\d+(?:\.\d+)*)`)
	headingPattern    = regexp.MustCompile(`^#{2,4}\s+(\d+(?:\.\d+)*)\.?\s`)
	anchorPattern     = regexp.MustCompile("`([A-Za-z0-9_./\\-]+\\.(?:go|ts|tsx))(?::(\\d+))?`")
)

// resolveAnchorPath maps an anchor path to an absolute filesystem path.
//   - `ragflow-x/...` anchors refer to this repository root;
//   - `ragflow/...` anchors refer to the sibling upstream ragflow worktree;
//   - everything else is repository-root relative.
func resolveAnchorPath(root, raw string) string {
	switch {
	case strings.HasPrefix(raw, "ragflow-x/"):
		return filepath.Join(root, strings.TrimPrefix(raw, "ragflow-x/"))
	case strings.HasPrefix(raw, "ragflow/"):
		return filepath.Join(filepath.Dir(root), raw)
	default:
		return filepath.Join(root, raw)
	}
}

// siblingWorktreeExists reports whether the sibling upstream ragflow worktree
// used for `ragflow/...` anchors is present next to this repository.
func siblingWorktreeExists(root string) bool {
	info, err := os.Stat(filepath.Join(filepath.Dir(root), "ragflow"))
	return err == nil && info.IsDir()
}

type tableLine struct {
	no   int
	text string
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	// GFM tables: an escaped pipe (\\|) never splits a cell, even inside code
	// spans. Substitute before splitting and restore afterwards.
	pipePlaceholder := "\x00"
	trimmed = strings.ReplaceAll(trimmed, "\\|", pipePlaceholder)
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], pipePlaceholder, "|")
	}
	return parts
}

func lintTable(result *DocLintResult, lines []tableLine) {
	if len(lines) < 2 {
		return
	}
	result.TableCount++
	headerCols := len(splitTableRow(lines[0].text))
	for _, line := range lines[1:] {
		cols := len(splitTableRow(line.text))
		if cols != headerCols {
			result.add(line.no, "table_columns",
				fmt.Sprintf("表格列数 %d 与表头 %d 不一致", cols, headerCols))
		}
	}
}

// collectHeadings indexes the numbered headings of one markdown file.
func collectHeadings(path string, into map[string]bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)
	for scanner.Scan() {
		if match := headingPattern.FindStringSubmatch(scanner.Text()); match != nil {
			into[match[1]] = true
		}
	}
	return scanner.Err()
}

func countLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}

// LintDocument validates one markdown contract document against the repo root:
//   - every pipe table has consistent column counts across header and rows;
//   - every “第 X(.Y) 节” / “§X.Y” reference resolves to an existing numbered
//     heading in this document or in extraHeadingFiles (cross-document refs);
//   - every `path:line` code anchor resolves to an existing file whose line is
//     inside the current file bounds (±50 line semantic tolerance still needs a
//     human pass; drift is corrected at final review, doc 137 §32 P2-1).
func LintDocument(root, path string, extraHeadingFiles ...string) (*DocLintResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	result := &DocLintResult{Path: path, Issues: []LintIssue{}}
	headings := map[string]bool{}
	for _, extra := range extraHeadingFiles {
		if err := collectHeadings(extra, headings); err != nil {
			return nil, err
		}
	}
	var tableLines []tableLine
	var sectionRefs []struct {
		no     int
		number string
	}
	var anchors []struct {
		no   int
		raw  string
		line int
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		for _, match := range headingPattern.FindAllStringSubmatch(line, 1) {
			headings[match[1]] = true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			tableLines = append(tableLines, tableLine{no: lineNo, text: line})
			continue
		}
		if len(tableLines) > 0 {
			lintTable(result, tableLines)
			tableLines = nil
		}
		for _, match := range sectionRefPattern.FindAllStringSubmatch(line, -1) {
			number := match[1]
			if number == "" {
				number = match[2]
			}
			sectionRefs = append(sectionRefs, struct {
				no     int
				number string
			}{lineNo, number})
		}
		for _, match := range anchorPattern.FindAllStringSubmatch(line, -1) {
			lineNumber := 0
			if match[2] != "" {
				lineNumber, _ = strconv.Atoi(match[2])
			}
			anchors = append(anchors, struct {
				no   int
				raw  string
				line int
			}{lineNo, match[1], lineNumber})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(tableLines) > 0 {
		lintTable(result, tableLines)
	}

	result.SectionRefs = len(sectionRefs)
	for _, ref := range sectionRefs {
		if !headings[ref.number] {
			result.add(ref.no, "section_ref", "未解析的章节引用：第 "+ref.number+" 节")
		}
	}
	result.Anchors = len(anchors)
	for _, anchor := range anchors {
		full := resolveAnchorPath(root, anchor.raw)
		info, err := os.Stat(full)
		switch {
		case err != nil && strings.HasPrefix(anchor.raw, "ragflow/") && !siblingWorktreeExists(root):
			// The `ragflow/...` anchors verify upstream code that only exists
			// in the sibling upstream worktree (developers' single-workspace
			// checkout). CI checks out ragflow-x alone, so the sibling is
			// unavailable there; degrade to an explicit soft-skip instead of
			// a hard lint failure. When the sibling is present the strict
			// existence check below still applies.
			result.AnchorsSkipped++
		case err != nil:
			result.add(anchor.no, "anchor_missing", "锚点文件不存在："+anchor.raw)
		case info.IsDir():
			// Directory anchors (whole-file facts) only need existence.
		case anchor.line > 0:
			if bound := countLines(full); anchor.line > bound {
				result.add(anchor.no, "anchor_out_of_bounds",
					fmt.Sprintf("锚点 %s:%d 超出文件行数 %d", anchor.raw, anchor.line, bound))
			}
		}
	}
	return result, nil
}

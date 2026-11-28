package engdocs

// ScenarioID: SC-DOC-006
//
// doc/specification/00 §5 tells authors to cite `file.go` plus a symbol name, never
// `file.go:<line>`. `TestDocAnchorsDoNotGrow` freezes the *count* of legacy
// line anchors per document, so a review record cannot add more of them. What was
// still missing is the other direction: nothing checked that a symbol anchor
// **resolves**. A record that says `doc/develop/304…::ReportQuoteStatusUnreadable`
// reads as verified evidence whether or not that function still exists — and after
// a rename or a file move it becomes a citation to nothing, in a document whose
// whole purpose is to be checkable a year later.
//
// This gate defines the machine-checkable form and enforces it:
//
//	`internal/service/report_assurance.go::ReportQuoteStatusUnreadable`
//
// For every such anchor in the engineering documents (doc/specification, doc/develop):
//  1. the path resolves to a file;
//  2. the symbol is declared in that file;
//  3. the declaration is unique — two declarations mean the anchor no longer says
//     which one the document meant, which is the same defect as a line number.
//
// It does **not** rewrite the 1 100+ legacy anchors. Append-only documents cannot
// be retrofitted, and a gate that starts red on day one is a gate that gets
// deleted. The floor below is the opposite guard: the form must be *used*, or a
// green run only proves that nobody writes anchors at all.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// symbolAnchorFloor is the number of resolvable `path.go::Symbol` anchors the
// documents must contain. It exists because "zero problems" and "zero anchors" are
// the same green: raise it when you add anchors, never lower it.
//
// 34 on 2026-11-28: 21 pre-existing (mostly test names in doc/develop 288/296/297/
// 316/324 and specification 13) + 13 added with doc/develop/328 and specification
// 16 §7. A drop here means an anchor was deleted rather than fixed.
const symbolAnchorFloor = 34

var symbolAnchorPattern = regexp.MustCompile("`([A-Za-z0-9_./-]+\\.go)::([A-Za-z_][A-Za-z0-9_]*)`")

// symbolDeclarationPatterns are the declaration shapes an anchor may name. Grouped
// `const (` blocks are included because that is where the closed-set tokens live,
// and those are the values a review record most often needs to cite.
func symbolDeclarationPatterns(symbol string) []*regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	return []*regexp.Regexp{
		regexp.MustCompile(`(?m)^func\s+(\([^)]*\)\s+)?` + quoted + `\s*[\(\[]`),
		regexp.MustCompile(`(?m)^type\s+` + quoted + `\s`),
		regexp.MustCompile(`(?m)^\s*(const|var)\s+` + quoted + `\b`),
		regexp.MustCompile(`(?m)^\s*` + quoted + `\s*(=|[A-Za-z_*\[]|\s+[A-Za-z_])`),
	}
}

// symbolDeclarationCount reports how many declarations of `symbol` the file body
// contains. Counting (rather than "found or not") is what makes the ambiguity check
// possible: a name declared twice is a name an anchor cannot point at.
func symbolDeclarationCount(body, symbol string) int {
	count := 0
	for _, pattern := range symbolDeclarationPatterns(symbol) {
		count += len(pattern.FindAllString(body, -1))
	}
	return count
}

// symbolAnchorCandidates resolves the file part of an anchor.
//
// Documents cite `service/report_archive_test.go` and `gateway.go` as often as they
// cite a repo-relative path, and an append-only archive cannot be retrofitted to
// one style. So the path is matched by suffix and the *anchor as a whole* must
// identify exactly one declaration: a short path is acceptable when only one file
// matches, and a short path that matches two files is reported unless exactly one
// of them declares the symbol. The alternative — demanding repo-relative paths —
// would start this gate red on documents nobody is allowed to edit.
func symbolAnchorCandidates(index []string, anchorPath string) []string {
	for _, candidate := range index {
		if candidate == anchorPath {
			return []string{anchorPath}
		}
	}
	suffix := "/" + anchorPath
	var matches []string
	for _, candidate := range index {
		if strings.HasSuffix(candidate, suffix) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// resolveSymbolAnchor is the checker, split out so the negative cases are ordinary
// unit tests instead of a temporary rename nobody repeats.
func resolveSymbolAnchor(read func(path string) (string, bool), index []string, anchorPath, symbol string) string {
	candidates := symbolAnchorCandidates(index, anchorPath)
	if len(candidates) == 0 {
		return "path " + anchorPath + " does not exist"
	}
	declarations := 0
	var declaring []string
	for _, candidate := range candidates {
		body, ok := read(candidate)
		if !ok {
			continue
		}
		if count := symbolDeclarationCount(body, symbol); count > 0 {
			declarations += count
			declaring = append(declaring, candidate)
		}
	}
	switch {
	case declarations == 0:
		return "symbol " + symbol + " is not declared in " + strings.Join(candidates, " / ")
	case declarations > 1:
		return "symbol " + symbol + " is declared " + strconv.Itoa(declarations) + " times across " +
			strings.Join(declaring, " / ") + "; the anchor does not say which"
	}
	return ""
}

// TestSymbolAnchorCheckerHasAFailureDirection proves all four outcomes before the
// checker is applied to the real documents.
func TestSymbolAnchorCheckerHasAFailureDirection(t *testing.T) {
	sources := map[string]string{
		"internal/service/report_assurance.go": `
func ReportQuoteStatusUnreadable(status string) bool { return false }

const (
	ReportQuoteStatusVerified = "verified"
)

type ReportAssuranceRun struct{}

// Duplicated exists twice on purpose: an anchor must not resolve to an ambiguous name.
func Duplicated() {}
func Duplicated() {}
`,
	}
	read := func(path string) (string, bool) {
		body, ok := sources[path]
		return body, ok
	}
	index := []string{"internal/service/report_assurance.go"}

	cases := []struct {
		name    string
		file    string
		symbol  string
		wantSub string
	}{
		{name: "method resolves", file: "internal/service/report_assurance.go", symbol: "ReportQuoteStatusUnreadable"},
		{name: "grouped const resolves", file: "internal/service/report_assurance.go", symbol: "ReportQuoteStatusVerified"},
		{name: "type resolves", file: "internal/service/report_assurance.go", symbol: "ReportAssuranceRun"},
		{name: "short path resolves by suffix", file: "service/report_assurance.go", symbol: "ReportQuoteStatusUnreadable"},
		{name: "missing file", file: "internal/service/gone.go", symbol: "ReportQuoteStatusUnreadable", wantSub: "does not exist"},
		{name: "missing symbol", file: "internal/service/report_assurance.go", symbol: "ReportQuoteStatusGone", wantSub: "is not declared"},
		{name: "ambiguous symbol", file: "internal/service/report_assurance.go", symbol: "Duplicated", wantSub: "declared 2 times"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := resolveSymbolAnchor(read, index, testCase.file, testCase.symbol)
			if testCase.wantSub == "" {
				if got != "" {
					t.Fatalf("unexpected problem: %s", got)
				}
				return
			}
			if !strings.Contains(got, testCase.wantSub) {
				t.Fatalf("problem %q does not mention %q", got, testCase.wantSub)
			}
		})
	}
}

// TestDocumentSymbolAnchorsResolve applies the checker to every engineering
// document. The anchor set must be non-empty and at least `symbolAnchorFloor`, or a
// green run means "no anchors were written" instead of "the anchors are valid".
func TestDocumentSymbolAnchorsResolve(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	// The anchor sources are the engineering docs, which are not part of the published
	// repository (engdocs.Present). Reading a shipped tree instead would either fail here or —
	// worse — pass on an empty scan, so the absence degrades to a named skip.
	if !Present(root) {
		t.Skip(SkipReason)
	}
	readFile := func(relative string) (string, bool) {
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			return "", false
		}
		return string(body), true
	}

	documents := map[string]string{}
	for _, directory := range []string{"doc/specification", "doc/develop"} {
		_ = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(directory)), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			documents[filepath.ToSlash(relative)] = string(body)
			return nil
		})
	}
	if len(documents) == 0 {
		t.Fatal("no engineering documents found: the scanner is blind")
	}

	type anchor struct {
		Document string
		File     string
		Symbol   string
	}
	var anchors []anchor
	seen := map[anchor]bool{}
	for document, body := range documents {
		for _, match := range symbolAnchorPattern.FindAllStringSubmatch(body, -1) {
			found := anchor{Document: document, File: match[1], Symbol: match[2]}
			if seen[found] {
				continue
			}
			seen[found] = true
			anchors = append(anchors, found)
		}
	}
	sort.Slice(anchors, func(i, j int) bool {
		if anchors[i].Document != anchors[j].Document {
			return anchors[i].Document < anchors[j].Document
		}
		return anchors[i].File+anchors[i].Symbol < anchors[j].File+anchors[j].Symbol
	})

	if len(anchors) < symbolAnchorFloor {
		t.Fatalf("only %d symbol anchors found, floor is %d: the form must be used, not merely permitted",
			len(anchors), symbolAnchorFloor)
	}

	// One index of every Go file in the repo; anchors are resolved against it.
	var goIndex []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == "node_modules" || name == ".git" || name == "vendor" || name == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		goIndex = append(goIndex, filepath.ToSlash(relative))
		return nil
	})
	if len(goIndex) < 100 {
		t.Fatalf("Go file index has %d entries: the scanner is blind", len(goIndex))
	}

	var problems []string
	for _, found := range anchors {
		if problem := resolveSymbolAnchor(readFile, goIndex, found.File, found.Symbol); problem != "" {
			problems = append(problems, found.Document+": "+problem)
		}
	}
	if len(problems) > 0 {
		t.Fatalf("symbol anchors do not resolve (doc/specification/00 §5):\n  %s", strings.Join(problems, "\n  "))
	}
}

// specTestNamePattern is the other way a living document cites a symbol: a bare
// “ `TestSomething` “ with no file, which is how §7 of spec 16 names the case that guards
// each contract seam. That form carries no path, so the anchor gate above never sees it — and
// a spec that names a renamed test is the same defect one step earlier: the reader clicks the
// name, finds nothing, and stops trusting the table.
//
// The scan is deliberately limited to `doc/specification`: the engineering archive is
// append-only, so an old round record naming a test that has since been renamed is *history*,
// not an error. Living specs have no such excuse.
var specTestNamePattern = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// specTestNameFloor keeps "zero names" from reading as "zero problems".
const specTestNameFloor = 40

// TestSpecTestNamesResolve requires every test name a living specification cites to exist.
func TestSpecTestNamesResolve(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	// Same boundary as the anchor gate above: the living specs (doc/specification/<NNN>_*.md)
	// are local-only, only doc/specification/api/api-reference.* ships.
	if !Present(root) {
		t.Skip(SkipReason)
	}

	var goSources strings.Builder
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return walkErr
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		goSources.Write(body)
		goSources.WriteString("\n")
		return nil
	})
	declared := goSources.String()
	if len(declared) < 100000 {
		t.Fatalf("read only %d bytes of Go source: the declaration index is blind", len(declared))
	}

	type mention struct {
		Document string
		Name     string
	}
	var mentions []mention
	seen := map[mention]bool{}
	specification := filepath.Join(root, "doc", "specification")
	_ = filepath.WalkDir(specification, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return walkErr
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, match := range specTestNamePattern.FindAllStringSubmatch(string(body), -1) {
			found := mention{Document: filepath.ToSlash(relative), Name: match[1]}
			if seen[found] {
				continue
			}
			seen[found] = true
			mentions = append(mentions, found)
		}
		return nil
	})
	if len(mentions) < specTestNameFloor {
		t.Fatalf("found %d test names across doc/specification, floor is %d: the scanner or the citation habit went blind",
			len(mentions), specTestNameFloor)
	}

	var unresolved []string
	for _, item := range mentions {
		if strings.Contains(declared, "func "+item.Name+"(") {
			continue
		}
		unresolved = append(unresolved, item.Document+" names "+item.Name)
	}
	sort.Strings(unresolved)
	if len(unresolved) > 0 {
		t.Fatalf("%d test name(s) a living specification cites do not exist — the reader follows the name into nothing:\n  %s",
			len(unresolved), strings.Join(unresolved, "\n  "))
	}
}

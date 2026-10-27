package relcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Report is the REL-003 validation report written next to the evidence.
type Report struct {
	GeneratedBy    string              `json:"generated_by"`
	PlanDocLint    *DocLintResult      `json:"plan_doc_lint"`
	StatusDocLint  *DocLintResult      `json:"status_doc_lint"`
	ReviewItems    []ReviewCheckResult `json:"review_items"`
	GateStats      *GateStats          `json:"gate_stats"`
	PrivacyScan    *PrivacyScanResult  `json:"privacy_scan"`
	Governance     []GovernanceCheck   `json:"governance_checks"`
	VersionBand    *VersionBandSummary `json:"version_band,omitempty"`
	FailedMappings []string            `json:"failed_mappings,omitempty"`
	Pass           bool                `json:"pass"`
}

var testSymbolPattern = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// collectTestSymbols indexes every regression test symbol under internal/ and
// cmd/ so the registry can be verified without running the suites.
func collectTestSymbols(t *testing.T, root string) map[string]bool {
	t.Helper()
	symbols := map[string]bool{}
	roots := []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")}
	for _, dir := range roots {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, match := range testSymbolPattern.FindAllStringSubmatch(string(raw), -1) {
				symbols[match[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return symbols
}

// crossDocUniverse returns the markdown files that a status document may cite:
// doc 137, ragflow-upstream-issues.md and every milestone doc named in it.
func crossDocUniverse(root, statusPath string) ([]string, error) {
	raw, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, err
	}
	named := milestoneDocPattern.FindAllStringSubmatch(string(raw), -1)
	files := []string{
		filepath.Join(root, "doc", "develop", "137_知识编译与Memory无声落地实施方案.md"),
		filepath.Join(root, "doc", "develop", "ragflow-upstream-issues.md"),
	}
	seen := map[string]bool{}
	for _, match := range named {
		full := filepath.Join(root, "doc", "develop", match[1]+".md")
		if !seen[full] && fileExists(full) {
			seen[full] = true
			files = append(files, full)
		}
	}
	return files, nil
}

var milestoneDocPattern = regexp.MustCompile("`(\\d{3})-[A-Za-z0-9\\p{Han}\\p{P}]+?\\.md`")

func verifyReviewItems(t *testing.T, root string, symbols map[string]bool) []ReviewCheckResult {
	t.Helper()
	planPath, _ := docPaths(root)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan doc: %v", err)
	}
	plan := string(raw)
	results := make([]ReviewCheckResult, 0, 32)
	for _, item := range ReviewRegistry() {
		result := ReviewCheckResult{ReviewItem: item}
		// Layer 1: the mapping row must exist inside the corresponding §section.
		if sectionText(plan, item.Section) != "" && strings.Contains(sectionText(plan, item.Section), item.ID) {
			result.MappingRowFound = true
		}
		// Layer 2: representative regression symbols must exist.
		for _, symbol := range item.TestSymbols {
			if !symbols[symbol] {
				result.MissingTests = append(result.MissingTests, symbol)
			}
		}
		// Layer 3: closure documents must exist.
		for _, doc := range item.MilestoneDocs {
			if !fileExists(filepath.Join(root, "doc", "develop", doc)) {
				result.MissingDocs = append(result.MissingDocs, doc)
			}
		}
		results = append(results, result)
	}
	return results
}

var sectionHeadingPattern = regexp.MustCompile(`^## (\d+)\. `)

// sectionText extracts the mapping section body for a given section number.
func sectionText(plan string, section int) string {
	want := itoa(section)
	var body []string
	inSection := false
	for _, line := range strings.Split(plan, "\n") {
		if match := sectionHeadingPattern.FindStringSubmatch(line); match != nil {
			inSection = match[1] == want
			if inSection {
				body = body[:0]
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inSection = false
		}
		if inSection {
			body = append(body, line)
		}
	}
	return strings.Join(body, "\n")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// TestREL3ExternalReviewGate runs the REL-003 audit end to end and persists the
// JSON evidence report. It fails when any review mapping loses its regression
// evidence, when document engineering lint finds new issues, or when a
// governance gate regresses.
func TestREL3ExternalReviewGate(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	symbols := collectTestSymbols(t, root)

	planPath, statusPath := docPaths(root)
	planLint, err := LintDocument(root, planPath)
	if err != nil {
		t.Fatalf("lint plan doc: %v", err)
	}
	// The status doc references sections of doc 137, the upstream-issues ledger
	// and the milestone docs it cites via “详见 `NNN-...`”; build the
	// cross-document resolution universe from those files.
	statusUniverse, err := crossDocUniverse(root, statusPath)
	if err != nil {
		t.Fatalf("status doc universe: %v", err)
	}
	statusLint, err := LintDocument(root, statusPath, statusUniverse...)
	if err != nil {
		t.Fatalf("lint status doc: %v", err)
	}

	reviewResults := verifyReviewItems(t, root, symbols)
	gateStats := CollectGateStats(root)
	privacy, err := ScanPrivacyRedLines(root)
	if err != nil {
		t.Fatalf("privacy scan: %v", err)
	}
	governance := RunGovernanceChecks(root, symbols)
	versionBand := CollectVersionBandGap()

	report := Report{
		GeneratedBy:    "relcheck REL-003 (doc 137 §14 / §32 P2-1 P2-8)",
		PlanDocLint:    planLint,
		StatusDocLint:  statusLint,
		ReviewItems:    reviewResults,
		GateStats:      gateStats,
		PrivacyScan:    privacy,
		Governance:     governance,
		VersionBand:    &versionBand,
		FailedMappings: []string{},
		Pass:           true,
	}

	for _, item := range reviewResults {
		if !item.Pass() {
			report.Pass = false
			reason := item.Review + " " + item.ID
			if !item.MappingRowFound {
				reason += " (mapping row missing)"
			}
			reason += strings.Join(item.MissingTests, ",")
			reason += strings.Join(item.MissingDocs, ",")
			report.FailedMappings = append(report.FailedMappings, reason)
		}
	}
	if len(planLint.Issues) > 0 || len(statusLint.Issues) > 0 {
		report.Pass = false
	}
	if len(privacy.Violations) > 0 {
		report.Pass = false
	}
	for _, check := range governance {
		if !check.Pass {
			report.Pass = false
		}
	}
	if !versionBand.Pass {
		report.Pass = false
	}

	outDir := filepath.Join(root, "test-results")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "rel3-external-review-validation.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}

	if !report.Pass {
		encoded, _ := json.MarshalIndent(report, "", "  ")
		t.Fatalf("REL-003 validation failed:\n%s", encoded)
	}
	if report.VersionBand == nil || !report.VersionBand.Pass || len(report.VersionBand.GapEndpoints) != 0 {
		t.Fatalf("REL-003 version-band gate must be clean after T6.2/rc1: %+v", report.VersionBand)
	}
}

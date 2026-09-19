package testquality

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChangedCoverageParsesProfiles(t *testing.T) {
	goReport := parseGoCoverage([]string{
		"mode: set",
		"internal/service/example.go:10.0,12.0 2 1",
		"internal/service/example.go:20.0,21.0 1 0",
	})
	if _, covered := goReport["internal/service/example.go"][11]; !covered {
		t.Fatal("expected line 11 to be covered")
	}
	if _, covered := goReport["internal/service/example.go"][20]; covered {
		t.Fatal("expected line 20 to be uncovered")
	}

	viteReport, err := parseViteCoverage([]byte(`{"web/src/example.ts":{"lines":{"hits":{"10":1,"11":0}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, covered := viteReport["web/src/example.ts"][10]; !covered {
		t.Fatal("expected Vite line 10 to be covered")
	}
	if _, covered := viteReport["web/src/example.ts"][11]; covered {
		t.Fatal("expected Vite line 11 to be uncovered")
	}

	viteIstanbul, err := parseViteCoverage([]byte(`{
		"web/src/example.ts": {
			"statementMap": {
				"0": {"start":{"line":10},"end":{"line":12}},
				"1": {"start":{"line":20},"end":{"line":20}}
			},
			"s": {"0":1,"1":0}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, covered := viteIstanbul["web/src/example.ts"][11]; !covered {
		t.Fatal("expected Istanbul line 11 to be covered")
	}
	if _, covered := viteIstanbul["web/src/example.ts"][20]; covered {
		t.Fatal("expected Istanbul line 20 to be uncovered")
	}

	relative, relativeErr := repositoryRelativePath("../..", modulePathFromRepository("../..")+"/internal/service/example.go")
	if relativeErr != nil || relative != "internal/service/example.go" {
		t.Fatalf("expected Go module path to map to repository-relative path, got %q, %v", relative, relativeErr)
	}
}

func TestChangedCoverageParsesDiffLineNumbers(t *testing.T) {
	changed := parseGitDiffLines([]byte(`diff --git a/internal/service/example.go b/internal/service/example.go
--- a/internal/service/example.go
+++ b/internal/service/example.go
@@ -10,0 +11,2 @@
+first
+second
@@ -20,0 +21 @@
+third
`))
	lines := changed["internal/service/example.go"]
	if _, exists := lines[11]; !exists {
		t.Fatal("expected line 11 in changed lines")
	}
	if _, exists := lines[12]; !exists {
		t.Fatal("expected line 12 in changed lines")
	}
	if _, exists := lines[21]; !exists {
		t.Fatal("expected line 21 in changed lines")
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 changed lines, got %d", len(lines))
	}
}

func TestChangedCoverageFiltersNonExecutableLines(t *testing.T) {
	nonExecutableGo := []string{
		"",
		"package service",
		"// ScenarioID: SC-TEST-001",
		"/* block comment */",
		"import \"errors\"",
		"import (",
		"\"errors\"",
	}
	for _, line := range nonExecutableGo {
		if isExecutableChangedLine("internal/service/example.go", line) {
			t.Fatalf("expected non-executable Go line %q to be filtered", line)
		}
	}
	if !isExecutableChangedLine("internal/service/example.go", "return true") {
		t.Fatal("expected executable Go line to be retained")
	}

	nonExecutableTS := []string{
		"",
		"// comment",
		"import { useEffect } from \"react\"",
		"import type { Foo } from \"./foo\"",
		"export type Result = string",
		"export interface Props {}",
	}
	for _, line := range nonExecutableTS {
		if isExecutableChangedLine("web/src/example.tsx", line) {
			t.Fatalf("expected non-executable TypeScript line %q to be filtered", line)
		}
	}
	if !isExecutableChangedLine("web/src/example.tsx", "return 42") {
		t.Fatal("expected executable TypeScript line to be retained")
	}
}

func TestChangedCoverageExcludesTestAssetsFromExecutableDenominator(t *testing.T) {
	changed := map[string]map[int]string{
		"internal/service/example.go":           {10: "return true"},
		"internal/service/example_test.go":      {10: "if got != want {"},
		"web/src/resources/example.tsx":         {10: "return <div />"},
		"web/src/resources/example.test.tsx":    {10: "expect(screen).toBeInTheDocument()"},
		"web/e2e/governance-navigation.spec.ts": {10: "await page.goto('/alerts/deliveries')"},
	}
	covered := map[string]map[int]struct{}{
		"internal/service/example.go": {10: {}},
	}

	files := buildChangedCoverageFiles(".", changed, covered)
	if len(files) != 2 {
		t.Fatalf("expected only production files, got %d: %+v", len(files), files)
	}
	for _, file := range files {
		if file.Path != "internal/service/example.go" && file.Path != "web/src/resources/example.tsx" {
			t.Fatalf("unexpected file %q", file.Path)
		}
	}
}

func TestChangedCoverageIncludesUntrackedSourceLines(t *testing.T) {
	repositoryDir := t.TempDir()
	if output, err := exec.Command("git", "-C", repositoryDir, "init").CombinedOutput(); err != nil {
		t.Fatalf("init git repository: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repositoryDir, "service.go"), []byte("func run() {\n\treturn\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryDir, "service_test.go"), []byte("func TestRun() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryDir, "asset.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := map[string]map[int]string{}
	if err := appendUntrackedChangedLines(repositoryDir, changed); err != nil {
		t.Fatal(err)
	}
	if len(changed["service.go"]) != 3 {
		t.Fatalf("expected all source lines, got %+v", changed["service.go"])
	}
	if _, exists := changed["service_test.go"]; exists {
		t.Fatal("test asset must not enter changed production denominator")
	}
	if _, exists := changed["asset.bin"]; exists {
		t.Fatal("binary asset must not be parsed as source")
	}
}

func TestChangedCoverageRiskAttributionPrefersP0(t *testing.T) {
	catalogue := ScenarioCatalogue{Scenarios: []Scenario{
		{ID: "SC-TEST-001", Risk: coverageRiskP0, MappedFiles: []string{"internal/service/shared.go"}},
		{ID: "SC-TEST-002", Risk: coverageRiskP1, MappedFiles: []string{"internal/service/shared.go"}},
		{ID: "SC-TEST-003", Risk: coverageRiskP1, MappedFiles: []string{"internal/service/other.go"}},
	}}
	files := []ChangedCoverageFile{
		{Path: "internal/service/shared.go"},
		{Path: "internal/service/other.go"},
	}
	assignChangedCoverageRisk(files, catalogue.RiskCountByFile(), catalogue.ScenarioIDsByFile(), map[string]bool{coverageRiskP0: true, coverageRiskP1: true})
	if files[0].Risk != coverageRiskP0 {
		t.Fatalf("expected shared file to stay P0, got %s", files[0].Risk)
	}
	if files[1].Risk != coverageRiskP1 {
		t.Fatalf("expected other file to be P1, got %s", files[1].Risk)
	}
}

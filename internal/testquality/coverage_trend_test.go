package testquality

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseGoCoverageStatements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go.out")
	content := strings.Join([]string{
		"mode: set",
		"github.com/ragflow-x/ragflow-x/internal/service/example.go:10.0,12.0 2 1",
		"github.com/ragflow-x/ragflow-x/internal/service/example.go:20.0,22.0 2 0",
	}, "\n")
	if err := osWriteFile(path, []byte(content)); err != nil {
		t.Fatal(err)
	}

	files, err := parseGoCoverageStatements(path, "../..")
	if err != nil {
		if !strings.Contains(err.Error(), "coverage regressed") {
			t.Fatalf("expected coverage regression error, got %v", err)
		}
	}
	if len(files) != 1 {
		t.Fatalf("expected one file, got %+v", files)
	}
	file := files[0]
	if file.Source != "go" || file.Path != "internal/service/example.go" || file.Statements != 4 || file.CoveredStatements != 2 {
		t.Fatalf("unexpected Go statements: %+v", file)
	}
}

func TestParseGoCoverageStatementsDeduplicatesCrossPackageBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-cross-package.out")
	block := "github.com/ragflow-x/ragflow-x/internal/service/example.go:10.0,12.0 2 1"
	content := strings.Join([]string{
		"mode: atomic",
		block,
		block,
		"github.com/ragflow-x/ragflow-x/internal/service/example.go:10.0,12.0 2 0",
	}, "\n")
	if err := osWriteFile(path, []byte(content)); err != nil {
		t.Fatal(err)
	}

	files, err := parseGoCoverageStatements(path, "../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Statements != 2 || files[0].CoveredStatements != 2 {
		t.Fatalf("expected one deduplicated covered block, got %+v", files)
	}
}

func TestParseViteCoverageStatements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vite.json")
	content := `{
		"C:/repo/web/src/example.ts": {
			"statementMap": {"0": {}, "1": {}, "2": {}},
			"s": {"0": 1, "1": 0, "2": 3}
		}
	}`
	if err := osWriteFile(path, []byte(content)); err != nil {
		t.Fatal(err)
	}

	files, err := parseViteCoverageStatements(path, "C:/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one file, got %+v", files)
	}
	file := files[0]
	if file.Source != "vitest" || file.Path != "web/src/example.ts" || file.Statements != 3 || file.CoveredStatements != 2 {
		t.Fatalf("unexpected Vite statements: %+v", file)
	}
}

func TestEvaluateCoverageTrendAggregatesTargetsAndRegression(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	goPath := filepath.Join(t.TempDir(), "go.out")
	vitePath := filepath.Join(t.TempDir(), "vite.json")
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	previousPath := filepath.Join(t.TempDir(), "previous.json")
	goContent := strings.Join([]string{
		"mode: set",
		"internal/service/example.go:10.0,12.0 3 1",
		"internal/service/example.go:20.0,21.0 1 1",
	}, "\n")
	viteContent := `{
		"E:/repo/web/src/lib/example.ts": {
			"statementMap": {"0": {}, "1": {}},
			"s": {"0": 1, "1": 1}
		}
	}`
	targetsContent := `{"version":1,"targets":[{"path":"internal/service","target":75},{"path":"web/src","target":80}]}`
	previous := CoverageTrendHistory{
		Version: 1,
		Snapshots: []CoverageTrendSnapshot{{
			GeneratedAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
			Total:       CoverageTrendScope{Path: "total", Statements: 4, CoveredStatements: 3, Coverage: 75},
			Scopes: []CoverageTrendScope{
				{Path: "internal/service", Statements: 4, CoveredStatements: 3, Coverage: 75, Target: 75, TargetPassed: true},
				{Path: "web/src", Statements: 0, Coverage: 0},
			},
		}},
	}
	if err := osWriteFile(goPath, []byte(goContent)); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(vitePath, []byte(viteContent)); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(targetsPath, []byte(targetsContent)); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(previousPath, mustJSON(previous)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCoverageTrend(CoverageTrendConfig{
		GoCoveragePath: goPath,
		FrontendPath:   vitePath,
		TargetsPath:    targetsPath,
		PreviousPath:   previousPath,
		GeneratedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected trend report to pass: %+v", report)
	}
	if report.Current.Total.Statements != 6 || report.Current.Total.CoveredStatements != 6 {
		t.Fatalf("unexpected total: %+v", report.Current.Total)
	}
	var service, frontend CoverageTrendScope
	for _, scope := range report.Current.Scopes {
		switch scope.Path {
		case "internal/service":
			service = scope
		case "web/src":
			frontend = scope
		}
	}
	if service.Coverage != 100 || !service.TargetPassed {
		t.Fatalf("unexpected service scope: %+v", service)
	}
	if frontend.Coverage != 100 || !frontend.TargetPassed {
		t.Fatalf("unexpected frontend scope: %+v", frontend)
	}
	if len(report.Deltas) != 3 {
		t.Fatalf("expected three deltas, got %+v", report.Deltas)
	}
}

func TestEvaluateCoverageTrendFailsOnRegression(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	goPath := filepath.Join(t.TempDir(), "go.out")
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	previousPath := filepath.Join(t.TempDir(), "previous.json")
	if err := osWriteFile(goPath, []byte("mode: set\ninternal/service/example.go:10.0,12.0 2 1\ninternal/service/example.go:20.0,21.0 1 0\n")); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(targetsPath, []byte(`{"version":1,"targets":[{"path":"internal/service","target":75}]}`)); err != nil {
		t.Fatal(err)
	}
	previous := CoverageTrendHistory{
		Version: 1,
		Snapshots: []CoverageTrendSnapshot{{
			GeneratedAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
			Total:       CoverageTrendScope{Path: "total", Statements: 3, CoveredStatements: 3, Coverage: 100},
			Scopes:      []CoverageTrendScope{{Path: "internal/service", Statements: 3, CoveredStatements: 3, Coverage: 100}},
		}},
	}
	if err := osWriteFile(previousPath, mustJSON(previous)); err != nil {
		t.Fatal(err)
	}

	report, _ := EvaluateCoverageTrend(CoverageTrendConfig{
		RepositoryDir:  ".",
		GoCoveragePath: goPath,
		TargetsPath:    targetsPath,
		PreviousPath:   previousPath,
		GeneratedAt:    now,
	})
	if report.Passed || len(report.Regressions) == 0 {
		t.Fatalf("expected coverage regression, got %+v", report)
	}
}

func TestEvaluateCoverageTrendToleratesMeasurementNoise(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	goPath := filepath.Join(t.TempDir(), "go.out")
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	previousPath := filepath.Join(t.TempDir(), "previous.json")
	if err := osWriteFile(goPath, []byte("mode: set\ninternal/service/example.go:10.0,12.0 3 1\ninternal/service/example.go:20.0,21.0 1 1\n")); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(targetsPath, []byte(`{"version":1,"targets":[{"path":"internal/service","target":75}]}`)); err != nil {
		t.Fatal(err)
	}
	previousCoverage := 75.04
	previous := CoverageTrendHistory{
		Version: 1,
		Snapshots: []CoverageTrendSnapshot{{
			GeneratedAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
			Total:       CoverageTrendScope{Path: "total", Statements: 4, CoveredStatements: 3, Coverage: previousCoverage},
			Scopes:      []CoverageTrendScope{{Path: "internal/service", Statements: 4, CoveredStatements: 3, Coverage: previousCoverage}},
		}},
	}
	if err := osWriteFile(previousPath, mustJSON(previous)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCoverageTrend(CoverageTrendConfig{
		RepositoryDir:  ".",
		GoCoveragePath: goPath,
		TargetsPath:    targetsPath,
		PreviousPath:   previousPath,
		GeneratedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Regressions) != 0 {
		t.Fatalf("expected 0.04%% fluctuation to pass, got %+v", report)
	}
}

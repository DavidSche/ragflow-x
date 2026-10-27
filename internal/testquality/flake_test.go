package testquality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateFlakesGoParserGroupsSameTest(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"run","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession"}`,
		`{"Action":"output","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession","Output":"    assertion failed\n"}`,
		`{"Action":"fail","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession","Elapsed":0.10}`,
		`{"Action":"pass","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession","Elapsed":0.20}`,
	}, "\n")
	report := evaluateTempFlakes(t, "go-test.json", input)

	if len(report.Tests) != 1 {
		t.Logf("DEBUG report errors=%#v attempts=%#v inputs=%#v", report.Errors, report.Tests, report.Inputs)
		t.Fatalf("expected one grouped test, got %d", len(report.Tests))
	}
	test := report.Tests[0]
	if test.TestName != "TestP0_AUTH_001_LoginAuditFailureRevokesSession" || test.Package != "internal/service" {
		t.Fatalf("unexpected identity: package=%s test=%s", test.Package, test.TestName)
	}
	if test.Status != flakeStatusSuspect {
		t.Fatalf("expected SUSPECTED_FLAKY, got %s", test.Status)
	}
	if test.Classification != flakeClassificationUnclassified || test.Owner != "Backend Engineer" {
		t.Fatalf("unexpected governance metadata: classification=%s owner=%s", test.Classification, test.Owner)
	}
	if report.Summary.P0ActiveFlake != 1 {
		t.Fatalf("expected P0 active flake, got %+v", report.Summary)
	}
	if report.Passed || !report.Failed {
		t.Fatalf("expected gate failure, got passed=%t failed=%t", report.Passed, report.Failed)
	}
}

func TestEvaluateFlakesGoParserKeepsStableFailure(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"run","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession"}`,
		`{"Action":"output","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession","Output":"    assertion failed\n"}`,
		`{"Action":"fail","Package":"internal/service","Test":"TestP0_AUTH_001_LoginAuditFailureRevokesSession","Elapsed":0.10}`,
	}, "\n")
	report := evaluateTempFlakes(t, "go-test.json", input)

	if len(report.Tests) != 1 || report.Tests[0].Status != flakeStatusFail {
		t.Fatalf("expected stable failure, got %+v", report.Tests)
	}
	if report.Summary.P0Fail != 1 || report.Summary.P0ActiveFlake != 1 {
		t.Fatalf("unexpected P0 summary: %+v", report.Summary)
	}
	if report.Tests[0].Classification != flakeClassificationStableFailure || report.Tests[0].Attempts[0].Message == "" {
		t.Fatalf("expected stable failure classification and message: %+v", report.Tests[0])
	}
}

func TestEvaluateFlakesGoParserNormalizesModulePackagePath(t *testing.T) {
	testName := "TestP0_AUTH_001_LoginAuditFailureRevokesSession"
	input := strings.Join([]string{
		`{"Action":"run","Package":"github.com/ragflow-x/ragflow-x/internal/service","Test":"` + testName + `"}`,
		`{"Action":"output","Package":"github.com/ragflow-x/ragflow-x/internal/service","Test":"` + testName + `","Output":"    assertion failed\n"}`,
		`{"Action":"fail","Package":"github.com/ragflow-x/ragflow-x/internal/service","Test":"` + testName + `","Elapsed":0.10}`,
	}, "\n")
	report := evaluateRepositoryTempFlakes(t, "go-test.json", input)

	if len(report.Tests) != 1 {
		t.Fatalf("expected one test, got %d", len(report.Tests))
	}
	test := report.Tests[0]
	if test.Package != "internal/service" || test.Risk != coverageRiskP0 || report.Summary.P0ActiveFlake != 1 {
		t.Fatalf("expected module path to map to catalogue P0 risk: %+v summary=%+v", test, report.Summary)
	}
}

func TestEvaluateFlakesGroupsSameTestAcrossReportFiles(t *testing.T) {
	testName := "TestP0_AUTH_001_LoginAuditFailureRevokesSession"
	first := strings.Join([]string{
		`{"Action":"run","Package":"internal/service","Test":"` + testName + `"}`,
		`{"Action":"fail","Package":"internal/service","Test":"` + testName + `"}`,
	}, "\n")
	second := strings.Join([]string{
		`{"Action":"run","Package":"internal/service","Test":"` + testName + `"}`,
		`{"Action":"pass","Package":"internal/service","Test":"` + testName + `"}`,
	}, "\n")
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "go-run-1.json")
	secondPath := filepath.Join(dir, "go-run-2.json")
	if err := os.WriteFile(firstPath, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	report, _ := EvaluateFlakes(FlakeConfig{
		CataloguePath: "testdata/scenario_catalogue.json",
		Inputs:        []string{firstPath, secondPath},
	})

	if len(report.Tests) != 1 || report.Tests[0].Status != flakeStatusSuspect {
		t.Fatalf("expected one suspected flake across reports, got %+v", report.Tests)
	}
	if len(report.Tests[0].Attempts) != 2 {
		t.Fatalf("expected two attempts, got %+v", report.Tests[0].Attempts)
	}
}

func TestEvaluateFlakesVitestParser(t *testing.T) {
	input := `{
		"testResults":[{
			"name":"C:/repo/web/src/example.test.ts",
			"assertionResults":[{
				"fullName":"example works",
				"title":"works",
				"status":"failed",
				"duration":12
			}]
		}]
	}`
	report := evaluateTempFlakes(t, "vitest.json", input)

	if len(report.Tests) != 1 {
		t.Fatalf("expected one test, got %d", len(report.Tests))
	}
	test := report.Tests[0]
	if test.Source != flakeSourceVitest || test.TestName != "example works" || test.Status != flakeStatusFail {
		t.Fatalf("unexpected Vitest test: %+v", test)
	}
	if test.Attempts[0].DurationMS != 12 {
		t.Fatalf("expected duration to be preserved, got %f", test.Attempts[0].DurationMS)
	}
}

func TestEvaluateFlakesPlaywrightParser(t *testing.T) {
	input := `{
		"suites":[{
			"title":"smoke",
			"file":"smoke.spec.ts",
			"specs":[{
				"title":"can access project",
				"tests":[{
					"status":"unexpected",
					"results":[{"status":"unexpected","duration":100}]
				}]
			}]
		}]
	}`
	report := evaluateTempFlakes(t, "playwright.json", input)

	if len(report.Tests) != 1 {
		t.Fatalf("expected one test, got %d", len(report.Tests))
	}
	test := report.Tests[0]
	if test.Source != flakeSourcePlaywright || test.TestName != "can access project" || test.Status != flakeStatusFail {
		t.Fatalf("unexpected Playwright test: %+v", test)
	}
	if test.Package != "web/e2e/smoke.spec.ts" {
		t.Fatalf("expected repository-relative file, got %s", test.Package)
	}
}

func TestEvaluateFlakesErrorsDoNotSilentlyPass(t *testing.T) {
	report := evaluateTempFlakes(t, "go-test.json", `{broken}`)

	if report.Passed || !report.Failed {
		t.Fatalf("expected parse errors to fail the report, got passed=%t failed=%t", report.Passed, report.Failed)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected parse error to be recorded")
	}
}

func TestFlakeReportMarshalsMachineReadableSchema(t *testing.T) {
	data, err := json.Marshal(FlakeReport{Summary: FlakeSummary{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"summary", "p0ActiveFlake", "p1ActiveFlake", "passed", "failed"} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("expected field %s in %s", field, string(data))
		}
	}
}

func evaluateTempFlakes(t *testing.T, name, content string) FlakeReport {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, _ := EvaluateFlakes(FlakeConfig{
		CataloguePath: "testdata/scenario_catalogue.json",
		Inputs:        []string{path},
		FailOnActive:  true,
	})
	return report
}

func evaluateRepositoryTempFlakes(t *testing.T, name, content string) FlakeReport {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, _ := EvaluateFlakes(FlakeConfig{
		RepositoryDir: "../..",
		CataloguePath: "testdata/scenario_catalogue.json",
		Inputs:        []string{path},
	})
	return report
}

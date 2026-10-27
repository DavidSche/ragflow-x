package performance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestRunAppSmoke(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "performance.db")
	cfg.App.JWTSecret = "performance-test-secret"
	cfg.Server.Mode = "test"
	policy := SamplingPolicy{
		DurationPerConcurrency: 200 * time.Millisecond,
		ConcurrencyLevels:      []int{1},
		TimeoutPerAttempt:      time.Second,
	}
	report, err := RunApp(context.Background(), RunnerOptions{
		Config: &cfg, Version: "test", CPUModel: "test-cpu", MemoryGB: "test-memory",
		DataScale: "smoke", KnowledgeBases: 0,
	}, NewSampler(&policy))
	if err != nil {
		t.Fatalf("run app: %v", err)
	}
	if len(report.Scenarios) != 7 {
		t.Fatalf("expected 7 scenarios, got %d", len(report.Scenarios))
	}
	for _, scenario := range report.Scenarios {
		if len(scenario.Results) != 1 || scenario.Results[0].Total == 0 {
			t.Fatalf("scenario %s captured no samples: %+v", scenario.Name, scenario.Results)
		}
	}
	if report.Resources.HeapAllocBytes == 0 || report.Resources.SampledAt == 0 {
		t.Fatalf("resource monitor did not observe the run: %+v", report.Resources)
	}
}

func TestRunAppWorkerScenarioCompletesUnderConcurrency(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "performance.db")
	cfg.App.JWTSecret = "performance-worker-secret"
	cfg.Server.Mode = "test"
	policy := SamplingPolicy{
		DurationPerConcurrency: 500 * time.Millisecond,
		ConcurrencyLevels:      []int{16},
		TimeoutPerAttempt:      5 * time.Second,
	}
	report, err := RunApp(context.Background(), RunnerOptions{
		Config: &cfg, Version: "worker-regression", CPUModel: "test-cpu", MemoryGB: "test-memory",
		DataScale: "worker regression", KnowledgeBases: 0,
	}, NewSampler(&policy))
	if err != nil {
		t.Fatalf("run app: %v", err)
	}
	for _, scenario := range report.Scenarios {
		if scenario.Name != "worker_single_task" {
			continue
		}
		result := scenario.Results[0]
		if result.Errors != 0 {
			t.Fatalf("worker errors = %d, samples = %d, error samples = %+v", result.Errors, result.Total, result.ErrorSamples)
		}
	}
}

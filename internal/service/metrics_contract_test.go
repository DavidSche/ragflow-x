package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMetricsContractGateAppliesComparators(t *testing.T) {
	cases := []struct {
		key   string
		value float64
		want  string
	}{
		{"top1_accuracy", 0.95, "passed"},
		{"top1_accuracy", 0.5, "failed"},
		{"auto_execute_wrong_rate", 0, "passed"},
		{"auto_execute_wrong_rate", 0.01, "failed"},
		{"route_p95_ms", 900, "passed"},
		{"route_p95_ms", 2000, "failed"},
		{"unknown_metric", 1, "unknown"},
	}
	for _, testCase := range cases {
		if got := metricsContractGate(testCase.key, testCase.value); got != testCase.want {
			t.Fatalf("gate(%s, %v) = %s, want %s", testCase.key, testCase.value, got, testCase.want)
		}
	}
}

func TestMetricsContractSummaryAggregatesBoard(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Metrics Contract Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "metrics-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Empty state: board renders with unknown gates, no error.
	board, err := svc.MetricsContractSummary(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Items) != 0 || board.WindowDays != 30 {
		t.Fatalf("empty board mismatch: %+v", board)
	}

	// Seed the latest route evaluation run with passing routing metrics.
	metrics := RouteEvaluationMetrics{
		CaseCount: 40, EvaluatedCount: 40,
		Top1Accuracy: 0.95, Top3Recall: 1, WrongRouteRate: 0.01,
		WrongExecutionRate: 0.01, AutoExecuteWrongRate: 0, AbstainRate: 0.05,
		ECE: 0.03, HighConfidenceWilsonLower: 0.9, RouteP95Ms: 800,
		GateState: model.RouteEvalGatePassed,
	}
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	run := &model.RouteEvaluationRun{
		ID: "route-run-1", TenantID: tenant.ID, Name: "contract-run", Source: "doc41-real-scenarios",
		ActorID: admin.ID, RouterVersion: "v1", CalibrationVersion: "c1", NormalizationMethod: "none",
		CaseCount: 40, MetricsJSON: string(metricsJSON), BoundaryJSON: "{}",
		GateState: model.RouteEvalGatePassed,
	}
	if err := svc.Store.CreateRouteEvaluationRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	board, err = svc.MetricsContractSummary(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if board.RouteGateState != model.RouteEvalGatePassed || board.RouteRunName != "contract-run" {
		t.Fatalf("route gate header mismatch: %+v", board)
	}
	byKey := map[string]MetricsContractItem{}
	for _, item := range board.Items {
		byKey[item.Key] = item
	}
	// Routing rows come from the seeded evaluation run.
	for _, key := range []string{
		"top1_accuracy", "top3_recall", "wrong_route_rate", "wrong_execution_rate",
		"auto_execute_wrong_rate", "abstain_rate", "ece", "high_confidence_wilson_lower_95",
		"route_p95_ms",
	} {
		item, ok := byKey[key]
		if !ok {
			t.Fatalf("contract board missing %s: %+v", key, byKey)
		}
		if item.Target == nil || item.Comparator == "" {
			t.Fatalf("%s missing frozen target/comparator: %+v", key, item)
		}
		if item.Value == nil {
			t.Fatalf("%s missing observed value", key)
		}
		if item.SampleSize != 40 {
			t.Fatalf("%s sample size mismatch: %+v", key, item)
		}
	}
	if byKey["top1_accuracy"].GateState != "passed" {
		t.Fatalf("top1_accuracy should pass at 0.95: %+v", byKey["top1_accuracy"])
	}
	if byKey["auto_execute_wrong_rate"].GateState != "passed" {
		t.Fatalf("auto_execute_wrong_rate should pass at 0: %+v", byKey["auto_execute_wrong_rate"])
	}
	if byKey["top1_accuracy"].JudgeType != "rule" {
		t.Fatalf("routing metrics are rule judged: %+v", byKey["top1_accuracy"])
	}
	// Answer/operations rows need observations; with an empty 30-day window
	// they are withheld rather than judged on zero samples.
	for _, key := range []string{"citation_rate", "no_answer_rate", "avg_latency_ms", "satisfaction_rate"} {
		if _, ok := byKey[key]; ok {
			t.Fatalf("%s must be withheld with zero observations", key)
		}
	}
	if len(metricContractComparators) == 0 {
		t.Fatal("frozen contract table must not be empty")
	}
}

func TestMetricsContractTargetsMatchFrozenContract(t *testing.T) {
	// doc/107 §3.3.2 frozen targets must not drift silently.
	expectations := map[string]struct {
		target     float64
		comparator string
	}{
		"top1_accuracy":                   {0.90, "gte"},
		"top3_recall":                     {0.98, "gte"},
		"wrong_route_rate":                {0.02, "lte"},
		"wrong_execution_rate":            {0.02, "lte"},
		"auto_execute_wrong_rate":         {0, "eq"},
		"abstain_rate":                    {0.10, "lte"},
		"ece":                             {0.05, "lte"},
		"high_confidence_wilson_lower_95": {0.85, "gte"},
		"route_p95_ms":                    {1200, "lte"},
		"citation_rate":                   {0.95, "gte"},
		"no_answer_rate":                  {0.05, "lte"},
		"avg_latency_ms":                  {3000, "lte"},
		"satisfaction_rate":               {0.85, "gte"},
	}
	if len(metricContractComparators) != len(expectations) {
		t.Fatalf("comparator table size drifted: %d != %d", len(metricContractComparators), len(expectations))
	}
	for key, want := range expectations {
		got, ok := metricContractComparators[key]
		if !ok || got.target == nil || *got.target != want.target || got.comparator != want.comparator {
			t.Fatalf("target drift for %s: %+v want %+v", key, got, want)
		}
	}
}

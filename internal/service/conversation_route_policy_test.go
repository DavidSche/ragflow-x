package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRoutePolicyRemainsRecommendOnlyWithoutGate(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	svc.SetRoutePolicy(config.ConversationRouting{Mode: RouteModeAutoLowRisk})
	tenant, err := svc.CreateTenant(ctx, "M3 Gate Closed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenant.ID, "Closed Gate Chat", []string{}); err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "m3_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := svc.RouteConversation(ctx, admin.ID, tenant.ID, RouteRequest{
		Query: "Closed Gate Chat", RequestedMode: "auto-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.EffectiveMode != RouteModeRecommendOnly || decision.Selected != nil || decision.ScoreStatus != "provisional" {
		t.Fatalf("closed gate must not auto route: %+v", decision)
	}
	if decision.RouteBudgetMs != 1500 || decision.BudgetExceeded {
		t.Fatalf("route must expose its configured total budget: %+v", decision)
	}
	for _, candidate := range decision.Candidates {
		if candidate.Confidence != nil || candidate.ConfidenceStatus != "not_calibrated" || candidate.ConfidenceMargin != nil {
			t.Fatalf("closed gate must not expose calibrated confidence: %+v", candidate)
		}
	}
}

func TestRoutePolicyNormalizesTotalBudgetAndRerankTimeout(t *testing.T) {
	policy := normalizeRoutePolicy(config.ConversationRouting{
		Mode: RouteModeAutoLowRisk, TotalTimeoutMs: 50, CatalogTTLSec: 300,
		Rerank: config.RouteRerank{TimeoutMs: 5000},
	})
	if policy.TotalTimeoutMs != 100 {
		t.Fatalf("route total budget = %d, want minimum 100", policy.TotalTimeoutMs)
	}
	if policy.Rerank.TimeoutMs != 50 {
		t.Fatalf("rerank timeout = %d, want half of minimum total budget", policy.Rerank.TimeoutMs)
	}
}

func TestTenantPolicyCannotBypassSystemRecommendOnly(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	svc.SetRoutePolicy(config.ConversationRouting{Mode: RouteModeRecommendOnly})
	tenant, err := svc.CreateTenant(ctx, "Tenant Cannot Escalate")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "tenant_mode_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTenantRoutePolicy(ctx, admin.ID, tenant.ID, UpdateRoutePolicyRequest{AutoRouteMode: RouteModeAutoLowRisk}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenant.ID, "Escalation Chat", []string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenant.ID, "Escalation Companion", []string{}); err != nil {
		t.Fatal(err)
	}
	calibration := RouteCalibration{
		Version: "m25-system-test-v1", Method: "l2_logistic_regression", Intercept: -5,
		Weights:      []float64{20, 20, 0, 0, 0, 0},
		FeatureNames: []string{"normalized_score", "normalized_margin", "candidate_count", "has_competitor", "routing_readiness", "query_length"},
		Validated:    true,
	}
	calibrationJSON, err := json.Marshal(calibration)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateRouteEvaluationRun(ctx, &model.RouteEvaluationRun{
		ID: "tenant-mode-run", TenantID: tenant.ID, Name: "system boundary", Source: "test",
		ActorID: admin.ID, RouterVersion: conversationRouterVersion,
		CalibrationVersion: calibration.Version, NormalizationMethod: "sigmoid",
		CaseCount: 2, MetricsJSON: "{}", BoundaryJSON: "[]", CalibrationJSON: string(calibrationJSON),
		GateState: model.RouteEvalGatePassed, AllowAutoLowRisk: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.RouteConversation(ctx, admin.ID, tenant.ID, RouteRequest{Query: "Escalation Chat"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.EffectiveMode != RouteModeRecommendOnly || decision.Selected != nil {
		t.Fatalf("tenant policy must not bypass system recommend-only: %+v", decision)
	}
}

func TestRoutePolicyAutoSelectsLowRiskCandidateAfterGate(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	svc.SetRoutePolicy(config.ConversationRouting{Mode: RouteModeAutoLowRisk})
	tenant, err := svc.CreateTenant(ctx, "M3 Gate Passed")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := svc.CreateChat(ctx, tenant.ID, "Alpha assistant", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenant.ID, "Alpha companion", []string{}); err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "m3_gate_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTenantRoutePolicy(ctx, admin.ID, tenant.ID, UpdateRoutePolicyRequest{AutoRouteMode: RouteModeAutoLowRisk}); err != nil {
		t.Fatal(err)
	}
	auto := true
	description := "Policy and compliance query assistant"
	categories := []string{"policy"}
	capabilities := []string{"knowledge"}
	keywords := []string{"policy"}
	intents := []string{"requirement"}
	examples := []string{"What does the policy require?"}
	for _, target := range []string{alpha.ID} {
		if _, err := svc.UpdateConversationAssistant(ctx, admin.ID, tenant.ID, model.AssistantKindChat, target, UpdateConversationAssistantRequest{
			Description: &description, Categories: &categories, Capabilities: &capabilities,
			Keywords: &keywords, Intents: &intents, Examples: &examples, AutoSelectEnabled: &auto,
		}); err != nil {
			t.Fatal(err)
		}
	}
	calibration := RouteCalibration{
		Version: "m25-test-v1", Method: "l2_logistic_regression", Intercept: -5,
		Weights:      []float64{20, 20, 0, 0, 0, 0},
		FeatureNames: []string{"normalized_score", "normalized_margin", "candidate_count", "has_competitor", "routing_readiness", "query_length"},
		Validated:    true,
	}
	calibrationJSON, err := json.Marshal(calibration)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateRouteEvaluationRun(ctx, &model.RouteEvaluationRun{
		ID: "gate-pass-run", TenantID: tenant.ID, Name: "passed", Source: "test",
		ActorID: admin.ID, RouterVersion: conversationRouterVersion,
		CalibrationVersion: calibration.Version, NormalizationMethod: "sigmoid",
		CaseCount: 400, MetricsJSON: "{}", BoundaryJSON: "[]", CalibrationJSON: string(calibrationJSON),
		GateState: model.RouteEvalGatePassed, AllowAutoLowRisk: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.RouteConversation(ctx, admin.ID, tenant.ID, RouteRequest{Query: "Alpha assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.EffectiveMode != RouteModeAutoLowRisk || decision.Selected == nil || decision.ScoreStatus != "calibrated" {
		t.Fatalf("expected calibrated auto route: %+v", decision)
	}
	if decision.Selected.TargetID != alpha.ID || decision.Selected.Confidence == nil || *decision.Selected.Confidence < 0.86 {
		t.Fatalf("unexpected auto selection: %+v", decision.Selected)
	}
	if decision.Selected.ConfidenceMargin == nil || *decision.Selected.ConfidenceMargin < 0.12 {
		t.Fatalf("auto route confidence margin missing: %+v", decision.Selected)
	}
	if decision.Selected.AutoSelectEnabled == false || decision.Selected.PreExecutionRisk != model.AssistantRiskLow {
		t.Fatalf("auto selection ignored governance metadata: %+v", decision.Selected)
	}
}

func TestRoutePolicyRequiresAgentFlowReadiness(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	svc.SetRoutePolicy(config.ConversationRouting{Mode: RouteModeAutoLowRisk})
	tenant, err := svc.CreateTenant(ctx, "Agent Flow Gate")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "agent_flow_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTenantRoutePolicy(ctx, admin.ID, tenant.ID, UpdateRoutePolicyRequest{AutoRouteMode: RouteModeAutoLowRisk}); err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "Risk workflow", map[string]interface{}{"components": map[string]interface{}{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	description := "Enterprise risk control workflow"
	keywords := []string{"risk control"}
	examples := []string{"enterprise risk control details"}
	if _, err := svc.UpdateConversationAssistant(ctx, admin.ID, tenant.ID, model.AssistantKindAgent, agent.ID, UpdateConversationAssistantRequest{
		Description: &description, Keywords: &keywords, Examples: &examples,
	}); err != nil {
		t.Fatal(err)
	}
	calibration := RouteCalibration{
		Version: "m25-agent-test-v1", Method: "l2_logistic_regression", Intercept: -5,
		Weights:      []float64{20, 20, 0, 0, 0, 0},
		FeatureNames: []string{"normalized_score", "normalized_margin", "candidate_count", "has_competitor", "routing_readiness", "query_length"},
		Validated:    true,
	}
	calibrationJSON, err := json.Marshal(calibration)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateRouteEvaluationRun(ctx, &model.RouteEvaluationRun{
		ID: "agent-flow-gate-run", TenantID: tenant.ID, Name: "agent flow", Source: "test",
		ActorID: admin.ID, RouterVersion: conversationRouterVersion,
		CalibrationVersion: calibration.Version, NormalizationMethod: "sigmoid",
		CaseCount: 3, MetricsJSON: "{}", BoundaryJSON: "[]", CalibrationJSON: string(calibrationJSON),
		GateState: model.RouteEvalGatePassed, AllowAutoLowRisk: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.RouteConversation(ctx, admin.ID, tenant.ID, RouteRequest{Query: "enterprise risk control details"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.EffectiveMode != RouteModeAutoLowRisk || decision.Selected != nil {
		t.Fatalf("unvalidated agent flow must not auto route: %+v", decision)
	}
}

func TestRouteRerankStrictJSONAndDegradation(t *testing.T) {
	candidates := []RouteCandidate{
		{Kind: model.AssistantKindChat, TargetID: "alpha", Name: "Alpha"},
		{Kind: model.AssistantKindAgent, TargetID: "beta", Name: "Beta"},
	}
	ordered, ok := decodeRouteRerank(`{"ranking":["beta","alpha"]}`, candidates)
	if !ok || len(ordered) != 2 || ordered[0].TargetID != "beta" || ordered[1].TargetID != "alpha" {
		t.Fatalf("strict rerank JSON rejected: ok=%v ordered=%+v", ok, ordered)
	}
	if _, ok := decodeRouteRerank(`{"ranking":["beta"]}`, candidates); ok {
		t.Fatal("incomplete rerank must be rejected")
	}
	if _, ok := decodeRouteRerank(`{"ranking":["beta","beta"]}`, candidates); ok {
		t.Fatal("duplicate rerank target must be rejected")
	}
	if _, ok := decodeRouteRerank(`{"ranking":["beta","gamma"]}`, candidates); ok {
		t.Fatal("unknown rerank target must be rejected")
	}
	if _, ok := decodeRouteRerank("```json\n{\"ranking\":[\"beta\",\"alpha\"]}\n```", candidates); ok {
		t.Fatal("non-JSON envelope must be rejected")
	}
}

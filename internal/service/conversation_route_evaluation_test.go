package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRunRouteEvaluationDoc41PilotSuite(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Route Evaluation")
	if err != nil {
		t.Fatal(err)
	}
	adminID := createDoc41RouteSuite(t, svc, tenant.ID)
	report, err := svc.RunRouteEvaluation(ctx, adminID, tenant.ID, RouteEvaluationRequest{})
	if err != nil {
		t.Fatalf("run evaluation: %v", err)
	}
	if report.Run == nil || report.Run.Source != routeEvaluationSourceDoc41 {
		t.Fatalf("doc41 source missing: %+v", report.Run)
	}
	metrics := report.Metrics
	if metrics.CaseCount != 103 {
		t.Fatalf("expected 103 doc41 cases, got %d", metrics.CaseCount)
	}
	if metrics.EvidenceLevel != RouteGateEvidencePilot {
		t.Fatalf("expected pilot evidence gate, got %s", metrics.EvidenceLevel)
	}
	if metrics.PermissionLeakage != 0 || metrics.OfflineErrorAutoExecutions != 0 {
		t.Fatalf("unexpected offline safety failures: %+v", metrics)
	}
	if metrics.GateState != model.RouteEvalGatePassed || len(metrics.GateFailures) != 0 {
		t.Fatalf("real-material pilot gate must pass: %+v", metrics)
	}
	if metrics.AgentFlowCheckedCount != 2 || metrics.AgentFlowReadyCount != 2 || metrics.AgentFlowBlockedCount != 0 {
		t.Fatalf("agent flow readiness was not evaluated: %+v", metrics)
	}
	if metrics.HighConfidenceCount < metrics.HighConfidenceMinimum || metrics.HighConfidenceWilsonLower < 0.85 {
		t.Fatalf("pilot high-confidence evidence below gate: %+v", metrics)
	}
	if metrics.ClarificationAccuracy < 0.90 {
		t.Fatalf("ambiguous questions must use clarification: %+v", metrics)
	}
	var calibration RouteCalibration
	if err := json.Unmarshal([]byte(report.Run.CalibrationJSON), &calibration); err != nil {
		t.Fatal(err)
	}
	if len(calibration.Weights) != len(calibration.FeatureNames) || calibration.Version == "" {
		t.Fatalf("calibration must be persisted for M3 policy: %+v", calibration)
	}
	for _, question := range Doc41RouteEvaluationCases() {
		if strings.Contains(report.Run.MetricsJSON, question.Question) || strings.Contains(report.Run.BoundaryJSON, question.Question) {
			t.Fatal("evaluation run persisted prompt text")
		}
	}
	var boundary RouteBoundaryMatrix
	if err := json.Unmarshal([]byte(report.Run.BoundaryJSON), &boundary); err != nil {
		t.Fatal(err)
	}
}

func TestRouteEvaluationPilotGateRequiresEvidence(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Pilot Evidence Minimum")
	if err != nil {
		t.Fatal(err)
	}
	adminID := createDoc41RouteSuite(t, svc, tenant.ID)
	cases := Doc41RouteEvaluationCases()[:20]
	report, err := svc.RunRouteEvaluation(ctx, adminID, tenant.ID, RouteEvaluationRequest{Cases: cases})
	if err != nil {
		t.Fatalf("run evaluation: %v", err)
	}
	if report.Metrics.GateState != model.RouteEvalGateInsuffi {
		t.Fatalf("small real-material suite must remain insufficient: %+v", report.Metrics)
	}
}

func TestRouteEvaluationProductionRequiresEvidence(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	svc.SetRoutePolicy(config.ConversationRouting{
		Gate: config.RouteGate{Production: config.RouteEvidenceThreshold{MinHighConfidence: 2, MinWilsonLower: 0.80, MaxECE: 0.05}},
	})
	tenant, err := svc.CreateTenant(ctx, "Production Evidence Minimum")
	if err != nil {
		t.Fatal(err)
	}
	adminID := createDoc41RouteSuite(t, svc, tenant.ID)
	report, err := svc.RunRouteEvaluation(ctx, adminID, tenant.ID, RouteEvaluationRequest{
		EvidenceLevel: RouteGateEvidenceProduction,
		Cases:         Doc41RouteEvaluationCases(),
	})
	if err != nil {
		t.Fatalf("run evaluation: %v", err)
	}
	metrics := report.Metrics
	if metrics.EvidenceLevel != RouteGateEvidenceProduction || metrics.HighConfidenceMinimum != 2 {
		t.Fatalf("production evidence level missing: %+v", metrics)
	}
	if metrics.GateState != model.RouteEvalGatePassed || len(metrics.GateFailures) != 0 {
		t.Fatalf("configured production gate must be honored: %+v", metrics)
	}
}

func TestRouteEvaluationBlocksUnreadyAgentFlow(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Agent Flow Evaluation")
	if err != nil {
		t.Fatal(err)
	}
	adminID := createDoc41RouteSuite(t, svc, tenant.ID)
	items, _, err := svc.ListConversationAssistants(ctx, adminID, tenant.ID, "安全生产风险点防控统计表问答-r2", []string{model.AssistantKindAgent}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("agent catalog item missing: %d", len(items))
	}
	flowReadiness := 0.0
	if _, err := svc.UpdateConversationAssistant(ctx, adminID, tenant.ID, model.AssistantKindAgent, items[0].ID, UpdateConversationAssistantRequest{
		AgentFlowReadiness: &flowReadiness,
	}); err != nil {
		t.Fatal(err)
	}
	var agentCases []RouteEvalCaseInput
	for _, item := range Doc41RouteEvaluationCases() {
		if item.ExpectedKind == model.AssistantKindAgent {
			agentCases = append(agentCases, item)
		}
	}
	report, err := svc.RunRouteEvaluation(ctx, adminID, tenant.ID, RouteEvaluationRequest{Cases: agentCases})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.AgentFlowCheckedCount != 3 || report.Metrics.AgentFlowBlockedCount != 3 {
		t.Fatalf("unready agent flow metrics missing: %+v", report.Metrics)
	}
	if report.Metrics.GateState != model.RouteEvalGateInsuffi {
		t.Fatalf("small unready-agent suite must not pass: %+v", report.Metrics)
	}
	found := false
	for _, failure := range report.Metrics.GateFailures {
		if strings.Contains(failure, "agent_flow_readiness_below_0.80") {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent flow failure missing: %+v", report.Metrics)
	}
}

func createDoc41RouteSuite(t *testing.T, svc *Service, tenantID string) string {
	t.Helper()
	admin, err := svc.CreateUser(context.Background(), tenantID, "", CreateUserRequest{
		Username: "route-eval-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range doc41RouteEvaluationMetadata() {
		var assistantID string
		if target.kind == model.AssistantKindAgent {
			assistant, err := svc.CreateAgent(context.Background(), tenantID, target.name, map[string]interface{}{"components": map[string]interface{}{}}, true, model.AgentCanvasCategoryWorkflow)
			if err != nil {
				t.Fatal(err)
			}
			assistantID = assistant.ID
		} else {
			assistant, err := svc.CreateChat(context.Background(), tenantID, target.name, []string{})
			if err != nil {
				t.Fatal(err)
			}
			assistantID = assistant.ID
		}
		if err != nil {
			t.Fatal(err)
		}
		keywords, examples := target.routingMetadata()
		description := target.description
		capabilities := []string{"conversation", "knowledge", "safety"}
		intents := []string{target.intent}
		flowReadiness := 1.0
		if _, err := svc.UpdateConversationAssistant(context.Background(), admin.ID, tenantID, target.kind, assistantID, UpdateConversationAssistantRequest{
			Description: &description, Capabilities: &capabilities, Intents: &intents,
			Keywords: &keywords, Examples: &examples, AgentFlowReadiness: &flowReadiness,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return admin.ID
}

func TestAssistantManagePermissionCatalog(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Assistant RBAC")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "assistant_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "assistant_viewer", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, admin.ID, "manage", "assistant"); err != nil {
		t.Fatalf("tenant admin must manage assistant catalog: %v", err)
	}
	if err := svc.Authorize(ctx, viewer.ID, "manage", "assistant"); err == nil {
		t.Fatal("viewer must not manage assistant catalog")
	}
}

type routeMemoryStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newRouteMemoryStore() *routeMemoryStore {
	return &routeMemoryStore{values: map[string][]byte{}}
}

func (store *routeMemoryStore) Save(_ context.Context, key string, value []byte, _ time.Duration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[key] = append([]byte(nil), value...)
	return nil
}

func (store *routeMemoryStore) Load(_ context.Context, key string) ([]byte, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, found := store.values[key]
	return append([]byte(nil), value...), found, nil
}

func (store *routeMemoryStore) Close() error { return nil }

func TestRouteStateStoreKeepsActorIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	state := newRouteMemoryStore()
	svc.SetRouteStateStore(state)
	tenant, err := svc.CreateTenant(ctx, "Route Redis State")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenant.ID, "Redis State Chat", []string{}); err != nil {
		t.Fatal(err)
	}
	actor := routeOperator(t, svc, tenant.ID, "")
	otherActor := routeOperator(t, svc, tenant.ID, "")
	decision, err := svc.RouteConversation(ctx, actor, tenant.ID, RouteRequest{Query: "redis state chat"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(state.values) != 1 {
		t.Fatalf("route state was not saved: %d", len(state.values))
	}
	_, err = svc.SelectRouteCandidate(ctx, otherActor, tenant.ID, decision.RouteID, "other-key", SelectRouteCandidateRequest{
		CandidateID:   decision.Candidates[0].TargetID,
		CandidateKind: decision.Candidates[0].Kind,
	})
	if err == nil {
		t.Fatal("another actor must not consume a private route decision")
	}
	selection, err := svc.SelectRouteCandidate(ctx, actor, tenant.ID, decision.RouteID, "actor-key", SelectRouteCandidateRequest{
		CandidateID:   decision.Candidates[0].TargetID,
		CandidateKind: decision.Candidates[0].Kind,
	})
	if err != nil || selection.State != model.RouteSelectionNew {
		t.Fatalf("owner must consume route decision: %v %+v", err, selection)
	}
}

package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newRouteSvc(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "route.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "route-test-secret")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func TestRoutePreExecutionRiskUpperBound(t *testing.T) {
	low := routePreExecutionRisk(ConversationAssistant{
		AssistantRiskLevel:     model.AssistantRiskLow,
		CapabilityRisk:         map[string]string{"delete_orders": model.AssistantRiskHigh},
		WorkflowRiskUpperBound: model.AssistantRiskMedium,
	})
	if low != model.AssistantRiskHigh {
		t.Fatalf("capability risk must raise pre-execution risk: %s", low)
	}
}

func routeOperator(t *testing.T, svc *Service, tenantID string, roleID string) string {
	t.Helper()
	user, err := svc.CreateUser(context.Background(), tenantID, "", CreateUserRequest{
		Username: "route-user-" + time.Now().Format("150405.000000000"),
		Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if roleID != "" && roleID != model.RoleOperator {
		if err := svc.Store.AssignUserRole(context.Background(), user.ID, roleID); err != nil {
			t.Fatal(err)
		}
	}
	return user.ID
}

func TestRouteConversationDeterministicAndPrivate(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Route")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := svc.CreateChat(ctx, tenant.ID, "Contract Assistant", []string{})
	if err != nil {
		t.Fatal(err)
	}
	quality, err := svc.CreateAgent(ctx, tenant.ID, "Quality Assistant", map[string]interface{}{"nodes": []interface{}{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	actor := routeOperator(t, svc, tenant.ID, "")
	decision, err := svc.RouteConversation(ctx, actor, tenant.ID, RouteRequest{
		Query: "contract assistant", IncludeKinds: []string{"chat", "agent"}, RequestedMode: "auto-request", DisplayLimit: 1,
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if decision.EffectiveMode != RouteModeRecommendOnly || decision.Selected != nil || decision.ScoreStatus != "provisional" {
		t.Fatalf("M2 must remain recommend-only: %+v", decision)
	}
	if len(decision.Candidates) != 1 || decision.CandidateCount != 2 || decision.Candidates[0].ID != contract.ID {
		t.Fatalf("unexpected candidates: %+v", decision)
	}
	candidate := decision.Candidates[0]
	if candidate.TargetID != quality.ID && candidate.TargetID != contract.ID {
		t.Fatal("candidate target mismatch")
	}
	if candidate.TargetID != contract.ID {
		t.Fatalf("wrong deterministic top candidate: %+v", candidate)
	}
	if candidate.Confidence != nil || candidate.ConfidenceStatus != "not_calibrated" || candidate.ConfidenceMargin != nil {
		t.Fatalf("M2 confidence must not be calibrated: %+v", candidate)
	}
	if candidate.NormalizedMargin == nil || !candidate.HasCompetitor || candidate.ConfidenceMargin != nil {
		t.Fatalf("displayed candidate must keep calibration-only margin: %+v", candidate)
	}
	if decision.PolicyMode != RouteModeRecommendOnly || decision.PolicyVersion == "" || decision.RerankStatus != "not_applicable" {
		t.Fatalf("M2 policy must remain recommend-only without a passed gate: %+v", decision)
	}
	if decision.Candidates[0].TargetID != contract.ID {
		t.Fatal("target id mismatch")
	}
	stored, err := svc.Store.GetRouteDecision(ctx, tenant.ID, actor, decision.RouteID)
	if err != nil || stored == nil {
		t.Fatalf("decision must persist: %v %+v", err, stored)
	}
	if strings.Contains(strings.ToLower(stored.QueryFingerprint), "contract assistant") {
		t.Fatal("route decision must not contain query text")
	}
	audits, _, err := svc.Store.ListAudits(ctx, tenant.ID, 1, 20, repository.AuditFilter{Action: "conversation.route.requested"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("route audit missing: %v %+v", err, audits)
	}
	if strings.Contains(strings.ToLower(audits[0].DetailJSON), "contract assistant") {
		t.Fatalf("audit must not contain prompt: %+v", audits[0].DetailJSON)
	}
	if !strings.Contains(audits[0].DetailJSON, "query_fingerprint") || !strings.Contains(audits[0].DetailJSON, "hmac-v1") {
		t.Fatalf("audit fingerprint metadata missing: %+v", audits[0].DetailJSON)
	}
}

func TestRouteSelectionBootstrapAndIdempotency(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "RouteBinding")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "Policy Chat", []string{})
	if err != nil {
		t.Fatal(err)
	}
	actor := routeOperator(t, svc, tenant.ID, "")
	decision, err := svc.RouteConversation(ctx, actor, tenant.ID, RouteRequest{Query: "policy chat"})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := svc.SelectRouteCandidate(ctx, actor, tenant.ID, decision.RouteID, "select-key", SelectRouteCandidateRequest{
		CandidateID: chat.ID, CandidateKind: model.AssistantKindChat,
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if selection.State != model.RouteSelectionNew || !selection.RequiresSession {
		t.Fatalf("selection must be NEW: %+v", selection)
	}
	retry, err := svc.SelectRouteCandidate(ctx, actor, tenant.ID, decision.RouteID, "select-key", SelectRouteCandidateRequest{
		CandidateID: chat.ID, CandidateKind: model.AssistantKindChat,
	})
	if err != nil || retry.RouteSelectionID != selection.RouteSelectionID {
		t.Fatalf("selection retry must be idempotent: %v %+v", err, retry)
	}
	bootstrap, err := svc.BootstrapRouteSelection(ctx, actor, tenant.ID, selection.RouteSelectionID, "bootstrap-key")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if bootstrap.SessionID == "" || bootstrap.SelectionState != model.RouteSelectionConsumed || bootstrap.BootstrapState != model.BootstrapSucceeded {
		t.Fatalf("bootstrap must create one session: %+v", bootstrap)
	}
	retryBootstrap, err := svc.BootstrapRouteSelection(ctx, actor, tenant.ID, selection.RouteSelectionID, "bootstrap-key")
	if err != nil || retryBootstrap.SessionID != bootstrap.SessionID {
		t.Fatalf("bootstrap retry must reuse session: %v %+v", err, retryBootstrap)
	}
	if _, err := svc.BootstrapRouteSelection(ctx, actor, tenant.ID, selection.RouteSelectionID, "another-key"); err == nil {
		t.Fatal("different request must not reuse consumed selection")
	}
}

func TestRouteAgentRequiresSessionCreatePermission(t *testing.T) {
	ctx := context.Background()
	svc := newRouteSvc(t)
	tenant, err := svc.CreateTenant(ctx, "AgentRoute")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "Approval Assistant", map[string]interface{}{"nodes": []interface{}{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	restricted := model.Role{ID: "agent_execute_no_create", Name: "Agent Execute", Scope: model.RoleScopeTenant}
	if err := svc.Store.CreateRole(ctx, &restricted); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetPermissions(ctx, restricted.ID, []model.Permission{
		{ID: "p-agent-execute", RoleID: restricted.ID, Action: "execute", Resource: "agent", Effect: model.PermissionEffectAllow},
		{ID: "p-assistant-execute", RoleID: restricted.ID, Action: "execute", Resource: "assistant", Effect: model.PermissionEffectAllow},
		{ID: "p-assistant-read", RoleID: restricted.ID, Action: "read", Resource: "assistant", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	actor := routeOperator(t, svc, tenant.ID, restricted.ID)
	if err := svc.Store.RemoveUserRoles(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AssignUserRole(ctx, actor, restricted.ID); err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUser(ctx, actor)
	if err != nil || user == nil {
		t.Fatalf("route user missing: %v %+v", err, user)
	}
	user.Role = restricted.ID
	if err := svc.Store.UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, actor, "execute", "agent"); err != nil {
		t.Fatalf("agent execute expected: %v", err)
	}
	if err := svc.Authorize(ctx, actor, "session:create", "agent"); err == nil {
		t.Fatal("session:create must be absent")
	}
	decision, err := svc.RouteConversation(ctx, actor, tenant.ID, RouteRequest{Query: "approval assistant", IncludeKinds: []string{"agent"}})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(decision.Candidates) != 0 {
		t.Fatalf("agent candidate without session:create must be filtered from route: %+v", decision.Candidates)
	}
	_, err = svc.SelectRouteCandidate(ctx, actor, tenant.ID, decision.RouteID, "select-agent", SelectRouteCandidateRequest{
		CandidateID: agent.ID, CandidateKind: model.AssistantKindAgent,
	})
	if err == nil {
		t.Fatal("select must reject a candidate outside the route decision")
	}
}

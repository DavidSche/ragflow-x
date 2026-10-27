package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func activateSelfMemoryPolicy(t *testing.T, svc *Service, tenantID, userID, assistantID string) (*model.MemoryPolicy, *model.MemoryShadow) {
	t.Helper()
	ctx := context.Background()
	template := `{"purpose":"assistant recall","read_policy":"owner","write_policy":"owner","retention_days":30,"embedding_model":"embedding-1","llm_model":"chat-1"}`
	if _, err := svc.UpdateSelfMemoryPolicyTemplate(ctx, tenantID, userID, SelfMemoryPolicyTemplateInput{Template: template}); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateSelfMemoryPolicy(ctx, tenantID, userID, assistantID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err = svc.SetMemoryPolicyConsent(ctx, tenantID, userID, policy.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := svc.Store.GetMemoryShadow(ctx, tenantID, policy.RagflowMemoryID, false)
	if err != nil || memory == nil {
		t.Fatalf("bound memory must exist: memory=%+v err=%v", memory, err)
	}
	return policy, memory
}

func addGovernedMemoryMessage(t *testing.T, svc *Service, tenantID, userID, memoryID string) ragflow.MemoryMessage {
	t.Helper()
	if err := svc.RAGFlow.AddMemoryMessage(t.Context(), ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: "assistant-1", SessionID: "session-1",
		UserID: "rgx:" + tenantID + ":" + userID, UserInput: "preferred deployment", AgentResponse: "on-premise",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.RAGFlow.ListMemoryMessages(t.Context(), memoryID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("fixture message: rows=%d err=%v", len(rows), err)
	}
	return rows[0]
}

func TestSearchGovernedMemoryAllowsActivePolicyAndForcesFilters(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	policy, memory := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	fixture := addGovernedMemoryMessage(t, svc, tenant.ID, user.ID, memory.ID)

	result, err := svc.SearchGovernedMemory(t.Context(), tenant.ID, user.ID, memory.ID, GovernedMemorySearchInput{
		Query: "deployment", AssistantID: "assistant-1", SessionID: "session-1",
		RequestID: "request-governed-read", TopN: 5,
	})
	if err != nil {
		t.Fatalf("governed search: %v", err)
	}
	if result.Decision.Decision != "memory_allowed" || result.Decision.MemoryPolicyID != policy.ID {
		t.Fatalf("unexpected decision: %+v", result.Decision)
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != fixture.MessageID {
		t.Fatalf("unexpected search result: %+v", result.Messages)
	}
	params := svc.RAGFlow.(*ragflow.Mock).LastMemorySearchParams()
	if params.UserID != "rgx:"+tenant.ID+":"+user.ID || params.AgentID != "assistant-1" ||
		params.SessionID != "session-1" || params.TopN != 5 {
		t.Fatalf("forced filters were not applied: %+v", params)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(result.Decision.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["reason"] != "active_policy_binding" || detail["search_side_effect"] != false {
		t.Fatalf("unexpected decision detail: %+v", detail)
	}
	reused, err := svc.SearchGovernedMemory(t.Context(), tenant.ID, user.ID, memory.ID, GovernedMemorySearchInput{
		Query: "deployment", AssistantID: "assistant-1", SessionID: "session-1",
		RequestID: "request-governed-read", TopN: 5,
	})
	if err != nil || reused.Decision.ID != result.Decision.ID {
		t.Fatalf("same request must reuse decision: result=%+v err=%v", reused, err)
	}
	callsAfterReuse := svc.RAGFlow.(*ragflow.Mock).MemorySearchCallCount()
	_, err = svc.SearchGovernedMemory(t.Context(), tenant.ID, user.ID, memory.ID, GovernedMemorySearchInput{
		Query: "different query", AssistantID: "assistant-1", SessionID: "session-1",
		RequestID: "request-governed-read", TopN: 5,
	})
	if err == nil {
		t.Fatal("same request with different query must conflict")
	}
	if svc.RAGFlow.(*ragflow.Mock).MemorySearchCallCount() != callsAfterReuse {
		t.Fatal("request conflict must not call RAGFlow")
	}
}

func TestSearchGovernedMemoryFailClosedBeforeRAGFlow(t *testing.T) {
	cases := []struct {
		name        string
		buildPolicy func(svc *Service, tenantID, actorID string) (memoryID, assistantID string, cleanup func())
		reason      string
		statusCode  int
	}{
		{
			name: "no_active_policy",
			buildPolicy: func(svc *Service, tenantID, actorID string) (string, string, func()) {
				memory, err := svc.CreateMemory(t.Context(), tenantID, "unbound", []string{"raw"}, "e", "l")
				if err != nil {
					t.Fatal(err)
				}
				return memory.ID, "assistant-1", func() {}
			},
			reason: "no_active_policy_binding", statusCode: 403,
		},
		{
			name: "owner_scope_denied",
			buildPolicy: func(svc *Service, tenantID, actorID string) (string, string, func()) {
				owner, err := svc.CreateUser(t.Context(), tenantID, model.RoleBusinessUser, CreateUserRequest{
					Username: "memory-owner-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
				})
				if err != nil {
					t.Fatal(err)
				}
				policy, memory := activateSelfMemoryPolicy(t, svc, tenantID, owner.ID, "assistant-1")
				return memory.ID, "assistant-1", func() { _ = policy }
			},
			reason: "owner_scope_denied", statusCode: 404,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, tenant, user := newMemoryPolicySvc(t)
			memoryID, assistantID, cleanup := tc.buildPolicy(svc, tenant.ID, user.ID)
			defer cleanup()
			_, err := svc.SearchGovernedMemory(t.Context(), tenant.ID, user.ID, memoryID, GovernedMemorySearchInput{
				Query: "deployment", AssistantID: assistantID, SessionID: "session-1", RequestID: tc.name,
			})
			if err == nil {
				t.Fatal("denied search must fail")
			}
			httpErr, ok := err.(*httperr.Error)
			if !ok || httpErr.Status != tc.statusCode {
				t.Fatalf("unexpected error: %T %v", err, err)
			}
			decision, err := svc.GetConversationContextDecisionByRequest(t.Context(), tenant.ID, user.ID, tc.name)
			if err != nil {
				t.Fatalf("denial decision: %v", err)
			}
			if decision.Decision != "memory_denied" {
				t.Fatalf("unexpected denial: %+v", decision)
			}
			if svc.RAGFlow.(*ragflow.Mock).MemorySearchCallCount() != 0 {
				t.Fatal("denial must not call RAGFlow")
			}
		})
	}
}

func TestSearchGovernedMemoryProjectMembershipAndReadPolicy(t *testing.T) {
	svc, tenant, admin := newMemoryPolicySvc(t)
	ctx := context.Background()
	project := &model.Project{ID: id.New(), TenantID: tenant.ID, Name: "Memory Project"}
	if err := svc.Store.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, model.RoleBusinessUser, CreateUserRequest{
		Username: "project-member-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := svc.CreateMemory(ctx, tenant.ID, "project-memory", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, AssistantID: "assistant-1",
		RagflowMemoryID: memory.ID, State: model.MemoryPolicyActive,
		ReadPolicy: "project_member", WritePolicy: "deny", Config: "{}",
		PolicyVersion: 1, PolicyGeneration: 1, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	outsiderInput := GovernedMemorySearchInput{
		Query: "deployment", AssistantID: "assistant-1", SessionID: "session-1", RequestID: "project-outsider",
	}
	if _, err := svc.SearchGovernedMemory(ctx, tenant.ID, member.ID, memory.ID, outsiderInput); err == nil {
		t.Fatal("project non-member must fail")
	}
	if svc.RAGFlow.(*ragflow.Mock).MemorySearchCallCount() != 0 {
		t.Fatal("project non-member denial must not call RAGFlow")
	}
	if err := svc.AddProjectUser(ctx, tenant.ID, project.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RAGFlow.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memory.ID}, AgentID: "assistant-1", SessionID: "session-1",
		UserID: "project-contributor", UserInput: "project deployment", AgentResponse: "project answer",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.SearchGovernedMemory(ctx, tenant.ID, member.ID, memory.ID, GovernedMemorySearchInput{
		Query: "deployment", AssistantID: "assistant-1", SessionID: "session-1", RequestID: "project-member",
	})
	if err != nil {
		t.Fatalf("project member governed search: %v", err)
	}
	params := svc.RAGFlow.(*ragflow.Mock).LastMemorySearchParams()
	if params.UserID != "" || params.AgentID != "assistant-1" || params.SessionID != "session-1" {
		t.Fatalf("project search must not force actor attribution: %+v", params)
	}

	denied := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, AssistantID: "assistant-2", UserID: admin.ID,
		RagflowMemoryID: memory.ID, State: model.MemoryPolicyActive,
		ReadPolicy: "deny", WritePolicy: "deny", Config: "{}",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, denied); err == nil {
		t.Fatal("second active memory binding must fail")
	}
}

func TestMemoryRetrievalToolUsesGovernedRuntimeAndRedactsAudit(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	policy, memory := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	addGovernedMemoryMessage(t, svc, tenant.ID, user.ID, memory.ID)
	spec := builtinToolSpecs["memory_retrieval"]
	tool, err := svc.CreateToolRegistry(t.Context(), tenant.ID, user.ID, ToolRegistryInput{
		ToolID: spec.ToolID, Version: "v1", ToolType: spec.ToolType, Name: "Governed memory",
		InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: spec.ImplementationRef,
	})
	if err != nil {
		t.Fatalf("create memory tool: %v", err)
	}
	input, _ := json.Marshal(map[string]interface{}{
		"memory_id": memory.ID, "assistant_id": "assistant-1", "session_id": "session-1",
		"question": "deployment", "top_n": json.Number("5"),
	})
	result, err := svc.ExecuteToolRegistry(t.Context(), tenant.ID, user.ID, tool.ID, ToolExecutionInput{
		AnswerRunID: "answer-run-memory", Input: input,
	})
	if err != nil {
		t.Fatalf("execute memory tool: %v", err)
	}
	var output map[string]interface{}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output["policy_id"] != policy.ID || output["total"] != float64(1) {
		t.Fatalf("unexpected memory tool output: %+v", output)
	}
	messages, _ := output["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("unexpected memory tool messages: %+v", messages)
	}
	audits, _, err := svc.Store.ListAudits(t.Context(), tenant.ID, 1, 20, repository.AuditFilter{
		Action: "tool_registry.executed", Resource: "tool-registry",
	})
	if err != nil || len(audits) == 0 {
		t.Fatalf("tool audit: items=%d err=%v", len(audits), err)
	}
	if strings.Contains(audits[0].DetailJSON, "preferred deployment") || strings.Contains(audits[0].DetailJSON, "on-premise") {
		t.Fatalf("audit leaked memory content: %s", audits[0].DetailJSON)
	}
}

func newMemoryPolicySvc(t *testing.T) (*Service, *model.Tenant, *model.User) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-policy.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	svc := New(store, ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(context.Background(), "Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(context.Background(), tenant.ID, model.RolePlatformAdmin, CreateUserRequest{
		Username: "tenant-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc, &tenant, user
}

func TestMemoryPolicyControlPlaneFailClosed(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	project := &model.Project{ID: id.New(), TenantID: tenant.ID, Name: "Governed Project"}
	if err := svc.Store.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateMemoryPolicy(ctx, tenant.ID, user.ID, MemoryPolicyInput{
		Scope: "project", ProjectID: project.ID, AssistantID: "assistant-1",
		Config: `{"purpose":"support","read_policy":"project_member","write_policy":"allow","retention_days":30}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.State != model.MemoryPolicyPaused || policy.ConsentVersion != 0 {
		t.Fatalf("expected paused draft with zero consent, got state=%s consent=%d", policy.State, policy.ConsentVersion)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, user.ID, policy.ID, true); err == nil {
		t.Fatal("opt-in on paused project policy must fail closed")
	}
}

func TestSelfMemoryPolicyTemplateAndConsent(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	if _, err := svc.CreateSelfMemoryPolicy(ctx, tenant.ID, user.ID, "assistant-1"); err == nil {
		t.Fatal("self policy without tenant template must return 409")
	}
	template := `{"purpose":"assistant recall","read_policy":"owner","write_policy":"owner","retention_days":30,"embedding_model":"embedding-1","llm_model":"chat-1"}`
	if _, err := svc.UpdateSelfMemoryPolicyTemplate(ctx, tenant.ID, user.ID, SelfMemoryPolicyTemplateInput{Template: template}); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateSelfMemoryPolicy(ctx, tenant.ID, user.ID, "assistant-1")
	if err != nil {
		t.Fatal(err)
	}
	if policy.State != model.MemoryPolicyPaused {
		t.Fatal("self policy must start paused")
	}
	consented, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, user.ID, policy.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if consented.State != model.MemoryPolicyActive || consented.ConsentVersion != 1 {
		t.Fatalf("expected active consent v1, got state=%s consent=%d", consented.State, consented.ConsentVersion)
	}
	withdrawn, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, user.ID, policy.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != model.MemoryPolicyPaused || withdrawn.PolicyGeneration != consented.PolicyGeneration+1 {
		t.Fatalf("opt-out must pause policy and invalidate generation, got state=%s generation=%d", withdrawn.State, withdrawn.PolicyGeneration)
	}
}

func TestConversationContextSearchHasNoSideEffects(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	template := `{"purpose":"assistant recall","read_policy":"owner","write_policy":"owner","retention_days":30,"embedding_model":"embedding-1","llm_model":"chat-1"}`
	if _, err := svc.UpdateSelfMemoryPolicyTemplate(ctx, tenant.ID, user.ID, SelfMemoryPolicyTemplateInput{Template: template}); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateSelfMemoryPolicy(ctx, tenant.ID, user.ID, "assistant-1")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewConversationContext(ctx, tenant.ID, user.ID, ConversationContextPreviewInput{
		RequestKind: "search", AssistantID: "assistant-1", RequestID: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Decision.Decision != "memory_denied" || preview.Decision.MemoryPolicyID != "" {
		t.Fatal("inactive policy must deny memory")
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, user.ID, policy.ID, true); err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.PreviewConversationContext(ctx, tenant.ID, user.ID, ConversationContextPreviewInput{
		RequestKind: "search", AssistantID: "assistant-1", RequestID: "request-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Decision.Decision != "memory_allowed" {
		t.Fatal("active owner policy must allow read decision")
	}
	if allowed.Decision.MemoryPolicyID != policy.ID {
		t.Fatal("allowed decision must bind the selected memory policy")
	}
	consented, err := svc.GetMemoryPolicy(ctx, tenant.ID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if consented.RagflowMemoryID == "" {
		t.Fatal("self opt-in must create and bind a RAGFlow memory")
	}
	memory, err := svc.Store.GetMemoryShadow(ctx, tenant.ID, consented.RagflowMemoryID, false)
	if err != nil || memory == nil || memory.OwnerID != user.ID {
		t.Fatalf("self opt-in memory ownership failed: memory=%+v err=%v", memory, err)
	}
	idempotent, err := svc.PreviewConversationContext(ctx, tenant.ID, user.ID, ConversationContextPreviewInput{
		RequestKind: "search", AssistantID: "assistant-1", RequestID: "request-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.Decision.ID != allowed.Decision.ID {
		t.Fatal("same request_id must reuse the immutable context decision")
	}
}

func TestMemoryOperationObservationScopes(t *testing.T) {
	svc, tenant, admin := newMemoryPolicySvc(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project := &model.Project{ID: id.New(), TenantID: tenant.ID, Name: "Scoped Project"}
	if err := svc.Store.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, ReadPolicy: "project_member", WritePolicy: "deny",
		PolicyGeneration: 1, Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	business := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-business-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	teamAdmin := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-team-" + id.New(),
		Role: model.RoleTeamAdmin, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	for _, user := range []*model.User{business, teamAdmin} {
		if err := svc.Store.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.AssignUserRole(ctx, user.ID, user.Role); err != nil {
			t.Fatal(err)
		}
	}
	team := &model.Team{ID: id.New(), TenantID: tenant.ID, Name: "Memory Team", OwnerID: teamAdmin.ID, CreatedAt: now, UpdatedAt: now}
	if err := svc.Store.CreateTeam(ctx, team); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetTeamProjects(ctx, tenant.ID, team.ID, []string{project.ID}); err != nil {
		t.Fatal(err)
	}
	owned := &model.MemoryOperation{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, PolicyID: policy.ID,
		MemoryID: "memory-1", AssistantID: "assistant-1", UserID: business.ID,
		OperationType: "ingest", IdempotencyKey: "op-owned", State: model.MemoryOperationAccepted,
		PolicyGeneration: 1, ConsentVersion: 1, Detail: `{"reason":"observation"}`,
		CreatedAt: now, UpdatedAt: now,
	}
	other := &model.MemoryOperation{
		ID: id.New(), TenantID: tenant.ID, PolicyID: policy.ID,
		MemoryID: "memory-2", AssistantID: "assistant-1", UserID: admin.ID,
		OperationType: "tombstone", IdempotencyKey: "op-other", State: model.MemoryOperationDispatched,
		PolicyGeneration: 1, ConsentVersion: 1, TombstoneActive: true,
		Detail: `{"reason":"governance"}`, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []*model.MemoryOperation{owned, other} {
		if err := svc.Store.CreateMemoryOperation(ctx, operation); err != nil {
			t.Fatalf("create memory operation: %v", err)
		}
	}

	adminItems, adminTotal, err := svc.ListMemoryOperations(ctx, tenant.ID, admin.ID, repository.MemoryOperationFilter{}, 1, 20)
	if err != nil || adminTotal != 2 || len(adminItems) != 2 {
		t.Fatalf("tenant governance scope: items=%d total=%d err=%v", len(adminItems), adminTotal, err)
	}
	businessItems, businessTotal, err := svc.ListMemoryOperations(ctx, tenant.ID, business.ID, repository.MemoryOperationFilter{
		UserID: admin.ID,
	}, 1, 20)
	if err != nil || businessTotal != 1 || len(businessItems) != 1 ||
		businessItems[0].ID != owned.ID || businessItems[0].UserID != business.ID {
		t.Fatalf("own scope: items=%+v total=%d err=%v", businessItems, businessTotal, err)
	}
	teamItems, teamTotal, err := svc.ListMemoryOperations(ctx, tenant.ID, teamAdmin.ID, repository.MemoryOperationFilter{}, 1, 20)
	if err != nil || teamTotal != 1 || len(teamItems) != 1 || teamItems[0].ProjectID != project.ID {
		t.Fatalf("team scope: items=%+v total=%d err=%v", teamItems, teamTotal, err)
	}
	if _, err := svc.GetMemoryOperation(ctx, tenant.ID, teamAdmin.ID, other.ID); err == nil {
		t.Fatal("team admin must not read an unscoped operation")
	}
	if _, _, err := svc.ListMemoryOperations(ctx, tenant.ID, teamAdmin.ID, repository.MemoryOperationFilter{
		ProjectID: id.New(),
	}, 1, 20); err == nil {
		t.Fatal("team admin project filter outside owned teams must fail closed")
	}
	for _, operation := range append(businessItems, teamItems...) {
		if operation.Detail != "" {
			t.Fatal("memory operation observation response must not expose detail payloads")
		}
	}
}

package service

// 137 §11.1 governance metric wiring tests (milestone 211). Each test asserts
// that the governance counters move on the exact service outcomes they are
// contractually tied to. They never enable compiled retrieval, auto
// enhancement, default routing, default Memory writes or the Release Gate.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const govMetricsNamespace = "govm211"

func newGovernanceMetrics(t *testing.T) *obs.Observability {
	t.Helper()
	metrics := obs.New(config.Observability{MetricsEnabled: true, MetricsNamespace: govMetricsNamespace})
	obs.Set(metrics)
	t.Cleanup(func() { obs.Set(nil) })
	return metrics
}

func govMetric(metrics *obs.Observability, name string, labels ...string) float64 {
	return metrics.MetricValue(govMetricsNamespace+"_"+name, labels...)
}

func TestGovernanceMetricsMemoryDispatchExternalResultAndPrivacy(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "Governance External Result")
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-gov-executed", IdempotencyKey: "idempotency-gov-executed",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	})
	if err != nil {
		t.Fatalf("create memory dispatch operation: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil || stored.State != model.MemoryOperationExecuted {
		t.Fatalf("operation must execute: %+v err=%v", stored, err)
	}
	if got := govMetric(metrics, "memory_dispatch_external_result_total", "write", "executed"); got != 1 {
		t.Fatalf("memory_dispatch_external_result_total{write,executed} = %v, want 1", got)
	}
	if got := govMetric(metrics, "memory_privacy_gate_decision_total", "ALLOW"); got != 1 {
		t.Fatalf("memory_privacy_gate_decision_total{ALLOW} = %v, want 1", got)
	}
	if got := govMetric(metrics, "memory_dispatch_principal_mapping_total", "success", ""); got < 1 {
		t.Fatalf("memory_dispatch_principal_mapping_total{success} = %v, want >= 1", got)
	}
}

func TestGovernanceMetricsPrivacyBlockDecision(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "Governance Privacy Block")
	_, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-gov-block", IdempotencyKey: "idempotency-gov-block",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "preferred deployment", AgentResponse: "password=hunter2secret",
	})
	if err == nil {
		t.Fatal("high-risk credential payload must be blocked")
	}
	if he, ok := err.(*httperr.Error); !ok || he.Code != 42220 {
		t.Fatalf("expected privacy block 42220, got %v", err)
	}
	if got := govMetric(metrics, "memory_privacy_gate_decision_total", "BLOCK"); got != 1 {
		t.Fatalf("memory_privacy_gate_decision_total{BLOCK} = %v, want 1", got)
	}
}

func TestGovernanceMetricsPendingInvalidatedUserActionAndRevocationEvent(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "Governance Pending Invalidated")
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-gov-pending", IdempotencyKey: "idempotency-gov-pending",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	})
	if err != nil {
		t.Fatalf("create memory dispatch operation: %v", err)
	}
	// Owner revokes consent before the dispatch worker runs: the accepted
	// operation must be invalidated at admission and classified user_action.
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, target.ID, policy.ID, false); err != nil {
		t.Fatalf("opt-out: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil || stored.State != model.MemoryOperationBlocked {
		t.Fatalf("stale-consent operation must block at admission: %+v err=%v", stored, err)
	}
	if got := govMetric(metrics, "memory_dispatch_pending_invalidated_total", "user_action"); got != 1 {
		t.Fatalf("memory_dispatch_pending_invalidated_total{user_action} = %v, want 1", got)
	}
	if got := govMetric(metrics, "memory_dispatch_revocation_event_total", "owner_opt_out"); got != 1 {
		t.Fatalf("memory_dispatch_revocation_event_total{owner_opt_out} = %v, want 1", got)
	}
}

func TestGovernanceMetricsUnauthorizedDispatchTripwireAndReasonClasses(t *testing.T) {
	metrics := newGovernanceMetrics(t)
	if got := govMetric(metrics, "memory_dispatch_unauthorized_external_dispatch_total", "write"); got != 0 {
		t.Fatalf("unauthorized dispatch tripwire must stay zero, got %v", got)
	}
	for reason, want := range map[string]string{
		"memory_dispatch_tombstone_active":             "user_action",
		"memory_dispatch_policy_not_active":            "user_action",
		"memory_dispatch_consent_not_current":          "user_action",
		"memory_dispatch_personal_consent_stale":       "user_action",
		"memory_dispatch_personal_consent_withdrawn":   "user_action",
		"memory_dispatch_principal_not_project_member": "user_action",
		"memory_dispatch_policy_generation_stale":      "system_mismatch",
		"memory_dispatch_identity_drift":               "system_mismatch",
		"memory_dispatch_principal_not_active":         "system_mismatch",
		"memory_dispatch_personal_consent_missing":     "system_mismatch",
	} {
		if got := memoryPendingInvalidatedReasonClass(reason); got != want {
			t.Fatalf("reason class(%s) = %s, want %s", reason, got, want)
		}
	}
	// The tripwire only increments when the fail-closed invariant is violated.
	obs.Get().IncMemoryUnauthorizedExternalDispatch("write")
	if got := govMetric(metrics, "memory_dispatch_unauthorized_external_dispatch_total", "write"); got != 1 {
		t.Fatalf("tripwire must increment when the invariant is violated, got %v", got)
	}
}

func TestGovernanceMetricsPreModelAuthClassification(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, tenant, user := newMemoryPolicySvc(t)
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-gov-denied", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-none", SessionID: "session-1", Query: "anything",
	}); err != nil {
		t.Fatalf("denied path must still record a decision: %v", err)
	}
	if got := govMetric(metrics, "context_pre_model_auth_total", "denied"); got != 1 {
		t.Fatalf("context_pre_model_auth_total{denied} = %v, want 1", got)
	}
	if _, policy := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-gov-auth"); policy == nil {
		t.Fatal("policy fixture missing")
	}
	decision, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-gov-allowed", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-gov-auth", SessionID: "session-1", Query: "deployment",
	})
	if err != nil || decision.Decision != "memory_allowed" {
		t.Fatalf("authorized path: decision=%+v err=%v", decision, err)
	}
	if got := govMetric(metrics, "context_pre_model_auth_total", "authorized"); got != 1 {
		t.Fatalf("context_pre_model_auth_total{authorized} = %v, want 1", got)
	}
	if got := govMetric(metrics, "context_decision_duration_seconds", model.ContextDecisionChat); got != 2 {
		t.Fatalf("context decision latency samples = %v, want 2", got)
	}
}

func TestGovernanceMetricsSearchAuthorizedFilterAndMapping(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, tenant, user := newMemoryPolicySvc(t)
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	addGovernedMemoryMessage(t, svc, tenant.ID, user.ID, policy.RagflowMemoryID)
	result, err := svc.SearchGovernedMemory(ctx, tenant.ID, user.ID, policy.RagflowMemoryID, GovernedMemorySearchInput{
		Query: "deployment", AssistantID: policy.AssistantID, SessionID: "session-1",
		RequestID: "request-gov-search",
	})
	if err != nil {
		t.Fatalf("governed search: %v", err)
	}
	if result == nil || len(result.Messages) != 1 {
		t.Fatalf("governed search must return the fixture message: %+v", result)
	}
	if got := govMetric(metrics, "memory_search_authorized_filter_total", "authorized"); got != 1 {
		t.Fatalf("memory_search_authorized_filter_total{authorized} = %v, want 1", got)
	}
	if got := govMetric(metrics, "memory_search_authorized_filter_total", "unauthorized"); got != 0 {
		t.Fatalf("unauthorized search filter must stay zero, got %v", got)
	}
	if got := govMetric(metrics, "memory_dispatch_principal_mapping_total", "success", ""); got < 1 {
		t.Fatalf("memory_dispatch_principal_mapping_total{success} = %v, want >= 1", got)
	}
	if got := govMetric(metrics, "context_decision_duration_seconds", model.ContextDecisionSearch); got != 1 {
		t.Fatalf("search decision latency samples = %v, want 1", got)
	}
}

func newGovernanceTombstoneFixture(t *testing.T, name string) (*Service, *gorm.DB, *ragflow.Mock, *model.User, *model.MemoryPolicy, string) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gov-metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	svc := New(store, ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	tenant, err := svc.CreateTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "gov-tombstone-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: name, MemoryType: []string{"raw"}, EmbdID: "embedding-model", LLMID: "chat-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: memoryID, State: model.MemoryPolicyActive,
		ReadPolicy: "owner", WritePolicy: "owner", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return svc, gdb, mock, target, policy, memoryID
}

func TestGovernanceMetricsTombstoneVerificationAndRevocationEvent(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, gdb, mock, target, policy, memoryID := newGovernanceTombstoneFixture(t, "Governance Tombstone Verify")
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-gov-tombstone", IdempotencyKey: "idempotency-gov-tombstone",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	})
	if err != nil {
		t.Fatalf("create memory dispatch operation: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	if _, err := svc.TombstoneMemoryOperation(ctx, policy.TenantID, target.ID, operation.ID); err != nil {
		t.Fatalf("tombstone operation: %v", err)
	}
	if got := govMetric(metrics, "memory_dispatch_revocation_event_total", "tombstone"); got != 1 {
		t.Fatalf("memory_dispatch_revocation_event_total{tombstone} = %v, want 1", got)
	}
	// Age the tombstone past the visibility grace so the compensation scan
	// claims it (same trick as ageMemoryTombstone in the compensation tests).
	visibleAt := time.Now().UTC().Add(-memoryCompensationVisibilityGrace - time.Minute)
	if err := gdb.Model(&model.MemoryOperation{}).
		Where("tenant_id = ? AND id = ?", policy.TenantID, operation.ID).
		Update("tombstone_created_at", visibleAt).Error; err != nil {
		t.Fatalf("age tombstone: %v", err)
	}
	summary, err := svc.ProcessMemoryCompensations(ctx, 10)
	if err != nil {
		t.Fatalf("process compensations: %v", err)
	}
	if summary.Compensated != 1 {
		t.Fatalf("tombstone compensation must remove the external message: %+v", summary)
	}
	if got := govMetric(metrics, "memory_tombstone_verification_total", "verified"); got != 1 {
		t.Fatalf("memory_tombstone_verification_total{verified} = %v, want 1", got)
	}
	messages, _ := mock.ListMemoryMessages(ctx, memoryID)
	for _, message := range messages {
		if memoryMessageActive(message) {
			t.Fatalf("external message must be forgotten after compensation: %+v", message)
		}
	}
}

func TestGovernanceMetricsCompilationRunTerminal(t *testing.T) {
	ctx := context.Background()
	metrics := newGovernanceMetrics(t)
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	dsl := compilationPipelineDSL()
	components := dsl["components"].(map[string]interface{})
	delete(components, "compiler")
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, dsl)
	svc.SetupWorker(fastWorkerConfig())

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-gov-terminal", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("process invalid compilation run: %v", err)
	}
	failed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || failed == nil || failed.State != model.CompilationRunFailed {
		t.Fatalf("missing compiler must fail the run: %+v err=%v", failed, err)
	}
	if got := govMetric(metrics, "compilation_run_terminal_total", tenant, "failed"); got != 1 {
		t.Fatalf("compilation_run_terminal_total{failed} = %v, want 1", got)
	}
	if got := govMetric(metrics, "compilation_ready_eval_total", tenant, "incomplete"); got != 0 {
		t.Fatalf("failed run must not evaluate readiness, got %v", got)
	}
}

func TestGovernanceMetricsRetrievalModeCounter(t *testing.T) {
	metrics := newGovernanceMetrics(t)
	obs.Get().AddRetrievalMode("compiled_first", "compiled_leaded")
	obs.Get().AddRetrievalMode("compiled_first", "raw_fallback")
	if got := govMetric(metrics, "retrieval_mode_total", "compiled_first", "compiled_leaded"); got != 1 {
		t.Fatalf("retrieval_mode_total{compiled_leaded} = %v, want 1", got)
	}
	if got := govMetric(metrics, "retrieval_mode_total", "compiled_first", "raw_fallback"); got != 1 {
		t.Fatalf("retrieval_mode_total{raw_fallback} = %v, want 1", got)
	}
}

func TestGovernanceMetricsCompilationQueueDepthGauge(t *testing.T) {
	metrics := newGovernanceMetrics(t)
	obs.Get().SetCompilationQueueDepth("tenant-q", "wiki", 3, 12)
	if got := govMetric(metrics, "compilation_queue_depth", "tenant-q", "wiki", "inflight"); got != 3 {
		t.Fatalf("compilation_queue_depth{inflight} = %v, want 3", got)
	}
	if got := govMetric(metrics, "compilation_queue_depth", "tenant-q", "wiki", "backlog"); got != 12 {
		t.Fatalf("compilation_queue_depth{backlog} = %v, want 12", got)
	}
	obs.Get().ClearCompilationQueueDepth("tenant-q", "wiki")
	if got := govMetric(metrics, "compilation_queue_depth", "tenant-q", "wiki", "backlog"); got != 0 {
		t.Fatalf("compilation_queue_depth{backlog} after clear = %v, want 0", got)
	}
}

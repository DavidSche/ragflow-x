package service

import (
	"context"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"testing"
	"time"
)

func TestKnowledgeTaskCreateValidationAndSourceEvent(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Knowledge Task Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "task-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		Title: " ", Category: model.FeedbackAttributionKnowledge, OwnerID: "owner",
	}); err == nil {
		t.Fatal("empty title must be rejected")
	}
	if _, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		Title: "bad", Category: "unknown", OwnerID: "owner",
	}); err == nil {
		t.Fatal("invalid category must be rejected")
	}
	if _, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		Title: "missing event", Category: model.FeedbackAttributionKnowledge, OwnerID: "owner",
		SourceEventID: "missing",
	}); err == nil {
		t.Fatal("missing source event must be rejected")
	}
	now := time.Now().UTC()
	if err := svc.Store.CreateEvalSetWithCases(ctx, &model.EvalSet{
		ID: "set-task-1", TenantID: tenant.ID, Name: "knowledge task regression", Source: model.EvalSourceManual,
		Version: 1, ItemCount: 1, Status: model.EvalStatusPublished, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}, []model.EvalCase{{
		ID: "case-task-1", TenantID: tenant.ID, EvalSetID: "set-task-1", Question: "知识是否生效?",
		Source: model.EvalSourceManual, Status: model.EvalStatusPublished, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	event := &model.KnowledgeOpsEvent{
		RequestID: "request-task", TenantID: tenant.ID, UserID: "user", AppType: "chat",
		AppID: "chat-1", SessionID: "session", Question: "坏了", QuestionHash: "hash",
		AnswerExcerpt: "bad", Status: model.KnowledgeOpsCompleted, FeedbackRating: model.FeedbackNegative,
		FeedbackAttribution: model.FeedbackAttributionTemplate,
	}
	if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		SourceEventID: event.ID, Title: "修正模板口径", Description: "更新 Prompt",
		Category: model.FeedbackAttributionTemplate, OwnerID: "owner",
		Priority: model.KnowledgeTaskPriorityHigh,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != model.KnowledgeTaskOpen || task.SourceRequestID != event.RequestID ||
		task.SourceAttribution != event.FeedbackAttribution {
		t.Fatalf("unexpected source projection: %+v", task)
	}
}

func TestKnowledgeTaskDirectRequestContext(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Direct Task Context Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "direct-task-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "session-direct", AssistantID: "chat-1",
		PrincipalID: admin.ID, Question: "采购制度", RequestID: "request-direct-task",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Summary: "summary", Content: "answer",
		Citations: []model.AnswerCitation{{
			ID: "citation-1", DatasetID: "dataset-1", DocumentID: "doc-1",
			DocumentVersion: "v3", ChunkID: "chunk-1",
			CitedContentExcerpt: "采购流程", CitationLocator: "policy.md#L1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		SourceRequestID: "request-direct-task", Title: "补充采购制度",
		Category: model.FeedbackAttributionKnowledge, OwnerID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceRequestID != "request-direct-task" ||
		task.SourceAnswerRunID != answer.Run.ID || task.SourceAnswerSnapshotID != answer.Snapshot.ID {
		t.Fatalf("direct request context incomplete: %+v", task)
	}
	if task.SourceDatasetID != "dataset-1" || task.SourceCitationID != "citation-1" ||
		task.SourceChunkID != "chunk-1" || task.SourceDocumentVersion != "v3" {
		t.Fatalf("citation context incomplete: %+v", task)
	}
}

func TestKnowledgeTaskUpdateRequiresRegression(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Task Regression Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "regression-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateKnowledgeTask(ctx, tenant.ID, admin.ID, KnowledgeTaskInput{
		Title: "更新文档", Category: model.FeedbackAttributionKnowledge, OwnerID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	regressionNow := time.Now().UTC()
	if err := svc.Store.CreateEvalSetWithCases(ctx, &model.EvalSet{
		ID: "set-task-1", TenantID: tenant.ID, Name: "knowledge task regression", Source: model.EvalSourceManual,
		Version: 1, ItemCount: 1, Status: model.EvalStatusPublished, CreatedBy: admin.ID, CreatedAt: regressionNow, UpdatedAt: regressionNow,
	}, []model.EvalCase{{
		ID: "case-task-1", TenantID: tenant.ID, EvalSetID: "set-task-1", Question: "知识是否生效?",
		Source: model.EvalSourceManual, Status: model.EvalStatusPublished, CreatedBy: admin.ID, CreatedAt: regressionNow, UpdatedAt: regressionNow,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateKnowledgeTask(ctx, admin.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskResolved, ResolutionNote: "done",
		RegressionEvalSetID: "missing-set", RegressionEvalCaseID: "missing-case",
		RegressionStatus: model.KnowledgeTaskRegressionPassed,
	}); err == nil {
		t.Fatal("resolve without passed regression must be rejected")
	}
	if _, err := svc.UpdateKnowledgeTask(ctx, admin.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskInProgress, RegressionEvalSetID: "set-task-1",
		RegressionEvalCaseID: "missing-case", RegressionStatus: model.KnowledgeTaskRegressionPassed,
	}); err == nil {
		t.Fatal("case outside eval set must be rejected")
	}
	if _, err := svc.UpdateKnowledgeTask(ctx, admin.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskInProgress, RegressionEvalSetID: "set-task-1",
		RegressionEvalCaseID: "case-task-1", RegressionStatus: model.KnowledgeTaskRegressionPassed,
	}); err != nil {
		t.Fatalf("update task: %v", err)
	}
	updated, err := svc.UpdateKnowledgeTask(ctx, admin.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskResolved, ResolutionNote: "知识已更新",
	})
	if err != nil {
		t.Fatalf("resolve task: %v", err)
	}
	if updated.Status != model.KnowledgeTaskResolved || updated.ResolvedAt == nil {
		t.Fatalf("unexpected resolved task: %+v", updated)
	}
}

func TestKnowledgeTaskApprovalGateAndExecutor(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 1, PolicyCacheTTLSec: 0,
	})
	tenant, err := svc.CreateTenant(ctx, "Task Approval Tenant")
	if err != nil {
		t.Fatal(err)
	}
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "task-requester", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "task-approver", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "knowledge-task-policy", TenantID: tenant.ID,
		ObjectType: model.ApprovalObjectKnowledgeTask, Action: model.ApprovalActionResolve,
		Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"knowledge task approval","approver_type":"user","approver_value":"` + approver.ID + `","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	approvalNow := time.Now().UTC()
	if err := svc.Store.CreateEvalSetWithCases(ctx, &model.EvalSet{
		ID: "set-approval-1", TenantID: tenant.ID, Name: "approval regression", Source: model.EvalSourceManual,
		Version: 1, ItemCount: 1, Status: model.EvalStatusPublished, CreatedBy: requester.ID, CreatedAt: approvalNow, UpdatedAt: approvalNow,
	}, []model.EvalCase{{
		ID: "case-approval-1", TenantID: tenant.ID, EvalSetID: "set-approval-1", Question: "审批回归是否通过?",
		Source: model.EvalSourceManual, Status: model.EvalStatusPublished, CreatedBy: requester.ID, CreatedAt: approvalNow, UpdatedAt: approvalNow,
	}}); err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateKnowledgeTask(ctx, tenant.ID, requester.ID, KnowledgeTaskInput{
		Title: "补充制度", Category: model.FeedbackAttributionKnowledge, OwnerID: "owner",
		RequiresApproval: true,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	held, err := svc.UpdateKnowledgeTask(ctx, requester.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskResolved, ResolutionNote: "制度已补充",
		RegressionEvalSetID: "set-approval-1", RegressionEvalCaseID: "case-approval-1",
		RegressionStatus: model.KnowledgeTaskRegressionPassed,
	})
	if err != nil {
		t.Fatalf("approval hold: %v", err)
	}
	if held.Status != model.KnowledgeTaskPendingApproval || held.ApprovalID == "" {
		t.Fatalf("task was not held: %+v", held)
	}
	approval, err := svc.Store.GetApproval(ctx, tenant.ID, held.ApprovalID)
	if err != nil || approval == nil {
		t.Fatalf("get approval: approval=%+v err=%v", approval, err)
	}
	if _, err := svc.CancelApproval(ctx, requester.ID, tenant.ID, approval.ID, "申请人取消"); err != nil {
		t.Fatalf("cancel approval: %v", err)
	}
	reopened, err := svc.Store.GetKnowledgeTask(ctx, tenant.ID, task.ID, false)
	if err != nil || reopened.Status != model.KnowledgeTaskOpen || reopened.ApprovalID != "" {
		t.Fatalf("canceled approval did not reopen task: task=%+v err=%v", reopened, err)
	}
	if _, err := svc.UpdateKnowledgeTask(ctx, requester.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskInProgress, RegressionEvalSetID: "set-approval-1",
		RegressionEvalCaseID: "case-approval-1", RegressionStatus: model.KnowledgeTaskRegressionPassed,
	}); err != nil {
		t.Fatalf("restart task after cancel: %v", err)
	}
	held, err = svc.UpdateKnowledgeTask(ctx, requester.ID, tenant.ID, task.ID, KnowledgeTaskUpdateInput{
		Status: model.KnowledgeTaskResolved, ResolutionNote: "制度已补充",
		RegressionEvalSetID: "set-approval-1", RegressionEvalCaseID: "case-approval-1",
		RegressionStatus: model.KnowledgeTaskRegressionPassed,
	})
	if err != nil {
		t.Fatalf("resubmit approval after cancel: %v", err)
	}
	if held.Status != model.KnowledgeTaskPendingApproval || held.ApprovalID == "" {
		t.Fatalf("task was not held again: %+v", held)
	}
	approval, err = svc.Store.GetApproval(ctx, tenant.ID, held.ApprovalID)
	if err != nil || approval == nil {
		t.Fatalf("get resubmitted approval: approval=%+v err=%v", approval, err)
	}
	executor, ok := svc.approvalExecutorRegistry().Get(model.ApprovalObjectKnowledgeTask + "." + model.ApprovalActionResolve)
	if !ok {
		t.Fatal("knowledge task approval executor is not registered")
	}
	if err := executor.Validate(ctx, approval); err != nil {
		t.Fatalf("validate approval: %v", err)
	}
	result, err := executor.Execute(ctx, approval)
	if err != nil {
		t.Fatalf("execute approval: %v", err)
	}
	if result["resolved"] != true {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	resolved, err := svc.Store.GetKnowledgeTask(ctx, tenant.ID, task.ID, false)
	if err != nil || resolved.Status != model.KnowledgeTaskResolved || resolved.ResolvedAt == nil {
		t.Fatalf("executor did not resolve task: task=%+v err=%v", resolved, err)
	}
}

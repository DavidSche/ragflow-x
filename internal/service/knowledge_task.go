package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type KnowledgeTaskInput struct {
	SourceEventID    string     `json:"source_event_id"`
	SourceRequestID  string     `json:"source_request_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Category         string     `json:"category"`
	OwnerID          string     `json:"owner_id"`
	DueAt            *time.Time `json:"due_at"`
	Priority         string     `json:"priority"`
	RequiresApproval bool       `json:"requires_approval"`
}

type KnowledgeTaskUpdateInput struct {
	Status               string     `json:"status"`
	OwnerID              string     `json:"owner_id"`
	DueAt                *time.Time `json:"due_at"`
	Priority             string     `json:"priority"`
	ResolutionNote       string     `json:"resolution_note"`
	RegressionEvalSetID  string     `json:"regression_eval_set_id"`
	RegressionEvalCaseID string     `json:"regression_eval_case_id"`
	RegressionStatus     string     `json:"regression_status"`
}

func (s *Service) CreateKnowledgeTask(ctx context.Context, tenantID, actorID string, input KnowledgeTaskInput) (*model.KnowledgeTask, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(input.Title)
	ownerID := strings.TrimSpace(input.OwnerID)
	if title == "" || len(title) > 255 {
		return nil, httperr.BadRequest(40097, "title is required and must be 1-255 characters")
	}
	if ownerID == "" {
		return nil, httperr.BadRequest(40097, "owner_id is required")
	}
	if !validKnowledgeTaskCategory(input.Category) {
		return nil, httperr.BadRequest(40097, "category must be knowledge, retrieval, template, model, routing or tool")
	}
	if !validKnowledgeTaskPriority(input.Priority) {
		return nil, httperr.BadRequest(40097, "priority must be low, medium, high or critical")
	}
	task := &model.KnowledgeTask{
		TenantID: tenantID, SourceEventID: input.SourceEventID, Title: title,
		Description: strings.TrimSpace(input.Description), Category: input.Category,
		OwnerID: ownerID, DueAt: input.DueAt, Priority: input.Priority,
		Status: model.KnowledgeTaskOpen, RequiresApproval: input.RequiresApproval,
		RegressionStatus: model.KnowledgeTaskRegressionPending, CreatedBy: actorID,
	}
	if input.SourceEventID != "" {
		event, err := s.Store.GetKnowledgeOpsEvent(ctx, tenantID, input.SourceEventID, false)
		if err != nil {
			return nil, err
		}
		if event == nil {
			return nil, httperr.NotFound("knowledge ops event not found")
		}
		task.SourceRequestID = event.RequestID
		task.SourceAttribution = event.FeedbackAttribution
		if event.FeedbackAttribution == "" {
			task.SourceAttribution = "unclassified"
		}
		s.applyAnswerDeliveryTaskContext(ctx, tenantID, event.RequestID, task)
	} else if strings.TrimSpace(input.SourceRequestID) != "" {
		task.SourceRequestID = strings.TrimSpace(input.SourceRequestID)
		task.SourceAttribution = "unclassified"
		s.applyAnswerDeliveryTaskContext(ctx, tenantID, task.SourceRequestID, task)
	}
	if err := s.Store.CreateKnowledgeTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) applyAnswerDeliveryTaskContext(ctx context.Context, tenantID, requestID string, task *model.KnowledgeTask) {
	run, err := s.Store.GetAnswerRunByRequest(ctx, tenantID, requestID)
	if err != nil || run == nil || run.ID == "" {
		return
	}
	task.SourceAnswerRunID = run.ID
	task.SourceTraceID = run.TraceID
	task.SourceAssistantID = run.AssistantID
	task.SourceAssistantReleaseID = run.AssistantReleaseID
	snapshot, err := s.Store.GetAnswerSnapshotByRequest(ctx, tenantID, requestID)
	if err != nil || snapshot == nil || snapshot.ID == "" {
		return
	}
	task.SourceAnswerSnapshotID = snapshot.ID
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &citations); err == nil && len(citations) > 0 {
		task.SourceCitationID = citations[0].ID
		task.SourceDatasetID = citations[0].DatasetID
		task.SourceChunkID = citations[0].ChunkID
		task.SourceCitationHash = citations[0].CitationContentHash
		task.SourceDocumentVersion = citations[0].DocumentVersion
	}
}

func (s *Service) GetKnowledgeTask(ctx context.Context, actorID, tenantID, taskID string) (*model.KnowledgeTask, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, err
	}
	scopeAll := s.KnowledgeOpsScopeAll(ctx, actorID)
	task, err := s.Store.GetKnowledgeTask(ctx, tenantID, taskID, scopeAll)
	if err != nil {
		return nil, err
	}
	if task.ID == "" {
		return nil, httperr.NotFound("knowledge task not found")
	}
	return task, nil
}

func (s *Service) ListKnowledgeTasks(ctx context.Context, actorID, tenantID string, page, pageSize int, filter repository.KnowledgeTaskFilter) ([]model.KnowledgeTask, int64, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, 0, err
	}
	scopeAll := s.KnowledgeOpsScopeAll(ctx, actorID)
	return s.Store.ListKnowledgeTasks(ctx, tenantID, scopeAll, page, pageSize, filter)
}

func (s *Service) KnowledgeTaskSummary(ctx context.Context, actorID, tenantID string) (*model.KnowledgeTaskSummary, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, err
	}
	scopeAll := s.KnowledgeOpsScopeAll(ctx, actorID)
	return s.Store.KnowledgeTaskSummary(ctx, tenantID, scopeAll)
}

func (s *Service) UpdateKnowledgeTask(ctx context.Context, actorID, tenantID, taskID string, input KnowledgeTaskUpdateInput) (*model.KnowledgeTask, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	scopeAll := s.KnowledgeOpsScopeAll(ctx, actorID)
	task, err := s.Store.GetKnowledgeTask(ctx, tenantID, taskID, scopeAll)
	if err != nil {
		return nil, err
	}
	if task.ID == "" {
		return nil, httperr.NotFound("knowledge task not found")
	}
	updates := map[string]interface{}{}
	if input.OwnerID != "" {
		updates["owner_id"] = strings.TrimSpace(input.OwnerID)
	}
	if input.Priority != "" {
		if !validKnowledgeTaskPriority(input.Priority) {
			return nil, httperr.BadRequest(40097, "priority must be low, medium, high or critical")
		}
		updates["priority"] = input.Priority
	}
	if input.DueAt != nil {
		updates["due_at"] = input.DueAt
	}
	if input.RegressionEvalSetID != "" || input.RegressionEvalCaseID != "" {
		if input.RegressionEvalSetID == "" || input.RegressionEvalCaseID == "" {
			return nil, httperr.BadRequest(40097, "regression eval set and case are required together")
		}
		if err := validateKnowledgeTaskRegression(ctx, s, tenantID, input.RegressionEvalSetID, input.RegressionEvalCaseID); err != nil {
			return nil, err
		}
	}
	if input.RegressionEvalSetID != "" {
		updates["regression_eval_set_id"] = input.RegressionEvalSetID
	}
	if input.RegressionEvalCaseID != "" {
		updates["regression_eval_case_id"] = input.RegressionEvalCaseID
	}
	if input.RegressionStatus != "" {
		if !validKnowledgeTaskRegressionStatus(input.RegressionStatus) {
			return nil, httperr.BadRequest(40097, "regression status must be pending, passed or failed")
		}
		updates["regression_status"] = input.RegressionStatus
	}
	if input.Status != "" {
		if !validKnowledgeTaskTransition(task, input.Status) {
			return nil, httperr.New(409, 40098, "invalid knowledge task status transition")
		}
		updates["status"] = input.Status
	}
	if input.Status == model.KnowledgeTaskResolved {
		if strings.TrimSpace(input.ResolutionNote) == "" && task.ResolutionNote == "" {
			return nil, httperr.BadRequest(40097, "resolution_note is required to resolve a knowledge task")
		}
		if input.ResolutionNote != "" {
			updates["resolution_note"] = strings.TrimSpace(input.ResolutionNote)
		}
		regressionSet := input.RegressionEvalSetID
		if regressionSet == "" {
			regressionSet = task.RegressionEvalSetID
		}
		regressionCase := input.RegressionEvalCaseID
		if regressionCase == "" {
			regressionCase = task.RegressionEvalCaseID
		}
		regressionStatus := input.RegressionStatus
		if regressionStatus == "" {
			regressionStatus = task.RegressionStatus
		}
		if regressionSet == "" || regressionCase == "" || regressionStatus != model.KnowledgeTaskRegressionPassed {
			return nil, httperr.BadRequest(40097, "resolved tasks require a passed regression eval set and case")
		}
		if err := validateKnowledgeTaskRegression(ctx, s, tenantID, regressionSet, regressionCase); err != nil {
			return nil, err
		}
		if task.RequiresApproval {
			approval, approvalErr := s.SubmitApproval(ctx, actorID, ApprovalSubmitRequest{
				TenantID: tenantID, ObjectType: model.ApprovalObjectKnowledgeTask,
				Action: model.ApprovalActionResolve, ObjectID: task.ID,
				Title:  "Resolve knowledge task: " + task.Title,
				Reason: strings.TrimSpace(input.ResolutionNote),
				Payload: map[string]any{
					"resolution_note":         strings.TrimSpace(input.ResolutionNote),
					"regression_eval_set_id":  regressionSet,
					"regression_eval_case_id": regressionCase,
					"regression_status":       regressionStatus,
				},
				IdempotencyKey: "knowledge-task-resolve-" + task.ID + "-" + strconv.FormatInt(task.UpdatedAt.UnixNano(), 10),
			})
			if approvalErr != nil {
				return nil, approvalErr
			}
			if approval == nil {
				return nil, httperr.BadRequest(40099, "approval policy is required for governed knowledge task resolution")
			}
			updates["status"] = model.KnowledgeTaskPendingApproval
			updates["approval_id"] = approval.ID
		} else {
			now := time.Now().UTC()
			updates["resolved_at"] = now
		}
	}
	if len(updates) == 0 {
		return task, nil
	}
	updated, err := s.Store.UpdateKnowledgeTask(ctx, tenantID, task.ID, scopeAll, updates)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, httperr.NotFound("knowledge task not found")
	}
	return s.Store.GetKnowledgeTask(ctx, tenantID, task.ID, scopeAll)
}

func validKnowledgeTaskCategory(category string) bool {
	switch category {
	case model.FeedbackAttributionKnowledge, model.FeedbackAttributionRetrieval,
		model.FeedbackAttributionTemplate, model.FeedbackAttributionModel,
		model.FeedbackAttributionRouting, model.FeedbackAttributionTool:
		return true
	default:
		return false
	}
}

func validKnowledgeTaskPriority(priority string) bool {
	if priority == "" {
		return true
	}
	switch priority {
	case model.KnowledgeTaskPriorityLow, model.KnowledgeTaskPriorityMedium,
		model.KnowledgeTaskPriorityHigh, model.KnowledgeTaskPriorityCritical:
		return true
	default:
		return false
	}
}

func validKnowledgeTaskRegressionStatus(status string) bool {
	switch status {
	case model.KnowledgeTaskRegressionPending, model.KnowledgeTaskRegressionPassed, model.KnowledgeTaskRegressionFailed:
		return true
	default:
		return false
	}
}

func validKnowledgeTaskTransition(task *model.KnowledgeTask, next string) bool {
	switch next {
	case model.KnowledgeTaskOpen, model.KnowledgeTaskInProgress, model.KnowledgeTaskBlocked,
		model.KnowledgeTaskResolved, model.KnowledgeTaskCanceled:
	default:
		return false
	}
	if task.Status == model.KnowledgeTaskPendingApproval {
		return next == model.KnowledgeTaskCanceled
	}
	if task.Status == model.KnowledgeTaskResolved || task.Status == model.KnowledgeTaskCanceled {
		return false
	}
	return true
}

func validateKnowledgeTaskRegression(ctx context.Context, svc *Service, tenantID, evalSetID, evalCaseID string) error {
	evalSet, err := svc.Store.GetEvalSet(ctx, tenantID, evalSetID)
	if err != nil {
		return err
	}
	if evalSet == nil || evalSet.ID == "" {
		return httperr.NotFound("regression eval set not found")
	}
	cases, err := svc.Store.ListEvalCases(ctx, tenantID, evalSetID)
	if err != nil {
		return err
	}
	for _, evalCase := range cases {
		if evalCase.ID == evalCaseID {
			return nil
		}
	}
	return httperr.NotFound("regression eval case not found in eval set")
}

func reopenKnowledgeTaskAfterApprovalDecision(ctx context.Context, svc *Service, approval *model.Approval, actorID, actorTenantID string) error {
	if approval.ObjectType != model.ApprovalObjectKnowledgeTask || approval.Action != model.ApprovalActionResolve {
		return nil
	}
	updated, err := svc.Store.UpdateKnowledgeTask(ctx, approval.TenantID, approval.ObjectID, false, map[string]interface{}{
		"status": model.KnowledgeTaskOpen, "approval_id": "",
	})
	if err != nil {
		return err
	}
	if !updated {
		return nil
	}
	detail, _ := json.Marshal(map[string]any{"approval_id": approval.ID, "approval_status": approval.Status})
	return svc.RecordAudit(ctx, &model.AuditLog{
		TenantID: approval.TenantID, UserID: actorID, Action: "knowledge-task.approval-reopened",
		Resource: "knowledge-ops", ResourceID: approval.ObjectID, DetailJSON: string(detail),
		ActorTenantID: actorTenantID, TargetTenantID: approval.TenantID, ApprovalID: approval.ID,
		ApprovalActionHash: approval.ApprovalActionHash, Result: approval.Status,
		AuthorizationDecision: "ALLOW", AuthorizationPermission: "manage:knowledge-ops",
	})
}

func validateKnowledgeTaskResolve(ctx context.Context, svc *Service, approval *model.Approval) error {
	task, err := svc.Store.GetKnowledgeTask(ctx, approval.TenantID, approval.ObjectID, false)
	if err != nil {
		return err
	}
	if task.ID == "" || task.Status != model.KnowledgeTaskPendingApproval {
		return httperr.NotFound("pending knowledge task not found")
	}
	return nil
}

func executeKnowledgeTaskResolve(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, httperr.BadRequest(40097, "invalid knowledge task approval payload")
	}
	var input KnowledgeTaskUpdateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, httperr.BadRequest(40097, "invalid knowledge task approval payload")
	}
	if input.Status == "" {
		input.Status = model.KnowledgeTaskResolved
	}
	if input.Status != model.KnowledgeTaskResolved || strings.TrimSpace(input.ResolutionNote) == "" ||
		input.RegressionEvalSetID == "" || input.RegressionEvalCaseID == "" ||
		input.RegressionStatus != model.KnowledgeTaskRegressionPassed {
		return nil, httperr.BadRequest(40097, "invalid knowledge task resolution approval payload")
	}
	if err := validateKnowledgeTaskRegression(ctx, svc, approval.TenantID, input.RegressionEvalSetID, input.RegressionEvalCaseID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	updated, err := svc.Store.UpdateKnowledgeTask(ctx, approval.TenantID, approval.ObjectID, false, map[string]interface{}{
		"status":                  model.KnowledgeTaskResolved,
		"resolution_note":         strings.TrimSpace(input.ResolutionNote),
		"regression_eval_set_id":  input.RegressionEvalSetID,
		"regression_eval_case_id": input.RegressionEvalCaseID,
		"regression_status":       model.KnowledgeTaskRegressionPassed,
		"resolved_at":             now,
	})
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, httperr.NotFound("knowledge task not found")
	}
	return map[string]any{"knowledge_task_id": approval.ObjectID, "resolved": true}, nil
}

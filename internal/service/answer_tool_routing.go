package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

type RoutedSQLToolAnswerInput struct {
	SessionID   string          `json:"session_id" binding:"required"`
	AssistantID string          `json:"assistant_id" binding:"required"`
	RequestID   string          `json:"request_id" binding:"required"`
	Question    string          `json:"question" binding:"required"`
	Intent      string          `json:"intent"`
	Entity      string          `json:"entity"`
	TimeRange   string          `json:"time_range"`
	Confidence  float64         `json:"confidence"`
	ToolInput   json.RawMessage `json:"tool_input" binding:"required"`
}

type RoutedSQLToolAnswerResult struct {
	Run              *model.AnswerRun      `json:"run"`
	Snapshot         *model.AnswerSnapshot `json:"snapshot"`
	ToolExecution    *ToolExecutionResult  `json:"tool_execution"`
	AnswerRunID      string                `json:"answer_run_id"`
	AnswerSnapshotID string                `json:"answer_snapshot_id"`
}

type RoutedSQLSubqueryInput struct {
	Intent     string          `json:"intent"`
	Entity     string          `json:"entity"`
	TimeRange  string          `json:"time_range"`
	Confidence float64         `json:"confidence"`
	ToolInput  json.RawMessage `json:"tool_input" binding:"required"`
}

type RoutedSQLMultiSubqueryAnswerInput struct {
	SessionID   string                   `json:"session_id" binding:"required"`
	AssistantID string                   `json:"assistant_id" binding:"required"`
	RequestID   string                   `json:"request_id" binding:"required"`
	Question    string                   `json:"question" binding:"required"`
	SubQueries  []RoutedSQLSubqueryInput `json:"sub_queries" binding:"required"`
}

type RoutedSQLMultiSubqueryAnswerResult struct {
	Run              *model.AnswerRun      `json:"run"`
	Snapshot         *model.AnswerSnapshot `json:"snapshot"`
	ToolExecutions   []ToolExecutionResult `json:"tool_executions"`
	AnswerRunID      string                `json:"answer_run_id"`
	AnswerSnapshotID string                `json:"answer_snapshot_id"`
}

type RoutedToolSubqueryInput struct {
	Intent     string          `json:"intent"`
	Entity     string          `json:"entity"`
	TimeRange  string          `json:"time_range"`
	Confidence float64         `json:"confidence"`
	ToolInput  json.RawMessage `json:"tool_input" binding:"required"`
}

type RoutedMixedToolAnswerInput struct {
	SessionID   string                    `json:"session_id" binding:"required"`
	AssistantID string                    `json:"assistant_id" binding:"required"`
	RequestID   string                    `json:"request_id" binding:"required"`
	Question    string                    `json:"question" binding:"required"`
	SubQueries  []RoutedToolSubqueryInput `json:"sub_queries" binding:"required"`
}

type RoutedMixedToolAnswerResult struct {
	Run              *model.AnswerRun      `json:"run"`
	Snapshot         *model.AnswerSnapshot `json:"snapshot"`
	ToolExecutions   []ToolExecutionResult `json:"tool_executions"`
	AnswerRunID      string                `json:"answer_run_id"`
	AnswerSnapshotID string                `json:"answer_snapshot_id"`
}

type routedMixedToolExecution struct {
	answerRun      *model.AnswerRun
	toolExecutions []ToolExecutionResult
	content        json.RawMessage
	execution      []map[string]interface{}
	anyTruncated   bool
	factGuard      map[string]interface{}
}

const routedSQLMaxSubqueries = 10

func (s *Service) ExecuteRoutedSQLToolAnswer(
	ctx context.Context, tenantID, userID, assistantID string, input RoutedSQLToolAnswerInput,
) (*RoutedSQLToolAnswerResult, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Question = strings.TrimSpace(input.Question)
	assistantID = strings.TrimSpace(assistantID)
	if tenantID == "" || assistantID == "" || input.SessionID == "" ||
		input.AssistantID != assistantID || input.RequestID == "" || input.Question == "" {
		return nil, httperr.BadRequest(40120, "tenant, assistant, session, request and question are required")
	}
	if len(input.ToolInput) == 0 {
		return nil, httperr.BadRequest(40120, "tool_input is required")
	}

	decision, err := s.RouteSource(ctx, tenantID, assistantID, SourceRoutingQuery{
		AssistantID: assistantID, Intent: strings.TrimSpace(input.Intent),
		Entity: strings.TrimSpace(input.Entity), TimeRange: strings.TrimSpace(input.TimeRange),
		Confidence: input.Confidence,
	})
	if err != nil {
		return nil, s.recordRoutedSQLAnswerFailure(ctx, tenantID, userID, input, err)
	}
	if decision.Tool.ToolType != model.ToolTypeSQLQuery {
		return nil, s.recordRoutedSQLAnswerFailure(ctx, tenantID, userID, input,
			httperr.BadRequest(40131, "routed answer execution only supports sql_query tools"),
		)
	}
	toolExecution, err := s.ExecuteToolRegistry(ctx, tenantID, userID, decision.Tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: decision.Rule.ID,
		Input:               input.ToolInput,
	})
	if err != nil {
		return nil, s.recordRoutedSQLAnswerFailure(ctx, tenantID, userID, input, err)
	}
	execution := []map[string]interface{}{{
		"tool_registry_id":       decision.Tool.ID,
		"tool_id":                decision.Tool.ToolID,
		"tool_version":           decision.Tool.Version,
		"source_routing_rule_id": decision.Rule.ID,
		"authorization_trace":    toolExecution.Trace,
	}}
	answer, err := s.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenantID, SessionID: input.SessionID, AssistantID: assistantID,
		PrincipalID: userID, Question: input.Question, RequestID: input.RequestID,
		Channel: "api", AnswerStatus: model.AnswerStatusAnswered,
		CompletionReason: model.CompletionReasonNormal, ReasonCode: "SQL_TOOL_ANSWERED",
		UserMessage: input.Question, Content: string(toolExecution.Output),
		Execution: execution, Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	return &RoutedSQLToolAnswerResult{
		Run: answer.Run, Snapshot: answer.Snapshot, ToolExecution: toolExecution,
		AnswerRunID: answer.AnswerRunID, AnswerSnapshotID: answer.AnswerSnapshotID,
	}, nil
}

func (s *Service) recordRoutedSQLAnswerFailure(
	ctx context.Context, tenantID, userID string, input RoutedSQLToolAnswerInput, cause error,
) error {
	return s.recordRoutedSQLAnswerDeliveryFailure(ctx, tenantID, userID,
		input.SessionID, input.AssistantID, input.RequestID, input.Question, cause,
	)
}

func (s *Service) recordRoutedSQLAnswerDeliveryFailure(
	ctx context.Context, tenantID, userID, sessionID, assistantID, requestID, question string, cause error,
) error {
	reason := "SQL_TOOL_EXECUTION_FAILED"
	if businessErr, ok := cause.(*httperr.Error); ok && businessErr.Status == 403 {
		reason = "FORBIDDEN_SOURCE"
	}
	if _, err := s.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenantID, SessionID: sessionID, AssistantID: assistantID,
		PrincipalID: userID, Question: question, RequestID: requestID,
		Channel: "api", AnswerStatus: model.AnswerStatusSystemFailed,
		CompletionReason: model.CompletionReasonSystemFailed, ReasonCode: reason,
		UserMessage: question, AdminReason: reason,
		Citations: []model.AnswerCitation{}, Execution: []map[string]interface{}{},
		Limitations: []string{}, Actions: []map[string]interface{}{},
	}); err != nil {
		return err
	}
	return cause
}

func (s *Service) ExecuteRoutedSQLMultiSubqueryAnswer(
	ctx context.Context, tenantID, userID, assistantID string, input RoutedSQLMultiSubqueryAnswerInput,
) (*RoutedSQLMultiSubqueryAnswerResult, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Question = strings.TrimSpace(input.Question)
	assistantID = strings.TrimSpace(assistantID)
	if tenantID == "" || assistantID == "" || input.SessionID == "" ||
		input.AssistantID != assistantID || input.RequestID == "" || input.Question == "" {
		return nil, httperr.BadRequest(40120, "tenant, assistant, session, request and question are required")
	}
	if len(input.SubQueries) == 0 || len(input.SubQueries) > routedSQLMaxSubqueries {
		return nil, httperr.BadRequest(40120, "sub_queries must contain between 1 and 10 items")
	}
	for index := range input.SubQueries {
		if len(input.SubQueries[index].ToolInput) == 0 {
			return nil, httperr.BadRequest(40120, "sub_queries[].tool_input is required")
		}
	}

	executions := make([]map[string]interface{}, 0, len(input.SubQueries))
	toolExecutions := make([]ToolExecutionResult, 0, len(input.SubQueries))
	toolOutputs := make([]json.RawMessage, 0, len(input.SubQueries))
	anyTruncated := false
	for index, subquery := range input.SubQueries {
		decision, err := s.RouteSource(ctx, tenantID, assistantID, SourceRoutingQuery{
			AssistantID: assistantID, Intent: strings.TrimSpace(subquery.Intent),
			Entity: strings.TrimSpace(subquery.Entity), TimeRange: strings.TrimSpace(subquery.TimeRange),
			Confidence: subquery.Confidence,
		})
		if err != nil {
			return nil, s.recordRoutedSQLAnswerDeliveryFailure(ctx, tenantID, userID,
				input.SessionID, assistantID, input.RequestID, input.Question, err,
			)
		}
		if decision.Tool.ToolType != model.ToolTypeSQLQuery {
			err = httperr.BadRequest(40131, "routed answer execution only supports sql_query tools")
			return nil, s.recordRoutedSQLAnswerDeliveryFailure(ctx, tenantID, userID,
				input.SessionID, assistantID, input.RequestID, input.Question, err,
			)
		}
		toolExecution, err := s.ExecuteToolRegistry(ctx, tenantID, userID, decision.Tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: decision.Rule.ID,
			Input:               subquery.ToolInput,
			AuthorizationTrace:  nil,
		})
		if err != nil {
			return nil, s.recordRoutedSQLAnswerDeliveryFailure(ctx, tenantID, userID,
				input.SessionID, assistantID, input.RequestID, input.Question, err,
			)
		}
		if truncated, ok := sqlToolOutputTruncated(toolExecution.Output); ok && truncated {
			anyTruncated = true
		}
		toolExecutions = append(toolExecutions, *toolExecution)
		toolOutputs = append(toolOutputs, toolExecution.Output)
		executions = append(executions, map[string]interface{}{
			"sub_query_index":        index,
			"tool_registry_id":       decision.Tool.ID,
			"tool_id":                decision.Tool.ToolID,
			"tool_version":           decision.Tool.Version,
			"source_routing_rule_id": decision.Rule.ID,
			"authorization_trace":    toolExecution.Trace,
		})
	}

	content, err := routedSQLMultiSubqueryEnvelope(toolOutputs)
	if err != nil {
		return nil, s.recordRoutedSQLAnswerDeliveryFailure(ctx, tenantID, userID,
			input.SessionID, assistantID, input.RequestID, input.Question, err,
		)
	}
	status := model.AnswerStatusAnswered
	reasonCode := "SQL_TOOL_ANSWERED"
	if anyTruncated {
		status = model.AnswerStatusPartial
		reasonCode = "SQL_TOOL_ANSWERED_PARTIAL"
	}
	answer, err := s.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenantID, SessionID: input.SessionID, AssistantID: assistantID,
		PrincipalID: userID, Question: input.Question, RequestID: input.RequestID,
		Channel: "api", AnswerStatus: status,
		CompletionReason: model.CompletionReasonNormal, ReasonCode: reasonCode,
		UserMessage: input.Question, Content: string(content),
		Execution: executions, Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	return &RoutedSQLMultiSubqueryAnswerResult{
		Run: answer.Run, Snapshot: answer.Snapshot, ToolExecutions: toolExecutions,
		AnswerRunID: answer.AnswerRunID, AnswerSnapshotID: answer.AnswerSnapshotID,
	}, nil
}

func sqlToolOutputTruncated(output json.RawMessage) (bool, bool) {
	var decoded map[string]interface{}
	if err := json.Unmarshal(output, &decoded); err != nil {
		return false, false
	}
	truncated, ok := decoded["truncated"].(bool)
	return truncated, ok
}

func routedSQLMultiSubqueryEnvelope(outputs []json.RawMessage) (json.RawMessage, error) {
	results := make([]map[string]interface{}, 0, len(outputs))
	for index, output := range outputs {
		results = append(results, map[string]interface{}{
			"sub_query_index": index,
			"output":          output,
		})
	}
	content, err := json.Marshal(map[string]interface{}{
		"schema":  "routed_sql_multi_subquery_v1",
		"results": results,
	})
	if err != nil {
		return nil, httperr.BadRequest(40131, "multi-subquery output encoding failed")
	}
	return content, nil
}

func (s *Service) createRoutedToolAuthorizationDenialAudit(
	ctx context.Context, tenantID, userID string, rule *model.SourceRoutingRule,
	tool *model.ToolRegistry, trace *AuthorizationTrace,
) error {
	now := time.Now().UTC()
	return s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: userID, Action: "tool_registry.authorization_denied", Resource: "tool-registry",
		ResourceID: tool.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"tool_id":                tool.ToolID,
			"tool_version":           tool.Version,
			"tool_type":              tool.ToolType,
			"source_routing_rule_id": rule.ID,
			"authorization_trace":    trace,
		}),
		At: now, Result: "DENIED", AuthorizationDecision: "DENY",
		AuthorizationPolicyVersion: authorizationPolicyVersion(trace),
	})
}

func (s *Service) authorizeRoutedToolSource(
	ctx context.Context, tenantID, userID string, rule *model.SourceRoutingRule, tool *model.ToolRegistry,
) (*AuthorizationTrace, error) {
	trace := newToolAuthorizationTrace(rule.ID, tool.ToolID)
	trace.PolicyVersion = rule.PolicyVersion
	user, err := s.Store.GetUser(ctx, userID)
	if err != nil || user == nil || user.TenantID != tenantID || user.Status != model.UserStatusActive ||
		!userAllowedByScope(rule.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerRoutingRule)
	} else if rule.ToolID != tool.ToolID || rule.ToolVersion != tool.Version || !tool.Active ||
		!userAllowedByScope(tool.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerToolRegistry)
	} else {
		return trace, nil
	}
	if err := s.createRoutedToolAuthorizationDenialAudit(ctx, tenantID, userID, rule, tool, trace); err != nil {
		return nil, err
	}
	return nil, forbiddenSQLSource()
}

func (s *Service) ExecuteRoutedMixedToolAnswer(
	ctx context.Context, tenantID, userID, assistantID string, input RoutedMixedToolAnswerInput,
) (*RoutedMixedToolAnswerResult, error) {
	execution, err := s.executeRoutedMixedToolAnswer(ctx, tenantID, userID, assistantID, input)
	if err != nil {
		return nil, err
	}
	return s.finalizeRoutedMixedToolExecution(
		ctx, tenantID, userID, input, execution, "MIXED_TOOL_ANSWERED",
	)
}

func (s *Service) executeRoutedMixedToolAnswer(
	ctx context.Context, tenantID, userID, assistantID string, input RoutedMixedToolAnswerInput,
) (*routedMixedToolExecution, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Question = strings.TrimSpace(input.Question)
	assistantID = strings.TrimSpace(assistantID)
	if tenantID == "" || assistantID == "" || input.SessionID == "" ||
		input.AssistantID != assistantID || input.RequestID == "" || input.Question == "" {
		return nil, httperr.BadRequest(40120, "tenant, assistant, session, request and question are required")
	}
	if len(input.SubQueries) == 0 || len(input.SubQueries) > routedSQLMaxSubqueries {
		return nil, httperr.BadRequest(40120, "sub_queries must contain between 1 and 10 items")
	}
	for index := range input.SubQueries {
		if len(input.SubQueries[index].ToolInput) == 0 {
			return nil, httperr.BadRequest(40120, "sub_queries[].tool_input is required")
		}
	}
	answerRun, err := s.startRoutedAnswerRun(ctx, tenantID, input.SessionID, assistantID, input.RequestID, input.Question)
	if err != nil {
		return nil, err
	}

	executions := make([]map[string]interface{}, 0, len(input.SubQueries))
	toolExecutions := make([]ToolExecutionResult, 0, len(input.SubQueries))
	toolOutputs := make([]json.RawMessage, 0, len(input.SubQueries))
	sources := make([]string, 0, len(input.SubQueries))
	anyTruncated := false
	for index, subquery := range input.SubQueries {
		decision, err := s.RouteSource(ctx, tenantID, assistantID, SourceRoutingQuery{
			AssistantID: assistantID, Intent: strings.TrimSpace(subquery.Intent),
			Entity: strings.TrimSpace(subquery.Entity), TimeRange: strings.TrimSpace(subquery.TimeRange),
			Confidence: subquery.Confidence,
		})
		if err != nil {
			return nil, s.recordRoutedMixedAnswerFailure(ctx, tenantID, userID, answerRun, input, err)
		}
		if !supportsMixedToolSource(decision.Rule.SourceType, decision.Tool.ToolType) {
			err = httperr.BadRequest(40131, "mixed answer execution supports only db sql_query, tool calculation/fact_lookup and knowledge knowledge_retrieval sources")
			return nil, s.recordRoutedMixedAnswerFailure(ctx, tenantID, userID, answerRun, input, err)
		}
		authorizationTrace, err := s.authorizeRoutedToolSource(ctx, tenantID, userID, decision.Rule, decision.Tool)
		if err != nil {
			return nil, s.recordRoutedMixedAnswerFailure(ctx, tenantID, userID, answerRun, input, err)
		}
		toolExecution, err := s.ExecuteToolRegistry(ctx, tenantID, userID, decision.Tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: decision.Rule.ID,
			AnswerRunID:         answerRun.ID,
			Input:               subquery.ToolInput,
			AuthorizationTrace:  authorizationTrace,
		})
		if err != nil {
			return nil, s.recordRoutedMixedAnswerFailure(ctx, tenantID, userID, answerRun, input, err)
		}
		if decision.Tool.ToolType == model.ToolTypeSQLQuery {
			if truncated, ok := sqlToolOutputTruncated(toolExecution.Output); ok && truncated {
				anyTruncated = true
			}
		}
		executionTrace := toolExecution.Trace
		if decision.Tool.ToolType != model.ToolTypeSQLQuery {
			executionTrace = authorizationTrace
		}
		toolExecutions = append(toolExecutions, *toolExecution)
		toolOutputs = append(toolOutputs, toolExecution.Output)
		sources = append(sources, decision.Rule.SourceType)
		executions = append(executions, map[string]interface{}{
			"sub_query_index":        index,
			"source":                 decision.Rule.SourceType,
			"tool_registry_id":       decision.Tool.ID,
			"tool_id":                decision.Tool.ToolID,
			"tool_version":           decision.Tool.Version,
			"source_routing_rule_id": decision.Rule.ID,
			"authorization_trace":    executionTrace,
		})
	}

	content, err := routedMixedToolEnvelope(sources, toolOutputs)
	if err != nil {
		return nil, s.recordRoutedMixedAnswerFailure(ctx, tenantID, userID, answerRun, input, err)
	}
	return &routedMixedToolExecution{
		answerRun: answerRun, toolExecutions: toolExecutions, content: content,
		execution: executions, anyTruncated: anyTruncated,
	}, nil
}

func (s *Service) finalizeRoutedMixedToolExecution(
	ctx context.Context, tenantID, userID string, input RoutedMixedToolAnswerInput,
	execution *routedMixedToolExecution, reasonCode string,
) (*RoutedMixedToolAnswerResult, error) {
	status := model.AnswerStatusAnswered
	if execution.anyTruncated {
		status = model.AnswerStatusPartial
		reasonCode += "_PARTIAL"
	}
	limitations := []string{}
	if flagged := factGuardFlagCount(execution.factGuard); flagged > 0 {
		limitations = append(limitations, fmt.Sprintf("fact_guard.flagged_claims:%d", flagged))
	}
	delivery := AnswerDeliveryInput{
		TenantID: tenantID, SessionID: input.SessionID, AssistantID: input.AssistantID,
		PrincipalID: userID, Question: input.Question, RequestID: input.RequestID,
		Channel: "api", AnswerStatus: status,
		CompletionReason: model.CompletionReasonNormal, ReasonCode: reasonCode,
		UserMessage: input.Question, Content: string(execution.content),
		Execution: execution.execution, Limitations: limitations, Actions: []map[string]interface{}{},
	}
	delivery.preparedRun = execution.answerRun
	answer, err := s.FinalizeAnswerDelivery(ctx, delivery)
	if err != nil {
		return nil, err
	}
	return &RoutedMixedToolAnswerResult{
		Run: answer.Run, Snapshot: answer.Snapshot, ToolExecutions: execution.toolExecutions,
		AnswerRunID: answer.AnswerRunID, AnswerSnapshotID: answer.AnswerSnapshotID,
	}, nil
}

func supportsMixedToolSource(sourceType, toolType string) bool {
	return (sourceType == model.SourceTypeDB && toolType == model.ToolTypeSQLQuery) ||
		(sourceType == model.SourceTypeTool &&
			(toolType == model.ToolTypeCalculation || toolType == model.ToolTypeLookup)) ||
		(sourceType == model.SourceTypeKnowledge && toolType == model.ToolTypeKnowledgeRetrieval)
}

func (s *Service) startRoutedAnswerRun(
	ctx context.Context, tenantID, sessionID, assistantID, requestID, question string,
) (*model.AnswerRun, error) {
	existing, err := s.Store.GetAnswerRunByRequest(ctx, tenantID, requestID)
	if err == nil {
		if existing == nil || existing.SessionID != sessionID || existing.AssistantID != assistantID {
			return nil, httperr.New(409, 40997, "answer run does not match the routing request")
		}
		if existing.LifecycleState != model.AnswerLifecycleInit &&
			existing.LifecycleState != model.AnswerLifecycleGenerating &&
			existing.LifecycleState != model.AnswerLifecycleFinalizing {
			return nil, httperr.New(409, 40997, "answer run has already completed")
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	now := time.Now().UTC()
	run := &model.AnswerRun{
		ID: id.New(), TenantID: tenantID, SessionID: sessionID, AssistantID: assistantID,
		QuestionRef: sha256Hex(strings.Join(strings.Fields(strings.ToLower(question)), " ")),
		RequestID:   requestID, LifecycleState: model.AnswerLifecycleInit,
		AnswerStatus: model.AnswerStatusNoAnswer, CompletionReason: model.CompletionReasonNormal,
		CreatedAt: now,
	}
	if err := s.Store.CreateAnswerRun(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) recordRoutedMixedAnswerFailure(
	ctx context.Context, tenantID, userID string, answerRun *model.AnswerRun,
	input RoutedMixedToolAnswerInput, cause error,
) error {
	reason := "MIXED_TOOL_EXECUTION_FAILED"
	if businessErr, ok := cause.(*httperr.Error); ok && businessErr.Status == 403 {
		reason = "FORBIDDEN_SOURCE"
	}
	delivery := AnswerDeliveryInput{
		TenantID: tenantID, SessionID: input.SessionID, AssistantID: input.AssistantID,
		PrincipalID: userID, Question: input.Question, RequestID: input.RequestID,
		Channel: "api", AnswerStatus: model.AnswerStatusSystemFailed,
		CompletionReason: model.CompletionReasonSystemFailed, ReasonCode: reason,
		UserMessage: input.Question, AdminReason: reason,
		Citations: []model.AnswerCitation{}, Execution: []map[string]interface{}{},
		Limitations: []string{}, Actions: []map[string]interface{}{},
	}
	delivery.preparedRun = answerRun
	if _, err := s.FinalizeAnswerDelivery(ctx, delivery); err != nil {
		return err
	}
	return cause
}

func routedMixedToolEnvelope(sources []string, outputs []json.RawMessage) (json.RawMessage, error) {
	results := make([]map[string]interface{}, 0, len(outputs))
	for index, output := range outputs {
		source := ""
		if index < len(sources) {
			source = sources[index]
		}
		results = append(results, map[string]interface{}{
			"sub_query_index": index,
			"source":          source,
			"output":          output,
		})
	}
	content, err := json.Marshal(map[string]interface{}{
		"schema":  "routed_mixed_tool_v1",
		"results": results,
	})
	if err != nil {
		return nil, httperr.BadRequest(40131, "mixed tool output encoding failed")
	}
	return content, nil
}

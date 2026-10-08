package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type SourceRoutingRuleInput struct {
	AssistantID         string  `json:"assistant_id"`
	AuthorizationScope  string  `json:"authorization_scope" binding:"required"`
	SourceType          string  `json:"source_type" binding:"required"`
	Matcher             string  `json:"matcher" binding:"required"`
	ToolID              string  `json:"tool_id" binding:"required"`
	ToolVersion         string  `json:"tool_version" binding:"required"`
	PolicyVersion       string  `json:"policy_version" binding:"required"`
	Priority            int     `json:"priority" binding:"required"`
	ConfidenceThreshold float64 `json:"confidence_threshold" binding:"required"`
	Active              *bool   `json:"active"`
}

type SourceRoutingQuery struct {
	AssistantID string  `json:"assistant_id"`
	Intent      string  `json:"intent"`
	Entity      string  `json:"entity"`
	TimeRange   string  `json:"time_range"`
	Confidence  float64 `json:"confidence"`
}

type SourceRoutingDecision struct {
	Rule *model.SourceRoutingRule `json:"rule"`
	Tool *model.ToolRegistry      `json:"tool"`
}

func notFoundSourceRoutingRule() error { return httperr.NotFound("source routing rule not found") }

func (s *Service) ListSourceRoutingRules(
	ctx context.Context, tenantID string, filter repository.SourceRoutingRuleFilter, page, pageSize int,
) ([]model.SourceRoutingRule, int64, error) {
	filter.AssistantID = strings.TrimSpace(filter.AssistantID)
	filter.SourceType = strings.TrimSpace(filter.SourceType)
	return s.Store.ListSourceRoutingRules(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetSourceRoutingRule(ctx context.Context, tenantID, ruleID string) (*model.SourceRoutingRule, error) {
	rule, err := s.Store.GetSourceRoutingRule(ctx, tenantID, ruleID)
	if err != nil {
		return nil, err
	}
	if rule == nil || rule.ID == "" {
		return nil, notFoundSourceRoutingRule()
	}
	return rule, nil
}

func (s *Service) CreateSourceRoutingRule(
	ctx context.Context, tenantID, userID string, input SourceRoutingRuleInput,
) (*model.SourceRoutingRule, error) {
	now := time.Now().UTC()
	rule, err := buildSourceRoutingRule(tenantID, userID, input, now)
	if err != nil {
		return nil, err
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := ensureActiveSourceRoutingTool(ctx, tx, rule); err != nil {
			return err
		}
		if err := tx.CreateSourceRoutingRule(ctx, rule); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForSourceRoutingRule(rule, "source_routing_rule.created", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return rule, nil
}

func (s *Service) UpdateSourceRoutingRule(
	ctx context.Context, tenantID, userID, ruleID string, input SourceRoutingRuleInput,
) (*model.SourceRoutingRule, error) {
	existing, err := s.GetSourceRoutingRule(ctx, tenantID, ruleID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	updated, err := buildSourceRoutingRule(tenantID, existing.CreatedBy, input, existing.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated.ID = existing.ID
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = now
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := ensureActiveSourceRoutingTool(ctx, tx, updated); err != nil {
			return err
		}
		if err := tx.UpdateSourceRoutingRule(ctx, updated); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForSourceRoutingRule(updated, "source_routing_rule.updated", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteSourceRoutingRule(ctx context.Context, tenantID, userID, ruleID string) error {
	rule, err := s.GetSourceRoutingRule(ctx, tenantID, ruleID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteSourceRoutingRule(ctx, tenantID, rule.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForSourceRoutingRule(rule, "source_routing_rule.deleted", userID, now))
	})
}

func (s *Service) RouteSource(
	ctx context.Context, tenantID, assistantID string, query SourceRoutingQuery,
) (*SourceRoutingDecision, error) {
	rules, err := s.Store.ListActiveSourceRoutingRules(ctx, tenantID, strings.TrimSpace(assistantID))
	if err != nil {
		return nil, err
	}
	for index := range rules {
		rule := &rules[index]
		if (rule.SourceType != model.SourceTypeTool && rule.SourceType != model.SourceTypeDB &&
			rule.SourceType != model.SourceTypeKnowledge) ||
			!matchesSourceRoutingRule(rule, &query) {
			continue
		}
		tool, toolErr := s.Store.FindActiveToolRegistry(ctx, tenantID, rule.ToolID, rule.ToolVersion)
		if toolErr != nil {
			return nil, toolErr
		}
		if tool == nil {
			continue
		}
		return &SourceRoutingDecision{Rule: rule, Tool: tool}, nil
	}
	return nil, httperr.NotFound("no active source routing rule matches the query")
}

func buildSourceRoutingRule(
	tenantID, userID string, input SourceRoutingRuleInput, now time.Time,
) (*model.SourceRoutingRule, error) {
	assistantID := strings.TrimSpace(input.AssistantID)
	sourceType := strings.TrimSpace(input.SourceType)
	toolID := strings.TrimSpace(input.ToolID)
	toolVersion := strings.TrimSpace(input.ToolVersion)
	policyVersion := strings.TrimSpace(input.PolicyVersion)
	if assistantID != "" && len(assistantID) > 32 {
		return nil, httperr.BadRequest(40135, "assistant_id must be at most 32 characters")
	}
	if sourceType != model.SourceTypeTool && sourceType != model.SourceTypeDB &&
		sourceType != model.SourceTypeKnowledge {
		return nil, httperr.BadRequest(40136, "source_type must be tool, db or knowledge")
	}
	if !validToolID.MatchString(toolID) {
		return nil, httperr.BadRequest(40135, "tool_id must match "+toolIDPattern)
	}
	if toolVersion == "" || len(toolVersion) > 64 {
		return nil, httperr.BadRequest(40135, "tool_version must contain 1 to 64 characters")
	}
	if policyVersion == "" || len(policyVersion) > 64 {
		return nil, httperr.BadRequest(40135, "policy_version must contain 1 to 64 characters")
	}
	if input.Priority < 1 || input.Priority > 10000 {
		return nil, httperr.BadRequest(40135, "priority must be between 1 and 10000")
	}
	if input.ConfidenceThreshold <= 0 || input.ConfidenceThreshold > 1 {
		return nil, httperr.BadRequest(40135, "confidence_threshold must be between 0 and 1")
	}
	authorizationScope, err := validateAuthorizationScope(input.AuthorizationScope, "authorization_scope")
	if err != nil {
		return nil, err
	}
	matcher, err := validateSourceMatcher(input.Matcher)
	if err != nil {
		return nil, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	return &model.SourceRoutingRule{
		ID: id.New(), TenantID: tenantID, AssistantID: assistantID, SourceType: sourceType,
		Matcher: matcher, ToolID: toolID, ToolVersion: toolVersion, PolicyVersion: policyVersion,
		AuthorizationScope: authorizationScope,
		Priority:           input.Priority, ConfidenceThreshold: input.ConfidenceThreshold,
		Active: active, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func ensureActiveSourceRoutingTool(
	ctx context.Context, store repository.Store, rule *model.SourceRoutingRule,
) error {
	tool, err := store.FindActiveToolRegistry(ctx, rule.TenantID, rule.ToolID, rule.ToolVersion)
	if err != nil {
		return err
	}
	if tool == nil || tool.ToolID != rule.ToolID || tool.Version != rule.ToolVersion || !tool.Active {
		return httperr.New(404, 40137, "tool registry entry not found or inactive")
	}
	return nil
}

func validateSourceMatcher(raw string) (string, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return "", httperr.BadRequest(40138, "matcher must be a valid JSON object")
	}
	if values == nil {
		return "", httperr.BadRequest(40138, "matcher must define at least one condition")
	}
	if len(values) == 0 {
		return "", httperr.BadRequest(40151, "matcher must define at least one condition")
	}
	allowed := map[string]bool{"intent": true, "entity": true, "time_range": true}
	conditions := make(map[string]interface{}, len(values))
	for name, value := range values {
		if !allowed[name] {
			return "", httperr.BadRequest(40139, "matcher contains an unsupported condition")
		}
		var single string
		if err := json.Unmarshal(value, &single); err == nil {
			single = strings.TrimSpace(single)
			if single == "" {
				return "", httperr.BadRequest(40152, "matcher conditions must not be empty")
			}
			conditions[name] = single
			continue
		}
		var multiple []string
		if err := json.Unmarshal(value, &multiple); err != nil {
			return "", httperr.BadRequest(40152, "matcher condition must be a string or array of strings")
		}
		if len(multiple) == 0 {
			return "", httperr.BadRequest(40152, "matcher condition must contain at least one value")
		}
		for index, item := range multiple {
			multiple[index] = strings.TrimSpace(item)
			if multiple[index] == "" {
				return "", httperr.BadRequest(40152, "matcher conditions must not be empty")
			}
		}
		conditions[name] = multiple
	}
	encoded, err := json.Marshal(conditions)
	if err != nil {
		return "", httperr.BadRequest(40138, "matcher must be a valid JSON object")
	}
	return string(encoded), nil
}

func matchesSourceRoutingRule(rule *model.SourceRoutingRule, query *SourceRoutingQuery) bool {
	assistantID := strings.TrimSpace(query.AssistantID)
	if rule.AssistantID != "" && rule.AssistantID != assistantID {
		return false
	}
	if query.Confidence < rule.ConfidenceThreshold {
		return false
	}
	var conditions map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rule.Matcher), &conditions); err != nil {
		return false
	}
	intent, ok := decodeSourceMatcherValues(conditions["intent"])
	if !ok || (len(intent) > 0 && !containsString(intent, strings.TrimSpace(query.Intent))) {
		return false
	}
	entity, ok := decodeSourceMatcherValues(conditions["entity"])
	if !ok || (len(entity) > 0 && !containsString(entity, strings.TrimSpace(query.Entity))) {
		return false
	}
	timeRange, ok := decodeSourceMatcherValues(conditions["time_range"])
	if !ok || (len(timeRange) > 0 && !containsString(timeRange, strings.TrimSpace(query.TimeRange))) {
		return false
	}
	return true
}

func decodeSourceMatcherValues(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, true
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		return multiple, true
	}
	return nil, false
}

func auditForSourceRoutingRule(
	rule *model.SourceRoutingRule, action, userID string, now time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: rule.TenantID, ActorTenantID: rule.TenantID, TargetTenantID: rule.TenantID,
		UserID: userID, Action: action, Resource: "source-routing-rule", ResourceID: rule.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"rule_id": rule.ID, "tool_id": rule.ToolID, "tool_version": rule.ToolVersion,
			"policy_version": rule.PolicyVersion, "priority": rule.Priority, "active": rule.Active,
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

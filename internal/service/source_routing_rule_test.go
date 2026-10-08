package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newSourceRoutingRuleService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "source-routing-rule.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func createCalculationTool(t *testing.T, ctx context.Context, svc *Service, tenant, toolID, version string) *model.ToolRegistry {
	t.Helper()
	tool, err := svc.CreateToolRegistry(ctx, tenant, id.New(), calculationToolInput(toolID, version, nil))
	if err != nil {
		t.Fatalf("create %s %s: %v", toolID, version, err)
	}
	return tool
}

func calculationToolInput(toolID, version string, active *bool) ToolRegistryInput {
	return ToolRegistryInput{
		ToolID: toolID, Version: version, ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:" + toolID,
		Active:             active,
	}
}

func sourceRoutingRuleInput(toolID, version string) SourceRoutingRuleInput {
	return SourceRoutingRuleInput{
		AuthorizationScope:  `{"roles":["platform_admin","tenant_admin"]}`,
		SourceType:          model.SourceTypeTool,
		Matcher:             `{"intent":"exposure_query","entity":["counterparty_a"],"time_range":"last_30_days"}`,
		ToolID:              toolID,
		ToolVersion:         version,
		PolicyVersion:       "policy-v1",
		Priority:            10,
		ConfidenceThreshold: 0.8,
	}
}

func TestCreateSourceRoutingRuleAuthorizationScope(t *testing.T) {
	ctx := context.Background()
	svc := newSourceRoutingRuleService(t)
	tenant, user := id.New(), id.New()
	tool := createCalculationTool(t, ctx, svc, tenant, "add", "v1")
	input := sourceRoutingRuleInput(tool.ToolID, tool.Version)
	input.AuthorizationScope = `{}`
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input); err == nil {
		t.Fatal("empty authorization scope must fail closed")
	}
	input.AuthorizationScope = `{"roles":["unknown_role"]}`
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input); err == nil {
		t.Fatal("unknown role must fail")
	}
	input.AuthorizationScope = `{"roles":["platform_admin","tenant_admin"]}`
	rule, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input)
	if err != nil || rule.AuthorizationScope != input.AuthorizationScope {
		t.Fatalf("valid authorization scope: %+v err=%v", rule, err)
	}
}

func TestCreateSourceRoutingRuleValidation(t *testing.T) {
	ctx := context.Background()
	svc := newSourceRoutingRuleService(t)
	tenant, user := id.New(), id.New()
	tool := createCalculationTool(t, ctx, svc, tenant, "add", "v1")

	input := sourceRoutingRuleInput("add", "v1")
	input.SourceType = model.SourceTypeKnowledge
	knowledgeRule, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("knowledge source rule must now be supported: %v", err)
	}
	if knowledgeRule.SourceType != model.SourceTypeKnowledge {
		t.Fatalf("unexpected knowledge rule source: %+v", knowledgeRule)
	}

	input = sourceRoutingRuleInput("missing", "v1")
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input); err == nil {
		t.Fatal("missing active tool must be rejected")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 404 || businessErr.Code != 40137 {
		t.Fatalf("expected 404/40137, got %#v", err)
	}

	for name, matcher := range map[string]string{
		"invalid json":    "{",
		"unknown key":     `{"query":"exposure"}`,
		"empty condition": `{"intent":"  "}`,
		"no condition":    `{}`,
	} {
		input = sourceRoutingRuleInput("add", "v1")
		input.Matcher = matcher
		if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input); err == nil {
			t.Fatalf("%s matcher must be rejected", name)
		}
	}

	input = sourceRoutingRuleInput("add", "v1")
	rule, err := svc.CreateSourceRoutingRule(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create source routing rule: %v", err)
	}
	if rule.TenantID != tenant || rule.CreatedBy != user || rule.Active != true {
		t.Fatalf("unexpected rule: %+v", rule)
	}
	if rule.Matcher != `{"entity":["counterparty_a"],"intent":"exposure_query","time_range":"last_30_days"}` {
		t.Fatalf("matcher was not canonicalized: %s", rule.Matcher)
	}
	if tool.Active != true {
		t.Fatalf("unexpected tool state: %+v", tool)
	}
}

func TestRouteSourceMatchesRulesAndCurrentActiveTool(t *testing.T) {
	ctx := context.Background()
	svc := newSourceRoutingRuleService(t)
	tenant, user := id.New(), id.New()
	createCalculationTool(t, ctx, svc, tenant, "add", "v1")
	createCalculationTool(t, ctx, svc, tenant, "subtract", "v1")

	assistantRule := sourceRoutingRuleInput("subtract", "v1")
	assistantRule.AssistantID = "assistant-1"
	assistantRule.Priority = 1
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, assistantRule); err != nil {
		t.Fatalf("create assistant rule: %v", err)
	}
	globalRule := sourceRoutingRuleInput("add", "v1")
	globalRule.ToolID = "subtract"
	globalRule.Priority = 2
	globalRule.ConfidenceThreshold = 0.5
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, globalRule); err != nil {
		t.Fatalf("create global rule: %v", err)
	}

	query := SourceRoutingQuery{
		AssistantID: "assistant-1", Intent: "exposure_query", Entity: "counterparty_a",
		TimeRange: "last_30_days", Confidence: 0.9,
	}
	decision, err := svc.RouteSource(ctx, tenant, query.AssistantID, query)
	if err != nil {
		t.Fatalf("route source: %v", err)
	}
	if decision.Rule.ToolID != "subtract" || decision.Tool.ToolID != "subtract" || decision.Tool.Version != "v1" {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	lowConfidence := query
	lowConfidence.Confidence = 0.49
	if _, err := svc.RouteSource(ctx, tenant, query.AssistantID, lowConfidence); err == nil {
		t.Fatal("low confidence must not route")
	}
	if _, err := svc.RouteSource(ctx, id.New(), query.AssistantID, query); err == nil {
		t.Fatal("routing must be tenant isolated")
	}

	inactive := false
	if _, err := svc.UpdateToolRegistry(ctx, tenant, user, decision.Tool.ID, calculationToolInput("subtract", "v1", &inactive)); err != nil {
		t.Fatalf("deactivate tool: %v", err)
	}
	if _, err := svc.RouteSource(ctx, tenant, query.AssistantID, query); err == nil {
		t.Fatal("inactive tool must not route")
	}
}

func TestUpdateAndDeleteSourceRoutingRule(t *testing.T) {
	ctx := context.Background()
	svc := newSourceRoutingRuleService(t)
	tenant, user := id.New(), id.New()
	createCalculationTool(t, ctx, svc, tenant, "add", "v1")
	createCalculationTool(t, ctx, svc, tenant, "subtract", "v1")
	rule, err := svc.CreateSourceRoutingRule(ctx, tenant, user, sourceRoutingRuleInput("add", "v1"))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	input := sourceRoutingRuleInput("missing", "v1")
	if _, err := svc.UpdateSourceRoutingRule(ctx, tenant, user, rule.ID, input); err == nil {
		t.Fatal("update to missing active tool must fail")
	}
	input = sourceRoutingRuleInput("subtract", "v1")
	input.Active = boolPtr(false)
	updated, err := svc.UpdateSourceRoutingRule(ctx, tenant, user, rule.ID, input)
	if err != nil {
		t.Fatalf("update rule: %v", err)
	}
	if updated.ToolID != "subtract" || updated.Active {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}

	other, err := svc.GetSourceRoutingRule(ctx, id.New(), rule.ID)
	if err == nil || other != nil {
		t.Fatalf("cross-tenant get = %+v err=%v", other, err)
	}
	if err := svc.DeleteSourceRoutingRule(ctx, tenant, user, rule.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if _, err := svc.GetSourceRoutingRule(ctx, tenant, rule.ID); err == nil {
		t.Fatal("deleted rule must not be found")
	}
	audits, _, err := svc.ListAudits(ctx, tenant, 1, 20, repository.AuditFilter{Resource: "source-routing-rule"})
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	actions := map[string]bool{}
	for _, audit := range audits {
		if audit.Resource == "source-routing-rule" && audit.ResourceID == rule.ID {
			actions[audit.Action] = true
		}
	}
	if len(audits) != 3 || !actions["source_routing_rule.created"] || !actions["source_routing_rule.updated"] || !actions["source_routing_rule.deleted"] {
		t.Fatalf("unexpected audit logs: %+v", audits)
	}
	if len(audits) != 3 || time.Time.IsZero(audits[0].At) {
		t.Fatalf("audit timestamps are missing: %+v", audits)
	}
}

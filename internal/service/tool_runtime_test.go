package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func factLookupToolRegistryInput(version string, active *bool) ToolRegistryInput {
	return ToolRegistryInput{
		ToolID: "fact_lookup", Version: version, ToolType: model.ToolTypeLookup, Name: "Fact lookup",
		InputSchema:        objectSchema([]string{"fact_key"}, "string"),
		OutputSchema:       objectSchema([]string{"value", "unit"}, "string", "string"),
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:fact_lookup", Active: active,
	}
}

func TestExecuteToolRegistryDeterministicCalculations(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	add, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create add tool: %v", err)
	}
	result, err := svc.ExecuteToolRegistry(ctx, tenant, user, add.ID, ToolExecutionInput{
		Input: json.RawMessage(`{"a":1.2,"b":2.3}`),
	})
	if err != nil {
		t.Fatalf("execute add: %v", err)
	}
	var output struct {
		Result json.Number `json:"result"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.Result.String() != "3.5" {
		t.Fatalf("add result = %s, want 3.5", output.Result)
	}

	sum, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "sum", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Sum items",
		InputSchema:        sumInputSchema(),
		OutputSchema:       objectSchema([]string{"result"}, "number"),
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:sum", Active: nil,
	})
	if err != nil {
		t.Fatalf("create sum tool: %v", err)
	}
	sumResult, err := svc.ExecuteToolRegistry(ctx, tenant, user, sum.ID, ToolExecutionInput{
		Input: json.RawMessage(`{"items":[1,2,3.5]}`),
	})
	if err != nil {
		t.Fatalf("execute sum: %v", err)
	}
	var sumOutput struct {
		Result json.Number `json:"result"`
	}
	if err := json.Unmarshal(sumResult.Output, &sumOutput); err != nil {
		t.Fatalf("decode sum output: %v", err)
	}
	if sumOutput.Result.String() != "6.5" {
		t.Fatalf("sum result = %s, want 6.5", sumOutput.Result)
	}
}

func TestExecuteToolRegistryTypedSchemaValidation(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	add, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create add tool: %v", err)
	}
	if _, err := svc.ExecuteToolRegistry(ctx, tenant, user, add.ID, ToolExecutionInput{
		Input: json.RawMessage(`{"a":"1.2","b":2.3}`),
	}); err == nil {
		t.Fatal("string input for number field must fail")
	}
	if _, err := svc.ExecuteToolRegistry(ctx, tenant, user, add.ID, ToolExecutionInput{
		Input: json.RawMessage(`{"a":1.2,"b":2.3,"c":3}`),
	}); err == nil {
		t.Fatal("unknown typed input field must fail")
	}
}

func TestExecuteToolRegistryExposureRemaining(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "calc_exposure_remaining", Version: "v1", ToolType: model.ToolTypeCalculation,
		Name: "Calculate exposure remaining", InputSchema: objectSchema([]string{"limit", "current"}, "number", "number"),
		OutputSchema:       objectSchema([]string{"remaining"}, "number"),
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:calc_exposure_remaining",
	})
	if err != nil {
		t.Fatalf("create exposure tool: %v", err)
	}
	result, err := svc.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		Input: json.RawMessage(`{"limit":100.25,"current":37.5}`),
	})
	if err != nil {
		t.Fatalf("execute exposure tool: %v", err)
	}
	var output struct {
		Remaining json.Number `json:"remaining"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode exposure output: %v", err)
	}
	if output.Remaining.String() != "62.75" {
		t.Fatalf("remaining = %s, want 62.75", output.Remaining)
	}
}

func TestExecuteToolRegistryFactLookup(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, factLookupToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create fact lookup tool: %v", err)
	}
	run := createAnswerRun(t, svc, tenant, "request-tool-runtime-"+id.New())
	fact := &model.FactRegistry{
		ID: id.New(), TenantID: tenant, AnswerRunID: run.ID, FactKey: "revenue", Value: "10,000,000",
		Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted,
		ConflictStatus: model.FactConflictNone, CreatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateFactRegistry(ctx, fact); err != nil {
		t.Fatalf("create fact: %v", err)
	}
	result, err := svc.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		AnswerRunID: run.ID, Input: json.RawMessage(`{"fact_key":"revenue"}`),
	})
	if err != nil {
		t.Fatalf("execute fact lookup: %v", err)
	}
	var output struct {
		Value string `json:"value"`
		Unit  string `json:"unit"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode fact lookup output: %v", err)
	}
	if output.Value != "10,000,000" || output.Unit != "USD" {
		t.Fatalf("fact lookup output = %+v", output)
	}
	if _, err := svc.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		AnswerRunID: id.New(), Input: json.RawMessage(`{"fact_key":"revenue"}`),
	}); err == nil {
		t.Fatal("cross-answer-run fact lookup must fail")
	}
}

func TestExecuteToolRegistryFactLookupConflictFailClosed(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, factLookupToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create fact lookup tool: %v", err)
	}
	run := createAnswerRun(t, svc, tenant, "request-tool-runtime-conflict-"+id.New())
	now := time.Now().UTC()
	left := &model.FactRegistry{ID: id.New(), TenantID: tenant, AnswerRunID: run.ID, FactKey: "revenue", Value: "8", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted, ConflictStatus: model.FactConflictValue, CreatedAt: now}
	right := &model.FactRegistry{ID: id.New(), TenantID: tenant, AnswerRunID: run.ID, FactKey: "revenue", Value: "10", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-2", ClaimType: model.FactClaimTypeExtracted, ConflictStatus: model.FactConflictValue, CreatedAt: now}
	for _, fact := range []*model.FactRegistry{left, right} {
		if err := svc.Store.CreateFactRegistry(ctx, fact); err != nil {
			t.Fatalf("create conflicting fact: %v", err)
		}
	}
	conflict := &model.EvidenceConflict{
		ID: id.New(), TenantID: tenant, AnswerRunID: run.ID, LeftFactID: left.ID, RightFactID: right.ID,
		ConflictType: model.FactConflictValue, Resolution: model.EvidenceConflictUnresolved,
		ResolverMode: "current", EvidenceRefs: `["` + left.ID + `","` + right.ID + `"]`, CreatedAt: now,
	}
	if err := svc.Store.CreateEvidenceConflict(ctx, conflict); err != nil {
		t.Fatalf("create conflict: %v", err)
	}
	if _, err := svc.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		AnswerRunID: run.ID, Input: json.RawMessage(`{"fact_key":"revenue"}`),
	}); err == nil {
		t.Fatal("fact lookup must fail closed on unresolved conflict")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 409 || businessErr.Code != 40996 {
		t.Fatalf("expected 409/40996, got %#v", err)
	}
}

type sqlQueryFixture struct {
	service      *Service
	tenantID     string
	adminUserID  string
	sourceRuleID string
	templateID   string
	connectionID string
	parameterSQL string
	resultSQL    string
	outputSchema string
	targetDSN    string
	seedTenantID string
}

func newSQLQueryFixture(t *testing.T, maxRows int) *sqlQueryFixture {
	t.Helper()
	svc := newToolRegistryService(t)
	adminUserID := id.New()
	adminTenantID := id.New()
	if err := svc.Store.CreateUser(context.Background(), &model.User{
		ID: adminUserID, TenantID: adminTenantID, Username: "sql-query-admin-" + adminUserID,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create fixture admin user: %v", err)
	}
	targetDSN := filepath.Join(t.TempDir(), "target.db")
	targetDB, err := sql.Open("sqlite", targetDSN)
	if err != nil {
		t.Fatalf("open target database: %v", err)
	}
	t.Cleanup(func() { _ = targetDB.Close() })
	if _, err := targetDB.Exec(`CREATE TABLE risk_exposure (tenant_id TEXT NOT NULL, exposure NUMERIC NOT NULL)`); err != nil {
		t.Fatalf("create target table: %v", err)
	}
	tenant := adminTenantID
	user := adminUserID
	connection, err := svc.CreateDBConnection(context.Background(), tenant, user, DBConnectionInput{
		Name: "Risk DB", Driver: model.DBDriverSQLite, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	policy := `{"read_only":true,"max_rows":` + strconv.Itoa(maxRows) + `,"timeout_ms":5000,` +
		`"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["tenant_id","exposure"]}}`
	template, err := svc.CreateQueryTemplate(context.Background(), tenant, user, QueryTemplateInput{
		Name:            "Risk exposure",
		SQLTemplate:     "SELECT tenant_id, exposure FROM risk_exposure WHERE tenant_id = :tenant_id",
		ParameterSchema: `{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		ResultSchema:    `{"type":"object","required":["tenant_id","exposure"],"properties":{"tenant_id":{"type":"string"},"exposure":{"type":"number"}}}`,
		ExecutionPolicy: policy, AuthorizationScope: `{"roles":["tenant_admin"]}`, ConnectionID: connection.ID,
	})
	if err != nil {
		t.Fatalf("create query template: %v", err)
	}
	seedTenantID := "tenant-a"
	if _, err := targetDB.Exec(`INSERT INTO risk_exposure (tenant_id, exposure) VALUES (?, ?)`, seedTenantID, 10.5); err != nil {
		t.Fatalf("seed first row: %v", err)
	}
	if _, err := targetDB.Exec(`INSERT INTO risk_exposure (tenant_id, exposure) VALUES (?, ?)`, "tenant-b", 12.5); err != nil {
		t.Fatalf("seed second row: %v", err)
	}
	outputSchema, err := queryTemplateToolOutputSchema(template.ResultSchema)
	if err != nil {
		t.Fatalf("build tool output schema: %v", err)
	}
	return &sqlQueryFixture{
		service: svc, tenantID: tenant, templateID: template.ID, connectionID: connection.ID,
		adminUserID: adminUserID, parameterSQL: template.ParameterSchema, resultSQL: template.ResultSchema,
		outputSchema: outputSchema, targetDSN: targetDSN, seedTenantID: seedTenantID,
	}
}

func (fixture *sqlQueryFixture) createTool(t *testing.T, tenant, user, implementationRef string) *model.ToolRegistry {
	t.Helper()
	tool, err := fixture.service.CreateToolRegistry(context.Background(), tenant, user, ToolRegistryInput{
		ToolID: "sql_query", Version: "v1", ToolType: model.ToolTypeSQLQuery, Name: "Risk SQL query",
		InputSchema:        fixture.parameterSQL,
		OutputSchema:       fixture.outputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  implementationRef, Active: nil,
	})
	if err != nil {
		t.Fatalf("create sql tool: %v", err)
	}
	rule, err := fixture.service.CreateSourceRoutingRule(context.Background(), tenant, user, SourceRoutingRuleInput{
		AuthorizationScope:  `{"roles":["platform_admin","tenant_admin"]}`,
		SourceType:          model.SourceTypeDB,
		Matcher:             `{"intent":["risk_query"]}`,
		ToolID:              tool.ToolID,
		ToolVersion:         tool.Version,
		PolicyVersion:       "policy-v1",
		Priority:            10,
		ConfidenceThreshold: 0.8,
	})
	if err != nil {
		t.Fatalf("create sql source rule: %v", err)
	}
	fixture.sourceRuleID = rule.ID
	return tool
}

func TestExecuteToolRegistrySQLQuery(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	seedTenant := fixture.tenantID
	dataTenant := fixture.seedTenantID
	user := fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, connection *model.DBConnection) (string, error) {
		if connection.TenantID != seedTenant {
			t.Fatalf("resolver got unexpected connection tenant %s", connection.TenantID)
		}
		return "file:" + fixture.targetDSN, nil
	})
	tool := fixture.createTool(t, seedTenant, user, "query-template:"+fixture.templateID)
	_ = tool
	result, err := svc.ExecuteToolRegistry(ctx, seedTenant, user, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: fixture.sourceRuleID,
		Input:               json.RawMessage(`{"tenant_id":"` + dataTenant + `"}`),
	})
	if err != nil {
		t.Fatalf("execute sql tool: %v", err)
	}
	var output struct {
		Rows      []map[string]interface{} `json:"rows"`
		RowCount  int                      `json:"row_count"`
		Truncated bool                     `json:"truncated"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode sql output: %v", err)
	}
	if len(output.Rows) != 1 || output.RowCount != 1 || output.Truncated {
		t.Fatalf("unexpected result: %+v", output)
	}
	if output.Rows[0]["tenant_id"] != dataTenant {
		t.Fatalf("unexpected tenant_id: %v", output.Rows[0])
	}
	if value, ok := output.Rows[0]["exposure"].(float64); !ok || value != 10.5 {
		t.Fatalf("unexpected exposure: %#v", output.Rows[0]["exposure"])
	}
	if result.Trace == nil || result.Trace.Authorization != "allowed" ||
		result.Trace.PolicyID != fixture.sourceRuleID || result.Trace.Source != "db" {
		t.Fatalf("unexpected authorization trace: %+v", result.Trace)
	}
	for _, layer := range []string{"routing_rule", "tool_registry", "query_template", "db_connection"} {
		if result.Trace.Layers[layer] != "allowed" {
			t.Fatalf("layer %s was not allowed: %+v", layer, result.Trace)
		}
	}
	if !strings.Contains(result.Trace.Resource, "sql_query") {
		t.Fatalf("trace resource must identify the tool, got %s", result.Trace.Resource)
	}
}

func TestExecuteToolRegistrySQLQueryCTE(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	seedTenant, dataTenant, user := fixture.tenantID, fixture.seedTenantID, fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, connection *model.DBConnection) (string, error) {
		if connection.TenantID != seedTenant {
			t.Fatalf("resolver got unexpected connection tenant %s", connection.TenantID)
		}
		return "file:" + fixture.targetDSN, nil
	})
	template, err := svc.Store.GetQueryTemplate(ctx, seedTenant, fixture.templateID)
	if err != nil || template == nil {
		t.Fatalf("get fixture template: %v", err)
	}
	template.SQLTemplate = "WITH scoped (tenant_id, exposure) AS (SELECT tenant_id, exposure FROM risk_exposure) SELECT tenant_id, exposure FROM scoped WHERE tenant_id = :tenant_id"
	if err := svc.Store.UpdateQueryTemplate(ctx, template); err != nil {
		t.Fatalf("update template to CTE: %v", err)
	}
	tool := fixture.createTool(t, seedTenant, user, "query-template:"+fixture.templateID)
	result, err := svc.ExecuteToolRegistry(ctx, seedTenant, user, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: fixture.sourceRuleID,
		Input:               json.RawMessage(`{"tenant_id":"` + dataTenant + `"}`),
	})
	if err != nil {
		t.Fatalf("execute CTE sql tool: %v", err)
	}
	var output struct {
		Rows      []map[string]interface{} `json:"rows"`
		RowCount  int                      `json:"row_count"`
		Truncated bool                     `json:"truncated"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode CTE sql output: %v", err)
	}
	if len(output.Rows) != 1 || output.RowCount != 1 || output.Truncated ||
		output.Rows[0]["tenant_id"] != dataTenant {
		t.Fatalf("unexpected CTE result: %+v", output)
	}
}

func TestExecuteRoutedSQLToolAnswer(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	tenant, dataTenant, user := fixture.tenantID, fixture.seedTenantID, fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, connection *model.DBConnection) (string, error) {
		if connection.TenantID != tenant {
			t.Fatalf("resolver got unexpected connection tenant %s", connection.TenantID)
		}
		return "file:" + fixture.targetDSN, nil
	})
	tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
	requestID := "request-routed-" + id.New()
	const assistantID = "assistant-routed-sql"
	result, err := svc.ExecuteRoutedSQLToolAnswer(ctx, tenant, user, assistantID, RoutedSQLToolAnswerInput{
		SessionID: "session-routed", AssistantID: assistantID, RequestID: requestID,
		Question: "What is the exposure?", Intent: "risk_query", Confidence: 0.9,
		ToolInput: json.RawMessage(`{"tenant_id":"` + dataTenant + `"}`),
	})
	if err != nil {
		t.Fatalf("execute routed sql answer: %v", err)
	}
	if result.Run.RequestID != requestID || result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.AnswerStatus != model.AnswerStatusAnswered {
		t.Fatalf("unexpected routed answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	if result.ToolExecution == nil || result.ToolExecution.Tool.ID != tool.ID ||
		result.ToolExecution.Trace == nil || result.ToolExecution.Trace.PolicyID != fixture.sourceRuleID {
		t.Fatalf("unexpected routed tool execution: %+v", result.ToolExecution)
	}
	var execution []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Snapshot.ExecutionJSON), &execution); err != nil {
		t.Fatalf("decode execution: %v", err)
	}
	if len(execution) != 1 || execution[0]["tool_registry_id"] != tool.ID ||
		execution[0]["source_routing_rule_id"] != fixture.sourceRuleID {
		t.Fatalf("unexpected snapshot execution: %+v", execution)
	}
	if strings.Contains(result.Snapshot.ExecutionJSON, fixture.targetDSN) {
		t.Fatal("routed answer must not leak database DSN")
	}
}

func TestExecuteRoutedSQLToolAnswerNoMatchRecordsFailedDelivery(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	_ = fixture.createTool(t, fixture.tenantID, fixture.adminUserID, "query-template:"+fixture.templateID)
	requestID := "request-routed-miss-" + id.New()
	const assistantID = "assistant-routed-sql-miss"
	_, err := fixture.service.ExecuteRoutedSQLToolAnswer(ctx, fixture.tenantID, fixture.adminUserID, assistantID, RoutedSQLToolAnswerInput{
		SessionID: "session-routed-miss", AssistantID: assistantID, RequestID: requestID,
		Question: "What is the exposure?", Intent: "unknown_query", Confidence: 0.9,
		ToolInput: json.RawMessage(`{"tenant_id":"tenant-a"}`),
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 404 {
		t.Fatalf("expected route miss, got %#v", err)
	}
	run, err := fixture.service.Store.GetAnswerRunByRequest(ctx, fixture.tenantID, requestID)
	if err != nil || run == nil {
		t.Fatalf("failed answer run was not recorded: %v", err)
	}
	if run.AnswerStatus != model.AnswerStatusSystemFailed || run.CompletionReason != model.CompletionReasonSystemFailed {
		t.Fatalf("unexpected failed answer run: %+v", run)
	}
}

func TestExecuteToolRegistrySQLQueryFourLayerAuthorization(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	tenant, adminUser := fixture.tenantID, fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
		return "file:" + fixture.targetDSN, nil
	})
	tool := fixture.createTool(t, tenant, adminUser, "query-template:"+fixture.templateID)
	input := ToolExecutionInput{
		SourceRoutingRuleID: fixture.sourceRuleID,
		Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
	}
	if _, err := svc.ExecuteToolRegistry(ctx, tenant, adminUser, tool.ID, input); err != nil {
		t.Fatalf("four-layer authorized sql query: %v", err)
	}

	layers := []struct {
		name      string
		authorize func() error
	}{
		{"source routing rule", func() error {
			rule, err := svc.Store.GetSourceRoutingRule(ctx, tenant, fixture.sourceRuleID)
			if err != nil || rule == nil {
				return err
			}
			rule.AuthorizationScope = `{"roles":["operator"]}`
			return svc.Store.UpdateSourceRoutingRule(ctx, rule)
		}},
		{"tool registry", func() error {
			tool, err := svc.Store.GetToolRegistry(ctx, tenant, tool.ID)
			if err != nil || tool == nil {
				return err
			}
			tool.AuthorizationScope = `{"roles":["operator"]}`
			return svc.Store.UpdateToolRegistry(ctx, tool)
		}},
		{"query template", func() error {
			template, err := svc.Store.GetQueryTemplate(ctx, tenant, fixture.templateID)
			if err != nil || template == nil {
				return err
			}
			template.AuthorizationScope = `{"roles":["operator"]}`
			return svc.Store.UpdateQueryTemplate(ctx, template)
		}},
		{"db connection", func() error {
			connection, err := svc.Store.GetDBConnection(ctx, tenant, fixture.connectionID)
			if err != nil || connection == nil {
				return err
			}
			connection.AuthorizationScope = `{"roles":["operator"]}`
			return svc.Store.UpdateDBConnection(ctx, connection)
		}},
	}
	for _, layer := range layers {
		t.Run(layer.name, func(t *testing.T) {
			if err := layer.authorize(); err != nil {
				t.Fatalf("change %s scope: %v", layer.name, err)
			}
			_, err := svc.ExecuteToolRegistry(ctx, tenant, adminUser, tool.ID, input)
			assertSQLQueryStatus(t, err, 403, 40310)
		})
	}
	audits, _, err := svc.ListAudits(ctx, tenant, 1, 20, repository.AuditFilter{
		Action: "tool_registry.authorization_denied", Resource: "tool-registry", Result: "DENIED",
	})
	if err != nil {
		t.Fatalf("list authorization denial audits: %v", err)
	}
	if len(audits) != len(layers) {
		t.Fatalf("expected %d denial audits, got %d", len(layers), len(audits))
	}
	for _, audit := range audits {
		if !strings.Contains(audit.DetailJSON, `"authorization":"denied"`) ||
			strings.Contains(audit.DetailJSON, "risk_exposure") {
			t.Fatalf("unexpected denial audit: %s", audit.DetailJSON)
		}
	}
}

func TestSQLQueryToolRegistrationValidatesTemplateContract(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	tenant := fixture.tenantID
	user := id.New()
	input := ToolRegistryInput{
		ToolID: "sql_query", Version: "v1", ToolType: model.ToolTypeSQLQuery, Name: "Risk SQL query",
		InputSchema:        fixture.parameterSQL,
		OutputSchema:       fixture.outputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:sql_query", Active: nil,
	}
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("sql_query must reference a governed query template")
	}
	input.ImplementationRef = "query-template:"
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("empty query template reference must fail")
	}
	input.ImplementationRef = "query-template:" + fixture.templateID
	input.InputSchema = `{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"number"}}}`
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("sql_query input schema must match template")
	}
	input.InputSchema = fixture.parameterSQL
	input.OutputSchema = objectSchema([]string{"rows"}, "string")
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("sql_query output schema must use governed envelope")
	}
}

func TestExecuteToolRegistrySQLQueryFailClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("missing resolver", func(t *testing.T) {
		fixture := newSQLQueryFixture(t, 10)
		tenant := fixture.tenantID
		user := fixture.adminUserID
		tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
		_, err := fixture.service.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: fixture.sourceRuleID,
			Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
		})
		assertSQLQueryStatus(t, err, 503, 50300)
	})
	t.Run("cross tenant", func(t *testing.T) {
		fixture := newSQLQueryFixture(t, 10)
		tenant := fixture.tenantID
		user := fixture.adminUserID
		tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
		fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
			t.Fatal("cross-tenant execution must not resolve credentials")
			return "", nil
		})
		_, err := fixture.service.ExecuteToolRegistry(ctx, id.New(), user, tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: fixture.sourceRuleID,
			Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
		})
		assertSQLQueryStatus(t, err, 404, 404)
	})
	t.Run("inactive template", func(t *testing.T) {
		fixture := newSQLQueryFixture(t, 10)
		tenant := fixture.tenantID
		user := fixture.adminUserID
		tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
		template, err := fixture.service.Store.GetQueryTemplate(ctx, tenant, fixture.templateID)
		if err != nil {
			t.Fatalf("get template: %v", err)
		}
		template.Active = false
		if err := fixture.service.Store.UpdateQueryTemplate(ctx, template); err != nil {
			t.Fatalf("deactivate template: %v", err)
		}
		fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
			t.Fatal("inactive template must not resolve credentials")
			return "", nil
		})
		_, err = fixture.service.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: fixture.sourceRuleID,
			Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
		})
		assertSQLQueryStatus(t, err, 403, 40310)
	})
	t.Run("missing connection", func(t *testing.T) {
		fixture := newSQLQueryFixture(t, 10)
		tenant := fixture.tenantID
		user := fixture.adminUserID
		tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
		if err := fixture.service.DeleteDBConnection(ctx, tenant, user, fixture.connectionID); err != nil {
			t.Fatalf("delete connection: %v", err)
		}
		fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
			t.Fatal("missing connection must not resolve credentials")
			return "", nil
		})
		_, err := fixture.service.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
			SourceRoutingRuleID: fixture.sourceRuleID,
			Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
		})
		assertSQLQueryStatus(t, err, 403, 40310)
	})
}

func TestExecuteToolRegistrySQLQueryTruncatesAndValidatesRows(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 1)
	tenant := fixture.tenantID
	user := fixture.adminUserID
	targetDB, err := sql.Open("sqlite", fixture.targetDSN)
	if err != nil {
		t.Fatalf("reopen target: %v", err)
	}
	defer targetDB.Close()
	if _, err := targetDB.Exec(`INSERT INTO risk_exposure (tenant_id, exposure) VALUES (?, ?)`, "tenant-a", 11.5); err != nil {
		t.Fatalf("seed duplicate row: %v", err)
	}
	fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
		return "file:" + fixture.targetDSN, nil
	})
	tool := fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
	result, err := fixture.service.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: fixture.sourceRuleID,
		Input:               json.RawMessage(`{"tenant_id":"tenant-a"}`),
	})
	if err != nil {
		t.Fatalf("execute truncated query: %v", err)
	}
	var output struct {
		RowCount  int  `json:"row_count"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.RowCount != 1 || !output.Truncated {
		t.Fatalf("unexpected truncation: %+v", output)
	}

	if _, err := targetDB.Exec(`INSERT INTO risk_exposure (tenant_id, exposure) VALUES (?, ?)`, "tenant-bad", "not-a-number"); err != nil {
		t.Fatalf("seed invalid row: %v", err)
	}
	_, err = fixture.service.ExecuteToolRegistry(ctx, tenant, user, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: fixture.sourceRuleID,
		Input:               json.RawMessage(`{"tenant_id":"tenant-bad"}`),
	})
	if err == nil {
		t.Fatal("result row schema mismatch must fail")
	}
}

func assertSQLQueryStatus(t *testing.T, err error, status, code int) {
	t.Helper()
	if err == nil {
		t.Fatal("expected failure")
	}
	businessErr, ok := err.(*httperr.Error)
	if !ok || businessErr.Status != status || businessErr.Code != code {
		t.Fatalf("expected %d/%d, got %#v", status, code, err)
	}
}

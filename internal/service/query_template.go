package service

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type QueryTemplateInput struct {
	Name               string `json:"name" binding:"required"`
	SQLTemplate        string `json:"sql_template" binding:"required"`
	ParameterSchema    string `json:"parameter_schema" binding:"required"`
	ResultSchema       string `json:"result_schema" binding:"required"`
	ExecutionPolicy    string `json:"execution_policy" binding:"required"`
	AuthorizationScope string `json:"authorization_scope" binding:"required"`
	ConnectionID       string `json:"connection_id" binding:"required"`
	Active             *bool  `json:"active"`
}

type QueryExecutionPolicy struct {
	ReadOnly       bool                `json:"read_only"`
	MaxRows        int                 `json:"max_rows"`
	TimeoutMs      int                 `json:"timeout_ms"`
	AllowedTables  []string            `json:"allowed_tables"`
	AllowedColumns map[string][]string `json:"allowed_columns"`
}

var readOnlyScalarFunctions = map[string]struct{}{
	"abs": {}, "avg": {}, "ceil": {}, "coalesce": {}, "count": {}, "floor": {},
	"length": {}, "lower": {}, "max": {}, "min": {}, "nullif": {}, "round": {},
	"substr": {}, "substring": {}, "sum": {}, "trim": {}, "upper": {},
}

var validIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func notFoundQueryTemplate() error { return httperr.NotFound("query template not found") }

func (s *Service) ListQueryTemplates(
	ctx context.Context, tenantID string, filter repository.QueryTemplateFilter, page, pageSize int,
) ([]model.QueryTemplate, int64, error) {
	filter.ConnectionID = strings.TrimSpace(filter.ConnectionID)
	return s.Store.ListQueryTemplates(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetQueryTemplate(
	ctx context.Context, tenantID, templateID string,
) (*model.QueryTemplate, error) {
	template, err := s.Store.GetQueryTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, notFoundQueryTemplate()
	}
	return template, nil
}

func (s *Service) CreateQueryTemplate(
	ctx context.Context, tenantID, userID string, input QueryTemplateInput,
) (*model.QueryTemplate, error) {
	now := time.Now().UTC()
	template, err := buildQueryTemplate(tenantID, userID, input, now)
	if err != nil {
		return nil, err
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := ensureQueryTemplateValid(ctx, tx, tenantID, template, ""); err != nil {
			return err
		}
		if err := tx.CreateQueryTemplate(ctx, template); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForQueryTemplate(template, "query_template.created", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return template, nil
}

func (s *Service) UpdateQueryTemplate(
	ctx context.Context, tenantID, userID, templateID string, input QueryTemplateInput,
) (*model.QueryTemplate, error) {
	existing, err := s.GetQueryTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	updated, err := buildQueryTemplate(tenantID, userID, input, now)
	if err != nil {
		return nil, err
	}
	updated.ID = existing.ID
	updated.CreatedBy = existing.CreatedBy
	updated.CreatedAt = existing.CreatedAt
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := ensureQueryTemplateValid(ctx, tx, tenantID, updated, existing.ID); err != nil {
			return err
		}
		if err := tx.UpdateQueryTemplate(ctx, updated); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForQueryTemplate(updated, "query_template.updated", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteQueryTemplate(
	ctx context.Context, tenantID, userID, templateID string,
) error {
	template, err := s.GetQueryTemplate(ctx, tenantID, templateID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteQueryTemplate(ctx, tenantID, template.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForQueryTemplate(template, "query_template.deleted", userID, now))
	})
}

func buildQueryTemplate(
	tenantID, userID string, input QueryTemplateInput, now time.Time,
) (*model.QueryTemplate, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return nil, httperr.BadRequest(40180, "name must contain 1 to 128 characters")
	}
	sqlTemplate := strings.TrimSpace(input.SQLTemplate)
	if sqlTemplate == "" || len(sqlTemplate) > 16000 {
		return nil, httperr.BadRequest(40181, "sql_template must contain 1 to 16000 characters")
	}
	parameterSchema, err := toolRegistryJSON(input.ParameterSchema, "parameter_schema")
	if err != nil {
		return nil, err
	}
	resultSchema, err := toolRegistryJSON(input.ResultSchema, "result_schema")
	if err != nil {
		return nil, err
	}
	authorizationScope, err := validateAuthorizationScope(input.AuthorizationScope, "authorization_scope")
	if err != nil {
		return nil, err
	}
	connectionID := strings.TrimSpace(input.ConnectionID)
	if connectionID == "" {
		return nil, httperr.BadRequest(40182, "connection_id is required")
	}
	executionPolicy, policy, err := validateQueryExecutionPolicy(input.ExecutionPolicy)
	if err != nil {
		return nil, err
	}
	if err := validateQueryTemplateSQL(sqlTemplate, policy); err != nil {
		return nil, err
	}
	if err := validateQueryParameters(sqlTemplate, parameterSchema); err != nil {
		return nil, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	return &model.QueryTemplate{
		ID: id.New(), TenantID: tenantID, Name: name, SQLTemplate: sqlTemplate,
		ParameterSchema: parameterSchema, ResultSchema: resultSchema,
		ExecutionPolicy: executionPolicy, AuthorizationScope: authorizationScope,
		ConnectionID: connectionID, Active: active,
		CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func ensureQueryTemplateValid(
	ctx context.Context, store repository.Store, tenantID string,
	template *model.QueryTemplate, excludeID string,
) error {
	duplicate, err := store.GetQueryTemplateByName(ctx, tenantID, template.Name, excludeID)
	if err != nil {
		return err
	}
	if duplicate != nil {
		return httperr.New(409, 40199, "query template name already exists")
	}
	connection, err := store.GetDBConnection(ctx, tenantID, template.ConnectionID)
	if err != nil {
		return err
	}
	if connection == nil {
		return httperr.BadRequest(40182, "connection_id must reference a database connection")
	}
	return nil
}

func validateQueryExecutionPolicy(raw string) (string, QueryExecutionPolicy, error) {
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &document); err != nil || document == nil {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40183, "execution_policy must be a JSON object")
	}
	var policy QueryExecutionPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40183, "execution_policy must be a JSON object")
	}
	if !policy.ReadOnly {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40184, "execution_policy.read_only must be true in v0.2.0")
	}
	if policy.MaxRows < 1 || policy.MaxRows > 10000 {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40185, "execution_policy.max_rows must be between 1 and 10000")
	}
	if policy.TimeoutMs < 1 || policy.TimeoutMs > 60000 {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40186, "execution_policy.timeout_ms must be between 1 and 60000")
	}
	tables := make(map[string]struct{}, len(policy.AllowedTables))
	for _, rawTable := range policy.AllowedTables {
		table := strings.ToLower(strings.TrimSpace(rawTable))
		if !validIdentifier.MatchString(table) {
			return "", QueryExecutionPolicy{}, httperr.BadRequest(40187, "execution_policy.allowed_tables contains an invalid table name")
		}
		tables[table] = struct{}{}
	}
	if len(tables) == 0 {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40187, "execution_policy.allowed_tables must not be empty")
	}
	columns := make(map[string][]string, len(policy.AllowedColumns))
	for rawTable, rawColumns := range policy.AllowedColumns {
		table := strings.ToLower(strings.TrimSpace(rawTable))
		if _, ok := tables[table]; !ok {
			return "", QueryExecutionPolicy{}, httperr.BadRequest(40188, "execution_policy.allowed_columns references an unknown table")
		}
		seen := make(map[string]struct{}, len(rawColumns))
		normalized := make([]string, 0, len(rawColumns))
		for _, rawColumn := range rawColumns {
			column := strings.ToLower(strings.TrimSpace(rawColumn))
			if !validIdentifier.MatchString(column) {
				return "", QueryExecutionPolicy{}, httperr.BadRequest(40188, "execution_policy.allowed_columns contains an invalid column name")
			}
			if _, exists := seen[column]; exists {
				return "", QueryExecutionPolicy{}, httperr.BadRequest(40188, "execution_policy.allowed_columns contains a duplicate column")
			}
			seen[column] = struct{}{}
			normalized = append(normalized, column)
		}
		if len(normalized) == 0 {
			return "", QueryExecutionPolicy{}, httperr.BadRequest(40188, "execution_policy.allowed_columns entries must not be empty")
		}
		columns[table] = normalized
	}
	for table := range tables {
		if len(columns[table]) == 0 {
			return "", QueryExecutionPolicy{}, httperr.BadRequest(40188, "execution_policy.allowed_columns must define every allowed table")
		}
	}
	encoded, err := json.Marshal(QueryExecutionPolicy{
		ReadOnly: true, MaxRows: policy.MaxRows, TimeoutMs: policy.TimeoutMs,
		AllowedTables: sortedLowercaseValues(policy.AllowedTables), AllowedColumns: columns,
	})
	if err != nil {
		return "", QueryExecutionPolicy{}, httperr.BadRequest(40183, "execution_policy must be a JSON object")
	}
	return string(encoded), QueryExecutionPolicy{
		ReadOnly: true, MaxRows: policy.MaxRows, TimeoutMs: policy.TimeoutMs,
		AllowedTables: sortedLowercaseValues(policy.AllowedTables), AllowedColumns: columns,
	}, nil
}

func validateQueryParameters(rawSQL, parameterSchema string) error {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(parameterSchema), &schema); err != nil {
		return httperr.BadRequest(40180, "parameter_schema must be valid JSON")
	}
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return httperr.BadRequest(40125, "parameter_schema.properties must be a non-empty object")
	}
	requiredNames := make(map[string]struct{}, len(properties))
	if rawRequired, ok := schema["required"].([]interface{}); ok {
		for _, rawName := range rawRequired {
			if name, ok := rawName.(string); ok {
				requiredNames[name] = struct{}{}
			}
		}
	}
	placeholders, _, err := collectQueryBindVariables(rawSQL)
	if err != nil {
		return err
	}
	for name := range placeholders {
		if _, allowed := properties[name]; !allowed {
			return httperr.BadRequest(40196, "sql_template bind variable is missing from parameter_schema")
		}
	}
	for name := range requiredNames {
		if _, present := placeholders[name]; !present {
			return httperr.BadRequest(40196, "sql_template is missing a required bind variable "+name)
		}
	}
	return nil
}

func collectQueryBindVariables(rawSQL string) (map[string]struct{}, []string, error) {
	_, names, ordered, err := normalizeQueryBindVariables(rawSQL)
	return names, ordered, err
}

func sortedLowercaseValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
	}
	sort.Strings(normalized)
	return normalized
}

func auditForQueryTemplate(
	template *model.QueryTemplate, action, userID string, now time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: template.TenantID, ActorTenantID: template.TenantID, TargetTenantID: template.TenantID,
		UserID: userID, Action: action, Resource: "query-template", ResourceID: template.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"name": template.Name, "connection_id": template.ConnectionID, "active": template.Active,
			"max_rows":   decodeQueryPolicyInt(template.ExecutionPolicy, "max_rows"),
			"timeout_ms": decodeQueryPolicyInt(template.ExecutionPolicy, "timeout_ms"),
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

func decodeQueryPolicyInt(raw, field string) int {
	var policy QueryExecutionPolicy
	if json.Unmarshal([]byte(raw), &policy) != nil {
		return 0
	}
	if field == "timeout_ms" {
		return policy.TimeoutMs
	}
	return policy.MaxRows
}

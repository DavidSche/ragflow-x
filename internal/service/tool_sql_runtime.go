package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func sqlExecutionAuditDetails(
	tool *model.ToolRegistry, output map[string]interface{}, trace *AuthorizationTrace,
) map[string]interface{} {
	details := map[string]interface{}{
		"tool_id": tool.ToolID, "tool_version": tool.Version, "tool_type": tool.ToolType,
	}
	if tool.ToolType != model.ToolTypeSQLQuery {
		return details
	}
	templateID, _ := parseQueryTemplateImplementationRef(tool.ImplementationRef)
	details["query_template_id"] = templateID
	if trace != nil {
		details["source_routing_rule_id"] = trace.PolicyID
		details["authorization_trace"] = trace
	}
	details["row_count"] = output["row_count"]
	details["truncated"] = output["truncated"]
	return details
}

func (s *Service) executeSQLQueryTool(
	ctx context.Context, tenantID, userID string, executionInput ToolExecutionInput,
	input map[string]interface{}, tool *model.ToolRegistry,
) (map[string]interface{}, *AuthorizationTrace, error) {
	template, connection, trace, err := s.authorizeSQLQuerySources(
		ctx, tenantID, userID, executionInput, tool,
	)
	if err != nil {
		if trace != nil {
			if auditErr := s.recordSQLQueryAuthorizationDenial(
				ctx, tenantID, userID, tool, trace, executionInput,
			); auditErr != nil {
				return nil, nil, auditErr
			}
		}
		return nil, nil, err
	}
	if !jsonEqual(tool.InputSchema, template.ParameterSchema) {
		return nil, trace, httperr.BadRequest(40129, "input_schema does not match the query template")
	}
	expectedOutput, err := queryTemplateToolOutputSchema(template.ResultSchema)
	if err != nil {
		return nil, trace, err
	}
	if !jsonEqual(tool.OutputSchema, expectedOutput) {
		return nil, trace, httperr.BadRequest(40130, "output_schema does not match the query template")
	}
	var policy QueryExecutionPolicy
	if err := json.Unmarshal([]byte(template.ExecutionPolicy), &policy); err != nil || !policy.ReadOnly {
		return nil, trace, httperr.BadRequest(40183, "execution_policy must define a read-only query")
	}
	if err := validateQueryTemplateSQL(template.SQLTemplate, policy); err != nil {
		return nil, trace, err
	}
	if err := validateQueryParameters(template.SQLTemplate, template.ParameterSchema); err != nil {
		return nil, trace, err
	}
	bindVariables, bindOrder, err := collectQueryBindVariables(template.SQLTemplate)
	if err != nil {
		return nil, trace, err
	}
	for name := range bindVariables {
		if _, exists := input[name]; !exists {
			return nil, trace, httperr.BadRequest(40131, "input."+name+" is required")
		}
	}
	args := make([]interface{}, 0, len(bindOrder))
	for _, name := range bindOrder {
		value, err := convertQueryBindValue(input[name])
		if err != nil {
			return nil, trace, err
		}
		args = append(args, value)
	}
	if s.SecretResolver == nil {
		return nil, trace, httperr.New(503, 50300, "database connection secret resolver is not configured")
	}
	dsn, err := s.SecretResolver.ResolveDBDSN(ctx, connection)
	if err != nil || strings.TrimSpace(dsn) == "" {
		return nil, trace, httperr.New(502, 50200, "database credential resolution failed")
	}
	queryContext, cancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutMs)*time.Millisecond)
	defer cancel()
	query, err := renderQuerySQL(template.SQLTemplate, connection.Driver, bindOrder, args)
	if err != nil {
		return nil, trace, err
	}
	columns, records, truncated, err := executeReadOnlySQLQuery(
		queryContext, connection, dsn, query, args, policy.MaxRows,
	)
	if err != nil {
		return nil, trace, httperr.New(502, 50201, "sql query execution failed")
	}
	if truncated {
		records = records[:policy.MaxRows]
	}
	resultSchema, err := decodeRuntimeToolSchema(template.ResultSchema)
	if err != nil {
		return nil, trace, err
	}
	rows := make([]interface{}, 0, len(records))
	for index, values := range records {
		if len(columns) != len(values) {
			return nil, trace, httperr.BadRequest(40193, "query result columns are inconsistent")
		}
		row := make(map[string]interface{}, len(values))
		for columnIndex, column := range columns {
			if _, exists := row[column]; exists {
				return nil, trace, httperr.BadRequest(40193, "query result contains duplicate columns")
			}
			columnSchema := resultSchema.Properties[column]
			row[column] = jsonSafeQueryValue(values[columnIndex], columnSchema.Type)
		}
		if err := validateRuntimeToolValue(row, resultSchema, fmt.Sprintf("output.rows[%d]", index)); err != nil {
			return nil, trace, err
		}
		rows = append(rows, row)
	}
	return map[string]interface{}{
		"rows":      rows,
		"row_count": json.Number(strconv.Itoa(len(rows))),
		"truncated": truncated,
	}, trace, nil
}

func (s *Service) authorizeSQLQuerySources(
	ctx context.Context, tenantID, userID string, executionInput ToolExecutionInput,
	tool *model.ToolRegistry,
) (*model.QueryTemplate, *model.DBConnection, *AuthorizationTrace, error) {
	ruleID := strings.TrimSpace(executionInput.SourceRoutingRuleID)
	trace := newSQLAuthorizationTrace(ruleID, tool.ToolID)
	if ruleID == "" {
		trace.deny(authorizationLayerRoutingRule)
		return nil, nil, trace, forbiddenSQLSource()
	}
	rule, err := s.Store.GetSourceRoutingRule(ctx, tenantID, ruleID)
	if err != nil || rule == nil || !rule.Active || rule.SourceType != model.SourceTypeDB {
		trace.deny(authorizationLayerRoutingRule)
		return nil, nil, trace, forbiddenSQLSource()
	}
	trace.PolicyVersion = rule.PolicyVersion
	user, err := s.Store.GetUser(ctx, userID)
	if err != nil || user == nil || user.TenantID != tenantID || user.Status != model.UserStatusActive {
		trace.deny(authorizationLayerRoutingRule)
		return nil, nil, trace, forbiddenSQLSource()
	}
	if rule.ToolID != tool.ToolID || rule.ToolVersion != tool.Version ||
		!userAllowedByScope(rule.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerToolRegistry)
		return nil, nil, trace, forbiddenSQLSource()
	}
	if !tool.Active || tool.ToolType != model.ToolTypeSQLQuery ||
		!userAllowedByScope(tool.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerToolRegistry)
		return nil, nil, trace, forbiddenSQLSource()
	}
	templateID, err := parseQueryTemplateImplementationRef(tool.ImplementationRef)
	if err != nil {
		trace.deny(authorizationLayerToolRegistry)
		return nil, nil, trace, forbiddenSQLSource()
	}
	template, err := s.Store.GetQueryTemplate(ctx, tenantID, templateID)
	if err != nil || template == nil || !template.Active ||
		tool.ImplementationRef != queryTemplateRefPrefix+template.ID {
		trace.deny(authorizationLayerQueryTemplate)
		return nil, nil, trace, forbiddenSQLSource()
	}
	if !userAllowedByScope(template.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerQueryTemplate)
		return nil, nil, trace, forbiddenSQLSource()
	}
	connection, err := s.Store.GetDBConnection(ctx, tenantID, template.ConnectionID)
	if err != nil || connection == nil || connection.ID != template.ConnectionID {
		trace.deny(authorizationLayerDBConnection)
		return nil, nil, trace, forbiddenSQLSource()
	}
	if !connection.ReadOnly || !userAllowedByScope(connection.AuthorizationScope, user.Role) {
		trace.deny(authorizationLayerDBConnection)
		return nil, nil, trace, forbiddenSQLSource()
	}
	return template, connection, trace, nil
}

func forbiddenSQLSource() error {
	return httperr.New(403, 40310, "FORBIDDEN_SOURCE")
}

func (s *Service) recordSQLQueryAuthorizationDenial(
	ctx context.Context, tenantID, userID string, tool *model.ToolRegistry,
	trace *AuthorizationTrace, executionInput ToolExecutionInput,
) error {
	templateID, _ := parseQueryTemplateImplementationRef(tool.ImplementationRef)
	now := time.Now().UTC()
	return s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: userID, Action: "tool_registry.authorization_denied", Resource: "tool-registry",
		ResourceID: tool.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"tool_id":                tool.ToolID,
			"tool_version":           tool.Version,
			"tool_type":              tool.ToolType,
			"query_template_id":      templateID,
			"source_routing_rule_id": executionInput.SourceRoutingRuleID,
			"authorization_trace":    trace,
		}),
		At: now, Result: "DENIED", AuthorizationDecision: "DENY",
		AuthorizationPolicyVersion: authorizationPolicyVersion(trace),
	})
}
func convertQueryBindValue(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case string, bool, nil:
		return typed, nil
	case json.Number:
		rational, ok := new(big.Rat).SetString(typed.String())
		if !ok {
			return nil, httperr.BadRequest(40131, "bind value must be a valid number")
		}
		if rational.IsInt() {
			intValue := new(big.Int).Quo(rational.Num(), rational.Denom())
			if intValue.IsInt64() && new(big.Rat).SetInt64(intValue.Int64()).Cmp(rational) == 0 {
				return intValue.Int64(), nil
			}
		}
		floatValue, _ := rational.Float64()
		return floatValue, nil
	default:
		return nil, httperr.BadRequest(40131, "bind value has an unsupported type")
	}
}

func renderQuerySQL(rawSQL, driver string, bindOrder []string, args []interface{}) (string, error) {
	if len(args) != len(bindOrder) {
		return "", httperr.BadRequest(40131, "bind value count does not match sql_template")
	}
	var rendered strings.Builder
	for index := 0; index < len(rawSQL); {
		character := rawSQL[index]
		switch character {
		case '\'':
			end := index + 1
			for end < len(rawSQL) {
				if rawSQL[end] == '\'' {
					if end+1 < len(rawSQL) && rawSQL[end+1] == '\'' {
						end += 2
						continue
					}
					end++
					break
				}
				if rawSQL[end] == '\\' && end+1 < len(rawSQL) {
					end += 2
					continue
				}
				end++
			}
			if end >= len(rawSQL) {
				return "", httperr.BadRequest(40189, "sql_template contains an unterminated string")
			}
			rendered.WriteString(rawSQL[index:end])
			index = end
		case '"', '`':
			end, err := findSQLIdentifierEnd(rawSQL, index)
			if err != nil {
				return "", httperr.BadRequest(40189, "sql_template contains an unterminated identifier")
			}
			rendered.WriteString(rawSQL[index:end])
			index = end
		case '-':
			if index+1 < len(rawSQL) && rawSQL[index+1] == '-' {
				end := strings.IndexByte(rawSQL[index:], '\n')
				if end < 0 {
					end = len(rawSQL)
				} else {
					end += index
				}
				rendered.WriteString(rawSQL[index:end])
				index = end
				continue
			}
			rendered.WriteByte(character)
			index++
		case '/':
			if index+1 < len(rawSQL) && rawSQL[index+1] == '*' {
				end := strings.Index(rawSQL[index+2:], "*/")
				if end < 0 {
					return "", httperr.BadRequest(40189, "sql_template contains an unterminated comment")
				}
				end += index + 4
				rendered.WriteString(rawSQL[index:end])
				index = end
				continue
			}
			rendered.WriteByte(character)
			index++
		case ':':
			if index+1 >= len(rawSQL) || !isQueryBindStart(rawSQL[index+1]) {
				rendered.WriteByte(character)
				index++
				continue
			}
			end := index + 1
			for end < len(rawSQL) && isQueryBindPart(rawSQL[end]) {
				end++
			}
			name := rawSQL[index+1 : end]
			if occurrenceOf(name, bindOrder) == 0 {
				return "", httperr.BadRequest(40196, "sql_template contains an unknown bind variable")
			}
			if driver == model.DBDriverPostgres {
				rendered.WriteString("$" + strconv.Itoa(occurrenceOf(name, bindOrder[:positionOf(name, bindOrder)+1])))
			} else {
				rendered.WriteString("?")
			}
			index = end
		default:
			rendered.WriteByte(character)
			index++
		}
	}
	return rendered.String(), nil
}

func isQueryBindStart(value byte) bool {
	return value == '_' || (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func isQueryBindPart(value byte) bool {
	return isQueryBindStart(value) || (value >= '0' && value <= '9')
}

func positionOf(name string, bindOrder []string) int {
	for index, candidate := range bindOrder {
		if candidate == name {
			return index
		}
	}
	return -1
}

func occurrenceOf(name string, bindOrder []string) int {
	count := 0
	for _, candidate := range bindOrder {
		if candidate == name {
			count++
		}
	}
	return count
}

func executeReadOnlySQLQuery(
	ctx context.Context, connection *model.DBConnection, dsn, query string, args []interface{}, maxRows int,
) ([]string, [][]interface{}, bool, error) {
	database, err := sql.Open(databaseDriverName(connection.Driver), dsn)
	if err != nil {
		return nil, nil, false, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	database.SetConnMaxLifetime(0)
	database.SetConnMaxIdleTime(0)
	databaseConnection, err := database.Conn(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	defer databaseConnection.Close()
	rollbackReadOnlyTransaction := func() {}
	switch connection.Driver {
	case model.DBDriverSQLite:
		if _, err := databaseConnection.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return nil, nil, false, err
		}
	case model.DBDriverMySQL:
		if _, err := databaseConnection.ExecContext(ctx, "START TRANSACTION READ ONLY"); err != nil {
			return nil, nil, false, err
		}
		rollbackReadOnlyTransaction = func() {
			_, _ = databaseConnection.ExecContext(context.Background(), "ROLLBACK")
		}
	case model.DBDriverPostgres:
		if _, err := databaseConnection.ExecContext(ctx, "BEGIN READ ONLY"); err != nil {
			return nil, nil, false, err
		}
		rollbackReadOnlyTransaction = func() {
			_, _ = databaseConnection.ExecContext(context.Background(), "ROLLBACK")
		}
	default:
		return nil, nil, false, httperr.BadRequest(40171, "driver must be postgres, mysql or sqlite")
	}
	defer rollbackReadOnlyTransaction()
	rows, err := databaseConnection.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	var records [][]interface{}
	for len(records) < maxRows+1 && rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, nil, false, err
		}
		records = append(records, values)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return columns, records, len(records) == maxRows+1, nil
}

func databaseDriverName(driver string) string {
	switch driver {
	case model.DBDriverPostgres:
		return "pgx"
	case model.DBDriverMySQL:
		return "mysql"
	case model.DBDriverSQLite:
		return "sqlite"
	default:
		return ""
	}
}

func jsonSafeQueryValue(value interface{}, schemaType string) interface{} {
	switch typed := value.(type) {
	case nil, bool, json.Number:
		return typed
	case int:
		return json.Number(strconv.Itoa(typed))
	case int64:
		return json.Number(strconv.FormatInt(typed, 10))
	case float64:
		return runtimeNumber(new(big.Rat).SetFloat64(typed))
	case []byte:
		text := string(typed)
		if schemaType != "number" && schemaType != "integer" {
			return text
		}
		rational, ok := new(big.Rat).SetString(text)
		if !ok {
			return text
		}
		return runtimeNumber(rational)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case string:
		if schemaType == "number" || schemaType == "integer" {
			if rational, ok := new(big.Rat).SetString(typed); ok {
				return runtimeNumber(rational)
			}
		}
		return typed
	default:
		return typed
	}
}

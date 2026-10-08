package service

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const toolIDPattern = `^[a-z][a-z0-9_]{1,63}$`
const queryTemplateRefPrefix = "query-template:"

type ToolRegistryInput struct {
	ToolID             string `json:"tool_id" binding:"required"`
	Version            string `json:"version" binding:"required"`
	ToolType           string `json:"tool_type" binding:"required"`
	Name               string `json:"name" binding:"required"`
	InputSchema        string `json:"input_schema" binding:"required"`
	OutputSchema       string `json:"output_schema" binding:"required"`
	AuthorizationScope string `json:"authorization_scope"`
	ImplementationRef  string `json:"implementation_ref" binding:"required"`
	Active             *bool  `json:"active"`
}

var validToolID = regexp.MustCompile(toolIDPattern)

var builtinToolSpecs = map[string]model.ToolRegistry{
	"add":                     {ToolID: "add", ToolType: model.ToolTypeCalculation, InputSchema: objectSchema([]string{"a", "b"}, "number", "number"), OutputSchema: objectSchema([]string{"result"}, "number"), ImplementationRef: "builtin:add"},
	"subtract":                {ToolID: "subtract", ToolType: model.ToolTypeCalculation, InputSchema: objectSchema([]string{"a", "b"}, "number", "number"), OutputSchema: objectSchema([]string{"result"}, "number"), ImplementationRef: "builtin:subtract"},
	"percentage_diff":         {ToolID: "percentage_diff", ToolType: model.ToolTypeCalculation, InputSchema: objectSchema([]string{"a", "b"}, "number", "number"), OutputSchema: objectSchema([]string{"result"}, "number"), ImplementationRef: "builtin:percentage_diff"},
	"sum":                     {ToolID: "sum", ToolType: model.ToolTypeCalculation, InputSchema: sumInputSchema(), OutputSchema: objectSchema([]string{"result"}, "number"), ImplementationRef: "builtin:sum"},
	"compare":                 {ToolID: "compare", ToolType: model.ToolTypeCalculation, InputSchema: objectSchema([]string{"a", "b"}, "number", "number"), OutputSchema: objectSchema([]string{"relation"}, "string"), ImplementationRef: "builtin:compare"},
	"calc_exposure_remaining": {ToolID: "calc_exposure_remaining", ToolType: model.ToolTypeCalculation, InputSchema: objectSchema([]string{"limit", "current"}, "number", "number"), OutputSchema: objectSchema([]string{"remaining"}, "number"), ImplementationRef: "builtin:calc_exposure_remaining"},
	"fact_lookup":             {ToolID: "fact_lookup", ToolType: model.ToolTypeLookup, InputSchema: objectSchema([]string{"fact_key"}, "string"), OutputSchema: objectSchema([]string{"value", "unit"}, "string", "string"), ImplementationRef: "builtin:fact_lookup"},
	"sql_query":               {ToolID: "sql_query", ToolType: model.ToolTypeSQLQuery, ImplementationRef: "builtin:sql_query"},
	"knowledge_retrieval":     {ToolID: "knowledge_retrieval", ToolType: model.ToolTypeKnowledgeRetrieval, InputSchema: knowledgeRetrievalInputSchema(), OutputSchema: knowledgeRetrievalOutputSchema(), ImplementationRef: "builtin:knowledge_retrieval"},
}

func objectSchema(required []string, propertyTypes ...string) string {
	properties := make(map[string]map[string]string, len(required))
	for index, name := range required {
		propertyType := "string"
		if index < len(propertyTypes) {
			propertyType = propertyTypes[index]
		}
		properties[name] = map[string]string{"type": propertyType}
	}
	encoded, _ := json.Marshal(map[string]interface{}{
		"type":       "object",
		"required":   required,
		"properties": properties,
	})
	return string(encoded)
}

func sumInputSchema() string {
	encoded, _ := json.Marshal(map[string]interface{}{
		"type":     "object",
		"required": []string{"items"},
		"properties": map[string]interface{}{
			"items": map[string]interface{}{
				"type":  "array",
				"items": map[string]string{"type": "number"},
			},
		},
	})
	return string(encoded)
}

func knowledgeRetrievalInputSchema() string {
	encoded, _ := json.Marshal(map[string]interface{}{
		"type":     "object",
		"required": []string{"strategy_id", "question"},
		"properties": map[string]interface{}{
			"strategy_id": map[string]string{"type": "string"},
			"question":    map[string]string{"type": "string"},
		},
	})
	return string(encoded)
}

func knowledgeRetrievalOutputSchema() string {
	encoded, _ := json.Marshal(map[string]interface{}{
		"type":     "object",
		"required": []string{"strategy_id", "used_strategy_type", "fallback_used", "total", "chunks"},
		"properties": map[string]interface{}{
			"strategy_id":        map[string]string{"type": "string"},
			"used_strategy_type": map[string]string{"type": "string"},
			"fallback_used":      map[string]string{"type": "boolean"},
			"total":              map[string]string{"type": "integer"},
			"chunks": map[string]interface{}{
				"type":  "array",
				"items": map[string]string{"type": "string"},
			},
		},
	})
	return string(encoded)
}

func toolRegistryJSON(raw string, field string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", httperr.BadRequest(40120, field+" must be valid JSON")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		return "", httperr.BadRequest(40120, field+" must be valid JSON")
	}
	var document interface{}
	if err := json.Unmarshal(compact.Bytes(), &document); err != nil {
		return "", httperr.BadRequest(40120, field+" must be valid JSON")
	}
	object, ok := document.(map[string]interface{})
	if !ok {
		return "", httperr.BadRequest(40121, field+" must be a JSON object")
	}
	if field != "authorization_scope" {
		if err := validateJSONSchema(object, field); err != nil {
			return "", err
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func validateJSONSchema(document map[string]interface{}, field string) error {
	schemaType, _ := document["type"].(string)
	if schemaType != "object" {
		return httperr.BadRequest(40122, field+".type must be object")
	}
	properties, ok := document["properties"].(map[string]interface{})
	if !ok || len(properties) == 0 {
		return httperr.BadRequest(40123, field+".properties must be a non-empty object")
	}
	names := make(map[string]struct{}, len(properties))
	for name, rawProperty := range properties {
		property, ok := rawProperty.(map[string]interface{})
		if !ok {
			return httperr.BadRequest(40123, field+".properties."+name+" must be an object")
		}
		propertyType, _ := property["type"].(string)
		switch propertyType {
		case "string", "number", "integer", "boolean":
		case "array":
			items, ok := property["items"].(map[string]interface{})
			if !ok {
				return httperr.BadRequest(40123, field+".properties."+name+".items is required")
			}
			itemType, _ := items["type"].(string)
			if itemType == "" {
				return httperr.BadRequest(40123, field+".properties."+name+".items.type is required")
			}
		case "object":
			if _, ok := property["properties"].(map[string]interface{}); !ok {
				return httperr.BadRequest(40123, field+".properties."+name+".properties is required")
			}
		default:
			return httperr.BadRequest(40124, field+".properties."+name+".type is unsupported")
		}
		names[name] = struct{}{}
	}
	rawRequired, ok := document["required"].([]interface{})
	if !ok || len(rawRequired) == 0 {
		return httperr.BadRequest(40125, field+".required must be a non-empty array")
	}
	seen := make(map[string]struct{}, len(rawRequired))
	for _, rawName := range rawRequired {
		name, ok := rawName.(string)
		if !ok || name == "" {
			return httperr.BadRequest(40125, field+".required must contain property names")
		}
		if _, exists := names[name]; !exists {
			return httperr.BadRequest(40125, field+".required references unknown property "+name)
		}
		if _, exists := seen[name]; exists {
			return httperr.BadRequest(40125, field+".required contains duplicate property "+name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateBuiltinToolContract(tool *model.ToolRegistry) error {
	spec, ok := builtinToolSpecs[tool.ToolID]
	if !ok {
		return httperr.BadRequest(40126, "tool_id is not in the deterministic builtin tool allowlist")
	}
	if tool.ToolType != spec.ToolType {
		return httperr.BadRequest(40127, "tool_type does not match the builtin tool contract")
	}
	if tool.ToolType == model.ToolTypeSQLQuery {
		if _, err := parseQueryTemplateImplementationRef(tool.ImplementationRef); err != nil {
			return err
		}
		return nil
	}
	if tool.ImplementationRef != spec.ImplementationRef {
		return httperr.BadRequest(40128, "implementation_ref must be "+spec.ImplementationRef)
	}
	if !jsonEqual(tool.InputSchema, spec.InputSchema) {
		return httperr.BadRequest(40129, "input_schema does not match the builtin tool contract")
	}
	if !jsonEqual(tool.OutputSchema, spec.OutputSchema) {
		return httperr.BadRequest(40130, "output_schema does not match the builtin tool contract")
	}
	return nil
}

func parseQueryTemplateImplementationRef(raw string) (string, error) {
	reference := strings.TrimSpace(raw)
	if !strings.HasPrefix(reference, queryTemplateRefPrefix) {
		return "", httperr.BadRequest(40128, "implementation_ref must be "+queryTemplateRefPrefix+"<query_template_id>")
	}
	templateID := strings.TrimPrefix(reference, queryTemplateRefPrefix)
	if templateID == "" || strings.ContainsAny(templateID, " \t\r\n") {
		return "", httperr.BadRequest(40128, "implementation_ref must reference a query template")
	}
	return templateID, nil
}

func queryTemplateToolOutputSchema(resultSchema string) (string, error) {
	var rowSchema map[string]interface{}
	if err := json.Unmarshal([]byte(resultSchema), &rowSchema); err != nil || rowSchema == nil {
		return "", httperr.BadRequest(40180, "query template result_schema must be a JSON object")
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"type":     "object",
		"required": []string{"rows", "row_count", "truncated"},
		"properties": map[string]interface{}{
			"rows":      map[string]interface{}{"type": "array", "items": rowSchema},
			"row_count": map[string]string{"type": "integer"},
			"truncated": map[string]string{"type": "boolean"},
		},
	})
	if err != nil {
		return "", httperr.Internal("build sql query output schema")
	}
	return string(encoded), nil
}

func ensureSQLQueryTemplateContract(
	ctx context.Context, store repository.Store, tenantID string, tool *model.ToolRegistry,
) error {
	if tool.ToolType != model.ToolTypeSQLQuery {
		return nil
	}
	templateID, err := parseQueryTemplateImplementationRef(tool.ImplementationRef)
	if err != nil {
		return err
	}
	template, err := store.GetQueryTemplate(ctx, tenantID, templateID)
	if err != nil {
		return err
	}
	if template == nil || !template.Active {
		return httperr.BadRequest(40192, "implementation_ref must reference an active query template")
	}
	if !jsonEqual(tool.InputSchema, template.ParameterSchema) {
		return httperr.BadRequest(40129, "input_schema does not match the query template")
	}
	expectedOutput, err := queryTemplateToolOutputSchema(template.ResultSchema)
	if err != nil {
		return err
	}
	if !jsonEqual(tool.OutputSchema, expectedOutput) {
		return httperr.BadRequest(40130, "output_schema does not match the query template")
	}
	return nil
}

func jsonEqual(left, right string) bool {
	var leftValue, rightValue interface{}
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	leftBytes, leftErr := json.Marshal(leftValue)
	rightBytes, rightErr := json.Marshal(rightValue)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return bytes.Equal(leftBytes, rightBytes)
}

func notFoundToolRegistry() error { return httperr.NotFound("tool registry entry not found") }

func (s *Service) ListToolRegistries(
	ctx context.Context, tenantID string, filter repository.ToolRegistryFilter, page, pageSize int,
) ([]model.ToolRegistry, int64, error) {
	filter.ToolType = strings.TrimSpace(filter.ToolType)
	return s.Store.ListToolRegistries(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetToolRegistry(ctx context.Context, tenantID, toolRegistryID string) (*model.ToolRegistry, error) {
	tool, err := s.Store.GetToolRegistry(ctx, tenantID, toolRegistryID)
	if err != nil {
		return nil, err
	}
	if tool == nil {
		return nil, notFoundToolRegistry()
	}
	return tool, nil
}

func (s *Service) CreateToolRegistry(
	ctx context.Context, tenantID, userID string, input ToolRegistryInput,
) (*model.ToolRegistry, error) {
	now := time.Now().UTC()
	tool, err := buildToolRegistry(tenantID, userID, input, now)
	if err != nil {
		return nil, err
	}
	tool.Active = input.Active == nil || *input.Active
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if tool.Active {
			if err := ensureNoActiveToolRegistry(ctx, tx, tenantID, tool.ToolID, ""); err != nil {
				return err
			}
		}
		if err := ensureSQLQueryTemplateContract(ctx, tx, tenantID, tool); err != nil {
			return err
		}
		if err := tx.CreateToolRegistry(ctx, tool); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForToolRegistry(tool, "tool_registry.created", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return tool, nil
}

func (s *Service) UpdateToolRegistry(
	ctx context.Context, tenantID, userID, toolRegistryID string, input ToolRegistryInput,
) (*model.ToolRegistry, error) {
	existing, err := s.GetToolRegistry(ctx, tenantID, toolRegistryID)
	if err != nil {
		return nil, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	now := time.Now().UTC()
	updated, err := buildToolRegistry(tenantID, existing.CreatedBy, input, existing.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated.ID = existing.ID
	updated.Active = active
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = now
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if updated.Active {
			if err := ensureNoActiveToolRegistry(ctx, tx, tenantID, updated.ToolID, updated.ID); err != nil {
				return err
			}
		}
		if err := ensureSQLQueryTemplateContract(ctx, tx, tenantID, updated); err != nil {
			return err
		}
		if err := tx.UpdateToolRegistry(ctx, updated); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForToolRegistry(updated, "tool_registry.updated", userID, now))
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteToolRegistry(ctx context.Context, tenantID, userID, toolRegistryID string) error {
	tool, err := s.GetToolRegistry(ctx, tenantID, toolRegistryID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteToolRegistry(ctx, tenantID, tool.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForToolRegistry(tool, "tool_registry.deleted", userID, now))
	})
}

func buildToolRegistry(tenantID, userID string, input ToolRegistryInput, now time.Time) (*model.ToolRegistry, error) {
	toolID := strings.TrimSpace(input.ToolID)
	version := strings.TrimSpace(input.Version)
	name := strings.TrimSpace(input.Name)
	if !validToolID.MatchString(toolID) {
		return nil, httperr.BadRequest(40120, "tool_id must match "+toolIDPattern)
	}
	if version == "" || len(version) > 64 {
		return nil, httperr.BadRequest(40120, "version must contain 1 to 64 characters")
	}
	if name == "" || len(name) > 128 {
		return nil, httperr.BadRequest(40120, "name must contain 1 to 128 characters")
	}
	inputSchema, err := toolRegistryJSON(input.InputSchema, "input_schema")
	if err != nil {
		return nil, err
	}
	outputSchema, err := toolRegistryJSON(input.OutputSchema, "output_schema")
	if err != nil {
		return nil, err
	}
	authorizationScope := strings.TrimSpace(input.AuthorizationScope)
	if authorizationScope == "" {
		return nil, httperr.BadRequest(40194, "authorization_scope must contain roles")
	}
	authorizationScope, err = validateAuthorizationScope(authorizationScope, "authorization_scope")
	if err != nil {
		return nil, err
	}
	implementationRef := strings.TrimSpace(input.ImplementationRef)
	toolType := strings.TrimSpace(input.ToolType)
	tool := &model.ToolRegistry{
		ID: id.New(), TenantID: tenantID, ToolID: toolID, Version: version, ToolType: toolType,
		Name: name, InputSchema: inputSchema, OutputSchema: outputSchema,
		AuthorizationScope: authorizationScope, ImplementationRef: implementationRef,
		Active: true, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := validateBuiltinToolContract(tool); err != nil {
		return nil, err
	}
	return tool, nil
}

func ensureNoActiveToolRegistry(ctx context.Context, store repository.Store, tenantID, toolID, excludeID string) error {
	count, err := store.CountActiveToolRegistries(ctx, tenantID, toolID, excludeID)
	if err != nil {
		return err
	}
	if count > 0 {
		return httperr.New(409, 40996, "one active version per tool is allowed")
	}
	return nil
}

func auditForToolRegistry(tool *model.ToolRegistry, action, userID string, now time.Time) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: tool.TenantID, ActorTenantID: tool.TenantID, TargetTenantID: tool.TenantID,
		UserID: userID, Action: action, Resource: "tool-registry", ResourceID: tool.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"tool_id": tool.ToolID, "tool_version": tool.Version, "tool_type": tool.ToolType,
			"active": tool.Active, "implementation_ref": tool.ImplementationRef,
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

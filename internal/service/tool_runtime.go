package service

import (
	"context"
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

type ToolExecutionInput struct {
	SourceRoutingRuleID string              `json:"source_routing_rule_id"`
	AnswerRunID         string              `json:"answer_run_id"`
	Input               json.RawMessage     `json:"input" binding:"required"`
	AuthorizationTrace  *AuthorizationTrace `json:"-"`
}

type ToolExecutionResult struct {
	Tool   *model.ToolRegistry `json:"tool"`
	Output json.RawMessage     `json:"output"`
	Trace  *AuthorizationTrace `json:"authorization_trace,omitempty"`
}

type runtimeToolSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]runtimeToolSchema `json:"properties,omitempty"`
	Required   []string                     `json:"required,omitempty"`
	Items      *runtimeToolSchema           `json:"items,omitempty"`
}

func (s *Service) ExecuteToolRegistry(
	ctx context.Context, tenantID, userID, toolRegistryID string, input ToolExecutionInput,
) (*ToolExecutionResult, error) {
	tool, err := s.GetToolRegistry(ctx, tenantID, toolRegistryID)
	if err != nil {
		return nil, err
	}
	if !tool.Active {
		return nil, notFoundToolRegistry()
	}
	var decodedInput map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(input.Input)))
	decoder.UseNumber()
	if err := decoder.Decode(&decodedInput); err != nil {
		return nil, httperr.BadRequest(40120, "tool input must be a JSON object")
	}
	schema, err := decodeRuntimeToolSchema(tool.InputSchema)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeToolValue(decodedInput, schema, "input"); err != nil {
		return nil, err
	}
	var output map[string]interface{}
	var sqlTrace *AuthorizationTrace
	switch tool.ToolType {
	case model.ToolTypeCalculation:
		output, err = executeCalculationTool(tool, decodedInput)
	case model.ToolTypeLookup:
		output, err = s.executeFactLookupTool(ctx, tenantID, input.AnswerRunID, decodedInput)
	case model.ToolTypeSQLQuery:
		output, sqlTrace, err = s.executeSQLQueryTool(ctx, tenantID, userID, input, decodedInput, tool)
	case model.ToolTypeKnowledgeRetrieval:
		output, err = s.executeKnowledgeRetrievalTool(ctx, tenantID, userID, decodedInput)
	default:
		return nil, httperr.BadRequest(40131, "unsupported tool type")
	}
	if err != nil {
		return nil, err
	}
	outputSchema, err := decodeRuntimeToolSchema(tool.OutputSchema)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeToolValue(output, outputSchema, "output"); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	auditTrace := sqlTrace
	if auditTrace == nil {
		auditTrace = input.AuthorizationTrace
	}
	now := time.Now().UTC()
	if err := s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: userID, Action: "tool_registry.executed", Resource: "tool-registry", ResourceID: tool.ID,
		DetailJSON: encodeAuditDetail(sqlExecutionAuditDetails(tool, output, sqlTrace)),
		At:         now,
		Result:     "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPolicyVersion: authorizationPolicyVersion(auditTrace),
	}); err != nil {
		return nil, err
	}
	return &ToolExecutionResult{Tool: tool, Output: encoded, Trace: sqlTrace}, nil
}

func decodeRuntimeToolSchema(raw string) (*runtimeToolSchema, error) {
	var schema runtimeToolSchema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, httperr.BadRequest(40120, "tool schema must be valid JSON")
	}
	if schema.Type != "object" || len(schema.Properties) == 0 {
		return nil, httperr.BadRequest(40121, "tool schema must define an object with properties")
	}
	return &schema, nil
}

func validateRuntimeToolValue(value interface{}, schema *runtimeToolSchema, path string) error {
	if schema == nil {
		return nil
	}
	switch schema.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return httperr.BadRequest(40131, path+" must be a string")
		}
	case "number", "integer":
		number, ok := value.(json.Number)
		if !ok {
			return httperr.BadRequest(40131, path+" must be a number")
		}
		rational, ok := new(big.Rat).SetString(number.String())
		if !ok {
			return httperr.BadRequest(40131, path+" must be a valid number")
		}
		if schema.Type == "integer" && !rational.IsInt() {
			return httperr.BadRequest(40131, path+" must be an integer")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return httperr.BadRequest(40131, path+" must be a boolean")
		}
	case "array":
		items, ok := value.([]interface{})
		if !ok {
			return httperr.BadRequest(40131, path+" must be an array")
		}
		for index, item := range items {
			if err := validateRuntimeToolValue(item, schema.Items, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case "object":
		object, ok := value.(map[string]interface{})
		if !ok {
			return httperr.BadRequest(40131, path+" must be an object")
		}
		for _, name := range schema.Required {
			if _, exists := object[name]; !exists {
				return httperr.BadRequest(40131, path+"."+name+" is required")
			}
		}
		for name, propertyValue := range object {
			property, exists := schema.Properties[name]
			if !exists {
				return httperr.BadRequest(40131, path+"."+name+" is not allowed")
			}
			if err := validateRuntimeToolValue(propertyValue, &property, path+"."+name); err != nil {
				return err
			}
		}
	default:
		return httperr.BadRequest(40131, path+" has an unsupported schema type")
	}
	return nil
}

func executeCalculationTool(tool *model.ToolRegistry, input map[string]interface{}) (map[string]interface{}, error) {
	switch tool.ToolID {
	case "add", "subtract", "percentage_diff", "compare", "calc_exposure_remaining":
		var leftKey, rightKey string
		if tool.ToolID == "calc_exposure_remaining" {
			leftKey, rightKey = "limit", "current"
		} else {
			leftKey, rightKey = "a", "b"
		}
		left, err := runtimeRational(input[leftKey], "input."+leftKey)
		if err != nil {
			return nil, err
		}
		right, err := runtimeRational(input[rightKey], "input."+rightKey)
		if err != nil {
			return nil, err
		}
		switch tool.ToolID {
		case "add":
			left.Add(left, right)
			return map[string]interface{}{"result": runtimeNumber(left)}, nil
		case "subtract":
			left.Sub(left, right)
			return map[string]interface{}{"result": runtimeNumber(left)}, nil
		case "percentage_diff":
			if right.Sign() == 0 {
				return nil, httperr.BadRequest(40132, "percentage_diff denominator must not be zero")
			}
			left.Sub(left, right)
			left.Quo(left, right)
			left.Mul(left, big.NewRat(100, 1))
			return map[string]interface{}{"result": runtimeNumber(left)}, nil
		case "compare":
			relation := "equal"
			switch left.Cmp(right) {
			case -1:
				relation = "less_than"
			case 1:
				relation = "greater_than"
			}
			return map[string]interface{}{"relation": relation}, nil
		case "calc_exposure_remaining":
			left.Sub(left, right)
			return map[string]interface{}{"remaining": runtimeNumber(left)}, nil
		}
	case "sum":
		items, ok := input["items"].([]interface{})
		if !ok || len(items) == 0 {
			return nil, httperr.BadRequest(40132, "sum items must contain at least one number")
		}
		total := new(big.Rat)
		for index, item := range items {
			value, err := runtimeRational(item, fmt.Sprintf("input.items[%d]", index))
			if err != nil {
				return nil, err
			}
			total.Add(total, value)
		}
		return map[string]interface{}{"result": runtimeNumber(total)}, nil
	}
	return nil, httperr.BadRequest(40133, "tool is not executable")
}

func runtimeRational(value interface{}, path string) (*big.Rat, error) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, httperr.BadRequest(40131, path+" must be a number")
	}
	rational, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return nil, httperr.BadRequest(40131, path+" must be a valid number")
	}
	return rational, nil
}

func runtimeNumber(value *big.Rat) json.Number {
	text := value.FloatString(12)
	text = strings.TrimRight(text, "0")
	text = strings.TrimSuffix(text, ".")
	if text == "-0" {
		text = "0"
	}
	return json.Number(text)
}

func (s *Service) executeFactLookupTool(
	ctx context.Context, tenantID, answerRunID string, input map[string]interface{},
) (map[string]interface{}, error) {
	factKey, ok := input["fact_key"].(string)
	if !ok || strings.TrimSpace(factKey) == "" {
		return nil, httperr.BadRequest(40131, "input.fact_key must be a string")
	}
	answerRunID = strings.TrimSpace(answerRunID)
	if answerRunID == "" {
		return nil, httperr.BadRequest(40134, "answer_run_id is required for fact_lookup")
	}
	if _, err := s.Store.GetAnswerRun(ctx, tenantID, answerRunID); err != nil {
		return nil, err
	}
	facts, err := s.Store.ListFactRegistry(ctx, tenantID, answerRunID)
	if err != nil {
		return nil, err
	}
	candidates := make([]model.FactRegistry, 0, len(facts))
	conflicted := false
	for index := range facts {
		if facts[index].FactKey != factKey {
			continue
		}
		if facts[index].ConflictStatus != model.FactConflictNone {
			conflicted = true
			continue
		}
		candidates = append(candidates, facts[index])
	}
	if conflicted {
		return nil, httperr.New(409, 40996, "fact lookup is ambiguous or conflicted")
	}
	if len(candidates) == 0 {
		return nil, httperr.NotFound("fact is not found or is in conflict")
	}
	signatures := make(map[string]model.FactRegistry, len(candidates))
	for index := range candidates {
		signature := strings.ToLower(candidates[index].Unit) + "|" + strings.ToLower(candidates[index].TimeRange) + "|" +
			normalizeFactValue(candidates[index].Value)
		if _, exists := signatures[signature]; !exists {
			signatures[signature] = candidates[index]
		}
	}
	if len(signatures) != 1 {
		return nil, httperr.New(409, 40996, "fact lookup is ambiguous or conflicted")
	}
	for _, fact := range signatures {
		return map[string]interface{}{"value": fact.Value, "unit": fact.Unit}, nil
	}
	return nil, httperr.New(409, 40996, "fact lookup is ambiguous or conflicted")
}

func (s *Service) executeKnowledgeRetrievalTool(
	ctx context.Context, tenantID, userID string, input map[string]interface{},
) (map[string]interface{}, error) {
	strategyID, _ := input["strategy_id"].(string)
	strategyID = strings.TrimSpace(strategyID)
	question, _ := input["question"].(string)
	question = strings.TrimSpace(question)
	if strategyID == "" || question == "" {
		return nil, httperr.BadRequest(40131, "input.strategy_id and input.question must be non-empty strings")
	}
	execution, err := s.ExecuteKnowledgeStrategy(
		ctx, tenantID, userID, strategyID, KnowledgeStrategyRetrieveInput{Question: question},
	)
	if err != nil {
		return nil, err
	}
	chunks := make([]interface{}, 0, len(execution.Chunks))
	for _, chunk := range execution.Chunks {
		content, ok := chunk["content"].(string)
		if !ok {
			return nil, httperr.BadRequest(40131, "knowledge retrieval returned an invalid chunk")
		}
		chunks = append(chunks, content)
	}
	return map[string]interface{}{
		"strategy_id":        execution.StrategyID,
		"used_strategy_type": execution.UsedStrategyType,
		"fallback_used":      execution.FallbackUsed,
		"total":              json.Number(strconv.FormatInt(execution.Total, 10)),
		"chunks":             chunks,
	}, nil
}

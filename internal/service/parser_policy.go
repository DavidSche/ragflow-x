package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

var (
	validChunkMethods = map[string]bool{
		"general": true, "table": true, "book": true,
		"presentation": true, "picture": true, "paper": true,
	}
	referenceMetricNames = []string{
		"table_recall", "reading_order_accuracy",
		"header_footer_precision", "ocr_character_accuracy",
	}
)

type QualityMetric struct {
	Weight  float64 `json:"weight"`
	Enabled bool    `json:"enabled"`
}

type QualityProfileInput struct {
	Name              string                   `json:"name" binding:"required"`
	Metrics           map[string]QualityMetric `json:"metrics"`
	Thresholds        map[string]float64       `json:"thresholds"`
	EvaluationMode    string                   `json:"evaluation_mode"`
	ReferenceWeight   float64                  `json:"reference_weight"`
	ReferenceHardFail bool                     `json:"reference_hard_fail"`
	Active            *bool                    `json:"active"`
}

type QualityProfilePatchInput struct {
	Name              *string                   `json:"name"`
	Metrics           *map[string]QualityMetric `json:"metrics"`
	Thresholds        *map[string]float64       `json:"thresholds"`
	EvaluationMode    *string                   `json:"evaluation_mode"`
	ReferenceWeight   *float64                  `json:"reference_weight"`
	ReferenceHardFail *bool                     `json:"reference_hard_fail"`
	Active            *bool                     `json:"active"`
}

type ParserPolicyInput struct {
	ProjectID        string          `json:"project_id"`
	DatasetID        string          `json:"dataset_id"`
	DocumentType     string          `json:"document_type"`
	ParseMode        string          `json:"parse_mode" binding:"required"`
	ChunkMethod      string          `json:"chunk_method"`
	PipelineID       string          `json:"pipeline_id"`
	ParserConfig     json.RawMessage `json:"parser_config"`
	FallbackPolicy   json.RawMessage `json:"fallback_policy"`
	QualityProfileID string          `json:"quality_profile_id" binding:"required"`
	Active           *bool           `json:"active"`
}

type ParserPolicyPatchInput struct {
	ProjectID        *string          `json:"project_id"`
	DatasetID        *string          `json:"dataset_id"`
	DocumentType     *string          `json:"document_type"`
	ParseMode        *string          `json:"parse_mode"`
	ChunkMethod      *string          `json:"chunk_method"`
	PipelineID       *string          `json:"pipeline_id"`
	ParserConfig     *json.RawMessage `json:"parser_config"`
	FallbackPolicy   *json.RawMessage `json:"fallback_policy"`
	QualityProfileID *string          `json:"quality_profile_id"`
	Active           *bool            `json:"active"`
}

type ParserFallbackPolicy struct {
	FallbackPolicyID string `json:"fallback_policy_id"`
	MaxAttempts      *int   `json:"max_attempts"`
}

func (s *Service) ListQualityProfiles(ctx context.Context, tenantID string, active *bool, page, pageSize int) ([]model.QualityProfile, int64, error) {
	return s.Store.ListQualityProfiles(ctx, tenantID, active, page, pageSize)
}

func (s *Service) GetQualityProfile(ctx context.Context, tenantID, profileID string) (*model.QualityProfile, error) {
	return s.getQualityProfile(ctx, tenantID, profileID)
}

func (s *Service) CreateQualityProfile(ctx context.Context, tenantID, userID string, input QualityProfileInput) (*model.QualityProfile, error) {
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	if input.EvaluationMode == "" {
		input.EvaluationMode = model.QualityEvaluationHeuristicOnly
	}
	profile, _, _, err := s.validateQualityProfileInput(ctx, tenantID, input.Name, input.Metrics, input.Thresholds, input.EvaluationMode, input.ReferenceWeight, input.ReferenceHardFail, active, "")
	if err != nil {
		return nil, err
	}
	profile.ID = id.New()
	profile.TenantID = tenantID
	profile.CreatedAt = time.Now().UTC()
	profile.UpdatedAt = profile.CreatedAt
	if err := s.Store.CreateQualityProfile(ctx, profile); err != nil {
		return nil, err
	}
	_ = userID
	return profile, nil
}

func (s *Service) UpdateQualityProfile(ctx context.Context, tenantID, userID, profileID string, input QualityProfilePatchInput) (*model.QualityProfile, error) {
	profile, err := s.getQualityProfile(ctx, tenantID, profileID)
	if err != nil || profile == nil {
		return nil, notFoundQualityProfile()
	}
	currentMetrics, currentThresholds, err := decodeQualityProfilePayload(profile.Metrics, profile.Thresholds)
	if err != nil {
		return nil, invalidQualityProfile("stored quality profile payload is invalid")
	}
	name := profile.Name
	if input.Name != nil {
		name = *input.Name
	}
	if input.Metrics != nil {
		currentMetrics = *input.Metrics
	}
	if input.Thresholds != nil {
		currentThresholds = *input.Thresholds
	}
	evaluationMode := profile.EvaluationMode
	if input.EvaluationMode != nil {
		evaluationMode = *input.EvaluationMode
	}
	referenceWeight := profile.ReferenceWeight
	if input.ReferenceWeight != nil {
		referenceWeight = *input.ReferenceWeight
	}
	referenceHardFail := profile.ReferenceHardFail
	if input.ReferenceHardFail != nil {
		referenceHardFail = *input.ReferenceHardFail
	}
	active := profile.Active
	if input.Active != nil {
		active = *input.Active
	}
	validated, _, _, err := s.validateQualityProfileInput(ctx, tenantID, name, currentMetrics, currentThresholds, evaluationMode, referenceWeight, referenceHardFail, active, profileID)
	if err != nil {
		return nil, err
	}
	profile.Name = validated.Name
	profile.Metrics = validated.Metrics
	profile.Thresholds = validated.Thresholds
	profile.EvaluationMode = validated.EvaluationMode
	profile.ReferenceWeight = validated.ReferenceWeight
	profile.ReferenceHardFail = validated.ReferenceHardFail
	if input.Active != nil {
		count, countErr := s.Store.CountActiveParserPoliciesByQualityProfile(ctx, tenantID, profile.ID)
		if countErr != nil {
			return nil, countErr
		}
		if count > 0 {
			return nil, httperr.New(409, 40981, "active parser policies still reference this quality profile")
		}
	}
	profile.Active = active
	profile.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateQualityProfile(ctx, profile); err != nil {
		return nil, err
	}
	_ = userID
	return profile, nil
}

func (s *Service) DeleteQualityProfile(ctx context.Context, tenantID, profileID string) error {
	profile, err := s.getQualityProfile(ctx, tenantID, profileID)
	if err != nil || profile == nil {
		return notFoundQualityProfile()
	}
	count, err := s.Store.CountActiveParserPoliciesByQualityProfile(ctx, tenantID, profileID)
	if err != nil {
		return err
	}
	if count > 0 {
		return httperr.New(409, 40981, "active parser policies still reference this quality profile")
	}
	return s.Store.DeleteQualityProfile(ctx, tenantID, profileID)
}

func (s *Service) ListParserPolicies(ctx context.Context, tenantID string, filter repository.ParserPolicyFilter, page, pageSize int) ([]model.ParserPolicy, int64, error) {
	return s.Store.ListParserPolicies(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetParserPolicy(ctx context.Context, tenantID, policyID string) (*model.ParserPolicy, error) {
	return s.getParserPolicy(ctx, tenantID, policyID)
}

func (s *Service) CreateParserPolicy(ctx context.Context, tenantID string, input ParserPolicyInput) (*model.ParserPolicy, error) {
	policy := &model.ParserPolicy{ProjectID: strings.TrimSpace(input.ProjectID), DatasetID: strings.TrimSpace(input.DatasetID)}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	if err := s.applyParserPolicyInput(ctx, tenantID, policy, input.DocumentType, input.ParseMode, input.ChunkMethod, input.PipelineID, input.ParserConfig, input.FallbackPolicy, input.QualityProfileID, active); err != nil {
		return nil, err
	}
	policy.ID = id.New()
	policy.TenantID = tenantID
	policy.CreatedAt = time.Now().UTC()
	policy.UpdatedAt = policy.CreatedAt
	if err := s.ensureNoActivePolicyConflict(ctx, tenantID, policy, ""); err != nil {
		return nil, err
	}
	if err := s.Store.CreateParserPolicy(ctx, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) UpdateParserPolicy(ctx context.Context, tenantID, policyID string, input ParserPolicyPatchInput) (*model.ParserPolicy, error) {
	policy, err := s.getParserPolicy(ctx, tenantID, policyID)
	if err != nil || policy == nil {
		return nil, notFoundParserPolicy()
	}
	if input.ProjectID != nil {
		policy.ProjectID = strings.TrimSpace(*input.ProjectID)
	}
	if input.DatasetID != nil {
		policy.DatasetID = strings.TrimSpace(*input.DatasetID)
	}
	if input.DocumentType != nil {
		policy.DocumentType = strings.TrimSpace(*input.DocumentType)
	}
	if input.ParseMode != nil {
		policy.ParseMode = *input.ParseMode
	}
	if input.ChunkMethod != nil {
		policy.ChunkMethod = *input.ChunkMethod
	}
	if input.PipelineID != nil {
		policy.PipelineID = strings.TrimSpace(*input.PipelineID)
	}
	if input.ParserConfig != nil {
		encoded, encodeErr := parserPolicyJSON(json.RawMessage(*input.ParserConfig))
		if encodeErr != nil {
			return nil, invalidParserPolicy("parser_config must be a JSON object")
		}
		policy.ParserConfig = encoded
	}
	if input.FallbackPolicy != nil {
		if err := s.applyFallbackPolicy(ctx, tenantID, policy, []byte(*input.FallbackPolicy)); err != nil {
			return nil, err
		}
	}
	if input.QualityProfileID != nil {
		policy.QualityProfileID = *input.QualityProfileID
	}
	if input.Active != nil {
		policy.Active = *input.Active
	}
	if err := s.validateParserPolicyInvariants(ctx, tenantID, policy); err != nil {
		return nil, err
	}
	if err := s.ensureNoActivePolicyConflict(ctx, tenantID, policy, policy.ID); err != nil {
		return nil, err
	}
	policy.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateParserPolicy(ctx, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) DeleteParserPolicy(ctx context.Context, tenantID, policyID string) error {
	policy, err := s.getParserPolicy(ctx, tenantID, policyID)
	if err != nil || policy == nil {
		return notFoundParserPolicy()
	}
	count, err := s.Store.CountActiveParserPoliciesByFallback(ctx, tenantID, policyID)
	if err != nil {
		return err
	}
	if count > 0 {
		return httperr.New(409, 40981, "active parser policies still reference this fallback policy")
	}
	if err := s.Store.DeleteParserPolicy(ctx, tenantID, policyID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundParserPolicy()
		}
		return err
	}
	return nil
}

func (s *Service) applyParserPolicyInput(ctx context.Context, tenantID string, policy *model.ParserPolicy, documentType, parseMode, chunkMethod, pipelineID string, parserConfig, fallbackPolicy json.RawMessage, qualityProfileID string, active bool) error {
	policy.DocumentType = strings.TrimSpace(documentType)
	policy.ParseMode = parseMode
	policy.ChunkMethod = chunkMethod
	policy.PipelineID = strings.TrimSpace(pipelineID)
	policy.QualityProfileID = qualityProfileID
	policy.Active = active
	encoded, err := parserPolicyJSON(parserConfig)
	if err != nil {
		return invalidParserPolicy("parser_config must be a JSON object")
	}
	policy.ParserConfig = encoded
	if err := s.applyFallbackPolicy(ctx, tenantID, policy, fallbackPolicy); err != nil {
		return err
	}
	if err := s.validateParserPolicyInvariants(ctx, tenantID, policy); err != nil {
		return err
	}
	return nil
}

func (s *Service) applyFallbackPolicy(ctx context.Context, tenantID string, policy *model.ParserPolicy, raw json.RawMessage) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		policy.FallbackPolicy = "{}"
		return nil
	}
	var fallback ParserFallbackPolicy
	if err := json.Unmarshal(raw, &fallback); err != nil {
		return invalidParserPolicy("fallback_policy payload is invalid")
	}
	maxAttempts := 2
	if fallback.MaxAttempts != nil {
		maxAttempts = *fallback.MaxAttempts
	}
	if maxAttempts <= 0 {
		return invalidParserPolicy("fallback max_attempts must be greater than zero")
	}
	fallback.MaxAttempts = &maxAttempts
	fallback.FallbackPolicyID = strings.TrimSpace(fallback.FallbackPolicyID)
	if fallback.FallbackPolicyID != "" {
		if fallback.FallbackPolicyID == policy.ID {
			return invalidParserPolicy("fallback policy chain contains a cycle")
		}
		visited := map[string]bool{}
		currentID := fallback.FallbackPolicyID
		for currentID != "" {
			if visited[currentID] {
				return invalidParserPolicy("fallback policy chain contains a cycle")
			}
			visited[currentID] = true
			next, err := s.getParserPolicy(ctx, tenantID, currentID)
			if err != nil {
				return err
			}
			if next == nil || !next.Active {
				return invalidParserPolicy("fallback policy must reference an active policy in the same tenant")
			}
			var nextFallback ParserFallbackPolicy
			if next.FallbackPolicy != "" && next.FallbackPolicy != "{}" {
				if err := json.Unmarshal([]byte(next.FallbackPolicy), &nextFallback); err != nil {
					return invalidParserPolicy("fallback policy chain is invalid")
				}
			}
			currentID = strings.TrimSpace(nextFallback.FallbackPolicyID)
			if currentID == policy.ID {
				return invalidParserPolicy("fallback policy chain contains a cycle")
			}
		}
	}
	encoded, err := parserPolicyJSON(fallback)
	if err != nil {
		return invalidParserPolicy("fallback_policy payload is invalid")
	}
	policy.FallbackPolicy = encoded
	return nil
}

func (s *Service) validateParserPolicyInvariants(ctx context.Context, tenantID string, policy *model.ParserPolicy) error {
	if len(policy.DocumentType) > 64 || len(policy.PipelineID) > 64 || len(policy.QualityProfileID) > 32 {
		return invalidParserPolicy("parser policy identifier exceeds its size limit")
	}
	if policy.ProjectID != "" {
		project, err := s.Store.GetProject(ctx, tenantID, policy.ProjectID)
		if err != nil {
			return err
		}
		if project == nil {
			return invalidParserPolicy("project_id must reference a project in the same tenant")
		}
	}
	if policy.DatasetID != "" {
		dataset, err := s.Store.GetDatasetLink(ctx, tenantID, policy.DatasetID)
		if err != nil {
			return err
		}
		if dataset == nil {
			return invalidParserPolicy("dataset_id must reference a dataset in the same tenant")
		}
	}
	switch policy.ParseMode {
	case model.ParseModeBuiltin:
		if policy.ChunkMethod == "" || policy.PipelineID != "" {
			return invalidParserPolicy("builtin parse mode requires chunk_method and empty pipeline_id")
		}
		if !validChunkMethods[policy.ChunkMethod] {
			return invalidParserPolicy("unsupported chunk_method")
		}
	case model.ParseModePipeline:
		if policy.PipelineID == "" || policy.ChunkMethod != "" {
			return invalidParserPolicy("pipeline parse mode requires pipeline_id and empty chunk_method")
		}
		if s.RAGFlow == nil {
			return httperr.New(503, 50302, "ragflow pipeline validation is unavailable")
		}
		pipeline, err := s.RAGFlow.GetPipeline(ctx, policy.PipelineID)
		if err != nil {
			var providerErr *ragflow.Error
			if errors.As(err, &providerErr) && providerErr.Type == ragflow.ErrorTypeBusiness {
				return invalidParserPolicy("pipeline_id must reference a RAGFlow ingestion pipeline")
			}
			return httperr.New(503, 50302, "ragflow pipeline validation failed")
		}
		if pipeline == nil || len(pipeline.DSL) == 0 {
			return invalidParserPolicy("pipeline_id must reference a RAGFlow ingestion pipeline")
		}
	default:
		return invalidParserPolicy("parse_mode must be builtin or pipeline")
	}
	profile, err := s.getQualityProfile(ctx, tenantID, policy.QualityProfileID)
	if err != nil {
		return err
	}
	if profile == nil || !profile.Active {
		return invalidParserPolicy("quality_profile_id must reference an active profile")
	}
	return nil
}

func (s *Service) ensureNoActivePolicyConflict(ctx context.Context, tenantID string, policy *model.ParserPolicy, excludeID string) error {
	if !policy.Active {
		return nil
	}
	count, err := s.Store.CountActiveParserPolicies(ctx, tenantID, policy.ProjectID, policy.DatasetID, policy.DocumentType, excludeID)
	if err != nil {
		return err
	}
	if count > 0 {
		return httperr.New(409, 40981, "an active parser policy already matches this scope")
	}
	return nil
}

func (s *Service) validateQualityProfileInput(ctx context.Context, tenantID, name string, metrics map[string]QualityMetric, thresholds map[string]float64, evaluationMode string, referenceWeight float64, referenceHardFail bool, active bool, excludeID string) (*model.QualityProfile, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return nil, "", "", invalidQualityProfile("name is required and must be 1-128 characters")
	}
	existing, err := s.Store.GetQualityProfileByName(ctx, tenantID, name, excludeID)
	if err != nil {
		return nil, "", "", err
	}
	if existing != nil {
		return nil, "", "", httperr.New(409, 40981, "quality profile name already exists")
	}
	weightSum := 0.0
	for metricName, metric := range metrics {
		if metric.Weight < 0 || metric.Weight > 1 || math.IsNaN(metric.Weight) || math.IsInf(metric.Weight, 0) {
			return nil, "", "", invalidQualityProfile(fmt.Sprintf("weight for %s must be between 0 and 1", metricName))
		}
		if metric.Enabled {
			weightSum += metric.Weight
		}
	}
	if len(metrics) == 0 || math.Abs(weightSum-1) > 0.0001 {
		return nil, "", "", invalidQualityProfile("enabled metric weights must sum to 1.0")
	}
	for _, threshold := range thresholds {
		if threshold < 0 || threshold > 1 || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
			return nil, "", "", invalidQualityProfile("thresholds must be between 0 and 1")
		}
	}
	passThreshold := defaultHeuristicPassThreshold
	warnThreshold := defaultHeuristicWarnThreshold
	if value, ok := thresholds["heuristic_pass"]; ok {
		passThreshold = value
	}
	if value, ok := thresholds["heuristic_warn"]; ok {
		warnThreshold = value
	}
	if warnThreshold >= passThreshold {
		return nil, "", "", invalidQualityProfile("heuristic_warn threshold must be lower than heuristic_pass threshold")
	}
	switch evaluationMode {
	case model.QualityEvaluationHeuristicOnly:
		if referenceWeight != 0 {
			return nil, "", "", invalidQualityProfile("reference_weight must be zero in heuristic_only mode")
		}
	case model.QualityEvaluationHeuristicPlusReference:
		if referenceWeight <= 0 || referenceWeight > 1 {
			return nil, "", "", invalidQualityProfile("reference_weight must be greater than zero and at most one")
		}
		for _, metricName := range referenceMetricNames {
			if _, ok := thresholds[metricName]; !ok {
				return nil, "", "", invalidQualityProfile("reference threshold " + metricName + " is required")
			}
		}
		return nil, "", "", invalidQualityProfile("golden parse set metadata is unavailable")
	default:
		return nil, "", "", invalidQualityProfile("evaluation_mode must be heuristic_only or heuristic_plus_reference")
	}
	encodedMetrics, err := parserPolicyJSON(metrics)
	if err != nil {
		return nil, "", "", invalidQualityProfile("metrics payload is invalid")
	}
	encodedThresholds, err := parserPolicyJSON(thresholds)
	if err != nil {
		return nil, "", "", invalidQualityProfile("thresholds payload is invalid")
	}
	return &model.QualityProfile{
		Name: name, Metrics: encodedMetrics, Thresholds: encodedThresholds,
		EvaluationMode: evaluationMode, ReferenceWeight: referenceWeight,
		ReferenceHardFail: referenceHardFail, Active: active,
	}, encodedMetrics, encodedThresholds, nil
}

func decodeQualityProfilePayload(metricsJSON, thresholdsJSON string) (map[string]QualityMetric, map[string]float64, error) {
	metrics := map[string]QualityMetric{}
	thresholds := map[string]float64{}
	if err := json.Unmarshal([]byte(metricsJSON), &metrics); err != nil {
		return nil, nil, err
	}
	if thresholdsJSON != "" && thresholdsJSON != "{}" {
		if err := json.Unmarshal([]byte(thresholdsJSON), &thresholds); err != nil {
			return nil, nil, err
		}
	}
	return metrics, thresholds, nil
}

func (s *Service) getParserPolicy(ctx context.Context, tenantID, policyID string) (*model.ParserPolicy, error) {
	if policyID == "" {
		return nil, notFoundParserPolicy()
	}
	return s.Store.GetParserPolicy(ctx, tenantID, policyID)
}

func (s *Service) getQualityProfile(ctx context.Context, tenantID, profileID string) (*model.QualityProfile, error) {
	if profileID == "" {
		return nil, notFoundQualityProfile()
	}
	return s.Store.GetQualityProfile(ctx, tenantID, profileID)
}

func parserPolicyJSON(value interface{}) (string, error) {
	if raw, ok := value.(json.RawMessage); ok {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			return "{}", nil
		}
		var decoded interface{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", err
		}
		if _, ok := decoded.(map[string]interface{}); !ok {
			return "", errors.New("json object required")
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func invalidParserPolicy(message string) error   { return httperr.New(400, 40081, message) }
func invalidQualityProfile(message string) error { return httperr.New(400, 40082, message) }
func notFoundParserPolicy() error                { return httperr.NotFound("parser policy not found") }
func notFoundQualityProfile() error              { return httperr.NotFound("quality profile not found") }

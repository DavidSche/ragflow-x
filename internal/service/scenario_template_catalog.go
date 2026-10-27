package service

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioTemplateCatalogPrompt is the resolved prompt copy a template applies
// when instantiated. It is resolved server-side from the template payload's
// preset id, so the wizard renders exactly what will be written to the
// assistant instead of keeping a second copy in the frontend.
type ScenarioTemplateCatalogPrompt struct {
	System        string `json:"system"`
	Prologue      string `json:"prologue"`
	EmptyResponse string `json:"empty_response"`
}

// ScenarioTemplateCatalogParameters is the resolved retrieval/generation knob
// set a template applies when instantiated (same values the server merges into
// the assistant config).
type ScenarioTemplateCatalogParameters struct {
	TopN                   int     `json:"top_n"`
	SimilarityThreshold    float64 `json:"similarity_threshold"`
	VectorSimilarityWeight float64 `json:"vector_similarity_weight"`
	Temperature            float64 `json:"temperature"`
	TopP                   float64 `json:"top_p"`
	MaxTokens              int     `json:"max_tokens"`
}

// ScenarioTemplateCatalogItem is the read-only projection the workspace wizard
// and the conversation-center empty state consume. The API is the single
// source of truth for template copy: the frontend no longer ships a static
// catalog (doc/228 §3.1 T7.0).
type ScenarioTemplateCatalogItem struct {
	ID                  string   `json:"id"`
	Key                 string   `json:"key"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	AppTypes            []string `json:"app_types"`
	LatestVersion       int64    `json:"latest_version"`
	Source              string   `json:"source"`
	Status              string   `json:"status"`
	PayloadSchema       string   `json:"payload_schema"`
	PromptPresetID      string   `json:"prompt_preset_id"`
	ParameterProfileID  string   `json:"parameter_profile_id"`
	DatasetSuggestions  []string `json:"dataset_suggestions"`
	SampleQuestions     []string `json:"sample_questions"`
	EvaluationQuestions []string `json:"evaluation_questions"`
	PermissionAdvice    string   `json:"permission_advice"`
	PublishChecklist    []string `json:"publish_checklist"`
	ExpectedOutcome     string   `json:"expected_outcome"`
	// Prompt and Parameters are the server-resolved instantiation defaults
	// (doc/228 T7.0 single source of truth). The wizard must render these, not
	// its own preset table, and must not send them back as overrides.
	Prompt       ScenarioTemplateCatalogPrompt          `json:"prompt"`
	Parameters   ScenarioTemplateCatalogParameters      `json:"parameters"`
	Translations map[string]ScenarioTemplateTranslation `json:"translations,omitempty"`
}

// ScenarioTemplateCatalog lists the published templates for a workspace. It
// performs no writes: bootstrap happens on tenant creation and at startup,
// never on a GET (doc/228 §4.4).
//
// The listing is deliberately workspace-scoped even for platform admins: the
// catalog is the wizard's creation feed, and instantiation resolves the asset
// in the caller's workspace. Returning foreign tenants' templates made the
// wizard show templates that then failed with 404 "scenario template not
// found" (found in the C3.4 live smoke, 2026-10-26). Cross-tenant inventory is
// the governance asset list, not this endpoint.
func (s *Service) ScenarioTemplateCatalog(ctx context.Context, tenantID string) ([]ScenarioTemplateCatalogItem, error) {
	assets, _, err := s.ListScenarioTemplates(ctx, tenantID, false, 1, 200, repository.GovernanceFilter{
		Status: model.AssetStatusPublished,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ScenarioTemplateCatalogItem, 0, len(assets))
	for i := range assets {
		asset := assets[i]
		payload, err := parseTemplatePayload(asset.PayloadJSON)
		if err != nil {
			// A single malformed custom template must not hide the catalog.
			payload = ScenarioTemplatePayload{}
		}
		description := strings.TrimSpace(asset.Description)
		if description == "" {
			description = payload.Description
		}
		prompt, parameters := resolveCatalogInstantiationDefaults(payload)
		out = append(out, ScenarioTemplateCatalogItem{
			ID: asset.ID, Key: asset.Key, Name: asset.Name, Description: description,
			AppTypes: splitAssetAppTypes(asset.AppTypes), LatestVersion: asset.LatestVersion,
			Source: asset.Source, Status: asset.Status,
			PayloadSchema:       scenarioTemplateSchema,
			PromptPresetID:      payload.PromptPresetID,
			ParameterProfileID:  payload.ParameterProfileID,
			DatasetSuggestions:  nonNilStrings(payload.DatasetSuggestions),
			SampleQuestions:     nonNilStrings(payload.SampleQuestions),
			EvaluationQuestions: nonNilStrings(payload.EvaluationQuestions),
			PermissionAdvice:    payload.PermissionAdvice,
			PublishChecklist:    nonNilStrings(payload.PublishChecklist),
			ExpectedOutcome:     payload.ExpectedOutcome,
			Prompt:              prompt,
			Parameters:          parameters,
			Translations:        payload.Translations,
		})
	}
	return out, nil
}

// resolveCatalogInstantiationDefaults reuses mergeTemplateAuthoring so the
// catalog preview and CreateChatFromScenarioTemplate cannot drift: both call the
// same resolver with no client override.
func resolveCatalogInstantiationDefaults(payload ScenarioTemplatePayload) (ScenarioTemplateCatalogPrompt, ScenarioTemplateCatalogParameters) {
	authoring := mergeTemplateAuthoring(payload, ChatAuthoring{})
	prompt := ScenarioTemplateCatalogPrompt{}
	if authoring.PromptConfig != nil {
		prompt.System, _ = authoring.PromptConfig["system"].(string)
		prompt.Prologue, _ = authoring.PromptConfig["prologue"].(string)
		prompt.EmptyResponse, _ = authoring.PromptConfig["empty_response"].(string)
	}
	parameters := ScenarioTemplateCatalogParameters{
		TopN:                   authoring.TopN,
		SimilarityThreshold:    authoring.SimilarityThreshold,
		VectorSimilarityWeight: authoring.VectorSimilarityWeight,
	}
	parameters.Temperature, _ = numericLLMSetting(authoring.LLMSetting, "temperature")
	parameters.TopP, _ = numericLLMSetting(authoring.LLMSetting, "top_p")
	maxTokens, _ := numericLLMSetting(authoring.LLMSetting, "max_tokens")
	parameters.MaxTokens = int(maxTokens)
	return prompt, parameters
}

// numericLLMSetting reads one generation knob from the loosely typed llm_setting
// map (values arrive as float64 from JSON or as int in code-defined profiles).
func numericLLMSetting(setting map[string]interface{}, key string) (float64, bool) {
	if setting == nil {
		return 0, false
	}
	switch value := setting[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func splitAssetAppTypes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

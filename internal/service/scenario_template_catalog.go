package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
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
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AppTypes    []string `json:"app_types"`
	// RenderableAppTypes is `app_types` minus the forms this template cannot
	// actually build. A v2 template declares `agent` for documentation purposes
	// but carries no canvas, and a v3 pack only instantiates the form it declares
	// — letting the wizard offer those buttons produces a guaranteed 40085 after
	// the user has already picked datasets (doc/276 §8.3).
	RenderableAppTypes  []string `json:"renderable_app_types"`
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
	// Knowledge is the optional 2.0 knowledge contract (doc/276 §7.2): the
	// wizard creates datasets with these parse intents and renders the
	// capability panel from these requirements.
	Knowledge *ScenarioTemplateKnowledge `json:"knowledge,omitempty"`
	// Baseline provenance (doc/276 §7.1). `ID` is the platform baseline id when
	// the workspace has not forked the template; OverridesBaseline=true means
	// the workspace row shadows the baseline and BaselineUpdateAvailable tells
	// the UI to offer a re-fork.
	BaselineKey             string `json:"baseline_key,omitempty"`
	BaselineVersion         int64  `json:"baseline_version"`
	LatestBaselineVersion   int64  `json:"latest_baseline_version"`
	OverridesBaseline       bool   `json:"overrides_baseline"`
	BaselineUpdateAvailable bool   `json:"baseline_update_available"`
	LineageState            string `json:"lineage_state"`
	// EvalGate explains why publish is unavailable for a pack that requires
	// evaluation evidence (doc/276 §9). Zero value = the pack does not require
	// it, so the wizard keeps publish enabled.
	EvalGate scenarioEvalGateStatus `json:"eval_gate"`
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
	tenantAssets, _, err := s.ListScenarioTemplates(ctx, tenantID, false, 1, 500, repository.GovernanceFilter{
		Status: model.AssetStatusPublished,
	})
	if err != nil {
		return nil, err
	}
	// The catalog is the union of the platform baseline and the workspace's own
	// published rows, with the workspace row winning on the same key (doc/276
	// §7.1 step 3). Forking is explicit, so a fresh workspace sees the baseline
	// rows (platform ids) and owns zero rows of its own.
	baselines, err := s.Store.ListPlatformScenarioTemplates(ctx, repository.GovernanceFilter{
		Status: model.AssetStatusPublished,
	})
	if err != nil {
		return nil, err
	}
	own := make(map[string]model.ScenarioTemplateAsset, len(tenantAssets))
	for _, asset := range tenantAssets {
		own[asset.Key] = asset
	}
	out := make([]ScenarioTemplateCatalogItem, 0, len(baselines)+len(tenantAssets))
	seen := make(map[string]bool, len(baselines))
	for i := range baselines {
		baseline := baselines[i]
		if baseline.TenantID != model.PlatformTenantID || baseline.Key == "" {
			continue
		}
		seen[baseline.Key] = true
		tenantAsset, overridden := own[baseline.Key]
		if !overridden {
			item := s.newCatalogItem(ctx, baseline)
			item.BaselineKey = baseline.Key
			item.BaselineVersion = baseline.LatestVersion
			item.LatestBaselineVersion = baseline.LatestVersion
			item.LineageState = model.LineageStateNone
			out = append(out, item)
			continue
		}
		item := s.newCatalogItem(ctx, tenantAsset)
		item.BaselineKey = baseline.Key
		item.BaselineVersion = tenantAsset.BaselineVersion
		item.LatestBaselineVersion = baseline.LatestVersion
		item.OverridesBaseline = true
		item.LineageState = tenantAsset.LineageState
		// A legacy (pre-2.0) copy has no baseline_version at all, so it always
		// advertises the update: its payload predates the knowledge contract and
		// only a re-fork can give it one (doc/276 §7.1 self-check S15).
		item.BaselineUpdateAvailable = tenantAsset.LineageState != model.LineageStateConfirmedBaseline ||
			tenantAsset.BaselineVersion < baseline.LatestVersion
		out = append(out, item)
	}
	for i := range tenantAssets {
		asset := tenantAssets[i]
		if seen[asset.Key] {
			continue
		}
		out = append(out, s.newCatalogItem(ctx, asset))
	}
	// Stage 1 ordering (doc/276 §8.5): every row here is already published, so
	// the tie-breakers that remain are baseline provenance, recent use, then key.
	// Stage 2 (capability verdict) re-sorts inside the wizard after datasets are
	// chosen; the service cannot know a dataset verdict before selection.
	lastUsed, err := s.Store.ListTemplateInstanceLastUsed(ctx, tenantID)
	if err != nil {
		// Ordering is cosmetic: a failed usage query falls back to key order
		// rather than failing the catalog the wizard depends on.
		logger.Warn("scenario template catalog usage lookup failed", "error", err)
		lastUsed = map[string]time.Time{}
	}
	sortCatalogStage1(out, lastUsed)
	return out, nil
}

// sortCatalogStage1 orders by baseline provenance, then recency, then key.
// Baseline rows lead because they are the supported path (fork/instantiate
// read-only); workspace rows follow so a forked copy never hides its origin.
func sortCatalogStage1(items []ScenarioTemplateCatalogItem, lastUsed map[string]time.Time) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		leftBaseline := left.Source == model.TemplateSourceBuiltin
		rightBaseline := right.Source == model.TemplateSourceBuiltin
		if leftBaseline != rightBaseline {
			return leftBaseline
		}
		leftUsed, rightUsed := lastUsed[left.ID], lastUsed[right.ID]
		if !leftUsed.Equal(rightUsed) {
			return leftUsed.After(rightUsed)
		}
		return left.Key < right.Key
	})
}

// newCatalogItem projects one stored asset into the read-only catalog shape.
// A single malformed custom template must not hide the catalog, so an
// unparsable payload degrades to an empty payload instead of an error.
func (s *Service) newCatalogItem(ctx context.Context, asset model.ScenarioTemplateAsset) ScenarioTemplateCatalogItem {
	payload, err := parseTemplatePayload(asset.PayloadJSON)
	if err != nil {
		payload = ScenarioTemplatePayload{}
	}
	description := strings.TrimSpace(asset.Description)
	if description == "" {
		description = payload.Description
	}
	prompt, parameters := resolveCatalogInstantiationDefaults(payload)
	schema := scenarioTemplateSchema
	if payload.Pack != nil {
		schema = ScenarioPackSchemaV3
	}
	return ScenarioTemplateCatalogItem{
		ID: asset.ID, Key: asset.Key, Name: asset.Name, Description: description,
		AppTypes:           splitAssetAppTypes(asset.AppTypes),
		RenderableAppTypes: renderableScenarioAppTypes(splitAssetAppTypes(asset.AppTypes), payload),
		LatestVersion:      asset.LatestVersion,
		Source:             asset.Source, Status: asset.Status,
		PayloadSchema:       schema,
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
		Knowledge:           payload.Knowledge,
		LineageState:        asset.LineageState,
		EvalGate:            s.describeScenarioEvalGate(ctx, asset.TenantID, asset.ID, asset.PayloadJSON),
	}
}

// renderableScenarioAppTypes answers "which buttons can this template honour?".
// It is deliberately conservative: chat and search are always renderable because
// both paths have a server-side fallback (preset resolver / default search knobs),
// while an agent needs a canvas that only a v3 pack supplies. A v3 pack is also
// form-specific, so it only renders the form it declares.
func renderableScenarioAppTypes(declared []string, payload ScenarioTemplatePayload) []string {
	out := make([]string, 0, len(declared))
	for _, appType := range declared {
		switch appType {
		case "chat", "search":
			if payload.Pack != nil && payload.Pack.Application.AppType != appType {
				continue
			}
			out = append(out, appType)
		case "agent":
			if payload.Pack != nil && payload.Pack.RuntimeExtension.RAGFlow != nil &&
				len(payload.Pack.RuntimeExtension.RAGFlow.CanvasDSL) > 0 {
				out = append(out, appType)
			}
		}
	}
	return out
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

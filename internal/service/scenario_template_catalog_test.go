package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestScenarioTemplateCatalogResolvesInstantiationDefaults pins doc/228 T7.0:
// the wizard renders exactly the prompt and parameters the server will apply,
// so a governance edit of a template's preset/profile is visible before the
// operator creates the assistant.
func TestScenarioTemplateCatalogResolvesInstantiationDefaults(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Catalog Resolve Tenant")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected seeded templates")
	}
	for _, item := range items {
		preset, ok := scenarioPromptPresets[item.PromptPresetID]
		if !ok {
			t.Fatalf("%s: unknown seeded preset %q", item.Key, item.PromptPresetID)
		}
		if item.Prompt.System != preset.system {
			t.Errorf("%s: catalog system prompt does not match instantiation resolver", item.Key)
		}
		if item.Prompt.Prologue != preset.prologue || item.Prompt.EmptyResponse != preset.emptyResponse {
			t.Errorf("%s: catalog prologue/empty_response do not match instantiation resolver", item.Key)
		}
		profile, ok := scenarioParameterProfiles[item.ParameterProfileID]
		if !ok {
			t.Fatalf("%s: unknown seeded profile %q", item.Key, item.ParameterProfileID)
		}
		if item.Parameters.TopN != profile.topN {
			t.Errorf("%s: catalog top_n=%d, resolver=%d", item.Key, item.Parameters.TopN, profile.topN)
		}
		if item.Parameters.SimilarityThreshold != profile.similarityThreshold {
			t.Errorf("%s: catalog similarity_threshold=%v, resolver=%v", item.Key, item.Parameters.SimilarityThreshold, profile.similarityThreshold)
		}
		if item.Parameters.MaxTokens <= 0 || item.Parameters.Temperature <= 0 || item.Parameters.TopP <= 0 {
			t.Errorf("%s: generation knobs were not projected: %+v", item.Key, item.Parameters)
		}
	}
}

// TestScenarioTemplateCatalogStaysInTheCallersWorkspace is the regression for
// the live-smoke 404: a platform admin used to receive a cross-tenant catalog
// (scopeAll) but instantiation resolves the asset in the caller's workspace, so
// every template shown by the wizard failed with "scenario template not found".
func TestScenarioTemplateCatalogStaysInTheCallersWorkspace(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	owner, err := svc.CreateTenant(ctx, "Catalog Owner Tenant")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateTenant(ctx, "Catalog Other Tenant")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected seeded templates for the owner tenant")
	}
	ownAssets, _, err := svc.ListScenarioTemplates(ctx, owner.ID, true, 1, 200, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	own := map[string]bool{}
	for _, asset := range ownAssets {
		if asset.TenantID == owner.ID {
			own[asset.ID] = true
		}
	}
	for _, item := range items {
		if !own[item.ID] {
			t.Fatalf("catalog leaked template %s from another workspace", item.ID)
		}
	}
	otherItems, err := svc.ScenarioTemplateCatalog(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range otherItems {
		if own[item.ID] {
			t.Fatalf("template %s appeared in two workspaces", item.ID)
		}
	}
}

// TestMergeTemplateAuthoringKeepsTemplateDefaultsOnPartialOverride is the
// regression for the drift this change fixes: sending only `llm_id` used to
// replace the whole authoring payload and drop the template prompt/params.
func TestMergeTemplateAuthoringKeepsTemplateDefaultsOnPartialOverride(t *testing.T) {
	payload := ScenarioTemplatePayload{
		PromptPresetID:     "enterprise-qa",
		ParameterProfileID: "balanced",
	}

	modelOnly := mergeTemplateAuthoring(payload, ChatAuthoring{LLMID: "model-1"})
	if modelOnly.LLMID != "model-1" {
		t.Fatalf("llm_id override lost: %+v", modelOnly)
	}
	if modelOnly.PromptConfig == nil || modelOnly.PromptConfig["system"] != scenarioPromptPresets["enterprise-qa"].system {
		t.Fatalf("template prompt dropped by a partial override: %+v", modelOnly.PromptConfig)
	}
	if modelOnly.TopN != scenarioParameterProfiles["balanced"].topN {
		t.Fatalf("template top_n dropped by a partial override: %d", modelOnly.TopN)
	}

	retrievalOnly := mergeTemplateAuthoring(payload, ChatAuthoring{TopN: 3, SimilarityThreshold: 0.5})
	if retrievalOnly.TopN != 3 || retrievalOnly.SimilarityThreshold != 0.5 {
		t.Fatalf("explicit retrieval override lost: %+v", retrievalOnly)
	}
	if retrievalOnly.PromptConfig["system"] != scenarioPromptPresets["enterprise-qa"].system {
		t.Fatal("prompt must survive a retrieval-only override")
	}

	llmKnob := mergeTemplateAuthoring(payload, ChatAuthoring{LLMSetting: map[string]interface{}{"temperature": 0.9}})
	if llmKnob.LLMSetting["temperature"] != 0.9 {
		t.Fatalf("llm_setting override lost: %+v", llmKnob.LLMSetting)
	}
	if llmKnob.LLMSetting["top_p"] != scenarioParameterProfiles["balanced"].llmSetting["top_p"] {
		t.Fatalf("unrelated llm_setting keys must be preserved: %+v", llmKnob.LLMSetting)
	}
}

// TestResolveCatalogInstantiationDefaultsToleratesUnknownPreset keeps the
// catalog resilient: a custom template with a stale preset id must not fail the
// whole list, it degrades to "no prompt defaults".
func TestResolveCatalogInstantiationDefaultsToleratesUnknownPreset(t *testing.T) {
	prompt, parameters := resolveCatalogInstantiationDefaults(ScenarioTemplatePayload{
		PromptPresetID: "does-not-exist", ParameterProfileID: "does-not-exist",
	})
	if prompt.System != "" || prompt.Prologue != "" || prompt.EmptyResponse != "" {
		t.Fatalf("unknown preset must not synthesize prompt copy: %+v", prompt)
	}
	if parameters.TopN != 0 || parameters.MaxTokens != 0 {
		t.Fatalf("unknown profile must not synthesize parameters: %+v", parameters)
	}

	prompt, _ = resolveCatalogInstantiationDefaults(ScenarioTemplatePayload{PromptPresetID: "policy"})
	if !strings.Contains(prompt.System, "制度") {
		t.Fatalf("known preset must resolve: %q", prompt.System)
	}
}

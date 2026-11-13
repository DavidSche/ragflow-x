package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// P0-3 capability check: fixed verdict order, per-role requirements, aggregate
// modes (doc/276 §6.5 / §7.3).
func TestEvaluateRequirementsBlockedBeforeRepairable(t *testing.T) {
	contract := &ScenarioTemplateKnowledge{
		Aggregate: scenarioAggregateAllRequired,
		RequiredCapabilities: []ScenarioCapabilityRequirement{
			{Capability: "wiki", Scope: scenarioRequirementScopeAll, MinLevel: CapabilityStatusConfigured},
			{Capability: "metadata_filter", Scope: scenarioRequirementScopeAll, MinLevel: CapabilityStatusReady},
		},
	}
	profile := DatasetCapabilityProfile{
		DatasetID: "ds-1", ChunkMethod: "general", DocumentCount: 3, ParsedCount: 3,
		Capabilities: []CapabilityEvidence{{Capability: "vector", Status: CapabilityStatusOperational}},
	}
	verdict := EvaluateRequirements(contract, scenarioDatasetRolePrimary, profile)
	if verdict.Verdict != CapabilityVerdictBlocked {
		t.Fatalf("unsupported engine capability must block, got %q", verdict.Verdict)
	}
	// Both gaps stay visible: the check reports evidence, it does not stop at the
	// first failure.
	if len(verdict.Requirements) != 2 {
		t.Fatalf("expected 2 requirement verdicts, got %d", len(verdict.Requirements))
	}
}

func TestEvaluateRequirementsSatisfiedWhenEvidenceReachesMinLevel(t *testing.T) {
	contract := &ScenarioTemplateKnowledge{
		RequiredCapabilities: []ScenarioCapabilityRequirement{
			{Capability: "vector", Scope: scenarioRequirementScopeAll, MinLevel: scenarioRequirementLevelConfig},
		},
	}
	profile := DatasetCapabilityProfile{
		DatasetID: "ds-1", ChunkMethod: "general", DocumentCount: 2, ParsedCount: 2,
		Capabilities: []CapabilityEvidence{{Capability: "vector", Status: CapabilityStatusOperational}},
	}
	if got := EvaluateRequirements(contract, scenarioDatasetRolePrimary, profile).Verdict; got != CapabilityVerdictSatisfied {
		t.Fatalf("expected satisfied, got %q", got)
	}
}

func TestEvaluateRequirementsMetadataGapWithoutDocumentsIsProbeLimited(t *testing.T) {
	contract := &ScenarioTemplateKnowledge{
		RequiredCapabilities: []ScenarioCapabilityRequirement{
			{Capability: "metadata_filter", Scope: scenarioRequirementScopeAll, MinLevel: CapabilityStatusReady},
		},
	}
	empty := DatasetCapabilityProfile{DatasetID: "ds-empty", DocumentCount: 0}
	if got := EvaluateRequirements(contract, scenarioDatasetRolePrimary, empty).Verdict; got != CapabilityVerdictProbeLimited {
		t.Fatalf("empty dataset must cap at probe_limited, got %q", got)
	}
	withDocs := DatasetCapabilityProfile{DatasetID: "ds-docs", DocumentCount: 4, ParsedCount: 4}
	if got := EvaluateRequirements(contract, scenarioDatasetRolePrimary, withDocs).Verdict; got != CapabilityVerdictRepairable {
		t.Fatalf("metadata gap with documents must be repairable, got %q", got)
	}
}

func TestEvaluateRequirementsChunkMethodMismatchIsRepairable(t *testing.T) {
	contract := &ScenarioTemplateKnowledge{
		Datasets: []ScenarioKnowledgeDataset{{
			Role:        scenarioDatasetRolePrimary,
			ParseIntent: &ScenarioParseIntent{Mode: model.ParseModeBuiltin, ChunkMethod: "book"},
		}},
		RequiredCapabilities: []ScenarioCapabilityRequirement{
			{Capability: "vector", Scope: scenarioRequirementScopeAll, MinLevel: CapabilityStatusSupported},
		},
	}
	profile := DatasetCapabilityProfile{DatasetID: "ds-1", ChunkMethod: "general"}
	verdict := EvaluateRequirements(contract, scenarioDatasetRolePrimary, profile)
	if verdict.Verdict != CapabilityVerdictRepairable {
		t.Fatalf("expected repairable, got %q", verdict.Verdict)
	}
	hasParseRepair := false
	for _, repair := range verdict.Repairs {
		if repair.Action == "apply_parse_profile" {
			hasParseRepair = true
		}
	}
	if !hasParseRepair {
		t.Fatalf("expected an apply_parse_profile repair, got %+v", verdict.Repairs)
	}
}

func TestAggregateVerdictsHonoursAnyPrimary(t *testing.T) {
	verdicts := []DatasetCapabilityVerdict{
		{Role: scenarioDatasetRolePrimary, Verdict: CapabilityVerdictSatisfied},
		{Role: scenarioDatasetRoleReference, Verdict: CapabilityVerdictBlocked},
	}
	anyPrimary := &ScenarioTemplateKnowledge{Aggregate: scenarioAggregateAnyPrimary}
	if got := AggregateVerdicts(anyPrimary, verdicts); got != "any_primary_satisfied" {
		t.Fatalf("any_primary must accept one satisfied primary dataset, got %q", got)
	}
	allRequired := &ScenarioTemplateKnowledge{Aggregate: scenarioAggregateAllRequired}
	if got := AggregateVerdicts(allRequired, verdicts); got != "all_required_unsatisfied" {
		t.Fatalf("all_required must reject a blocked dataset, got %q", got)
	}
}

func TestCheckScenarioTemplateCapabilitiesReportsRoles(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Capability Tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "capability-kb")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), ScenarioTemplateInput{
		Key: "cap-check", Name: "Cap Check", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{
			Knowledge: &ScenarioTemplateKnowledge{
				Aggregate: scenarioAggregateAllRequired,
				Datasets: []ScenarioKnowledgeDataset{{
					Role:        scenarioDatasetRolePrimary,
					ParseIntent: &ScenarioParseIntent{Mode: model.ParseModeBuiltin, ChunkMethod: "general"},
				}},
				// The mock engine exposes no embedding model, so vector tops out at
				// `supported` and a `configured` requirement is honestly repairable.
				// This test is about role aggregation + repair reporting, not about
				// the mock's vector ceiling.
				RequiredCapabilities: []ScenarioCapabilityRequirement{
					{Capability: "vector", Scope: scenarioRequirementScopeAll, MinLevel: scenarioRequirementLevelConfig},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.CheckScenarioTemplateCapabilities(ctx, tenant.ID, asset.ID, CapabilityCheckInput{
		AppType:  "chat",
		Datasets: []CapabilityCheckDataset{{Role: scenarioDatasetRolePrimary, DatasetID: dataset.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Datasets) != 1 || view.Datasets[0].Role != scenarioDatasetRolePrimary {
		t.Fatalf("expected one primary dataset verdict, got %+v", view.Datasets)
	}
	if view.Verdict != CapabilityVerdictRepairable {
		t.Fatalf("expected repairable, got %q (%+v)", view.Verdict, view.Datasets[0].Requirements)
	}
	if view.Aggregate != "all_required_unsatisfied" {
		t.Fatalf("unexpected aggregate %q", view.Aggregate)
	}
	if len(view.Datasets[0].Repairs) == 0 {
		t.Fatal("expected at least one repair for the unsatisfied vector requirement")
	}
	if view.CapabilitySetVersion != CapabilityCheckVersion {
		t.Fatalf("capability set version missing: %q", view.CapabilitySetVersion)
	}
}

// P0-4 repairs: 40915 before 40917, and the pushdown writer finally exists.
func TestApplyParseProfileRefusesToRewriteParsedDocuments(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Parse Profile Tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "parsed-kb")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := svc.UploadDocument(ctx, tenant.ID, dataset.ID, actorID(t, svc), "doc.md", []byte("# hello"))
	if err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	if err := mock.SetDocumentExecutionState(ctx, dataset.RAGFlowDatasetID, doc.ID, "DONE", 7, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplyParseProfile(ctx, tenant.ID, actorID(t, svc), dataset.ID, ParseProfileInput{ChunkMethod: "book"})
	if err == nil {
		t.Fatal("expected 40915 for a dataset with parsed documents")
	}
	var typed *httperr.Error
	if !errors.As(err, &typed) || typed.Code != codeParseProfileRequiresRewrite {
		t.Fatalf("expected code %d, got %v", codeParseProfileRequiresRewrite, err)
	}
	// The 40915 check runs before the shared-impact check, so the error stays the
	// same even though the dataset is also shared by the chat that owns it.
	if typed.Status != 409 {
		t.Fatalf("unexpected status %d", typed.Status)
	}
}

func TestSetDatasetPushdownRequiresSharedImpactAcknowledgement(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Pushdown Tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "pushdown-kb")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "shared chat", []string{dataset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if chat.ID == "" {
		t.Fatal("chat not created")
	}
	err = svc.SetDatasetPushdown(ctx, tenant.ID, actorID(t, svc), dataset.ID, true, false)
	var typed *httperr.Error
	if !errors.As(err, &typed) || typed.Code != codeDatasetSharedUnacknowledged {
		t.Fatalf("expected %d, got %v", codeDatasetSharedUnacknowledged, err)
	}
	if err := svc.SetDatasetPushdown(ctx, tenant.ID, actorID(t, svc), dataset.ID, true, true); err != nil {
		t.Fatal(err)
	}
	link, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || link == nil {
		t.Fatal(err)
	}
	if !link.PushdownEnabled {
		t.Fatal("pushdown_enabled was not persisted")
	}
}

// A pushdown-only update must not need a rename: an empty name means "keep the
// current display name" (drill defect 2026-11-13).
func TestUpdateDatasetWithoutNameKeepsCurrentName(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Partial Update Tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "pushdown-only-kb")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := svc.UpdateDataset(ctx, tenant.ID, dataset.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Name != "pushdown-only-kb" {
		t.Fatalf("name drifted to %q", summary.Name)
	}
	if err := svc.SetDatasetPushdown(ctx, tenant.ID, actorID(t, svc), dataset.ID, true, true); err != nil {
		t.Fatal(err)
	}
	link, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || link == nil {
		t.Fatal(err)
	}
	if link.Name != "pushdown-only-kb" || !link.PushdownEnabled {
		t.Fatalf("unexpected link state name=%q pushdown=%v", link.Name, link.PushdownEnabled)
	}
}

// P0-5 unified instantiation: header-only idempotency + replay + in-progress.
func TestInstantiateScenarioTemplateReplaysOnSameKey(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Instantiate Tenant")
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(ctx, tenant.ID, actorID(t, svc), "inst project")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("catalog empty: %v", err)
	}
	templateID := items[0].ID

	first, err := svc.InstantiateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), templateID, "key-1", UnifiedInstantiateInput{
		AppType: "chat", Name: "inst chat", ProjectID: project.ID, CreateMissingDatasets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Chat == nil {
		t.Fatalf("expected a fresh chat instantiation, got %+v", first)
	}
	second, err := svc.InstantiateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), templateID, "key-1", UnifiedInstantiateInput{
		AppType: "chat", Name: "inst chat", ProjectID: project.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.InstanceID != first.InstanceID {
		t.Fatalf("expected replay of %s, got %+v", first.InstanceID, second)
	}
	if second.Chat == nil || second.Chat.ID != first.Chat.ID {
		t.Fatalf("replay must return the same chat, got %+v", second.Chat)
	}
}

func TestInstantiateScenarioTemplateRequiresIdempotencyKey(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	_, err := svc.InstantiateScenarioTemplate(ctx, "t", "u", "tpl", "", UnifiedInstantiateInput{})
	var typed *httperr.Error
	if !errors.As(err, &typed) || typed.Code != codeInstantiationKeyRequired {
		t.Fatalf("expected %d, got %v", codeInstantiationKeyRequired, err)
	}
}

func TestInstantiateScenarioTemplateRejectsConcurrentRun(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Concurrent Tenant")
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(ctx, tenant.ID, actorID(t, svc), "concurrent project")
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformBaseline(t, svc)
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("catalog empty: %v", err)
	}
	templateID := items[0].ID
	_, selected, _, err := svc.GetScenarioTemplate(ctx, tenant.ID, templateID, 0, false)
	if err != nil || selected == nil {
		t.Fatalf("template version missing: %v", err)
	}
	// A pending row with the same key models a run that has not settled.
	pending := &model.TemplateInstance{
		ID: "inst-pending", TenantID: tenant.ID, ProjectID: project.ID,
		TemplateID: templateID, TemplateVersionID: selected.ID, OwnerID: actorID(t, svc),
		IdempotencyKey: "busy-key", AssistantKind: instantiationAppTypeChat,
		InstantiationState: model.InstantiationPending, UpstreamRefs: "{}",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateTemplateInstance(ctx, pending); err != nil {
		t.Fatal(err)
	}
	_, err = svc.InstantiateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), templateID, "busy-key", UnifiedInstantiateInput{
		AppType: "chat", ProjectID: project.ID,
	})
	var typed *httperr.Error
	if !errors.As(err, &typed) || typed.Code != codeInstantiationInProgress {
		t.Fatalf("expected %d, got %v", codeInstantiationInProgress, err)
	}
}

// A failed run must be resumable on the same key: a terminal failure that keeps
// answering "in progress" forces the caller onto a new key and a duplicate
// instance row (doc/276 §7.5 rule 3).
func TestInstantiateScenarioTemplateResumesAfterFailure(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Resume Tenant")
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(ctx, tenant.ID, actorID(t, svc), "resume project")
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformBaseline(t, svc)
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("catalog empty: %v", err)
	}
	templateID := items[0].ID
	_, selected, _, err := svc.GetScenarioTemplate(ctx, tenant.ID, templateID, 0, false)
	if err != nil || selected == nil {
		t.Fatalf("template version missing: %v", err)
	}
	now := time.Now().UTC()
	failed := &model.TemplateInstance{
		ID: "inst-failed", TenantID: tenant.ID, ProjectID: project.ID,
		TemplateID: templateID, TemplateVersionID: selected.ID, OwnerID: actorID(t, svc),
		IdempotencyKey: "resume-key", AssistantKind: instantiationAppTypeChat,
		InstantiationState: model.InstantiationFailed, UpstreamRefs: "{}",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateTemplateInstance(ctx, failed); err != nil {
		t.Fatal(err)
	}
	result, err := svc.InstantiateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), templateID, "resume-key", UnifiedInstantiateInput{
		AppType: "chat", Name: "resumed chat", ProjectID: project.ID, CreateMissingDatasets: true,
	})
	if err != nil {
		t.Fatalf("a failed instance must be resumable, got %v", err)
	}
	if result.InstanceID != "inst-failed" {
		t.Fatalf("resume must reuse the existing instance, got %s", result.InstanceID)
	}
	if result.Replayed || result.Chat == nil || result.InstantiationState != model.InstantiationCompleted {
		t.Fatalf("resume must complete the run, got %+v", result)
	}
}

// P0-7 immutability: an update appends a version and never rewrites history.
func TestScenarioTemplateUpdateNeverRewritesHistory(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Immutable Tenant")
	if err != nil {
		t.Fatal(err)
	}
	actor := actorID(t, svc)
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, actor, ScenarioTemplateInput{
		Key: "immutable", Name: "Immutable", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{Description: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := svc.Store.GetScenarioTemplateVersion(ctx, tenant.ID, asset.ID, 1)
	if err != nil || v1 == nil {
		t.Fatal(err)
	}
	if v1.PayloadSchema != model.TemplatePayloadSchemaV2 || v1.SnapshotProvenance != model.SnapshotAuthored {
		t.Fatalf("unexpected version metadata: %s/%s", v1.PayloadSchema, v1.SnapshotProvenance)
	}
	original := v1.PayloadJSON
	if _, err := svc.UpdateScenarioTemplate(ctx, tenant.ID, actor, asset.ID, ScenarioTemplateInput{
		Name: "Immutable", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{Description: "v2"},
	}); err != nil {
		t.Fatal(err)
	}
	readBack, err := svc.Store.GetScenarioTemplateVersion(ctx, tenant.ID, asset.ID, 1)
	if err != nil || readBack == nil {
		t.Fatal(err)
	}
	if readBack.PayloadJSON != original {
		t.Fatalf("v1 payload was rewritten:\nold %s\nnew %s", original, readBack.PayloadJSON)
	}
	latest, err := svc.Store.GetScenarioTemplateVersion(ctx, tenant.ID, asset.ID, 2)
	if err != nil || latest == nil {
		t.Fatal(err)
	}
	if latest.PayloadJSON == original {
		t.Fatal("v2 must differ from v1")
	}
}

func TestFrozenPresetSnapshotResolvesLegacyVersions(t *testing.T) {
	legacy := promptPresetTextForVersion("enterprise-qa", model.TemplatePayloadSchemaV2, model.SnapshotLegacyReconstructed)
	if legacy == nil || legacy.system == "" {
		t.Fatal("legacy v2 version must resolve the frozen snapshot")
	}
	authored := promptPresetTextForVersion("enterprise-qa", model.TemplatePayloadSchemaV2, model.SnapshotAuthored)
	if authored == nil || authored.system == "" {
		t.Fatal("authored v2 version must resolve the live preset map")
	}
	if legacy.system != authored.system {
		// Today the snapshot was copied verbatim; if someone edits the live map the
		// frozen value must stay put, which is exactly the point.
		t.Log("live preset map diverged from the frozen snapshot (expected once presets change)")
	}
	if promptPresetTextForVersion("unknown", model.TemplatePayloadSchemaV2, model.SnapshotLegacyReconstructed) != nil {
		t.Fatal("unknown preset must not resolve")
	}
}

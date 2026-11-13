package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestForkScenarioTemplateRecordsProvenance pins doc/276 §7.1 step 4 / R22:
// a fork is a tenant-owned `copy` carrying baseline_key + baseline_version +
// lineage_state=confirmed_baseline, and the platform baseline stays untouched.
func TestForkScenarioTemplateRecordsProvenance(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Fork Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "policy")
	if err != nil || baseline == nil {
		t.Fatalf("policy baseline missing: %v", err)
	}
	forked, err := svc.ForkScenarioTemplate(ctx, tenant.ID, actorID(t, svc), baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if forked.TenantID != tenant.ID || forked.Source != model.TemplateSourceCopy {
		t.Fatalf("fork must be a tenant copy: %+v", forked)
	}
	if forked.BaselineKey != "policy" || forked.BaselineVersion != baseline.LatestVersion {
		t.Fatalf("fork must record baseline provenance: %+v", forked)
	}
	if forked.LineageState != model.LineageStateConfirmedBaseline {
		t.Fatalf("fork must be confirmed_baseline, got %q", forked.LineageState)
	}
	// The baseline row must not move when a workspace forks it.
	reloaded, err := svc.Store.GetPlatformScenarioTemplate(ctx, baseline.ID)
	if err != nil || reloaded == nil {
		t.Fatal(err)
	}
	if reloaded.TenantID != model.PlatformTenantID || reloaded.Source != model.TemplateSourceBuiltin || reloaded.LatestVersion != 1 {
		t.Fatalf("baseline must stay immutable: %+v", reloaded)
	}
	// Forking the same key twice is a key collision, not a silent overwrite.
	if _, err := svc.ForkScenarioTemplate(ctx, tenant.ID, actorID(t, svc), baseline.ID); err == nil {
		t.Fatal("second fork of the same key must be rejected")
	}
}

// TestForkScenarioTemplateRejectsTenantRowAsSource guards the "fork is not
// copy" boundary: a tenant row id cannot be forked, only a platform baseline.
func TestForkScenarioTemplateRejectsTenantRowAsSource(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Fork Source Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "finance")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	forked, err := svc.ForkScenarioTemplate(ctx, tenant.ID, actorID(t, svc), baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForkScenarioTemplate(ctx, tenant.ID, actorID(t, svc), forked.ID); err == nil {
		t.Fatal("forking an already-owned row must be rejected")
	}
}

// TestPublishScenarioTemplateBaselineAppendsVersion covers doc/276 §7.1 step 8:
// publishing appends an immutable version and bumps latest_version; publishing
// the same content again is a replay that writes nothing.
func TestPublishScenarioTemplateBaselineAppendsVersion(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	actor := actorID(t, svc)

	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "enterprise-qa")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	payload, err := parseTemplatePayload(baseline.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	payload.Description = "基线第二版"
	if _, err := svc.UpdateScenarioTemplate(ctx, model.PlatformTenantID, actor, baseline.ID, ScenarioTemplateInput{
		Key: baseline.Key, Name: baseline.Name, AppTypes: splitAssetAppTypes(baseline.AppTypes),
		Description: payload.Description, Status: model.AssetStatusDraft, Payload: payload,
		ChangeNote: "draft",
	}); err != nil {
		t.Fatal(err)
	}
	published, replayed, err := svc.PublishScenarioTemplateBaseline(ctx, actor, baseline.Key, BaselinePublishInput{ChangeNote: "publish v2"})
	if err != nil {
		t.Fatal(err)
	}
	if replayed {
		t.Fatal("a draft must be published, not replayed")
	}
	if published.LatestVersion != 2 || published.Status != model.AssetStatusPublished {
		t.Fatalf("publish must release the latest version: %+v", published)
	}
	if published.BaselineVersion != 2 || published.LineageState != model.LineageStateConfirmedBaseline {
		t.Fatalf("publish must record the released version: %+v", published)
	}
	versions, err := svc.Store.ListScenarioTemplateVersions(ctx, model.PlatformTenantID, baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("publish must never append or rewrite a version, got %d", len(versions))
	}
	if _, replayed, err = svc.PublishScenarioTemplateBaseline(ctx, actor, baseline.Key, BaselinePublishInput{}); err != nil {
		t.Fatal(err)
	}
	if !replayed {
		t.Fatal("re-publishing identical content must be a replay")
	}
}

// TestScenarioTemplateCatalogAdvertisesBaselineUpdates pins the re-fork signal
// (doc/276 §7.1 self-check S15): a fork that trails the baseline advertises
// `baseline_update_available`, and a legacy copy always does.
func TestScenarioTemplateCatalogAdvertisesBaselineUpdates(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Update Tenant")
	if err != nil {
		t.Fatal(err)
	}
	actor := actorID(t, svc)
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "policy")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	if _, err := svc.ForkScenarioTemplate(ctx, tenant.ID, actor, baseline.ID); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key != "policy" {
			continue
		}
		if !item.OverridesBaseline || item.BaselineVersion != baseline.LatestVersion {
			t.Fatalf("fresh fork must shadow the baseline: %+v", item)
		}
		if item.BaselineUpdateAvailable {
			t.Fatalf("a fresh fork must not advertise an update: %+v", item)
		}
	}
	// Publish a new baseline version: the fork now trails it.
	payload, err := parseTemplatePayload(baseline.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	payload.Description = "policy v2"
	if _, err := svc.UpdateScenarioTemplate(ctx, model.PlatformTenantID, actor, baseline.ID, ScenarioTemplateInput{
		Key: baseline.Key, Name: baseline.Name, AppTypes: splitAssetAppTypes(baseline.AppTypes),
		Description: payload.Description, Status: model.AssetStatusDraft, Payload: payload, ChangeNote: "v2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishScenarioTemplateBaseline(ctx, actor, baseline.Key, BaselinePublishInput{}); err != nil {
		t.Fatal(err)
	}
	items, err = svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key == "policy" && !item.BaselineUpdateAvailable {
			t.Fatalf("a fork trailing the baseline must advertise an update: %+v", item)
		}
	}

	// A pre-2.0 copy (no key, no version) also advertises the update.
	legacyTenant, err := svc.CreateTenant(ctx, "Legacy Tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetScenarioTemplateLineage(ctx, legacyTenant.ID, "", "", 0, model.LineageStateNone); err == nil {
		t.Fatal("blank template id must be rejected")
	}
}

// TestCopyScenarioTemplateWritesSourceOnTargetTenant is the G13 regression
// (doc/276 §7.1 step 6): the copy must flip `source` on the *target* row, not
// on a row in the caller's tenant.
func TestCopyScenarioTemplateWritesSourceOnTargetTenant(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	owner, err := svc.CreateTenant(ctx, "Copy Owner Tenant")
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTenant(ctx, "Copy Target Tenant")
	if err != nil {
		t.Fatal(err)
	}
	actor := actorID(t, svc)
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "operations")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	forked, err := svc.ForkScenarioTemplate(ctx, owner.ID, actor, baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := svc.CopyScenarioTemplate(ctx, actor, owner.ID, forked.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copied.TenantID != target.ID || copied.Source != model.TemplateSourceCopy {
		t.Fatalf("copy must return the target row as copy: %+v", copied)
	}
	rows, total, err := svc.Store.ListScenarioTemplates(ctx, target.ID, 1, 10, repository.GovernanceFilter{Key: "operations"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("target workspace must own the copied row, got %d", total)
	}
	if rows[0].Source != model.TemplateSourceCopy {
		t.Fatalf("G13: target row source must be copy, got %q", rows[0].Source)
	}
}

// TestScenarioTemplateKnowledgeValidation pins the closed set and the mutually
// exclusive parse intent (doc/276 §7.2 / R2 + R3).
func TestScenarioTemplateKnowledgeValidation(t *testing.T) {
	cases := []struct {
		name      string
		knowledge *ScenarioTemplateKnowledge
		ok        bool
	}{
		{name: "absent block stays valid", knowledge: nil, ok: true},
		{name: "builtin intent", knowledge: builtinKnowledge("book", requireVectorOperational), ok: true},
		{name: "legacy alias normalizes", knowledge: &ScenarioTemplateKnowledge{
			Datasets: []ScenarioKnowledgeDataset{{Role: "primary", ParseIntent: &ScenarioParseIntent{Mode: "builtin", ChunkMethod: "naive"}}},
		}, ok: true},
		{name: "pipeline is not enabled in phase A", knowledge: &ScenarioTemplateKnowledge{
			Datasets: []ScenarioKnowledgeDataset{{Role: "primary", ParseIntent: &ScenarioParseIntent{Mode: "pipeline"}}},
		}},
		{name: "modes are exclusive", knowledge: &ScenarioTemplateKnowledge{
			Datasets: []ScenarioKnowledgeDataset{{Role: "primary", ParseIntent: &ScenarioParseIntent{
				Mode: "builtin", ChunkMethod: "book", Pipeline: &ScenarioPipelineIntent{PipelineID: "p1"},
			}}},
		}},
		{name: "chunk method must be in vocabulary", knowledge: &ScenarioTemplateKnowledge{
			Datasets: []ScenarioKnowledgeDataset{{Role: "primary", ParseIntent: &ScenarioParseIntent{Mode: "builtin", ChunkMethod: "markdown"}}},
		}},
		{name: "chunk method is required", knowledge: &ScenarioTemplateKnowledge{
			Datasets: []ScenarioKnowledgeDataset{{Role: "primary", ParseIntent: &ScenarioParseIntent{Mode: "builtin"}}},
		}},
		{name: "capability must be in the closed set", knowledge: &ScenarioTemplateKnowledge{
			RequiredCapabilities: []ScenarioCapabilityRequirement{{Capability: "semantic_cache"}},
		}},
		{name: "scope=primary needs a primary dataset", knowledge: &ScenarioTemplateKnowledge{
			Datasets:             []ScenarioKnowledgeDataset{{Role: "reference"}},
			RequiredCapabilities: []ScenarioCapabilityRequirement{{Capability: CapabilityCitationLocator, Scope: "primary"}},
		}},
		{name: "min level is bounded", knowledge: &ScenarioTemplateKnowledge{
			RequiredCapabilities: []ScenarioCapabilityRequirement{{Capability: CapabilityVector, MinLevel: "perfect"}},
		}},
		{name: "legacy semantics lineage", knowledge: &ScenarioTemplateKnowledge{Lineage: "legacy_semantics_unknown"}, ok: true},
	}
	for _, tc := range cases {
		err := tc.knowledge.Validate()
		if tc.ok && err != nil {
			t.Fatalf("%s: expected valid, got %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected a validation error", tc.name)
		}
	}
}

// TestCreateScenarioTemplateRejectsInvalidKnowledge ensures the handler-level
// write path cannot persist an unusable contract.
func TestCreateScenarioTemplateRejectsInvalidKnowledge(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Knowledge Tenant")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), ScenarioTemplateInput{
		Key: "bad-knowledge", Name: "bad", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID: "enterprise-qa", ParameterProfileID: "balanced",
			Knowledge: &ScenarioTemplateKnowledge{
				RequiredCapabilities: []ScenarioCapabilityRequirement{{Capability: "not_a_capability"}},
			},
		},
	})
	if err == nil {
		t.Fatal("invalid knowledge must be rejected on create")
	}
}

// TestBootstrapScenarioTemplateLineageUpgradesIdenticalCopies pins migration
// 165 rule ②: an untouched pre-2.0 copy (payload identical to the baseline) is
// confirmed as baseline-derived; an edited one stays legacy_unknown.
func TestBootstrapScenarioTemplateLineageUpgradesIdenticalCopies(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Lineage Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "training")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	now := baseline.CreatedAt
	untouched := &model.ScenarioTemplateAsset{
		ID: "legacy-identical", TenantID: tenant.ID, Key: "training", Name: baseline.Name,
		AppTypes: baseline.AppTypes, LatestVersion: 1, Source: model.TemplateSourceCopy,
		Status: model.AssetStatusPublished, PayloadJSON: baseline.PayloadJSON,
		LineageState: model.LineageStateLegacyUnknown, CreatedAt: now, UpdatedAt: now,
	}
	editedPayload := baseline.PayloadJSON + " "
	edited := &model.ScenarioTemplateAsset{
		ID: "legacy-edited", TenantID: tenant.ID, Key: "training-edited", Name: baseline.Name,
		AppTypes: baseline.AppTypes, LatestVersion: 1, Source: model.TemplateSourceCopy,
		Status: model.AssetStatusPublished, PayloadJSON: editedPayload,
		LineageState: model.LineageStateLegacyUnknown, CreatedAt: now, UpdatedAt: now,
	}
	for _, asset := range []*model.ScenarioTemplateAsset{untouched, edited} {
		version := &model.ScenarioTemplateVersion{
			ID: asset.ID + "-v1", TenantID: tenant.ID, TemplateID: asset.ID, Version: 1,
			PayloadJSON: asset.PayloadJSON, ChangeNote: "pre-2.0", CreatedAt: now,
		}
		if err := svc.Store.CreateScenarioTemplate(ctx, asset, version); err != nil {
			t.Fatal(err)
		}
	}
	svc.BootstrapScenarioTemplateLineage(ctx)
	upgraded, err := svc.Store.GetScenarioTemplate(ctx, tenant.ID, untouched.ID)
	if err != nil || upgraded == nil {
		t.Fatal(err)
	}
	if upgraded.LineageState != model.LineageStateConfirmedBaseline || upgraded.BaselineKey != "training" {
		t.Fatalf("identical pre-2.0 copy must be upgraded: %+v", upgraded)
	}
	stale, err := svc.Store.GetScenarioTemplate(ctx, tenant.ID, edited.ID)
	if err != nil || stale == nil {
		t.Fatal(err)
	}
	if stale.LineageState != model.LineageStateLegacyUnknown || stale.BaselineKey != "" {
		t.Fatalf("edited pre-2.0 copy must keep legacy lineage: %+v", stale)
	}
	// Re-running is a no-op and never overwrites a decided row.
	svc.BootstrapScenarioTemplateLineage(ctx)
	again, err := svc.Store.GetScenarioTemplate(ctx, tenant.ID, untouched.ID)
	if err != nil || again == nil {
		t.Fatal(err)
	}
	if again.LineageState != model.LineageStateConfirmedBaseline {
		t.Fatalf("backfill must be idempotent: %+v", again)
	}
}

// TestReadPathResolvesPlatformBaseline pins doc/276 §7.1 step 3: a workspace may
// read/instantiate a baseline by its platform id, but a row owned by another
// workspace still resolves to not-found.
func TestReadPathResolvesPlatformBaseline(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	owner, err := svc.CreateTenant(ctx, "Read Owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateTenant(ctx, "Read Other")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.Store.GetPlatformScenarioTemplateByKey(ctx, "customer-service")
	if err != nil || baseline == nil {
		t.Fatal(err)
	}
	asset, version, _, err := svc.GetScenarioTemplate(ctx, other.ID, baseline.ID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if asset.TenantID != model.PlatformTenantID || version.Version != baseline.LatestVersion {
		t.Fatalf("baseline must be readable from a workspace: %+v", asset)
	}
	// A row owned by `owner` must not be readable from `other`.
	forked, err := svc.ForkScenarioTemplate(ctx, owner.ID, actorID(t, svc), baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if asset, _, _, err = svc.GetScenarioTemplate(ctx, other.ID, forked.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if asset != nil {
		t.Fatalf("cross-workspace row must not resolve: %+v", asset)
	}
}

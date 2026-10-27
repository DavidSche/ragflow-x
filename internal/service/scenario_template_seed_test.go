package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newScenarioSeedService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "seed.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "seed-secret")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

// TestCreateTenantSeedsBuiltinScenarioTemplates covers the tenant-creation
// trigger of doc/228 §3.1 T7.0: a fresh workspace must be able to open the
// wizard without any admin import step.
func TestCreateTenantSeedsBuiltinScenarioTemplates(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Seed Tenant")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(builtinScenarioTemplates) {
		t.Fatalf("expected %d seeded templates, got %d", len(builtinScenarioTemplates), len(items))
	}
	for _, item := range items {
		if item.Source != "builtin" || item.Status != "published" || item.LatestVersion != 1 {
			t.Fatalf("unexpected seeded template shape: %+v", item)
		}
		if item.PromptPresetID == "" || item.ParameterProfileID == "" {
			t.Fatalf("seeded template must carry preset/profile ids: %+v", item)
		}
		if len(item.SampleQuestions) == 0 || len(item.EvaluationQuestions) == 0 {
			t.Fatalf("seeded template must carry sample and evaluation questions: %+v", item.Key)
		}
	}
}

// TestEnsureBuiltinScenarioTemplatesIsIdempotent replays the bootstrap twice:
// no duplicate assets, no version churn.
func TestEnsureBuiltinScenarioTemplatesIsIdempotent(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Idempotent Tenant")
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.EnsureBuiltinScenarioTemplates(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("replay must create nothing, created=%d", created)
	}
	assets, total, err := svc.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 100, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len(builtinScenarioTemplates)) {
		t.Fatalf("unexpected template count after replay: %d", total)
	}
	for _, asset := range assets {
		versions, err := svc.Store.ListScenarioTemplateVersions(ctx, tenant.ID, asset.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(versions) != 1 {
			t.Fatalf("bootstrap must not append versions: key=%s versions=%d", asset.Key, len(versions))
		}
	}
}

// TestEnsureBuiltinScenarioTemplatesKeepsTenantCustomization asserts seeding
// never overwrites a tenant edit that reuses a builtin key.
func TestEnsureBuiltinScenarioTemplatesKeepsTenantCustomization(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Custom Tenant")
	if err != nil {
		t.Fatal(err)
	}
	assets, _, err := svc.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 100, repository.GovernanceFilter{Key: "finance"})
	if err != nil || len(assets) != 1 {
		t.Fatalf("finance template missing: %v", err)
	}
	if _, err := svc.UpdateScenarioTemplate(ctx, tenant.ID, "owner", assets[0].ID, ScenarioTemplateInput{
		Name:        "财务自定义",
		AppTypes:    []string{"chat"},
		Description: "tenant owned",
		Payload: ScenarioTemplatePayload{
			PromptPresetID: "finance", ParameterProfileID: "safe",
		},
		ChangeNote: "tenant edit",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureBuiltinScenarioTemplates(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := svc.Store.GetScenarioTemplate(ctx, tenant.ID, assets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "财务自定义" || loaded.LatestVersion != 2 {
		t.Fatalf("seed must not overwrite tenant customization: %+v", loaded)
	}
}

// TestBuiltinScenarioContentIsComplete enforces the content contract: every
// template ships valid preset/profile ids and a full English translation so
// the wizard has a single source of truth for both locales.
func TestBuiltinScenarioContentIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, item := range builtinScenarioTemplates {
		if seen[item.Key] {
			t.Fatalf("duplicate builtin template key: %s", item.Key)
		}
		seen[item.Key] = true
		if !templateKeyPattern.MatchString(item.Key) {
			t.Fatalf("invalid template key: %s", item.Key)
		}
		if _, err := validAppTypes(item.AppTypes); err != nil {
			t.Fatalf("invalid app types for %s: %v", item.Key, err)
		}
		if _, ok := scenarioPromptPresets[item.Payload.PromptPresetID]; !ok {
			t.Fatalf("unknown prompt preset %q for %s", item.Payload.PromptPresetID, item.Key)
		}
		if _, ok := scenarioParameterProfiles[item.Payload.ParameterProfileID]; !ok {
			t.Fatalf("unknown parameter profile %q for %s", item.Payload.ParameterProfileID, item.Key)
		}
		en, ok := item.Payload.Translations["en"]
		if !ok {
			t.Fatalf("missing en translation for %s", item.Key)
		}
		if en.Name == "" || en.Description == "" || en.ExpectedOutcome == "" || en.PermissionAdvice == "" {
			t.Fatalf("incomplete en copy for %s", item.Key)
		}
		if len(en.SampleQuestions) == 0 || len(en.EvaluationQuestions) == 0 ||
			len(en.DatasetSuggestions) == 0 || len(en.PublishChecklist) == 0 {
			t.Fatalf("incomplete en lists for %s", item.Key)
		}
		if containsCJK(en.Name + en.Description + en.ExpectedOutcome + en.PermissionAdvice) {
			t.Fatalf("en copy for %s must not contain CJK characters", item.Key)
		}
	}
}

// TestScenarioTemplateCatalogIsReadOnly asserts the catalog GET never mutates
// state: bootstrap happens on tenant creation/startup only (doc/228 §4.4).
func TestScenarioTemplateCatalogIsReadOnly(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Read Only Tenant")
	if err != nil {
		t.Fatal(err)
	}
	before, totalBefore, err := svc.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 100, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	_ = before
	for i := 0; i < 3; i++ {
		if _, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, totalAfter, err := svc.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 100, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if totalBefore != totalAfter {
		t.Fatalf("catalog read must not write: before=%d after=%d", totalBefore, totalAfter)
	}
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

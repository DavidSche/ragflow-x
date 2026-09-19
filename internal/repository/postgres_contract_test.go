package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newPostgresContractStore(t *testing.T) Store {
	t.Helper()
	baseDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL repository contract")
	}

	databaseName := strings.ToLower("rgx_repo_contract_" + strings.ReplaceAll(id.New(), "-", ""))
	createDatabase(t, baseDSN, databaseName)
	contractDSN := replacePostgresDatabase(baseDSN, databaseName)
	gdb, err := gorm.Open(postgres.Open(contractDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open isolated postgres database: %v", err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("migrate isolated postgres database: %v", err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close isolated postgres store: %v", err)
		}
		dropDatabase(t, baseDSN, databaseName)
	})
	return store
}

func createDatabase(t *testing.T, adminDSN, databaseName string) {
	t.Helper()
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres admin database: %v", err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatalf("get postgres admin pool: %v", err)
	}
	if err := admin.Exec(fmt.Sprintf("CREATE DATABASE %s", databaseName)).Error; err != nil {
		_ = sqlDB.Close()
		t.Fatalf("create isolated postgres database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close postgres admin pool: %v", err)
	}
}

func dropDatabase(t *testing.T, adminDSN, databaseName string) {
	t.Helper()
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Errorf("open postgres admin database for cleanup: %v", err)
		return
	}
	sqlDB, err := admin.DB()
	if err != nil {
		t.Errorf("get postgres admin pool for cleanup: %v", err)
		return
	}
	if err := admin.Exec(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = ? AND pid <> pg_backend_pid()
	`, databaseName).Error; err != nil {
		t.Errorf("terminate postgres test connections: %v", err)
	}
	if err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", databaseName)).Error; err != nil {
		t.Errorf("drop isolated postgres database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Errorf("close postgres admin cleanup pool: %v", err)
	}
}

func replacePostgresDatabase(dsn, databaseName string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Path = "/" + databaseName
		return parsed.String()
	}
	fields := strings.Fields(dsn)
	replaced := false
	for index, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "dbname=") {
			fields[index] = "dbname=" + databaseName
			replaced = true
		}
	}
	if !replaced {
		fields = append(fields, "dbname="+databaseName)
	}
	return strings.Join(fields, " ")
}

func mustCreatePostgresTenant(t *testing.T, store Store, name string) *model.Tenant {
	t.Helper()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tenant := &model.Tenant{ID: "pg-" + id.New()[:24], Name: name, Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("create tenant %s: %v", name, err)
	}
	return tenant
}

func mustCreatePostgresProject(t *testing.T, store Store, tenant *model.Tenant, idValue, name, description string) *model.Project {
	t.Helper()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	project := &model.Project{ID: idValue, TenantID: tenant.ID, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateProject(context.Background(), project); err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	return project
}

func mustCreatePostgresTeam(t *testing.T, store Store, tenant *model.Tenant, idValue, name, ownerID string) *model.Team {
	t.Helper()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	team := &model.Team{ID: idValue, TenantID: tenant.ID, Name: name, OwnerID: ownerID, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateTeam(context.Background(), team); err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
	return team
}

func postgresCount(t *testing.T, testStore Store, query string, args ...interface{}) int64 {
	t.Helper()
	var count int64
	if err := testStore.(*store).DB.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count postgres rows: %v", err)
	}
	return count
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_PostgresProjectRepositoryIsTenantScoped(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()

	tenantA := mustCreatePostgresTenant(t, store, "PG Repository Tenant A")
	tenantB := mustCreatePostgresTenant(t, store, "PG Repository Tenant B")
	projectA := mustCreatePostgresProject(t, store, tenantA, "pg-project-a", "PG Project A", "tenant scoped")
	projectB := mustCreatePostgresProject(t, store, tenantB, "pg-project-b", "PG Project B", "other tenant")

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	member := &model.User{
		ID: "pg-user-a", TenantID: tenantA.ID, Username: "pg-project-member-a",
		PasswordHash: "test-hash", Role: model.RoleOperator, Status: model.UserStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateUser(ctx, member); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProjectMember(ctx, member.ID, projectA.ID); err != nil {
		t.Fatal(err)
	}

	projectsA, err := store.ListProjects(ctx, tenantA.ID)
	if err != nil || len(projectsA) != 1 || projectsA[0].ID != projectA.ID {
		t.Fatalf("tenant A projects: %+v err=%v", projectsA, err)
	}
	projectsB, err := store.ListProjects(ctx, tenantB.ID)
	if err != nil || len(projectsB) != 1 || projectsB[0].ID != projectB.ID {
		t.Fatalf("tenant B projects: %+v err=%v", projectsB, err)
	}
	if got, err := store.GetProject(ctx, tenantB.ID, projectA.ID); err != nil || got != nil {
		t.Fatalf("cross-tenant project get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetProjectByName(ctx, tenantB.ID, "PG PROJECT A", ""); err != nil || got != nil {
		t.Fatalf("cross-tenant project name get: got=%+v err=%v", got, err)
	}
	memberProjectsB, err := store.ListProjectsByMember(ctx, tenantB.ID, member.ID)
	if err != nil || len(memberProjectsB) != 0 {
		t.Fatalf("cross-tenant membership projects: %+v err=%v", memberProjectsB, err)
	}
	memberProjectsA, err := store.ListProjectsByMember(ctx, tenantA.ID, member.ID)
	if err != nil || len(memberProjectsA) != 1 || memberProjectsA[0].ID != projectA.ID {
		t.Fatalf("tenant membership projects: %+v err=%v", memberProjectsA, err)
	}

	hijack := &model.Project{ID: projectA.ID, TenantID: tenantB.ID, Name: "Hijacked", Description: "hijacked", UpdatedAt: now}
	if err := store.UpdateProject(ctx, hijack); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetProject(ctx, tenantA.ID, projectA.ID); got == nil || got.Name != projectA.Name || got.Description != projectA.Description {
		t.Fatalf("cross-tenant update leaked: %+v", got)
	}
	if err := store.DeleteProject(ctx, tenantB.ID, projectA.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetProject(ctx, tenantA.ID, projectA.ID); got == nil {
		t.Fatal("cross-tenant delete leaked")
	}

	if err := store.CreateProject(ctx, &model.Project{ID: "pg-project-duplicate", TenantID: tenantA.ID, Name: "pg project a", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("expected case-insensitive duplicate project to fail")
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_001_PostgresTeamProjectBindingIsTransactional(t *testing.T) {
	testStore := newPostgresContractStore(t)
	ctx := context.Background()

	tenantA := mustCreatePostgresTenant(t, testStore, "PG Binding Tenant A")
	tenantB := mustCreatePostgresTenant(t, testStore, "PG Binding Tenant B")
	mustCreatePostgresProject(t, testStore, tenantA, "pg-bind-project-a", "PG Binding Project A", "valid")
	mustCreatePostgresProject(t, testStore, tenantB, "pg-bind-project-b", "PG Binding Project B", "foreign")
	teamA := mustCreatePostgresTeam(t, testStore, tenantA, "pg-bind-team-a", "PG Binding Team A", "owner-a")
	teamB := mustCreatePostgresTeam(t, testStore, tenantB, "pg-bind-team-b", "PG Binding Team B", "owner-b")

	if err := testStore.SetTeamProjects(ctx, tenantA.ID, teamA.ID, []string{"pg-bind-project-a"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetTeamProjects(ctx, tenantA.ID, teamA.ID, []string{"pg-bind-project-b"}); err == nil {
		t.Fatal("expected foreign project binding to fail")
	}
	if count := postgresCount(t, testStore, `SELECT COUNT(*) FROM rgx_team_project WHERE team_id = ?`, teamA.ID); count != 1 {
		t.Fatalf("foreign project transaction changed bindings: %d", count)
	}

	if err := testStore.SetProjectTeams(ctx, tenantA.ID, "pg-bind-project-a", []string{teamA.ID}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetProjectTeams(ctx, tenantA.ID, "pg-bind-project-a", []string{teamB.ID}); err == nil {
		t.Fatal("expected foreign team binding to fail")
	}
	if count := postgresCount(t, testStore, `SELECT COUNT(*) FROM rgx_team_project WHERE project_id = ?`, "pg-bind-project-a"); count != 1 {
		t.Fatalf("foreign team transaction changed bindings: %d", count)
	}

	if err := testStore.(*store).DB.Create(&model.TeamProject{TeamID: teamA.ID, ProjectID: "pg-bind-project-a"}).Error; err == nil {
		t.Fatal("expected duplicate primary key to fail")
	}
	projects, err := testStore.ListTeamProjects(ctx, tenantA.ID, teamA.ID)
	if err != nil || len(projects) != 1 || projects[0].ID != "pg-bind-project-a" {
		t.Fatalf("team projects: %+v err=%v", projects, err)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_PostgresAuditAnchorStoreIsTenantScoped(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	anchorAt := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tenantA := mustCreatePostgresTenant(t, store, "PG Anchor Tenant A")
	tenantB := mustCreatePostgresTenant(t, store, "PG Anchor Tenant B")

	anchor := &model.AuditAnchor{TenantID: tenantA.ID, LastSeq: 12, LastHash: "hash-a", AnchorAt: anchorAt, CreatedAt: anchorAt}
	created, err := store.CreateAuditAnchor(ctx, anchor)
	if err != nil || !created || anchor.ID == "" || anchor.Algorithm != model.AuditAnchorAlgorithm {
		t.Fatalf("create anchor: created=%v anchor=%+v err=%v", created, anchor, err)
	}
	created, err = store.CreateAuditAnchor(ctx, &model.AuditAnchor{TenantID: tenantA.ID, LastSeq: 12, LastHash: "hash-a", AnchorAt: anchorAt, CreatedAt: anchorAt})
	if err != nil || created {
		t.Fatalf("duplicate anchor: created=%v err=%v", created, err)
	}
	if _, err := store.CreateAuditAnchor(ctx, &model.AuditAnchor{LastSeq: 13, LastHash: "hash-b", AnchorAt: anchorAt}); err == nil {
		t.Fatal("expected missing tenant to fail")
	}

	list, total, err := store.ListAuditAnchors(ctx, tenantA.ID, false, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].TenantID != tenantA.ID || list[0].LastSeq != 12 {
		t.Fatalf("tenant anchors: total=%d list=%+v err=%v", total, list, err)
	}
	if _, total, err := store.ListAuditAnchors(ctx, tenantB.ID, false, 1, 20); err != nil || total != 0 {
		t.Fatalf("foreign tenant anchors: total=%d err=%v", total, err)
	}
	all, err := store.ListAuditAnchorsAll(ctx, tenantA.ID, false)
	if err != nil || len(all) != 1 || all[0].TenantID != tenantA.ID {
		t.Fatalf("tenant anchor all: all=%+v err=%v", all, err)
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_002_PostgresModelProviderIdentityIsTenantScoped(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tenantA := mustCreatePostgresTenant(t, store, "PG Provider Tenant A")
	tenantB := mustCreatePostgresTenant(t, store, "PG Provider Tenant B")
	providerA := &model.ModelProvider{ID: "pg-provider-a", TenantID: tenantA.ID, ProviderType: "openai-api-compatible", Name: "Provider A", ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	providerB := &model.ModelProvider{ID: "pg-provider-b", TenantID: tenantB.ID, ProviderType: "openai-api-compatible", Name: "Provider B", ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProvider(ctx, providerA); err != nil {
		t.Fatalf("create provider A: %v", err)
	}
	if err := store.CreateModelProvider(ctx, providerB); err != nil {
		t.Fatalf("create provider B: %v", err)
	}
	instanceA := &model.ModelProviderInstance{ID: "pg-instance-a", TenantID: tenantA.ID, ProviderID: providerA.ID, InstanceName: "Production", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	instanceB := &model.ModelProviderInstance{ID: "pg-instance-b", TenantID: tenantB.ID, ProviderID: providerB.ID, InstanceName: "Production", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProviderInstance(ctx, instanceA); err != nil {
		t.Fatalf("create tenant A instance: %v", err)
	}
	if err := store.CreateModelProviderInstance(ctx, instanceB); err != nil {
		t.Fatalf("create tenant B instance: %v", err)
	}
	if err := store.CreateModelProviderInstance(ctx, &model.ModelProviderInstance{ID: "pg-instance-duplicate", TenantID: tenantA.ID, ProviderID: providerA.ID, InstanceName: " production ", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("expected whitespace-insensitive duplicate instance to fail")
	}
	instanceUpdate := *instanceA
	instanceUpdate.TenantID = tenantB.ID
	instanceUpdate.BaseURL = "https://tenant-b.example.com/v1"
	if err := store.UpdateModelProviderInstance(ctx, &instanceUpdate); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetModelProviderInstance(ctx, tenantA.ID, providerA.ID, instanceA.ID); err != nil || got == nil || got.BaseURL == instanceUpdate.BaseURL {
		t.Fatalf("cross-tenant instance update leaked: got=%+v err=%v", got, err)
	}
	if err := store.DeleteModelProviderInstances(ctx, tenantB.ID, providerB.ID, []string{instanceA.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetModelProviderInstance(ctx, tenantA.ID, providerA.ID, instanceA.ID); got == nil {
		t.Fatal("cross-tenant instance delete leaked")
	}

	modelA := &model.ModelProviderModel{ID: "pg-model-a", TenantID: tenantA.ID, ProviderID: providerA.ID, InstanceID: instanceA.ID, ModelName: "GPT-4o", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	modelB := &model.ModelProviderModel{ID: "pg-model-b", TenantID: tenantB.ID, ProviderID: providerB.ID, InstanceID: instanceB.ID, ModelName: "GPT-4o", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProviderModel(ctx, modelA); err != nil {
		t.Fatalf("create tenant A model: %v", err)
	}
	if err := store.CreateModelProviderModel(ctx, modelB); err != nil {
		t.Fatalf("create tenant B model: %v", err)
	}
	if err := store.CreateModelProviderModel(ctx, &model.ModelProviderModel{ID: "pg-model-duplicate", TenantID: tenantA.ID, ProviderID: providerA.ID, InstanceID: instanceA.ID, ModelName: " gpt-4o ", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("expected whitespace-insensitive duplicate model to fail")
	}
	modelUpdate := *modelA
	modelUpdate.TenantID = tenantB.ID
	modelUpdate.Status = model.ModelStatusInactive
	if err := store.UpdateModelProviderModel(ctx, &modelUpdate); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetModelProviderModel(ctx, tenantA.ID, instanceA.ID, modelA.ID); err != nil || got == nil || got.Status == model.ModelStatusInactive {
		t.Fatalf("cross-tenant model update leaked: got=%+v err=%v", got, err)
	}
	if err := store.DeleteModelProviderModels(ctx, tenantB.ID, instanceB.ID, []string{modelA.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetModelProviderModel(ctx, tenantA.ID, instanceA.ID, modelA.ID); got == nil {
		t.Fatal("cross-tenant model delete leaked")
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_003_PostgresModelProviderWritesAreAtomic(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tenant := mustCreatePostgresTenant(t, store, "PG Provider Atomic Tenant")
	provider := &model.ModelProvider{ID: "pg-provider-atomic", TenantID: tenant.ID, ProviderType: "openai-api-compatible", Name: "Provider Atomic", ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	instance := &model.ModelProviderInstance{ID: "pg-atomic-instance", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Atomic", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	models := []*model.ModelProviderModel{
		{ID: "pg-atomic-model-1", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instance.ID, ModelName: "gpt-4o", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: "pg-atomic-model-2", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instance.ID, ModelName: "GPT-4O", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
	}
	if err := store.CreateModelProviderInstanceWithModels(ctx, instance, models); err == nil {
		t.Fatal("expected duplicate model transaction to fail")
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_model_provider_instance WHERE id = ?`, instance.ID); count != 0 {
		t.Fatalf("failed create transaction leaked instance rows: %d", count)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_model_provider_model WHERE instance_id = ?`, instance.ID); count != 0 {
		t.Fatalf("failed create transaction leaked model rows: %d", count)
	}

	validModel := &model.ModelProviderModel{ID: "pg-atomic-model-3", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instance.ID, ModelName: "gpt-4o", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProviderInstanceWithModels(ctx, instance, []*model.ModelProviderModel{validModel}); err != nil {
		t.Fatalf("create atomic provider instance: %v", err)
	}
	changed := *instance
	changed.BaseURL = "https://updated.example.com/v1"
	updatedModel := *validModel
	updatedModel.MaxTokens = 128000
	duplicate := &model.ModelProviderModel{ID: "pg-atomic-model-4", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instance.ID, ModelName: " GPT-4O ", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.UpdateModelProviderInstanceWithModels(ctx, provider, &changed, []*model.ModelProviderModel{&updatedModel, duplicate}, nil); err == nil {
		t.Fatal("expected duplicate model update transaction to fail")
	}
	storedInstance, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, instance.ID)
	if err != nil || storedInstance == nil || storedInstance.BaseURL == changed.BaseURL {
		t.Fatalf("failed update transaction leaked instance changes: %+v err=%v", storedInstance, err)
	}
	storedModel, err := store.GetModelProviderModel(ctx, tenant.ID, instance.ID, validModel.ID)
	if err != nil || storedModel == nil || storedModel.MaxTokens != 8192 {
		t.Fatalf("failed update transaction leaked model changes: %+v err=%v", storedModel, err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_model_provider_model WHERE instance_id = ?`, instance.ID); count != 1 {
		t.Fatalf("failed update transaction model count = %d, want 1", count)
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_004_PostgresModelProviderDeleteAndRestoreAreAtomic(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tenant := mustCreatePostgresTenant(t, store, "PG Provider Delete Tenant")
	provider := &model.ModelProvider{ID: "pg-provider-delete", TenantID: tenant.ID, ProviderType: "openai-api-compatible", Name: "Provider Delete", BaseURL: "https://first.example.com/v1", APIKeyEnc: "first-key", ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	first := &model.ModelProviderInstance{ID: "pg-delete-first", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "First", BaseURL: "https://first.example.com/v1", APIKeyEnc: "first-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	second := &model.ModelProviderInstance{ID: "pg-delete-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Second", BaseURL: "https://second.example.com/v1", APIKeyEnc: "second-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	if err := store.CreateModelProviderInstance(ctx, first); err != nil {
		t.Fatalf("create first instance: %v", err)
	}
	if err := store.CreateModelProviderInstance(ctx, second); err != nil {
		t.Fatalf("create second instance: %v", err)
	}
	firstModel := &model.ModelProviderModel{ID: "pg-delete-model-first", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: first.ID, ModelName: "gpt-first", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	secondModel := &model.ModelProviderModel{ID: "pg-delete-model-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: second.ID, ModelName: "gpt-second", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProviderModel(ctx, firstModel); err != nil {
		t.Fatalf("create first model: %v", err)
	}
	if err := store.CreateModelProviderModel(ctx, secondModel); err != nil {
		t.Fatalf("create second model: %v", err)
	}
	providerSnapshot := *provider

	if err := store.DeleteModelProviderInstancesWithModels(ctx, provider, []string{first.ID}); err != nil {
		t.Fatalf("atomic delete: %v", err)
	}
	if got, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, first.ID); err != nil || got != nil {
		t.Fatalf("deleted instance remains: got=%+v err=%v", got, err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_model_provider_model WHERE instance_id = ?`, first.ID); count != 0 {
		t.Fatalf("deleted model count = %d, want 0", count)
	}
	updatedProvider, err := store.GetModelProvider(ctx, tenant.ID, provider.ID)
	if err != nil || updatedProvider == nil || updatedProvider.BaseURL != second.BaseURL {
		t.Fatalf("provider default not repointed: %+v err=%v", updatedProvider, err)
	}

	if err := store.RestoreModelProviderInstancesWithModels(ctx, &providerSnapshot, []*model.ModelProviderInstance{first}, []*model.ModelProviderModel{firstModel}); err != nil {
		t.Fatalf("atomic restore: %v", err)
	}
	if got, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, first.ID); err != nil || got == nil {
		t.Fatalf("restored instance missing: got=%+v err=%v", got, err)
	}
	restoredModel, err := store.GetModelProviderModel(ctx, tenant.ID, first.ID, firstModel.ID)
	if err != nil || restoredModel == nil || restoredModel.ModelName != "gpt-first" {
		t.Fatalf("restored model missing: %+v err=%v", restoredModel, err)
	}
	restoredProvider, err := store.GetModelProvider(ctx, tenant.ID, provider.ID)
	if err != nil || restoredProvider == nil || restoredProvider.BaseURL != second.BaseURL || restoredProvider.APIKeyEnc != second.APIKeyEnc {
		t.Fatalf("provider default not recomputed: %+v err=%v", restoredProvider, err)
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_005_PostgresProviderRestoreRecomputesDefaultAfterPartialBatch(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	tenant := mustCreatePostgresTenant(t, store, "PG Provider Partial Restore Tenant")
	provider := &model.ModelProvider{ID: "pg-provider-partial", TenantID: tenant.ID, ProviderType: "openai-api-compatible", Name: "Provider Partial", BaseURL: "https://first.example.com/v1", APIKeyEnc: "first-key", ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	instances := []*model.ModelProviderInstance{
		{ID: "pg-partial-first", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "First", BaseURL: "https://first.example.com/v1", APIKeyEnc: "first-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: "pg-partial-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Second", BaseURL: "https://second.example.com/v1", APIKeyEnc: "second-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "pg-partial-third", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Third", BaseURL: "https://third.example.com/v1", APIKeyEnc: "third-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	models := []*model.ModelProviderModel{
		{ID: "pg-partial-model-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instances[1].ID, ModelName: "gpt-second", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: "pg-partial-model-third", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instances[2].ID, ModelName: "gpt-third", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
	}
	for _, instance := range instances {
		if err := store.CreateModelProviderInstance(ctx, instance); err != nil {
			t.Fatalf("create instance %s: %v", instance.ID, err)
		}
	}
	for _, providerModel := range models {
		if err := store.CreateModelProviderModel(ctx, providerModel); err != nil {
			t.Fatalf("create model %s: %v", providerModel.ID, err)
		}
	}

	if err := store.DeleteModelProviderInstancesWithModels(ctx, provider, []string{instances[0].ID, instances[1].ID, instances[2].ID}); err != nil {
		t.Fatalf("atomic delete batch: %v", err)
	}
	concurrent := &model.ModelProviderInstance{ID: "pg-partial-concurrent", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Concurrent", BaseURL: "https://concurrent.example.com/v1", APIKeyEnc: "concurrent-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second)}
	if err := store.CreateModelProviderInstanceDefault(ctx, concurrent); err != nil {
		t.Fatalf("create concurrent instance: %v", err)
	}

	providerSnapshot := *provider
	if err := store.RestoreModelProviderInstancesWithModels(ctx, &providerSnapshot, []*model.ModelProviderInstance{instances[1], instances[2]}, models); err != nil {
		t.Fatalf("atomic restore failed batch items: %v", err)
	}
	for _, instance := range []*model.ModelProviderInstance{instances[1], instances[2]} {
		got, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, instance.ID)
		if err != nil || got == nil {
			t.Fatalf("restored instance %s missing: got=%+v err=%v", instance.ID, got, err)
		}
	}
	if got, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, instances[0].ID); err != nil || got != nil {
		t.Fatalf("successful batch item was restored: got=%+v err=%v", got, err)
	}
	restoredProvider, err := store.GetModelProvider(ctx, tenant.ID, provider.ID)
	if err != nil || restoredProvider == nil || restoredProvider.BaseURL != concurrent.BaseURL || restoredProvider.APIKeyEnc != concurrent.APIKeyEnc {
		t.Fatalf("provider default not recomputed after concurrent change: %+v err=%v", restoredProvider, err)
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_009_PostgresProviderDeleteAndRestoreSerializeDefaultCredentials(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	tenant := mustCreatePostgresTenant(t, store, "PG Provider Concurrency Tenant")
	provider := &model.ModelProvider{
		ID: "pg-provider-concurrent", TenantID: tenant.ID, ProviderType: "openai-api-compatible",
		Name: "Provider Concurrent", BaseURL: "https://third.example.com/v1", APIKeyEnc: "third-key",
		ModelsJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instances := []*model.ModelProviderInstance{
		{ID: "pg-concurrent-first", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "First", BaseURL: "https://first.example.com/v1", APIKeyEnc: "first-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: "pg-concurrent-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Second", BaseURL: "https://second.example.com/v1", APIKeyEnc: "second-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "pg-concurrent-third", TenantID: tenant.ID, ProviderID: provider.ID, InstanceName: "Third", BaseURL: "https://third.example.com/v1", APIKeyEnc: "third-key", Status: model.ProviderStatusActive, ExtraJSON: "{}", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	models := []*model.ModelProviderModel{
		{ID: "pg-concurrent-model-first", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instances[0].ID, ModelName: "gpt-first", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: "pg-concurrent-model-second", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instances[1].ID, ModelName: "gpt-second", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "pg-concurrent-model-third", TenantID: tenant.ID, ProviderID: provider.ID, InstanceID: instances[2].ID, ModelName: "gpt-third", ModelType: model.ModelTypeChat, Status: model.ModelStatusActive, MaxTokens: 8192, ExtraJSON: "{}", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	for index, instance := range instances {
		if err := store.CreateModelProviderInstance(ctx, instance); err != nil {
			t.Fatalf("create instance %d: %v", index, err)
		}
		if err := store.CreateModelProviderModel(ctx, models[index]); err != nil {
			t.Fatalf("create model %d: %v", index, err)
		}
	}

	providerSnapshot := *provider
	secondSnapshot := *instances[1]
	secondModel := *models[1]
	thirdSnapshot := *instances[2]
	if err := store.DeleteModelProviderInstancesWithModels(ctx, provider, []string{instances[0].ID, instances[1].ID}); err != nil {
		t.Fatalf("delete first and second instances: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = store.DeleteModelProviderInstancesWithModels(ctx, provider, []string{thirdSnapshot.ID})
	}()
	go func() {
		defer wg.Done()
		errs[1] = store.RestoreModelProviderInstancesWithModels(ctx, &providerSnapshot, []*model.ModelProviderInstance{&secondSnapshot}, []*model.ModelProviderModel{&secondModel})
	}()
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent delete/restore: %v", err)
	}

	remaining, err := store.ListModelProviderInstances(ctx, tenant.ID, provider.ID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("remaining instances=%+v err=%v", remaining, err)
	}
	if remaining[0].ID != secondSnapshot.ID || remaining[0].BaseURL != secondSnapshot.BaseURL || remaining[0].APIKeyEnc != secondSnapshot.APIKeyEnc {
		t.Fatalf("remaining instance=%+v", remaining[0])
	}
	updated, err := store.GetModelProvider(ctx, tenant.ID, provider.ID)
	if err != nil || updated == nil || updated.BaseURL != secondSnapshot.BaseURL || updated.APIKeyEnc != secondSnapshot.APIKeyEnc {
		t.Fatalf("provider default after race=%+v err=%v", updated, err)
	}
	if got, err := store.GetModelProviderInstance(ctx, tenant.ID, provider.ID, thirdSnapshot.ID); err != nil || got != nil {
		t.Fatalf("third instance after race=%+v err=%v", got, err)
	}
	if got, err := store.GetModelProviderModel(ctx, tenant.ID, secondSnapshot.ID, secondModel.ID); err != nil || got == nil || got.ModelName != secondModel.ModelName {
		t.Fatalf("restored model=%+v err=%v", got, err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_model_provider_model WHERE instance_id = ?`, thirdSnapshot.ID); count != 0 {
		t.Fatalf("third model count=%d, want 0", count)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_PG_006_PostgresAlertEventsAreIdempotentAndTenantScoped(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	tenantA := mustCreatePostgresTenant(t, store, "PG Alert Tenant A")
	tenantB := mustCreatePostgresTenant(t, store, "PG Alert Tenant B")
	event := &model.AlertEvent{
		ID: "pg-alert-idempotent", TenantID: tenantA.ID, Title: "Provider compensation failed",
		Severity: "error", Type: "provider.compensation.failed", Resource: "model-provider",
		ResourceID: "provider-1", Detail: "manual reconciliation required", FieldsJSON: `{"operation":"delete"}`,
		Fingerprint: "fingerprint", OccurredAt: now, Status: model.AlertStatusOpen,
	}
	if err := store.CreateAlertEvent(ctx, event); err != nil {
		t.Fatalf("create alert: %v", err)
	}
	duplicate := *event
	duplicate.Title = "duplicate should be ignored"
	duplicate.Detail = "different payload"
	if err := store.CreateAlertEvent(ctx, &duplicate); err != nil {
		t.Fatalf("create duplicate alert: %v", err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_alert_event WHERE id = ?`, event.ID); count != 1 {
		t.Fatalf("alert event count = %d, want 1", count)
	}
	got, err := store.GetAlertEvent(ctx, tenantA.ID, event.ID, false)
	if err != nil || got == nil || got.Title != event.Title || got.Detail != event.Detail {
		t.Fatalf("original alert changed or missing: got=%+v err=%v", got, err)
	}
	if other, err := store.GetAlertEvent(ctx, tenantB.ID, event.ID, false); err != nil || other != nil {
		t.Fatalf("cross-tenant alert read leaked: got=%+v err=%v", other, err)
	}
	items, total, err := store.ListAlertEvents(ctx, tenantB.ID, false, 1, 20, AlertFilter{Type: event.Type})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("cross-tenant alert list: items=%d total=%d err=%v", len(items), total, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_PG_007_PostgresAlertDeliveryLifecycleIsRestartableAndTenantScoped(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	tenantA := mustCreatePostgresTenant(t, store, "PG Alert Delivery Tenant A")
	tenantB := mustCreatePostgresTenant(t, store, "PG Alert Delivery Tenant B")
	delivery := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery", Channel: "primary", TenantID: tenantA.ID,
		Status: model.AlertDeliveryStatusPending, Attempts: 0, LastAttemptAt: now,
		NextRetryAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	retryAt := now.Add(time.Minute)
	failed := *delivery
	failed.Status = model.AlertDeliveryStatusFailed
	failed.Attempts = 3
	failed.LastError = "webhook returned 503"
	failed.LastAttemptAt = retryAt
	failed.NextRetryAt = &retryAt
	if err := store.UpsertAlertDelivery(ctx, &failed); err != nil {
		t.Fatalf("upsert failed delivery: %v", err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_alert_delivery WHERE alert_event_id = ?`, failed.AlertEventID); count != 1 {
		t.Fatalf("alert delivery count = %d, want 1", count)
	}

	items, total, err := store.ListAlertDeliveries(ctx, tenantB.ID, false, 1, 20, AlertDeliveryFilter{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("cross-tenant delivery list: items=%d total=%d err=%v", len(items), total, err)
	}
	compensable, err := store.ListCompensableAlertDeliveries(ctx, retryAt.Add(time.Second), 10)
	if err != nil || len(compensable) != 1 || compensable[0].AlertEventID != failed.AlertEventID || compensable[0].Attempts != 3 {
		t.Fatalf("compensable delivery: items=%+v err=%v", compensable, err)
	}
	stalePendingAt := retryAt.Add(-2 * time.Minute)
	stalePending := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery-stale", Channel: "primary", TenantID: tenantA.ID,
		Status: model.AlertDeliveryStatusPending, Attempts: 0, LastAttemptAt: stalePendingAt,
		NextRetryAt: &stalePendingAt, CreatedAt: stalePendingAt, UpdatedAt: stalePendingAt,
	}
	if err := store.UpsertAlertDelivery(ctx, stalePending); err != nil {
		t.Fatalf("upsert stale pending delivery: %v", err)
	}
	compensable, err = store.ListCompensableAlertDeliveries(ctx, retryAt, 10)
	if err != nil || len(compensable) != 2 {
		t.Fatalf("stale pending delivery: items=%+v err=%v", compensable, err)
	}

	succeededAt := retryAt.Add(time.Minute)
	succeeded := failed
	succeeded.Status = model.AlertDeliveryStatusSucceeded
	succeeded.Attempts = 1
	succeeded.LastError = ""
	succeeded.LastAttemptAt = succeededAt
	succeeded.NextRetryAt = nil
	succeeded.DeliveredAt = &succeededAt
	if err := store.UpsertAlertDelivery(ctx, &succeeded); err != nil {
		t.Fatalf("upsert succeeded delivery: %v", err)
	}
	compensable, err = store.ListCompensableAlertDeliveries(ctx, succeededAt, 10)
	if err != nil || len(compensable) != 1 || compensable[0].AlertEventID != stalePending.AlertEventID {
		t.Fatalf("only stale pending should remain compensable: items=%+v err=%v", compensable, err)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_alert_delivery WHERE alert_event_id = ? AND attempts = ?`, failed.AlertEventID, 4); count != 1 {
		t.Fatalf("cumulative delivery attempts = %d, want 4", count)
	}
	stalePending.Status = model.AlertDeliveryStatusSucceeded
	stalePending.Attempts = 1
	stalePending.LastAttemptAt = succeededAt
	stalePending.NextRetryAt = nil
	stalePending.DeliveredAt = &succeededAt
	if err := store.UpsertAlertDelivery(ctx, stalePending); err != nil {
		t.Fatalf("resolve stale pending delivery: %v", err)
	}
	compensable, err = store.ListCompensableAlertDeliveries(ctx, succeededAt, 10)
	if err != nil || len(compensable) != 0 {
		t.Fatalf("resolved deliveries remain compensable: items=%+v err=%v", compensable, err)
	}
}

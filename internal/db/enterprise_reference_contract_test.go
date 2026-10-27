package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestEnterpriseConnectionReferenceContractSQLite(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "enterprise-contracts.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	workspace := &model.Tenant{ID: "workspace", Name: "Workspace", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(workspace).Error; err != nil {
		t.Fatal(err)
	}
	connection := &model.EnterpriseConnection{
		ID: "connection", OwnerTenantID: model.PlatformTenantID, ManagedBy: model.EnterpriseConnectionManagedByPlatform,
		Visibility: model.EnterpriseConnectionVisibilityShared, UsableBy: "AUTHORIZED_BINDINGS",
		CredentialScope: model.EnterpriseConnectionCredentialScopeConnection, CurrentConnectionVersion: 1,
		LifecycleStatus: model.EnterpriseLifecycleActive, RuntimeHealth: model.RuntimeHealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(connection).Error; err != nil {
		t.Fatal(err)
	}
	connectionVersion := &model.EnterpriseConnectionVersion{
		ConnectionID: connection.ID, Version: 1, ProviderName: "openai-api-compatible", DisplayName: "Private LLM",
		BaseURL: "https://llm.example.com", CredentialRef: "vault://llm", CredentialVersion: "v1",
		CredentialPolicyVersion: "v1", Visibility: connection.Visibility, UsableBy: connection.UsableBy,
		ManagedBy: connection.ManagedBy, CredentialScope: connection.CredentialScope,
		ConnectionConfigHash: "sha256:connection", CreatedAt: now,
	}
	if err := gdb.Create(connectionVersion).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.EnterpriseConnectionVersion{
		ConnectionID: "missing", Version: 1, ProviderName: "openai-api-compatible", DisplayName: "Orphan",
		BaseURL: "https://llm.example.com", CredentialRef: "vault://llm", CredentialVersion: "v1",
		CredentialPolicyVersion: "v1", Visibility: connection.Visibility, UsableBy: connection.UsableBy,
		ManagedBy: connection.ManagedBy, CredentialScope: connection.CredentialScope,
		ConnectionConfigHash: "sha256:orphan", CreatedAt: now,
	}).Error; err == nil {
		t.Fatal("expected orphan connection version to be rejected")
	}

	binding := &model.EnterpriseConnectionBinding{
		BindingID: "binding", ConnectionID: connection.ID, TenantID: workspace.ID,
		CurrentBindingVersion: 1, LifecycleStatus: model.EnterpriseLifecycleActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.EnterpriseConnectionBinding{
		BindingID: "orphan-binding", ConnectionID: "missing", TenantID: workspace.ID,
		CurrentBindingVersion: 1, LifecycleStatus: model.EnterpriseLifecycleActive,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err == nil {
		t.Fatal("expected orphan binding to be rejected")
	}
	bindingVersion := &model.EnterpriseConnectionBindingVersion{
		BindingID: binding.BindingID, Version: 1, ConnectionID: connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: `["chat"]`, AllowedModelRefs: `["gpt-4o-mini"]`,
		CreatedBy: "admin", CreatedAt: now,
	}
	if err := gdb.Create(bindingVersion).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.EnterpriseConnectionBindingVersion{
		BindingID: binding.BindingID, Version: 2, ConnectionID: connection.ID, TenantID: "missing-tenant",
		AllowedCapabilities: `["chat"]`, AllowedModelRefs: `["gpt-4o-mini"]`,
		CreatedBy: "admin", CreatedAt: now,
	}).Error; err == nil {
		t.Fatal("expected binding version tenant mismatch to be rejected")
	}

	route := &model.ModelRoute{ID: "route", TenantID: workspace.ID, Scenario: "chat", ModelAlias: "private", TargetModel: "gpt-4o-mini", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	pin := &model.ModelRouteEnterprisePin{
		PinID: "pin", TenantID: workspace.ID, RouteID: route.ID, Version: 1,
		ConnectionID: connection.ID, ConnectionVersion: 1, BindingID: binding.BindingID,
		BindingVersion: 1, ModelRef: "gpt-4o-mini", CreatedBy: "admin", CreatedAt: now,
	}
	if err := gdb.Create(pin).Error; err != nil {
		t.Fatal(err)
	}
	invalidPin := *pin
	invalidPin.PinID = "invalid-pin"
	invalidPin.BindingID = "missing-binding"
	if err := gdb.Create(&invalidPin).Error; err == nil {
		t.Fatal("expected pin with missing binding version to be rejected")
	}

	if err := gdb.Create(&model.ExecutionSnapshot{
		ID: "snapshot", TenantID: workspace.ID, ReleaseCandidateID: "candidate", CandidateVersion: 1,
		TargetType: "assistant", TargetID: route.ID, TargetVersion: "v1",
		SnapshotSchemaVersion: "v1", SnapshotHashAlgorithm: "SHA256", SnapshotHash: "sha256:snapshot",
		ExecutionConfig: "{}", ModelRoutePinID: pin.PinID, ModelRoutePinVersion: pin.Version,
		EnterpriseConnectionID: connection.ID, EnterpriseConnectionVersion: 1,
		EnterpriseBindingID: binding.BindingID, EnterpriseBindingVersion: 1,
		EnterpriseModelRef: pin.ModelRef, CreatedBy: "admin", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.ExecutionSnapshot{
		ID: "invalid-snapshot", TenantID: workspace.ID, ReleaseCandidateID: "candidate-invalid", CandidateVersion: 1,
		TargetType: "assistant", TargetID: route.ID, TargetVersion: "v1",
		SnapshotSchemaVersion: "v1", SnapshotHashAlgorithm: "SHA256", SnapshotHash: "sha256:invalid",
		ExecutionConfig: "{}", ModelRoutePinID: "missing", ModelRoutePinVersion: 1,
		CreatedBy: "admin", CreatedAt: now,
	}).Error; err == nil {
		t.Fatal("expected snapshot with missing pin to be rejected")
	}

	if err := gdb.Delete(connection).Error; err == nil {
		t.Fatal("expected connection with versions to be protected from deletion")
	}
}

// ScenarioID: SC-PG-001
func TestP0_PG_001_EnterpriseConnectionReferenceContractPostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL reference contract")
	}
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, constraint := range []string{
		"fk_enterprise_connection_version_connection",
		"fk_enterprise_binding_connection",
		"fk_enterprise_binding_tenant",
		"fk_enterprise_binding_version_binding",
		"fk_enterprise_model_route_pin_route",
		"fk_enterprise_model_route_pin_connection",
		"fk_enterprise_model_route_pin_binding",
		"fk_resource_binding_current_version",
		"uk_resource_binding_version_pointer",
	} {
		var count int64
		if err := gdb.Raw(`SELECT COUNT(*) FROM pg_constraint WHERE conname = ?`, constraint).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("PostgreSQL constraint %q is missing", constraint)
		}
	}
}

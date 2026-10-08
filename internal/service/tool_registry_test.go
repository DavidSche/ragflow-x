package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newToolRegistryService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "tool-registry.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	mock := ragflow.NewMock()
	svc := New(repository.NewStore(gdb), mock, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func addToolRegistryInput(version string, active *bool) ToolRegistryInput {
	return ToolRegistryInput{
		ToolID: "add", Version: version, ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:add", Active: active,
	}
}

func TestCreateToolRegistryEnforcesContractAndActiveVersion(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant := id.New()
	user := id.New()
	first, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create tool registry: %v", err)
	}
	if first.TenantID != tenant || first.Active != true || first.InputSchema != builtinToolSpecs["add"].InputSchema {
		t.Fatalf("unexpected tool: %+v", first)
	}
	inactive := false
	if _, err := svc.UpdateToolRegistry(ctx, tenant, user, first.ID, addToolRegistryInput("v1", &inactive)); err != nil {
		t.Fatalf("deactivate tool: %v", err)
	}
	second, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v2", nil))
	if err != nil {
		t.Fatalf("create second tool version: %v", err)
	}
	active := true
	if _, err := svc.UpdateToolRegistry(ctx, tenant, user, first.ID, addToolRegistryInput("v1", &active)); err == nil {
		t.Fatal("reactivating a second active version must fail")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 409 || businessErr.Code != 40996 {
		t.Fatalf("expected 409/40996, got %#v", err)
	}
	list, total, err := svc.ListToolRegistries(ctx, tenant, repository.ToolRegistryFilter{Active: &active}, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("active list = %+v total=%d err=%v", list, total, err)
	}
	other, _, err := svc.ListToolRegistries(ctx, id.New(), repository.ToolRegistryFilter{}, 1, 20)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant list = %+v err=%v", other, err)
	}
}

func TestToolRegistryValidation(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	input := addToolRegistryInput("v1", nil)
	input.ToolID = "unknown"
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("unknown builtin tool must fail")
	}
	input = addToolRegistryInput("v1", nil)
	input.ImplementationRef = "javascript:eval"
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("non-builtin implementation reference must fail")
	}
	input = addToolRegistryInput("v1", nil)
	input.InputSchema = `{"type":"object","required":["a"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("builtin contract mismatch must fail")
	}
	input = addToolRegistryInput("v1", nil)
	input.OutputSchema = "not-json"
	if _, err := svc.CreateToolRegistry(ctx, tenant, user, input); err == nil {
		t.Fatal("invalid JSON schema must fail")
	}
}

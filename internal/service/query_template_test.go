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
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newQueryTemplateService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "query-template.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func queryTemplatePolicy() string {
	return `{"read_only":true,"max_rows":1000,"timeout_ms":5000,"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["exposure","tenant_id"]}}`
}

func queryTemplateInput(sql string) QueryTemplateInput {
	return QueryTemplateInput{
		Name: "Risk exposure", SQLTemplate: sql,
		ParameterSchema: `{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		ResultSchema:    `{"type":"object","required":["exposure"],"properties":{"exposure":{"type":"number"}}}`,
		ExecutionPolicy: queryTemplatePolicy(), AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ConnectionID: "connection-1",
	}
}

func TestQueryTemplateCRUDAndASTValidation(t *testing.T) {
	ctx := context.Background()
	svc := newQueryTemplateService(t)
	tenant, user := id.New(), id.New()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, DBConnectionInput{
		Name: "Risk DB", Driver: model.DBDriverSQLite, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	input := queryTemplateInput("SELECT tenant_id, exposure FROM risk_exposure WHERE tenant_id = :tenant_id")
	input.ConnectionID = connection.ID
	template, err := svc.CreateQueryTemplate(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create query template: %v", err)
	}
	if template.ID == "" || template.ConnectionID != connection.ID || !template.Active {
		t.Fatalf("unexpected template: %+v", template)
	}
	if template.ExecutionPolicy != `{"read_only":true,"max_rows":1000,"timeout_ms":5000,"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["exposure","tenant_id"]}}` {
		t.Fatalf("execution policy was not canonicalized: %s", template.ExecutionPolicy)
	}

	duplicate := input
	if _, err := svc.CreateQueryTemplate(ctx, tenant, user, duplicate); err == nil {
		t.Fatal("duplicate template name must be rejected")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 409 || businessErr.Code != 40199 {
		t.Fatalf("expected 409/40199, got %#v", err)
	}

	rejected := map[string]QueryTemplateInput{
		"write statement":   queryTemplateInput("DELETE FROM risk_exposure WHERE tenant_id = :tenant_id"),
		"unknown table":     queryTemplateInput("SELECT tenant_id FROM other_table WHERE tenant_id = :tenant_id"),
		"star projection":   queryTemplateInput("SELECT * FROM risk_exposure WHERE tenant_id = :tenant_id"),
		"unknown function":  queryTemplateInput("SELECT now() FROM risk_exposure WHERE tenant_id = :tenant_id"),
		"unknown column":    queryTemplateInput("SELECT password_hash FROM risk_exposure WHERE tenant_id = :tenant_id"),
		"unknown bind":      queryTemplateInput("SELECT tenant_id FROM risk_exposure WHERE tenant_id = :missing"),
		"missing required":  queryTemplateInput("SELECT tenant_id FROM risk_exposure"),
		"database prefix":   queryTemplateInput("SELECT tenant_id FROM main.risk_exposure WHERE tenant_id = :tenant_id"),
		"readonly disabled": queryTemplateInput("SELECT tenant_id FROM risk_exposure WHERE tenant_id = :tenant_id"),
	}
	for name, invalid := range rejected {
		if name == "readonly disabled" {
			invalid.ExecutionPolicy = `{"read_only":false,"max_rows":10,"timeout_ms":1000,"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["tenant_id"]}}`
		}
		invalid.ConnectionID = connection.ID
		if _, err := svc.CreateQueryTemplate(ctx, tenant, user, invalid); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}

	inactive := false
	update := queryTemplateInput("SELECT exposure, tenant_id FROM risk_exposure WHERE tenant_id = :tenant_id")
	update.Name = "Risk exposure renamed"
	update.ConnectionID = connection.ID
	update.Active = &inactive
	updated, err := svc.UpdateQueryTemplate(ctx, tenant, user, template.ID, update)
	if err != nil {
		t.Fatalf("update query template: %v", err)
	}
	if updated.Name != "Risk exposure renamed" || updated.Active || updated.CreatedAt != template.CreatedAt {
		t.Fatalf("unexpected updated template: %+v", updated)
	}

	if _, err := svc.GetQueryTemplate(ctx, id.New(), template.ID); err == nil {
		t.Fatal("cross-tenant query template must be rejected")
	}
	if err := svc.DeleteQueryTemplate(ctx, tenant, user, template.ID); err != nil {
		t.Fatalf("delete query template: %v", err)
	}
	if _, err := svc.GetQueryTemplate(ctx, tenant, template.ID); err == nil {
		t.Fatal("deleted query template must be rejected")
	}
	audits, _, err := svc.ListAudits(ctx, tenant, 1, 20, repository.AuditFilter{Resource: "query-template"})
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if len(audits) != 3 {
		t.Fatalf("expected created/updated/deleted audits, got %+v", audits)
	}
}

func TestQueryTemplateCTEASTValidation(t *testing.T) {
	ctx := context.Background()
	svc := newQueryTemplateService(t)
	tenant, user := id.New(), id.New()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, DBConnectionInput{
		Name: "Risk DB", Driver: model.DBDriverSQLite, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	input := queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM risk_exposure) SELECT tenant_id FROM scoped WHERE tenant_id = :tenant_id")
	input.ConnectionID = connection.ID
	template, err := svc.CreateQueryTemplate(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create safe CTE template: %v", err)
	}
	if template.ID == "" {
		t.Fatal("expected CTE template id")
	}

	rejected := map[string]QueryTemplateInput{
		"recursive CTE":      queryTemplateInput("WITH RECURSIVE scoped (tenant_id) AS (SELECT tenant_id FROM risk_exposure UNION ALL SELECT tenant_id FROM scoped) SELECT tenant_id FROM scoped"),
		"unknown CTE table":  queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM other_table) SELECT tenant_id FROM scoped"),
		"unknown CTE column": queryTemplateInput("WITH scoped (tenant_id) AS (SELECT password_hash FROM risk_exposure) SELECT tenant_id FROM scoped"),
		"schema prefix":      queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM main.risk_exposure) SELECT tenant_id FROM scoped"),
		"duplicate CTE":      queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM risk_exposure), scoped (exposure) AS (SELECT exposure FROM risk_exposure) SELECT exposure FROM scoped"),
		"unknown CTE":        queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM risk_exposure) SELECT tenant_id FROM other_cte"),
		"CTE star":           queryTemplateInput("WITH scoped (tenant_id) AS (SELECT tenant_id FROM risk_exposure) SELECT * FROM scoped"),
		"CTE body star":      queryTemplateInput("WITH scoped (tenant_id) AS (SELECT * FROM risk_exposure) SELECT tenant_id FROM scoped"),
	}
	for name, invalid := range rejected {
		invalid.ConnectionID = connection.ID
		if _, err := svc.CreateQueryTemplate(ctx, tenant, user, invalid); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestQueryTemplateRequiresTenantConnection(t *testing.T) {
	ctx := context.Background()
	svc := newQueryTemplateService(t)
	tenant, user := id.New(), id.New()
	input := queryTemplateInput("SELECT tenant_id FROM risk_exposure WHERE tenant_id = :tenant_id")
	if _, err := svc.CreateQueryTemplate(ctx, tenant, user, input); err == nil {
		t.Fatal("missing connection must be rejected")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Code != 40182 {
		t.Fatalf("expected 40182, got %#v", err)
	}
}

package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newDBConnectionService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "db-connection.db")})
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

func dbConnectionInput() DBConnectionInput {
	return DBConnectionInput{
		Name: "Risk DB", Driver: model.DBDriverPostgres, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
		MaxOpenConns:       5, MaxIdleConns: 1,
	}
}

func TestDBConnectionAuthorizationScope(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	input := dbConnectionInput()
	input.AuthorizationScope = `{}`
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("empty db connection authorization scope must fail")
	}
	input.AuthorizationScope = `{"roles":["unknown_role"]}`
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("unknown db connection role must fail")
	}
	input.AuthorizationScope = `{"roles":["tenant_admin"]}`
	connection, err := svc.CreateDBConnection(ctx, tenant, user, input)
	if err != nil || connection.AuthorizationScope != input.AuthorizationScope {
		t.Fatalf("valid db connection scope: %+v err=%v", connection, err)
	}
}

func TestCreateDBConnectionValidation(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	input := dbConnectionInput()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	if connection.ID == "" || connection.TenantID != tenant || !connection.ReadOnly || connection.HealthStatus != model.DBHealthUnknown {
		t.Fatalf("unexpected connection: %+v", connection)
	}
	if connection.MaxOpenConns != 5 || connection.MaxIdleConns != 1 || connection.LastVerifiedAt != nil {
		t.Fatalf("unexpected connection defaults: %+v", connection)
	}

	input = dbConnectionInput()
	input.ReadOnly = boolPtr(false)
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("read-write connection must be rejected")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Code != 40173 {
		t.Fatalf("expected 40173, got %#v", err)
	}

	input = dbConnectionInput()
	input.Driver = "mongodb"
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("unsupported driver must be rejected")
	}

	input = dbConnectionInput()
	input.DSNRef = "postgres://user:password@localhost:5432/risk"
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("plaintext DSN must be rejected")
	}

	input = dbConnectionInput()
	input.MaxOpenConns = 2
	input.MaxIdleConns = 3
	if _, err := svc.CreateDBConnection(ctx, tenant, user, input); err == nil {
		t.Fatal("max idle connections above max open connections must be rejected")
	}
}

func TestDBConnectionCRUDTenantIsolationAndAudit(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, dbConnectionInput())
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	verifiedAt := time.Now().UTC().Add(-time.Minute)
	if err := svc.Store.UpdateDBConnection(ctx, connection); err != nil {
		t.Fatalf("seed verified at: %v", err)
	}
	connection.LastVerifiedAt = &verifiedAt
	if err := svc.Store.UpdateDBConnection(ctx, connection); err != nil {
		t.Fatalf("set last verified: %v", err)
	}

	input := dbConnectionInput()
	input.Name = "Risk DB Updated"
	input.Driver = model.DBDriverMySQL
	input.DSNRef = "vault://tenant/risk-db"
	updated, err := svc.UpdateDBConnection(ctx, tenant, user, connection.ID, input)
	if err != nil {
		t.Fatalf("update db connection: %v", err)
	}
	if updated.Name != input.Name || updated.Driver != input.Driver || updated.DSNRef != input.DSNRef {
		t.Fatalf("unexpected updated connection: %+v", updated)
	}
	if updated.ID != connection.ID || updated.CreatedBy != user || updated.HealthStatus != model.DBHealthUnknown {
		t.Fatalf("update changed immutable fields: %+v", updated)
	}
	if updated.LastVerifiedAt == nil || !updated.LastVerifiedAt.Equal(verifiedAt) {
		t.Fatalf("update changed last verified at: %+v", updated.LastVerifiedAt)
	}

	list, total, err := svc.ListDBConnections(ctx, tenant, repository.DBConnectionFilter{Driver: model.DBDriverMySQL}, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != connection.ID {
		t.Fatalf("db connection list = %+v total=%d err=%v", list, total, err)
	}
	if _, err := svc.GetDBConnection(ctx, id.New(), connection.ID); err == nil {
		t.Fatal("cross-tenant get must fail")
	}

	if err := svc.DeleteDBConnection(ctx, tenant, user, connection.ID); err != nil {
		t.Fatalf("delete db connection: %v", err)
	}
	if _, err := svc.GetDBConnection(ctx, tenant, connection.ID); err == nil {
		t.Fatal("deleted connection must not be found")
	}
	audits, err := svc.Store.ListAuditsAll(ctx, tenant)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	var actions []string
	for _, audit := range audits {
		if audit.Resource == "db-connection" && audit.ResourceID == connection.ID {
			actions = append(actions, audit.Action)
			if strings.Contains(audit.DetailJSON, "secret://") || strings.Contains(audit.DetailJSON, "vault://") {
				t.Fatalf("audit must not contain DSNRef: %s", audit.DetailJSON)
			}
		}
	}
	if len(actions) != 3 || actions[0] != "db_connection.created" || actions[1] != "db_connection.updated" || actions[2] != "db_connection.deleted" {
		t.Fatalf("unexpected audit actions: %v", actions)
	}
}

func TestDBConnectionTestFailsClosedWithoutSecretResolver(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, dbConnectionInput())
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	_, err = svc.TestDBConnection(ctx, tenant, user, connection.ID)
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 503 || businessErr.Code != 50300 {
		t.Fatalf("expected resolver missing 503/50300, got %#v", err)
	}
}

func TestDBConnectionTestHealthyRoute(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	input := dbConnectionInput()
	input.Driver = model.DBDriverSQLite
	connection, err := svc.CreateDBConnection(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	dsn := filepath.Join(t.TempDir(), "risk.db")
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
		return dsn, nil
	})

	result, err := svc.TestDBConnection(ctx, tenant, user, connection.ID)
	if err != nil {
		t.Fatalf("test db connection: %v", err)
	}
	if result.Status != model.DBHealthHealthy || !result.ReadOnly || result.Message != "connection verified" || result.LastVerifiedAt == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	verified, err := svc.GetDBConnection(ctx, tenant, connection.ID)
	if err != nil || verified.HealthStatus != model.DBHealthHealthy || verified.LastVerifiedAt == nil {
		t.Fatalf("test state not projected: %+v err=%v", verified, err)
	}
	audits, err := svc.Store.ListAuditsAll(ctx, tenant)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	var found *model.AuditLog
	for _, audit := range audits {
		if audit.Resource == "db-connection" && audit.ResourceID == connection.ID && audit.Action == "db_connection.tested" {
			found = &audit
		}
	}
	if found == nil {
		t.Fatal("db connection test audit not found")
	}
	if strings.Contains(found.DetailJSON, "secret://") || strings.Contains(found.DetailJSON, dsn) {
		t.Fatalf("audit must not contain DSNRef or resolved DSN: %s", found.DetailJSON)
	}
}

func TestVerifyDBConnectionReadOnlySQLite(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "read-only.db")
	if err := verifyDBConnectionReadOnly(ctx, model.DBDriverSQLite, dsn); err != nil {
		t.Fatalf("sqlite session read-only verification: %v", err)
	}
	target, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var count int
	if err := target.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'rgx_read_only_probe'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("read-only verification must not leave a probe table")
	}
}

func TestDBConnectionTestUnhealthyDoesNotLeakSecrets(t *testing.T) {
	ctx := context.Background()
	svc := newDBConnectionService(t)
	tenant, user := id.New(), id.New()
	connection, err := svc.CreateDBConnection(ctx, tenant, user, dbConnectionInput())
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
		return "postgres://user:super-secret@localhost:1/risk", nil
	})

	_, err = svc.TestDBConnection(ctx, tenant, user, connection.ID)
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 502 || businessErr.Code != 50200 {
		t.Fatalf("expected 502/50200, got %#v", err)
	}
	if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("connection error leaked DSN: %v", err)
	}
	verified, err := svc.GetDBConnection(ctx, tenant, connection.ID)
	if err != nil || verified.HealthStatus != model.DBHealthUnhealthy || verified.LastVerifiedAt == nil {
		t.Fatalf("unhealthy state not projected: %+v err=%v", verified, err)
	}
	audits, err := svc.Store.ListAuditsAll(ctx, tenant)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	for _, audit := range audits {
		if audit.Resource == "db-connection" && audit.ResourceID == connection.ID {
			if strings.Contains(audit.DetailJSON, "secret://") || strings.Contains(audit.DetailJSON, "super-secret") || strings.Contains(audit.DetailJSON, "postgres://") {
				t.Fatalf("audit leaked connection data: %s", audit.DetailJSON)
			}
		}
	}
}

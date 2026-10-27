package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestDBConnectionPersistenceAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "db-connection-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	if !gdb.Migrator().HasTable(&model.DBConnection{}) {
		t.Fatal("db connection table is missing")
	}
	if !gdb.Migrator().HasIndex(&model.DBConnection{}, "idx_rgx_db_connection_lookup") {
		t.Fatal("db connection lookup index is missing")
	}

	tenant := id.New()
	now := time.Now().UTC()
	first := &model.DBConnection{
		ID: id.New(), TenantID: tenant, Name: "Risk DB", Driver: model.DBDriverPostgres,
		DSNRef: "secret://tenant/risk-db", ReadOnly: true, MaxOpenConns: 5, MaxIdleConns: 1,
		HealthStatus: model.DBHealthUnknown, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	second := &model.DBConnection{
		ID: id.New(), TenantID: tenant, Name: "Finance DB", Driver: model.DBDriverMySQL,
		DSNRef: "vault://finance/db", ReadOnly: true, MaxOpenConns: 2, MaxIdleConns: 1,
		HealthStatus: model.DBHealthUnhealthy, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	for _, connection := range []*model.DBConnection{first, second} {
		if err := store.CreateDBConnection(ctx, connection); err != nil {
			t.Fatalf("create db connection: %v", err)
		}
	}

	list, total, err := store.ListDBConnections(ctx, tenant, DBConnectionFilter{}, 1, 20)
	if err != nil || total != 2 || len(list) != 2 || list[0].Name != "Finance DB" || list[1].Name != "Risk DB" {
		t.Fatalf("db connection list = %+v total=%d err=%v", list, total, err)
	}
	filtered, total, err := store.ListDBConnections(ctx, tenant, DBConnectionFilter{Driver: model.DBDriverMySQL, HealthStatus: model.DBHealthUnhealthy}, 1, 20)
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].ID != second.ID {
		t.Fatalf("filtered db connection list = %+v total=%d err=%v", filtered, total, err)
	}
	if other, total, err := store.ListDBConnections(ctx, id.New(), DBConnectionFilter{}, 1, 20); err != nil || total != 0 || len(other) != 0 {
		t.Fatalf("cross-tenant list = %+v total=%d err=%v", other, total, err)
	}

	got, err := store.GetDBConnection(ctx, tenant, first.ID)
	if err != nil || got == nil || got.DSNRef != first.DSNRef {
		t.Fatalf("get db connection = %+v err=%v", got, err)
	}
	if crossTenant, err := store.GetDBConnection(ctx, id.New(), first.ID); err != nil || crossTenant != nil {
		t.Fatalf("cross-tenant get = %+v err=%v", crossTenant, err)
	}
	first.HealthStatus = model.DBHealthHealthy
	first.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateDBConnection(ctx, first); err != nil {
		t.Fatalf("update db connection: %v", err)
	}
	updated, err := store.GetDBConnection(ctx, tenant, first.ID)
	if err != nil || updated.HealthStatus != model.DBHealthHealthy {
		t.Fatalf("updated db connection = %+v err=%v", updated, err)
	}
	if err := store.DeleteDBConnection(ctx, tenant, first.ID); err != nil {
		t.Fatalf("delete db connection: %v", err)
	}
	if deleted, err := store.GetDBConnection(ctx, tenant, first.ID); err != nil || deleted != nil {
		t.Fatalf("deleted db connection = %+v err=%v", deleted, err)
	}
}

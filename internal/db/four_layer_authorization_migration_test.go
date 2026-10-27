package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV122FourLayerAuthorizationScopes(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "four-layer.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	if !gdb.Migrator().HasColumn(&model.SourceRoutingRule{}, "authorization_scope") {
		t.Fatal("source routing rule authorization_scope is missing")
	}
	if !gdb.Migrator().HasColumn(&model.DBConnection{}, "authorization_scope") {
		t.Fatal("db connection authorization_scope is missing")
	}
	var ruleCount, connectionCount int64
	if err := gdb.Model(&model.SourceRoutingRule{}).Where("authorization_scope = '' OR authorization_scope = '{}'").Count(&ruleCount).Error; err != nil || ruleCount != 0 {
		t.Fatalf("rule scope backfill count=%d err=%v", ruleCount, err)
	}
	if err := gdb.Model(&model.DBConnection{}).Where("authorization_scope = '' OR authorization_scope = '{}'").Count(&connectionCount).Error; err != nil || connectionCount != 0 {
		t.Fatalf("connection scope backfill count=%d err=%v", connectionCount, err)
	}
}

func TestMigrationV122BackfillsPreScopeRows(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "four-layer-upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := gdb.AutoMigrate(&model.SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for version := int64(1); version <= 121; version++ {
		if err := gdb.Create(&model.SchemaVersion{Version: version, AppliedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`CREATE TABLE rgx_tenant (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE rgx_source_routing_rule (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			assistant_id TEXT,
			source_type TEXT NOT NULL,
			matcher TEXT NOT NULL,
			tool_id TEXT NOT NULL,
			tool_version TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			priority INTEGER NOT NULL,
			confidence_threshold REAL NOT NULL,
			active BOOLEAN NOT NULL,
			created_by TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE rgx_db_connection (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			driver TEXT NOT NULL,
			dsn_ref TEXT NOT NULL,
			read_only BOOLEAN NOT NULL,
			max_open_conns INTEGER NOT NULL,
			max_idle_conns INTEGER NOT NULL,
			health_status TEXT NOT NULL,
			last_verified_at DATETIME,
			created_by TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`INSERT INTO rgx_tenant (id, type, name) VALUES ('tenant-1', 'platform', 'Platform')`,
		`INSERT INTO rgx_source_routing_rule (
			id, tenant_id, assistant_id, source_type, matcher, tool_id, tool_version,
			policy_version, priority, confidence_threshold, active, created_by, created_at, updated_at
		) VALUES (
			'rule-1', 'tenant-1', '', 'db', '{}', 'sql_query', 'v1', 'policy-v1', 1, 0.8,
			1, 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
		`INSERT INTO rgx_db_connection (
			id, tenant_id, name, driver, dsn_ref, read_only, max_open_conns, max_idle_conns,
			health_status, last_verified_at, created_by, created_at, updated_at
		) VALUES (
			'connection-1', 'tenant-1', 'Risk DB', 'sqlite', 'secret://tenant/risk-db', 1, 5, 1,
			'unknown', NULL, 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
	}
	for _, statement := range statements {
		if err := gdb.Exec(statement).Error; err != nil {
			t.Fatalf("prepare pre-scope schema: %v", err)
		}
	}
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	expectedScope := `{"roles":["platform_admin","tenant_admin"]}`
	var ruleScope string
	if err := gdb.Raw(`SELECT authorization_scope FROM rgx_source_routing_rule WHERE id = 'rule-1'`).Scan(&ruleScope).Error; err != nil || ruleScope != expectedScope {
		t.Fatalf("rule scope=%q err=%v", ruleScope, err)
	}
	var connectionScope string
	if err := gdb.Raw(`SELECT authorization_scope FROM rgx_db_connection WHERE id = 'connection-1'`).Scan(&connectionScope).Error; err != nil || connectionScope != expectedScope {
		t.Fatalf("connection scope=%q err=%v", connectionScope, err)
	}
}

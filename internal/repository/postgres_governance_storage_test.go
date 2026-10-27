package repository

// ScenarioID: SC-PG-001

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-PG-001
func TestP0_PG_013_PostgresGovernanceStorageUsesJSONBAndSyncPartialIndexes(t *testing.T) {
	testStore := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := mustCreatePostgresTenant(t, testStore, "PG Governance Storage Tenant")

	revision := &model.SettingRevision{
		SchemaVersion: 1,
		SnapshotJSON:  `{"security":{"session_ttl_min":30}}`,
		CreatedBy:     "pg-storage-actor", CreatedAt: now, UpdatedAt: now,
		Checksum: "pg-storage-checksum",
	}
	if err := testStore.CreateSettingRevisionWithPointer(ctx, revision, ""); err != nil {
		t.Fatalf("create setting revision: %v", err)
	}
	setting := &model.ResourceSyncSetting{
		ID: "pg-sync-setting", SourceID: "pg-sync-source", TenantID: tenant.ID,
		ResourceTypesJSON: `["dataset","chat"]`,
		ScopeJSON:         `{"scope_type":"TENANTS","tenant_ids":["tenant-a"]}`,
		CreatedAt:         now, UpdatedAt: now,
	}
	if err := testStore.SaveResourceSyncSetting(ctx, setting); err != nil {
		t.Fatalf("create resource sync setting: %v", err)
	}
	run := &model.SyncRun{
		ID: "pg-sync-run", SourceID: "pg-sync-source", SourceCredentialVersion: "v1",
		TriggerType: model.SyncTriggerManualReconcile, Status: model.SyncRunScanning,
		ResourceTypesJSON: `["dataset"]`, ScopeJSON: `{"tenant_ids":["tenant-a"]}`,
		SourceSnapshotJSON: `{"datasets":[]}`, PlanSummaryJSON: `{"dataset":{"create":1}}`,
		ResultSummaryJSON: `{"dataset":{"create_succeeded":1}}`,
		CreatedBy:         "pg-storage-actor", CreatedAt: now, UpdatedAt: now,
	}
	if err := testStore.CreateSyncRun(ctx, run); err != nil {
		t.Fatalf("create sync run: %v", err)
	}

	type columnType struct {
		TableName  string
		ColumnName string
		DataType   string
	}
	expectedTypes := []columnType{
		{"rgx_setting_revision", "snapshot_json", "jsonb"},
		{"rgx_resource_sync_settings", "resource_types_json", "jsonb"},
		{"rgx_resource_sync_settings", "scope_json", "jsonb"},
		{"rgx_sync_run", "resource_types_json", "jsonb"},
		{"rgx_sync_run", "scope_json", "jsonb"},
		{"rgx_sync_run", "source_snapshot_json", "jsonb"},
		{"rgx_sync_run", "plan_summary_json", "jsonb"},
		{"rgx_sync_run", "result_summary_json", "jsonb"},
	}
	var actualTypes []columnType
	tables := []string{"rgx_setting_revision", "rgx_resource_sync_settings", "rgx_sync_run"}
	columns := []string{
		"snapshot_json", "resource_types_json", "scope_json", "source_snapshot_json",
		"plan_summary_json", "result_summary_json",
	}
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name IN ?
			AND column_name IN ?
		ORDER BY table_name, column_name
	`, tables, columns).Scan(&actualTypes).Error; err != nil {
		t.Fatalf("load JSONB column types: %v", err)
	}
	actualTypesByName := make(map[string]string, len(actualTypes))
	for _, column := range actualTypes {
		actualTypesByName[column.TableName+"."+column.ColumnName] = column.DataType
	}
	for _, expected := range expectedTypes {
		key := expected.TableName + "." + expected.ColumnName
		if actualTypesByName[key] != "jsonb" {
			t.Fatalf("column %s type = %q, want jsonb", key, actualTypesByName[key])
		}
	}

	malformedUpdates := []struct {
		name  string
		query string
		args  []interface{}
	}{
		{
			name:  "setting snapshot",
			query: "UPDATE rgx_setting_revision SET snapshot_json = ? WHERE id = ?",
			args:  []interface{}{"not-valid-json", revision.ID},
		},
		{
			name:  "setting resource types",
			query: "UPDATE rgx_resource_sync_settings SET resource_types_json = ? WHERE id = ?",
			args:  []interface{}{"not-valid-json", setting.ID},
		},
		{
			name:  "sync scope",
			query: "UPDATE rgx_sync_run SET scope_json = ? WHERE id = ?",
			args:  []interface{}{"not-valid-json", run.ID},
		},
		{
			name:  "sync result summary",
			query: "UPDATE rgx_sync_run SET result_summary_json = ? WHERE id = ?",
			args:  []interface{}{"not-valid-json", run.ID},
		},
	}
	for _, malformed := range malformedUpdates {
		if err := testStore.(*store).DB.WithContext(ctx).Exec(malformed.query, malformed.args...).Error; err == nil {
			t.Fatalf("malformed %s JSON was accepted", malformed.name)
		}
	}

	var matchedSettings int64
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT COUNT(*) FROM rgx_resource_sync_settings
		WHERE scope_json @> ?::jsonb
	`, `{"scope_type":"TENANTS"}`).Scan(&matchedSettings).Error; err != nil || matchedSettings != 1 {
		t.Fatalf("JSONB scope containment: count=%d err=%v", matchedSettings, err)
	}

	if err := testStore.(*store).DB.WithContext(ctx).Exec(`
		UPDATE rgx_setting_revision
		SET checksum = ?
		WHERE id = ?
	`, "mutated", revision.ID).Error; err == nil || !strings.Contains(err.Error(), "setting revisions are immutable") {
		t.Fatalf("setting revision immutability trigger missing: err=%v", err)
	}

	type indexDefinition struct {
		Name     string
		Indexdef string
	}
	var indexes []indexDefinition
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT indexname AS name, indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND indexname IN ?
		ORDER BY indexname
	`, []string{"idx_sync_item_skipped_conflict_identity", "idx_sync_run_active_by_source"}).
		Scan(&indexes).Error; err != nil {
		t.Fatalf("load sync partial indexes: %v", err)
	}
	if len(indexes) != 2 {
		t.Fatalf("sync partial indexes = %+v, want 2", indexes)
	}
	if indexes[0].Name != "idx_sync_item_skipped_conflict_identity" || !containsAll(indexes[0].Indexdef,
		"resource_type", "external_scope_key", "external_id", "conflict_type", "skipped", "conflict", "relink") {
		t.Fatalf("sync item index definition = %q", indexes[0].Indexdef)
	}
	if indexes[1].Name != "idx_sync_run_active_by_source" || !containsAll(indexes[1].Indexdef,
		"source_id", "updated_at", "scanning", "running") {
		t.Fatalf("sync run index definition = %q", indexes[1].Indexdef)
	}
}

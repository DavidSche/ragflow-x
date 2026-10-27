package repository

// ScenarioID: SC-AUDIT-002

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-AUDIT-002
func TestP0_PG_012_PostgresAlertStorageUsesJSONBAndCompensationPartialIndexes(t *testing.T) {
	testStore := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := mustCreatePostgresTenant(t, testStore, "PG Alert Storage Tenant")

	event := &model.AlertEvent{
		ID: "pg-alert-jsonb", TenantID: tenant.ID, Title: "Provider compensation failed",
		Severity: "error", Type: "provider.compensation.failed", Resource: "model-provider",
		ResourceID: "provider-1", Detail: "manual reconciliation required",
		FieldsJSON: `{"operation":"delete"}`, Fingerprint: "pg-alert-jsonb-fingerprint",
		OccurredAt: now, Status: model.AlertStatusOpen,
	}
	if err := testStore.CreateAlertEvent(ctx, event); err != nil {
		t.Fatalf("create alert event: %v", err)
	}

	var dataType string
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name = 'rgx_alert_event'
			AND column_name = 'fields_json'
	`).Scan(&dataType).Error; err != nil || dataType != "jsonb" {
		t.Fatalf("alert fields column type = %q err=%v, want jsonb", dataType, err)
	}

	if err := testStore.(*store).DB.WithContext(ctx).Exec(`
		UPDATE rgx_alert_event
		SET fields_json = ?
		WHERE id = ?
	`, "not-valid-json", event.ID).Error; err == nil {
		t.Fatal("malformed alert fields were accepted by PostgreSQL")
	}

	var matched int64
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT COUNT(*)
		FROM rgx_alert_event
		WHERE fields_json @> ?::jsonb
	`, `{"operation":"delete"}`).Scan(&matched).Error; err != nil || matched != 1 {
		t.Fatalf("JSONB containment query: count=%d err=%v", matched, err)
	}

	failedAt := now.Add(-2 * time.Minute)
	failed := &model.AlertDelivery{
		AlertEventID: event.ID, Channel: "primary", TenantID: tenant.ID,
		Status: model.AlertDeliveryStatusFailed, Attempts: 1, LastAttemptAt: failedAt,
		NextRetryAt: &failedAt, CreatedAt: failedAt, UpdatedAt: failedAt,
	}
	if err := testStore.UpsertAlertDelivery(ctx, failed); err != nil {
		t.Fatalf("create failed delivery: %v", err)
	}
	pendingAt := now.Add(-3 * time.Minute)
	pending := &model.AlertDelivery{
		AlertEventID: event.ID, Channel: "secondary", TenantID: tenant.ID,
		Status: model.AlertDeliveryStatusPending, Attempts: 0, LastAttemptAt: pendingAt,
		NextRetryAt: &pendingAt, CreatedAt: pendingAt, UpdatedAt: pendingAt,
	}
	if err := testStore.UpsertAlertDelivery(ctx, pending); err != nil {
		t.Fatalf("create stale pending delivery: %v", err)
	}

	var indexes []struct {
		Name     string
		Indexdef string
	}
	if err := testStore.(*store).DB.WithContext(ctx).Raw(`
		SELECT indexname AS name, indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND tablename = 'rgx_alert_delivery'
			AND indexname IN ?
		ORDER BY indexname
	`, []string{"idx_alert_delivery_failed_retry", "idx_alert_delivery_pending_compensation"}).
		Scan(&indexes).Error; err != nil {
		t.Fatalf("load compensation partial indexes: %v", err)
	}
	if len(indexes) != 2 {
		t.Fatalf("compensation partial indexes = %+v, want 2", indexes)
	}
	for _, index := range indexes {
		if index.Indexdef == "" || (index.Name != "idx_alert_delivery_failed_retry" && index.Name != "idx_alert_delivery_pending_compensation") {
			t.Fatalf("unexpected PostgreSQL partial index: %+v", index)
		}
	}
	if indexes[0].Name != "idx_alert_delivery_failed_retry" || !containsAll(indexes[0].Indexdef, "next_retry_at", "status", "failed") {
		t.Fatalf("failed retry index definition = %q", indexes[0].Indexdef)
	}
	if indexes[1].Name != "idx_alert_delivery_pending_compensation" || !containsAll(indexes[1].Indexdef, "last_attempt_at", "status", "pending") {
		t.Fatalf("pending compensation index definition = %q", indexes[1].Indexdef)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

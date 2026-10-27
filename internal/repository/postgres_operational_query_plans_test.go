package repository

// ScenarioID: SC-PG-001

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

const (
	postgresQueryPlanThresholdMS = 250.0
	postgresQueryPlanScenarioID  = "SC-PG-001"
)

var postgresOperationalQueryPlanIndexes = map[string]string{
	"active_sync_runs":           "idx_sync_run_active_by_source",
	"conflict_relink_items":      "idx_sync_item_skipped_conflict_identity",
	"failed_alert_retry":         "idx_alert_delivery_failed_retry",
	"pending_alert_compensation": "idx_alert_delivery_pending_compensation",
}

var postgresPlanExecutionPattern = regexp.MustCompile(`Execution Time: ([0-9]+(?:\.[0-9]+)?) ms`)

type postgresQueryPlanResult struct {
	Name            string  `json:"name"`
	ExpectedIndex   string  `json:"expectedIndex"`
	IndexUsed       bool    `json:"indexUsed"`
	ExecutionTimeMS float64 `json:"executionTimeMS"`
	ThresholdMS     float64 `json:"thresholdMS"`
	WithinThreshold bool    `json:"withinThreshold"`
	Plan            string  `json:"plan"`
}

type postgresQueryPlanReport struct {
	GeneratedAt         string                    `json:"generatedAt"`
	ScenarioID          string                    `json:"scenarioId"`
	Scale               int                       `json:"scale"`
	CompletedSyncRuns   int                       `json:"completedSyncRuns"`
	ActiveSyncRuns      int                       `json:"activeSyncRuns"`
	CompletedSyncItems  int                       `json:"completedSyncItems"`
	ConflictSyncItems   int                       `json:"conflictSyncItems"`
	SucceededDeliveries int                       `json:"succeededDeliveries"`
	FailedDeliveries    int                       `json:"failedDeliveries"`
	PendingDeliveries   int                       `json:"pendingDeliveries"`
	ThresholdMS         float64                   `json:"thresholdMS"`
	AllIndexesUsed      bool                      `json:"allIndexesUsed"`
	AllWithinThreshold  bool                      `json:"allWithinThreshold"`
	Queries             []postgresQueryPlanResult `json:"queries"`
}

// PG014 proves the operational hot paths choose the dedicated partial indexes
// when the tables contain a production-shaped completed/history majority.
func TestP0_PG_014_PostgresOperationalQueriesUsePartialIndexes(t *testing.T) {
	plans := runPostgresOperationalQueryPlanScenario(t, 1)
	assertPostgresQueryPlanIndexes(t, plans)
	assertPostgresQueryPlanRuntime(t, plans)
	writePostgresQueryPlanReport(t, plans, 1)
}

// PG015 proves the same index selections remain stable at 10x the PG014
// production shape and records runtime evidence for nightly trend tracking.
func TestP0_PG_015_PostgresOperationalQueriesRemainIndexedAtTenTimesScale(t *testing.T) {
	plans := runPostgresOperationalQueryPlanScenario(t, 10)
	assertPostgresQueryPlanIndexes(t, plans)
	assertPostgresQueryPlanRuntime(t, plans)
	writePostgresQueryPlanReport(t, plans, 10)
}

// PG016 is an opt-in warning-only capacity probe: index choice remains a hard
// contract, while runtime is collected for trend review without blocking merge.
//
// ScenarioID: SC-PG-002
func TestP1_PG_016_PostgresOperationalIndexSelectionAtHundredTimesScale(t *testing.T) {
	if os.Getenv("RGX_QUERY_PLAN_100X_ENABLED") != "true" {
		t.Skip("set RGX_QUERY_PLAN_100X_ENABLED=true to collect the warning-only 100x plan probe")
	}
	plans := runPostgresOperationalQueryPlanScenario(t, 100)
	assertPostgresQueryPlanIndexes(t, plans)
	writePostgresQueryPlanReport(t, plans, 100)
}

func TestPostgresQueryPlanReportScenarioIdentityMatchesScale(t *testing.T) {
	t.Setenv("RGX_QUERY_PLAN_REPORT_DIR", t.TempDir())
	for scale, expected := range map[int]string{1: "SC-PG-001", 100: "SC-PG-002"} {
		path := writePostgresQueryPlanReport(t, map[string]string{}, scale)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read query plan report: %v", err)
		}
		var report postgresQueryPlanReport
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("decode query plan report: %v", err)
		}
		if report.ScenarioID != expected {
			t.Fatalf("scale %d report scenario = %s, expected %s", scale, report.ScenarioID, expected)
		}
	}
}

func runPostgresOperationalQueryPlanScenario(t *testing.T, scale int) map[string]string {
	t.Helper()
	testStore := newPostgresContractStore(t)
	ctx := context.Background()
	db := testStore.(*store).DB.WithContext(ctx)
	now := seedPostgresQueryPlanData(t, testStore, db, scale)

	return map[string]string{
		"active_sync_runs": postgresExplainQueryPlan(t, db, `
			SELECT count(*)
			FROM rgx_sync_run
			WHERE source_id = 'ragflow_global'
				AND status IN ('scanning', 'running')
		`),
		"conflict_relink_items": postgresExplainQueryPlan(t, db, `
			SELECT count(*)
			FROM rgx_sync_item
			WHERE status = 'skipped'
				AND action IN ('conflict', 'relink')
				AND resource_type = 'dataset'
				AND external_scope_key = 'pg-plan-conflict-scope'
				AND external_id = 'pg-plan-conflict-1'
				AND conflict_type = 'identity'
		`),
		"failed_alert_retry": postgresExplainQueryPlan(t, db, `
			SELECT alert_event_id, channel
			FROM rgx_alert_delivery
			WHERE status = 'failed'
				AND next_retry_at IS NOT NULL
				AND next_retry_at <= ?
			ORDER BY next_retry_at, alert_event_id, channel
			LIMIT 10
		`, now),
		"pending_alert_compensation": postgresExplainQueryPlan(t, db, `
			SELECT alert_event_id, channel
			FROM rgx_alert_delivery
			WHERE status = 'pending'
				AND last_attempt_at <= ?
			ORDER BY last_attempt_at, alert_event_id, channel
			LIMIT 10
		`, now),
	}
}

func seedPostgresQueryPlanData(t *testing.T, testStore Store, db *gorm.DB, scale int) time.Time {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	completedSyncRuns := 1200 * scale
	activeSyncRuns := 25 * scale
	completedSyncItems := 1500 * scale
	conflictSyncItems := 25 * scale
	succeededDeliveries := 1500 * scale
	terminalDeliveries := 30 * scale

	if err := db.Exec(`
		INSERT INTO rgx_sync_run (
			id, source_id, source_credential_version, trigger_type, status,
			resource_types_json, scope_json, scan_consistency, created_by, created_at, updated_at
		)
		SELECT 'pg-plan-completed-' || g, 'ragflow_global', 'v1', 'manual_import', 'succeeded',
			'["dataset"]', '{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}', 'page_scan',
			'pg-plan-actor', ?::timestamptz - (g || ' seconds')::interval,
			?::timestamptz - (g || ' seconds')::interval
		FROM generate_series(1, ?) g
	`, now, now, completedSyncRuns).Error; err != nil {
		t.Fatalf("seed completed sync runs: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_sync_run (
			id, source_id, source_credential_version, trigger_type, status,
			resource_types_json, scope_json, scan_consistency, created_by, created_at, updated_at
		)
		SELECT 'pg-plan-active-' || g, 'ragflow_global', 'v1', 'manual_reconcile',
			CASE WHEN g % 2 = 0 THEN 'scanning' ELSE 'running' END,
			'["dataset"]', '{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}', 'page_scan',
			'pg-plan-actor', ?, ?
		FROM generate_series(1, ?) g
	`, now, now, activeSyncRuns).Error; err != nil {
		t.Fatalf("seed active sync runs: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_sync_item (
			id, run_id, resource_type, external_scope_key, external_id, action, status,
			conflict_type, upstream_current_hash, upstream_last_synced_hash,
			local_current_hash, local_last_synced_hash, created_at, updated_at
		)
		SELECT 'pg-plan-item-completed-' || g, 'pg-plan-completed-' || g, 'dataset',
			'pg-plan-scope-' || (g % 20), 'external-' || g, 'create', 'succeeded', 'none',
			'sha256:' || md5(g::text), '', '', '',
			?::timestamptz - (g || ' seconds')::interval,
			?::timestamptz - (g || ' seconds')::interval
		FROM generate_series(1, ?) g
	`, now, now, completedSyncItems).Error; err != nil {
		t.Fatalf("seed completed sync items: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_sync_item (
			id, run_id, resource_type, external_scope_key, external_id, action, status,
			conflict_type, upstream_current_hash, upstream_last_synced_hash,
			local_current_hash, local_last_synced_hash, created_at, updated_at
		)
		SELECT 'pg-plan-item-conflict-' || g, 'pg-plan-completed-' || g, 'dataset',
			'pg-plan-conflict-scope', 'pg-plan-conflict-' || g, 'conflict', 'skipped', 'identity',
			'sha256:conflict-' || md5(g::text), '', '', '', ?, ?
		FROM generate_series(1, ?) g
	`, now, now, conflictSyncItems).Error; err != nil {
		t.Fatalf("seed conflict sync items: %v", err)
	}

	tenant := mustCreatePostgresTenant(t, testStore, fmt.Sprintf("PG Operational Query Plan Tenant %dx", scale))
	if err := db.Exec(`
		INSERT INTO rgx_alert_event (
			id, tenant_id, title, severity, type, resource, resource_id, detail,
			fields_json, fingerprint, occurred_at, status, created_at, updated_at
		)
		VALUES ('pg-plan-alert', ?, 'Operational plan baseline', 'error', 'provider.test',
			'model-provider', 'provider-1', 'query plan baseline', '{}', 'pg-plan-alert-fingerprint',
			?, 'open', ?, ?)
	`, tenant.ID, now, now, now).Error; err != nil {
		t.Fatalf("seed alert event: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_alert_delivery (
			alert_event_id, channel, tenant_id, status, attempts, last_attempt_at,
			next_retry_at, lease_owner, lease_generation, created_at, updated_at
		)
		SELECT 'pg-plan-alert', 'succeeded-' || g, ?, 'succeeded', 1,
			?::timestamptz - (g || ' seconds')::interval, NULL, '', 0,
			?::timestamptz - (g || ' seconds')::interval, ?::timestamptz - (g || ' seconds')::interval
		FROM generate_series(1, ?) g
	`, tenant.ID, now, now, now, succeededDeliveries).Error; err != nil {
		t.Fatalf("seed succeeded alert deliveries: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_alert_delivery (
			alert_event_id, channel, tenant_id, status, attempts, last_attempt_at,
			next_retry_at, lease_owner, lease_generation, created_at, updated_at
		)
		SELECT 'pg-plan-alert', 'failed-' || g, ?, 'failed', 1,
			?::timestamptz - (g || ' seconds')::interval, ?::timestamptz - (g || ' seconds')::interval, '', 0,
			?::timestamptz - (g || ' seconds')::interval, ?::timestamptz - (g || ' seconds')::interval
		FROM generate_series(1, ?) g
	`, tenant.ID, now, now, now, now, terminalDeliveries).Error; err != nil {
		t.Fatalf("seed failed alert deliveries: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO rgx_alert_delivery (
			alert_event_id, channel, tenant_id, status, attempts, last_attempt_at,
			next_retry_at, lease_owner, lease_generation, created_at, updated_at
		)
		SELECT 'pg-plan-alert', 'pending-' || g, ?, 'pending', 0,
			?::timestamptz - (g || ' seconds')::interval, ?::timestamptz - (g || ' seconds')::interval, '', 0,
			?::timestamptz - (g || ' seconds')::interval, ?::timestamptz - (g || ' seconds')::interval
		FROM generate_series(1, ?) g
	`, tenant.ID, now, now, now, now, terminalDeliveries).Error; err != nil {
		t.Fatalf("seed pending alert deliveries: %v", err)
	}
	return now
}

func assertPostgresQueryPlanIndexes(t *testing.T, plans map[string]string) {
	t.Helper()
	for queryName, indexName := range postgresOperationalQueryPlanIndexes {
		plan := plans[queryName]
		if !strings.Contains(plan, indexName) {
			t.Fatalf("%s plan did not use %s:\n%s", queryName, indexName, plan)
		}
		if !strings.Contains(plan, "actual time") {
			t.Fatalf("%s plan was not analyzed with runtime timing:\n%s", queryName, plan)
		}
		t.Logf("%s plan:\n%s", queryName, plan)
	}
}

func assertPostgresQueryPlanRuntime(t *testing.T, plans map[string]string) {
	t.Helper()
	for queryName, plan := range plans {
		executionMS, err := postgresPlanExecutionMS(plan)
		if err != nil {
			t.Fatalf("parse %s execution time: %v", queryName, err)
		}
		if executionMS > postgresQueryPlanThresholdMS {
			t.Fatalf("%s execution time %.3fms exceeds %.0fms", queryName, executionMS, postgresQueryPlanThresholdMS)
		}
	}
}

func postgresExplainQueryPlan(t *testing.T, db *gorm.DB, query string, args ...interface{}) string {
	t.Helper()
	lines := []string{}
	if err := db.Raw("EXPLAIN (ANALYZE, FORMAT TEXT, BUFFERS) "+query, args...).Scan(&lines).Error; err != nil {
		t.Fatalf("explain operational query: %v", err)
	}
	return strings.Join(lines, "\n")
}

func postgresPlanExecutionMS(plan string) (float64, error) {
	match := postgresPlanExecutionPattern.FindStringSubmatch(plan)
	if len(match) != 2 {
		return 0, fmt.Errorf("execution time not found in plan")
	}
	return strconv.ParseFloat(match[1], 64)
}

func writePostgresQueryPlanReport(t *testing.T, plans map[string]string, scale int) string {
	t.Helper()
	reportDir := os.Getenv("RGX_QUERY_PLAN_REPORT_DIR")
	if strings.TrimSpace(reportDir) == "" {
		return ""
	}
	if err := os.MkdirAll(reportDir, 0o750); err != nil {
		t.Fatalf("create query plan report directory: %v", err)
	}

	queries := make([]postgresQueryPlanResult, 0, len(plans))
	allIndexesUsed := true
	allWithinThreshold := true
	for queryName, plan := range plans {
		expectedIndex := postgresOperationalQueryPlanIndexes[queryName]
		executionMS, err := postgresPlanExecutionMS(plan)
		if err != nil {
			t.Fatalf("parse %s execution time: %v", queryName, err)
		}
		indexUsed := strings.Contains(plan, expectedIndex)
		withinThreshold := executionMS <= postgresQueryPlanThresholdMS
		allIndexesUsed = allIndexesUsed && indexUsed
		allWithinThreshold = allWithinThreshold && withinThreshold
		queries = append(queries, postgresQueryPlanResult{
			Name: queryName, ExpectedIndex: expectedIndex, IndexUsed: indexUsed,
			ExecutionTimeMS: executionMS, ThresholdMS: postgresQueryPlanThresholdMS,
			WithinThreshold: withinThreshold, Plan: plan,
		})
	}
	scenarioID := postgresQueryPlanScenarioID
	if scale > 10 {
		scenarioID = "SC-PG-002"
	}
	report := postgresQueryPlanReport{
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		ScenarioID:        scenarioID,
		Scale:             scale,
		CompletedSyncRuns: 1200 * scale, ActiveSyncRuns: 25 * scale,
		CompletedSyncItems: 1500 * scale, ConflictSyncItems: 25 * scale,
		SucceededDeliveries: 1500 * scale, FailedDeliveries: 30 * scale,
		PendingDeliveries: 30 * scale, ThresholdMS: postgresQueryPlanThresholdMS,
		AllIndexesUsed: allIndexesUsed, AllWithinThreshold: allWithinThreshold,
		Queries: queries,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal query plan report: %v", err)
	}
	path := filepath.Join(reportDir, fmt.Sprintf("postgres-query-plan-report-%dx.json", scale))
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write query plan report: %v", err)
	}
	return path
}

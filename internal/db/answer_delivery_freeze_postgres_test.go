package db

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ScenarioID: SC-PG-118-F02
// TestAnswerDeliveryFreezeGuardsPostgreSQL proves the six immutable answer
// delivery tables reject UPDATE and DELETE on PostgreSQL (the production
// driver). It exists because migration 102 shipped a freeze trigger whose body
// was `RETURN OLD`, silently allowing every mutation; migration 108 repairs
// the function with RAISE EXCEPTION semantics.
func TestAnswerDeliveryFreezeGuardsPostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL freeze guard regression")
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

	now := time.Now().UTC()
	unique := strings.ToLower(fmt.Sprintf("fz%x", now.UnixNano()))
	seeds := []struct {
		table string
		row   map[string]interface{}
	}{
		{
			table: "rgx_answer_snapshot",
			row: map[string]interface{}{
				"id": unique + "snp", "tenant_id": unique, "answer_run_id": unique + "run",
				"answer_schema_version": "answer.v1", "lifecycle_state": "COMPLETED",
				"answer_status": "ANSWERED", "completion_reason": "NORMAL", "reason_code": "PROBE",
				"content": "freeze probe", "citations_json": "[]", "artifacts_json": "[]",
				"execution_json": "[]", "limitations_json": "[]", "actions_json": "[]",
				"canonical_hash": unique + "hash", "hash_algorithm": "SHA256", "created_at": now,
			},
		},
		{
			table: "rgx_answer_event",
			row: map[string]interface{}{
				"id": unique + "evt", "tenant_id": unique, "answer_run_id": unique + "run",
				"event_id": unique + "eid", "event_seq": int64(1), "event_type": "STATUS",
				"payload_json": "{}", "occurred_at": now,
			},
		},
		{
			table: "rgx_answer_authorization_projection",
			row: map[string]interface{}{
				"id": unique + "prj", "tenant_id": unique, "answer_snapshot_id": unique + "snp",
				"principal_id": unique + "usr", "channel": "web", "policy_version": "authorization.v1",
				"policy_evaluated_at": now, "policy_input_hash": unique + "pih", "decision_hash": unique + "dh",
				"answer_visibility": "VISIBLE", "content_visibility": "VISIBLE", "citation_visibility": "VISIBLE",
				"artifact_visibility": "VISIBLE", "execution_visibility": "VISIBLE",
				"metadata_visibility": "VISIBLE", "actions_visibility": "VISIBLE",
				"redaction_reasons_json": "[]", "created_at": now,
			},
		},
		{
			table: "rgx_export_snapshot",
			row: map[string]interface{}{
				"id": unique + "exs", "tenant_id": unique, "answer_snapshot_ids": `["` + unique + `snp"]`,
				"template_id": unique + "tpl", "template_version": int64(1), "policy_version": "authorization.v1",
				"policy_evaluated_at": now, "policy_input_hash": unique + "pih", "canonical_hash": unique + "ch",
				"payload_json": "{}", "created_at": now,
			},
		},
		{
			table: "rgx_export_artifact",
			row: map[string]interface{}{
				"id": unique + "exa", "tenant_id": unique, "export_job_id": unique + "job",
				"format": "markdown", "renderer_version": "md.v1", "file_ref": "/tmp/freeze-probe.md",
				"filename": "freeze-probe.md", "mime_type": "text/markdown", "byte_size": int64(1),
				"sha256": unique + "sha", "created_at": now, "expires_at": now.Add(time.Hour),
			},
		},
		{
			table: "rgx_export_download_audit",
			row: map[string]interface{}{
				"id": unique + "aud", "tenant_id": unique, "export_artifact_id": unique + "exa",
				"export_job_id": unique + "job", "answer_snapshot_id": unique + "snp",
				"downloaded_by": unique + "usr", "downloaded_at": now, "download_count": int64(1),
				"decision_hash": unique + "dh",
			},
		},
	}

	for _, seed := range seeds {
		seed := seed
		t.Run(seed.table, func(t *testing.T) {
			// INSERT must still succeed: the guard only freezes mutations of
			// existing rows.
			if err := gdb.Table(seed.table).Create(seed.row).Error; err != nil {
				t.Fatalf("seed insert should succeed: %v", err)
			}
			pk, _ := seed.row["id"].(string)

			if err := gdb.Table(seed.table).Where("id = ?", pk).Update("tenant_id", unique+"mutated").Error; err == nil {
				t.Fatalf("%s: UPDATE was silently allowed by the freeze trigger", seed.table)
			} else if !isFreezeViolation(err) {
				t.Fatalf("%s: UPDATE failed for an unexpected reason: %v", seed.table, err)
			}

			if err := gdb.Table(seed.table).Where("id = ?", pk).Delete(nil).Error; err == nil {
				t.Fatalf("%s: DELETE was silently allowed by the freeze trigger", seed.table)
			} else if !isFreezeViolation(err) {
				t.Fatalf("%s: DELETE failed for an unexpected reason: %v", seed.table, err)
			}
		})
	}
}

func isFreezeViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	// RAISE EXCEPTION without a SQLSTATE code surfaces as P0001.
	return strings.Contains(message, "answer delivery rows are immutable") ||
		strings.Contains(message, "p0001")
}

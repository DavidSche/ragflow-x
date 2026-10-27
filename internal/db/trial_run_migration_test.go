package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestMigrationV157TrialRunIsIdempotent guards the v0.4.0 migration contract
// (doc/228 §3.1 T7.3′): exactly one new migration, guarded DDL, re-runnable,
// with the two uniqueness constraints that make runs replay-safe.
func TestMigrationV157TrialRunIsIdempotent(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "trial-run.db")})
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
	for _, table := range []string{"rgx_trial_run", "rgx_trial_case_result"} {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("migration 157 must create %s", table)
		}
	}
	for _, column := range []string{"scenario_template_id", "scenario_template_version"} {
		if !gdb.Migrator().HasColumn(&model.ChatShadow{}, column) {
			t.Fatalf("migration 157 must add rgx_chat.%s", column)
		}
	}

	// Re-running 157 (upgrade from an older binary that stopped mid-way, or a
	// plain re-entry) must stay a no-op.
	for _, step := range migrations {
		if step.Version == 157 {
			if err := step.Migrate(gdb); err != nil {
				t.Fatalf("migration 157 must be re-entrant: %v", err)
			}
		}
	}

	now := time.Now().UTC()
	run := &model.TrialRun{
		ID: "run-1", TenantID: "t1", IdempotencyKey: "key-1",
		ScenarioTemplateID: "tpl-1", AssistantID: "chat-1", AssistantKind: model.TrialAssistantKindChat,
		EvalSetID: "set-1", EvalSetHash: "hash", PolicyID: "trial-retrieval-v1", PolicyHash: "policy",
		Mode: model.TrialModeRetrievalOnly, AuthoringSnapshot: "{}", DatasetIDs: "[]",
		Status: model.TrialRunQueued, Actor: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := *run
	duplicate.ID = "run-2"
	if err := gdb.Create(&duplicate).Error; err == nil {
		t.Fatal("(tenant_id, idempotency_key) must be unique so re-submission cannot fork a run")
	}

	row := model.TrialCaseResult{
		ID: "case-1", TenantID: "t1", RunID: run.ID, CaseID: "eval-case-1",
		Question: "q", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	duplicateCase := row
	duplicateCase.ID = "case-2"
	if err := gdb.Create(&duplicateCase).Error; err == nil {
		t.Fatal("(run_id, case_id) must be unique so one case cannot be recorded twice")
	}
}

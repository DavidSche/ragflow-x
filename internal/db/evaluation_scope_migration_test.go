package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV137EvaluationScopeBackfillAndEvidenceUniqueness(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "evaluation-scope.db")})
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
	now := time.Now().UTC()
	candidateID := "cand"
	run := &model.EvaluationRun{
		ID: "scope-run", TenantID: "t1", ReleaseCandidateID: &candidateID, CandidateVersion: int64Ptr(1),
		EvalSetID: "set", EvalSetVersion: 1, EvalSetHash: "hash",
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation",
		ExecutionSnapshotID: "snapshot", Status: model.EvaluationRunQueued,
		Actor: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec("DROP TRIGGER trg_evaluation_run_scope_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec("UPDATE rgx_evaluation_run SET evaluation_scope = '' WHERE id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	bundle := &model.EvidenceBundle{
		ID: "scope-evidence", TenantID: "t1", ReleaseCandidateID: &candidateID, CandidateVersion: int64Ptr(1),
		EvaluationRunID: run.ID, EvalSetID: "set", EvalSetVersion: 1, EvalSetHash: "hash",
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation",
		SecurityEvidence: "{}", PolicyEvidence: "{}", RiskEvidence: "{}", PermissionEvidence: "{}",
		ConfigurationEvidence: "{}", ApprovalEvidence: "{}", EvidenceItems: "[]",
		HashAlgorithm: model.HashAlgorithmSHA256, BundleHash: "bundle", CreatedBy: "admin", CreatedAt: now,
	}
	if err := gdb.Create(bundle).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := *bundle
	duplicate.ID = "duplicate-evidence"
	if err := gdb.Create(&duplicate).Error; err == nil {
		t.Fatal("expected one evidence bundle per evaluation run")
	}

	var migrationVersion int64
	if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 137).
		Select("COALESCE(SUM(version), 0)").Scan(&migrationVersion).Error; err != nil {
		t.Fatal(err)
	}
	if migrationVersion != 137 {
		t.Fatalf("migration 137 not recorded: %d", migrationVersion)
	}
	for _, step := range migrations {
		if step.Version == 137 {
			if err := step.Migrate(gdb); err != nil {
				t.Fatal(err)
			}
		}
	}
	var loaded model.EvaluationRun
	if err := gdb.First(&loaded, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.EvaluationScope != model.EvaluationScopeRelease || loaded.CandidateVersion == nil || *loaded.CandidateVersion != 1 {
		t.Fatalf("migration did not preserve release scope: %+v", loaded)
	}
	if err := gdb.Exec("INSERT INTO rgx_evaluation_run (id, tenant_id, release_candidate_id, candidate_version, evaluation_scope, eval_set_id, eval_set_version, eval_set_hash, evaluation_policy_version, evaluation_policy_hash, aggregation_policy_version, aggregation_policy_hash, execution_snapshot_id, status, actor, created_at, updated_at) VALUES ('bad-scope', 't1', NULL, NULL, 'compilation_strategy', 'set', 1, 'hash', 'v1', 'hash', 'v1', 'hash', 'snapshot', 'QUEUED', 'admin', ?, ?)", now, now).Error; err == nil {
		t.Fatal("expected strategy run without strategy attribution to violate scope constraint")
	}
}

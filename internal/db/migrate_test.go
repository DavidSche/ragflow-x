package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV49GovernanceGuards(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "guards.db")})
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
	candidate := &model.ReleaseCandidate{
		ID: "candidate", TenantID: "t1", CandidateID: "cand", CandidateVersion: 1,
		TargetType: "assistant", TargetID: "chat-1", TargetVersion: "v1",
		CandidateManifest: "{}", CandidateHash: "hash", HashAlgorithm: "SHA256",
		Status: model.CandidateReadyForEvaluation, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(candidate).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.ReleaseCandidate{}).Where("id = ?", candidate.ID).
		Update("candidate_hash", "tampered").Error; err == nil {
		t.Fatal("expected frozen candidate update to fail")
	}

	snapshot := &model.ExecutionSnapshot{
		ID: "snapshot", TenantID: "t1", ReleaseCandidateID: "cand", CandidateVersion: 1,
		TargetType: "assistant", TargetID: "chat-1", TargetVersion: "v1",
		SnapshotSchemaVersion: "v1", SnapshotHashAlgorithm: "SHA256", SnapshotHash: "hash",
		ExecutionConfig: "{}", CreatedBy: "admin", CreatedAt: now,
	}
	if err := gdb.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(snapshot).Update("snapshot_hash", "tampered").Error; err == nil {
		t.Fatal("expected snapshot update to fail")
	}

	run := &model.EvaluationRun{
		ID: "run", TenantID: "t1", ReleaseCandidateID: "cand", CandidateVersion: 1,
		EvalSetID: "set", EvalSetVersion: 1, EvalSetHash: "hash",
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "hash",
		ExecutionSnapshotID: snapshot.ID, Status: model.EvaluationRunRunning,
		Actor: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	pass := true
	result := &model.EvaluationCaseResult{
		ID: "result", TenantID: "t1", RunID: run.ID, CaseID: "case",
		CaseVersionID: "cv", CaseVersionHash: "hash", ActualAnswer: "answer",
		References: "[]", Metrics: "{}", Pass: &pass, CreatedAt: now,
	}
	if err := gdb.Create(result).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.EvaluationRun{}).Where("id = ?", run.ID).
		Updates(map[string]interface{}{"status": model.EvaluationRunCompleted}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(result).Update("actual_answer", "tampered").Error; err == nil {
		t.Fatal("expected terminal case result update to fail")
	}
	if err := gdb.Delete(result).Error; err == nil {
		t.Fatal("expected terminal case result delete to fail")
	}
	if err := gdb.Model(&model.EvaluationRun{}).Where("id = ?", run.ID).
		Update("evaluation_policy_hash", "tampered").Error; err == nil {
		t.Fatal("expected terminal evaluation run update to fail")
	}
	if err := gdb.Delete(&model.EvaluationRun{}, "id = ?", run.ID).Error; err == nil {
		t.Fatal("expected terminal evaluation run delete to fail")
	}

	subStates := "{}"
	first := &model.ReleaseGateDecision{
		ID: "gate-1", TenantID: "t1", ReleaseCandidateID: "cand", CandidateVersion: 1,
		DecisionVersion: 1, EvidenceBundleID: "evidence", Environment: "production",
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "hash",
		SubGateStates: subStates, Decision: model.GateDecisionPass, ActiveGate: true,
		Actor: "admin", CreatedAt: now,
	}
	second := &model.ReleaseGateDecision{
		ID: "gate-2", TenantID: "t1", ReleaseCandidateID: "cand", CandidateVersion: 1,
		DecisionVersion: 2, EvidenceBundleID: "evidence", Environment: "production",
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "hash",
		SubGateStates: subStates, Decision: model.GateDecisionPass, ActiveGate: true,
		Actor: "admin", CreatedAt: now,
	}
	if err := gdb.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(second).Error; err == nil {
		t.Fatal("expected second active gate to violate partial unique index")
	}
}

func TestMigrationV55TenantTypeAndPlatformGuard(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "tenant-guards.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var platform model.Tenant
	if err := gdb.Where("id = ?", model.PlatformTenantID).First(&platform).Error; err != nil {
		t.Fatalf("platform tenant missing: %v", err)
	}
	if platform.Type != model.TenantTypePlatform || platform.Status != model.TenantStatusActive {
		t.Fatalf("invalid platform tenant: %+v", platform)
	}

	workspace := &model.Tenant{ID: "workspace", Name: "Workspace", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive}
	if err := gdb.Create(workspace).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := &model.Tenant{ID: "second-platform", Name: "Second Platform", Type: model.TenantTypePlatform, Status: model.TenantStatusActive}
	if err := gdb.Create(duplicate).Error; err == nil {
		t.Fatal("expected second platform tenant to violate singleton index")
	}
	if err := gdb.Model(&model.Tenant{}).Where("id = ?", model.PlatformTenantID).
		Update("type", model.TenantTypeWorkspace).Error; err == nil {
		t.Fatal("expected platform type transition to fail")
	}
	if err := gdb.Model(&model.Tenant{}).Where("id = ?", model.PlatformTenantID).
		Update("status", model.TenantStatusDisabled).Error; err == nil {
		t.Fatal("expected platform status transition to fail")
	}
	if err := gdb.Delete(&model.Tenant{}, "id = ?", model.PlatformTenantID).Error; err == nil {
		t.Fatal("expected platform tenant delete to fail")
	}
	if err := gdb.Model(&model.Tenant{}).Where("id = ?", workspace.ID).
		Update("status", model.TenantStatusDisabled).Error; err != nil {
		t.Fatalf("workspace status update should be allowed: %v", err)
	}
}

func TestMigrationV70WorkspaceRoleTerms(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "role-terms.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var role model.Role
	if err := gdb.Where("id = ?", model.RoleTenantAdmin).First(&role).Error; err != nil {
		t.Fatalf("tenant admin role missing: %v", err)
	}
	if role.Name != "工作区管理员" || role.Description != "工作区内管理" {
		t.Fatalf("unexpected tenant admin role terms: %+v", role)
	}
}

func TestMigrationV87OperatorRoleTerms(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "operator-role-terms.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var role model.Role
	if err := gdb.Where("id = ?", model.RoleOperator).First(&role).Error; err != nil {
		t.Fatalf("operator role missing: %v", err)
	}
	if role.Name != "内容运营员" || role.Description != "工作区内内容与业务运营" {
		t.Fatalf("unexpected operator role terms: %+v", role)
	}
}

func TestMigrationV88BusinessUserRole(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "business-user-role.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var role model.Role
	if err := gdb.Where("id = ?", model.RoleBusinessUser).First(&role).Error; err != nil {
		t.Fatalf("business user role missing: %v", err)
	}
	if role.Name != "业务使用者" || role.Scope != model.RoleScopeTenant ||
		role.Description != "使用助手并维护数据集文档" {
		t.Fatalf("unexpected business user role: %+v", role)
	}

	var permissions []model.Permission
	if err := gdb.Where("role_id = ?", model.RoleBusinessUser).Order("action, resource").Find(&permissions).Error; err != nil {
		t.Fatal(err)
	}
	grants := map[string]bool{}
	for _, permission := range permissions {
		grants[permission.Action+"|"+permission.Resource] = true
	}
	expected := []string{
		"append|document", "delete:own|document", "execute|agent", "execute|assistant", "execute|chat",
		"execute|memory", "execute|search-app", "read|agent", "read|assistant",
		"read|chat", "read|dataset", "read|document", "read|memory",
		"read|search-app", "read|user", "session:create|agent",
	}
	if len(permissions) != len(expected) {
		t.Fatalf("expected %d business user permissions, got %d: %+v", len(expected), len(permissions), permissions)
	}
	for _, grant := range expected {
		if !grants[grant] {
			t.Fatalf("business user permission %q missing", grant)
		}
	}
	for _, denied := range []string{
		"manage|agent", "manage|chat", "manage|dataset", "manage|document",
		"manage|prompt-policy", "manage|role", "manage|user", "session:create|chat",
	} {
		if grants[denied] {
			t.Fatalf("business user unexpectedly has %q", denied)
		}
	}

	if !gdb.Migrator().HasTable(&model.DocumentOwnershipLink{}) {
		t.Fatal("document ownership table missing")
	}
}

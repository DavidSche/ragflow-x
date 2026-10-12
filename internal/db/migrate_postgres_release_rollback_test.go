package db

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestPostgresReleaseRollbackContract(t *testing.T) {
	baseDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if strings.TrimSpace(baseDSN) == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL release rollback contract")
	}

	dumpBinary := os.Getenv("RGX_TEST_PG_DUMP_BIN")
	if dumpBinary == "" {
		dumpBinary = "pg_dump"
	}
	restoreBinary := os.Getenv("RGX_TEST_PG_RESTORE_BIN")
	if restoreBinary == "" {
		restoreBinary = "pg_restore"
	}
	if _, err := exec.LookPath(dumpBinary); err != nil {
		t.Skipf("pg_dump unavailable: %v", err)
	}
	if _, err := exec.LookPath(restoreBinary); err != nil {
		t.Skipf("pg_restore unavailable: %v", err)
	}

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping postgres admin: %v", err)
	}

	nonce := strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()))
	targetName := "rgx_release_rollback_" + nonce
	targetDSN := swapPostgresDatabase(baseDSN, targetName)
	t.Cleanup(func() {
		_ = dropPostgresDatabase(admin, targetName)
	})
	_ = dropPostgresDatabase(admin, targetName)
	if err := createPostgresDatabase(admin, targetName, ""); err != nil {
		t.Fatalf("create migration target: %v", err)
	}

	baselineVersion := migrations[len(migrations)-2].Version
	latestVersion := migrations[len(migrations)-1].Version
	target, err := openPostgresContractDB(targetDSN)
	if err != nil {
		t.Fatalf("open migration target: %v", err)
	}
	if err := MigrateTo(target, baselineVersion); err != nil {
		t.Fatalf("migrate to baseline: %v", err)
	}

	now := time.Now().UTC()
	workspace := &model.Tenant{
		ID: "releaserollbackworkspace00000", Name: "Release Rollback Workspace",
		Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := target.Create(workspace).Error; err != nil {
		t.Fatalf("seed baseline workspace: %v", err)
	}
	operator := &model.User{
		ID: "releaserollbackoperator000", TenantID: workspace.ID,
		Username: "release-rollback-operator", PasswordHash: "bcrypt:rollback-contract",
		Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := target.Create(operator).Error; err != nil {
		t.Fatalf("seed baseline business user: %v", err)
	}
	if err := closePostgresContractDB(target); err != nil {
		t.Fatalf("close baseline target: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "release-baseline.dump")
	if err := runPostgresTool(t, dumpBinary, []string{
		"--format=custom",
		"--serializable-deferrable",
		"--file=" + backupPath,
		targetDSN,
	}); err != nil {
		t.Fatalf("create pg_dump baseline: %v", err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat pg_dump baseline: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("pg_dump baseline is empty")
	}

	if err := dropPostgresDatabase(admin, targetName); err != nil {
		t.Fatalf("drop upgraded target: %v", err)
	}
	if err := createPostgresDatabase(admin, targetName, ""); err != nil {
		t.Fatalf("create restore target: %v", err)
	}
	if err := runPostgresTool(t, restoreBinary, []string{
		"--exit-on-error",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--dbname=" + targetDSN,
		backupPath,
	}); err != nil {
		t.Fatalf("restore pg_dump baseline: %v", err)
	}

	target, err = openPostgresContractDB(targetDSN)
	if err != nil {
		t.Fatalf("open restored target: %v", err)
	}
	var restoredVersion int64
	if err := target.Model(&model.SchemaVersion{}).
		Select("COALESCE(MAX(version), 0)").Scan(&restoredVersion).Error; err != nil {
		t.Fatalf("read restored schema version: %v", err)
	}
	if restoredVersion != baselineVersion {
		t.Fatalf("restored schema version = %d, want %d", restoredVersion, baselineVersion)
	}
	assertReleaseRollbackBusinessData(t, target, workspace.ID, operator.ID, 1)
	if target.Migrator().HasIndex("rgx_compilation_run", "idx_rgx_compilation_recovery_state") {
		t.Fatal("restored snapshot must not contain post-upgrade schema")
	}

	if err := MigrateTo(target, latestVersion); err != nil {
		t.Fatalf("re-apply latest after rollback: %v", err)
	}
	if !target.Migrator().HasIndex("rgx_compilation_run", "idx_rgx_compilation_recovery_state") {
		t.Fatal("latest migration schema is missing after recovery")
	}
	assertReleaseRollbackBusinessData(t, target, workspace.ID, operator.ID, 1)
	if err := closePostgresContractDB(target); err != nil {
		t.Fatalf("close recovered target: %v", err)
	}
}

func assertReleaseRollbackBusinessData(t *testing.T, target *gorm.DB, workspaceID string, operatorID string, expected int64) {
	t.Helper()

	var workspaceCount int64
	if err := target.Model(&model.Tenant{}).Where("id = ?", workspaceID).
		Count(&workspaceCount).Error; err != nil {
		t.Fatalf("count restored workspace: %v", err)
	}
	if workspaceCount != expected {
		t.Fatalf("restored workspace count = %d, want %d", workspaceCount, expected)
	}
	var operatorCount int64
	if err := target.Model(&model.User{}).Where("id = ?", operatorID).
		Count(&operatorCount).Error; err != nil {
		t.Fatalf("count restored business user: %v", err)
	}
	if operatorCount != expected {
		t.Fatalf("restored business user count = %d, want %d", operatorCount, expected)
	}
}

func runPostgresTool(t *testing.T, binary string, args []string) error {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "PGAPPNAME=rgx-release-rollback-contract")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return fmt.Errorf("%s failed: %v", filepath.Base(binary), err)
	}
	return fmt.Errorf("%s failed: %s", filepath.Base(binary), trimmed)
}

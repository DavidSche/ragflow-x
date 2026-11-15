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

// logStage marks the progress of the long dump/restore pipeline so a CI run's
// captured output identifies exactly which external operation failed without
// needing the full job log. doc/283 §4.
func logStage(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	t.Logf("[release-rollback] "+format, args...)
}

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

	// Surface the exact toolchain so host/client version skew (a CI runner
	// image drift can silently change the bundled pg_dump/pg_restore) is visible
	// in captured output instead of surfacing as an opaque dump failure.
	for _, b := range []string{dumpBinary, restoreBinary} {
		if vOut, vErr := exec.Command(b, "--version").Output(); vErr == nil {
			t.Logf("[release-rollback] %s version: %s", filepath.Base(b), strings.TrimSpace(string(vOut)))
		}
	}

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping postgres admin: %v", err)
	}

	logStage(t, "admin connected; preparing isolated target database")
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
	logStage(t, "migrating isolated target to baseline %d", baselineVersion)
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

	// The baseline dump is an idempotent read of an untouched schema, so a
	// transient external-process failure (FS/pipe/timing under CI load) is safe
	// to retry once; a schema regression produces the same failure on retry and
	// still fails loudly with both attempts' output. doc/283 §4.
	backupPath := filepath.Join(t.TempDir(), "release-baseline.dump")
	var dumpErr error
	logStage(t, "creating pg_dump baseline (custom, serializable-deferrable)")
	for attempt := 1; attempt <= 2; attempt++ {
		dumpErr = runPostgresTool(t, dumpBinary, []string{
			"--format=custom",
			"--serializable-deferrable",
			"--file=" + backupPath,
			targetDSN,
		})
		if dumpErr == nil {
			break
		}
		if attempt == 1 {
			t.Logf("[release-rollback] pg_dump attempt 1 failed, retrying: %v", dumpErr)
		}
	}
	if dumpErr != nil {
		t.Fatalf("create pg_dump baseline (after retry): %v", dumpErr)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat pg_dump baseline: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("pg_dump baseline is empty")
	}
	logStage(t, "pg_dump baseline ok (%d bytes); rebuilding target from dump", info.Size())

	if err := dropPostgresDatabase(admin, targetName); err != nil {
		t.Fatalf("drop upgraded target: %v", err)
	}
	if err := createPostgresDatabase(admin, targetName, ""); err != nil {
		t.Fatalf("create restore target: %v", err)
	}
	logStage(t, "restoring baseline from dump (exit-on-error, no-owner)")
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
	logStage(t, "restore ok; verifying restored schema/data")
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
	// Post-upgrade schema must be absent from the restored baseline. Column
	// lengths are NOT valid markers here: migrations that AutoMigrate the
	// current model build fresh baselines with up-to-date column sizes, so a
	// baseline built from current code may already carry widened columns. The
	// authoritative marker is the applied-version ledger itself: no migration
	// newer than the baseline may be recorded in the restored snapshot.
	var futureVersions int64
	if err := target.Model(&model.SchemaVersion{}).
		Where("version > ?", baselineVersion).Count(&futureVersions).Error; err != nil {
		t.Fatalf("count restored future schema versions: %v", err)
	}
	if futureVersions != 0 {
		t.Fatalf("restored baseline records %d post-upgrade schema versions, want 0", futureVersions)
	}

	logStage(t, "baseline restored at version %d; re-applying latest %d", restoredVersion, latestVersion)
	if err := MigrateTo(target, latestVersion); err != nil {
		t.Fatalf("re-apply latest after rollback: %v", err)
	}
	var recoveredVersion int64
	if err := target.Model(&model.SchemaVersion{}).
		Select("COALESCE(MAX(version), 0)").Scan(&recoveredVersion).Error; err != nil {
		t.Fatalf("read recovered schema version: %v", err)
	}
	if recoveredVersion != latestVersion {
		t.Fatalf("recovered schema version = %d, want %d", recoveredVersion, latestVersion)
	}
	assertReleaseRollbackBusinessData(t, target, workspace.ID, operator.ID, 1)
	logStage(t, "recovery ok at version %d; contract complete", recoveredVersion)
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
		logStage(t, "%s failed: %v", filepath.Base(binary), err)
		return fmt.Errorf("%s failed: %v", filepath.Base(binary), err)
	}
	// Re-emit the external tool's stderr through the S-prefix stage marker so
	// the failure lands in the first annotation window (doc/283 §4): the
	// T-prefix assertion lines carry it too, but GitHub surfaces only a small
	// window and marks grab the first slots.
	logStage(t, "%s failed: %s", filepath.Base(binary), strings.ReplaceAll(trimmed, "\n", " · "))
	return fmt.Errorf("%s failed: %s", filepath.Base(binary), trimmed)
}

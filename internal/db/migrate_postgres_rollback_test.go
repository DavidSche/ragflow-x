package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestPostgresMigrationSnapshotRollbackContract(t *testing.T) {
	baseDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if strings.TrimSpace(baseDSN) == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL migration snapshot rollback contract")
	}

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping postgres admin: %v", err)
	}

	nonce := strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()))
	targetName := "rgx_migration_rollback_" + nonce
	backupName := targetName + "_backup"
	targetDSN := swapPostgresDatabase(baseDSN, targetName)
	t.Cleanup(func() {
		_ = dropPostgresDatabase(admin, targetName)
		_ = dropPostgresDatabase(admin, backupName)
	})
	_ = dropPostgresDatabase(admin, targetName)
	_ = dropPostgresDatabase(admin, backupName)
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
	marker := &model.Tenant{
		ID: "migrationrollbackmarker00000000", Name: "Migration Rollback Marker",
		Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := target.Create(marker).Error; err != nil {
		t.Fatalf("seed baseline marker: %v", err)
	}
	if err := closePostgresContractDB(target); err != nil {
		t.Fatalf("close baseline target: %v", err)
	}

	if err := createPostgresDatabase(admin, backupName, targetName); err != nil {
		t.Fatalf("create baseline snapshot: %v", err)
	}
	target, err = openPostgresContractDB(targetDSN)
	if err != nil {
		t.Fatalf("reopen target for upgrade: %v", err)
	}
	if err := MigrateTo(target, latestVersion); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}
	if !target.Migrator().HasColumn("rgx_evaluation_run", "capability") {
		t.Fatal("latest migration did not create evaluation capability column")
	}
	upgradeMarker := &model.Tenant{
		ID: "migrationupgrademarker000000000", Name: "Upgrade Marker",
		Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := target.Create(upgradeMarker).Error; err != nil {
		t.Fatalf("seed upgrade marker: %v", err)
	}
	if err := closePostgresContractDB(target); err != nil {
		t.Fatalf("close upgraded target: %v", err)
	}

	if err := dropPostgresDatabase(admin, targetName); err != nil {
		t.Fatalf("drop upgraded target: %v", err)
	}
	if err := createPostgresDatabase(admin, targetName, backupName); err != nil {
		t.Fatalf("restore baseline snapshot: %v", err)
	}
	target, err = openPostgresContractDB(targetDSN)
	if err != nil {
		t.Fatalf("reopen restored target: %v", err)
	}
	var restoredVersion int64
	if err := target.Model(&model.SchemaVersion{}).
		Select("COALESCE(MAX(version), 0)").Scan(&restoredVersion).Error; err != nil {
		t.Fatalf("read restored schema version: %v", err)
	}
	if restoredVersion != baselineVersion {
		t.Fatalf("restored schema version = %d, want %d", restoredVersion, baselineVersion)
	}
	var markerCount int64
	if err := target.Model(&model.Tenant{}).Where("id = ?", marker.ID).
		Count(&markerCount).Error; err != nil {
		t.Fatalf("count restored marker: %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("restored marker count = %d, want 1", markerCount)
	}
	var upgradeMarkerCount int64
	if err := target.Model(&model.Tenant{}).Where("id = ?", upgradeMarker.ID).Count(&upgradeMarkerCount).Error; err != nil {
		t.Fatalf("count post-upgrade marker: %v", err)
	}
	if upgradeMarkerCount != 0 {
		t.Fatal("restored snapshot must not contain post-upgrade data")
	}

	if err := MigrateTo(target, latestVersion); err != nil {
		t.Fatalf("re-apply latest after rollback: %v", err)
	}
	if !target.Migrator().HasColumn("rgx_evaluation_run", "capability") || !target.Migrator().HasIndex("rgx_evaluation_run", "idx_rgx_evaluation_run_capability") {
		t.Fatal("latest migration schema is missing after recovery")
	}
	if err := closePostgresContractDB(target); err != nil {
		t.Fatalf("close recovered target: %v", err)
	}
}

func openPostgresContractDB(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

func closePostgresContractDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}
	return nil
}

func createPostgresDatabase(admin *sql.DB, name, template string) error {
	if !validPostgresIdentifier(name) {
		return fmt.Errorf("invalid postgres database name %q", name)
	}
	query := "CREATE DATABASE " + name
	if strings.TrimSpace(template) != "" {
		if !validPostgresIdentifier(template) {
			return fmt.Errorf("invalid postgres template name %q", template)
		}
		query += " TEMPLATE " + template
	}
	_, err := admin.Exec(query)
	return err
}

func dropPostgresDatabase(admin *sql.DB, name string) error {
	if !validPostgresIdentifier(name) {
		return fmt.Errorf("invalid postgres database name %q", name)
	}
	_, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	return err
}

func validPostgresIdentifier(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func swapPostgresDatabase(dsn, dbname string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" && strings.Contains(parsed.Scheme, "postgres") {
		parsed.Path = "/" + dbname
		return parsed.String()
	}
	fields := strings.Fields(dsn)
	out := make([]string, 0, len(fields)+1)
	replaced := false
	for _, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "dbname=") {
			out = append(out, "dbname="+dbname)
			replaced = true
			continue
		}
		out = append(out, field)
	}
	if !replaced {
		out = append(out, "dbname="+dbname)
	}
	return strings.Join(out, " ")
}

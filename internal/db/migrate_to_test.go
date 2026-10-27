package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrateToAppliesBoundedPendingMigrations(t *testing.T) {
	gdb, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "bounded-migration.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	baseline := LatestMigrationVersion() - 1
	latest := LatestMigrationVersion()
	if err := MigrateTo(gdb, baseline); err != nil {
		t.Fatalf("migrate to baseline: %v", err)
	}
	var version int64
	if err := gdb.Model(&model.SchemaVersion{}).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != baseline {
		t.Fatalf("baseline schema version = %d, want %d", version, baseline)
	}

	if err := MigrateTo(gdb, 999); err == nil {
		t.Fatal("unknown migration target must fail closed")
	}
	if err := MigrateTo(gdb, latest); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}
	if err := gdb.Model(&model.SchemaVersion{}).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != latest {
		t.Fatalf("latest schema version = %d, want %d", version, latest)
	}
}

package db

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

type migrationRollbackProbe struct {
	ID    string `gorm:"primaryKey;size:64"`
	Value string `gorm:"size:255"`
}

func (migrationRollbackProbe) TableName() string { return "migration_rollback_probe_rows" }

func TestMigrationFailureRollsBackSchemaAndVersion(t *testing.T) {
	gdb, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "migration-rollback.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	originalMigrations := migrations
	failedVersion := LatestMigrationVersion() + 1
	migrations = append(append([]Migration{}, originalMigrations...), Migration{
		Version: failedVersion,
		Name:    "test_failed_transactional_step",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&migrationRollbackProbe{}); err != nil {
				return err
			}
			if err := tx.Create(&migrationRollbackProbe{ID: "rollback-probe", Value: "partial"}).Error; err != nil {
				return err
			}
			return errors.New("forced migration failure")
		},
	})
	t.Cleanup(func() { migrations = originalMigrations })

	if err := Migrate(gdb); err == nil {
		t.Fatal("expected forced migration failure")
	}

	var recordedVersions int64
	if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", failedVersion).
		Count(&recordedVersions).Error; err != nil {
		t.Fatal(err)
	}
	if recordedVersions != 0 {
		t.Fatalf("failed migration version must not be recorded: %d", recordedVersions)
	}

	var probeTables int64
	if err := gdb.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_rollback_probe_rows'").
		Scan(&probeTables).Error; err != nil {
		t.Fatal(err)
	}
	if probeTables != 0 {
		t.Fatal("failed migration schema must be rolled back")
	}

	migrations = originalMigrations
	if err := Migrate(gdb); err != nil {
		t.Fatalf("recovery migration after rollback: %v", err)
	}
}

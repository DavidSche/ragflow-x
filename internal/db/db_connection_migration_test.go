package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV120DBConnection(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "db-connection.db")})
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
	if !gdb.Migrator().HasTable(&model.DBConnection{}) {
		t.Fatal("rgx_db_connection is missing")
	}
	if !gdb.Migrator().HasIndex(&model.DBConnection{}, "idx_rgx_db_connection_lookup") {
		t.Fatal("db connection lookup index is missing")
	}
}

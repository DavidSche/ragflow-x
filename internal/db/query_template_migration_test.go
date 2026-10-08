package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV121QueryTemplate(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "query-template.db")})
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
	if !gdb.Migrator().HasTable(&model.QueryTemplate{}) {
		t.Fatal("rgx_query_template is missing")
	}
	if !gdb.Migrator().HasIndex(&model.QueryTemplate{}, "idx_rgx_query_template_lookup") {
		t.Fatal("query template lookup index is missing")
	}
	if !gdb.Migrator().HasIndex(&model.QueryTemplate{}, "uq_rgx_query_template_tenant_name") {
		t.Fatal("query template tenant/name unique index is missing")
	}
}

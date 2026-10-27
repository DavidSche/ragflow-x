package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV119SourceRoutingRule(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "source-routing-rule.db")})
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
	if !gdb.Migrator().HasTable(&model.SourceRoutingRule{}) {
		t.Fatal("rgx_source_routing_rule is missing")
	}
	if !gdb.Migrator().HasIndex(&model.SourceRoutingRule{}, "idx_rgx_source_routing_rule_lookup") {
		t.Fatal("source routing rule lookup index is missing")
	}
}

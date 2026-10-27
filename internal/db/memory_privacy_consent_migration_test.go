package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestMigrationV144MemoryPrivacyConsentGovernance(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-privacy-consent.db")})
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
	for _, table := range []string{"rgx_memory_consent", "rgx_memory_privacy_exemption"} {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("migration 144 missing table %s", table)
		}
	}
	for _, column := range []string{"pii_action", "pii_labels", "pii_rule_version", "forget_at"} {
		if !gdb.Migrator().HasColumn("rgx_memory_operation", column) {
			t.Fatalf("migration 144 missing memory operation column %s", column)
		}
	}
	for _, index := range []string{
		"idx_rgx_memory_consent_policy", "idx_rgx_memory_consent_project",
		"idx_rgx_memory_privacy_exemption_lookup", "idx_rgx_memory_operation_forget",
	} {
		if !gdb.Migrator().HasIndex("rgx_memory_consent", index) &&
			!gdb.Migrator().HasIndex("rgx_memory_privacy_exemption", index) &&
			!gdb.Migrator().HasIndex("rgx_memory_operation", index) {
			t.Fatalf("migration 144 missing index %s", index)
		}
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 144 must be idempotent: %v", err)
	}
}

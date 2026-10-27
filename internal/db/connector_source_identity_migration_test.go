package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/connectorid"
	"gorm.io/gorm"
)

// doc/227 C2.3: migration 156 gives every legacy connector a stable ownership
// identity, backfills credential generations, and enforces one logical source
// per tenant + target dataset through a unique index.
func TestConnectorSourceIdentityMigration(t *testing.T) {
	gdb := openConnectorIdentityDB(t)
	if err := MigrateTo(gdb, 155); err != nil {
		t.Fatalf("migrate to 155: %v", err)
	}
	insertLegacyConnector(t, gdb, "legacy-with-credential", "/srv/docs-a", true, time.Unix(1, 0))
	insertLegacyConnector(t, gdb, "legacy-without-credential", "/srv/docs-b", false, time.Unix(2, 0))

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	wantKeys := make(map[string]string, 2)
	for id, wantVersion := range map[string]int{
		"legacy-with-credential":    1,
		"legacy-without-credential": 0,
	} {
		got := readConnector(t, gdb, id)
		if !strings.HasPrefix(got.SourceOwnershipKey, "src:v1:") {
			t.Fatalf("%s ownership key = %q, want canonical identity", id, got.SourceOwnershipKey)
		}
		if got.SourceOwnershipKey != connectorid.OwnershipKey("tenant-a", "local_folder", "ds-1", map[string]any{
			"root": map[string]string{
				"legacy-with-credential":    "/srv/docs-a",
				"legacy-without-credential": "/srv/docs-b",
			}[id],
		}) {
			t.Fatalf("%s ownership key does not match runtime canonicalization", id)
		}
		if got.AdapterKind != model.ConnectorAdapterNative {
			t.Fatalf("%s adapter = %q", id, got.AdapterKind)
		}
		if got.CredentialVersion != wantVersion {
			t.Fatalf("%s credential version = %d, want %d", id, got.CredentialVersion, wantVersion)
		}
		wantKeys[id] = got.SourceOwnershipKey
	}
	if !gdb.Migrator().HasIndex(&model.Connector{}, "uk_connector_ownership") {
		t.Fatal("unique ownership index missing after migration 156")
	}
	// The index must reject a second active binding for the same identity.
	if err := insertActiveConnectorErr(gdb, "duplicate", wantKeys["legacy-with-credential"]); err == nil {
		t.Fatal("duplicate tenant + source ownership key must be rejected")
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 156 must be idempotent: %v", err)
	}
	for id, want := range wantKeys {
		if got := readConnector(t, gdb, id); got.SourceOwnershipKey != want {
			t.Fatalf("%s key changed on re-run: %q != %q", id, got.SourceOwnershipKey, want)
		}
	}
}

// A pre-156 database may already contain duplicate active bindings for one
// source. The migration must not delete audit history or fail to create the
// unique index: the oldest row keeps the canonical key, later rows get a
// legacy key and fail closed (409) if edited to the same source again.
func TestConnectorSourceIdentityMigrationDedupesLegacyDuplicates(t *testing.T) {
	gdb := openConnectorIdentityDB(t)
	if err := MigrateTo(gdb, 155); err != nil {
		t.Fatalf("migrate to 155: %v", err)
	}
	insertLegacyConnector(t, gdb, "oldest", "/srv/shared", true, time.Unix(1, 0))
	insertLegacyConnector(t, gdb, "newer", "/srv/shared/", false, time.Unix(2, 0))

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate to head with duplicate legacy source: %v", err)
	}
	oldest := readConnector(t, gdb, "oldest")
	newer := readConnector(t, gdb, "newer")
	if !strings.HasPrefix(oldest.SourceOwnershipKey, "src:v1:") {
		t.Fatalf("oldest must keep canonical key, got %q", oldest.SourceOwnershipKey)
	}
	if newer.SourceOwnershipKey != "legacy:newer" {
		t.Fatalf("newer duplicate must get a legacy key, got %q", newer.SourceOwnershipKey)
	}
	if !gdb.Migrator().HasIndex(&model.Connector{}, "uk_connector_ownership") {
		t.Fatal("unique ownership index missing after dedupe migration")
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("dedupe migration must be idempotent: %v", err)
	}
	if got := readConnector(t, gdb, "newer"); got.SourceOwnershipKey != "legacy:newer" {
		t.Fatalf("legacy key changed on re-run: %q", got.SourceOwnershipKey)
	}
}

func openConnectorIdentityDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "connector-identity.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return gdb
}

func insertLegacyConnector(t *testing.T, gdb *gorm.DB, id, root string, hasCredential bool, createdAt time.Time) {
	t.Helper()
	if err := gdb.Exec(`INSERT INTO rgx_connector
		(id, tenant_id, name, provider, target_dataset_id, spec_json, credential_crypted,
		 has_credential, cursor_json, schedule_kind, schedule_value, schedule_tz_offset_min,
		 sensitive, enabled, status, last_run_status, created_by, created_at, updated_at)
		VALUES (?, 'tenant-a', ?, 'local_folder', 'ds-1', ?, '', ?, '{}',
		        'manual', 0, 480, 0, 1, 'idle', '', 'legacy', ?, ?)`,
		id, id, `{"root":"`+root+`"}`, hasCredential, createdAt, createdAt).Error; err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func insertActiveConnectorErr(gdb *gorm.DB, id, ownershipKey string) error {
	return gdb.Exec(`INSERT INTO rgx_connector
		(id, tenant_id, name, provider, adapter_kind, source_ownership_key, target_dataset_id,
		 spec_json, credential_crypted, has_credential, credential_version, cursor_json,
		 schedule_kind, schedule_value, schedule_tz_offset_min, sensitive, enabled, status,
		 last_run_status, created_by, external_connector_id)
		VALUES (?, 'tenant-a', 'dup', 'local_folder', 'native', ?, 'ds-1', '{}', '', 0, 0, '{}',
		        'manual', 0, 480, 0, 1, 'idle', '', 'legacy', '')`, id, ownershipKey).Error
}

func readConnector(t *testing.T, gdb *gorm.DB, id string) model.Connector {
	t.Helper()
	var got model.Connector
	if err := gdb.First(&got, "id = ?", id).Error; err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return got
}

package db

import (
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Migration 190 adds the metering intent table and the idempotency ledger's
// `metering_state` column (doc/304 §4 E147). Two properties have to hold for the
// upgrade, not just for a fresh install:
//
//   - rows completed before the column existed read as `not_tracked`, never as
//     `recorded`. History is not a reading about metering.
//   - the intent table carries a unique request_id, because that uniqueness is
//     what makes a recovery replay a no-op instead of a second charge.
func TestMigrationV190AddsMeteringIntentAndState(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/metering-intent.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := MigrateTo(gdb, 189); err != nil {
		t.Fatalf("migrate to 189: %v", err)
	}

	now := time.Now().UTC()
	// A completion written by the pre-190 code: no metering_state column existed,
	// so the ledger could not have recorded whether the books were written.
	err = gdb.Exec(`INSERT INTO rgx_gateway_idempotency
		(id, tenant_id, principal_id, idempotency_key, request_fingerprint, status, request_id, response_ref, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"idem-old", "tenant-1", "user-1", "key-old", "fingerprint", model.IdempotencyCompleted,
		"req-old", `{"ok":true}`, now, now.Add(time.Hour)).Error
	if err != nil {
		t.Fatalf("seed pre-190 completion: %v", err)
	}

	if err := MigrateTo(gdb, 190); err != nil {
		t.Fatalf("migrate to 190: %v", err)
	}
	if LatestMigrationVersion() < 190 {
		t.Fatalf("latest migration = %d, want at least 190", LatestMigrationVersion())
	}
	if !gdb.Migrator().HasTable(&model.MeteringIntent{}) {
		t.Fatal("rgx_metering_intent was not created")
	}

	var old model.GatewayIdempotency
	if err := gdb.Where("id = ?", "idem-old").First(&old).Error; err != nil {
		t.Fatalf("read pre-190 row: %v", err)
	}
	if old.MeteringState != model.MeteringNotTracked {
		t.Fatalf("pre-190 completion metering_state = %q, want %q: history must not read as recorded",
			old.MeteringState, model.MeteringNotTracked)
	}

	// The unique key is the whole safety property of the replay: a second intent
	// for the same request cannot be created.
	intent := &model.MeteringIntent{
		ID: "intent-1", TenantID: "tenant-1", RequestID: "req-1", UserID: "user-1",
		Date: "2026-11-25", TokensIn: 10, Requests: 1, State: model.MeteringPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(intent).Error; err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	dup := *intent
	dup.ID = "intent-2"
	if err := gdb.Create(&dup).Error; err == nil {
		t.Fatal("a second intent for the same request_id was accepted; the replay key is not unique")
	}
}

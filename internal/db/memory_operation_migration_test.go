package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

func TestP0_DBMIG_131_MemoryOperationOutboxIndexes(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-outbox-contract.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(gdb); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	for _, name := range []string{
		"uidx_rgx_memory_operation_request",
		"uidx_rgx_memory_operation_idempotency",
	} {
		if !gdb.Migrator().HasIndex(&model.MemoryOperation{}, name) {
			t.Fatalf("migration 131 must create index %s", name)
		}
		for _, field := range []string{"TombstoneScopeHash", "TombstonePayloadHash", "TombstoneMessageHash", "TombstoneCreatedAt"} {
			if !gdb.Migrator().HasColumn(&model.MemoryOperation{}, field) {
				t.Fatalf("migration 132 must add %s", field)
			}
		}
		if !gdb.Migrator().HasIndex(&model.MemoryOperation{}, "idx_rgx_memory_operation_tombstone") {
			t.Fatal("migration 132 must create tombstone observation index")
		}
		for _, role := range []string{model.RoleBusinessUser, model.RoleTeamAdmin, model.RolePlatformAdmin, model.RoleTenantAdmin} {
			var count int64
			if err := gdb.Model(&model.Permission{}).Where(
				"role_id = ? AND action = ? AND resource = ?", role, "execute", "memory-forget",
			).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("migration 132 must grant execute:memory-forget to %s", role)
			}
		}
		for _, role := range []string{model.RoleOperator, model.RoleViewer} {
			var count int64
			if err := gdb.Model(&model.Permission{}).Where(
				"role_id = ? AND action = ? AND resource = ?", role, "execute", "memory-forget",
			).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("migration 132 must not grant execute:memory-forget to %s", role)
			}
		}
	}

	now := time.Now().UTC()
	base := model.MemoryOperation{
		ID: id.New(), TenantID: "tenant-1", PolicyID: "policy-1", MemoryID: "memory-1",
		AssistantID: "assistant-1", UserID: "user-1", OutboxEventID: "event-1",
		RequestID: "request-1", OperationType: "write", IdempotencyKey: "idempotency-1",
		State: model.MemoryOperationAccepted, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&base).Error; err != nil {
		t.Fatal(err)
	}
	duplicateRequest := base
	duplicateRequest.ID = id.New()
	if err := gdb.Create(&duplicateRequest).Error; err == nil {
		t.Fatal("duplicate request identity must fail")
	}
	duplicateIdempotency := base
	duplicateIdempotency.ID = id.New()
	duplicateIdempotency.RequestID = "request-2"
	if err := gdb.Create(&duplicateIdempotency).Error; err == nil {
		t.Fatal("duplicate idempotency identity must fail")
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestP0_DBMIG_133_MemoryPolicyRuntimeBinding(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-policy-runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(gdb); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	for _, name := range []string{
		"uidx_rgx_memory_policy_active_memory",
		"uidx_rgx_memory_policy_active_scope",
	} {
		if !gdb.Migrator().HasIndex(&model.MemoryPolicy{}, name) {
			t.Fatalf("migration 133 must create index %s", name)
		}
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: "tenant-1", AssistantID: "assistant-1", UserID: "user-1",
		RagflowMemoryID: "memory-1", State: model.MemoryPolicyActive,
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(policy).Error; err != nil {
		t.Fatal(err)
	}
	duplicateMemory := *policy
	duplicateMemory.ID = id.New()
	duplicateMemory.UserID = "user-2"
	if err := gdb.Create(&duplicateMemory).Error; err == nil {
		t.Fatal("duplicate active memory binding must fail")
	}
	paused := policy
	paused.ID = id.New()
	paused.State = model.MemoryPolicyPaused
	paused.UserID = "user-2"
	if err := gdb.Create(paused).Error; err != nil {
		t.Fatalf("paused policy must not violate active binding index: %v", err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestP0_DBMIG_135_MemoryCompensationFingerprint(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-compensation-fingerprint.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(gdb); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if !gdb.Migrator().HasColumn(&model.MemoryOperation{}, "ExternalMessageHash") {
		t.Fatal("migration 135 must add external_message_hash")
	}
	if !gdb.Migrator().HasIndex(&model.MemoryOperation{}, "idx_rgx_memory_operation_compensation") {
		t.Fatal("migration 135 must create compensation index")
	}

	now := time.Now().UTC()
	operation := model.MemoryOperation{
		ID: id.New(), TenantID: "tenant-1", PolicyID: "policy-1", MemoryID: "memory-1",
		AssistantID: "assistant-1", UserID: "user-1", OutboxEventID: "event-1",
		RequestID: "request-135", OperationType: "write", IdempotencyKey: "idempotency-135",
		State: model.MemoryOperationExecuted, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.MemoryOperation{}).
		Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).
		Update("external_message_hash", gorm.Expr("NULL")).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("fingerprint repair migration: %v", err)
	}
	var stored model.MemoryOperation
	if err := gdb.Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).
		First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ExternalMessageHash != "" {
		t.Fatalf("external_message_hash NULL must normalize to empty, got %q", stored.ExternalMessageHash)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

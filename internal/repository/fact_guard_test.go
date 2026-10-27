package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestFactGuardPersistenceAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "fact-guard-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })

	if !gdb.Migrator().HasTable(&model.FactRegistry{}) ||
		!gdb.Migrator().HasTable(&model.ClaimValidation{}) ||
		!gdb.Migrator().HasTable(&model.EvidenceConflict{}) {
		t.Fatal("fact guard migration tables are missing")
	}
	if !gdb.Migrator().HasIndex(&model.FactRegistry{}, "idx_rgx_fact_registry_lookup") {
		t.Fatal("fact guard lookup index is missing")
	}

	tenant := id.New()
	otherTenant := id.New()
	runID := id.New()
	now := time.Now().UTC()
	fact := &model.FactRegistry{
		ID: id.New(), TenantID: tenant, AnswerRunID: runID, FactKey: "revenue", Value: "10,000,000",
		Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted,
		ConflictStatus: model.FactConflictNone, Confidence: 0.9, CreatedAt: now,
	}
	if err := store.CreateFactRegistry(ctx, fact); err != nil {
		t.Fatalf("create fact: %v", err)
	}
	fact.Confidence = 0.95
	if err := store.UpdateFactRegistry(ctx, fact); err != nil {
		t.Fatalf("update fact: %v", err)
	}
	conflict := &model.EvidenceConflict{
		ID: id.New(), TenantID: tenant, AnswerRunID: runID, LeftFactID: fact.ID, RightFactID: id.New(),
		ConflictType: model.FactConflictValue, Resolution: model.EvidenceConflictUnresolved,
		ResolverMode: "current", EvidenceRefs: `["` + fact.ID + `","right"]`, CreatedAt: now,
	}
	if err := store.CreateEvidenceConflict(ctx, conflict); err != nil {
		t.Fatalf("create conflict: %v", err)
	}
	validation := &model.ClaimValidation{
		ID: id.New(), TenantID: tenant, AnswerRunID: runID, FactIDs: `["` + fact.ID + `"]`,
		LLMClaim: "Revenue was 10 million USD", FactKey: "revenue", Value: "10000000",
		Unit: "USD", TimeRange: "FY2025", ValidationStatus: model.ClaimValidationSupported,
		Action: model.ClaimActionKeep, CreatedAt: now,
	}
	if err := store.CreateClaimValidation(ctx, validation); err != nil {
		t.Fatalf("create claim validation: %v", err)
	}

	facts, err := store.ListFactRegistry(ctx, tenant, runID)
	if err != nil || len(facts) != 1 || facts[0].Confidence != 0.95 {
		t.Fatalf("list facts = %+v err=%v", facts, err)
	}
	byIDs, err := store.ListFactRegistryByIDs(ctx, tenant, runID, []string{fact.ID})
	if err != nil || len(byIDs) != 1 {
		t.Fatalf("list facts by ids = %+v err=%v", byIDs, err)
	}
	if otherFacts, err := store.ListFactRegistry(ctx, otherTenant, runID); err != nil || len(otherFacts) != 0 {
		t.Fatalf("cross-tenant facts = %+v err=%v", otherFacts, err)
	}
	conflicts, err := store.ListEvidenceConflicts(ctx, tenant, runID)
	if err != nil || len(conflicts) != 1 || conflicts[0].ID != conflict.ID {
		t.Fatalf("list conflicts = %+v err=%v", conflicts, err)
	}
	validations, err := store.ListClaimValidations(ctx, tenant, runID)
	if err != nil || len(validations) != 1 || validations[0].ID != validation.ID {
		t.Fatalf("list validations = %+v err=%v", validations, err)
	}
}

package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func timePtr() *time.Time {
	now := time.Now().UTC()
	return &now
}

func TestRepositoryGovernanceDictionaryLifecycle(t *testing.T) {
	s := newGovernanceRepoStore(t)
	ctx := context.Background()
	tenantID := "t-dict-1"

	// Platform builtins are seeded by migration.
	levelTerms, err := s.ListGovernanceDictionary(ctx, tenantID, model.DictCategoryLevel)
	if err != nil {
		t.Fatal(err)
	}
	if len(levelTerms) != 3 {
		t.Fatalf("platform level terms = %d, want 3 (internal/confidential/top_secret)", len(levelTerms))
	}
	statusTerms, err := s.ListGovernanceDictionary(ctx, tenantID, model.DictCategoryStatus)
	if err != nil {
		t.Fatal(err)
	}
	if len(statusTerms) != 2 {
		t.Fatalf("platform status terms = %d, want 2 (draft/published)", len(statusTerms))
	}

	// Tenant extension starts pending + inactive, then approval activates it.
	ext := &model.GovernanceDictionaryEntry{
		ID: "dict-ext-1", TenantID: tenantID, Category: model.DictCategoryLevel,
		Key: "restricted", Label: "受控", Rank: 4, Origin: model.DictOriginTenant,
		ApprovalState: model.DictApprovalPending, Active: false, CreatedBy: "u1",
	}
	if err := s.UpsertGovernanceDictionaryEntry(ctx, ext); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetGovernanceDictionaryEntry(ctx, tenantID, model.DictCategoryLevel, "restricted")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Active {
		t.Fatalf("tenant entry should exist and be inactive, got %+v", got)
	}

	// Approve activates the entry; the merged view now includes it.
	now := timePtr()
	if err := s.SetGovernanceDictionaryApproval(ctx, ext.ID, model.DictApprovalApproved, "governor1", now); err != nil {
		t.Fatal(err)
	}
	merged, err := s.ListGovernanceDictionary(ctx, tenantID, model.DictCategoryLevel)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 {
		t.Fatalf("merged level terms = %d, want 4", len(merged))
	}
	foundApproved := false
	for _, term := range merged {
		if term.Key == "restricted" && term.Active && term.ApprovalState == model.DictApprovalApproved {
			foundApproved = true
		}
	}
	if !foundApproved {
		t.Fatal("approved tenant extension not present in merged view")
	}

	// Reject deactivates.
	if err := s.SetGovernanceDictionaryApproval(ctx, ext.ID, model.DictApprovalRejected, "governor1", nil); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetGovernanceDictionaryEntry(ctx, tenantID, model.DictCategoryLevel, "restricted")
	if err != nil {
		t.Fatal(err)
	}
	if after == nil || after.Active || after.ApprovalState != model.DictApprovalRejected {
		t.Fatalf("rejected entry should be inactive, got %+v", after)
	}

	// Delete removes only tenant-owned entries.
	if err := s.DeleteGovernanceDictionaryEntry(ctx, tenantID, ext.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.GetGovernanceDictionaryEntry(ctx, tenantID, model.DictCategoryLevel, "restricted")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != nil {
		t.Fatal("tenant entry should be gone after delete")
	}
	ungated, err := s.ListAllGovernanceDictionary(ctx, model.DictCategoryLevel)
	if err != nil {
		t.Fatal(err)
	}
	if len(ungated) < 3 {
		t.Fatalf("platform terms missing from all view: %d", len(ungated))
	}
}

func TestRepositoryGovernanceSchemaCRUD(t *testing.T) {
	s := newGovernanceRepoStore(t)
	ctx := context.Background()
	tenantID := "t-schema-1"

	g := &model.GovernanceSchema{
		ID: "schema-1", TenantID: tenantID, Name: "绝密字段", Category: model.DictCategoryLevel,
		MatcherType: model.SchemaMatcherRegex, Pattern: `(?i)top.?secret`, TargetKey: "top_secret",
		QualityMin: model.GovernanceSchemaDefaultQualityMin, BindScope: model.SchemaBindDataset,
		DatasetID: "ds-1", Active: true, CreatedBy: "u1",
	}
	if err := s.CreateGovernanceSchema(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGovernanceSchema(ctx, tenantID, "schema-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TargetKey != "top_secret" {
		t.Fatalf("schema mismatch: %+v", got)
	}

	active := true
	list, err := s.ListGovernanceSchemas(ctx, tenantID, GovernanceSchemaFilter{DatasetID: "ds-1", Active: &active})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].AppliesTo("ds-1", "doc-any") {
		t.Fatalf("dataset-bound schema not listed/applied: %+v", list)
	}

	// document bind should not apply to another document
	doc := &model.GovernanceSchema{
		ID: "schema-2", TenantID: tenantID, Name: "单文档", Category: model.DictCategoryStatus,
		MatcherType: model.SchemaMatcherKeyword, Pattern: "draft", TargetKey: "draft",
		QualityMin: model.GovernanceSchemaDefaultQualityMin, BindScope: model.SchemaBindDocument,
		DatasetID: "ds-1", DocumentID: "doc-2", Active: true, CreatedBy: "u1",
	}
	if err := s.CreateGovernanceSchema(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if doc.AppliesTo("ds-1", "doc-2") != true || doc.AppliesTo("ds-1", "doc-9") != false {
		t.Fatal("document bind scope application mismatch")
	}

	if err := s.DeleteGovernanceSchema(ctx, tenantID, "schema-1"); err != nil {
		t.Fatal(err)
	}
	gone, err := s.GetGovernanceSchema(ctx, tenantID, "schema-1")
	if err != nil {
		t.Fatal(err)
	}
	if gone != nil {
		t.Fatal("deleted schema still present")
	}
}

func TestRepositoryGovernanceDictionary_MigratePathOnly(t *testing.T) {
	gdb, err := db.Open(configDatabaseSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sqlDB, err := gdb.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	if err := gdb.Model(&model.GovernanceDictionaryEntry{}).Where("origin = ?", model.DictOriginPlatform).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 5 {
		t.Fatalf("seeded platform dictionary terms = %d, want 5", cnt)
	}
}

func configDatabaseSQLite(t *testing.T) config.Database {
	t.Helper()
	return config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "rgx-test.db")}
}

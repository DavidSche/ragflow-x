package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func dictEntryInput(category, key, label string) GovernanceDictionaryEntryInput {
	return GovernanceDictionaryEntryInput{Category: category, Key: key, Label: label}
}

func TestGovernanceDictionaryApprovalFlow(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, dsID := newGovernanceService(t)

	// Effective vocabulary starts with the platform seed only.
	dict, err := svc.ListGovernanceDictionary(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dict[model.DictCategoryLevel]) != 3 || len(dict[model.DictCategoryStatus]) != 2 {
		t.Fatalf("platform seed expected 3 level + 2 status, got %d/%d",
			len(dict[model.DictCategoryLevel]), len(dict[model.DictCategoryStatus]))
	}

	// Tenant extension lands pending + inactive.
	entry, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryLevel, "restricted", "受控"))
	if err != nil {
		t.Fatal(err)
	}
	if entry.ApprovalState != model.DictApprovalPending || entry.Active {
		t.Fatalf("tenant entry must be pending+inactive, got %+v", entry)
	}

	// Not visible in effective vocabulary while pending.
	dict2, _ := svc.ListGovernanceDictionary(ctx, tenantID)
	if len(dict2[model.DictCategoryLevel]) != 3 {
		t.Fatal("pending entry must not be visible in effective vocabulary")
	}

	// Approve activates it.
	approved, err := svc.DecideGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !approved.Active || approved.ApprovalState != model.DictApprovalApproved {
		t.Fatalf("approved entry should be active, got %+v", approved)
	}
	dict3, _ := svc.ListGovernanceDictionary(ctx, tenantID)
	if len(dict3[model.DictCategoryLevel]) != 4 {
		t.Fatalf("approved entry should be visible, got %d", len(dict3[model.DictCategoryLevel]))
	}

	// Double decision is rejected.
	if _, err := svc.DecideGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID, false); err == nil || errCode(err) != 40133 {
		t.Fatalf("double decision expected 40133, got %v", err)
	}

	// Reject path: rejected term keeps its audit trail and blocks the same key
	// until deleted (no silent reuse under the same identity).
	entry2, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryStatus, "archived", "归档"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideGovernanceDictionaryEntry(ctx, tenantID, governorID, entry2.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryStatus, "archived", "归档")); err == nil || errCode(err) != 40132 {
		t.Fatalf("rejected key must stay occupied until deleted, got %v", err)
	}
	if err := svc.DeleteGovernanceDictionaryEntry(ctx, tenantID, governorID, entry2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryStatus, "archived", "归档")); err != nil {
		t.Fatalf("re-register after reject+delete should be allowed: %v", err)
	}

	// Platform key shadowing is blocked.
	if _, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryLevel, "top_secret", "x")); err == nil || errCode(err) != 40132 {
		t.Fatalf("platform shadowing expected 40132, got %v", err)
	}

	// Delete removes a tenant entry; platform terms are immutable (404).
	if err := svc.DeleteGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID); err != nil {
		t.Fatal(err)
	}
	platformID := dict[model.DictCategoryLevel][0].ID
	if err := svc.DeleteGovernanceDictionaryEntry(ctx, tenantID, governorID, platformID); err == nil || errHTTPStatus(err) != 404 {
		t.Fatalf("deleting platform term expected 404, got %v", err)
	}
	if _, er := svc.ListGovernanceSchemas(ctx, tenantID, repository.GovernanceSchemaFilter{DatasetID: dsID}); er != nil {
		t.Fatalf("list schemas: %v", er)
	}
}

func TestGovernanceDictionaryAuditTrail(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, _ := newGovernanceService(t)

	entry, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, memberID, dictEntryInput(model.DictCategoryLevel, "restricted", "受控"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID); err != nil {
		t.Fatal(err)
	}

	logs, _, err := svc.Store.ListAudits(ctx, tenantID, 1, 50, repository.AuditFilter{Action: "governance_dictionary"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 dictionary audit events (created/decided/deleted), got %d", len(logs))
	}
	actions := map[string]bool{}
	for _, l := range logs {
		if l.Resource != "document-governance" {
			t.Fatalf("audit resource must be document-governance, got %s", l.Resource)
		}
		actions[l.Action] = true
	}
	for _, want := range []string{"governance_dictionary.created", "governance_dictionary.decided", "governance_dictionary.deleted"} {
		if !actions[want] {
			t.Fatalf("missing audit event %s, got %v", want, actions)
		}
	}
}

func TestGovernanceSchemaValidation(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)

	base := GovernanceSchemaInput{Name: "绝密识别", Category: model.DictCategoryLevel, MatcherType: model.SchemaMatcherRegex, Pattern: `(?i)top.?secret`, TargetKey: "top_secret", QualityMin: 60, BindScope: model.SchemaBindDataset, DatasetID: dsID}

	var err error
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, base); err != nil {
		t.Fatalf("valid schema should create: %v", err)
	}

	bad := base
	bad.MatcherType = "regex"
	bad.Pattern = "("
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, bad); err == nil || errCode(err) != 40141 {
		t.Fatalf("invalid regex expected 40141, got %v", err)
	}

	bad = base
	bad.TargetKey = "nonexistent"
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, bad); err == nil || errCode(err) != 40142 {
		t.Fatalf("inactive target expected 40142, got %v", err)
	}

	bad = base
	bad.QualityMin = 120
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, bad); err == nil || errCode(err) != 40141 {
		t.Fatalf("quality_min overflow expected 40141, got %v", err)
	}

	bad = base
	bad.BindScope = model.SchemaBindDocument
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, bad); err == nil || errCode(err) != 40141 {
		t.Fatalf("document bind without document_id expected 40141, got %v", err)
	}

	existing := base
	existing.Name = base.Name + "-list"
	if _, err = svc.CreateGovernanceSchema(ctx, tenantID, memberID, existing); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListGovernanceSchemas(ctx, tenantID, repository.GovernanceSchemaFilter{DatasetID: dsID})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 schemas listed, got %d", len(listed))
	}

	// update path
	created, err := svc.CreateGovernanceSchema(ctx, tenantID, memberID, base)
	if err != nil {
		t.Fatal(err)
	}
	upd := base
	upd.QualityMin = 80
	updated, err := svc.UpdateGovernanceSchema(ctx, tenantID, memberID, created.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if updated.QualityMin != 80 {
		t.Fatalf("update quality_min failed: %v", updated.QualityMin)
	}
	if err := svc.DeleteGovernanceSchema(ctx, tenantID, memberID, created.ID); err != nil {
		t.Fatal(err)
	}
}

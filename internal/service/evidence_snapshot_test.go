package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func evidenceSnapshotInput(evalCaseID, logicalDocumentID string) EvidenceSnapshotInput {
	return EvidenceSnapshotInput{
		EvalCaseID: evalCaseID,
		DatasetIDs: []string{" dataset-1 ", "dataset-2"},
		Dependencies: []EvidenceDependencyInput{
			{LogicalDocumentID: logicalDocumentID, BindingType: model.EvidenceBindingRetrieval},
		},
		RetrievalPolicyVersion:     "retrieval-v1",
		PromptVersion:              "prompt-v1",
		ModelVersion:               "model-v1",
		ParserPolicyVersion:        "parser-v1",
		ToolPolicyVersion:          "tool-v1",
		AuthorizationPolicyVersion: "authorization-v1",
	}
}

func createEvidenceSnapshotFixture(t *testing.T, svc *Service) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	tenantID, evalCaseID, logicalID := id.New(), id.New(), id.New()
	now := time.Now().UTC()
	logical := &model.LogicalDocument{
		ID: logicalID, TenantID: tenantID, DatasetID: "dataset-1", Name: "Policy",
		CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateLogicalDocument(ctx, logical); err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	evalCase := &model.EvalCase{
		ID: evalCaseID, TenantID: tenantID, EvalSetID: id.New(), Question: "question",
		Status: model.EvalStatusPublished, CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateEvalCases(ctx, tenantID, []model.EvalCase{*evalCase}); err != nil {
		t.Fatalf("create eval case: %v", err)
	}
	return tenantID, evalCaseID, logicalID
}

func TestCreateEvidenceSnapshotBundlesCanonicalContract(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantID, evalCaseID, logicalID := createEvidenceSnapshotFixture(t, svc)
	result, err := svc.CreateEvidenceSnapshot(context.Background(), tenantID, "tester", evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create evidence snapshot: %v", err)
	}
	if result.Snapshot.ID == "" || result.Snapshot.TenantID != tenantID || result.Snapshot.SnapshotHash == "" {
		t.Fatalf("unexpected snapshot: %+v", result.Snapshot)
	}
	if result.Evidence.EvalCaseID != evalCaseID || result.Evidence.StaleStatus != model.EvidenceStatusFresh {
		t.Fatalf("unexpected evidence: %+v", result.Evidence)
	}
	if len(result.Dependencies) != 1 || result.Dependencies[0].LogicalDocumentID != logicalID ||
		result.Dependencies[0].BindingType != model.EvidenceBindingRetrieval {
		t.Fatalf("unexpected dependencies: %+v", result.Dependencies)
	}
	var datasetIDs []string
	if err := json.Unmarshal([]byte(result.Snapshot.DatasetIDs), &datasetIDs); err != nil {
		t.Fatalf("decode dataset ids: %v", err)
	}
	if len(datasetIDs) != 2 || datasetIDs[0] != "dataset-1" || datasetIDs[1] != "dataset-2" {
		t.Fatalf("dataset ids were not normalized: %v", datasetIDs)
	}
	if result.Snapshot.DocumentVersions != result.Evidence.DependencySet {
		t.Fatalf("dependency set and snapshot document versions differ")
	}
	bundle, err := svc.Store.GetEvidenceSnapshotBundle(context.Background(), tenantID, result.Snapshot.ID)
	if err != nil || bundle == nil || bundle.Snapshot.ID != result.Snapshot.ID || len(bundle.Dependencies) != 1 {
		t.Fatalf("evidence bundle was not persisted: bundle=%+v err=%v", bundle, err)
	}
}

func TestEvalCaseEvidenceCurrentSetLifecycle(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenantID, evalCaseID, logicalID := createEvidenceSnapshotFixture(t, svc)

	if _, err := svc.GetEvalCaseEvidence(ctx, tenantID, evalCaseID); err == nil {
		t.Fatal("missing eval case evidence must be rejected")
	}

	created, err := svc.UpdateEvalCaseEvidence(ctx, tenantID, "tester", evalCaseID, evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create eval case evidence: %v", err)
	}
	current, err := svc.GetEvalCaseEvidence(ctx, tenantID, evalCaseID)
	if err != nil || current.Snapshot.ID != created.Snapshot.ID {
		t.Fatalf("get current evidence: current=%+v err=%v", current, err)
	}

	updated, err := svc.UpdateEvalCaseEvidence(ctx, tenantID, "tester", evalCaseID, evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil || updated.Snapshot.ID != created.Snapshot.ID {
		t.Fatalf("same hash update must be idempotent: updated=%+v err=%v", updated, err)
	}

	changedInput := evidenceSnapshotInput(evalCaseID, logicalID)
	changedInput.RetrievalPolicyVersion = "retrieval-v2"
	changed, err := svc.UpdateEvalCaseEvidence(ctx, tenantID, "tester", evalCaseID, changedInput)
	if err != nil || changed.Snapshot.ID == created.Snapshot.ID || changed.Evidence.ID == created.Evidence.ID {
		t.Fatalf("changed hash must create current evidence: changed=%+v err=%v", changed, err)
	}
	if current, err = svc.GetEvalCaseEvidence(ctx, tenantID, evalCaseID); err != nil ||
		current.Snapshot.ID != changed.Snapshot.ID {
		t.Fatalf("current evidence not replaced: current=%+v err=%v", current, err)
	}

	if _, err = svc.RevalidateEvalCaseEvidence(ctx, tenantID, "tester", evalCaseID); err == nil {
		t.Fatal("fresh evidence must not be revalidated")
	}
	if _, err = svc.RevalidateEvalCaseEvidence(ctx, id.New(), "tester", evalCaseID); err == nil {
		t.Fatal("cross-tenant revalidation must be rejected")
	}
	changed.Evidence.StaleStatus = model.EvidenceStatusStale
	if _, err = svc.Store.UpdateEvalCaseEvidenceTransition(ctx, tenantID, changed.Evidence.ID, model.EvidenceStatusFresh, model.EvidenceStatusStale, nil); err != nil {
		t.Fatalf("prepare stale evidence: %v", err)
	}
	revalidated, err := svc.RevalidateEvalCaseEvidence(ctx, tenantID, "tester", evalCaseID)
	if err != nil || revalidated == nil || revalidated.Evidence.StaleStatus != model.EvidenceStatusRevalidated ||
		revalidated.Evidence.RevalidatedAt == nil {
		t.Fatalf("revalidate evidence: result=%+v err=%v", revalidated, err)
	}
	audits, _, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{Resource: "eval-set"})
	if err != nil || len(audits) != 3 {
		t.Fatalf("expected update and revalidate audits: audits=%+v err=%v", audits, err)
	}
}

func TestCreateEvidenceSnapshotHashIsDeterministic(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantID, evalCaseID, logicalID := createEvidenceSnapshotFixture(t, svc)
	first, err := svc.CreateEvidenceSnapshot(context.Background(), tenantID, "tester", evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create first snapshot: %v", err)
	}
	second, err := svc.CreateEvidenceSnapshot(context.Background(), tenantID, "tester", evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create second snapshot: %v", err)
	}
	if first.Snapshot.SnapshotHash != second.Snapshot.SnapshotHash {
		t.Fatalf("snapshot hashes differ: %s != %s", first.Snapshot.SnapshotHash, second.Snapshot.SnapshotHash)
	}
}

func TestListEvidenceSnapshotsTenantScopedAndFiltered(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenantID, evalCaseID, logicalID := createEvidenceSnapshotFixture(t, svc)
	first, err := svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", evidenceSnapshotInput(evalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create first snapshot: %v", err)
	}
	if _, err = svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", evidenceSnapshotInput(evalCaseID, logicalID)); err != nil {
		t.Fatalf("create second snapshot: %v", err)
	}
	secondEvalCaseID := id.New()
	if err = svc.Store.CreateEvalCases(ctx, tenantID, []model.EvalCase{{
		ID: secondEvalCaseID, TenantID: tenantID, EvalSetID: id.New(), Question: "question",
		Status: model.EvalStatusPublished, CreatedBy: "tester", CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("create second eval case: %v", err)
	}
	second, err := svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", evidenceSnapshotInput(secondEvalCaseID, logicalID))
	if err != nil {
		t.Fatalf("create second snapshot: %v", err)
	}
	items, total, err := svc.ListEvidenceSnapshots(ctx, tenantID, "tester", repository.EvidenceSnapshotFilter{}, 1, 20)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list snapshots: items=%d total=%d err=%v", len(items), total, err)
	}
	if items[0].Snapshot.ID != second.Snapshot.ID || items[1].Snapshot.ID != first.Snapshot.ID {
		t.Fatalf("snapshots are not newest first: %s %s", items[0].Snapshot.ID, items[1].Snapshot.ID)
	}
	if items[0].DependencyCount != 1 {
		t.Fatalf("unexpected dependency count: %+v", items[0])
	}
	items, total, err = svc.ListEvidenceSnapshots(ctx, tenantID, "tester", repository.EvidenceSnapshotFilter{
		EvalCaseID: evalCaseID, StaleStatus: model.EvidenceStatusFresh,
	}, 1, 20)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("filter snapshots: items=%d total=%d err=%v", len(items), total, err)
	}
	if _, _, err := svc.ListEvidenceSnapshots(ctx, tenantID, "tester", repository.EvidenceSnapshotFilter{
		StaleStatus: "unknown",
	}, 1, 20); err == nil {
		t.Fatal("unknown stale status must be rejected")
	}
	other, otherTotal, err := svc.ListEvidenceSnapshots(ctx, id.New(), "tester", repository.EvidenceSnapshotFilter{}, 1, 20)
	if err != nil || otherTotal != 0 || len(other) != 0 {
		t.Fatalf("cross-tenant list: items=%d total=%d err=%v", len(other), otherTotal, err)
	}
	bundle, err := svc.GetEvidenceSnapshot(ctx, tenantID, "tester", first.Snapshot.ID)
	if err != nil || bundle == nil || bundle.Snapshot.ID != first.Snapshot.ID || len(bundle.Dependencies) != 1 {
		t.Fatalf("get snapshot bundle: bundle=%+v err=%v", bundle, err)
	}
	if _, err := svc.GetEvidenceSnapshot(ctx, tenantID, "tester", id.New()); err == nil {
		t.Fatal("missing snapshot must not be found")
	}
}

func TestEvidenceSnapshotTeamAdminOwnScope(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "evidence-own-tenant")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "evidence-team-owner", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := svc.CreateTeam(ctx, tenant.ID, "Evidence Team", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownedProject, err := svc.CreateProject(ctx, tenant.ID, "Owned Project", "test")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := svc.CreateProject(ctx, tenant.ID, "Other Project", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetTeamProjects(ctx, tenant.ID, team.ID, []string{ownedProject.ID}); err != nil {
		t.Fatal(err)
	}
	datasetIDs := map[string]string{}
	for name, projectID := range map[string]string{"owned": ownedProject.ID, "other": otherProject.ID} {
		datasetID := id.New()
		if err = svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
			ID: datasetID, TenantID: tenant.ID, RAGFlowDatasetID: "ragflow-" + name,
			Name: name, ProjectID: projectID,
		}); err != nil {
			t.Fatal(err)
		}
		datasetIDs[name] = datasetID
	}
	logical := &model.LogicalDocument{
		ID: id.New(), TenantID: tenant.ID, DatasetID: datasetIDs["owned"], Name: "Policy",
		CreatedBy: "tester", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateLogicalDocument(ctx, logical); err != nil {
		t.Fatal(err)
	}
	createCase := func() string {
		evalCaseID := id.New()
		if err = svc.Store.CreateEvalCases(ctx, tenant.ID, []model.EvalCase{{
			ID: evalCaseID, TenantID: tenant.ID, EvalSetID: id.New(), Question: "question",
			Status: model.EvalStatusPublished, CreatedBy: "tester", CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatal(err)
		}
		return evalCaseID
	}
	ownedCase, mixedCase := createCase(), createCase()
	input := func(evalCaseID string, datasets []string) EvidenceSnapshotInput {
		value := evidenceSnapshotInput(evalCaseID, logical.ID)
		value.DatasetIDs = datasets
		return value
	}
	owned, err := svc.CreateEvidenceSnapshot(ctx, tenant.ID, "tester", input(ownedCase, []string{datasetIDs["owned"]}))
	if err != nil {
		t.Fatalf("create owned snapshot: %v", err)
	}
	mixed, err := svc.CreateEvidenceSnapshot(ctx, tenant.ID, "tester", input(mixedCase, []string{datasetIDs["owned"], datasetIDs["other"]}))
	if err != nil {
		t.Fatalf("create mixed snapshot: %v", err)
	}

	tenantAdminItems, total, err := svc.ListEvidenceSnapshots(ctx, tenant.ID, "tenant-admin", repository.EvidenceSnapshotFilter{}, 1, 20)
	if err != nil || total != 2 || len(tenantAdminItems) != 2 {
		t.Fatalf("tenant admin evidence list: total=%d items=%d err=%v", total, len(tenantAdminItems), err)
	}
	teamItems, teamTotal, err := svc.ListEvidenceSnapshots(ctx, tenant.ID, owner.ID, repository.EvidenceSnapshotFilter{}, 1, 20)
	if err != nil || teamTotal != 1 || len(teamItems) != 1 || teamItems[0].Snapshot.ID != owned.Snapshot.ID {
		t.Fatalf("team admin scoped list: total=%d items=%+v err=%v", teamTotal, teamItems, err)
	}
	if _, err = svc.GetEvidenceSnapshot(ctx, tenant.ID, owner.ID, owned.Snapshot.ID); err != nil {
		t.Fatalf("team admin owned detail: %v", err)
	}
	if _, err = svc.GetEvidenceSnapshot(ctx, tenant.ID, owner.ID, mixed.Snapshot.ID); err == nil {
		t.Fatal("team admin mixed dataset detail must be denied")
	}
}

func TestCreateEvidenceSnapshotValidation(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantID, evalCaseID, logicalID := createEvidenceSnapshotFixture(t, svc)
	ctx := context.Background()
	otherTenant := id.New()
	otherLogical := &model.LogicalDocument{
		ID: id.New(), TenantID: otherTenant, DatasetID: "dataset-other", Name: "Other",
		CreatedBy: "test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateLogicalDocument(ctx, otherLogical); err != nil {
		t.Fatalf("create other logical document: %v", err)
	}
	cases := map[string]func(*EvidenceSnapshotInput){
		"missing eval case":       func(input *EvidenceSnapshotInput) { input.EvalCaseID = "" },
		"missing policy versions": func(input *EvidenceSnapshotInput) { input.ToolPolicyVersion = " " },
		"missing dataset":         func(input *EvidenceSnapshotInput) { input.DatasetIDs = nil },
		"duplicate dependency": func(input *EvidenceSnapshotInput) {
			input.Dependencies = append(input.Dependencies, EvidenceDependencyInput{
				LogicalDocumentID: logicalID, BindingType: model.EvidenceBindingRetrieval,
			})
		},
		"invalid binding": func(input *EvidenceSnapshotInput) {
			input.Dependencies[0].BindingType = "derived"
		},
		"cross tenant dependency": func(input *EvidenceSnapshotInput) {
			input.Dependencies[0].LogicalDocumentID = otherLogical.ID
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input := evidenceSnapshotInput(evalCaseID, logicalID)
			mutate(&input)
			if _, err := svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", input); err == nil {
				t.Fatalf("%s must be rejected", name)
			}
		})
	}
}

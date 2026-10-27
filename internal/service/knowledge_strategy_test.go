package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func knowledgeStrategyInput(datasetID string, strategyType string) KnowledgeStrategyInput {
	config := `{"probe_key":"knowledge-strategy"}`
	if strategyType == model.KnowledgeStrategyCompiled {
		config = `{"probe_key":"knowledge-strategy","compilation_template":"tree"}`
	}
	capability := "retrieval"
	if strategyType == model.KnowledgeStrategyStructured {
		capability = model.RAGFlowCapabilityMetadataCondition
	}
	return KnowledgeStrategyInput{
		DatasetID: datasetID, StrategyType: strategyType,
		Config:             config,
		FallbackStrategy:   "hybrid",
		RAGFlowCapability:  capability,
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
		Active:             nil,
	}
}

func createKnowledgeStrategyDataset(t *testing.T, svc *Service, tenant, project string) string {
	return createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Strategy Dataset")
}

func createKnowledgeStrategyDatasetNamed(
	t *testing.T, svc *Service, tenant, project, name string,
) string {
	t.Helper()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-" + id.New(),
		Name: name, ProjectID: project, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(context.Background(), dataset); err != nil {
		t.Fatalf("create dataset link: %v", err)
	}
	return dataset.ID
}

func TestKnowledgeStrategyCRUDValidationAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, otherTenant, user, project := id.New(), id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDataset(t, svc, tenant, project)

	compiledInput := knowledgeStrategyInput(datasetID, "compiled")
	compiledInput.RAGFlowCapability = model.RAGFlowCapabilityKnowledgeCompilation
	created, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, compiledInput)
	if err != nil {
		t.Fatalf("create knowledge strategy: %v", err)
	}
	if created.ID == "" || created.StrategyType != model.KnowledgeStrategyCompiled ||
		created.RAGFlowCapability != model.RAGFlowCapabilityKnowledgeCompilation ||
		created.FallbackStrategy != model.KnowledgeStrategyHybrid ||
		created.ProbeStatus != model.KnowledgeProbeStatusUnknown || !created.Active {
		t.Fatalf("unexpected strategy: %+v", created)
	}
	if created.Config != `{"compilation_template":"tree","probe_key":"knowledge-strategy"}` {
		t.Fatalf("config was not canonicalized: %s", created.Config)
	}
	if strings.Contains(created.Config, "password") {
		t.Fatal("strategy config must not contain secrets")
	}

	list, total, err := svc.ListKnowledgeStrategies(ctx, tenant, user, repository.KnowledgeStrategyFilter{
		DatasetID: datasetID, StrategyType: model.KnowledgeStrategyCompiled,
	}, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list strategy: items=%+v total=%d err=%v", list, total, err)
	}
	crossTenant, total, err := svc.ListKnowledgeStrategies(ctx, otherTenant, user, repository.KnowledgeStrategyFilter{}, 1, 20)
	if err != nil || total != 0 || len(crossTenant) != 0 {
		t.Fatalf("cross-tenant list: items=%+v total=%d err=%v", crossTenant, total, err)
	}

	inactive := false
	input := knowledgeStrategyInput(datasetID, "hybrid")
	input.RAGFlowCapability = "retrieval"
	input.FallbackStrategy = "semantic"
	input.Active = &inactive
	updated, err := svc.UpdateKnowledgeStrategy(ctx, tenant, user, created.ID, input)
	if err != nil {
		t.Fatalf("update knowledge strategy: %v", err)
	}
	if updated.StrategyType != model.KnowledgeStrategyHybrid || updated.Active {
		t.Fatalf("unexpected updated strategy: %+v", updated)
	}
	_, err = svc.GetKnowledgeStrategy(ctx, otherTenant, created.ID)
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusNotFound {
		t.Fatalf("cross-tenant get must be isolated, got %#v", err)
	}
	if err := svc.DeleteKnowledgeStrategy(ctx, tenant, user, created.ID); err != nil {
		t.Fatalf("delete knowledge strategy: %v", err)
	}
	_, err = svc.GetKnowledgeStrategy(ctx, tenant, created.ID)
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusNotFound {
		t.Fatalf("deleted strategy is still visible, got %#v", err)
	}
}

func TestKnowledgeStrategyValidation(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDataset(t, svc, tenant, project)

	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(datasetID, "graph")); err == nil {
		t.Fatal("unsupported strategy type must fail")
	}
	input := knowledgeStrategyInput(datasetID, "semantic")
	input.Config = `{"api_key":"secret"}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("secret-bearing config must fail")
	}
	input = knowledgeStrategyInput(datasetID, "semantic")
	input.Config = `[]`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("non-object config must fail")
	}
	input = knowledgeStrategyInput(datasetID, "semantic")
	input.FallbackStrategy = "semantic"
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("strategy fallback to itself must fail")
	}
	input = knowledgeStrategyInput(datasetID, "semantic")
	input.RAGFlowCapability = "pipeline"
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("unsupported ragflow capability must fail")
	}
	input = knowledgeStrategyInput(datasetID, model.KnowledgeStrategyCompiled)
	input.RAGFlowCapability = model.RAGFlowCapabilityKnowledgeCompilation
	input.Config = `{"compilation_template":"missing-template"}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("unknown compilation template must fail")
	}
	input = knowledgeStrategyInput(datasetID, "semantic")
	input.Config = `{"probe_key":"knowledge-strategy","structured_filters":[{"name":"author","comparison_operator":"is","value":"alice"}]}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("structured filters on a non-structured strategy must fail")
	}
	input = knowledgeStrategyInput(datasetID, "structured")
	input.Config = `{"structured_filters":[]}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("structured strategy requires structured filters")
	}
	input = knowledgeStrategyInput(datasetID, "structured")
	input.Config = `{"structured_filters":[{"name":"author","comparison_operator":"contains","value":"alice"}]}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("unsupported structured filter operator must fail")
	}
	input = knowledgeStrategyInput(datasetID, "structured")
	input.Config = `{"structured_filters":[{"name":"author","comparison_operator":"is","value":"alice","query":"boom"}]}`
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input); err == nil {
		t.Fatal("unknown structured filter fields must fail")
	}
	input = knowledgeStrategyInput(datasetID, "structured")
	input.Config = `{"probe_key":"knowledge-strategy","structured_filters":[{"name":"author","comparison_operator":"is","value":"alice"}]}`
	created, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create structured strategy: %v", err)
	}
	if created.Config != `{"probe_key":"knowledge-strategy","structured_filters":[{"name":"author","comparison_operator":"is","value":"alice"}]}` {
		t.Fatalf("structured config was not canonicalized: %s", created.Config)
	}
	if created.RAGFlowCapability != model.RAGFlowCapabilityMetadataCondition {
		t.Fatalf("structured capability: %+v", created)
	}
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(id.New(), "semantic")); err == nil {
		t.Fatal("unknown dataset must fail")
	}
}

func TestKnowledgeStrategyTeamAdminOwnScope(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Team Admin Strategy")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "team-owner", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "team-member", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "tenant-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := svc.CreateTeam(ctx, tenant.ID, "Strategy Team", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateProject(ctx, tenant.ID, "Other Project", "unbound")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := svc.CreateProject(ctx, tenant.ID, "Owned Project", "bound")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTeamProjects(ctx, tenant.ID, team.ID, []string{owned.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddUserToTeam(ctx, tenant.ID, team.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	ownedDataset := createKnowledgeStrategyDatasetNamed(t, svc, tenant.ID, owned.ID, "Owned Dataset")
	otherDataset := createKnowledgeStrategyDatasetNamed(t, svc, tenant.ID, other.ID, "Other Dataset")

	if err := svc.Store.AssignUserRole(ctx, member.ID, model.RoleTeamAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant.ID, member.ID, knowledgeStrategyInput(otherDataset, "semantic")); err == nil {
		t.Fatal("team membership alone must not create knowledge strategies in non-owned projects")
	}
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant.ID, owner.ID, knowledgeStrategyInput(otherDataset, "semantic")); err == nil {
		t.Fatal("team admin must not create knowledge strategies in non-owned projects")
	}
	created, err := svc.CreateKnowledgeStrategy(ctx, tenant.ID, owner.ID, knowledgeStrategyInput(ownedDataset, "semantic"))
	if err != nil {
		t.Fatalf("create in owned project: %v", err)
	}
	if _, err := svc.CreateKnowledgeStrategy(ctx, tenant.ID, admin.ID, knowledgeStrategyInput(otherDataset, "lexical")); err != nil {
		t.Fatalf("tenant admin create in any tenant project: %v", err)
	}

	ownerList, total, err := svc.ListKnowledgeStrategies(ctx, tenant.ID, owner.ID, repository.KnowledgeStrategyFilter{}, 1, 20)
	if err != nil || total != 1 || len(ownerList) != 1 || ownerList[0].ID != created.ID {
		t.Fatalf("team owner list: items=%+v total=%d err=%v", ownerList, total, err)
	}
	memberList, total, err := svc.ListKnowledgeStrategies(ctx, tenant.ID, member.ID, repository.KnowledgeStrategyFilter{}, 1, 20)
	if err != nil || total != 0 || len(memberList) != 0 {
		t.Fatalf("team member list must be empty: items=%+v total=%d err=%v", memberList, total, err)
	}
	adminList, total, err := svc.ListKnowledgeStrategies(ctx, tenant.ID, admin.ID, repository.KnowledgeStrategyFilter{}, 1, 20)
	if err != nil || total != 2 || len(adminList) != 2 {
		t.Fatalf("tenant admin list: items=%+v total=%d err=%v", adminList, total, err)
	}
}

type knowledgeProbeStub struct {
	ragflow.Client
	chunks      []map[string]interface{}
	err         error
	artifacts   []ragflow.DatasetArtifact
	artifactErr error
}

func (stub knowledgeProbeStub) EngineVersion(context.Context) (string, error) {
	return "1.0.0-rc1", nil
}

func (stub knowledgeProbeStub) RetrieveDatasets(
	_ context.Context, req ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	return &ragflow.RetrieveDatasetsResult{Chunks: stub.chunks, Total: int64(len(stub.chunks))}, nil
}

func (stub knowledgeProbeStub) ListDatasetArtifacts(
	_ context.Context, _ string, _ ragflow.DatasetArtifactFilter,
) ([]ragflow.DatasetArtifact, int64, error) {
	if stub.artifactErr != nil {
		return nil, 0, stub.artifactErr
	}
	return stub.artifacts, int64(len(stub.artifacts)), nil
}

func TestKnowledgeCapabilityProbeRequiresTenantScopedProbeDatasetAndRealEvidence(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, otherTenant, user, project := id.New(), id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDataset(t, svc, tenant, project)
	probeDatasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Compiled Probe Dataset")

	input := knowledgeStrategyInput(datasetID, model.KnowledgeStrategyCompiled)
	input.RAGFlowCapability = model.RAGFlowCapabilityKnowledgeCompilation
	input.Config = `{"compilation_template":"tree"}`
	created, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, created.ID); err == nil {
		t.Fatal("probe without a dedicated dataset must fail")
	}

	input.Config = `{"dataset_id":"missing","compilation_template":"tree"}`
	_, err = svc.UpdateKnowledgeStrategy(ctx, tenant, user, created.ID, input)
	if err != nil {
		t.Fatalf("update strategy: %v", err)
	}
	if _, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, created.ID); err == nil {
		t.Fatal("unknown probe dataset must fail")
	}

	input.Config = `{"dataset_id":"` + probeDatasetID + `","compilation_template":"tree"}`
	if _, err := svc.UpdateKnowledgeStrategy(ctx, tenant, user, created.ID, input); err != nil {
		t.Fatalf("update probe dataset: %v", err)
	}
	if _, err := svc.ProbeKnowledgeStrategy(ctx, otherTenant, user, created.ID); err == nil {
		t.Fatal("cross-tenant probe must fail")
	}
}

func TestKnowledgeCapabilityProbeUsesRollingP95Latency(t *testing.T) {
	latencies := make([]int64, 0, 20)
	for value := 1; value <= 20; value++ {
		latencies = append(latencies, int64(value))
	}
	if got := knowledgeProbeP95(latencies); got != 19 {
		t.Fatalf("rolling p95 = %d, want 19", got)
	}
}

func TestKnowledgeCapabilityProbePassesOnlyAfterThreeConsecutiveCompiledHits(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Compiled Strategy Dataset")
	probeDatasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Compiled Probe Dataset")
	input := knowledgeStrategyInput(datasetID, model.KnowledgeStrategyCompiled)
	input.RAGFlowCapability = model.RAGFlowCapabilityKnowledgeCompilation
	input.Config = `{"dataset_id":"` + probeDatasetID + `","compilation_template":"tree","thresholds":{"latency_ms":3000}}`
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	probeDatasetLink, err := svc.Store.GetDatasetLink(ctx, tenant, probeDatasetID)
	if err != nil || probeDatasetLink == nil {
		t.Fatalf("get probe dataset link: link=%+v err=%v", probeDatasetLink, err)
	}
	probeRAGFlowDatasetID := probeDatasetLink.RAGFlowDatasetID

	svc.RAGFlow = knowledgeProbeStub{chunks: []map[string]interface{}{{
		"id": "chunk-missing-artifact", "dataset_id": probeRAGFlowDatasetID, "compile_kwd": "tree",
	}}}
	if _, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, strategy.ID); err != nil {
		t.Fatalf("artifact miss must persist as failed probe, got %v", err)
	}
	strategyAfterArtifactMiss, err := svc.GetKnowledgeStrategy(ctx, tenant, strategy.ID)
	if err != nil || strategyAfterArtifactMiss.ProbeStatus != model.KnowledgeProbeStatusFailed {
		t.Fatalf("artifact miss status: %+v err=%v", strategyAfterArtifactMiss, err)
	}

	svc.RAGFlow = knowledgeProbeStub{chunks: []map[string]interface{}{{
		"id": "chunk-1", "dataset_id": probeRAGFlowDatasetID, "compile_kwd": "tree",
	}}, artifacts: []ragflow.DatasetArtifact{{Slug: "entity/tree", Title: "Tree", PageType: "entity"}}}
	for attempt := 1; attempt <= 3; attempt++ {
		probe, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, strategy.ID)
		if err != nil {
			t.Fatalf("probe %d: %v", attempt, err)
		}
		if probe.Status != model.KnowledgeProbeStatusPassed || probe.ProbeKey != "knowledge-strategy-probe" ||
			probe.RAGFlowVersion != "1.0.0-rc1" {
			t.Fatalf("probe %d: %+v", attempt, probe)
		}
		wantStatus := model.KnowledgeProbeStatusUnknown
		if attempt == 3 {
			wantStatus = model.KnowledgeProbeStatusPassed
		}
		updated, err := svc.GetKnowledgeStrategy(ctx, tenant, strategy.ID)
		if err != nil || updated.ProbeStatus != wantStatus {
			t.Fatalf("strategy status after probe %d: %+v err=%v", attempt, updated, err)
		}
	}

	svc.RAGFlow = knowledgeProbeStub{chunks: []map[string]interface{}{{
		"id": "chunk-2", "dataset_id": probeRAGFlowDatasetID, "compile_kwd": "",
	}}}
	failed, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, strategy.ID)
	if err != nil {
		t.Fatalf("failed probe must persist as a result, got %v", err)
	}
	if failed.Status != model.KnowledgeProbeStatusFailed {
		t.Fatalf("unexpected probe: %+v", failed)
	}
	updated, err := svc.GetKnowledgeStrategy(ctx, tenant, strategy.ID)
	if err != nil || updated.ProbeStatus != model.KnowledgeProbeStatusFailed {
		t.Fatalf("failed strategy status: %+v err=%v", updated, err)
	}
}

func TestKnowledgeCapabilityProbeAcceptsPlainRetrievalEvidence(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Plain Strategy Dataset")
	probeDatasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Plain Probe Dataset")
	input := knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic)
	input.Config = `{"dataset_id":"` + probeDatasetID + `"}`
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	probeDatasetLink, err := svc.Store.GetDatasetLink(ctx, tenant, probeDatasetID)
	if err != nil || probeDatasetLink == nil {
		t.Fatalf("get probe dataset link: link=%+v err=%v", probeDatasetLink, err)
	}
	probeRAGFlowDatasetID := probeDatasetLink.RAGFlowDatasetID
	svc.RAGFlow = knowledgeProbeStub{chunks: []map[string]interface{}{{
		"id": "chunk-plain", "dataset_id": probeRAGFlowDatasetID,
	}}}
	probe, err := svc.ProbeKnowledgeStrategy(ctx, tenant, user, strategy.ID)
	if err != nil {
		t.Fatalf("probe strategy: %v", err)
	}
	if probe.Status != model.KnowledgeProbeStatusPassed {
		t.Fatalf("unexpected probe: %+v", probe)
	}
	updated, err := svc.GetKnowledgeStrategy(ctx, tenant, strategy.ID)
	if err != nil || updated.ProbeStatus != model.KnowledgeProbeStatusUnknown {
		t.Fatalf("one pass must not mark strategy available: %+v err=%v", updated, err)
	}
}

type knowledgeExecutionStub struct {
	ragflow.Client
	chunks    []map[string]interface{}
	failFirst bool
	requests  []ragflow.RetrieveDatasetsRequest
}

func (stub *knowledgeExecutionStub) EngineVersion(context.Context) (string, error) {
	return "1.0.0-rc1", nil
}

func (stub *knowledgeExecutionStub) ListDatasetArtifacts(
	_ context.Context, _ string, _ ragflow.DatasetArtifactFilter,
) ([]ragflow.DatasetArtifact, int64, error) {
	return []ragflow.DatasetArtifact{{Slug: "entity/tree"}}, 1, nil
}

func (stub *knowledgeExecutionStub) RetrieveDatasets(
	_ context.Context, req ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	stub.requests = append(stub.requests, req)
	if stub.failFirst && len(stub.requests) == 1 {
		return nil, errors.New("primary unavailable")
	}
	return &ragflow.RetrieveDatasetsResult{Chunks: stub.chunks, Total: int64(len(stub.chunks))}, nil
}

func TestExecuteKnowledgeStrategyUsesProbeGateAndFallback(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-business",
		Name: "Exec Dataset", ProjectID: project, Sensitivity: "restricted", PushdownEnabled: true,
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	input := knowledgeStrategyInput(dataset.ID, model.KnowledgeStrategyCompiled)
	input.RAGFlowCapability = model.RAGFlowCapabilityKnowledgeCompilation
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create compiled strategy: %v", err)
	}

	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{{
		"id": "chunk-1", "dataset_id": "ragflow-business", "compile_kwd": "tree",
	}}}
	svc.RAGFlow = stub
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?", PageSize: intPtr(10),
	})
	if err != nil {
		t.Fatalf("execute compiled strategy: %v", err)
	}
	if result.UsedStrategyType != model.KnowledgeStrategyCompiled || result.FallbackUsed {
		t.Fatalf("unexpected compiled execution: %+v", result)
	}
	if len(stub.requests) != 1 || stub.requests[0].MetadataCondition == nil ||
		len(stub.requests[0].MetadataCondition.Conditions) != 1 {
		t.Fatalf("requests=%+v", stub.requests)
	}

	strategy.ProbeStatus = model.KnowledgeProbeStatusUnknown
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err = svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{Question: "What changed?"})
	if err != nil {
		t.Fatalf("fallback execute: %v", err)
	}
	if !result.FallbackUsed || result.UsedStrategyType != model.KnowledgeStrategyHybrid ||
		result.RequestedStrategyType != model.KnowledgeStrategyCompiled {
		t.Fatalf("unexpected fallback execution: %+v", result)
	}
	if len(stub.requests) != 2 || stub.requests[1].IncludeKnowledgeCompilation {
		t.Fatalf("fallback request mismatch: %+v", stub.requests[1])
	}
}

func TestExecuteKnowledgeStrategyMapsRetrievalStrategyWeights(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-weights",
		Name: "Weights Dataset", ProjectID: project,
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	input := knowledgeStrategyInput(dataset.ID, model.KnowledgeStrategySemantic)
	input.FallbackStrategy = model.KnowledgeStrategyLexical
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create semantic strategy: %v", err)
	}
	stub := &knowledgeExecutionStub{
		chunks:    []map[string]interface{}{{"id": "lexical-chunk", "dataset_id": "ragflow-weights"}},
		failFirst: true,
	}
	svc.RAGFlow = stub
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?",
	})
	if err != nil {
		t.Fatalf("execute strategy: %v", err)
	}
	if !result.FallbackUsed || result.UsedStrategyType != model.KnowledgeStrategyLexical || len(stub.requests) != 2 {
		t.Fatalf("unexpected execution: %+v requests=%+v", result, stub.requests)
	}
	if stub.requests[0].VectorSimilarityWeight == nil || *stub.requests[0].VectorSimilarityWeight != 1 {
		t.Fatalf("semantic request weight: %+v", stub.requests[0])
	}
	if stub.requests[1].VectorSimilarityWeight == nil || *stub.requests[1].VectorSimilarityWeight != 0 {
		t.Fatalf("lexical request weight: %+v", stub.requests[1])
	}
}

func TestKnowledgeStrategyRetrievalWeightMapping(t *testing.T) {
	svc := &Service{}
	expected := map[string]float64{
		model.KnowledgeStrategySemantic:   1,
		model.KnowledgeStrategyHybrid:     0.5,
		model.KnowledgeStrategyLexical:    0,
		model.KnowledgeStrategyStructured: 0,
	}
	for strategyType, weight := range expected {
		request := svc.buildKnowledgeStrategyRetrieveRequest(
			"ragflow-dataset", "What changed?", nil, nil, strategyType, 1, 10,
			knowledgeStrategyConfigValues{},
		)
		if request.VectorSimilarityWeight == nil || *request.VectorSimilarityWeight != weight {
			t.Fatalf("%s weight: %+v", strategyType, request)
		}
	}
	compiled := svc.buildKnowledgeStrategyRetrieveRequest(
		"ragflow-dataset", "What changed?", nil, nil, model.KnowledgeStrategyCompiled, 1, 10,
		knowledgeStrategyConfigValues{},
	)
	if compiled.VectorSimilarityWeight != nil || !compiled.IncludeKnowledgeCompilation {
		t.Fatalf("compiled request: %+v", compiled)
	}
}

func TestExecuteKnowledgeStrategyPushesStructuredFilters(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-structured",
		Name: "Structured Dataset", ProjectID: project,
		Sensitivity: "restricted", PushdownEnabled: true,
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	input := knowledgeStrategyInput(dataset.ID, model.KnowledgeStrategyStructured)
	input.FallbackStrategy = model.KnowledgeStrategyLexical
	input.Config = `{"structured_filters":[` +
		`{"name":"author","comparison_operator":"is","value":"alice"},` +
		`{"name":"year","comparison_operator":">=","value":"2026"}]}`
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create structured strategy: %v", err)
	}
	stub := &knowledgeExecutionStub{
		chunks:    []map[string]interface{}{{"id": "structured-chunk", "dataset_id": "ragflow-structured"}},
		failFirst: true,
	}
	svc.RAGFlow = stub
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?",
	})
	if err != nil {
		t.Fatalf("execute structured strategy: %v", err)
	}
	if !result.FallbackUsed || result.UsedStrategyType != model.KnowledgeStrategyLexical {
		t.Fatalf("unexpected structured execution: %+v", result)
	}
	if len(stub.requests) != 2 {
		t.Fatalf("unexpected requests: %+v", stub.requests)
	}
	for index, request := range stub.requests {
		governance := findKnowledgeCondition(request, "rgx_sensitivity")
		author := findKnowledgeCondition(request, "author")
		year := findKnowledgeCondition(request, "year")
		if governance == nil || governance.ComparisonOperator != "!=" || governance.Value != "restricted" ||
			author == nil || author.Value != "alice" || year == nil || year.Value != "2026" {
			t.Fatalf("request %d condition mismatch: %+v", index, request.MetadataCondition)
		}
	}
}

func TestExecuteKnowledgeStrategyAppliesKeywordAndRerank(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-keyword",
		Name: "Keyword Dataset", ProjectID: project,
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	input := knowledgeStrategyInput(dataset.ID, model.KnowledgeStrategyHybrid)
	input.FallbackStrategy = model.KnowledgeStrategyLexical
	input.Config = `{"keyword":true,"rerank_id":"tenant-rerank-v1"}`
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create keyword strategy: %v", err)
	}
	if strategy.Config != `{"keyword":true,"rerank_id":"tenant-rerank-v1"}` {
		t.Fatalf("unexpected canonical config: %s", strategy.Config)
	}
	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{
		{"id": "chunk-1", "dataset_id": "ragflow-keyword"},
	}}
	svc.RAGFlow = stub
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?",
	})
	if err != nil {
		t.Fatalf("execute keyword strategy: %v", err)
	}
	if result.FallbackUsed || len(stub.requests) != 1 {
		t.Fatalf("unexpected execution: %+v requests=%+v", result, stub.requests)
	}
	if !stub.requests[0].Keyword || stub.requests[0].RerankID != "tenant-rerank-v1" {
		t.Fatalf("keyword/rerank request mismatch: %+v", stub.requests[0])
	}
}

func intPtr(value int) *int { return &value }

func createKnowledgeStrategyLogicalVersion(
	t *testing.T, svc *Service, tenant, datasetID string, version int64,
	status string, from time.Time, to *time.Time,
) *model.DocumentVersion {
	t.Helper()
	var logical *model.LogicalDocument
	documents, _, err := svc.Store.ListLogicalDocuments(context.Background(), tenant, repository.DocumentVersionFilter{DatasetID: datasetID}, 1, 1)
	if err != nil {
		t.Fatalf("list logical documents: %v", err)
	}
	if len(documents) == 0 {
		now := time.Now().UTC()
		logical = &model.LogicalDocument{
			ID: id.New(), TenantID: tenant, DatasetID: datasetID, Name: "Logical Document",
			SourceURI: "test://logical", CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
		}
		if err := svc.Store.CreateLogicalDocument(context.Background(), logical); err != nil {
			t.Fatalf("create logical document: %v", err)
		}
	} else {
		logical = &documents[0]
	}
	documentVersion := &model.DocumentVersion{
		ID: id.New(), TenantID: tenant, LogicalDocumentID: logical.ID,
		RAGFlowDocumentID: "ragflow-doc-" + id.New(), Version: version, Status: status,
		EffectiveFrom: from, EffectiveTo: to, ContentHash: "hash-" + id.New(),
		CreatedBy: "test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDocumentVersion(context.Background(), documentVersion); err != nil {
		t.Fatalf("create document version %d: %v", version, err)
	}
	return documentVersion
}

func findKnowledgeCondition(request ragflow.RetrieveDatasetsRequest, name string) *ragflow.MetadataConditionOp {
	if request.MetadataCondition == nil {
		return nil
	}
	for index := range request.MetadataCondition.Conditions {
		if request.MetadataCondition.Conditions[index].Name == name {
			return &request.MetadataCondition.Conditions[index]
		}
	}
	return nil
}

func TestExecuteKnowledgeStrategyResolvesTemporalVersions(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Version Dataset")
	input := knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic)
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}

	asOf := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	newEffectiveFrom := asOf.AddDate(0, 1, 0)
	oldVersion := createKnowledgeStrategyLogicalVersion(
		t, svc, tenant, datasetID, 1, model.DocumentVersionSuperseded,
		asOf.AddDate(0, -1, 0), &newEffectiveFrom,
	)
	newVersion := createKnowledgeStrategyLogicalVersion(
		t, svc, tenant, datasetID, 2, model.DocumentVersionActive, newEffectiveFrom, nil,
	)

	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{
		{"id": "old-hit", "dataset_id": "ragflow-version", "document_id": oldVersion.RAGFlowDocumentID},
		{"id": "new-hit", "dataset_id": "ragflow-version", "document_id": newVersion.RAGFlowDocumentID},
	}}
	svc.RAGFlow = stub

	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?",
		VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: oldVersion.LogicalDocumentID,
		},
	})
	if err != nil {
		t.Fatalf("resolve current version: %v", err)
	}
	if result.VersionResolver == nil || result.VersionResolver.VersionID != newVersion.ID ||
		result.VersionResolver.Version != 2 || len(result.Chunks) != 1 ||
		result.Chunks[0]["document_id"] != newVersion.RAGFlowDocumentID || result.Total != 2 {
		t.Fatalf("current resolution: %+v", result)
	}
	if !result.MetadataPushdown || findKnowledgeCondition(stub.requests[0], "rgx_version") == nil {
		t.Fatalf("version was not pushed down: %+v", stub.requests[0])
	}

	stub.requests = nil
	result, err = svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What was true?", VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionAsOf, LogicalDocumentID: oldVersion.LogicalDocumentID, AsOf: &asOf,
		},
	})
	if err != nil {
		t.Fatalf("resolve as-of version: %v", err)
	}
	if result.VersionResolver == nil || result.VersionResolver.VersionID != oldVersion.ID ||
		len(result.Chunks) != 1 || result.Chunks[0]["document_id"] != oldVersion.RAGFlowDocumentID {
		t.Fatalf("as-of resolution: %+v", result)
	}

	stub.requests = nil
	exactVersion := int64(1)
	result, err = svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What was true?", VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionExact, LogicalDocumentID: oldVersion.LogicalDocumentID,
			Version: &exactVersion,
		},
	})
	if err != nil {
		t.Fatalf("resolve exact version: %v", err)
	}
	if result.VersionResolver == nil || result.VersionResolver.VersionID != oldVersion.ID {
		t.Fatalf("exact resolution: %+v", result)
	}
}

func TestExecuteKnowledgeStrategyVersionResolverValidationAndPostFilter(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, otherTenant, user, project := id.New(), id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Version Validation Dataset")
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic))
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	effectiveFrom := time.Now().UTC().Add(-time.Hour)
	version := createKnowledgeStrategyLogicalVersion(
		t, svc, tenant, datasetID, 1, model.DocumentVersionActive, effectiveFrom, nil,
	)
	otherDatasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Version Mismatch Dataset")
	mismatchedVersion := createKnowledgeStrategyLogicalVersion(
		t, svc, tenant, otherDatasetID, 1, model.DocumentVersionActive, effectiveFrom, nil,
	)
	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{
		{"id": "foreign-hit", "dataset_id": "ragflow-version", "document_id": "ragflow-other"},
		{"id": "version-hit", "dataset_id": "ragflow-version", "document_id": version.RAGFlowDocumentID},
	}}
	svc.RAGFlow = stub

	cases := []struct {
		name     string
		tenant   string
		status   int
		code     int
		resolver KnowledgeStrategyVersionResolverInput
	}{
		{name: "unknown logical document", tenant: tenant, status: http.StatusNotFound, code: 404, resolver: KnowledgeStrategyVersionResolverInput{Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: id.New()}},
		{name: "cross tenant logical document", tenant: otherTenant, status: http.StatusNotFound, code: 404, resolver: KnowledgeStrategyVersionResolverInput{Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: version.LogicalDocumentID}},
		{name: "dataset mismatch", tenant: tenant, status: http.StatusNotFound, code: 404, resolver: KnowledgeStrategyVersionResolverInput{Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: mismatchedVersion.LogicalDocumentID}},
		{name: "missing as-of", tenant: tenant, status: http.StatusBadRequest, code: 40120, resolver: KnowledgeStrategyVersionResolverInput{Mode: KnowledgeStrategyVersionAsOf, LogicalDocumentID: version.LogicalDocumentID}},
		{name: "missing exact version", tenant: tenant, status: http.StatusBadRequest, code: 40120, resolver: KnowledgeStrategyVersionResolverInput{Mode: KnowledgeStrategyVersionExact, LogicalDocumentID: version.LogicalDocumentID}},
	}
	for _, test := range cases {
		_, err = svc.ExecuteKnowledgeStrategy(ctx, test.tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
			Question: "What changed?", VersionResolver: &test.resolver,
		})
		businessErr, ok := err.(*httperr.Error)
		if !ok || businessErr.Status != test.status || businessErr.Code != test.code {
			t.Fatalf("%s returned unexpected error %#v", test.name, err)
		}
	}

	_, err = svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?", DocumentIDs: []string{"ragflow-mismatch"},
		VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: version.LogicalDocumentID,
		},
	})
	businessErr, ok := err.(*httperr.Error)
	if !ok || businessErr.Status != http.StatusBadRequest {
		t.Fatalf("document ID conflict must be rejected, got %#v", err)
	}

	err = svc.Store.UpsertVersionFilterPolicy(ctx, &model.VersionFilterPolicy{
		ID: id.New(), TenantID: tenant, MaxPushdownIDs: 1, MaxRequestBytes: 32768,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("upsert filter policy: %v", err)
	}
	policy, policyErr := svc.Store.GetVersionFilterPolicy(ctx, tenant)
	if policyErr != nil || policy == nil || policy.MaxPushdownIDs != 1 {
		t.Fatalf("filter policy after upsert=%+v err=%v", policy, policyErr)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?", VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: version.LogicalDocumentID,
		},
	})
	if err != nil {
		t.Fatalf("execute post-filter: %v", err)
	}
	if result.VersionResolver == nil || result.VersionResolver.PushdownUsed || !result.VersionResolver.PostFiltered ||
		len(result.Chunks) != 1 || findKnowledgeCondition(stub.requests[len(stub.requests)-1], "rgx_version") != nil {
		t.Fatalf("post-filter execution: %+v resolver=%+v request=%+v", result, result.VersionResolver, stub.requests[len(stub.requests)-1])
	}
}

func TestExecuteKnowledgeStrategyPreservesVersionResolverDuringFallback(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Version Fallback Dataset")
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic))
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	version := createKnowledgeStrategyLogicalVersion(
		t, svc, tenant, datasetID, 1, model.DocumentVersionActive, time.Now().UTC().Add(-time.Hour), nil,
	)
	stub := &knowledgeExecutionStub{
		chunks:    []map[string]interface{}{{"id": "fallback-hit", "document_id": version.RAGFlowDocumentID}},
		failFirst: true,
	}
	svc.RAGFlow = stub
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?", VersionResolver: &KnowledgeStrategyVersionResolverInput{
			Mode: KnowledgeStrategyVersionCurrent, LogicalDocumentID: version.LogicalDocumentID,
		},
	})
	if err != nil {
		t.Fatalf("fallback execution: %v", err)
	}
	if !result.FallbackUsed || result.VersionResolver == nil || result.VersionResolver.Version != 1 ||
		len(result.Chunks) != 1 || len(stub.requests) != 2 ||
		findKnowledgeCondition(stub.requests[1], "rgx_version") == nil {
		t.Fatalf("fallback result=%+v requests=%+v", result, stub.requests)
	}
}

func TestExecuteKnowledgeStrategyFallsBackOnRetrievalFailure(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	datasetID := createKnowledgeStrategyDatasetNamed(t, svc, tenant, project, "Exec Fallback Dataset")
	input := knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic)
	input.FallbackStrategy = model.KnowledgeStrategyHybrid
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, input)
	if err != nil {
		t.Fatal(err)
	}
	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{{"id": "fallback-hit"}}}
	stub.failFirst = true
	svc.RAGFlow = stub
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategy.ID, KnowledgeStrategyRetrieveInput{Question: "What changed?"})
	if err != nil {
		t.Fatalf("fallback after provider failure: %v", err)
	}
	if !result.FallbackUsed || len(result.Chunks) != 1 || len(stub.requests) != 2 {
		t.Fatalf("fallback result=%+v requests=%d", result, len(stub.requests))
	}
}

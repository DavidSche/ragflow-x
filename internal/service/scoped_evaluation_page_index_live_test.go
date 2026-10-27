package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestLivePageIndexSearchDatasetPairedABE2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" || os.Getenv("RGX_ALLOW_SCOPED_EVALUATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 and RGX_ALLOW_SCOPED_EVALUATION_LIVE=1 are required")
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	groups, _, err := client.ListCompilationTemplateGroups(ctx, ragflow.CompilationTemplateGroupFilter{
		Page: 1, PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list live compilation template groups: %v", err)
	}
	var group *ragflow.CompilationTemplateGroup
	for index := range groups {
		for _, template := range groups[index].Templates {
			if strings.EqualFold(template.Kind, model.CompilationKindPageIndex) {
				selected := groups[index]
				group = &selected
				break
			}
		}
		if group != nil {
			break
		}
	}
	if group == nil {
		t.Skip("live PageIndex A/B drill requires a page_index compilation template group")
	}

	embeddingModel := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		models, modelErr := client.ListModels(ctx, "embedding")
		if modelErr != nil {
			t.Fatalf("list live embedding models: %v", modelErr)
		}
		for _, candidate := range models {
			if candidate.Name == "qwen3-embedding-0.6b" && candidate.Instance == "gpustack-local" &&
				candidate.Provider == "OpenAI-API-Compatible" {
				embeddingModel = candidate.ModelID
				break
			}
		}
		if embeddingModel == "" {
			t.Fatal("live PageIndex A/B drill requires qwen3-embedding-0.6b")
		}
	}
	chatModel := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_CHAT_MODEL"))
	if chatModel == "" {
		chatModels, modelErr := client.ListModels(ctx, "chat")
		if modelErr != nil {
			t.Fatalf("list live chat models: %v", modelErr)
		}
		for _, candidate := range chatModels {
			if strings.HasPrefix(candidate.Name, "glm-5.3-flash") {
				chatModel = candidate.ModelID
				break
			}
		}
		if chatModel == "" {
			t.Fatal("live PageIndex A/B drill requires glm-5.3-flash")
		}
	}

	suffix := id.New()[:8]
	pipeline, pipelineErr := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title:          "rgx-pageindex-ab-" + suffix,
		Description:    "Disposable RAGFlow-X PageIndex scoped evaluation drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create live PageIndex A/B pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup live PageIndex A/B pipeline %s: %v", pipeline.ID, err)
		}
	}()
	pipelineDetail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live PageIndex A/B pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(pipelineDetail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("live PageIndex A/B pipeline is not compilation ready: %+v", contract)
	}

	datasetName := "rgx-pageindex-ab-" + suffix
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipeline.ID,
	})
	if err != nil {
		t.Fatalf("create live PageIndex A/B dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup live PageIndex A/B dataset %s: %v", upstream.ID, err)
		}
	}()
	document, err := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
		Name:    "artifact-binding.md",
		Content: []byte(liveArtifactBindingDocumentContent()),
	})
	if err != nil {
		t.Fatalf("create live PageIndex A/B document: %v", err)
	}
	if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: pipeline.ID,
	}); err != nil {
		t.Fatalf("bind live PageIndex A/B pipeline: %v", err)
	}
	if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
		t.Fatalf("ingest live PageIndex A/B document: %v", err)
	}
	if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
		t.Fatalf("wait for live PageIndex A/B document: %v", err)
	}

	svc, ctx, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = client
	svc.SetupWorker(fastWorkerConfig())
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstream.ID,
		Name: datasetName, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create live PageIndex A/B dataset link: %v", err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindPageIndex, DocumentIDs: []string{document.ID},
	})
	if err != nil {
		t.Fatalf("trigger live PageIndex A/B compilation: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live PageIndex A/B compilation: %v", err)
	}
	confirmed := waitLiveCompilationRunConfirmed(t, ctx, svc, tenant.ID, dataset.ID, run.ID, 15*time.Minute)
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, run.ID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected live PageIndex A/B artifact projections: count=%d err=%v", len(artifacts), err)
	}

	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: dataset.ID,
		StrategyType:       model.KnowledgeStrategyCompiled,
		RAGFlowCapability:  model.RAGFlowCapabilityKnowledgeCompilation,
		Config:             `{"compilation_template":"page_index"}`,
		FallbackStrategy:   model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatalf("create live PageIndex A/B strategy: %v", err)
	}

	caseManifest := make([]map[string]interface{}, 0, 30)
	for index := range 30 {
		sectionIndex := index%17 + 1
		question := fmt.Sprintf("What does verification rule %d establish in the artifact binding fixture?", sectionIndex)
		if index >= 17 {
			question = fmt.Sprintf("Why is verification rule %d important for live compilation evidence?", sectionIndex)
		}
		caseManifest = append(caseManifest, map[string]interface{}{
			"case_id":          fmt.Sprintf("pageindex-ab-%02d", index+1),
			"paired_sample_id": fmt.Sprintf("pageindex-pair-%02d", index+1),
			"question":         question,
			"expected_doc_ids": []string{document.ID},
			"capability":       model.EvaluationCapabilityPageIndex,
		})
	}
	encodedCases, marshalErr := json.Marshal(caseManifest)
	if marshalErr != nil {
		t.Fatalf("encode live PageIndex A/B cases: %v", marshalErr)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		ChangeSummary: "live RAGFlow PageIndex SearchDataset A/B", Cases: encodedCases,
	})
	if err != nil {
		t.Fatalf("create live PageIndex A/B evaluation set: %v", err)
	}
	for index := range 30 {
		sectionIndex := index%17 + 1
		question := fmt.Sprintf("What does verification rule %d establish in the artifact binding fixture?", sectionIndex)
		if index >= 17 {
			question = fmt.Sprintf("Why is verification rule %d important for live compilation evidence?", sectionIndex)
		}
		if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
			EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version,
			CaseID: fmt.Sprintf("pageindex-ab-%02d", index+1), CaseVersion: int64(index + 1),
			Question: question, ExpectedCitationDocIDs: document.ID,
			Metadata:       json.RawMessage(`{"capability":"page_index"}`),
			PairedSampleID: fmt.Sprintf("pageindex-pair-%02d", index+1),
		}); err != nil {
			t.Fatalf("create live PageIndex A/B case %d: %v", index+1, err)
		}
	}

	evaluationRun, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "pageindex-live-v1", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v1", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v1","top_k":10,"min_samples":20,
			"min_hit_lift_pp":2,"max_p95_latency_delta_ms":2000,
			"max_no_answer_lift_pp":0,"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatalf("create live PageIndex A/B evaluation run: %v", err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenant.ID, user.ID, model.EvaluationCapabilityPageIndex,
		json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v1","top_k":10,"min_samples":20,
			"min_hit_lift_pp":2,"max_p95_latency_delta_ms":2000,
			"max_no_answer_lift_pp":0,"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`))
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, evaluationRun.ID)
	if err != nil {
		t.Fatalf("execute live PageIndex A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("live PageIndex A/B did not complete: %+v", completed)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 60 {
		t.Fatalf("expected 60 paired A/B results: count=%d err=%v", len(results), err)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatalf("decode live PageIndex A/B metrics: %v", err)
	}
	if int(metrics["pair_count"].(float64)) != 30 {
		t.Fatalf("expected 30 pairs, got %+v", metrics)
	}
	t.Logf("live PageIndex SearchDataset A/B complete: dataset=%s pipeline=%s compilation_run=%s evaluation_run=%s gate=%s reason=%s hit_lift_pp=%v p95_delta_ms=%v",
		upstream.ID, pipeline.ID, confirmed.ID, completed.ID, completed.GateStatus,
		completed.GateReason, metrics["hit_lift_pp"], metrics["p95_latency_delta_ms"])
}

func runLivePageIndexSearchDatasetPairedABProtocolE2E(t *testing.T, protocolVersion string) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" || os.Getenv("RGX_ALLOW_SCOPED_EVALUATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 and RGX_ALLOW_SCOPED_EVALUATION_LIVE=1 are required")
	}
	if protocolVersion != scopedEvaluationContractVersionV2 && protocolVersion != scopedEvaluationContractVersionV3 {
		t.Fatalf("unsupported live PageIndex protocol: %s", protocolVersion)
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	groups, _, err := client.ListCompilationTemplateGroups(ctx, ragflow.CompilationTemplateGroupFilter{
		Page: 1, PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list v2 compilation template groups: %v", err)
	}
	var group *ragflow.CompilationTemplateGroup
	for index := range groups {
		for _, template := range groups[index].Templates {
			if strings.EqualFold(template.Kind, model.CompilationKindPageIndex) {
				selected := groups[index]
				group = &selected
				break
			}
		}
		if group != nil {
			break
		}
	}
	if group == nil {
		t.Skip("live PageIndex v2 drill requires a page_index compilation template group")
	}

	embeddingModel := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		models, modelErr := client.ListModels(ctx, "embedding")
		if modelErr != nil {
			t.Fatalf("list v2 embedding models: %v", modelErr)
		}
		for _, candidate := range models {
			if candidate.Name == "qwen3-embedding-0.6b" && candidate.Instance == "gpustack-local" &&
				candidate.Provider == "OpenAI-API-Compatible" {
				embeddingModel = candidate.ModelID
				break
			}
		}
		if embeddingModel == "" {
			t.Fatal("live PageIndex v2 drill requires qwen3-embedding-0.6b")
		}
	}
	chatModel := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_CHAT_MODEL"))
	if chatModel == "" {
		chatModels, modelErr := client.ListModels(ctx, "chat")
		if modelErr != nil {
			t.Fatalf("list v2 chat models: %v", modelErr)
		}
		for _, candidate := range chatModels {
			if strings.HasPrefix(candidate.Name, "glm-5.3-flash") {
				chatModel = candidate.ModelID
				break
			}
		}
		if chatModel == "" {
			t.Fatal("live PageIndex v2 drill requires glm-5.3-flash")
		}
	}

	suffix := id.New()[:8]
	pipeline, pipelineErr := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title:          "rgx-pageindex-ab-v2-" + suffix,
		Description:    "Disposable RAGFlow-X PageIndex v2 diagnostic drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create v2 pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup v2 pipeline %s: %v", pipeline.ID, err)
		}
	}()
	pipelineDetail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get v2 pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(pipelineDetail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("v2 pipeline is not compilation ready: %+v", contract)
	}

	datasetName := "rgx-pageindex-ab-v2-" + suffix
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipeline.ID,
	})
	if err != nil {
		t.Fatalf("create v2 dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup v2 dataset %s: %v", upstream.ID, err)
		}
	}()

	fixture := livePageIndexV2Fixture()
	documentIDs := make([]string, 0, len(fixture))
	for documentIndex, item := range fixture {
		document, createErr := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
			Name:    fmt.Sprintf("%s.md", item.Slug),
			Content: []byte(item.Content),
		})
		if createErr != nil {
			t.Fatalf("create v2 document %d: %v", documentIndex+1, createErr)
		}
		fixture[documentIndex].DocumentID = document.ID
		documentIDs = append(documentIDs, document.ID)
		if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
			Pipeline: true, PipelineID: pipeline.ID,
		}); err != nil {
			t.Fatalf("bind v2 document %d: %v", documentIndex+1, err)
		}
		if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
			t.Fatalf("ingest v2 document %d: %v", documentIndex+1, err)
		}
		if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
			t.Fatalf("wait for v2 document %d: %v", documentIndex+1, err)
		}
	}

	svc, ctx, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = client
	svc.SetupWorker(fastWorkerConfig())
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstream.ID,
		Name: datasetName, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create v2 dataset link: %v", err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindPageIndex, DocumentIDs: documentIDs,
	})
	if err != nil {
		t.Fatalf("trigger v2 compilation: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch v2 compilation: %v", err)
	}
	confirmed := waitLiveCompilationRunConfirmed(t, ctx, svc, tenant.ID, dataset.ID, run.ID, 20*time.Minute)
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, run.ID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected v2 artifact projections: count=%d err=%v", len(artifacts), err)
	}

	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: dataset.ID,
		StrategyType:       model.KnowledgeStrategyCompiled,
		RAGFlowCapability:  model.RAGFlowCapabilityKnowledgeCompilation,
		Config:             `{"compilation_template":"page_index"}`,
		FallbackStrategy:   model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatalf("create v2 strategy: %v", err)
	}

	caseManifest := make([]map[string]interface{}, 0, 48)
	pairIndex := 0
	for _, item := range fixture {
		for _, topic := range item.Topics {
			pairIndex++
			caseManifest = append(caseManifest, map[string]interface{}{
				"case_id":          fmt.Sprintf("pageindex-v2-%02d", pairIndex),
				"paired_sample_id": fmt.Sprintf("pageindex-v2-pair-%02d", pairIndex),
				"question":         topic.Question,
				"expected_doc_ids": []string{item.DocumentID},
				"capability":       model.EvaluationCapabilityPageIndex,
			})
		}
	}
	if pairIndex != 48 {
		t.Fatalf("v2 fixture produced %d pairs, expected 48", pairIndex)
	}
	encodedCases, marshalErr := json.Marshal(caseManifest)
	if marshalErr != nil {
		t.Fatalf("encode v2 cases: %v", marshalErr)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		ChangeSummary: "pre-registered PageIndex v2 diagnostic live A/B", Cases: encodedCases,
	})
	if err != nil {
		t.Fatalf("create v2 evaluation set: %v", err)
	}
	for index, manifest := range caseManifest {
		documentID := manifest["expected_doc_ids"].([]string)[0]
		if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
			EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version,
			CaseID: fmt.Sprintf("pageindex-v2-%02d", index+1), CaseVersion: int64(index + 1),
			Question: manifest["question"].(string), ExpectedCitationDocIDs: documentID,
			Metadata:       json.RawMessage(`{"capability":"page_index","task_type":"project_state"}`),
			PairedSampleID: fmt.Sprintf("pageindex-v2-pair-%02d", index+1),
		}); err != nil {
			t.Fatalf("create v2 case %d: %v", index+1, err)
		}
	}

	evaluationRun, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "pageindex-live-v2", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v2", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: scopedPageIndexLiveConfig(protocolVersion),
	})
	if err != nil {
		t.Fatalf("create live PageIndex evaluation run: %v", err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenant.ID, user.ID, model.EvaluationCapabilityPageIndex,
		scopedPageIndexLiveConfig(protocolVersion))
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, evaluationRun.ID)
	if err != nil {
		t.Fatalf("execute v2 live A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("v2 live A/B did not complete: %+v", completed)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 96 {
		t.Fatalf("expected 96 paired v2 results: count=%d err=%v", len(results), err)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatalf("decode v2 metrics: %v", err)
	}
	if int(metrics["pair_count"].(float64)) != 48 {
		t.Fatalf("expected 48 pairs, got %+v", metrics)
	}
	evidence := map[string]interface{}{
		"dataset_id":      upstream.ID,
		"pipeline_id":     pipeline.ID,
		"compilation_run": confirmed.ID,
		"evaluation_run":  completed.ID,
		"gate_status":     completed.GateStatus,
		"gate_reason":     completed.GateReason,
		"metrics":         metrics,
	}
	encodedEvidence, encodeErr := json.MarshalIndent(evidence, "", "  ")
	if encodeErr != nil {
		t.Fatalf("encode v2 evidence: %v", encodeErr)
	}
	if err := os.MkdirAll("test-results", 0o755); err != nil {
		t.Fatalf("create v2 evidence directory: %v", err)
	}
	evidenceName := "pageindex-v2-live-ab.json"
	if protocolVersion == scopedEvaluationContractVersionV3 {
		evidenceName = "pageindex-v3-live-ab.json"
	}
	if err := os.WriteFile("test-results/"+evidenceName, encodedEvidence, 0o600); err != nil {
		t.Fatalf("write v2 evidence: %v", err)
	}
	t.Logf("live PageIndex v2 A/B complete: dataset=%s pipeline=%s compilation_run=%s evaluation_run=%s gate=%s reason=%s hit_lift_pp=%v artifact_pairs=%v p95_delta_ms=%v",
		upstream.ID, pipeline.ID, confirmed.ID, completed.ID, completed.GateStatus,
		completed.GateReason, metrics["hit_lift_pp"], metrics["compiled_artifact_pair_count"],
		metrics["p95_latency_delta_ms"])
}

func TestLivePageIndexSearchDatasetPairedABV2E2E(t *testing.T) {
	runLivePageIndexSearchDatasetPairedABProtocolE2E(t, scopedEvaluationContractVersionV2)
}

func TestLivePageIndexSearchDatasetPairedABV3E2E(t *testing.T) {
	runLivePageIndexSearchDatasetPairedABProtocolE2E(t, scopedEvaluationContractVersionV3)
}

func scopedPageIndexLiveConfig(protocolVersion string) json.RawMessage {
	if protocolVersion == scopedEvaluationContractVersionV3 {
		return json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":30,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":2500,
			"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
			"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":15,
			"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`)
	}
	return json.RawMessage(`{
		"contract_version":"scoped-paired-ab-v2","top_k":8,"min_samples":30,
		"min_hit_lift_pp":2,"max_p95_latency_delta_ms":2500,
		"max_no_answer_lift_pp":0,"min_compiled_artifact_pairs":15,
		"answer_correctness_method":"expected_evidence_match",
		"require_citation_precision":true
	}`)
}

func waitLiveCompilationRunConfirmed(
	t *testing.T, ctx context.Context, svc *Service, tenantID, datasetID, runID string, timeout time.Duration,
) *model.CompilationRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
			t.Fatalf("reconcile live PageIndex A/B compilation: %v", err)
		}
		current, err := svc.Store.GetCompilationRun(ctx, tenantID, runID)
		if err != nil {
			t.Fatalf("load live PageIndex A/B compilation: %v", err)
		}
		if current != nil && current.State == model.CompilationRunConfirmed {
			return current
		}
		if current != nil && current.State == model.CompilationRunFailed {
			t.Fatalf("live PageIndex A/B compilation failed: %s", current.Detail)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live PageIndex A/B context ended: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	t.Fatalf("live PageIndex A/B compilation did not confirm in time: run=%s", runID)
	return nil
}

type livePageIndexV2Topic struct {
	Question string
	Summary  string
}

type livePageIndexV2Item struct {
	Slug       string
	Domain     string
	Topics     []livePageIndexV2Topic
	Content    string
	DocumentID string
}

func livePageIndexV2Fixture() []livePageIndexV2Item {
	domains := []string{
		"Operations Platform", "Billing Console", "Security Gateway",
		"Data Exchange", "Mobile Companion", "Audit Studio",
	}
	topicNames := []string{
		"cold start recovery", "regional failover", "quota escalation",
		"credential rotation", "partner onboarding", "offline sync",
		"exception triage", "release rollback",
	}
	items := make([]livePageIndexV2Item, 0, len(domains))
	for _, domain := range domains {
		topics := make([]livePageIndexV2Topic, 0, len(topicNames))
		var builder strings.Builder
		builder.WriteString("# " + domain + " Navigation Handbook\n\n")
		builder.WriteString("This handbook organizes the operational pages used by the support and delivery teams.\n\n")
		for topicIndex, topicName := range topicNames {
			action := map[int]string{
				0: "restart the edge worker, replay queued state, and verify health probes",
				1: "fail traffic to the standby region and verify data consistency",
				2: "review current usage, approve a temporary ceiling, and notify the owner",
				3: "issue a staged replacement, revoke the old secret, and validate access",
				4: "provision the integration scope, exchange keys, and run a sandbox transaction",
				5: "preserve the local mutation log, reconcile conflicts, and resume uploads",
				6: "classify the exception, attach the trace bundle, and route it to the owning squad",
				7: "freeze the rollout, restore the prior release manifest, and verify session compatibility",
			}[topicIndex]
			summary := fmt.Sprintf(
				"The %s %s page explains how to %s while keeping tenant scope and audit evidence intact.",
				domain, topicName, action,
			)
			topics = append(topics, livePageIndexV2Topic{
				Question: fmt.Sprintf("What does the %s %s page explain?", domain, topicName),
				Summary:  summary,
			})
			builder.WriteString(fmt.Sprintf("## %s %s\n%s\n", domain, topicName, summary))
		}
		items = append(items, livePageIndexV2Item{
			Slug:   strings.ToLower(strings.ReplaceAll(domain, " ", "-")) + "-handbook",
			Domain: domain, Topics: topics, Content: builder.String(),
		})
	}
	return items
}

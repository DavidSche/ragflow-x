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

func TestLiveWikiSearchDatasetPairedABV3E2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" || os.Getenv("RGX_ALLOW_SCOPED_EVALUATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 and RGX_ALLOW_SCOPED_EVALUATION_LIVE=1 are required")
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	embeddingModel := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		models, modelErr := client.ListModels(ctx, "embedding")
		if modelErr != nil {
			t.Fatalf("list live wiki embedding models: %v", modelErr)
		}
		for _, candidate := range models {
			if candidate.Name == "qwen3-embedding-0.6b" && candidate.Instance == "gpustack-local" &&
				candidate.Provider == "OpenAI-API-Compatible" {
				embeddingModel = candidate.ModelID
				break
			}
		}
		if embeddingModel == "" {
			t.Fatal("live wiki A/B drill requires qwen3-embedding-0.6b")
		}
	}
	chatModel := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_CHAT_MODEL"))
	if chatModel == "" {
		chatModels, modelErr := client.ListModels(ctx, "chat")
		if modelErr != nil {
			t.Fatalf("list live wiki chat models: %v", modelErr)
		}
		for _, candidate := range chatModels {
			if strings.HasPrefix(candidate.Name, "glm-5.3-flash") {
				chatModel = fmt.Sprintf("%s@%s@%s", candidate.Name, candidate.Instance, candidate.Provider)
				break
			}
		}
		if chatModel == "" {
			t.Fatal("live wiki A/B drill requires glm-5.3-flash")
		}
	}

	suffix := id.New()[:8]
	group, groupErr := client.SaveCompilationTemplateGroup(ctx, ragflow.CompilationTemplateGroupRequest{
		Name:        "rgx-wiki-ab-" + suffix,
		Description: "Disposable RAGFlow-X Wiki scoped evaluation drill",
		Templates: []ragflow.CompilationTemplateGroupTemplate{{
			Name: "rgx-wiki-ab-" + suffix, Kind: model.CompilationKindWiki,
		}},
	})
	if groupErr != nil {
		t.Fatalf("create live wiki template group: %v", groupErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := client.DeleteCompilationTemplateGroup(cleanupCtx, group.ID); err != nil {
			t.Logf("cleanup live wiki template group %s: %v", group.ID, err)
		}
	}()

	pipeline, pipelineErr := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title:          "rgx-wiki-ab-" + suffix,
		Description:    "Disposable RAGFlow-X Wiki scoped evaluation drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create live wiki pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup live wiki pipeline %s: %v", pipeline.ID, err)
		}
	}()
	pipelineDetail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live wiki pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(pipelineDetail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("live wiki pipeline is not compilation ready: %+v", contract)
	}

	datasetName := "rgx-wiki-ab-" + suffix
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipeline.ID,
	})
	if err != nil {
		t.Fatalf("create live wiki dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup live wiki dataset %s: %v", upstream.ID, err)
		}
	}()

	fixture := liveWikiV3Fixture()
	documentIDs := make([]string, 0, len(fixture))
	for documentIndex, item := range fixture {
		document, createErr := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
			Name: fmt.Sprintf("%s.md", item.Slug), Content: []byte(item.Content),
		})
		if createErr != nil {
			t.Fatalf("create live wiki document %d: %v", documentIndex+1, createErr)
		}
		fixture[documentIndex].DocumentID = document.ID
		documentIDs = append(documentIDs, document.ID)
		if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
			Pipeline: true, PipelineID: pipeline.ID,
		}); err != nil {
			t.Fatalf("bind live wiki document %d: %v", documentIndex+1, err)
		}
		if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
			t.Fatalf("ingest live wiki document %d: %v", documentIndex+1, err)
		}
		if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
			t.Fatalf("wait for live wiki document %d: %v", documentIndex+1, err)
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
		t.Fatalf("create live wiki dataset link: %v", err)
	}
	evidenceModel := map[string]interface{}{
		"embedding": embeddingModel,
		"chat":      chatModel,
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindWiki, DocumentIDs: documentIDs,
	})
	if err != nil {
		t.Fatalf("trigger live wiki compilation: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live wiki compilation: %v", err)
	}
	confirmed := waitLiveCompilationRunConfirmed(t, ctx, svc, tenant.ID, dataset.ID, run.ID, 20*time.Minute)
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, run.ID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected live wiki artifact projections: count=%d err=%v", len(artifacts), err)
	}

	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: dataset.ID,
		StrategyType:       model.KnowledgeStrategyCompiled,
		RAGFlowCapability:  model.RAGFlowCapabilityKnowledgeCompilation,
		Config:             `{"compilation_template":"wiki"}`,
		FallbackStrategy:   model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatalf("create live wiki strategy: %v", err)
	}

	caseManifest := make([]map[string]interface{}, 0, 30)
	pairIndex := 0
	for _, item := range fixture {
		for _, topic := range item.Topics {
			pairIndex++
			caseManifest = append(caseManifest, map[string]interface{}{
				"case_id":          fmt.Sprintf("wiki-v3-%02d", pairIndex),
				"paired_sample_id": fmt.Sprintf("wiki-v3-pair-%02d", pairIndex),
				"question":         topic.Question,
				"expected_doc_ids": []string{item.DocumentID},
				"capability":       model.EvaluationCapabilityWiki,
			})
		}
	}
	if pairIndex != 30 {
		t.Fatalf("live wiki fixture produced %d pairs, expected 30", pairIndex)
	}
	encodedCases, marshalErr := json.Marshal(caseManifest)
	if marshalErr != nil {
		t.Fatalf("encode live wiki cases: %v", marshalErr)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		ChangeSummary: "pre-registered Wiki v3 live A/B", Cases: encodedCases,
	})
	if err != nil {
		t.Fatalf("create live wiki evaluation set: %v", err)
	}
	for index, manifest := range caseManifest {
		if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
			EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version,
			CaseID: fmt.Sprintf("wiki-v3-%02d", index+1), CaseVersion: int64(index + 1),
			Question:               manifest["question"].(string),
			ExpectedCitationDocIDs: manifest["expected_doc_ids"].([]string)[0],
			Metadata:               json.RawMessage(`{"capability":"wiki","task_type":"project_state"}`),
			PairedSampleID:         fmt.Sprintf("wiki-v3-pair-%02d", index+1),
		}); err != nil {
			t.Fatalf("create live wiki case %d: %v", index+1, err)
		}
	}
	evaluationRun, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "wiki-live-v3", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "wiki-live-v3", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityWiki, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":30,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":2500,
			"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
			"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":15,
			"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatalf("create live wiki evaluation run: %v", err)
	}
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, evaluationRun.ID)
	if err != nil {
		t.Fatalf("execute live wiki A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("live wiki A/B did not complete: %+v", completed)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 60 {
		t.Fatalf("expected 60 paired wiki results: count=%d err=%v", len(results), err)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatalf("decode live wiki metrics: %v", err)
	}
	if int(metrics["pair_count"].(float64)) != 30 {
		t.Fatalf("expected 30 wiki pairs, got %+v", metrics)
	}
	evidence := map[string]interface{}{
		"dataset_id":      upstream.ID,
		"pipeline_id":     pipeline.ID,
		"compilation_run": confirmed.ID,
		"evaluation_run":  completed.ID,
		"document_ids":    documentIDs,
		"models":          evidenceModel,
		"gate_status":     completed.GateStatus,
		"gate_reason":     completed.GateReason,
		"metrics":         metrics,
	}
	encodedEvidence, encodeErr := json.MarshalIndent(evidence, "", "  ")
	if encodeErr != nil {
		t.Fatalf("encode live wiki evidence: %v", encodeErr)
	}
	if err := os.MkdirAll("test-results", 0o755); err != nil {
		t.Fatalf("create live wiki evidence directory: %v", err)
	}
	if err := os.WriteFile("test-results/wiki-v3-live-ab.json", encodedEvidence, 0o600); err != nil {
		t.Fatalf("write live wiki evidence: %v", err)
	}
	t.Logf("live Wiki v3 A/B complete: dataset=%s pipeline=%s compilation_run=%s evaluation_run=%s gate=%s reason=%s citation_lift_pp=%v artifact_effect_pairs=%v p95_delta_ms=%v",
		upstream.ID, pipeline.ID, confirmed.ID, completed.ID, completed.GateStatus,
		completed.GateReason, metrics["citation_precision_lift_pp"],
		metrics["compiled_artifact_effect_pair_count"], metrics["p95_latency_delta_ms"])
}

type liveWikiV3Topic struct {
	Question string
	Summary  string
}

type liveWikiV3Item struct {
	Slug       string
	Domain     string
	Topics     []liveWikiV3Topic
	Content    string
	DocumentID string
}

func liveWikiV3Fixture() []liveWikiV3Item {
	domains := []string{
		"Product Handbook", "Operations Handbook", "Security Handbook",
		"Integration Handbook", "Support Handbook",
	}
	topicNames := []string{
		"onboarding", "incident response", "quota management",
	}
	actions := []string{
		"provision workspace access and verify ownership boundaries",
		"classify severity, attach the trace bundle, and route ownership",
		"review usage, approve a temporary ceiling, and notify the owner",
		"preserve the retention ledger and validate deletion evidence",
		"freeze the manifest, run acceptance checks, and confirm rollback readiness",
	}
	items := make([]liveWikiV3Item, 0, len(domains))
	for _, domain := range domains {
		topics := make([]liveWikiV3Topic, 0, len(topicNames))
		var builder strings.Builder
		builder.WriteString("# " + domain + " Wiki\n\n")
		builder.WriteString("This wiki page consolidates the procedures used by delivery teams.\n\n")
		for topicIndex, topicName := range topicNames {
			summary := fmt.Sprintf("The %s %s section explains how to %s while preserving tenant scope and audit evidence.", domain, topicName, actions[topicIndex%len(actions)])
			topics = append(topics, liveWikiV3Topic{
				Question: fmt.Sprintf("Which wiki section explains how to %s?", actions[topicIndex%len(actions)]),
				Summary:  summary,
			})
			builder.WriteString(fmt.Sprintf("## %s %s\n%s\n", domain, topicName, summary))
		}
		items = append(items, liveWikiV3Item{
			Slug: strings.ToLower(strings.ReplaceAll(domain, " ", "-")), Domain: domain,
			Topics: topics, Content: builder.String(),
		})
	}
	return items
}

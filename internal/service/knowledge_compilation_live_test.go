package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestLiveCompilationPipelineDSLContract(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pipelines, err := client.ListPipelines(ctx)
	if err != nil {
		t.Fatalf("list live pipelines: %v", err)
	}
	if len(pipelines) == 0 {
		t.Skip("live pipeline contract requires at least one builtin pipeline")
	}

	compilerCount := 0
	for _, pipeline := range pipelines {
		detail, err := client.GetPipeline(ctx, pipeline.ID)
		if err != nil {
			t.Errorf("get live pipeline %s: %v", pipeline.ID, err)
			continue
		}
		contract, parsed := inspectCompilationPipelineDSL(detail.DSL)
		if !parsed {
			t.Errorf("pipeline %s has an unparsable DSL", pipeline.ID)
			continue
		}
		hasCompiler := contractBool(contract, "has_compiler")
		if !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
			!contractBool(contract, "has_indexer") || contractBool(contract, "path_valid") != hasCompiler {
			t.Errorf("pipeline %s is not a valid ingestion graph: %+v", pipeline.ID, contract)
			continue
		}
		if hasCompiler {
			compilerCount++
		}
		t.Logf("live pipeline contract pipeline=%s nodes=%d compiler=%t compiler_path=%t",
			pipeline.ID, contract["node_count"], hasCompiler, contract["path_valid"])
	}
	if pipelineID := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_PIPELINE_ID")); pipelineID != "" {
		detail, err := client.GetPipeline(ctx, pipelineID)
		if err != nil {
			t.Fatalf("get live user pipeline %s: %v", pipelineID, err)
		}
		contract, parsed := inspectCompilationPipelineDSL(detail.DSL)
		if !parsed {
			t.Fatalf("live user pipeline %s has an unparsable DSL", pipelineID)
		}
		if !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
			!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
			!contractBool(contract, "path_valid") {
			t.Fatalf("live user pipeline %s is not compilation ready: %+v", pipelineID, contract)
		}
		compilerCount++
		t.Logf("live user compilation pipeline pipeline=%s nodes=%d compiler_path=true",
			pipelineID, contract["node_count"])
	}
	t.Logf("live compilation pipeline DSL contract complete: pipelines=%d compiler_pipelines=%d",
		len(pipelines), compilerCount)
}

type liveIngestCountingClient struct {
	ragflow.Client
	ingestCalls atomic.Int64
}

func (client *liveIngestCountingClient) IngestDocuments(ctx context.Context, documentIDs []string) error {
	client.ingestCalls.Add(1)
	return client.Client.IngestDocuments(ctx, documentIDs)
}

func TestLiveCompilationDispatchRaceE2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 is required for the disposable live dispatch race drill")
	}

	rawClient := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	client := &liveIngestCountingClient{Client: rawClient}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
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
		t.Skip("live dispatch race drill requires a page_index compilation template group")
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
			t.Fatal("live dispatch race drill requires qwen3-embedding-0.6b on gpustack-local/OpenAI-API-Compatible")
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
			t.Fatal("live dispatch race drill requires glm-5.3-flash chat model")
		}
	}

	suffix := id.New()[:8]
	pipelineName := "rgx-dispatch-race-" + suffix
	pipeline, pipelineErr := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title: pipelineName, Description: "Disposable RAGFlow-X dispatch race drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create live dispatch race pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup live dispatch race pipeline %s: %v", pipeline.ID, err)
		}
	}()
	detail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live dispatch race pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(detail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("live dispatch race pipeline is not compilation ready: %+v", contract)
	}

	datasetName := "rgx-dispatch-race-" + suffix
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipeline.ID,
	})
	if err != nil {
		t.Fatalf("create live dispatch race dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup live dispatch race dataset %s: %v", upstream.ID, err)
		}
	}()
	document, err := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
		Name: "dispatch-race.md", Content: []byte("# Dispatch Race\n\nOne admission must produce one real upstream dispatch.\n"),
	})
	if err != nil {
		t.Fatalf("create live dispatch race document: %v", err)
	}
	if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: pipeline.ID,
	}); err != nil {
		t.Fatalf("bind live dispatch race pipeline: %v", err)
	}
	if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
		t.Fatalf("prepare live dispatch race document: %v", err)
	}
	if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
		t.Fatalf("wait for live dispatch race document: %v", err)
	}
	client.ingestCalls.Store(0)

	svc, _, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = client
	svc.SetupWorker(fastWorkerConfig())
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstream.ID,
		Name: datasetName, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create live dispatch race dataset link: %v", err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindPageIndex, DocumentIDs: []string{document.ID},
	})
	if err != nil {
		t.Fatalf("trigger live dispatch race run: %v", err)
	}

	var workers sync.WaitGroup
	workerErrors := make([]error, 2)
	replicas := []*Service{svc, {Store: svc.Store, RAGFlow: client, Runner: svc.Runner}}
	workers.Add(2)
	for index := range workerErrors {
		go func(slot int) {
			defer workers.Done()
			workerErrors[slot] = replicas[slot].ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, "")
		}(index)
	}
	workers.Wait()
	if err := errors.Join(workerErrors...); err != nil {
		t.Fatalf("run live dispatch race workers: %v", err)
	}

	current, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
	if err != nil || current == nil || current.State != model.CompilationRunDispatched {
		t.Fatalf("live dispatch race run must remain dispatched: %+v err=%v", current, err)
	}
	if !strings.Contains(current.Detail, `"has_compiler":true`) ||
		!strings.Contains(current.Detail, `"path_valid":true`) {
		t.Fatalf("live dispatch race run lacks compiler DSL evidence: %s", current.Detail)
	}
	if got := client.ingestCalls.Load(); got != 1 {
		t.Fatalf("live dispatch race must call RAGFlow ingest exactly once: calls=%d run=%s", got, run.ID)
	}
	t.Logf("live compilation dispatch race complete: dataset=%s pipeline=%s run=%s ingest_calls=1",
		upstream.ID, pipeline.ID, run.ID)
}

func liveDispatchRacePipelineDSL(groupID, embeddingModel, chatModel string) map[string]interface{} {
	suffix := id.New()[:6]
	components := map[string]interface{}{
		"File": map[string]interface{}{
			"downstream": []interface{}{"Parser:" + suffix},
			"obj":        map[string]interface{}{"component_name": "File", "params": map[string]interface{}{}},
			"upstream":   []interface{}{},
		},
		"Parser:" + suffix: map[string]interface{}{
			"downstream": []interface{}{"TokenChunker:" + suffix},
			"obj": map[string]interface{}{
				"component_name": "Parser", "params": map[string]interface{}{
					"outputs": map[string]interface{}{
						"html":     map[string]interface{}{"type": "string", "value": ""},
						"json":     map[string]interface{}{"type": "Array<object>", "value": ""},
						"markdown": map[string]interface{}{"type": "string", "value": ""},
						"text":     map[string]interface{}{"type": "string", "value": ""},
					},
				},
			},
			"upstream": []interface{}{"File"},
		},
		"TokenChunker:" + suffix: map[string]interface{}{
			"downstream": []interface{}{"Compiler:" + suffix},
			"obj": map[string]interface{}{
				"component_name": "TokenChunker", "params": map[string]interface{}{
					"chunk_token_size": 512, "delimiters": []interface{}{"\n", "!", "?", ";", "。", "；", "！", "？"},
				},
			},
			"upstream": []interface{}{"Parser:" + suffix},
		},
		"Compiler:" + suffix: map[string]interface{}{
			"downstream": []interface{}{"Tokenizer:" + suffix},
			"obj": map[string]interface{}{
				"component_name": "Compiler", "params": map[string]interface{}{
					"compilation_template_group_id": groupID,
					"embedding_model":               embeddingModel,
					"llm_id":                        chatModel,
					"plan":                          false,
				},
			},
			"upstream": []interface{}{"TokenChunker:" + suffix},
		},
		"Tokenizer:" + suffix: map[string]interface{}{
			"downstream": []interface{}{},
			"obj": map[string]interface{}{
				"component_name": "Tokenizer", "params": map[string]interface{}{
					"fields": "text", "filename_embd_weight": 0.1,
					"search_method": []interface{}{"embedding", "full_text"},
				},
			},
			"upstream": []interface{}{"Compiler:" + suffix},
		},
	}
	return map[string]interface{}{
		"components": components,
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{"id": "File", "type": "fileNode"},
				map[string]interface{}{"id": "Parser:" + suffix, "type": "parserNode"},
				map[string]interface{}{"id": "TokenChunker:" + suffix, "type": "chunkerNode"},
				map[string]interface{}{"id": "Compiler:" + suffix, "type": "compilationNode"},
				map[string]interface{}{"id": "Tokenizer:" + suffix, "type": "tokenizerNode"},
			},
			"edges": []interface{}{
				map[string]interface{}{"source": "File", "target": "Parser:" + suffix},
				map[string]interface{}{"source": "Parser:" + suffix, "target": "TokenChunker:" + suffix},
				map[string]interface{}{"source": "TokenChunker:" + suffix, "target": "Compiler:" + suffix},
				map[string]interface{}{"source": "Compiler:" + suffix, "target": "Tokenizer:" + suffix},
			},
		},
	}
}

func TestLiveCompilationDispatchE2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	pipelineID := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_PIPELINE_ID"))
	if baseURL == "" || pipelineID == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL and RGX_COMPILATION_LIVE_PIPELINE_ID are required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 is required for the disposable live dispatch drill")
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	svc, _, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = client
	svc.SetupWorker(fastWorkerConfig())

	datasetName := fmt.Sprintf("rgx-compilation-e2e-%s", id.New()[:8])
	embeddingModel := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		models, err := client.ListModels(ctx, "embedding")
		if err != nil {
			t.Fatalf("list live embedding models: %v", err)
		}
		for _, candidate := range models {
			if candidate.Name == "qwen3-embedding-0.6b" && candidate.Instance == "gpustack-local" &&
				candidate.Provider == "OpenAI-API-Compatible" {
				embeddingModel = candidate.ModelID
				break
			}
		}
		if embeddingModel == "" {
			t.Fatal("live compilation E2E requires qwen3-embedding-0.6b on gpustack-local/OpenAI-API-Compatible")
		}
	}
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipelineID,
	})
	if err != nil {
		t.Fatalf("create live compilation dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup live compilation dataset %s: %v", upstream.ID, err)
		}
	}()

	document, err := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
		Name:    "compilation-e2e.md",
		Content: []byte("# Compilation E2E\n\n## Safety Baseline\n\nThe gateway requires explicit authorization.\n\n## Evidence Rules\n\nUnverified compilation artifacts must not enter the default route.\n"),
	})
	if err != nil {
		t.Fatalf("create live compilation document: %v", err)
	}
	if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: pipelineID,
	}); err != nil {
		t.Fatalf("bind live compiler pipeline: %v", err)
	}
	if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
		t.Fatalf("ingest live compilation document: %v", err)
	}
	if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
		t.Fatalf("wait for live document parse: %v", err)
	}

	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstream.ID,
		Name: datasetName, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create dataset link: %v", err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{document.ID},
	})
	if err != nil {
		t.Fatalf("trigger live compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live compilation run: %v", err)
	}
	dispatched, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
	if err != nil || dispatched == nil || dispatched.State != model.CompilationRunDispatched {
		t.Fatalf("expected dispatched live run: %+v err=%v", dispatched, err)
	}
	if !strings.Contains(dispatched.Detail, `"has_compiler":true`) ||
		!strings.Contains(dispatched.Detail, `"path_valid":true`) {
		t.Fatalf("dispatched run lacks compiler DSL evidence: %s", dispatched.Detail)
	}

	deadline := time.Now().Add(15 * time.Minute)
	var confirmed *model.CompilationRun
	for time.Now().Before(deadline) {
		if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
			t.Fatalf("reconcile live compilation run: %v", err)
		}
		current, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		if err != nil {
			t.Fatalf("load live compilation run: %v", err)
		}
		if current != nil && current.State == model.CompilationRunConfirmed {
			confirmed = current
			break
		}
		if current != nil && current.State == model.CompilationRunFailed {
			t.Fatalf("live compilation failed: %s", current.Detail)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live compilation context ended: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if confirmed == nil {
		current, _ := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		t.Fatalf("live compilation did not confirm in time: %+v", current)
	}
	if !strings.Contains(confirmed.Detail, `"evidence_state":"unverified"`) ||
		!strings.Contains(confirmed.Detail, `"artifact_run_binding":false`) {
		t.Fatalf("confirmed live run must remain unverified: %s", confirmed.Detail)
	}
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, run.ID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected artifact projection: count=%d err=%v", len(artifacts), err)
	}
	for _, artifact := range artifacts {
		if artifact.State != model.ArtifactProjectionUnverified || artifact.ArtifactHash == "" {
			t.Fatalf("artifact projection must remain unverified metadata: %+v", artifact)
		}
	}
	detail := map[string]interface{}{}
	if err := json.Unmarshal([]byte(confirmed.Detail), &detail); err != nil {
		t.Fatalf("decode confirmed detail: %v", err)
	}
	t.Logf("live compilation dispatch E2E complete: dataset=%s run=%s artifacts=%d state=%s",
		upstream.ID, confirmed.ID, len(artifacts), confirmed.State)
}

func TestLiveCompilationCompletedArtifactBindingE2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 is required for the completed artifact binding live drill")
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
		t.Skip("live artifact binding drill requires a page_index compilation template group")
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
			t.Fatal("live artifact binding drill requires qwen3-embedding-0.6b on gpustack-local/OpenAI-API-Compatible")
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
			t.Fatal("live artifact binding drill requires glm-5.3-flash chat model")
		}
	}

	suffix := id.New()[:8]
	pipelineName := "rgx-artifact-binding-" + suffix
	pipeline, pipelineErr := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title: pipelineName, Description: "Disposable RAGFlow-X completed artifact binding drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create live artifact binding pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup live artifact binding pipeline %s: %v", pipeline.ID, err)
		}
	}()
	pipelineDetail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live artifact binding pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(pipelineDetail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("live artifact binding pipeline is not compilation ready: %+v", contract)
	}

	datasetName := "rgx-artifact-binding-" + suffix
	upstream, err := client.CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name: datasetName, EmbeddingModel: embeddingModel,
		ParseType: 2, PipelineID: pipeline.ID,
	})
	if err != nil {
		t.Fatalf("create live artifact binding dataset: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, upstream.ID); err != nil {
			t.Logf("cleanup live artifact binding dataset %s: %v", upstream.ID, err)
		}
	}()
	document, err := client.CreateDocument(ctx, upstream.ID, &ragflow.DocumentUpload{
		Name:    "artifact-binding.md",
		Content: []byte(liveArtifactBindingDocumentContent()),
	})
	if err != nil {
		t.Fatalf("create live artifact binding document: %v", err)
	}
	if err := client.UpdateDocumentParseConfig(ctx, upstream.ID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: pipeline.ID,
	}); err != nil {
		t.Fatalf("bind live artifact binding pipeline: %v", err)
	}
	if err := client.IngestDocuments(ctx, []string{document.ID}); err != nil {
		t.Fatalf("ingest live artifact binding document: %v", err)
	}
	if err := waitLiveCompilationDocument(t, ctx, client, upstream.ID, document.ID, 8*time.Minute); err != nil {
		t.Fatalf("wait for live artifact binding document: %v", err)
	}

	svc, _, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = client
	svc.SetupWorker(fastWorkerConfig())
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstream.ID,
		Name: datasetName, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create live artifact binding dataset link: %v", err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: id.New(), Kind: model.CompilationKindPageIndex, DocumentIDs: []string{document.ID},
	})
	if err != nil {
		t.Fatalf("trigger live artifact binding run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live artifact binding run: %v", err)
	}

	deadline := time.Now().Add(15 * time.Minute)
	var confirmed *model.CompilationRun
	for time.Now().Before(deadline) {
		if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
			t.Fatalf("reconcile live artifact binding run: %v", err)
		}
		current, loadErr := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		if loadErr != nil {
			t.Fatalf("load live artifact binding run: %v", loadErr)
		}
		if current != nil && current.State == model.CompilationRunConfirmed {
			confirmed = current
			break
		}
		if current != nil && current.State == model.CompilationRunFailed {
			t.Fatalf("live artifact binding failed: %s", current.Detail)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live artifact binding context ended: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if confirmed == nil {
		current, _ := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		t.Fatalf("live artifact binding did not confirm in time: %+v", current)
	}
	if !strings.Contains(confirmed.Detail, `"artifact_run_binding":true`) ||
		!strings.Contains(confirmed.Detail, `"artifact_hash_binding":true`) {
		t.Fatalf("confirmed live run lacks artifact binding evidence: %s", confirmed.Detail)
	}
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, run.ID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected live artifact projections: count=%d err=%v", len(artifacts), err)
	}
	for _, artifact := range artifacts {
		if artifact.State != model.ArtifactProjectionReady || artifact.ContentHash == "" {
			t.Fatalf("live artifact must be ready with content hash: %+v", artifact)
		}
		snapshot := map[string]interface{}{}
		if err := json.Unmarshal([]byte(artifact.ArtifactJSON), &snapshot); err != nil {
			t.Fatalf("decode live artifact snapshot: %v", err)
		}
		if snapshot["run_binding"] != true || snapshot["content_hash_binding"] != true {
			t.Fatalf("live artifact run/content binding is incomplete: %+v", snapshot)
		}
		matched, _ := snapshot["matched_document_ids"].([]interface{})
		if len(matched) == 0 {
			t.Fatalf("live artifact must bind to a source document: %+v", snapshot)
		}
		if matched[0] != document.ID {
			t.Fatalf("live artifact matched document mismatch: got=%v want=%s", matched[0], document.ID)
		}
	}
	t.Logf("live completed artifact binding E2E complete: dataset=%s pipeline=%s run=%s artifacts=%d state=%s",
		upstream.ID, pipeline.ID, confirmed.ID, len(artifacts), confirmed.State)
}

func liveArtifactBindingDocumentContent() string {
	sections := []string{
		"Run binding establishes the execution boundary for a knowledge compilation product.",
		"Every projection must trace to the accepted dispatch manifest before confirmation.",
		"Content hash binding proves that the stored payload is reproducible from its snapshot.",
		"The gateway treats unverified evidence as diagnostic only and keeps it out of default retrieval.",
		"Reconciliation must preserve the source document identity across retries and replicas.",
		"A stable manifest prevents a completed result from being attached to a newer document set.",
		"Artifact metadata records the run, kind, source documents, and verification state.",
		"Failure paths remain explicit so unknown states require human recovery rather than silent resend.",
		"Post-run validation compares the selected source manifest against the dispatch manifest.",
		"The completed upstream status is accepted only after the artifact list contains bound rows.",
		"Chunk-level provenance and rendered output both participate in the payload fingerprint.",
		"Disposable live drills must clean their pipelines, datasets, and documents after execution.",
		"The recovery queue exposes unknown age, evidence, and approval state to operators.",
		"Avoid automatic re-dispatch because an upstream deletion can otherwise become invisible.",
		"Confirmed runs are downstream evidence, not permission to enable default model routing.",
		"Tenant boundaries apply to metrics, alerts, queue reads, and recovery probes.",
	}
	var builder strings.Builder
	builder.WriteString("# Artifact Binding Fixture\n\n")
	for index, section := range sections {
		builder.WriteString(fmt.Sprintf("## Verification Rule %d\n\n%s The system repeats the rule with source-safe wording so the parser emits multiple grounded chunks. Rule %d is intentionally explicit and deterministic for the live drill.\n\n", index+1, section, index+1))
	}
	return builder.String()
}

func waitLiveCompilationDocument(
	t *testing.T, ctx context.Context, client ragflow.Client, datasetID, documentID string, timeout time.Duration,
) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		documents, err := client.ListDocuments(ctx, datasetID)
		if err != nil {
			return err
		}
		for _, document := range documents {
			if document.ID == documentID {
				if document.Progress >= 1 {
					return nil
				}
				if strings.EqualFold(document.IngestionStatus, "FAILED") {
					message := ""
					if document.LatestIngestionEvent != nil {
						message = document.LatestIngestionEvent.Message
					}
					return fmt.Errorf("live ingestion failed: %s", message)
				}
				t.Logf("live compilation document progress progress=%.2f run=%q enabled=%q",
					document.Progress, document.Status, document.Enabled)
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("document parse timed out")
}

func contractBool(contract map[string]interface{}, key string) bool {
	value, _ := contract[key].(bool)
	return value
}

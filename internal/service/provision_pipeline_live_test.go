package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestProvisionLiveCompilationPipeline provisions a disposable live compilation
// pipeline (page_index template group + dispatch race DSL) and prints its ID so
// that RGX_COMPILATION_LIVE_PIPELINE_ID-consuming drills
// (TestLiveCompilationUnknownRecoveryE2E, TestLiveCompilationBudgetExhaustedE2E)
// can run against it. With RGX_PROVISION_PIPELINE_CLEANUP=1 it deletes the
// pipeline named by RGX_COMPILATION_LIVE_PIPELINE_ID instead of creating one.
//
//	RGX_RAGFLOW_LIVE_BASE_URL=... RGX_ALLOW_COMPILATION_LIVE=1 \
//	  go test ./internal/service -run '^TestProvisionLiveCompilationPipeline$' -count=1 -v
func TestProvisionLiveCompilationPipeline(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" || os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL and RGX_ALLOW_COMPILATION_LIVE=1 are required")
	}
	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if cleanupID := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_PIPELINE_ID")); os.Getenv("RGX_PROVISION_PIPELINE_CLEANUP") == "1" && cleanupID != "" {
		if err := client.DeleteAgent(ctx, cleanupID); err != nil {
			t.Fatalf("cleanup pipeline %s: %v", cleanupID, err)
		}
		t.Logf("cleanup pipeline %s done", cleanupID)
		return
	}

	groups, _, err := client.ListCompilationTemplateGroups(ctx, ragflow.CompilationTemplateGroupFilter{Page: 1, PageSize: 100})
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
		t.Skip("provisioning requires a page_index compilation template group")
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
			t.Fatal("provisioning requires qwen3-embedding-0.6b on gpustack-local/OpenAI-API-Compatible")
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
			t.Fatal("provisioning requires glm-5.3-flash chat model")
		}
	}
	pipeline, err := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title: "rgx-provision-" + id.New()[:8], Description: "Disposable RAGFlow-X live drill pipeline",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if err != nil {
		t.Fatalf("create live pipeline: %v", err)
	}
	detail, err := client.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(detail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("provisioned pipeline is not compilation ready: %+v", contract)
	}
	t.Logf("PROVISIONED_PIPELINE_ID=%s", pipeline.ID)
}

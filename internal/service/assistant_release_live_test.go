package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestLiveAssistantReleaseGoldenPathWithRAGFlowV1RC1(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" || strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_API_KEY")) == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL and RGX_RAGFLOW_LIVE_API_KEY are required")
	}
	if os.Getenv("RGX_ALLOW_ASSISTANT_RELEASE_LIVE") != "1" {
		t.Skip("RGX_ALLOW_ASSISTANT_RELEASE_LIVE=1 is required for the disposable live AssistantRelease drill")
	}

	client := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	svc := newAuthzSvc(t)
	svc.RAGFlow = client
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	suffix := id.New()[:8]
	tenant, err := svc.CreateTenant(ctx, "Live Assistant Release "+suffix)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Live Release Dataset "+suffix)
	if err != nil {
		t.Fatalf("create live dataset: %v", err)
	}
	if err := configureLiveDefaultEmbedding(ctx, client, dataset.RAGFlowDatasetID); err != nil {
		t.Fatalf("configure live embedding: %v", err)
	}
	document, err := client.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{
		Name:    "live-assistant-release-" + suffix + ".md",
		Content: []byte(liveAssistantReleaseDocumentContent(suffix)),
	})
	if err != nil {
		t.Fatalf("create live document: %v", err)
	}
	if err := svc.ParseDocuments(ctx, tenant.ID, dataset.ID, []string{document.ID}); err != nil {
		t.Fatalf("parse live document: %v", err)
	}
	if err := waitLiveAssistantReleaseDocument(ctx, client, dataset.RAGFlowDatasetID, document.ID); err != nil {
		t.Fatalf("wait live document: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.DeleteDataset(cleanupCtx, dataset.RAGFlowDatasetID); err != nil {
			t.Logf("cleanup live dataset %s: %v", dataset.RAGFlowDatasetID, err)
		}
	})
	project, err := svc.CreateProject(ctx, tenant.ID, "Live Release Project "+suffix, "disposable AssistantRelease drill")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "Live Release Chat "+suffix, []string{dataset.ID})
	if err != nil {
		t.Fatalf("create live chat: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := svc.DeleteChat(cleanupCtx, tenant.ID, chat.ID, false); err != nil {
			t.Logf("cleanup live chat %s: %v", chat.ID, err)
		}
	})

	name := "live-assistant-release-" + suffix
	evidenceID, gateID, approvalID := createReleaseEvidenceFixture(t, svc, tenant.ID, name)
	release, err := svc.CreateAssistantRelease(ctx, tenant.ID, "creator-"+suffix, CreateAssistantReleaseInput{
		ProjectID:            project.ID,
		ScenarioTemplateID:   "tmpl-" + name,
		TemplateVersionID:    "tmplver-" + name,
		Name:                 name,
		Description:          "Disposable v1.0.0-rc1 AssistantRelease live drill",
		OwnerID:              "owner-" + suffix,
		ScenarioPackSchema:   "1",
		ScenarioPackPayload:  "{}",
		ExecutionContract:    "{}",
		PolicyVersion:        "policy-v1",
		PolicyJSON:           "{}",
		RuntimeProfile:       "{}",
		DesiredState:         "{}",
		DesiredTargetID:      chat.ID,
		DesiredTargetType:    "chat",
		DesiredTargetVersion: "1",
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_chat", TargetType: "chat", TargetID: chat.ID,
			TargetVersion: "1", RGXResourceID: chat.ID, OwnershipVerified: true,
		}},
		DatasetBindings: []DatasetBindingInput{{
			BindingID: "dataset-main", DatasetID: dataset.ID, DatasetVersion: "1",
		}},
		ModelRouteBindings: []ModelRouteBindingInput{{
			BindingID: "model-main", ProviderID: "provider-main", ModelID: "model-main",
		}},
		EvidenceBundleID: evidenceID, GateDecisionID: gateID,
		GateResult: model.GateDecisionPass, ApprovalID: approvalID,
		IdempotencyKey: "create-" + name,
	})
	if err != nil {
		t.Fatalf("create assistant release: %v", err)
	}
	if err := approveReleaseFixture(svc, tenant.ID, "creator-"+suffix, release.ID); err != nil {
		t.Fatalf("approve assistant release: %v", err)
	}
	if _, err := svc.ApplyAssistantRelease(ctx, tenant.ID, release.ID, "apply-"+name, OperationInput{
		IdempotencyKey: "apply-" + name, FencingToken: svc.currentFencingToken(t, release.ID),
	}); err != nil {
		t.Fatalf("apply assistant release: %v", err)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, tenant.ID, "activate-"+name, release.ID, ActivationInput{
		IdempotencyKey: "activate-" + name, FencingToken: svc.currentFencingToken(t, release.ID),
	}); err != nil {
		t.Fatalf("activate assistant release: %v", err)
	}

	verified, err := svc.GetAssistantRelease(ctx, tenant.ID, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ReleaseState != model.ReleaseActive || verified.ReconcileStatus != model.ReconcileConsistent {
		t.Fatalf("expected ACTIVE/CONSISTENT, got %s/%s", verified.ReleaseState, verified.ReconcileStatus)
	}
	assistant, err := svc.GetAssistant(ctx, tenant.ID, verified.AssistantID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.CurrentAssistantReleaseID != release.ID {
		t.Fatalf("assistant current release = %s, want %s", assistant.CurrentAssistantReleaseID, release.ID)
	}
	t.Logf("live assistant release drill passed release=%s assistant=%s dataset=%s chat=%s",
		release.ID, assistant.ID, dataset.RAGFlowDatasetID, chat.ID)
}

func waitLiveAssistantReleaseDocument(
	ctx context.Context, client *ragflow.HTTPClient, datasetID, documentID string,
) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		documents, err := client.ListDocuments(ctx, datasetID)
		if err != nil {
			return err
		}
		for _, document := range documents {
			if document.ID != documentID {
				continue
			}
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
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("live document parse timed out")
}

func configureLiveDefaultEmbedding(
	ctx context.Context, client *ragflow.HTTPClient, datasetID string,
) error {
	defaults, err := client.ListDefaultModels(ctx)
	if err != nil {
		return err
	}
	models, err := client.ListModels(ctx, "embedding")
	if err != nil {
		return err
	}
	for _, candidate := range defaults {
		if candidate.Type != "embedding" {
			continue
		}
		for _, model := range models {
			if model.ModelID == candidate.ModelID {
				if err := client.UpdateDatasetConfig(ctx, datasetID, ragflow.DatasetConfigUpdate{
					EmbeddingModel: &model.ModelID,
				}); err != nil {
					return err
				}
				return nil
			}
		}
	}
	return errors.New("registered default embedding model not found")
}

func liveAssistantReleaseDocumentContent(suffix string) string {
	sections := []string{
		"Assistant release requires a verified active version and consistent runtime bindings.",
		"Knowledge chat bindings must point to an owned RAGFlow chat resource.",
		"Dataset bindings preserve the source of retrieval authority for the release.",
		"Model route bindings make the execution path explicit and auditable.",
		"Approval and gate evidence must remain attached to every version transition.",
	}
	content := "# Live Assistant Release " + suffix + "\n\n"
	for index, section := range sections {
		content += fmt.Sprintf("## Rule %d\n\n%s Drill suffix %s keeps this fixture disposable.\n\n", index+1, section, suffix)
	}
	return content
}

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func finalizeTestAnswer(t *testing.T, svc *Service, ctx context.Context, tenantID, actorID, requestID string) *AnswerDeliveryResult {
	t.Helper()
	result, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenantID, SessionID: "session-answer", AssistantID: "assistant-1",
		PrincipalID: actorID, Question: " What is retrieval ? ", RequestID: requestID,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Summary: "summary", UserMessage: "What is retrieval ?",
		Content: "The answer uses retrieval.",
		Citations: []model.AnswerCitation{{
			ID: "citation-1", Title: "Policy", ChunkID: "chunk-1",
			CitedContentExcerpt: "Retrieval locates relevant policy text.",
			CitationLocator:     "policy.md#L1",
		}},
		Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("finalize answer delivery: %v", err)
	}
	return result
}

// TestAnswerDeliveryProjectionRedactsRestrictedDatasets covers doc/118 F-06:
// citations from a restricted/confidential dataset must be REDACTED in the
// authorization projection, with the sensitivity level recorded in
// redaction_reasons; internal datasets stay VISIBLE.
func TestAnswerDeliveryProjectionRedactsRestrictedDatasets(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Redaction Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "redaction-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	restricted := &model.DatasetLink{ID: "dataset-restricted", TenantID: tenant.ID, RAGFlowDatasetID: "rag-restricted", Name: "Restricted", Sensitivity: "restricted"}
	if err := svc.Store.CreateDatasetLink(ctx, restricted); err != nil {
		t.Fatal(err)
	}
	result, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "session-redaction", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: "restricted question", RequestID: "request-redaction-1",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: "answer with restricted citation",
		Citations: []model.AnswerCitation{{
			ID: "citation-restricted", DatasetID: restricted.ID, ChunkID: "chunk-1",
			CitedContentExcerpt: "restricted text", CitationLocator: "secret.md#L1",
		}},
		Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Projection.CitationVisibility != model.AuthorizationVisibilityRedacted {
		t.Fatalf("restricted citation must be redacted: %+v", result.Projection)
	}
	if !strings.Contains(result.Projection.RedactionReasonsJSON, "sensitivity:restricted") {
		t.Fatalf("redaction reason missing: %s", result.Projection.RedactionReasonsJSON)
	}
}

// TestAnswerDeliveryProjectionFailsClosedOnUnknownSensitivity pins the fail-
// closed behavior for unknown sensitivity levels (doc/118 F-06).
func TestAnswerDeliveryProjectionFailsClosedOnUnknownSensitivity(t *testing.T) {
	visibility, reasons := citationRedactionReasons(`[{"id":"c1","dataset_id":"d1"}]`, func(string) (string, error) {
		return "top-secret-unknown", nil
	})
	if visibility != model.AuthorizationVisibilityRedacted || len(reasons) == 0 {
		t.Fatalf("unknown sensitivity must fail closed: %s %v", visibility, reasons)
	}
	visible, reasons := citationRedactionReasons(`[{"id":"c1","dataset_id":"d1"}]`, func(string) (string, error) {
		return "internal", nil
	})
	if visible != model.AuthorizationVisibilityVisible || len(reasons) != 0 {
		t.Fatalf("internal sensitivity must stay visible: %s %v", visible, reasons)
	}
}

func TestAnswerDeliveryCanonicalSnapshotAndProjection(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Answer Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "answer-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	result := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-answer-1")
	if result.Snapshot.AnswerSchemaVersion != model.AnswerSchemaVersion || result.Snapshot.CanonicalHash == "" {
		t.Fatalf("canonical snapshot contract missing: %+v", result.Snapshot)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered || result.Run.CompletionReason != model.CompletionReasonNormal {
		t.Fatalf("status and completion must be separate: %+v", result.Run)
	}
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(result.Snapshot.CitationsJSON), &citations); err != nil {
		t.Fatal(err)
	}
	if len(citations) != 1 || citations[0].CitationContentHash == "" || citations[0].CitationLocator == "" {
		t.Fatalf("citation must be locatable and verifiable: %+v", citations)
	}
	projection := result.Projection
	for _, visibility := range []string{projection.AnswerVisibility, projection.ContentVisibility, projection.CitationVisibility, projection.ArtifactVisibility, projection.ExecutionVisibility, projection.MetadataVisibility, projection.ActionsVisibility} {
		if visibility != model.AuthorizationVisibilityVisible {
			t.Fatalf("projection surface is not covered: %+v", projection)
		}
	}
	if projection.PolicyInputHash == "" || projection.DecisionHash == "" {
		t.Fatalf("projection decision evidence is missing: %+v", projection)
	}
	snapshot, gotProjection, err := svc.GetAnswerSnapshot(ctx, admin.ID, tenant.ID, result.Snapshot.ID, "web")
	if err != nil || snapshot.ID != result.Snapshot.ID || gotProjection.ID != projection.ID {
		t.Fatalf("get answer snapshot: %v", err)
	}
}

func TestAnswerSnapshotCrossChannelProjectionConsistency(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Cross Channel Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "cross-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-cross-channel")
	for _, channel := range []string{"web", "api", "wecom"} {
		snapshot, projection, err := svc.GetAnswerSnapshot(ctx, admin.ID, tenant.ID, answer.Snapshot.ID, channel)
		if err != nil {
			t.Fatalf("get %s projection: %v", channel, err)
		}
		if snapshot.ID != answer.Snapshot.ID || snapshot.CanonicalHash != answer.Snapshot.CanonicalHash {
			t.Fatalf("%s returned a different canonical snapshot", channel)
		}
		if projection.Channel != channel || projection.AnswerSnapshotID != answer.Snapshot.ID {
			t.Fatalf("%s projection inconsistent: %+v", channel, projection)
		}
	}
}

func TestConversationExportRendersAllAuthorizedAnswers(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Conversation Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "conversation-export-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	first := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-conversation-1")
	second, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "session-answer", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: " How do citations work ? ", RequestID: "request-conversation-2",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Summary: "citation summary", UserMessage: "How do citations work ?",
		Content:   "Citations point to immutable evidence.",
		Citations: []model.AnswerCitation{}, Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: second.Snapshot.ID, Format: model.ExportFormatMarkdown,
		Scope: ExportScopeConversation, IdempotencyKey: "conversation-export-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(result.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	exported := string(content)
	for _, expected := range []string{
		"What is retrieval ?", "The answer uses retrieval.",
		"How do citations work ?", "Citations point to immutable evidence.",
		"Canonical Hash", first.Snapshot.ID, second.Snapshot.ID,
	} {
		if !strings.Contains(exported, expected) {
			t.Fatalf("conversation export missing %q: %s", expected, exported)
		}
	}
	for _, format := range []string{model.ExportFormatDOCX, model.ExportFormatPDF} {
		document, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
			AnswerSnapshotID: second.Snapshot.ID, Format: format,
			Scope: ExportScopeConversation, IdempotencyKey: "conversation-export-" + format,
		})
		if err != nil {
			t.Fatalf("conversation %s export: %v", format, err)
		}
		if document.Artifact == nil || document.Artifact.Format != format {
			t.Fatalf("conversation %s artifact missing: %+v", format, document.Artifact)
		}
	}
}

func TestLargeExportUsesQueuedJob(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Large Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "large-export-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "large-export-session", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: "large export", RequestID: "request-large-export",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: strings.Repeat("answer ", 40960),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatMarkdown,
		IdempotencyKey: "large-export-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != model.ExportJobQueued || result.Artifact != nil {
		t.Fatalf("large export must remain queued: %+v", result.Job)
	}
}

func TestAnswerExportSnapshotJobAndDownloadAudit(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "export-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	answer := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-export-1")
	first, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatMarkdown,
		IdempotencyKey: "export-key", TraceID: "trace-export",
	})
	if err != nil {
		t.Fatalf("create export: %v", err)
	}
	if first.Job.Status != model.ExportJobSucceeded || first.Artifact.SHA256 == "" || first.Artifact.FileRef == "" {
		t.Fatalf("export lifecycle incomplete: %+v %+v", first.Job, first.Artifact)
	}
	content, err := os.ReadFile(first.Artifact.FileRef)
	if err != nil || !strings.Contains(string(content), answer.Snapshot.ID) || !strings.Contains(string(content), "Citations") {
		t.Fatalf("export renderer content invalid: %v", err)
	}
	second, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatMarkdown,
		IdempotencyKey: "export-key", TraceID: "trace-export",
	})
	if err != nil || second.Job.ID != first.Job.ID || second.Artifact.ID != first.Artifact.ID {
		t.Fatalf("export idempotency failed: %v", err)
	}
	if _, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatJSON,
		IdempotencyKey: "export-key", TraceID: "trace-conflict",
	}); err == nil {
		t.Fatal("idempotency key reuse with a different format must conflict")
	}
	docx, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatDOCX,
		IdempotencyKey: "export-key-docx", TraceID: "trace-docx",
	})
	if err != nil || docx.Job.Status != model.ExportJobSucceeded {
		t.Fatalf("create docx export: %v", err)
	}
	docxContent, err := os.ReadFile(docx.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	docxReader, err := zip.NewReader(bytes.NewReader(docxContent), int64(len(docxContent)))
	if err != nil {
		t.Fatalf("invalid docx package: %v", err)
	}
	var documentXML string
	for _, file := range docxReader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		documentXML = string(data)
	}
	if !strings.Contains(documentXML, "RAGFlow-X Business Answer") || !strings.Contains(documentXML, answer.Snapshot.ID) {
		t.Fatalf("docx content missing canonical answer: %s", documentXML)
	}
	pdfFont := filepath.Join(`C:\Windows\Fonts`, "Deng.ttf")
	if _, err := os.Stat(pdfFont); err == nil {
		t.Setenv("RGX_EXPORT_PDF_FONT", pdfFont)
		pdf, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
			AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatPDF,
			IdempotencyKey: "export-key-pdf", TraceID: "trace-pdf",
		})
		if err != nil || pdf.Job.Status != model.ExportJobSucceeded {
			t.Fatalf("create pdf export: %v", err)
		}
		pdfContent, err := os.ReadFile(pdf.Artifact.FileRef)
		if err != nil || !strings.HasPrefix(string(pdfContent), "%PDF-") {
			t.Fatalf("invalid pdf artifact: %v", err)
		}
	}
	download, err := svc.OpenExportArtifact(ctx, admin.ID, tenant.ID, first.Artifact.ID, "trace-download")
	if err != nil || download.Path != first.Artifact.FileRef {
		t.Fatalf("authorized download failed: %v", err)
	}
	otherTenant, err := svc.CreateTenant(ctx, "Other Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	otherAdmin, err := svc.CreateUser(ctx, otherTenant.ID, "", CreateUserRequest{Username: "other-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OpenExportArtifact(ctx, otherAdmin.ID, otherTenant.ID, first.Artifact.ID, "trace-download"); err == nil {
		t.Fatal("cross-tenant download must be denied")
	}
}

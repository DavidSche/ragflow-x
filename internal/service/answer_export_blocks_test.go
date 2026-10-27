package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func TestParseAnswerContentStructuredBlocks(t *testing.T) {
	content := "# Result\n\nParagraph\n- evidence\n\n| Field | Value |\n| --- | --- |\n| Owner | Finance |\n\n```go\nfmt.Println(\"answer\")\n```\n\n![diagram](https://example.com/diagram.png)\n\n$$E=mc^2$$"
	blocks := parseAnswerContent(content)
	kinds := make([]string, 0, len(blocks))
	for _, block := range blocks {
		kinds = append(kinds, string(block.kind))
	}
	expected := []string{"heading1", "paragraph", "bullet", "table", "code", "image", "formula"}
	if strings.Join(kinds, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected blocks: %v", kinds)
	}
	if len(blocks[3].rows) != 2 || blocks[3].rows[1][0] != "Owner" || blocks[3].rows[1][1] != "Finance" {
		t.Fatalf("unexpected table rows: %+v", blocks[3].rows)
	}
}

func TestParseAnswerContentDeepHeadings(t *testing.T) {
	blocks := parseAnswerContent("### Question\n#### Evidence\n####### Not A Heading\n#NoSpace")
	kinds := make([]string, 0, len(blocks))
	for _, block := range blocks {
		kinds = append(kinds, string(block.kind))
	}
	if strings.Join(kinds, ",") != "heading3,heading3,paragraph,paragraph" {
		t.Fatalf("unexpected deep heading blocks: %v", kinds)
	}
}

func TestDOCXRendererStructuredBlocks(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Structured Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "structured-export-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	content := "| Field | Value |\n| --- | --- |\n| Owner | Contract Team |\n\n```sql\nSELECT policy FROM documents\n```\n\n![architecture](https://example.com/architecture.png)\n\n$$confidence = support \\times evidence$$"
	answer, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "structured-session", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: "structured export", RequestID: "request-structured-export",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatDOCX,
		IdempotencyKey: "structured-export-key", TraceID: "trace-structured-export",
	})
	if err != nil || exported.Job.Status != model.ExportJobSucceeded {
		t.Fatalf("structured export: job=%+v err=%v", exported.Job, err)
	}
	contentBytes, err := os.ReadFile(exported.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(contentBytes), int64(len(contentBytes)))
	if err != nil {
		t.Fatal(err)
	}
	var documentXML string
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(entry)
		_ = entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		documentXML = string(data)
	}
	for _, required := range []string{"<w:tbl>", "Contract Team", "SELECT policy", "architecture", "confidence = support"} {
		if !strings.Contains(documentXML, required) {
			t.Fatalf("structured DOCX is missing %q", required)
		}
	}
	if !strings.Contains(documentXML, "structured blocks") {
		t.Fatalf("structured DOCX capability notice is missing")
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "exports", tenant.ID)); err != nil {
		t.Fatalf("artifact directory missing: %v", err)
	}
}

func TestExportRendererRejectsUnsupportedSchema(t *testing.T) {
	snapshot := &model.AnswerSnapshot{AnswerSchemaVersion: "answer.v999", Content: "answer"}
	projection := &model.AuthorizationProjection{
		AnswerVisibility:  model.AuthorizationVisibilityVisible,
		ContentVisibility: model.AuthorizationVisibilityVisible,
	}
	job := &model.ExportJob{Format: model.ExportFormatMarkdown}
	_, err := renderExport(snapshot, projection, job, "export-snapshot")
	var expected *httperr.Error
	if !errors.As(err, &expected) || expected.Code != 40134 {
		t.Fatalf("unsupported schema error: %v", err)
	}
}

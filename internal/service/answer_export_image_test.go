package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type exportImageClient struct {
	ragflow.Mock
	image    []byte
	chunk    *ragflow.Chunk
	chunkErr error
}

func (c *exportImageClient) GetChunkImage(context.Context, string) ([]byte, string, error) {
	return c.image, "image/png", nil
}

func (c *exportImageClient) GetChunk(context.Context, string, string, string) (*ragflow.Chunk, error) {
	return c.chunk, c.chunkErr
}

func pdfImageTestPNG(t *testing.T, withPixel bool) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 12, 8))
	if withPixel {
		source.Set(1, 1, color.RGBA{R: 220, G: 40, B: 40, A: 255})
	}
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func pdfImageTestContext(t *testing.T, name string) (*Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: strings.ToLower(strings.ReplaceAll(name, " ", "-")) + "-admin",
		Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = admin
	return svc, ctx
}

func pdfImageAnswer(t *testing.T, svc *Service, ctx context.Context, name, content, contentHash string) (*AnswerDeliveryResult, error) {
	t.Helper()
	tenant, err := svc.CreateTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: strings.ToLower(strings.ReplaceAll(name, " ", "-")) + "-admin",
		Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: name + "-session", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: name, RequestID: "request-" + name,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: content,
		Citations: []model.AnswerCitation{{
			ID: "citation-1", DatasetID: "dataset-1", DocumentID: "document-1", ChunkID: "chunk-1",
			CitedContentExcerpt: "Contract chart", CitationLocator: "chart.md#L1",
			CitationContentHash: contentHash,
		}},
	})
}

func pdfImageRender(t *testing.T, svc *Service, ctx context.Context, answer *AnswerDeliveryResult) []byte {
	t.Helper()
	pdfFont := filepath.Join(`C:\Windows\Fonts`, "Deng.ttf")
	if _, err := os.Stat(pdfFont); err != nil {
		t.Skip("valid CJK PDF font is unavailable")
	}
	t.Setenv("RGX_EXPORT_PDF_FONT", pdfFont)
	job := &model.ExportJob{
		TenantID: answer.Snapshot.TenantID, Format: model.ExportFormatPDF,
		TemplateVersion: 1, PolicyVersion: "test-policy",
	}
	pdfBytes, err := svc.renderPDF(ctx, answer.Snapshot, answer.Projection, job, "export-image-snapshot")
	if err != nil {
		t.Fatalf("PDF image export: %v", err)
	}
	return pdfBytes
}

func TestPDFRendererEmbedsAuthorizedChunkImage(t *testing.T) {
	svc, ctx := pdfImageTestContext(t, "PDF Image Tenant")
	chunk := &ragflow.Chunk{ID: "chunk-1", DocumentID: "document-1", Content: "Contract chart", ImageID: "image-1"}
	svc.RAGFlow = &exportImageClient{Mock: *ragflow.NewMock(), image: pdfImageTestPNG(t, true), chunk: chunk}
	content := `![Contract chart](/api/v1/chat/image?dataset=dataset-1&doc=document-1&chunk=chunk-1&image=image-1)`
	answer, err := pdfImageAnswer(t, svc, ctx, "PDF Image User Tenant", content, sha256Hex("Contract chart"))
	if err != nil {
		t.Fatal(err)
	}
	pdfBytes := pdfImageRender(t, svc, ctx, answer)
	if !strings.Contains(string(pdfBytes), "/Subtype /Image") {
		t.Fatal("PDF artifact did not embed an authorized image bitmap")
	}
}

func TestPDFRendererBlocksUnauthorizedImageSources(t *testing.T) {
	svc, ctx := pdfImageTestContext(t, "PDF Image Gate Tenant")
	chunk := &ragflow.Chunk{ID: "chunk-1", DocumentID: "document-1", Content: "Contract chart", ImageID: "image-1"}
	svc.RAGFlow = &exportImageClient{Mock: *ragflow.NewMock(), image: pdfImageTestPNG(t, false), chunk: chunk}
	content := "![external](https://example.com/chart.png)\n\n![forged](/api/v1/chat/image?dataset=other&doc=other&chunk=other&image=image-1)\n\n![fragment](/api/v1/chat/image?dataset=dataset-1&doc=document-1&chunk=chunk-1&image=image-1#fragment)"
	answer, err := pdfImageAnswer(t, svc, ctx, "PDF Image Gate User Tenant", content, sha256Hex("Contract chart"))
	if err != nil {
		t.Fatal(err)
	}
	pdfBytes := pdfImageRender(t, svc, ctx, answer)
	if strings.Contains(string(pdfBytes), "/Subtype /Image") {
		t.Fatal("PDF artifact must not embed an unauthorized image source")
	}
}

func TestPDFRendererBlocksChunkImageMismatch(t *testing.T) {
	svc, ctx := pdfImageTestContext(t, "PDF Image Mismatch Tenant")
	chunk := &ragflow.Chunk{ID: "chunk-1", DocumentID: "document-1", Content: "Contract chart", ImageID: "actual-image"}
	svc.RAGFlow = &exportImageClient{Mock: *ragflow.NewMock(), image: pdfImageTestPNG(t, true), chunk: chunk}
	content := `![Contract chart](/api/v1/chat/image?dataset=dataset-1&doc=document-1&chunk=chunk-1&image=forged-image)`
	answer, err := pdfImageAnswer(t, svc, ctx, "PDF Image Mismatch User Tenant", content, sha256Hex("Contract chart"))
	if err != nil {
		t.Fatal(err)
	}
	pdfBytes := pdfImageRender(t, svc, ctx, answer)
	if strings.Contains(string(pdfBytes), "/Subtype /Image") {
		t.Fatal("PDF artifact must not embed an image from another chunk")
	}
}

func TestPDFRendererBlocksStaleChunkContent(t *testing.T) {
	svc, ctx := pdfImageTestContext(t, "PDF Image Stale Tenant")
	chunk := &ragflow.Chunk{ID: "chunk-1", DocumentID: "document-1", Content: "Updated chart", ImageID: "image-1"}
	svc.RAGFlow = &exportImageClient{Mock: *ragflow.NewMock(), image: pdfImageTestPNG(t, true), chunk: chunk}
	content := `![Contract chart](/api/v1/chat/image?dataset=dataset-1&doc=document-1&chunk=chunk-1&image=image-1)`
	answer, err := pdfImageAnswer(t, svc, ctx, "PDF Image Stale User Tenant", content, sha256Hex("Contract chart"))
	if err != nil {
		t.Fatal(err)
	}
	pdfBytes := pdfImageRender(t, svc, ctx, answer)
	if strings.Contains(string(pdfBytes), "/Subtype /Image") {
		t.Fatal("PDF artifact must not embed an image after chunk content changes")
	}
}

package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

// writeEnvelope writes a RAGFlow-style response.
func writeEnvelope(t *testing.T, w http.ResponseWriter, data interface{}) {
	t.Helper()
	env := map[string]interface{}{"code": 0, "message": "", "data": data}
	if err := json.NewEncoder(w).Encode(env); err != nil {
		t.Fatal(err)
	}
}

// TestHTTPClient_Contract verifies that the HTTP provider sends the expected
// request shape (path + authorization) and parses the RAGFlow envelope.
// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientContract(t *testing.T) {
	var gotPath, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v1/datasets":
			writeEnvelope(t, w, map[string]interface{}{"id": "ds-1", "name": "kb-a"})
		case "/api/v1/datasets/ds-1/documents":
			writeEnvelope(t, w, []map[string]interface{}{{"id": "doc-1", "name": "a.pdf"}})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "ragflow-test-key", 5*time.Second, 4)
	ctx := context.Background()

	ds, err := client.CreateDataset(ctx, CreateDatasetRequest{Name: "kb-a"})
	if err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if ds.ID != "ds-1" || ds.Name != "kb-a" {
		t.Fatalf("unexpected dataset: %+v", ds)
	}
	if gotPath != "/api/v1/datasets" {
		t.Fatalf("unexpected dataset path: %s", gotPath)
	}
	if gotAuth != "ragflow-test-key" {
		t.Fatalf("unexpected auth header: %s", gotAuth)
	}

	doc, err := client.CreateDocument(ctx, "ds-1", &DocumentUpload{Name: "a.pdf", Content: []byte("hello")})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents" {
		t.Fatalf("unexpected document path: %s", gotPath)
	}
	if !strings.Contains(doc.Name, "a.pdf") {
		t.Fatalf("unexpected document: %+v", doc)
	}
}

func TestHTTPClientEscapesResourceIDs(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeEnvelope(t, w, map[string]interface{}{"docs": []interface{}{}})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	if _, err := client.ListDocuments(context.Background(), "id/with:slash?query"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/id%2Fwith:slash%3Fquery/documents" {
		t.Fatalf("resource id was not escaped: %s", gotPath)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientForwardsRequestID(t *testing.T) {
	var gotRequestID, gotResponseID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = r.Header.Get("X-Request-Id")
		gotResponseID = r.Header.Get("X-Request-Id")
		writeEnvelope(t, w, map[string]interface{}{"id": "ds-1", "name": "kb-a"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := requestid.NewContext(context.Background(), "request-123")
	if _, err := client.CreateDataset(ctx, CreateDatasetRequest{Name: "kb-a"}); err != nil {
		t.Fatal(err)
	}
	if gotRequestID != "request-123" || gotResponseID != "request-123" {
		t.Fatalf("request id: got %q/%q, want request-123", gotRequestID, gotResponseID)
	}
}

func TestHTTPClientEscapesDocumentMetadataResourceIDs(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeEnvelope(t, w, map[string]interface{}{})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	if err := client.UpdateDocumentMetadata(context.Background(), "id/with:slash?query", "doc#1", nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/id%2Fwith:slash%3Fquery/documents/doc%231/metadata/config" {
		t.Fatalf("metadata resource id was not escaped: %s", gotPath)
	}
}

// TestHTTPClient_Error propagates non-zero engine error codes.
// TestDocumentFlexibleProcessFields ensures RAGFlow documents with numeric
// process_begin_at/process_duration fields still unmarshal (some RAGFlow
// versions return numbers, others strings).
func TestDocumentFlexibleProcessFields(t *testing.T) {
	raw := `[{"id":"d1","name":"a.pdf","run":"3","chunk_count":5,"token_count":10,"process_begin_at":1720000000,"process_duration":1.5,"progress":1,"progress_msg":"ok"}]`
	var out []Document
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal docs: %v", err)
	}
	if len(out) != 1 || out[0].ID != "d1" || out[0].Name != "a.pdf" {
		t.Fatalf("unexpected docs: %+v", out)
	}
	if string(out[0].ProcessDuration) != "1.5" {
		t.Fatalf("process_duration: %q", out[0].ProcessDuration)
	}
}

// TestHTTPClient_DocumentOps verifies parse/stop/delete hit the current
// RAGFlow document endpoints with the expected request shape.
// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientDocumentOps(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		writeEnvelope(t, w, map[string]interface{}{})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "k", 2*time.Second, 2)
	ctx := context.Background()

	if err := client.ParseDocuments(ctx, "ds-1", []string{"doc-a"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/parse" || gotMethod != http.MethodPost {
		t.Fatalf("parse: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "doc-a") {
		t.Fatalf("parse body missing doc id: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), "document_ids") {
		t.Fatalf("parse body must be an object with document_ids: %s", gotBody)
	}

	if err := client.StopDocuments(ctx, "ds-1", []string{"doc-a"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/stop" || gotMethod != http.MethodPost {
		t.Fatalf("stop: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "document_ids") {
		t.Fatalf("stop body must be an object with document_ids: %s", gotBody)
	}

	if err := client.DeleteDocuments(ctx, "ds-1", []string{"doc-a"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents" || gotMethod != http.MethodDelete {
		t.Fatalf("delete: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "doc-a") {
		t.Fatalf("delete body missing doc id: %s", gotBody)
	}

	if err := client.SetDocumentsStatus(ctx, "ds-1", []string{"doc-a"}, true); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/batch-update-status" || gotMethod != http.MethodPost {
		t.Fatalf("set status: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "doc_ids") || !strings.Contains(string(gotBody), "\"status\":\"1\"") {
		t.Fatalf("set status body: %s", gotBody)
	}

	if err := client.UpdateDocumentMetadata(ctx, "ds-1", "doc-a", map[string]interface{}{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/doc-a/metadata/config" || gotMethod != http.MethodPut {
		t.Fatalf("metadata: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "metadata") {
		t.Fatalf("metadata body: %s", gotBody)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientChunkOps(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		writeEnvelope(t, w, map[string]interface{}{})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "k", 2*time.Second, 2)
	ctx := context.Background()

	if err := client.DeleteChunks(ctx, "ds-1", "doc-a", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/doc-a/chunks" || gotMethod != http.MethodDelete {
		t.Fatalf("delete chunks: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "chunk_ids") {
		t.Fatalf("delete chunks body: %s", gotBody)
	}

	if err := client.SetChunksAvailable(ctx, "ds-1", "doc-a", []string{"c1"}, false); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/datasets/ds-1/documents/doc-a/chunks" || gotMethod != http.MethodPatch {
		t.Fatalf("patch chunks: path=%s method=%s", gotPath, gotMethod)
	}
	if !strings.Contains(string(gotBody), "available_int") {
		t.Fatalf("patch chunks body: %s", gotBody)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 102, "message": "failed"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "k", 2*time.Second, 2)
	if _, err := client.CreateDataset(context.Background(), CreateDatasetRequest{Name: "x"}); err == nil {
		t.Fatal("expected error for non-zero ragflow code")
	}
}

func TestHTTPClient_HealthParsesBareJSON(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok", "db": "ok", "redis": "ok", "doc_engine": "ok",
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "k", 2*time.Second, 2)
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/system/healthz" || gotMethod != http.MethodGet {
		t.Fatalf("health: path=%s method=%s", gotPath, gotMethod)
	}
	if health.Status != "ok" || health.DB != "ok" || health.Redis != "ok" || health.DocEngine != "ok" {
		t.Fatalf("unexpected health: %+v", health)
	}
}

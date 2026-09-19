package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RawDownloadsKeepWireContractAndStructuredErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v1/documents/document%2Fone/preview":
			if r.Method != http.MethodGet {
				t.Fatalf("document method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4\n"))
		case "/api/v1/documents/images/image%3Fid":
			if r.Method != http.MethodGet {
				t.Fatalf("image method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()

	content, contentType, err := client.GetDocumentContent(ctx, "document/one")
	if err != nil || string(content) != "%PDF-1.4\n" || contentType != "application/pdf" {
		t.Fatalf("document content: %q %q err=%v", content, contentType, err)
	}
	content, contentType, err = client.GetChunkImage(ctx, "image?id")
	if err != nil || string(content) != string([]byte{0x89, 'P', 'N', 'G'}) || contentType != "image/png" {
		t.Fatalf("chunk image: %v %q err=%v", content, contentType, err)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RawDownloadHTTPErrorIsStructured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":0,"message":"document is missing","data":null}`))
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	_, _, err := client.GetDocumentContent(context.Background(), "missing")
	var providerErr *Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if providerErr.HTTPStatus != http.StatusNotFound || providerErr.Type != ErrorTypeBusiness || providerErr.Message != "document is missing" {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RawDownloadClassifiesContextLifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", time.Second, 1)

	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelTimeout()
	_, _, err := client.GetDocumentContent(timeoutCtx, "slow")
	var timeoutErr *Error
	if !errors.As(err, &timeoutErr) || timeoutErr.Type != ErrorTypeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected structured timeout, got %T: %v", err, err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = client.GetDocumentContent(cancelCtx, "slow")
	var cancelledErr *Error
	if !errors.As(err, &cancelledErr) || cancelledErr.Type != ErrorTypeCancelled || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected structured cancellation, got %T: %v", err, err)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_429PreservesRetryAfterOnStructuredError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":0,"message":"rate limited","data":null}`))
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	_, err := client.CreateDataset(context.Background(), CreateDatasetRequest{Name: "rate"})
	var providerErr *Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if providerErr.HTTPStatus != http.StatusTooManyRequests || providerErr.Type != ErrorTypeTransport || providerErr.Message != "rate limited" {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
	if providerErr.RetryAfterMs != 2000 {
		t.Fatalf("RetryAfterMs = %d, want 2000", providerErr.RetryAfterMs)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UploadsUseMultipartContract(t *testing.T) {
	type received struct {
		method      string
		path        string
		contentType string
		fileName    string
		content     string
	}
	var uploads []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read file: %v", err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read upload: %v", err)
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		uploads = append(uploads, received{
			method: r.Method, path: r.URL.EscapedPath(), contentType: r.Header.Get("Content-Type"),
			fileName: header.Filename, content: string(body),
		})
		writeTestEnvelope(w, map[string]interface{}{
			"id": "upload-1", "name": header.Filename, "size": len(body), "extension": "txt",
			"mime_type": r.Header.Get("Content-Type"), "created_by": "tenant-1",
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()
	chatFile, err := client.UploadChatFile(ctx, "notes.txt", []byte("chat attachment"))
	if err != nil {
		t.Fatal(err)
	}
	agentFile, err := client.UploadAgentFile(ctx, "agent/1", "notes.txt", []byte("agent attachment"))
	if err != nil {
		t.Fatal(err)
	}

	if len(uploads) != 2 {
		t.Fatalf("upload count = %d, want 2", len(uploads))
	}
	if uploads[0].method != http.MethodPost || uploads[0].path != "/api/v1/documents/upload" || uploads[0].fileName != "notes.txt" || uploads[0].content != "chat attachment" {
		t.Fatalf("chat upload: %+v", uploads[0])
	}
	if uploads[1].method != http.MethodPost || uploads[1].path != "/api/v1/agents/agent%2F1/upload" || uploads[1].fileName != "notes.txt" || uploads[1].content != "agent attachment" {
		t.Fatalf("agent upload: %+v", uploads[1])
	}
	for _, upload := range uploads {
		if !strings.HasPrefix(upload.contentType, "multipart/form-data;") {
			t.Fatalf("content type = %q, want multipart", upload.contentType)
		}
	}
	if chatFile.ID != "upload-1" || chatFile.Name != "notes.txt" || chatFile.Size != int64(len("chat attachment")) || chatFile.CreatedBy != "tenant-1" {
		t.Fatalf("unexpected chat file: %+v", chatFile)
	}
	if agentFile.ID != "upload-1" || agentFile.Name != "notes.txt" || agentFile.Size != int64(len("agent attachment")) {
		t.Fatalf("unexpected agent file: %+v", agentFile)
	}
}

func TestRetryAfterMillisecondsSupportsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if got := retryAfterMilliseconds("2", now); got != 2000 {
		t.Fatalf("seconds = %d, want 2000", got)
	}
	if got := retryAfterMilliseconds(now.Add(3*time.Second).Format(http.TimeFormat), now); got != 3000 {
		t.Fatalf("http date = %d, want 3000", got)
	}
	if got := retryAfterMilliseconds("", now); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
}

func TestUploadedFileJSONUsesRAGFlowFields(t *testing.T) {
	data, err := json.Marshal(UploadedFile{ID: "u1", Name: "file.txt", Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"mime_type"`)) {
		t.Fatalf("uploaded file contract changed: %s", data)
	}
}

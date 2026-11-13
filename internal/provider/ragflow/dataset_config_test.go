package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestUpdateDatasetConfigTranslatesChunkMethod is the live-drill regression:
// RAGFlow v1.0.0 ignores a `chunk_method` body field (it answers 102 "no
// properties were modified"), so a repair would silently do nothing. The wire
// contract is the discriminated {"parse_type":1,"parser_id":...} form.
func TestUpdateDatasetConfigTranslatesChunkMethod(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/datasets/ds-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, map[string]interface{}{})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 0)

	method := "paper"
	if err := client.UpdateDatasetConfig(context.Background(), "ds-1", DatasetConfigUpdate{ChunkMethod: &method}); err != nil {
		t.Fatal(err)
	}
	if gotBody["parser_id"] != "paper" {
		t.Fatalf("parser_id not sent: %+v", gotBody)
	}
	if gotBody["parse_type"] != float64(ParseTypeBuiltIn) {
		t.Fatalf("parse_type not sent as the builtin discriminator: %+v", gotBody)
	}
	if _, ok := gotBody["chunk_method"]; ok {
		t.Fatalf("chunk_method must not reach the wire: %+v", gotBody)
	}
}

// TestUpdateDatasetConfigTreatsNoopAsSuccess pins the semantics of upstream code
// 102 "no properties were modified": the caller asked for a state the dataset is
// already in, which is a success, not a 502.
func TestUpdateDatasetConfigTreatsNoopAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 102, "message": "no properties were modified", "data": nil,
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 0)

	method := "naive"
	if err := client.UpdateDatasetConfig(context.Background(), "ds-1", DatasetConfigUpdate{ChunkMethod: &method}); err != nil {
		t.Fatalf("idempotent update must succeed, got %v", err)
	}
}

// TestUpdateDatasetConfigKeepsRealBusinessErrors makes sure the no-op carve-out
// is narrow: code 102 also carries genuine validation failures.
func TestUpdateDatasetConfigKeepsRealBusinessErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 102, "message": "parse_type is required", "data": nil,
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 0)

	method := "paper"
	if err := client.UpdateDatasetConfig(context.Background(), "ds-1", DatasetConfigUpdate{ChunkMethod: &method}); err == nil {
		t.Fatal("a validation failure must still be an error")
	}
}

// TestGetDatasetConfigNormalisesParserID covers the read side: v1.0.0 answers
// `parser_id` and never `chunk_method`, which used to surface as an empty parse
// profile.
func TestGetDatasetConfigNormalisesParserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]interface{}{
			"id": "ds-1", "name": "kb", "parser_id": "general",
			"parser_config": map[string]interface{}{"chunk_token_num": float64(512)},
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 0)

	cfg, err := client.GetDatasetConfig(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChunkMethod != "general" {
		t.Fatalf("chunk_method=%q, want the parser_id value general", cfg.ChunkMethod)
	}
	if cfg.ParseMethod() != "general" {
		t.Fatalf("ParseMethod()=%q, want general", cfg.ParseMethod())
	}
}

package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestHTTPClient_ListDatasetArtifactsContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v1/datasets/ds%2F1/artifacts" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		query := r.URL.Query()
		if query.Get("page") != "2" || query.Get("page_size") != "20" ||
			query.Get("page_type") != "entity" || query.Get("topic") != "architecture" ||
			query.Get("keywords") != "router" {
			t.Fatalf("unexpected artifact query: %s", r.URL.RawQuery)
		}
		writeEnvelope(t, w, map[string]interface{}{
			"items": []map[string]interface{}{
				{"slug": "entity/router", "title": "Router", "page_type": "entity"},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	artifacts, total, err := client.ListDatasetArtifacts(context.Background(), "ds/1", DatasetArtifactFilter{
		Page: 2, PageSize: 20, PageType: "entity", Topic: "architecture", Keywords: "router",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []DatasetArtifact{{Slug: "entity/router", Title: "Router", PageType: "entity"}}
	if total != 1 || !reflect.DeepEqual(artifacts, want) {
		t.Fatalf("artifacts=%+v total=%d", artifacts, total)
	}
}

func TestHTTPClient_GetDatasetArtifactContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.EscapedPath() != "/api/v1/datasets/ds%2F1/artifacts/page%2Findex/topic%2Fone" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		writeEnvelope(t, w, map[string]interface{}{
			"slug":                "page_index/topic-one",
			"title":               "Topic One",
			"page_type":           "page_index",
			"topic":               "architecture",
			"content_md_rendered": "# Topic One",
			"summary":             "Compiled page",
			"source_chunk_ids":    []string{"chunk-1"},
			"source_doc_ids":      []string{"doc-1"},
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	detail, err := client.GetDatasetArtifact(context.Background(), "ds/1", "page/index", "topic/one")
	if err != nil {
		t.Fatal(err)
	}
	want := &DatasetArtifactDetail{
		Slug: "page_index/topic-one", Title: "Topic One", PageType: "page_index",
		Topic: "architecture", ContentMDRendered: "# Topic One", Summary: "Compiled page",
		SourceChunkIDs: []string{"chunk-1"}, SourceDocIDs: []string{"doc-1"},
	}
	if !reflect.DeepEqual(detail, want) {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestHTTPClient_GetDatasetArtifactMissingRowContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":404,"message":"page not found","data":null}`))
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	detail, err := client.GetDatasetArtifact(context.Background(), "dataset-1", "page_index", "missing")
	if err != nil || detail != nil {
		t.Fatalf("business 404 for missing row must be nil: detail=%+v err=%v", detail, err)
	}
}

func TestDatasetArtifactValidation(t *testing.T) {
	client := NewHTTPClient("http://127.0.0.1:1", "key", time.Second, 1)
	ctx := context.Background()
	if _, _, err := client.ListDatasetArtifacts(ctx, " ", DatasetArtifactFilter{Page: 1, PageSize: 10}); err == nil {
		t.Fatal("empty dataset id must fail")
	}
	if _, _, err := client.ListDatasetArtifacts(ctx, "dataset-1", DatasetArtifactFilter{Page: 0, PageSize: 10}); err == nil {
		t.Fatal("invalid page must fail")
	}
	if _, _, err := client.ListDatasetArtifacts(ctx, "dataset-1", DatasetArtifactFilter{Page: 1, PageSize: 101}); err == nil {
		t.Fatal("oversized page must fail")
	}
	if _, err := client.GetDatasetArtifact(ctx, " ", "page_index", "topic"); err == nil {
		t.Fatal("empty dataset id must fail")
	}
	if _, err := client.GetDatasetArtifact(ctx, "dataset-1", " ", "topic"); err == nil {
		t.Fatal("empty page type must fail")
	}
	if _, err := client.GetDatasetArtifact(ctx, "dataset-1", "page_index", " "); err == nil {
		t.Fatal("empty slug must fail")
	}
}

func TestMock_ListDatasetArtifactsContract(t *testing.T) {
	mock := NewMock()
	artifact := DatasetArtifact{Slug: "entity/router", Title: "Router", PageType: "entity"}
	mock.SetDatasetArtifacts("dataset-1", []DatasetArtifact{artifact})
	filter := DatasetArtifactFilter{Page: 1, PageSize: 10, PageType: "entity", Topic: "architecture", Keywords: "router"}
	artifacts, total, err := mock.ListDatasetArtifacts(context.Background(), "dataset-1", filter)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || !reflect.DeepEqual(artifacts, []DatasetArtifact{artifact}) {
		t.Fatalf("artifacts=%+v total=%d", artifacts, total)
	}
	if captured := mock.LastDatasetArtifactFilter(); captured == nil || *captured != filter {
		t.Fatalf("captured=%+v", mock.LastDatasetArtifactFilter())
	}
}

func TestMock_GetDatasetArtifactContract(t *testing.T) {
	mock := NewMock()
	detail := DatasetArtifactDetail{
		Slug: "page_index/manual", Title: "Manual", PageType: "page_index",
		ContentMDRendered: "# Manual", SourceDocIDs: []string{"doc-1"},
	}
	mock.SetDatasetArtifactDetail("dataset-1", detail)
	got, err := mock.GetDatasetArtifact(context.Background(), "dataset-1", "page_index", "page_index/manual")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ContentMDRendered != "# Manual" || !reflect.DeepEqual(got.SourceDocIDs, []string{"doc-1"}) {
		t.Fatalf("detail=%+v", got)
	}
	missing, err := mock.GetDatasetArtifact(context.Background(), "dataset-1", "page_index", "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing detail must be nil: %+v err=%v", missing, err)
	}
}

func TestLiveGetDatasetArtifactContract(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	apiKey := os.Getenv("RGX_RAGFLOW_LIVE_API_KEY")
	if baseURL == "" || apiKey == "" {
		t.Skip("RAGFlow live environment is not configured")
	}
	client := NewHTTPClient(baseURL, apiKey, 15*time.Second, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	datasets, err := client.ListDatasets(ctx)
	if err != nil {
		t.Fatalf("list live datasets: %v", err)
	}
	if len(datasets) == 0 {
		t.Skip("live tenant has no datasets")
	}
	detail, err := client.GetDatasetArtifact(ctx, datasets[0].ID, "page_index", "rgx-live-contract-probe")
	if err != nil {
		t.Fatalf("artifact detail endpoint contract failed: %v", err)
	}
	t.Logf("live artifact detail endpoint contract passed: dataset=%s present=%t", datasets[0].ID, detail != nil)
}

package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
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

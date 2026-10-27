package ragflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_ListDocumentsFollowsPagination(t *testing.T) {
	type request struct {
		path string
		page string
		size string
	}
	var requests []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, request{
			path: r.URL.Path,
			page: r.URL.Query().Get("page"),
			size: r.URL.Query().Get("page_size"),
		})
		switch page := r.URL.Query().Get("page"); page {
		case "1":
			writeTestEnvelope(w, map[string]interface{}{
				"docs": []map[string]interface{}{
					{"id": "doc-1", "name": "one"},
					{"id": "doc-2", "name": "two"},
				},
				"total": 3,
			})
		case "2":
			writeTestEnvelope(w, map[string]interface{}{
				"docs": []map[string]interface{}{
					{"id": "doc-3", "name": "three"},
				},
				"total": 3,
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected page %s", page), http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	docs, err := client.ListDocuments(context.Background(), "dataset-1")
	if err != nil {
		t.Fatal(err)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2: %+v", len(requests), requests)
	}
	if requests[0].path != "/api/v1/datasets/dataset-1/documents" || requests[0].page != "1" || requests[0].size != "100" {
		t.Fatalf("first page request: %+v", requests[0])
	}
	if requests[1].page != "2" || requests[1].size != "100" {
		t.Fatalf("second page request: %+v", requests[1])
	}
	if len(docs) != 3 || docs[0].ID != "doc-1" || docs[1].ID != "doc-2" || docs[2].ID != "doc-3" {
		t.Fatalf("unexpected documents: %+v", docs)
	}
}

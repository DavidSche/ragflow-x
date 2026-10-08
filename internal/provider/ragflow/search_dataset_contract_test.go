package ragflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestHTTPClient_SearchDatasetContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v1/datasets/ds%2F1/search" {
			http.Error(w, "unexpected endpoint "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"question":"What changed?","doc_ids":["doc-1"],"page":2,"size":20,"top_k":8,` +
			`"similarity_threshold":0.2,"vector_similarity_weight":0.5,"use_kg":true,` +
			`"cross_languages":["en"],"keyword":true,` +
			`"meta_data_filter":{"method":"manual"},"include_knowledge_compilation":true}`
		if string(body) != want {
			t.Fatalf("unexpected search body:\n got %s\nwant %s", body, want)
		}
		writeEnvelope(t, w, map[string]interface{}{
			"chunks": []map[string]interface{}{
				{"id": "chunk-1", "content": "scoped hit", "dataset_id": "ds/1"},
			},
			"total":  1,
			"labels": []string{"internal"},
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	result, err := client.SearchDataset(context.Background(), "ds/1", SearchDatasetRequest{
		Question:                    "What changed?",
		DocumentIDs:                 []string{"doc-1"},
		Page:                        2,
		Size:                        20,
		TopK:                        8,
		SimilarityThreshold:         floatPtr(0.2),
		VectorSimilarityWeight:      floatPtr(0.5),
		UseKG:                       true,
		CrossLanguages:              []string{"en"},
		Keyword:                     true,
		MetadataFilter:              map[string]interface{}{"method": "manual"},
		IncludeKnowledgeCompilation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Chunks) != 1 || result.Chunks[0]["id"] != "chunk-1" ||
		len(result.Labels) != 1 || result.Labels[0] != "internal" {
		t.Fatalf("unexpected search result: %+v", result)
	}
}

func TestSearchDatasetRejectsInvalidInput(t *testing.T) {
	client := NewMock()
	valid := SearchDatasetRequest{Question: "What changed?"}
	similarity := 1.5
	weight := 1.5
	cases := []SearchDatasetRequest{
		{Question: " "},
		{Question: "What changed?", Page: -1},
		{Question: "What changed?", Size: -1},
		{Question: "What changed?", TopK: -1},
		{Question: "What changed?", DocumentIDs: []string{" "}},
		{Question: "What changed?", CrossLanguages: []string{" "}},
		{Question: "What changed?", SimilarityThreshold: &similarity},
		{Question: "What changed?", VectorSimilarityWeight: &weight},
	}
	for _, request := range cases {
		if _, err := client.SearchDataset(context.Background(), "dataset-1", request); err == nil {
			t.Fatalf("invalid search request must fail: %+v", request)
		}
	}
	if _, err := client.SearchDataset(context.Background(), " ", valid); err == nil {
		t.Fatal("empty dataset_id must fail")
	}
}

func TestHTTPClient_SearchDatasetDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"question":"What changed?","include_knowledge_compilation":false}` {
			t.Fatalf("unexpected default search body: %s", body)
		}
		writeEnvelope(t, w, map[string]interface{}{"chunks": []interface{}{}})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	result, err := client.SearchDataset(context.Background(), "dataset-1", SearchDatasetRequest{
		Question: "What changed?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chunks) != 0 || result.Total != 0 || len(result.Labels) != 0 {
		t.Fatalf("unexpected empty search result: %+v", result)
	}
}

func TestMock_SearchDatasetContract(t *testing.T) {
	mock := NewMock()
	request := SearchDatasetRequest{
		Question:                    "What changed?",
		DocumentIDs:                 []string{"doc-1"},
		Page:                        1,
		Size:                        10,
		TopK:                        5,
		VectorSimilarityWeight:      floatPtr(0.4),
		IncludeKnowledgeCompilation: true,
	}
	result, err := mock.SearchDataset(context.Background(), "dataset-1", request)
	if err != nil {
		t.Fatal(err)
	}
	captured := mock.LastSearchDatasetRequest()
	if len(result.Chunks) != 1 || result.Total != 1 || result.Chunks[0]["dataset_id"] != "dataset-1" ||
		captured == nil || !reflect.DeepEqual(*captured, request) {
		t.Fatalf("unexpected mock search: result=%+v captured=%+v", result, captured)
	}
}

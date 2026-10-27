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

func TestHTTPClient_RetrieveDatasetsContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/retrieval" {
			http.Error(w, "unexpected endpoint "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"dataset_ids":["dataset-1","dataset-2"],"question":"What changed?","document_ids":["doc-1"],` +
			`"metadata_condition":{"logic":"and","conditions":[{"name":"rgx_logical_doc_id","comparison_operator":"is","value":"logical-1"}]},` +
			`"include_knowledge_compilation":true,"page":2,"page_size":20,"vector_similarity_weight":0.5,` +
			`"keyword":true,"rerank_id":"rerank-1"}`
		if string(body) != want {
			t.Fatalf("unexpected retrieval body:\n got %s\nwant %s", body, want)
		}
		writeEnvelope(t, w, map[string]interface{}{
			"chunks": []map[string]interface{}{
				{
					"id": "chunk-1", "content": "compiled hit", "dataset_id": "dataset-1",
					"document_id": "doc-1", "similarity": 0.91, "compile_kwd": "tree",
				},
			},
			"total": 1,
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	result, err := client.RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{
		DatasetIDs:  []string{"dataset-1", "dataset-2"},
		Question:    "What changed?",
		DocumentIDs: []string{"doc-1"},
		MetadataCondition: &MetadataCondition{
			Logic: "and",
			Conditions: []MetadataConditionOp{
				{Name: "rgx_logical_doc_id", ComparisonOperator: "is", Value: "logical-1"},
			},
		},
		IncludeKnowledgeCompilation: true, Page: 2, PageSize: 20,
		VectorSimilarityWeight: floatPtr(0.5),
		Keyword:                true,
		RerankID:               "rerank-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Chunks) != 1 || result.Chunks[0]["id"] != "chunk-1" ||
		result.Chunks[0]["compile_kwd"] != "tree" {
		t.Fatalf("unexpected retrieval result: %+v", result)
	}
}

func TestRetrieveDatasetsDefaultsOmitKeywordAndRerank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"dataset_ids":["dataset-1"],"question":"What changed?","include_knowledge_compilation":false}`
		if string(body) != want {
			t.Fatalf("unexpected default retrieval body:\n got %s\nwant %s", body, want)
		}
		writeEnvelope(t, w, map[string]interface{}{"chunks": []interface{}{}})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	result, err := client.RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{
		DatasetIDs: []string{"dataset-1"}, Question: "What changed?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chunks) != 0 || result.Total != 0 {
		t.Fatalf("unexpected empty retrieval result: %+v", result)
	}
}

func TestRetrieveDatasetsRejectsBlankRerankID(t *testing.T) {
	if _, err := NewMock().RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{
		DatasetIDs: []string{"dataset-1"}, Question: "What changed?", RerankID: " ",
	}); err == nil {
		t.Fatal("blank rerank_id must fail")
	}
}

func TestRetrieveDatasetsRejectsInvalidVectorSimilarityWeight(t *testing.T) {
	weight := 1.5
	_, err := NewMock().RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{
		DatasetIDs: []string{"dataset-1"}, Question: "What changed?",
		VectorSimilarityWeight: &weight,
	})
	if err == nil {
		t.Fatal("invalid vector_similarity_weight must fail")
	}
}

// Retrieval-test tuning knobs (221 §5.4 C2) must serialize onto the upstream
// payload with their upstream names and stay omitted when unset.
func TestRetrieveDatasetsSerializesRetrievalTestKnobs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		want := `{"dataset_ids":["dataset-1"],"question":"What changed?","include_knowledge_compilation":false,"page":1,"page_size":10,` +
			`"similarity_threshold":0.35,"top_k":64,"vector_similarity_weight":0.8,` +
			`"keyword":true,"rerank_id":"rerank-1","cross_languages":["zh","en"],"highlight":true}`
		if string(body) != want {
			t.Fatalf("unexpected retrieval-test body:\n got %s\nwant %s", body, want)
		}
		writeEnvelope(t, w, map[string]interface{}{"chunks": []interface{}{}, "total": 0})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	highlight := true
	_, err := client.RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{
		DatasetIDs: []string{"dataset-1"}, Question: "What changed?",
		Page: 1, PageSize: 10,
		SimilarityThreshold: floatPtr(0.35), TopK: 64, VectorSimilarityWeight: floatPtr(0.8),
		Keyword: true, RerankID: "rerank-1", CrossLanguages: []string{"zh", "en"},
		Highlight: &highlight,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetrieveDatasetsRejectsInvalidTuningKnobs(t *testing.T) {
	cases := []struct {
		name string
		req  RetrieveDatasetsRequest
	}{{
		name: "similarity_threshold above one",
		req:  RetrieveDatasetsRequest{DatasetIDs: []string{"d"}, Question: "q", SimilarityThreshold: floatPtr(1.2)},
	}, {
		name: "negative top_k",
		req:  RetrieveDatasetsRequest{DatasetIDs: []string{"d"}, Question: "q", TopK: -1},
	}, {
		name: "blank cross language",
		req:  RetrieveDatasetsRequest{DatasetIDs: []string{"d"}, Question: "q", CrossLanguages: []string{"zh", " "}},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewMock().RetrieveDatasets(context.Background(), tc.req); err == nil {
				t.Fatalf("%s must fail", tc.name)
			}
		})
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func TestMock_RetrieveDatasetsContract(t *testing.T) {
	mock := NewMock()
	condition := &MetadataCondition{
		Conditions: []MetadataConditionOp{{Name: "sensitivity", ComparisonOperator: "is", Value: "internal"}},
	}
	request := RetrieveDatasetsRequest{
		DatasetIDs: []string{"dataset-1"}, Question: "What changed?",
		DocumentIDs: []string{"doc-1"}, MetadataCondition: condition,
		// PageSize 1 keeps the single-hit mock shape; PageSize > 1 returns the
		// two-chunk retrieval-test fixture (C2).
		IncludeKnowledgeCompilation: true, Page: 1, PageSize: 1,
	}
	result, err := mock.RetrieveDatasets(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	captured := mock.LastRetrievalRequest()
	if len(result.Chunks) != 1 || result.Total != 1 || captured == nil ||
		!reflect.DeepEqual(*captured, request) {
		t.Fatalf("unexpected mock retrieval: result=%+v captured=%+v", result, mock.LastRetrievalRequest())
	}
	if _, err := mock.RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{Question: "q"}); err == nil {
		t.Fatal("retrieval without datasets must fail")
	}
	if _, err := mock.RetrieveDatasets(context.Background(), RetrieveDatasetsRequest{DatasetIDs: []string{"dataset-1"}}); err == nil {
		t.Fatal("retrieval without question must fail")
	}
}

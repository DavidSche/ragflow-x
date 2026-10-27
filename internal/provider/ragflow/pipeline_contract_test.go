package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientPipelineContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pipelines":
			if r.Method != http.MethodGet {
				t.Errorf("list method = %s, want GET", r.Method)
			}
			writeEnvelope(t, w, pipelineListResponse{Canvas: []PipelineTemplate{{
				ID: "general", Title: "General", Description: "General pipeline", Filename: "general.json",
			}}})
		case "/api/v1/pipelines/general":
			if r.Method != http.MethodGet {
				t.Errorf("get method = %s, want GET", r.Method)
			}
			writeEnvelope(t, w, PipelineTemplateDetail{DSL: map[string]interface{}{"graph": true}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewHTTPClient(server.URL, "key", 2*time.Second, 1)
	pipelines, err := client.ListPipelines(context.Background())
	if err != nil || len(pipelines) != 1 || pipelines[0].ID != "general" {
		t.Fatalf("list pipelines: pipelines=%+v err=%v", pipelines, err)
	}
	pipeline, err := client.GetPipeline(context.Background(), "general")
	if err != nil || pipeline == nil || pipeline.DSL["graph"] != true {
		t.Fatalf("get pipeline: %+v err=%v", pipeline, err)
	}
}

func TestHTTPClientUserPipelineFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pipelines/user-pipeline":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":102,"message":"pipeline not found"}`))
		case "/api/v1/agents/user-pipeline":
			if r.Method != http.MethodGet {
				t.Errorf("agent method = %s, want GET", r.Method)
			}
			writeEnvelope(t, w, map[string]interface{}{
				"id": "user-pipeline",
				"dsl": map[string]interface{}{
					"graph":      true,
					"components": map[string]interface{}{},
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewHTTPClient(server.URL, "key", 2*time.Second, 1)
	pipeline, err := client.GetPipeline(context.Background(), "user-pipeline")
	if err != nil || pipeline == nil || pipeline.DSL["graph"] != true {
		t.Fatalf("get user pipeline: %+v err=%v", pipeline, err)
	}
}

func TestMockPipelineRegistry(t *testing.T) {
	mock := NewMock()
	mock.SetPipelineTemplate(PipelineTemplate{ID: "general"}, map[string]interface{}{"graph": true})
	pipelines, err := mock.ListPipelines(context.Background())
	if err != nil || len(pipelines) != 1 || pipelines[0].ID != "general" {
		t.Fatalf("mock list pipeline: %+v err=%v", pipelines, err)
	}
	pipeline, err := mock.GetPipeline(context.Background(), "general")
	if err != nil || pipeline == nil || pipeline.DSL["graph"] != true {
		t.Fatalf("mock get pipeline: %+v err=%v", pipeline, err)
	}
	if _, err := mock.GetPipeline(context.Background(), "missing"); err == nil {
		t.Fatal("missing mock pipeline must fail")
	}
}

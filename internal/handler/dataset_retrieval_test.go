package handler

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestTestDatasetRetrievalRunsAndReturnsChunks(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, _ := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/retrieval-test",
		map[string]any{"question": "what is the policy?", "page_size": 5},
		token,
	)
	if res.Code != http.StatusOK {
		t.Fatalf("retrieval-test status = %d, body = %s", res.Code, res.Body.String())
	}
	body := jsonResponse(t, res)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("retrieval-test data missing: %v", body)
	}
	chunks, ok := data["chunks"].([]any)
	if !ok || len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got: %v", data["chunks"])
	}
	if data["question"] != "what is the policy?" {
		t.Fatalf("question not echoed: %v", data["question"])
	}
	// Every interactive test run must land in the knowledge-ops event log.
	events, _, err := env.svc.ListKnowledgeOpsEvents(t.Context(), env.tenantID, false, 1, 10,
		repository.KnowledgeOpsFilter{AppType: "retrieval_test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].CitationsCount != 2 {
		t.Fatalf("knowledge-ops event not recorded: %d events", len(events))
	}
}

func TestTestDatasetRetrievalValidatesAndGuards(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, _ := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	// Blank question -> 400.
	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/retrieval-test",
		map[string]any{"question": "  "}, token)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("blank question status = %d, want 400", res.Code)
	}

	// page_size over 30 -> 400.
	res = env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/retrieval-test",
		map[string]any{"question": "q", "page_size": 100}, token)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("page_size=100 status = %d, want 400", res.Code)
	}

	// Unknown dataset -> 404.
	res = env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/does-not-exist/retrieval-test",
		map[string]any{"question": "q"}, token)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing dataset status = %d, want 404", res.Code)
	}

	// Viewer holds the builtin read:dataset grant (migrate.go seed), so the
	// read-path retrieval test is available to workspace readers as well.
	viewerToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleViewer)
	res = env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/retrieval-test",
		map[string]any{"question": "q"}, viewerToken)
	if res.Code != http.StatusOK {
		t.Fatalf("viewer status = %d, want 200 (viewer has read:dataset)", res.Code)
	}
}

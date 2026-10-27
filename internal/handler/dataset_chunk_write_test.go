package handler

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func seedChunkWriteDocument(t *testing.T, env *testEnv) (string, string) {
	t.Helper()
	dataset, err := env.svc.CreateDataset(t.Context(), env.tenantID, "chunk writes")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := env.svc.UploadDocument(t.Context(), env.tenantID, dataset.ID, env.adminID, "chunks.md", []byte("chunk editing content"))
	if err != nil {
		t.Fatal(err)
	}
	return dataset.ID, doc.ID
}

func chunkWriteToken(t *testing.T, env *testEnv) string {
	t.Helper()
	return env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
}

func TestAddDocumentChunkCreatesChunkAndAudits(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)
	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"content": "manually added chunk", "important_keywords": []string{"manual"}, "questions": []string{"who added this?"}},
		token,
	)
	if res.Code != http.StatusOK {
		t.Fatalf("add chunk status = %d, body = %s", res.Code, res.Body.String())
	}
	body := jsonResponse(t, res)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("add chunk data missing: %v", body)
	}
	chunk, ok := data["chunk"].(map[string]any)
	if !ok {
		t.Fatalf("add chunk response chunk missing: %v", data)
	}
	if chunk["id"].(string) == "" {
		t.Fatalf("add chunk response id empty: %v", chunk)
	}
	if chunk["content"] != "manually added chunk" {
		t.Fatalf("add chunk content mismatch: %v", chunk["content"])
	}
	chunks, _, err := env.svc.ListDocumentChunks(t.Context(), env.tenantID, datasetID, docID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Content != "manually added chunk" {
		t.Fatalf("chunk not visible after add: %+v", chunks)
	}
	if len(chunks[0].Keywords) != 1 || chunks[0].Keywords[0] != "manual" {
		t.Fatalf("chunk keywords mismatch: %+v", chunks[0].Keywords)
	}
}

func TestAddDocumentChunkRequiresContent(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)
	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"important_keywords": []string{"x"}},
		token,
	)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("add chunk without content status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestAddDocumentChunkRejectsBlankContent(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)
	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"content": "   "},
		token,
	)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("add chunk with blank content status = %d", res.Code)
	}
}

func TestUpdateDocumentChunkRewritesFields(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	addRes := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"content": "original content"},
		token,
	)
	if addRes.Code != http.StatusOK {
		t.Fatalf("seed add chunk status = %d, body = %s", addRes.Code, addRes.Body.String())
	}
	addBody := jsonResponse(t, addRes)
	addData := addBody["data"].(map[string]any)
	chunkID := addData["chunk"].(map[string]any)["id"].(string)

	updated := "rewritten by editor"
	res := env.doRequest(t, http.MethodPut,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks/"+chunkID,
		map[string]any{"content": updated, "important_keywords": []string{"rewritten"}},
		token,
	)
	if res.Code != http.StatusOK {
		t.Fatalf("update chunk status = %d, body = %s", res.Code, res.Body.String())
	}

	chunks, _, err := env.svc.ListDocumentChunks(t.Context(), env.tenantID, datasetID, docID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunk count after update = %d", len(chunks))
	}
	if chunks[0].Content != updated {
		t.Fatalf("chunk content not updated: %q", chunks[0].Content)
	}
	if len(chunks[0].Keywords) != 1 || chunks[0].Keywords[0] != "rewritten" {
		t.Fatalf("chunk keywords not updated: %+v", chunks[0].Keywords)
	}
}

func TestUpdateDocumentChunkKeywordsOnly(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	addRes := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"content": "body"},
		token,
	)
	if addRes.Code != http.StatusOK {
		t.Fatalf("seed add chunk status = %d", addRes.Code)
	}
	chunkID := jsonResponse(t, addRes)["data"].(map[string]any)["chunk"].(map[string]any)["id"].(string)

	res := env.doRequest(t, http.MethodPut,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks/"+chunkID,
		map[string]any{"important_keywords": []string{"only-keywords"}},
		token,
	)
	if res.Code != http.StatusOK {
		t.Fatalf("keywords-only update status = %d, body = %s", res.Code, res.Body.String())
	}
	chunks, _, err := env.svc.ListDocumentChunks(t.Context(), env.tenantID, datasetID, docID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Content != "body" {
		t.Fatalf("content unexpectedly changed: %+v", chunks)
	}
	if len(chunks[0].Keywords) != 1 || chunks[0].Keywords[0] != "only-keywords" {
		t.Fatalf("keywords not updated: %+v", chunks[0].Keywords)
	}
}

func TestUpdateDocumentChunkNotFoundPassesThrough(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	res := env.doRequest(t, http.MethodPut,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks/chunk-missing",
		map[string]any{"content": "x"},
		token,
	)
	if res.Code == http.StatusOK {
		t.Fatalf("update missing chunk unexpectedly succeeded")
	}
	if res.Code < 400 || res.Code >= 500 {
		t.Fatalf("missing chunk should surface upstream client error, got %d", res.Code)
	}
}

func TestAddDocumentChunkTrimsContent(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	res := env.doRequest(t, http.MethodPost,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks",
		map[string]any{"content": "  padded body  "},
		token,
	)
	if res.Code != http.StatusOK {
		t.Fatalf("add chunk status = %d, body = %s", res.Code, res.Body.String())
	}
	chunks, _, err := env.svc.ListDocumentChunks(t.Context(), env.tenantID, datasetID, docID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Content != "padded body" {
		t.Fatalf("content not trimmed: %+v", chunks)
	}
}

func TestUpdateDocumentChunkRejectsEmptyPayload(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, docID := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)
	res := env.doRequest(t, http.MethodPut,
		"/api/v1/datasets/"+datasetID+"/documents/"+docID+"/chunks/"+"chunk-1",
		map[string]any{},
		token,
	)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("update chunk with empty payload status = %d", res.Code)
	}
}

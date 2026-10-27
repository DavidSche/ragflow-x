package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetDatasetGraphReturnsReadOnlyGraph(t *testing.T) {
	env := setupTestEnv(t)
	datasetID, _ := seedChunkWriteDocument(t, env)
	token := chunkWriteToken(t, env)

	res := env.doRequest(t, http.MethodGet, "/api/v1/datasets/"+datasetID+"/graph", nil, token)
	if res.Code != http.StatusOK {
		t.Fatalf("graph status = %d, body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Data struct {
			Nodes []map[string]any `json:"nodes"`
			Edges []map[string]any `json:"edges"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Nodes) != 2 || len(body.Data.Edges) != 1 {
		t.Fatalf("mock graph shape mismatch: %d nodes, %d edges", len(body.Data.Nodes), len(body.Data.Edges))
	}
	// Unknown dataset resolves 404 at the RGX boundary before the engine is
	// consulted (GetDatasetForScope fail-fast), never an empty graph.
	res = env.doRequest(t, http.MethodGet, "/api/v1/datasets/missing/graph", nil, token)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing dataset graph status = %d, want 404", res.Code)
	}
}

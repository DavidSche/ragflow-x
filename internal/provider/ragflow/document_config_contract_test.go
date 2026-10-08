package ragflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientDocumentParseConfigContract(t *testing.T) {
	var gotMethod, gotPath string
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeEnvelope(t, w, map[string]interface{}{})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "key", 2*time.Second, 1)
	err := client.UpdateDocumentParseConfig(context.Background(), "ds/a", "doc/a", DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: "table", ParserConfig: map[string]interface{}{"pdf:ocr": true},
		MetaFields: map[string]interface{}{"rgx_parser_policy_id": "policy-1"},
	})
	if err != nil {
		t.Fatalf("update document parse config: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/datasets/ds%2Fa/documents/doc%2Fa" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if gotPayload["parse_type"] != float64(2) || gotPayload["pipeline_id"] != "table" ||
		gotPayload["parser_config"].(map[string]interface{})["pdf:ocr"] != true ||
		gotPayload["meta_fields"].(map[string]interface{})["rgx_parser_policy_id"] != "policy-1" {
		t.Fatalf("unexpected payload: %+v", gotPayload)
	}
}

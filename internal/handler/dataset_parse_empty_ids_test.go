package handler

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `POST /datasets/:id/parse` with an empty `document_ids` used to be forwarded
// to the engine, which answers `code:102 document_ids is required`; the service
// maps any engine error to `502/50203 ragflow parse documents failed`. A caller
// mistake therefore looked like an engine outage — the error a user sees when
// they click "parse" on a selection the UI already considers empty. The sibling
// stop/status/delete handlers already reject the empty list.
func TestParseEndpointRejectsEmptyDocumentIDs(t *testing.T) {
	env, datasetID, stub := setupParseProfileEndpoint(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)

	res := env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+datasetID+"/parse",
		map[string]any{"document_ids": []string{}}, token)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", res.Code, res.Body.String())
	}
	body := jsonResponse(t, res)
	if code, ok := body["code"].(float64); !ok || int(code) != 40000 {
		t.Fatalf("error code = %v, want 40000 (body %s)", body["code"], res.Body.String())
	}
	if stub.parseCalls != 0 {
		t.Fatalf("a rejected request must not reach the engine (parseCalls=%d)", stub.parseCalls)
	}
}

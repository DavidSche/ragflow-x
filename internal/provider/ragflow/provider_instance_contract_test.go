package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type capturedRequest struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

func decodeCapturedJSON(t *testing.T, request *http.Request) capturedRequest {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	captured := capturedRequest{Method: request.Method, Path: request.URL.Path, Body: map[string]interface{}{}}
	if len(body) != 0 {
		if err := json.Unmarshal(body, &captured.Body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
	}
	return captured
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPProviderInstanceWriteContract(t *testing.T) {
	var requests []capturedRequest
	ctx := context.Background()
	request := RegisterModelProviderRequest{
		FactoryName:  "OpenAI-API-Compatible",
		InstanceName: "production",
		APIKey:       "secret",
		BaseURL:      "https://api.example.com/v1",
		Region:       "intl",
		Models: []ModelInfo{{
			ModelType: []string{"chat"},
			ModelName: "gpt-4o",
			MaxTokens: 128000,
			Extra:     map[string]interface{}{"is_tools": true},
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, decodeCapturedJSON(t, r))
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/providers/OpenAI-API-Compatible/instances" {
			writeTestEnvelope(w, []map[string]interface{}{{
				"id":            "upstream-instance-1",
				"instance_name": request.InstanceName,
			}})
			return
		}
		writeTestEnvelope(w, map[string]interface{}{})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "provider-key", 2*time.Second, 1)

	created, err := client.CreateProviderInstance(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || created.ID != "upstream-instance-1" {
		t.Fatalf("created instance = %+v, want upstream-instance-1", created)
	}
	if err := client.UpdateProviderInstance(ctx, "OpenAI-API-Compatible", "production", request); err != nil {
		t.Fatal(err)
	}
	if err := client.AddModelToInstance(ctx, "OpenAI-API-Compatible", "production", request.Models[0]); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateModel(ctx, "OpenAI-API-Compatible", "production", "gpt-4o", ModelUpdate{
		Status:    "disabled",
		MaxTokens: 64000,
		ModelType: []string{"chat"},
		Extra:     map[string]interface{}{"thinking": true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteModelsFromInstance(ctx, "OpenAI-API-Compatible", "production", []string{"gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteProviderInstances(ctx, "OpenAI-API-Compatible", []string{created.ID}); err != nil {
		t.Fatal(err)
	}

	if len(requests) != 8 {
		t.Fatalf("request count = %d, want 8", len(requests))
	}
	assertCapturedRequest(t, requests[0], http.MethodPost, "/api/v1/providers/OpenAI-API-Compatible/instances", map[string]interface{}{
		"instance_name": request.InstanceName,
		"api_key":       request.APIKey,
		"base_url":      request.BaseURL,
		"region":        request.Region,
		"model_info": []interface{}{map[string]interface{}{
			"model_type": []interface{}{"chat"},
			"model_name": "gpt-4o",
			"max_tokens": float64(128000),
			"extra":      map[string]interface{}{"is_tools": true},
		}},
	})
	assertCapturedRequest(t, requests[1], http.MethodGet, "/api/v1/providers/OpenAI-API-Compatible/instances", map[string]interface{}{})
	assertCapturedRequest(t, requests[2], http.MethodPut, "/api/v1/providers/OpenAI-API-Compatible/instances/production", map[string]interface{}{
		"instance_name": request.InstanceName,
		"api_key":       request.APIKey,
		"base_url":      request.BaseURL,
		"region":        request.Region,
		"verify":        true,
		"model_info": []interface{}{map[string]interface{}{
			"model_type": []interface{}{"chat"},
			"model_name": "gpt-4o",
			"max_tokens": float64(128000),
			"extra":      map[string]interface{}{"is_tools": true},
		}},
	})
	assertCapturedRequest(t, requests[3], http.MethodPost, "/api/v1/providers/OpenAI-API-Compatible/instances/production/models", map[string]interface{}{
		"model_name": "gpt-4o",
		"model_type": []interface{}{"chat"},
		"max_tokens": float64(128000),
		"extra":      map[string]interface{}{"is_tools": true},
	})
	assertCapturedRequest(t, requests[4], http.MethodPatch, "/api/v1/providers/OpenAI-API-Compatible/instances/production/models/gpt-4o", map[string]interface{}{
		"status":     "disabled",
		"max_tokens": float64(64000),
		"model_type": []interface{}{"chat"},
		"extra":      map[string]interface{}{"thinking": true},
	})
	assertCapturedRequest(t, requests[5], http.MethodDelete, "/api/v1/providers/OpenAI-API-Compatible/instances/production/models", map[string]interface{}{
		"model_name": []interface{}{"gpt-4o"},
	})
	assertCapturedRequest(t, requests[7], http.MethodDelete, "/api/v1/providers/OpenAI-API-Compatible/instances", map[string]interface{}{
		"instances": []interface{}{"upstream-instance-1"},
	})
}

func assertCapturedRequest(t *testing.T, actual capturedRequest, method, path string, body map[string]interface{}) {
	t.Helper()
	if actual.Method != method || actual.Path != path {
		t.Fatalf("request = %s %s, want %s %s", actual.Method, actual.Path, method, path)
	}
	assertJSONValue(t, actual.Body, body)
}

func assertJSONValue(t *testing.T, actual, expected interface{}) {
	t.Helper()
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("JSON = %s, want %s", actualJSON, expectedJSON)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_MockProviderInstanceLifecycle(t *testing.T) {
	mock := NewMock()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	if err := mock.AddProvider(ctx, "OpenAI-API-Compatible"); err != nil {
		t.Fatal(err)
	}
	request := RegisterModelProviderRequest{
		FactoryName:  "OpenAI-API-Compatible",
		InstanceName: "production",
		APIKey:       "secret",
		BaseURL:      "https://api.example.com/v1",
		Region:       "intl",
	}
	created, err := mock.CreateProviderInstance(ctx, request)
	if err != nil {
		t.Fatalf("create provider instance: %v", err)
	}
	if created == nil || created.ID == "" || created.InstanceName != request.InstanceName {
		t.Fatalf("created mock instance = %+v", created)
	}
	instances, err := mock.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].InstanceName != "production" || instances[0].Region != "intl" || instances[0].APIKey != "secret" {
		t.Fatalf("unexpected instances: %+v", instances)
	}

	model := ModelInfo{ModelType: []string{"chat"}, ModelName: "gpt-4o", MaxTokens: 128000}
	if err := mock.AddModelToInstance(ctx, "OpenAI-API-Compatible", "production", model); err != nil {
		t.Fatalf("add model: %v", err)
	}
	models, err := mock.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "gpt-4o" || models[0].MaxTokens != 128000 || models[0].Status != "active" {
		t.Fatalf("unexpected models: %+v", models)
	}

	if err := mock.UpdateModel(ctx, "OpenAI-API-Compatible", "production", "gpt-4o", ModelUpdate{Status: "disabled", MaxTokens: 64000}); err != nil {
		t.Fatalf("update model: %v", err)
	}
	models, err = mock.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Status != "disabled" || models[0].MaxTokens != 64000 {
		t.Fatalf("updated models: %+v", models)
	}
	if err := mock.DeleteModelsFromInstance(ctx, "OpenAI-API-Compatible", "production", []string{"gpt-4o"}); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("mock lifecycle exceeded deadline, likely lock contention: %v", err)
	}
}

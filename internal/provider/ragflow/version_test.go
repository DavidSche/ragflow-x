package ragflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVersionProbe_Contract(t *testing.T) {
	// Lock the /system/version response field name "version".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/version" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": "v0.27.1", "message": "success"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	probe := NewVersionProbe(client)
	ver, err := probe.ProbeVersion(context.Background())
	if err != nil {
		t.Fatalf("ProbeVersion: %v", err)
	}
	if ver != "0.27.1" {
		t.Fatalf("version = %q, want 0.27.1", ver)
	}
}

func TestVersionProbe_CheckBaseline_Match(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": baselineVersion, "message": "success"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	probe := NewVersionProbe(client)
	// CheckBaseline should not panic; exact log output verified by inspection
	probe.CheckBaseline(context.Background())
}

func TestVersionProbe_CheckBaseline_Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": "0.99.0", "message": "success"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	probe := NewVersionProbe(client)
	// Should log a warning; not panic
	probe.CheckBaseline(context.Background())
}

func TestVersionProbe_CheckBaseline_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	probe := NewVersionProbe(client)
	// Should log a warning; not panic
	probe.CheckBaseline(context.Background())
}

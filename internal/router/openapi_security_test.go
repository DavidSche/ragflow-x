package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func expectedOpenAPISecurity(path string) []string {
	if strings.HasPrefix(path, "/v1/") {
		return []string{"ApiKeyAuth"}
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return nil
	}
	if path == "/api/v1/auth/me" {
		return []string{"BearerAuth"}
	}
	publicPrefixes := []string{
		"/api/v1/health", "/api/v1/healthz", "/api/v1/readyz",
		"/api/v1/branding/public",
		"/api/v1/auth/login", "/api/v1/auth/refresh", "/api/v1/auth/logout",
		"/api/v1/auth/oidc/",
		"/api/v1/system/setup/",
		"/api/v1/system/settings/runtime/report",
		"/api/v1/im/",
		"/api/v1/static/",
	}
	for _, prefix := range publicPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return []string{"BearerAuth"}
}

func TestOpenAPISecurityMatchesRoutePolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "docs", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger spec: %v", err)
	}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if method == "parameters" {
				continue
			}
			got := make([]string, 0, len(op.Security))
			for _, sec := range op.Security {
				for name := range sec {
					got = append(got, name)
				}
			}
			want := expectedOpenAPISecurity(path)
			if !sameStringSet(got, want) {
				t.Errorf("%s %s security mismatch: got %v want %v", strings.ToUpper(method), path, got, want)
			}
		}
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}

func TestOpenAPIOperationIDUnique(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "docs", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger spec: %v", err)
	}
	seen := make(map[string]string)
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if method == "parameters" {
				continue
			}
			if op.OperationID == "" {
				t.Errorf("%s %s missing operationId", strings.ToUpper(method), path)
				continue
			}
			if prev, ok := seen[op.OperationID]; ok {
				t.Errorf("duplicate operationId %s: %s and %s", op.OperationID, prev, path)
			}
			seen[op.OperationID] = path
		}
	}
}

func TestSwaggerUIDisabledByDefaultInRelease(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Docs.Enabled {
		t.Fatal("Docs.Enabled should default to false")
	}
}

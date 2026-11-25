package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func openAPIDocumentationConfig() config.Config {
	return config.Config{
		Server:        config.Server{Mode: "test"},
		Approval:      config.Approval{Enabled: true},
		Observability: config.Observability{MetricsEnabled: true},
	}
}

type operationKey struct {
	Method string
	Path   string
}

func normalizeOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = "{" + part[1:] + "}"
		} else if strings.HasPrefix(part, "*") {
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func isExternalAPIPath(path string) bool {
	if path == "/health" || path == "/healthz" || path == "/readyz" {
		return true
	}
	if strings.HasPrefix(path, "/v1/") {
		return true
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	if strings.HasPrefix(path, "/api/v1/static/") {
		return false
	}
	return true
}

func collectExternalOperations(routes gin.RoutesInfo) map[operationKey]struct{} {
	result := make(map[operationKey]struct{})
	for _, route := range routes {
		if !isExternalAPIPath(route.Path) {
			continue
		}
		result[operationKey{
			Method: strings.ToUpper(route.Method),
			Path:   normalizeOpenAPIPath(route.Path),
		}] = struct{}{}
	}
	return result
}

func loadSwaggerOperations(t *testing.T) map[operationKey]struct{} {
	t.Helper()
	specPath := filepath.Join("..", "..", "internal", "docs", "swagger.json")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read swagger spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger spec: %v", err)
	}
	result := make(map[operationKey]struct{})
	httpMethods := map[string]struct{}{
		"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "head": {}, "options": {},
	}
	for path, methods := range spec.Paths {
		if !isExternalAPIPath(path) {
			continue
		}
		for method := range methods {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			result[operationKey{Method: strings.ToUpper(method), Path: path}] = struct{}{}
		}
	}
	return result
}

func TestOpenAPIRouteCoverage(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "openapi-coverage.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.Migrate(gdb)
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("openapi-coverage", 1), "openapi-coverage")
	engine := New(openAPIDocumentationConfig(), handler.New(svc), ratelimit.NewMemory())

	actual := collectExternalOperations(engine.Routes())
	documented := loadSwaggerOperations(t)

	for key := range actual {
		if _, ok := documented[key]; !ok {
			t.Errorf("route missing from OpenAPI: %s %s", key.Method, key.Path)
		}
	}
	for key := range documented {
		if _, ok := actual[key]; !ok {
			t.Errorf("OpenAPI route not registered: %s %s", key.Method, key.Path)
		}
	}
}

func TestOpenAPIPathParameters(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "docs", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
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
			declared := make(map[string]bool)
			for _, p := range op.Parameters {
				if p.In == "path" {
					declared[p.Name] = p.Required
				}
			}
			for _, segment := range strings.Split(path, "/") {
				if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
					continue
				}
				name := segment[1 : len(segment)-1]
				if !declared[name] {
					t.Errorf("%s %s missing path parameter %s", strings.ToUpper(method), path, name)
				} else if !declared[name] {
					t.Errorf("%s %s path parameter %s is not required", strings.ToUpper(method), path, name)
				}
			}
		}
	}
}

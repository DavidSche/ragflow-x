package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// The repair endpoint is the layer the operator actually touched (doc/285): the
// service already refused to rewrite parsed documents without an opt-in, but
// nobody could see whether the HTTP layer agreed on the field name. A rename or
// a missing json tag here would reproduce the same field failure with the
// service tests still green, so the wire format gets its own test.

type parseProfileEndpointStub struct {
	ragflow.Mock
	docs       []ragflow.Document
	parseCalls int
	updatedTo  string
}

func (s *parseProfileEndpointStub) ListDocuments(context.Context, string) ([]ragflow.Document, error) {
	return s.docs, nil
}

func (s *parseProfileEndpointStub) ParseDocuments(context.Context, string, []string) error {
	s.parseCalls++
	return nil
}

func (s *parseProfileEndpointStub) UpdateDatasetConfig(_ context.Context, _ string, cfg ragflow.DatasetConfigUpdate) error {
	if cfg.ChunkMethod != nil {
		s.updatedTo = *cfg.ChunkMethod
	}
	return nil
}

func setupParseProfileEndpoint(t *testing.T) (*testEnv, string, *parseProfileEndpointStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "parse_profile_endpoint.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	stub := &parseProfileEndpointStub{
		Mock: *ragflow.NewMock(),
		docs: []ragflow.Document{{ID: "doc-1", Name: "guide.pdf", Enabled: "1", ChunkCount: 12}},
	}
	jwtMgr := jwt.NewManager("test-jwt-secret-for-integration-tests-32b", 24)
	svc := service.New(repository.NewStore(gdb), stub, jwtMgr, "test-encrypt-key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := t.Context()
	admin, err := svc.CreateUser(ctx, model.PlatformTenantID, "platform_admin", service.CreateUserRequest{
		Username: "admin-" + id.New()[:8],
		Password: "password123",
		Role:     "platform_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "parse-profile-"+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	tenantAdmin, err := svc.CreateUser(ctx, tenant.ID, admin.ID, service.CreateUserRequest{
		Username: "tenant-admin-" + id.New()[:8],
		Password: "password123",
		Role:     "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "raven-guide")
	if err != nil {
		t.Fatal(err)
	}

	h := New(svc)
	r := gin.New()
	r.Use(middleware.Auth(svc.JWT, svc))
	protected := r.Group("/api/v1")
	protected.GET("/datasets/:id/parse-profile", h.GetDatasetParseProfile)
	protected.POST("/datasets/:id/parse-profile", h.ApplyDatasetParseProfile)
	protected.POST("/datasets/:id/parse", h.ParseDocuments)

	return &testEnv{
		svc:        svc,
		h:          h,
		router:     r,
		jwtMgr:     jwtMgr,
		platformID: admin.ID,
		adminID:    tenantAdmin.ID,
		tenantID:   tenant.ID,
	}, dataset.ID, stub
}

// TestParseProfileEndpointRefusesWithoutRewriteFlag replays the field failure:
// a body without `rewrite_existing` must be refused with 40915 and must leave
// the dataset untouched.
func TestParseProfileEndpointRefusesWithoutRewriteFlag(t *testing.T) {
	env, datasetID, stub := setupParseProfileEndpoint(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)

	res := env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+datasetID+"/parse-profile",
		map[string]any{"chunk_method": "book"}, token)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", res.Code, res.Body.String())
	}
	body := jsonResponse(t, res)
	if code, ok := body["code"].(float64); !ok || int(code) != 40915 {
		t.Fatalf("error code = %v, want 40915 (body %s)", body["code"], res.Body.String())
	}
	if stub.updatedTo != "" || stub.parseCalls != 0 {
		t.Fatalf("a refused repair must not write, updated=%q parseCalls=%d", stub.updatedTo, stub.parseCalls)
	}
}

// TestParseProfileEndpointAcceptsRewriteFlag is the exact request the wizard now
// sends: the wire name has to be `rewrite_existing` or the fix is cosmetic.
func TestParseProfileEndpointAcceptsRewriteFlag(t *testing.T) {
	env, datasetID, stub := setupParseProfileEndpoint(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)

	res := env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+datasetID+"/parse-profile",
		map[string]any{"chunk_method": "book", "rewrite_existing": true}, token)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	body := jsonResponse(t, res)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing: %v", body)
	}
	if data["chunk_method"] != "book" {
		t.Fatalf("chunk_method = %v, want book", data["chunk_method"])
	}
	if data["reparse_triggered"] != true {
		t.Fatalf("reparse_triggered = %v, want true (the wizard keys its message off it)", data["reparse_triggered"])
	}
	if stub.updatedTo != "book" || stub.parseCalls != 1 {
		t.Fatalf("write channel mismatch: updated=%q parseCalls=%d", stub.updatedTo, stub.parseCalls)
	}
}

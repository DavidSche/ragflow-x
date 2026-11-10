package router

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// The per-route scope resolvers (access.go) are the only place where RBAC learns
// the project/user a nested resource belongs to. They were reachable only through
// full HTTP journeys, so the branch that extracts a project/user scope from a real
// row never executed — the nightly coverage trend caught internal/router sliding
// from 90.69% to 84.60%. These cases call the resolvers directly.
func newScopeResolverFixture(t *testing.T) (*service.Service, *gorm.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "resolver.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if conn, err := gdb.DB(); err == nil {
			_ = conn.Close()
		}
	})
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "resolver-test-key")
	const tenantID = "tenant-resolver"
	return svc, gdb, tenantID
}

func resolverContext(t *testing.T, tenantID, paramKey, paramValue string) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(context.Background())
	c.Set(middleware.ContextTenantID, tenantID)
	if paramKey != "" {
		c.Params = gin.Params{{Key: paramKey, Value: paramValue}}
	}
	return c
}

func TestAccessResolversReturnTenantWhenResourceIsAbsent(t *testing.T) {
	svc, _, tenantID := newScopeResolverFixture(t)
	cases := []struct {
		name     string
		paramKey string
		resolve  middleware.ResolveContext
	}{
		{name: "parser-policy", paramKey: "id", resolve: resolveParserPolicyScope(svc)},
		{name: "quality-profile", paramKey: "id", resolve: resolveQualityProfileScope(svc)},
		{name: "logical-document", paramKey: "id", resolve: resolveLogicalDocumentScope(svc)},
		{name: "tool-registry", paramKey: "id", resolve: resolveToolRegistryScope(svc)},
		{name: "source-routing-rule", paramKey: "id", resolve: resolveSourceRoutingRuleScope(svc)},
		{name: "db-connection", paramKey: "id", resolve: resolveDBConnectionScope(svc)},
		{name: "query-template", paramKey: "id", resolve: resolveQueryTemplateScope(svc)},
		{name: "knowledge-strategy", paramKey: "id", resolve: resolveKnowledgeStrategyScope(svc)},
		{name: "evidence-snapshot", paramKey: "id", resolve: resolveEvidenceSnapshotScope(svc)},
		{name: "outbox-event", paramKey: "id", resolve: resolveOutboxEventScope(svc)},
		{name: "eval-case", paramKey: "id", resolve: resolveEvalCaseScope(svc)},
		{name: "memory-operation", paramKey: "operationId", resolve: resolveMemoryOperationScope(svc)},
		{name: "dataset", paramKey: "id", resolve: resolveDatasetScope(svc)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A missing row must not widen the scope: the middleware keeps the
			// tenant bound and leaves project/user empty, so the handler then
			// decides 404 on its own.
			gotTenant, gotProject, gotUser, err := tc.resolve(resolverContext(t, tenantID, tc.paramKey, "missing"))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if gotTenant != tenantID || gotProject != "" || gotUser != "" {
				t.Fatalf("scope = (%q,%q,%q), want (%q,\"\",\"\")", gotTenant, gotProject, gotUser, tenantID)
			}
		})
	}
}

func TestAccessResolversExtractProjectAndUserScopes(t *testing.T) {
	svc, gdb, tenantID := newScopeResolverFixture(t)
	now := time.Now().UTC()

	seed := []interface{}{
		&model.DatasetLink{ID: "ds-1", TenantID: tenantID, RAGFlowDatasetID: "rf-ds-1", Name: "Dataset", ProjectID: "project-1",
			CreatedAt: now, UpdatedAt: now, ReviewStatus: "none"},
		&model.LogicalDocument{ID: "ld-1", TenantID: tenantID, DatasetID: "ds-1", Name: "Doc", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now},
		&model.ParserPolicy{ID: "pp-1", TenantID: tenantID, ProjectID: "project-pp", DatasetID: "ds-1", DocumentType: "pdf",
			ParseMode: "builtin", QualityProfileID: "qp-1", CreatedAt: now, UpdatedAt: now},
		&model.KnowledgeStrategy{ID: "ks-1", TenantID: tenantID, ProjectID: "project-ks", DatasetID: "ds-1",
			StrategyType: "hybrid", RAGFlowCapability: "retrieval", Config: "{}", FallbackStrategy: "none",
			AuthorizationScope: "{}", Active: true, CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now},
		&model.MemoryOperation{ID: "mo-1", TenantID: tenantID, ProjectID: "project-mo", PolicyID: "mp-1", MemoryID: "mem-1",
			AssistantID: "as-1", UserID: "user-mo", RequestID: "req-mo", OperationType: "forget",
			IdempotencyKey: "idem-mo", State: "pending", CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range seed {
		if err := gdb.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}

	cases := []struct {
		name        string
		paramKey    string
		paramValue  string
		resolve     middleware.ResolveContext
		wantProject string
		wantUser    string
	}{
		{name: "parser-policy", paramKey: "id", paramValue: "pp-1", resolve: resolveParserPolicyScope(svc), wantProject: "project-pp"},
		{name: "logical-document", paramKey: "id", paramValue: "ld-1", resolve: resolveLogicalDocumentScope(svc), wantProject: "project-1"},
		{name: "knowledge-strategy", paramKey: "id", paramValue: "ks-1", resolve: resolveKnowledgeStrategyScope(svc), wantProject: "project-ks"},
		{name: "memory-operation", paramKey: "operationId", paramValue: "mo-1", resolve: resolveMemoryOperationScope(svc), wantProject: "project-mo", wantUser: "user-mo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTenant, gotProject, gotUser, err := tc.resolve(resolverContext(t, tenantID, tc.paramKey, tc.paramValue))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if gotTenant != tenantID || gotProject != tc.wantProject || gotUser != tc.wantUser {
				t.Fatalf("scope = (%q,%q,%q), want (%q,%q,%q)", gotTenant, gotProject, gotUser, tenantID, tc.wantProject, tc.wantUser)
			}
		})
	}
}

// A store failure must abort the resolution instead of silently granting the
// tenant-wide scope.
func TestAccessResolverPropagatesStoreFailure(t *testing.T) {
	svc, gdb, tenantID := newScopeResolverFixture(t)
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, _, _, resolveErr := resolveParserPolicyScope(svc)(resolverContext(t, tenantID, "id", "pp-1"))
	if resolveErr == nil {
		t.Fatal("expected store failure to surface as a resolution error")
	}
}

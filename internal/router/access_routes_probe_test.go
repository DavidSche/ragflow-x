package router

import (
	"net/http"
	"net/http/httptest"
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

// e155RoutesThatAnswer401WithoutBeingProtected lists routes that are
// deliberately NOT behind the RBAC middleware but still answer 401 to an
// anonymous caller, so the behavioural probe below cannot tell them apart from
// protected routes. Each entry must still exist in the route table (asserted),
// so a deleted route leaves a failing test rather than a silent exception.
var e155RoutesThatAnswer401WithoutBeingProtected = map[string]string{
	"POST /api/v1/system/settings/runtime/report": "RuntimeReportAuth validates a shared token, not a user session",
	"GET /api/v1/im/wecom/callback":               "public webhook; the handler rejects a call without a valid WeCom signature",
	"POST /api/v1/im/wecom/callback":              "public webhook; the handler rejects a call without a valid WeCom signature",
}

// TestEveryAuthenticatedRouteHasAnAccessRule derives "is this route protected"
// from the running router's behaviour instead of from a hand-kept list.
//
// Why: accessRules denies unregistered paths by default, so a route that is
// registered in router.go but forgotten in accessRules is a permanent 403 for
// every role — exactly what happened to GET /api/v1/usage/filter-options
// (E159). The old hand-maintained list (protectedRoutes in access_test.go) had
// the same omission as accessRules, so it stayed green: two copies of one fact
// hid each other's gap (E138 shape).
//
// The probe works because Auth runs before RBAC: an anonymous request to a
// protected route stops at 401/403 no matter what the handler would do.
func TestEveryAuthenticatedRouteHasAnAccessRule(t *testing.T) {
	engine := newRouteProbeEngine(t)

	ruleKeys := map[string]bool{}
	for _, rule := range accessRules(nil) {
		ruleKeys[rule.Method+" "+rule.Path] = true
	}

	routes := map[string]bool{}
	ungoverned := []string{}
	protected := 0
	for _, info := range engine.Routes() {
		if !strings.HasPrefix(info.Path, "/api/v1/") {
			continue
		}
		// Served by gin.Static; there is no handler to authorize.
		if strings.HasPrefix(info.Path, "/api/v1/static/") {
			continue
		}
		key := info.Method + " " + info.Path
		routes[key] = true
		status := probeAnonymous(t, engine, info.Method, info.Path)
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			continue
		}
		if _, excused := e155RoutesThatAnswer401WithoutBeingProtected[key]; excused {
			protected++
			continue
		}
		protected++
		if !ruleKeys[key] {
			ungoverned = append(ungoverned, key)
		}
	}
	if len(ungoverned) > 0 {
		t.Fatalf("routes behind Auth have no accessRules entry (RBAC default-denies them, so every role gets 403): %v", ungoverned)
	}
	if protected == 0 {
		t.Fatal("probe found no authenticated routes at all; the classification is broken")
	}

	// Reverse direction: a rule that names a route nobody registered is dead
	// weight that hides a rename.
	stale := []string{}
	for key := range ruleKeys {
		if !routes[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("accessRules entries match no registered route: %v", stale)
	}

	for key := range e155RoutesThatAnswer401WithoutBeingProtected {
		if !routes[key] {
			t.Errorf("exception %q names a route that no longer exists", key)
		}
	}
}

// newRouteProbeEngine builds the real router over a throwaway sqlite database.
// Nothing is served; the test only reads the route table and the status code of
// an unauthenticated request.
func newRouteProbeEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		Server:   config.Server{Mode: "test"},
		App:      config.App{JWTSecret: "route-probe-secret", JWTExpireHours: 24, DataDir: t.TempDir()},
		Database: config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "route-probe.db")},
		RAGFlow:  config.RAGFlow{Provider: "mock"},
		// Approval routes only exist when the feature is on (router.go:399);
		// without this the reverse check would call their rules stale.
		Approval: config.Approval{Enabled: true},
	}
	gdb, err := db.Open(cfg.Database)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(),
		jwt.NewManager(cfg.App.JWTSecret, cfg.App.JWTExpireHours), "route-probe-secret")
	return New(cfg, handler.New(svc), ratelimit.NewMemory())
}

// probeAnonymous sends an empty request with every path parameter replaced by a
// literal so gin can route it.
func probeAnonymous(t *testing.T, engine *gin.Engine, method, path string) int {
	t.Helper()
	concrete := path
	for {
		start := strings.Index(concrete, ":")
		star := strings.Index(concrete, "*")
		idx := start
		if idx < 0 || (star >= 0 && star < idx) {
			idx = star
		}
		if idx < 0 {
			break
		}
		end := idx + 1
		for end < len(concrete) && concrete[end] != '/' {
			end++
		}
		concrete = concrete[:idx] + "probe" + concrete[end:]
	}
	req := httptest.NewRequest(method, concrete, nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		engine.ServeHTTP(rec, req)
	}()
	return rec.Code
}

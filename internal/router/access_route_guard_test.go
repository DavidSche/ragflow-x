package router

import (
	"sort"
	"strings"
	"testing"

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

// TestAccessRulesMatchRegisteredRoutes guards the direction the RBAC middleware
// cannot: a permission entry without a route. That combination is invisible at
// runtime — the middleware simply never runs, and the endpoint the doc promises
// is a 404 — so it has to be a build-time check.
//
// The reverse direction (a registered route with no rule) is enforced at runtime
// by deny-by-default: an unregistered authenticated route answers 403, which is
// loud. This test therefore only reports rules that point at nothing.
func TestAccessRulesMatchRegisteredRoutes(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "access-route-guard.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, dbErr := gdb.DB(); dbErr == nil {
			_ = raw.Close()
		}
	})
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("access-route-guard", 1), "access-route-guard")
	// Conditional route groups must be enabled here, otherwise the guard reports
	// their rules as phantom: the rule table is static, the route table is not.
	engine := New(config.Config{
		Server: config.Server{Mode: "test"},
		Approval: config.Approval{
			Enabled: true,
		},
	}, handler.New(svc), ratelimit.NewMemory())

	registered := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	var phantom []string
	for _, rule := range accessRules(svc) {
		if _, ok := registered[rule.Method+" "+rule.Path]; !ok {
			phantom = append(phantom, rule.Method+" "+rule.Path)
		}
	}
	sort.Strings(phantom)
	for _, entry := range phantom {
		t.Errorf("permission rule has no registered route: %s", entry)
	}
	if len(phantom) > 0 {
		t.Logf("phantom permission rules=%d (every one is a documented-but-missing endpoint)", len(phantom))
	}
	t.Logf("access rules=%d registered routes=%d phantom=%d", len(accessRules(svc)), len(engine.Routes()), len(phantom))
}

// TestAccessRulePathsAreNormalised is a cheap lint for the rule table: gin only
// matches its own `:param`/`*wildcard` syntax, so a rule written with braces or
// a regex (a common copy/paste from an OpenAPI file) would never match.
func TestAccessRulePathsAreNormalised(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "access-route-syntax.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, dbErr := gdb.DB(); dbErr == nil {
			_ = raw.Close()
		}
	})
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("access-route-syntax", 1), "access-route-syntax")
	_ = svc
	for _, rule := range accessRules(svc) {
		if strings.ContainsAny(rule.Path, "{}") {
			t.Errorf("permission rule path is not gin-normalised: %s", rule.Path)
		}
		if !strings.HasPrefix(rule.Path, "/api/v1/") {
			t.Errorf("permission rule path is outside /api/v1: %s", rule.Path)
		}
	}
}

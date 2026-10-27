package router

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ApprovalRoutesMatchAccessRules(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "approval-routes.db")})
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
	jwtManager := jwt.NewManager("approval-route-test-secret", 1)
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwtManager, "approval-route-test-key")
	svc.SetApprovalConfig(config.Approval{Enabled: true})
	engine := New(config.Config{Server: config.Server{Mode: "test"}, Approval: config.Approval{Enabled: true}}, handler.New(svc), ratelimit.NewMemory())

	actual := map[string]bool{}
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/approval") {
			actual[route.Method+" "+route.Path] = true
		}
	}
	expected := map[string]bool{}
	for _, rule := range accessRules(nil) {
		if rule.Resource == "approval" || rule.Resource == "approval-policy" {
			expected[rule.Method+" "+rule.Path] = true
		}
	}
	for route := range actual {
		if !expected[route] {
			t.Errorf("registered approval route is missing an access rule: %s", route)
		}
	}
	for route := range expected {
		if !actual[route] {
			t.Errorf("approval access rule has no registered route: %s", route)
		}
	}
}

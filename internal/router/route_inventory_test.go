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

// TestRouteInventory is the recomputable source of truth for the route counts in
// `doc/architecture/04_后端设计_golang_gin.md` §8. It asserts nothing about the
// absolute numbers on purpose — a magic constant would turn every new endpoint
// into a red build — it prints the real gin route table so the doc numbers can
// be re-derived with `go test ./internal/router/ -run TestRouteInventory -v`.
func TestRouteInventory(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "route-inventory.db")})
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
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("route-inventory", 1), "route-inventory")
	engine := New(config.Config{Server: config.Server{Mode: "test"}}, handler.New(svc), ratelimit.NewMemory())

	counts := map[string]int{}
	for _, route := range engine.Routes() {
		counts[routePrefix(route.Path)]++
	}
	prefixes := make([]string, 0, len(counts))
	for prefix := range counts {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	t.Logf("total=%d prefixes=%d", len(engine.Routes()), len(counts))
	for _, prefix := range prefixes {
		t.Logf("%s=%d", prefix, counts[prefix])
	}
}

// routePrefix folds a route path onto its first three segments so the inventory
// groups `/api/v1/datasets/:id/chunks` under `/api/v1/datasets`.
func routePrefix(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return "/" + strings.Join(parts, "/")
}

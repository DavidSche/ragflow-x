package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newFilterStore(t *testing.T) Store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "filters.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mkTenant(t *testing.T, store Store, id, name, status string, created time.Time) {
	t.Helper()
	if err := store.CreateTenant(context.Background(), &model.Tenant{ID: id, Name: name, Status: status, CreatedAt: created, UpdatedAt: created}); err != nil {
		t.Fatalf("create tenant %s: %v", id, err)
	}
}

func mkUser(t *testing.T, store Store, id, tenantID, username, role, status string) {
	t.Helper()
	if err := store.CreateUser(context.Background(), &model.User{ID: id, TenantID: tenantID, Username: username, Role: role, Status: status}); err != nil {
		t.Fatalf("create user %s: %v", id, err)
	}
}

func mkDataset(t *testing.T, store Store, id, tenantID, ragflowID, name string) {
	t.Helper()
	if err := store.CreateDatasetLink(context.Background(), &model.DatasetLink{ID: id, TenantID: tenantID, RAGFlowDatasetID: ragflowID, Name: name}); err != nil {
		t.Fatalf("create dataset %s: %v", id, err)
	}
}

func names(ts []model.Tenant) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func usernames(us []model.User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Username)
	}
	return out
}

func assertSet(t *testing.T, got []string, want map[string]bool, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s mismatch: got %v want %v", what, got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("%s unexpected %q in %v", what, g, got)
		}
	}
}

func TestListTenantsFilter(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mkTenant(t, store, "t1", "Acme Corp", "active", base)
	mkTenant(t, store, "t2", "Globex 100%", "disabled", base.AddDate(0, 0, 1))
	mkTenant(t, store, "t3", "Initech", "active", base.AddDate(0, 0, 2))

	// Name LIKE (no wildcard leakage when searching literal "%").
	items, total, err := store.ListTenants(ctx, 1, 20, TenantFilter{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Name != "Acme Corp" {
		t.Fatalf("name filter: got %v total=%d", names(items), total)
	}

	// An input containing a wildcard must match literally, not as a pattern.
	items, total, err = store.ListTenants(ctx, 1, 20, TenantFilter{Name: "100%"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Name != "Globex 100%" {
		t.Fatalf("wildcard-escaped name filter: got %v total=%d", names(items), total)
	}

	// Status exact.
	items, total, err = store.ListTenants(ctx, 1, 20, TenantFilter{Status: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "t2" {
		t.Fatalf("status filter: got %v total=%d", names(items), total)
	}

	// Date-only created_from is inclusive.
	items, total, err = store.ListTenants(ctx, 1, 20, TenantFilter{CreatedFrom: "2026-08-02"})
	if err != nil {
		t.Fatal(err)
	}
	// 08-2 and 08-3 are both >= 08-02.
	if total != 2 {
		t.Fatalf("created_from filter: got %v total=%d", names(items), total)
	}

	// Date-only created_to is exclusive and includes the whole day.
	items, total, err = store.ListTenants(ctx, 1, 20, TenantFilter{CreatedTo: "2026-08-02"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("created_to filter: got %v total=%d", names(items), total)
	}
}

func TestListUsersFilter(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	mkUser(t, store, "u1", "t1", "alice", "tenant_admin", "active")
	mkUser(t, store, "u2", "t1", "bob_100%", "operator", "active")
	mkUser(t, store, "u3", "t1", "carol", "viewer", "disabled")
	mkUser(t, store, "u4", "t2", "dan", "operator", "active")

	items, total, err := store.ListUsersByTenant(ctx, "t1", 1, 20, UserFilter{Username: "ali"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Username != "alice" {
		t.Fatalf("username filter: got %v total=%d", usernames(items), total)
	}

	// Role exact match against the stored role id.
	items, total, err = store.ListUsersByTenant(ctx, "t1", 1, 20, UserFilter{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "u2" {
		t.Fatalf("role filter: got %v total=%d", usernames(items), total)
	}

	// LIKE wildcard must be escaped.
	items, total, err = store.ListUsersByTenant(ctx, "t1", 1, 20, UserFilter{Username: "100%"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "u2" {
		t.Fatalf("username wildcard filter: got %v total=%d", usernames(items), total)
	}

	items, total, err = store.ListUsersByTenant(ctx, "t1", 1, 20, UserFilter{Status: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "u3" {
		t.Fatalf("status filter: got %v total=%d", usernames(items), total)
	}

	// Other tenants must never leak.
	items, total, err = store.ListUsersByTenant(ctx, "other", 1, 20, UserFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("cross-tenant leak: got %d/%d users", len(items), total)
	}

	// Cross-tenant governance APIs honor an explicit workspace filter.
	items, total, err = store.ListUsers(ctx, 1, 20, UserFilter{TenantID: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "u4" {
		t.Fatalf("cross-tenant user filter: got %v total=%d", usernames(items), total)
	}
}

func TestListAllDatasetLinksFilter(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	mkDataset(t, store, "d1", "t1", "rag-1", "Knowledge Base A")
	mkDataset(t, store, "d2", "t2", "rag-2", "Finance 50%")
	mkDataset(t, store, "d3", "t2", "rag-3", "HR Manual")

	items, err := store.ListAllDatasetLinks(ctx, DatasetFilter{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	assertSet(t, []string{items[0].ID}, map[string]bool{"d2": true}, "dataset name filter")

	// Name filter must escape literal wildcard chars.
	items, err = store.ListAllDatasetLinks(ctx, DatasetFilter{Name: "50%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "d2" {
		t.Fatalf("dataset wildcard filter: got %d rows", len(items))
	}

	// tenant_id exact match.
	items, err = store.ListAllDatasetLinks(ctx, DatasetFilter{TenantID: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	assertSet(t, []string{items[0].ID, items[1].ID}, map[string]bool{"d2": true, "d3": true}, "dataset tenant filter")
}

func TestListAuditsFilter(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	base := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	mkAudit := func(id, user, action, resource string, at time.Time) {
		t.Helper()
		if err := store.CreateAudit(ctx, &model.AuditLog{ID: id, TenantID: "t1", UserID: user, Action: action, Resource: resource, At: at}); err != nil {
			t.Fatalf("create audit %s: %v", id, err)
		}
	}
	mkAudit("a1", "u1", "tenant.create", "tenant", base)
	mkAudit("a2", "u2", "user.role", "user", base.Add(time.Hour))
	mkAudit("a3", "u1", "dataset.delete", "dataset", base.Add(2*time.Hour))

	// user_id exact.
	items, total, err := store.ListAudits(ctx, "t1", 1, 20, AuditFilter{UserID: "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "a2" {
		t.Fatalf("user_id filter: got %d rows", total)
	}

	// action LIKE with escaping.
	items, total, err = store.ListAudits(ctx, "t1", 1, 20, AuditFilter{Action: "user."})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "a2" {
		t.Fatalf("action LIKE filter: got %d rows", total)
	}

	// resource LIKE.
	items, total, err = store.ListAudits(ctx, "t1", 1, 20, AuditFilter{Resource: "dataset"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "a3" {
		t.Fatalf("resource LIKE filter: got %d rows", total)
	}

	// Date range [from, to): inclusive start, exclusive end.
	items, total, err = store.ListAudits(ctx, "t1", 1, 20, AuditFilter{CreatedFrom: "2026-08-10T08:30:00Z", CreatedTo: "2026-08-10T09:30:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "a2" {
		t.Fatalf("audit date range filter: got %d rows", total)
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	cases := map[string]string{
		"plain": "%plain%",
		"a%b":   "%a\\%b%",
		"a_b":   "%a\\_b%",
		"a\\b":  "%a\\\\b%",
		"":      "",
	}
	for in, want := range cases {
		if got := likePattern(in); got != want {
			t.Fatalf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
	if !(DatasetFilter{Name: "x"}).IsActive() || !(DatasetFilter{TenantID: "t"}).IsActive() {
		t.Fatal("non-empty dataset filter must be active")
	}
	if (DatasetFilter{}).IsActive() {
		t.Fatal("empty dataset filter must be inactive")
	}
}

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The usage reports filter by IDs (chat/user/key/project/assistant/release) and
// by model/scenario. Those were free-text inputs; these options are what the
// dropdowns show, so they must be scoped and carry a readable label.
func TestUsageFilterOptionsCarryLabelsAndRespectScope(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "usage-filter-options.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	tenantA, tenantB := "tenant-a", "tenant-b"
	for _, tenant := range []*model.Tenant{
		{ID: tenantA, Name: "Workspace A", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now},
		{ID: tenantB, Name: "Workspace B", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now},
	} {
		if err := gdb.Create(tenant).Error; err != nil {
			t.Fatal(err)
		}
	}
	user := &model.User{ID: "user-a", TenantID: tenantA, Username: "alice", Email: "alice@example.com", PasswordHash: "x", Role: model.RoleOperator, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{ID: "key-a", TenantID: tenantA, UserID: user.ID, Name: "Gateway key", KeyHash: "hash", Prefix: "rgx_ab12"}
	if err := gdb.Create(key).Error; err != nil {
		t.Fatal(err)
	}
	chat := &model.ChatShadow{ID: "chat-a", TenantID: tenantA, Name: "Policy assistant", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(chat).Error; err != nil {
		t.Fatal(err)
	}
	project := &model.Project{ID: "project-a", TenantID: tenantA, Name: "Onboarding", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(project).Error; err != nil {
		t.Fatal(err)
	}
	assistant := &model.Assistant{ID: "assistant-a", TenantID: tenantA, ProjectID: project.ID, Name: "Policy helper", OwnerID: user.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(assistant).Error; err != nil {
		t.Fatal(err)
	}
	release := &model.AssistantRelease{
		ID: "release-a", TenantID: tenantA, AssistantID: assistant.ID, AssistantVersionID: "version-a",
		ReleaseVersion: 3, ReleaseState: "PUBLISHED", DesiredStateJSON: "{}", DesiredStateHash: "h",
		SnapshotManifestID: "manifest", CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(release).Error; err != nil {
		t.Fatal(err)
	}

	metrics := []*model.CostMetric{
		{ID: "cm-a", RequestID: "req-a", TenantID: tenantA, UserID: user.ID, KeyID: key.ID, Date: "2026-11-20", Model: "deepseek-chat", Scenario: "chat", ChatID: chat.ID, CreatedAt: now},
		{ID: "cm-b", RequestID: "req-b", TenantID: tenantB, UserID: "user-b", Date: "2026-11-20", Model: "qwen-max", Scenario: "search", ChatID: "chat-b", CreatedAt: now},
	}
	for _, metric := range metrics {
		if err := gdb.Create(metric).Error; err != nil {
			t.Fatal(err)
		}
	}
	trace := &model.TraceRun{
		ID: "trace-a", TenantID: tenantA, TraceID: "trace-a", SpanID: "root", RequestID: "req-a",
		UserID: user.ID, ProjectID: project.ID, AssistantID: assistant.ID, AssistantReleaseID: release.ID,
		AppType: "chat", AppID: chat.ID, Status: model.TraceRunCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(trace).Error; err != nil {
		t.Fatal(err)
	}

	scoped, err := s.UsageFilterOptions(ctx, tenantA, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Models) != 1 || scoped.Models[0].Value != "deepseek-chat" {
		t.Fatalf("models = %+v, want only the caller tenant's model", scoped.Models)
	}
	if len(scoped.Users) != 1 || scoped.Users[0].Label != "alice" {
		t.Fatalf("users = %+v, want alice", scoped.Users)
	}
	if len(scoped.Keys) != 1 || scoped.Keys[0].Label != "Gateway key (rgx_ab12)" {
		t.Fatalf("keys = %+v", scoped.Keys)
	}
	if len(scoped.Chats) != 1 || scoped.Chats[0].Label != "Policy assistant" {
		t.Fatalf("chats = %+v", scoped.Chats)
	}
	if len(scoped.Projects) != 1 || scoped.Projects[0].Label != "Onboarding" {
		t.Fatalf("projects = %+v", scoped.Projects)
	}
	if len(scoped.Assistants) != 1 || scoped.Assistants[0].Label != "Policy helper" {
		t.Fatalf("assistants = %+v", scoped.Assistants)
	}
	if len(scoped.Releases) != 1 || scoped.Releases[0].Label != "Policy helper v3" {
		t.Fatalf("releases = %+v", scoped.Releases)
	}
	if len(scoped.Scenarios) != 1 || scoped.Scenarios[0].Value != "chat" {
		t.Fatalf("scenarios = %+v, want the union of cost and trace scenarios", scoped.Scenarios)
	}
	if len(scoped.Tenants) != 0 {
		t.Fatalf("tenant options are governance-only, got %+v", scoped.Tenants)
	}

	all, err := s.UsageFilterOptions(ctx, tenantA, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Models) != 2 {
		t.Fatalf("scoped-all models = %+v, want both tenants", all.Models)
	}
	if len(all.Tenants) != 2 || all.Tenants[0].Label == "" {
		t.Fatalf("scoped-all tenants = %+v", all.Tenants)
	}
}

// The assistant/chat comboboxes narrow their list with ?search while the
// operator types; the match must work against the resolved label (a name), not
// only the raw ID, and every other dimension narrows too.
func TestUsageFilterOptionsSearchMatchesLabels(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "usage-filter-search.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	tenant := &model.Tenant{ID: "tenant-a", Name: "Workspace A", Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(tenant).Error; err != nil {
		t.Fatal(err)
	}
	for _, chat := range []*model.ChatShadow{
		{ID: "chat-policy", TenantID: tenant.ID, Name: "Policy assistant", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "chat-billing", TenantID: tenant.ID, Name: "Billing assistant", Status: "active", CreatedAt: now, UpdatedAt: now},
	} {
		if err := gdb.Create(chat).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, chatID := range []string{"chat-policy", "chat-billing"} {
		metric := &model.CostMetric{
			ID: fmt.Sprintf("cm-%d", i), RequestID: fmt.Sprintf("req-%d", i), TenantID: tenant.ID,
			UserID: "user-a", Date: "2026-11-20", Model: "deepseek-chat", Scenario: "chat", ChatID: chatID, CreatedAt: now,
		}
		if err := gdb.Create(metric).Error; err != nil {
			t.Fatal(err)
		}
	}

	found, err := s.UsageFilterOptions(ctx, tenant.ID, false, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Chats) != 1 || found.Chats[0].Value != "chat-billing" {
		t.Fatalf("chat search = %+v, want only Billing assistant", found.Chats)
	}
	if len(found.Models) != 0 {
		t.Fatalf("search should narrow every dimension, models = %+v", found.Models)
	}

	byID, err := s.UsageFilterOptions(ctx, tenant.ID, false, "chat-policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(byID.Chats) != 1 || byID.Chats[0].Label != "Policy assistant" {
		t.Fatalf("search by ID = %+v", byID.Chats)
	}
}

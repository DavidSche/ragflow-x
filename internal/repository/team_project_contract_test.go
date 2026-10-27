package repository

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newTeamProjectStore(t *testing.T) Store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "team-project.db")})
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

func seedTenantProjectAndTeam(t *testing.T, store Store, tenantID, projectID, teamID string) {
	t.Helper()
	ctx := context.Background()
	seededAt := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	if err := store.CreateProject(ctx, &model.Project{
		ID: projectID, TenantID: tenantID, Name: projectID, CreatedAt: seededAt, UpdatedAt: seededAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(ctx, &model.Team{
		ID: teamID, TenantID: tenantID, Name: teamID, OwnerID: "owner", CreatedAt: seededAt, UpdatedAt: seededAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func countTeamProjects(t *testing.T, testStore Store, tenantID string) int64 {
	t.Helper()
	var count int64
	err := testStore.(*store).DB.
		Model(&model.TeamProject{}).
		Joins("JOIN rgx_project ON rgx_project.id = rgx_team_project.project_id").
		Where("rgx_project.tenant_id = ?", tenantID).
		Count(&count).Error
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_SetTeamProjectsFailsAtomicallyOnDuplicateBinding(t *testing.T) {
	store := newTeamProjectStore(t)
	ctx := context.Background()
	seedTenantProjectAndTeam(t, store, "tenant-a", "project-1", "team-1")

	if err := store.SetTeamProjects(ctx, "tenant-a", "team-1", []string{"project-1"}); err != nil {
		t.Fatal(err)
	}

	err := store.SetTeamProjects(ctx, "tenant-a", "team-1", []string{"project-2", "project-1", "project-1"})
	if err == nil {
		t.Fatal("expected duplicate project binding to fail")
	}
	if count := countTeamProjects(t, store, "tenant-a"); count != 1 {
		t.Fatalf("transaction rolled back to existing binding, got %d rows", count)
	}
}

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_SetProjectTeamsRejectsForeignTenantAndRollsBack(t *testing.T) {
	store := newTeamProjectStore(t)
	ctx := context.Background()
	seedTenantProjectAndTeam(t, store, "tenant-a", "project-1", "team-1")
	seedTenantProjectAndTeam(t, store, "tenant-b", "project-foreign", "team-foreign")

	if err := store.SetProjectTeams(ctx, "tenant-a", "project-1", []string{"team-1"}); err != nil {
		t.Fatal(err)
	}

	err := store.SetProjectTeams(ctx, "tenant-a", "project-1", []string{"team-foreign", "team-1"})
	if err == nil || !strings.Contains(err.Error(), "not found in tenant") {
		t.Fatalf("expected foreign tenant rejection, got %v", err)
	}
	if count := countTeamProjects(t, store, "tenant-a"); count != 1 {
		t.Fatalf("transaction rolled back to existing binding, got %d rows", count)
	}
}

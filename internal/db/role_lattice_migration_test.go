package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestMigrationV159AlignsTeamAdminMemberSurfaces proves the corrective data
// migration lands the doc/243 lattice on a database that was seeded before the
// change: team_admin becomes a superset of business_user, operator becomes a
// superset of viewer, and no duplicate grant rows survive.
func TestMigrationV159AlignsTeamAdminMemberSurfaces(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	var applied int64
	if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 159).Count(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected migration 159 to be recorded once, got %d", applied)
	}

	var teamReads []model.Permission
	if err := gdb.Where("role_id = ? AND action = ?", model.RoleTeamAdmin, "read").
		Find(&teamReads).Error; err != nil {
		t.Fatal(err)
	}
	team := map[string]bool{}
	for _, row := range teamReads {
		team[row.Resource] = true
	}
	for _, resource := range []string{"chat", "search-app", "agent", "assistant", "memory", "eval-set"} {
		if !team[resource] {
			t.Fatalf("team_admin must read %q after migration 159 (doc/243)", resource)
		}
	}

	// operator must keep *not* holding execute@memory-forget: forgetting someone
	// else's memory is a privacy boundary (doc/137 §10.2/§10.3), not a missing
	// grant. This is the one deliberate break in "operator dominates member".
	var operatorForget int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleOperator, "execute", "memory-forget").
		Count(&operatorForget).Error; err != nil {
		t.Fatal(err)
	}
	if operatorForget != 0 {
		t.Fatalf("operator must not hold memory-forget execute (doc/137 §10.3), got %d", operatorForget)
	}

	// The declared lattice must hold on the persisted rows, not just in code.
	index := map[string]map[string]map[string]struct{}{}
	var all []model.Permission
	if err := gdb.Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range all {
		if index[row.Resource] == nil {
			index[row.Resource] = map[string]map[string]struct{}{}
		}
		if index[row.Resource][row.RoleID] == nil {
			index[row.Resource][row.RoleID] = map[string]struct{}{}
		}
		index[row.Resource][row.RoleID][row.Action] = struct{}{}
	}
	for _, rel := range roleSupersetRelations {
		for resource, byRole := range index {
			for action := range byRole[rel.Subset] {
				if _, ok := byRole[rel.Superset][action]; !ok {
					t.Fatalf("persisted grants violate %s ⊇ %s on %q/%q", rel.Superset, rel.Subset, resource, action)
				}
			}
		}
	}
}

// TestMigrationV159IsReentrant deletes the grants the migration adds, plants a
// stale row the migration must remove, and re-runs the reconciler twice.
func TestMigrationV159IsReentrant(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	if err := gdb.Where("role_id = ? AND resource = ? AND action IN ?",
		model.RoleTeamAdmin, "chat", []string{"read", "execute"}).Delete(&model.Permission{}).Error; err != nil {
		t.Fatal(err)
	}
	stale := model.Permission{
		ID: "perm-stale-159", RoleID: model.RoleTeamAdmin, Action: "manage", Resource: "system",
	}
	if err := gdb.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}

	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var chatGrants, staleRows int64
	gdb.Model(&model.Permission{}).
		Where("role_id = ? AND resource = ? AND action IN ?", model.RoleTeamAdmin, "chat", []string{"read", "execute"}).
		Count(&chatGrants)
	gdb.Model(&model.Permission{}).Where("id = ?", "perm-stale-159").Count(&staleRows)
	if chatGrants != 2 {
		t.Fatalf("expected chat read+execute for team_admin after reconcile, got %d", chatGrants)
	}
	if staleRows != 0 {
		t.Fatal("reconcile must drop grants that are not in the builtin matrix")
	}

	var before, after int64
	gdb.Model(&model.Permission{}).Count(&before)
	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	gdb.Model(&model.Permission{}).Count(&after)
	if before != after {
		t.Fatalf("reconcile is not idempotent: %d rows then %d rows", before, after)
	}
}

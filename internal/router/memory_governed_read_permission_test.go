package router

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMemoryGovernedReadPermissionBootstrap(t *testing.T) {
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "memory-policy" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	for _, role := range []string{
		model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator, model.RoleTeamAdmin, model.RoleBusinessUser,
	} {
		if !grants[role]["execute"] || !grants[role]["read"] {
			t.Fatalf("role %s must be able to run governed memory retrieval, got %v", role, grants[role])
		}
	}
	if grants[model.RoleViewer]["execute"] {
		t.Fatal("viewer must not run governed memory retrieval")
	}
}

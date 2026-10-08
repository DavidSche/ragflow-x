package service

import (
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

var authorizationRoles = map[string]struct{}{
	model.RolePlatformAdmin: {}, model.RoleTenantAdmin: {}, model.RoleTeamAdmin: {},
	model.RoleOperator: {}, model.RoleBusinessUser: {}, model.RoleViewer: {},
}

type authorizationScope struct {
	Roles []string `json:"roles"`
}

func validateAuthorizationScope(raw, field string) (string, error) {
	compact, err := toolRegistryJSON(raw, field)
	if err != nil {
		return "", err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(compact), &document); err != nil {
		return "", httperr.BadRequest(40194, field+" must be a valid authorization scope")
	}
	if len(document) != 1 {
		return "", httperr.BadRequest(40194, field+" must contain only roles")
	}
	var scope authorizationScope
	if err := json.Unmarshal([]byte(compact), &scope); err != nil {
		return "", httperr.BadRequest(40194, field+".roles must be an array")
	}
	if len(scope.Roles) == 0 {
		return "", httperr.BadRequest(40194, field+".roles must not be empty")
	}
	seen := make(map[string]struct{}, len(scope.Roles))
	for _, role := range scope.Roles {
		role = strings.TrimSpace(role)
		if _, allowed := authorizationRoles[role]; !allowed {
			return "", httperr.BadRequest(40194, field+".roles contains an unknown role "+role)
		}
		if _, duplicate := seen[role]; duplicate {
			return "", httperr.BadRequest(40194, field+".roles contains duplicate role "+role)
		}
		seen[role] = struct{}{}
	}
	return compact, nil
}

func userAllowedByScope(rawScope, role string) bool {
	canonical, err := validateAuthorizationScope(rawScope, "authorization_scope")
	if err != nil {
		return false
	}
	var scope authorizationScope
	if json.Unmarshal([]byte(canonical), &scope) != nil {
		return false
	}
	for _, allowed := range scope.Roles {
		if strings.TrimSpace(allowed) == role {
			return true
		}
	}
	return false
}

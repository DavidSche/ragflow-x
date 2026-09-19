package service

import (
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

const (
	APIKeyScopeChatApps = "chat_apps"
	APIKeyScopeModels   = "models"
	APIKeyScopeRoutes   = "routes"
)

// APIKeyScopes is a credential constraint, not a second authorization policy.
// An omitted ScopesJSON remains an unrestricted legacy credential; an empty
// list inside an explicit object denies that dimension.
type APIKeyScopes struct {
	ChatApps []string `json:"chat_apps"`
	Models   []string `json:"models"`
	Routes   []string `json:"routes"`
}

func parseAPIKeyScopes(raw string) APIKeyScopes {
	var scopes APIKeyScopes
	_ = json.Unmarshal([]byte(raw), &scopes)
	return scopes
}

func scopeAllows(values []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, item := range values {
		if item == "*" || item == value {
			return true
		}
	}
	return false
}

// AuthorizeAPIKeyScope applies the credential dimension after principal RBAC
// and resource ownership have been checked. It therefore intersects the API
// Key constraint with the platform authorization model instead of replacing it.
func (s *Service) AuthorizeAPIKeyScope(key *model.APIKey, dimension, value string) error {
	if key == nil || key.ScopesJSON == "" {
		return nil
	}
	var allowed []string
	switch dimension {
	case APIKeyScopeChatApps:
		allowed = parseAPIKeyScopes(key.ScopesJSON).ChatApps
	case APIKeyScopeModels:
		allowed = parseAPIKeyScopes(key.ScopesJSON).Models
	case APIKeyScopeRoutes:
		allowed = parseAPIKeyScopes(key.ScopesJSON).Routes
	default:
		return httperr.BadRequest(40042, "unsupported api key scope")
	}
	if !scopeAllows(allowed, value) {
		return httperr.Forbidden("api key is not authorized for this scope")
	}
	return nil
}

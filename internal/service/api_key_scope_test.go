package service

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestAuthorizeAPIKeyScopeIntersectsCredentialConstraint(t *testing.T) {
	svc := &Service{}
	key := &model.APIKey{ScopesJSON: `{"chat_apps":["chat-1"],"models":["model-a","model-b"],"routes":["chat"]}`}

	if err := svc.AuthorizeAPIKeyScope(key, APIKeyScopeChatApps, "chat-1"); err != nil {
		t.Fatalf("allowed chat scope: %v", err)
	}
	if err := svc.AuthorizeAPIKeyScope(key, APIKeyScopeModels, "model-b"); err != nil {
		t.Fatalf("allowed model scope: %v", err)
	}
	if err := svc.AuthorizeAPIKeyScope(key, APIKeyScopeChatApps, "chat-2"); err == nil {
		t.Fatal("expected out-of-scope chat to be denied")
	}
	if err := svc.AuthorizeAPIKeyScope(key, APIKeyScopeModels, "model-c"); err == nil {
		t.Fatal("expected out-of-scope model to be denied")
	}
	if err := svc.AuthorizeAPIKeyScope(key, APIKeyScopeRoutes, "agent"); err == nil {
		t.Fatal("expected out-of-scope route to be denied")
	}
}

func TestAuthorizeAPIKeyScopeDefaultDenyAndLegacyCompatibility(t *testing.T) {
	svc := &Service{}
	explicit := &model.APIKey{ScopesJSON: `{}`}
	if err := svc.AuthorizeAPIKeyScope(explicit, APIKeyScopeModels, "model-a"); err == nil {
		t.Fatal("expected explicit empty scope to deny")
	}
	if err := svc.AuthorizeAPIKeyScope(&model.APIKey{}, APIKeyScopeModels, "model-a"); err != nil {
		t.Fatalf("legacy unrestricted key: %v", err)
	}
}

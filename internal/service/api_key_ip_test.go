package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestAuthorizeAPIKeyIP(t *testing.T) {
	legacy := &model.APIKey{}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(legacy, "203.0.113.9"); err != nil {
		t.Fatalf("legacy key should be unrestricted: %v", err)
	}

	empty := &model.APIKey{AllowedIPsJSON: "[]"}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(empty, "127.0.0.1"); err == nil {
		t.Fatal("explicit empty allowlist must deny every IP")
	}

	key := &model.APIKey{AllowedIPsJSON: `["127.0.0.1","10.20.0.0/24"]`}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(key, "127.0.0.1"); err != nil {
		t.Fatalf("exact IP should be allowed: %v", err)
	}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(key, "10.20.0.18"); err != nil {
		t.Fatalf("CIDR address should be allowed: %v", err)
	}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(key, "203.0.113.9"); err == nil {
		t.Fatal("unlisted IP should be denied")
	}

	malformed := &model.APIKey{AllowedIPsJSON: `["not-an-ip"]`}
	if err := newAuthzSvc(t).AuthorizeAPIKeyIP(malformed, "127.0.0.1"); err == nil {
		t.Fatal("malformed persisted allowlist must fail closed")
	}
}

func TestCreateAPIKeyValidatesAndPersistsAllowedIPs(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "API Key IP Tenant "+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin: %+v err=%v", user, err)
	}

	ips := []string{"127.0.0.1", "10.20.0.0/24", " bad "}
	if _, _, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "invalid", nil, 0, 0, nil, &ips); err == nil {
		t.Fatal("expected invalid allowed IP to be rejected")
	}

	ips = []string{"127.0.0.1", "10.20.0.0/24"}
	_, key, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "allowlist", nil, 0, 0, nil, &ips)
	if err != nil {
		t.Fatal(err)
	}
	if key.AllowedIPsJSON != `["127.0.0.1","10.20.0.0/24"]` {
		t.Fatalf("allowed ips = %s", key.AllowedIPsJSON)
	}

	empty := []string{}
	_, key, err = svc.CreateAPIKey(ctx, tenant.ID, user.ID, "deny-all", nil, 0, 0, nil, &empty)
	if err != nil || key.AllowedIPsJSON != "[]" {
		t.Fatalf("empty allowlist key: key=%+v err=%v", key, err)
	}
}

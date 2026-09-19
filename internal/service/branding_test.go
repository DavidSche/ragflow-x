package service

import (
	"context"
	"testing"
)

func TestBrandingUpdatesAndWorkspaceFallback(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	if got := svc.PublicBranding(ctx); got.Name != DefaultBrandName || got.Logo != "" {
		t.Fatalf("unexpected default branding: %+v", got)
	}

	if err := svc.SetBranding(ctx, SystemTenantID, "Platform Brand", "/platform.png"); err != nil {
		t.Fatal(err)
	}
	platform := svc.PublicBranding(ctx)
	if platform.Name != "Platform Brand" || platform.Logo != "/platform.png" {
		t.Fatalf("platform branding was not updated: %+v", platform)
	}

	tenant, err := svc.CreateTenant(ctx, "Branding Tenant")
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.GetBranding(ctx, tenant.ID); got != platform {
		t.Fatalf("workspace without branding should fall back to platform, got %+v", got)
	}

	if err := svc.SetBranding(ctx, tenant.ID, "Tenant Brand", ""); err != nil {
		t.Fatal(err)
	}
	got := svc.GetBranding(ctx, tenant.ID)
	if got.Name != "Tenant Brand" || got.Logo != "/platform.png" {
		t.Fatalf("workspace name and platform logo fallback are wrong: %+v", got)
	}

	if err := svc.SetBrandingLogo(ctx, tenant.ID, "/tenant.png"); err != nil {
		t.Fatal(err)
	}
	got = svc.GetBranding(ctx, tenant.ID)
	if got.Name != "Tenant Brand" || got.Logo != "/tenant.png" {
		t.Fatalf("logo-only update changed brand name or lost logo: %+v", got)
	}
}

func TestBrandingMissingTenantReturnsNotFound(t *testing.T) {
	svc := newAuthzSvc(t)
	if err := svc.SetBranding(context.Background(), "missing", "Name", ""); err == nil {
		t.Fatal("expected missing tenant error")
	}
}

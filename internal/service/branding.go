package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// DefaultBrandName is used when no platform branding is configured.
const DefaultBrandName = "RAGFlow-X"

// Branding is the effective system name + logo for a tenant or the platform.
type Branding struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
}

// PublicBranding returns the platform-level branding (used on the login page
// and as the fallback for all tenants).
func (s *Service) PublicBranding(ctx context.Context) Branding {
	return s.brandingFor(ctx, SystemTenantID)
}

// GetBranding returns the effective branding for a tenant, falling back to the
// platform branding when the tenant has not customized its name/logo.
func (s *Service) GetBranding(ctx context.Context, tenantID string) Branding {
	return s.brandingFor(ctx, tenantID)
}

// SetBranding updates the branding of the caller's tenant (the platform tenant
// updates the platform-wide branding).
func (s *Service) SetBranding(ctx context.Context, tenantID, name, logo string) error {
	t, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if t == nil {
		return httperr.NotFound("tenant not found")
	}
	t.BrandName = name
	t.BrandLogo = logo
	return s.Store.UpdateTenant(ctx, t)
}

// SetBrandingLogo updates only the tenant logo, preserving the brand name.
func (s *Service) SetBrandingLogo(ctx context.Context, tenantID, logo string) error {
	t, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if t == nil {
		return httperr.NotFound("tenant not found")
	}
	t.BrandLogo = logo
	return s.Store.UpdateTenant(ctx, t)
}

func (s *Service) brandingFor(ctx context.Context, tenantID string) Branding {
	platform := Branding{Name: DefaultBrandName}
	if pt, err := s.Store.GetTenant(ctx, SystemTenantID); err == nil && pt != nil {
		if pt.BrandName != "" {
			platform.Name = pt.BrandName
		}
		platform.Logo = pt.BrandLogo
	}
	brand := platform
	if tenantID != SystemTenantID {
		if t, err := s.Store.GetTenant(ctx, tenantID); err == nil && t != nil {
			if t.BrandName != "" {
				brand.Name = t.BrandName
			}
			if t.BrandLogo != "" {
				brand.Logo = t.BrandLogo
			} else if t.BrandName == "" {
				brand = platform
			}
		}
	}
	return brand
}

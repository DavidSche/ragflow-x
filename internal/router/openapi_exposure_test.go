package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
)

// doc/310 §13.2 treats the three doc resources as security resources and §13.1
// requires an authenticated platform administrator in production. The gap these
// tests cover was not a missing test but a missing gate: `if cfg.Docs.Enabled`
// mounted /swagger, /docs/openapi.json and /docs/openapi.yaml with no
// authentication at all, so "enabled" and "publicly readable" were the same
// switch. A team that read §13.1, flipped Docs.Enabled on a production box and
// walked away would have published the whole API description to anonymous
// callers while every other check was green.

func TestDocsExposureDefaultsAreClosed(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Docs.Enabled {
		t.Fatal("Docs.Enabled must default to false (doc/310 §13.1)")
	}
	if !cfg.Docs.RequireAdmin {
		t.Fatal("Docs.RequireAdmin must default to true: the plan puts the administrator " +
			"requirement on production, so the default has to be the safe side, not the convenient one")
	}
}

func TestDocsNotMountedWhenDisabled(t *testing.T) {
	app := newTestAppWith(t, func(cfg *config.Config) {
		cfg.Docs = config.Docs{Enabled: false, RequireAdmin: false}
	})
	defer app.close()

	token := app.login(t)
	for _, path := range []string{"/docs/openapi.json", "/docs/openapi.yaml", "/swagger/index.html"} {
		resp, body := app.doAuth(t, http.MethodGet, path, token, nil)
		// Not 404: the router installs the SPA fallback as NoRoute, so an unmounted
		// doc path answers with index.html and 200. The assertion that matters is
		// that the document is not there — a 404-only check would pass on a box that
		// served the spec through the fallback.
		if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "json") || strings.Contains(ct, "yaml") {
			t.Errorf("%s: want no document when Docs.Enabled=false, got %q", path, ct)
		}
		if strings.Contains(string(body), `"openapi":"3`) || strings.Contains(string(body), "swagger:") {
			t.Errorf("%s: Docs.Enabled=false but the body looks like a spec (%d bytes)", path, len(body))
		}
	}
}

func TestDocsRequirePlatformAdminWhenEnabled(t *testing.T) {
	app := newTestAppWith(t, func(cfg *config.Config) {
		cfg.Docs = config.Docs{Enabled: true, RequireAdmin: true}
	})
	defer app.close()

	adminToken := app.login(t)

	// An authenticated non-administrator in the platform tenant: 403, not 404 and
	// not 200. The plan's §13.1 says "认证与授权" — both, and they are two answers.
	memberToken, err := jwt.NewManager("test-secret", 24).Issue("u-docs-member", model.PlatformTenantID, "docs-member", model.RoleUser)
	if err != nil {
		t.Fatalf("issue member token: %v", err)
	}

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"authenticated user", memberToken, http.StatusForbidden},
		{"platform admin", adminToken, http.StatusOK},
	}
	for _, path := range []string{"/docs/openapi.json", "/docs/openapi.yaml", "/swagger/index.html"} {
		for _, tc := range cases {
			resp, body := app.doAuth(t, http.MethodGet, path, tc.token, nil)
			if resp.StatusCode != tc.want {
				t.Errorf("%s as %s: want %d, got %d (%s)", path, tc.name, tc.want, resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}
	}

	// The gate must gate the spec itself, not just the UI shell: an admin read has
	// to return the real document, or the three 403/401s above prove nothing about
	// what an admin would have seen.
	resp, body := app.doAuth(t, http.MethodGet, "/docs/openapi.json", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin read: %d", resp.StatusCode)
	}
	var spec struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatalf("spec is not JSON: %v", err)
	}
	if spec.OpenAPI == "" || len(spec.Paths) == 0 {
		t.Fatalf("admin read returned something that is not a spec: openapi=%q paths=%d", spec.OpenAPI, len(spec.Paths))
	}
}

func TestDocsOpenWithoutAdminWhenExplicitlyOptedOut(t *testing.T) {
	app := newTestAppWith(t, func(cfg *config.Config) {
		cfg.Docs = config.Docs{Enabled: true, RequireAdmin: false}
	})
	defer app.close()

	// A development box that opts out must actually be open — otherwise the switch
	// is a decoration and someone will "fix" the 401 by turning Docs off.
	resp, body := app.doAuth(t, http.MethodGet, "/docs/openapi.json", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous read with RequireAdmin=false: want 200, got %d (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"openapi"`) {
		t.Fatal("anonymous read did not return the spec")
	}
}

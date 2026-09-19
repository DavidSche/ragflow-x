package router_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type routerOIDCClient struct {
	claims service.OIDCClaims
	nonce  string
}

func (client *routerOIDCClient) ResolveEndpoints(_ context.Context, cfg config.OIDC) (config.OIDC, error) {
	return cfg, nil
}

func (client *routerOIDCClient) AuthURL(cfg config.OIDC, state, nonce string) string {
	client.nonce = nonce
	return "https://idp.example.com/authorize?client_id=" + url.QueryEscape(cfg.ClientID) +
		"&state=" + url.QueryEscape(state) + "&nonce=" + url.QueryEscape(nonce)
}

func (client *routerOIDCClient) Exchange(_ context.Context, _ config.OIDC, _ string) (service.OIDCClaims, error) {
	client.claims.Nonce = client.nonce
	return client.claims, nil
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestE2E_OIDCAuthorizationCodeLogin(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()
	tenant, err := app.svc.CreateTenant(ctx, "OIDC Workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "oidc-user", Password: "secret123", Email: "oidc-user@example.com", Role: model.RoleBusinessUser,
	}); err != nil {
		t.Fatal(err)
	}
	app.svc.SetOIDCConfig(config.OIDC{
		Enabled: true, Issuer: "https://idp.example.com", TenantID: tenant.ID,
		ClientID: "ragflow-x", ClientSecret: "client-secret", RedirectURL: "https://app.example.com/api/v1/auth/oidc/callback",
		AuthURL: "https://idp.example.com/authorize", TokenURL: "https://idp.example.com/token",
		JWKSURL: "https://idp.example.com/jwks", PostLoginPath: "/",
	})
	oidcClient := &routerOIDCClient{claims: service.OIDCClaims{
		Issuer: "https://idp.example.com", Subject: "subject-1", Nonce: "",
		Email: "oidc-user@example.com", EmailVerified: true,
	}}
	app.svc.SetOIDCClient(oidcClient)

	start, err := noRedirectClient().Get(app.ts.URL + "/api/v1/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	defer start.Body.Close()
	if start.StatusCode != http.StatusFound {
		t.Fatalf("OIDC start status = %d", start.StatusCode)
	}
	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Host != "idp.example.com" || authURL.Query().Get("client_id") != "ragflow-x" {
		t.Fatalf("unexpected authorization URL: %s", start.Header.Get("Location"))
	}
	state := cookieValue(start.Cookies(), "rgx_oidc_state")
	nonce := cookieValue(start.Cookies(), "rgx_oidc_nonce")
	if len(state) < 32 || len(nonce) < 32 {
		t.Fatalf("OIDC flow cookies are missing: state=%d nonce=%d", len(state), len(nonce))
	}

	badCallback, err := http.Get(app.ts.URL + "/api/v1/auth/oidc/callback?code=code&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	badCallback.Body.Close()
	if badCallback.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong state status = %d, want 400", badCallback.StatusCode)
	}

	callbackURL := app.ts.URL + "/api/v1/auth/oidc/callback?code=code&state=" + url.QueryEscape(state)
	req, err := http.NewRequest(http.MethodGet, callbackURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "rgx_oidc_state", Value: state})
	req.AddCookie(&http.Cookie{Name: "rgx_oidc_nonce", Value: nonce})
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("OIDC callback status = %d location = %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	accessToken := cookieValue(resp.Cookies(), "rgx_access")
	if accessToken == "" {
		t.Fatal("OIDC callback did not issue an access cookie")
	}
	me, body := app.doAuth(t, http.MethodGet, "/api/v1/auth/me", accessToken, nil)
	if me.StatusCode != http.StatusOK || !strings.Contains(string(body), "oidc-user") {
		t.Fatalf("OIDC session /auth/me = %d %s", me.StatusCode, body)
	}
}

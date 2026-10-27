package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

type stubOIDCClient struct {
	resolved    config.OIDC
	claims      OIDCClaims
	exchangeErr error
}

func (client *stubOIDCClient) ResolveEndpoints(_ context.Context, cfg config.OIDC) (config.OIDC, error) {
	return client.resolved, nil
}

func (client *stubOIDCClient) AuthURL(cfg config.OIDC, state, nonce string) string {
	return cfg.AuthURL + "?state=" + state + "&nonce=" + nonce
}

func (client *stubOIDCClient) Exchange(_ context.Context, _ config.OIDC, _ string) (OIDCClaims, error) {
	return client.claims, client.exchangeErr
}

func newOIDCService(t *testing.T) (*Service, model.Tenant) {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "SSO Workspace")
	if err != nil {
		t.Fatal(err)
	}
	return svc, tenant
}

func TestLoginWithOIDCLinksProvisionedUserOnly(t *testing.T) {
	ctx := context.Background()
	svc, tenant := newOIDCService(t)
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "sso-user", Password: "secret123", Email: "user@example.com", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.OIDC{
		Enabled: true, Issuer: "https://idp.example.com", TenantID: tenant.ID,
		ClientID: "ragflow-x", ClientSecret: "secret", RedirectURL: "https://x.example.com/api/v1/auth/oidc/callback",
		AuthURL: "https://idp.example.com/authorize", TokenURL: "https://idp.example.com/token",
		JWKSURL: "https://idp.example.com/jwks", PostLoginPath: "/",
	}
	svc.SetOIDCConfig(cfg)
	svc.SetOIDCClient(&stubOIDCClient{resolved: cfg, claims: OIDCClaims{
		Issuer: cfg.Issuer, Subject: "subject-1", Nonce: "nonce", Email: "USER@example.com",
		EmailVerified: true, Claims: jwt.MapClaims{},
	}})

	first, gotCfg, err := svc.LoginWithOIDC(ctx, "code", "nonce", "127.0.0.1")
	if err != nil {
		t.Fatalf("first OIDC login: %v", err)
	}
	if first.User.ID != user.ID || first.User.Role != model.RoleBusinessUser || gotCfg.PostLoginPath != "/" || first.Token == "" || first.Refresh == "" {
		t.Fatalf("unexpected OIDC session: %+v cfg=%+v", first, gotCfg)
	}
	identity, err := svc.Store.GetOidcIdentity(ctx, cfg.Issuer, "subject-1")
	if err != nil || identity == nil || identity.UserID != user.ID {
		t.Fatalf("identity not linked: %+v err=%v", identity, err)
	}
	if _, _, err = svc.LoginWithOIDC(ctx, "code", "nonce", "127.0.0.1"); err != nil {
		t.Fatalf("repeat OIDC login: %v", err)
	}
}

func TestLoginWithOIDCRejectsUnprovisionedIdentity(t *testing.T) {
	ctx := context.Background()
	svc, _ := newOIDCService(t)
	cfg := config.OIDC{
		Enabled: true, Issuer: "https://idp.example.com", ClientID: "ragflow-x", ClientSecret: "secret",
		RedirectURL: "https://x.example.com/api/v1/auth/oidc/callback", AuthURL: "https://idp.example.com/authorize",
		TokenURL: "https://idp.example.com/token", JWKSURL: "https://idp.example.com/jwks",
	}
	svc.SetOIDCConfig(cfg)
	svc.SetOIDCClient(&stubOIDCClient{resolved: cfg, claims: OIDCClaims{
		Issuer: cfg.Issuer, Subject: "unknown", Nonce: "nonce", Email: "unknown@example.com",
		EmailVerified: true, Claims: jwt.MapClaims{},
	}})
	_, _, err := svc.LoginWithOIDC(ctx, "code", "nonce", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("unprovisioned user must be denied, got %v", err)
	}
}

func newOIDCIDPServer(t *testing.T, privateKey *rsa.PublicKey, tokenResponse map[string]interface{}) *httptest.Server {
	t.Helper()
	modulus := base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes())
	exponent := big.NewInt(int64(privateKey.E)).Bytes()
	jwks := map[string]interface{}{"keys": []map[string]interface{}{{
		"kty": "RSA", "kid": "test-key", "alg": "RS256", "n": modulus,
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tokenResponse)
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwks)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHTTPOIDCClientVerifiesIDToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokenResponse := map[string]interface{}{"id_token": ""}
	server := newOIDCIDPServer(t, &privateKey.PublicKey, tokenResponse)
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": server.URL, "sub": "subject-1", "aud": "client", "nonce": "nonce",
		"email": "user@example.com", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tokenResponse["id_token"] = raw
	cfg := config.OIDC{
		Issuer: server.URL, TokenURL: server.URL + "/token", JWKSURL: server.URL + "/jwks",
		ClientID: "client", RequireEmailVerified: true,
	}
	claims, err := newHTTPOIDCClient(nil, true).Exchange(context.Background(), cfg, "code")
	if err != nil {
		t.Fatalf("verify ID token: %v", err)
	}
	if claims.Subject != "subject-1" || claims.Email != "user@example.com" ||
		!claims.EmailVerified || claims.Nonce != "nonce" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestHTTPOIDCClientRejectsUnverifiedEmail(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokenResponse := map[string]interface{}{"id_token": ""}
	server := newOIDCIDPServer(t, &privateKey.PublicKey, tokenResponse)
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": server.URL, "sub": "subject-1", "aud": "client", "nonce": "nonce",
		"email": "user@example.com", "email_verified": false, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tokenResponse["id_token"] = raw
	cfg := config.OIDC{
		Issuer: server.URL, TokenURL: server.URL + "/token", JWKSURL: server.URL + "/jwks",
		ClientID: "client", RequireEmailVerified: true,
	}
	if _, err := newHTTPOIDCClient(nil, true).Exchange(context.Background(), cfg, "code"); err == nil {
		t.Fatal("unverified email claim must not be accepted")
	}
}

func TestCurrentOIDCClientUsesConfiguredTimeout(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetGatewayClients(DefaultGatewayConfig())
	svc.SetOIDCConfig(config.OIDC{HTTPTimeoutSec: 3})
	client, ok := svc.currentOIDCClient().(*httpOIDCClient)
	if !ok || client.client.Timeout != 3*time.Second {
		t.Fatalf("OIDC client timeout = %+v, want 3s", client)
	}
}

package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

type httpOIDCClient struct {
	client              *http.Client
	allowPrivateNetwork bool
}

func newHTTPOIDCClient(client *http.Client, allowPrivateNetwork bool) *httpOIDCClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &httpOIDCClient{client: client, allowPrivateNetwork: allowPrivateNetwork}
}

func normalizeOIDC(cfg config.OIDC) config.OIDC {
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	cfg.AuthURL = strings.TrimSpace(cfg.AuthURL)
	cfg.TokenURL = strings.TrimSpace(cfg.TokenURL)
	cfg.JWKSURL = strings.TrimSpace(cfg.JWKSURL)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	if cfg.PostLoginPath == "" || !strings.HasPrefix(cfg.PostLoginPath, "/") || strings.HasPrefix(cfg.PostLoginPath, "//") {
		cfg.PostLoginPath = "/"
	}
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = 10
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	return cfg
}

// ResolveEndpoints fills missing endpoint metadata from the issuer discovery
// document. Explicitly configured endpoints always win, which also supports
// contract-test providers.
func (c *httpOIDCClient) ResolveEndpoints(ctx context.Context, cfg config.OIDC) (config.OIDC, error) {
	cfg = normalizeOIDC(cfg)
	if cfg.AuthURL != "" && cfg.TokenURL != "" && cfg.JWKSURL != "" {
		return cfg, nil
	}
	if err := ValidateProviderBaseURL(cfg.Issuer, c.allowPrivateNetwork); err != nil {
		return cfg, err
	}
	discoveryURL := strings.TrimRight(cfg.Issuer, "/") + "/.well-known/openid-configuration"
	var discovery struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if err := c.getJSON(ctx, &discovery, discoveryURL); err != nil {
		return cfg, httperr.New(502, 50240, "OIDC discovery failed")
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = discovery.AuthorizationEndpoint
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = discovery.TokenEndpoint
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = discovery.JWKSURI
	}
	if cfg.AuthURL == "" || cfg.TokenURL == "" || cfg.JWKSURL == "" {
		return cfg, httperr.New(502, 50241, "OIDC discovery response is incomplete")
	}
	return cfg, nil
}

func (c *httpOIDCClient) AuthURL(cfg config.OIDC, state, nonce string) string {
	cfg = normalizeOIDC(cfg)
	parsed, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", cfg.RedirectURL)
	query.Set("scope", strings.Join(cfg.Scopes, " "))
	query.Set("state", state)
	query.Set("nonce", nonce)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (c *httpOIDCClient) Exchange(ctx context.Context, cfg config.OIDC, code string) (OIDCClaims, error) {
	cfg = normalizeOIDC(cfg)
	if code == "" {
		return OIDCClaims{}, httperr.Unauthorized("missing OIDC authorization code")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}
	var tokenResponse struct {
		IDToken string `json:"id_token"`
	}
	if err := c.postJSON(ctx, cfg.TokenURL, form, &tokenResponse); err != nil || tokenResponse.IDToken == "" {
		return OIDCClaims{}, httperr.Unauthorized("OIDC token exchange failed")
	}
	return c.verifyIDToken(ctx, cfg, tokenResponse.IDToken)
}

func (c *httpOIDCClient) verifyIDToken(ctx context.Context, cfg config.OIDC, raw string) (OIDCClaims, error) {
	parser := jwt.NewParser()
	unverified, _, err := parser.ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return OIDCClaims{}, httperr.Unauthorized("invalid OIDC token")
	}
	kid, _ := unverifiedJWTKeyID(unverified)
	key, err := c.findJWKSKey(ctx, cfg, kid)
	if err != nil {
		return OIDCClaims{}, err
	}
	claims := jwt.MapClaims{}
	verified, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	).ParseWithClaims(raw, claims, func(*jwt.Token) (interface{}, error) { return key, nil })
	if err != nil || !verified.Valid {
		return OIDCClaims{}, httperr.Unauthorized("invalid OIDC token")
	}
	audience, err := claims.GetAudience()
	if err != nil || !containsString(audience, cfg.ClientID) {
		return OIDCClaims{}, httperr.Unauthorized("invalid OIDC audience")
	}
	nonce, _ := claims["nonce"].(string)
	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return OIDCClaims{}, httperr.Unauthorized("OIDC token has no subject")
	}
	email, _ := claims["email"].(string)
	emailVerified, _ := claims["email_verified"].(bool)
	if strings.TrimSpace(email) == "" {
		return OIDCClaims{}, httperr.Unauthorized("OIDC token has no verified email")
	}
	if cfg.RequireEmailVerified && !emailVerified {
		return OIDCClaims{}, httperr.Unauthorized("OIDC token has no verified email")
	}
	issuer, err := claims.GetIssuer()
	if err != nil {
		return OIDCClaims{}, httperr.Unauthorized("invalid OIDC issuer")
	}
	return OIDCClaims{
		Issuer: issuer, Subject: subject, Nonce: nonce, Email: email,
		EmailVerified: emailVerified, Claims: claims, Verified: time.Now().UTC(),
	}, nil
}

func (c *httpOIDCClient) findJWKSKey(ctx context.Context, cfg config.OIDC, kid string) (*rsa.PublicKey, error) {
	if err := ValidateProviderBaseURL(cfg.JWKSURL, c.allowPrivateNetwork); err != nil {
		return nil, err
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := c.getJSON(ctx, &jwks, cfg.JWKSURL); err != nil {
		return nil, httperr.New(502, 50242, "OIDC JWKS fetch failed")
	}
	for _, key := range jwks.Keys {
		if (kid != "" && key.Kid != kid) || key.Kty != "RSA" || (key.Alg != "" && key.Alg != "RS256") {
			continue
		}
		modulus, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil || len(modulus) == 0 {
			continue
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			continue
		}
		exponent := 0
		for _, part := range exponentBytes {
			exponent = exponent<<8 | int(part)
		}
		if exponent < 3 {
			continue
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}, nil
	}
	return nil, httperr.Unauthorized("OIDC signing key not found")
}

func (c *httpOIDCClient) getJSON(ctx context.Context, target interface{}, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OIDC HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(target)
}

func (c *httpOIDCClient) postJSON(ctx context.Context, rawURL string, form url.Values, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OIDC HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 512*1024)).Decode(target)
}

func unverifiedJWTKeyID(token *jwt.Token) (string, error) {
	if token == nil {
		return "", fmt.Errorf("missing token")
	}
	kid, _ := token.Header["kid"].(string)
	return kid, nil
}

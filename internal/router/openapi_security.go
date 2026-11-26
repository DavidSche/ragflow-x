package router

import "strings"

// OpenAPISecurity is the single source of "<path> is served under which security scheme".
//
// It lives here, next to the route table, because the answer is a property of how the
// routes are mounted: everything except the public group is behind a credential, the SPA
// shell is behind none. The published spec and `openapi_security_test.go` both read this
// function — the test used to own a private copy of the list, which is the same "second
// hand-written description" that doc/304 §4 E159 removed from the RBAC side.
//
// There is exactly one scheme, `BearerAuth`: `Authorization: Bearer <credential>`. The
// gateway (`/v1/...`) reads the tenant API key out of that same header
// (internal/handler/gateway.go strips the prefix), so "API key" and "JWT" are two
// credentials for one header, not two schemes. swag v2 holds a single security definition
// (a second `@securityDefinitions` block overwrites the first — measured while writing
// doc/310 §22), so a two-scheme document was never an option here; the old
// `ApiKeyAuth`/`X-API-Key` entry described a header no handler reads.
func OpenAPISecurity(path string) []string {
	if !strings.HasPrefix(path, "/api/v1/") && !strings.HasPrefix(path, "/v1/") {
		return nil
	}
	if path == "/api/v1/auth/me" {
		return []string{"BearerAuth"}
	}
	for _, prefix := range publicRoutePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return []string{"BearerAuth"}
}

// publicRoutePrefixes are the routes registered on the *unauthenticated* group in
// router.go: health probes, login/refresh/logout, OIDC, first-run setup, the runtime
// report the frontend reads before login, IM callbacks, and the static/branding assets.
var publicRoutePrefixes = []string{
	"/api/v1/health", "/api/v1/healthz", "/api/v1/readyz",
	"/api/v1/branding/public",
	"/api/v1/auth/login", "/api/v1/auth/refresh", "/api/v1/auth/logout",
	"/api/v1/auth/oidc/",
	"/api/v1/system/setup/",
	"/api/v1/system/settings/runtime/report",
	"/api/v1/im/",
	"/api/v1/static/",
}

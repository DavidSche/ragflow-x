package router_test

// doc/301 GW-4 (task T22): the four end-to-end checks of a gateway key's life.
//
//	1. expiry            — an expired key authenticates nothing and writes nothing
//	2. rotation grace    — the old key keeps working until its window closes, and the
//	                       successor cannot be minted twice
//	3. last used         — "used" means "authenticated", and it is recorded on the
//	                       credential that actually did the work
//	4. scope drift       — every gateway route is scope-checked, and the scope values
//	                       are a closed set that a source scan can pin
//
// The point of writing these as *router* tests rather than service tests is that three of
// the four are properties of the assembled middleware stack: a service test cannot prove
// that an expired key never reaches metering, because metering happens in the handler.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// keyLifecycleApp is a gateway app with one provider and one model route, so a call can
// actually succeed and be metered.
type keyLifecycleApp struct {
	app        *testApp
	token      string
	providerAt *int
}

func newKeyLifecycleApp(t *testing.T) *keyLifecycleApp {
	t.Helper()
	hits := 0
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-life", "model": "life-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "ok"}}},
			"usage":   map[string]int64{"prompt_tokens": 2, "completion_tokens": 3},
		})
	}))
	t.Cleanup(providerSrv.Close)

	app := newTestApp(t)
	t.Cleanup(app.close)
	token := app.login(t)

	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"life","base_url":%q,"api_key":"sk-life","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create provider: %d %s", resp.StatusCode, body)
	}
	var provider struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &provider); err != nil {
		t.Fatal(err)
	}
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"life-model","target_model":"m"}`, provider.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create route: %d %s", resp.StatusCode, body)
	}
	return &keyLifecycleApp{app: app, token: token, providerAt: &hits}
}

// createKey posts to /keys and returns the raw secret and the key row.
func (k *keyLifecycleApp) createKey(t *testing.T, payload string) (secret, keyID string) {
	t.Helper()
	resp, body := k.app.doAuth(t, http.MethodPost, "/api/v1/keys", k.token, []byte(payload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create key: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			Secret string `json:"secret"`
			Key    struct {
				ID string `json:"id"`
			} `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.Secret == "" || created.Data.Key.ID == "" {
		t.Fatalf("create key returned no secret/id: %s", body)
	}
	return created.Data.Secret, created.Data.Key.ID
}

// call runs one gateway completion with the given raw secret.
func (k *keyLifecycleApp) call(t *testing.T, secret, idempotencyKey string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, k.app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"life-model","messages":[{"role":"user","content":"life"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, buf.Bytes()
}

func (k *keyLifecycleApp) count(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	if err := k.app.db.Table(table).Count(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func (k *keyLifecycleApp) keyRow(t *testing.T, keyID string) model.APIKey {
	t.Helper()
	var row model.APIKey
	if err := k.app.db.Where("id = ?", keyID).First(&row).Error; err != nil {
		t.Fatalf("load key %s: %v", keyID, err)
	}
	return row
}

// TestAPIKeyExpiryAuthenticatesNothingAndWritesNothing is check 1.
//
// The interesting half is "writes nothing": a 401 that still inserted a quota or
// idempotency row would let an expired credential consume the allowance and poison the
// replay ledger, and the status code alone would never show it.
func TestAPIKeyExpiryAuthenticatesNothingAndWritesNothing(t *testing.T) {
	k := newKeyLifecycleApp(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	secret, keyID := k.createKey(t, fmt.Sprintf(`{"name":"expired","expires_at":%q}`, past))

	status, body := k.call(t, secret, "expired-call")
	if status != http.StatusUnauthorized {
		t.Fatalf("expired key must be 401, got %d: %s", status, body)
	}
	if *k.providerAt != 0 {
		t.Fatalf("an expired key reached the provider %d times", *k.providerAt)
	}
	for _, table := range []string{"rgx_quota_usage", "rgx_meter_request", "rgx_gateway_idempotency"} {
		if n := k.count(t, table); n != 0 {
			t.Fatalf("an expired key wrote %d row(s) to %s", n, table)
		}
	}
	// `last_used_at` is the observable half of the same promise: a refused credential
	// must not look alive. `ResolveAPIKey` raises before it touches the row.
	if row := k.keyRow(t, keyID); row.LastUsedAt != nil {
		t.Fatalf("an expired key must not stamp last_used_at, got %v", row.LastUsedAt)
	}
}

// TestAPIKeyRotationKeepsTheOldKeyUntilItsWindowCloses is check 2.
func TestAPIKeyRotationKeepsTheOldKeyUntilItsWindowCloses(t *testing.T) {
	k := newKeyLifecycleApp(t)
	oldSecret, oldID := k.createKey(t, `{"name":"rotate","token_quota":1000,"request_quota":50,"scopes":{"models":["life-model"],"routes":["chat"]}}`)
	original := k.keyRow(t, oldID)

	// Grace 0 is the "cut over now" case: the old key must be dead the moment the
	// response returns, and the new one must already work.
	resp, body := k.app.doAuth(t, http.MethodPost, "/api/v1/keys/"+oldID+"/rotate", k.token, []byte(`{"grace_hours":0}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d %s", resp.StatusCode, body)
	}
	var rotated struct {
		Data struct {
			Secret  string       `json:"secret"`
			Key     model.APIKey `json:"key"`
			Retired model.APIKey `json:"retired"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Data.Secret == "" || rotated.Data.Secret == oldSecret {
		t.Fatal("rotation must return a new secret")
	}
	if rotated.Data.Retired.RotatedToKeyID != rotated.Data.Key.ID {
		t.Fatalf("the retired key must point at its successor: %+v", rotated.Data.Retired)
	}

	// Inheritance is total: a rotation that quietly dropped a constraint would be a
	// privilege change with an innocuous name.
	if rotated.Data.Key.TokenQuota != original.TokenQuota ||
		rotated.Data.Key.RequestQuota != original.RequestQuota ||
		rotated.Data.Key.ScopesJSON != original.ScopesJSON ||
		rotated.Data.Key.AllowedIPsJSON != original.AllowedIPsJSON ||
		rotated.Data.Key.UserID != original.UserID {
		t.Fatalf("successor did not inherit the old constraints: old=%+v new=%+v", original, rotated.Data.Key)
	}

	if status, _ := k.call(t, oldSecret, ""); status != http.StatusUnauthorized {
		t.Fatalf("grace 0 must retire the old key immediately, got %d", status)
	}
	if status, body := k.call(t, rotated.Data.Secret, ""); status != http.StatusOK {
		t.Fatalf("the successor must work at once, got %d: %s", status, body)
	}

	// A second rotation of the same key is the double-click case: a new successor
	// would be a second live secret nobody asked for.
	resp, body = k.app.doAuth(t, http.MethodPost, "/api/v1/keys/"+oldID+"/rotate", k.token, []byte(`{"grace_hours":0}`))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second rotation must be 409, got %d: %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("41150")) {
		t.Fatalf("the conflict must carry 41150 so the client can translate it, got %s", body)
	}
}

// TestAPIKeyRotationGraceWindowIsHonoured is the other half of check 2: a non-zero grace
// keeps the old key working, and the stored expiry is the number that governs it.
func TestAPIKeyRotationGraceWindowIsHonoured(t *testing.T) {
	k := newKeyLifecycleApp(t)
	oldSecret, oldID := k.createKey(t, `{"name":"grace"}`)

	resp, body := k.app.doAuth(t, http.MethodPost, "/api/v1/keys/"+oldID+"/rotate", k.token, []byte(`{"grace_hours":24}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d %s", resp.StatusCode, body)
	}
	row := k.keyRow(t, oldID)
	if row.ExpiredAt == nil {
		t.Fatal("a rotated key must carry an expiry: that expiry *is* the grace window")
	}
	remaining := time.Until(row.ExpiredAt.UTC())
	if remaining < 23*time.Hour || remaining > 25*time.Hour {
		t.Fatalf("grace window is %.1fh, want ~24h", remaining.Hours())
	}
	// The retired key is still enabled — that is what makes the window a window. A
	// rotation that also disabled it would be a revoke wearing a different name.
	if !row.Enabled {
		t.Fatal("a rotated key must stay enabled during its grace window")
	}
	if status, body := k.call(t, oldSecret, ""); status != http.StatusOK {
		t.Fatalf("the old key must keep working inside the window, got %d: %s", status, body)
	}

	// A revoked key cannot be rotated: rotation would resurrect the capability under a
	// new secret, which is the one thing revoke promises it prevented.
	_, revokedID := k.createKey(t, `{"name":"revoked"}`)
	if resp, body := k.app.doAuth(t, http.MethodPost, "/api/v1/keys/"+revokedID+"/revoke", k.token, []byte(`{}`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d %s", resp.StatusCode, body)
	}
	resp, body = k.app.doAuth(t, http.MethodPost, "/api/v1/keys/"+revokedID+"/rotate", k.token, []byte(`{"grace_hours":0}`))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("rotating a revoked key must be 409, got %d: %s", resp.StatusCode, body)
	}
}

// TestAPIKeyLastUsedRecordsAuthenticationNotSuccess is check 3, and it pins the *meaning*
// of the column as much as its behaviour.
//
// `last_used_at` answers "which credentials are still in use", which is how an operator
// decides what to revoke. Recording it only on a 200 would make a key whose every call is
// refused look unused — and that is exactly the key somebody should be looking at.
func TestAPIKeyLastUsedRecordsAuthenticationNotSuccess(t *testing.T) {
	k := newKeyLifecycleApp(t)
	secret, keyID := k.createKey(t, `{"name":"touched"}`)
	if row := k.keyRow(t, keyID); row.LastUsedAt != nil {
		t.Fatal("a fresh key has never authenticated anything")
	}

	if status, body := k.call(t, secret, ""); status != http.StatusOK {
		t.Fatalf("call: %d %s", status, body)
	}
	first := k.keyRow(t, keyID).LastUsedAt
	if first == nil {
		t.Fatal("a successful call must stamp last_used_at")
	}

	// A scope refusal happens *after* the credential authenticated, so it must stamp too.
	scoped, scopedID := k.createKey(t, `{"name":"scoped-out","scopes":{"models":["other-model"]}}`)
	before := time.Now().UTC()
	if status, _ := k.call(t, scoped, ""); status != http.StatusForbidden {
		t.Fatalf("an out-of-scope model must be 403, got %d", status)
	}
	stamped := k.keyRow(t, scopedID).LastUsedAt
	if stamped == nil || stamped.Before(before.Add(-time.Second)) {
		t.Fatalf("a refused-but-authenticated call must still stamp last_used_at, got %v", stamped)
	}
}

// TestGatewayRouteScopeValuesAreAClosedSet is check 4.
//
// Scope drift is not a bug in one place; it is the shape of a route added next quarter
// with a scope value nobody enumerated, so the test reads the source. Every gateway route
// in `gatewayRouteSpecs` must be scope-checked with a value from a closed set, and the
// route-role set must match the route count — a new route that skips the check changes
// that count and fails here.
func TestGatewayRouteScopeValuesAreAClosedSet(t *testing.T) {
	src, err := os.ReadFile("../handler/gateway.go")
	if err != nil {
		t.Fatalf("read gateway handler: %v", err)
	}
	text := string(src)

	routeScope := regexp.MustCompile(`AuthorizeAPIKeyScope\(key, service\.APIKeyScopeRoutes, "([^"]*)"\)`)
	declared := map[string]bool{"chat": true, "search-app": true, "agent": true}
	// Distinct values, not call sites: `/v1/chat/completions` legitimately checks
	// `routes=chat` on both of its branches (chat-app target vs model pass-through),
	// and counting branches would make a refactor look like drift.
	seen := map[string]bool{}
	for _, m := range routeScope.FindAllStringSubmatch(text, -1) {
		if !declared[m[1]] {
			t.Errorf("route scope %q is not in the closed set {chat, search-app, agent}", m[1])
		}
		seen[m[1]] = true
	}

	// The three gateway routes, one each. A route that lost its check shows up as a
	// missing entry; a fourth route that was added without one shows up as a count
	// mismatch against `gatewayRouteSpecs`.
	routes, err := os.ReadFile("../router/router.go")
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	specCount := strings.Count(string(routes), `{Method: "POST", Path: "/v1/`)
	// The surface is three routes, so this is a literal: a fourth gateway route must
	// stop here and decide two things (its scope value, its contract entry). Deriving
	// the expectation from the source would let a new route slide in unnoticed.
	if specCount != 3 {
		t.Fatalf("the gateway surface is 3 routes, router declares %d; a new route must give this test a scope value", specCount)
	}
	if len(seen) != 3 {
		t.Fatalf("3 gateway routes but %d distinct route scopes: %v", len(seen), seen)
	}
	for value := range declared {
		if !seen[value] {
			t.Errorf("route scope %q is declared but no gateway route checks it", value)
		}
	}

	// The value the handler passes must also be a value a key can be created with.
	// `models` and `chat_apps` scopes are checked elsewhere and are enumerated here so
	// that a rename in either direction fails this test rather than silently drifting.
	for _, dimension := range []string{"APIKeyScopeChatApps", "APIKeyScopeModels", "APIKeyScopeRoutes"} {
		if !strings.Contains(text, "service."+dimension) {
			t.Errorf("gateway handler no longer consults service.%s", dimension)
		}
	}
}

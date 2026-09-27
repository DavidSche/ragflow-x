package router_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// End-to-end contract tests for the Feishu/DingTalk public callback routes
// (doc/125 §2.4): the routes are unauthenticated by design — signature/token
// verification, replay windows and idempotency protect them instead, and each
// accepted question leaves an im.<channel>.question audit trail.

func newFeishuRouterConfig(encryptKey bool) config.Feishu {
	cfg := config.Feishu{
		Enabled: true, AppID: "cli-router", AppSecret: "app-secret",
		VerificationToken: "router-token", IdentityIssuer: "feishu:router",
		HTTPTimeoutSec: 5, MaxMessageRunes: 2000,
	}
	if encryptKey {
		cfg.EncryptKey = "router-encrypt-key"
	}
	return cfg
}

func dingTalkRouterConfig() config.DingTalk {
	return config.DingTalk{
		Enabled: true, CorpID: "corp-router", AppKey: "router-key", AppSecret: "router-secret",
		IdentityIssuer: "dingtalk:router", HTTPTimeoutSec: 5, MaxMessageRunes: 2000,
	}
}

func seedIMIdentity(t *testing.T, app *testApp, issuer, subject string) {
	t.Helper()
	ctx := context.Background()
	tenant, err := app.svc.CreateTenant(ctx, "IM Router Tenant "+subject)
	if err != nil {
		t.Fatal(err)
	}
	user, err := app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "im-user-" + subject, Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.svc.Store.UpsertOidcIdentity(ctx, &model.OidcIdentity{
		ID: "identity-router-" + subject, Issuer: issuer, Subject: subject, UserID: user.ID,
	}); err != nil {
		t.Fatal(err)
	}
}

func auditActionExists(t *testing.T, app *testApp, tenantID, action string) bool {
	t.Helper()
	audits, err := app.svc.Store.ListAuditsAll(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range audits {
		if entry.Action == action {
			return true
		}
	}
	return false
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// appTenantID resolves the single tenant created by seedIMIdentity so audit
// assertions can scope their lookup.
func appTenantID(t *testing.T, app *testApp) string {
	t.Helper()
	tenants, _, err := app.svc.Store.ListTenants(context.Background(), 1, 50, repository.TenantFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range tenants {
		if strings.HasPrefix(tenant.Name, "IM Router Tenant ") {
			return tenant.ID
		}
	}
	t.Fatal("IM router tenant not found")
	return ""
}

func hmacSHA256(t *testing.T, secret, payload string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// encryptFeishuValueForTest mirrors the Feishu AES envelope (random(16) ||
// 4-byte big-endian length || payload || PKCS7 padding) with the key derived
// as SHA256(EncryptKey); it must stay aligned with decryptFeishuValue.
func encryptFeishuValueForTest(t *testing.T, cfg config.Feishu, payload string) string {
	t.Helper()
	key := sha256.Sum256([]byte(cfg.EncryptKey))
	random := make([]byte, aes.BlockSize)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 0, aes.BlockSize+4+len(payload))
	data = append(data, random...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(payload)))
	data = append(data, length...)
	data = append(data, []byte(payload)...)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	for index := 0; index < padding; index++ {
		data = append(data, byte(padding))
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(encrypted, data)
	return base64.StdEncoding.EncodeToString(encrypted)
}

func io_CopyDiscard(resp *http.Response) (int64, error) {
	defer resp.Body.Close()
	return io.Copy(io.Discard, resp.Body)
}

func TestFeishuCallbackRouterContract(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	seedIMIdentity(t, app, "feishu:router", "ou-router")
	app.svc.SetFeishuConfig(newFeishuRouterConfig(false))

	// Handshake: the challenge is echoed as plain JSON.
	resp := postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback",
		`{"type":"url_verification","challenge":"chk-router","token":"router-token"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("url_verification must echo 200, got %d", resp.StatusCode)
	}
	var echo struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if echo.Challenge != "chk-router" {
		t.Fatalf("challenge not echoed: %+v", echo)
	}

	// Handshake with a wrong token is a 401, not an event ack.
	resp = postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback",
		`{"type":"url_verification","challenge":"chk-2","token":"wrong"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong verification token must 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Malformed bodies are rejected without leaking event semantics.
	resp = postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback", `not-json`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("malformed body must 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A disabled channel fails closed with 404 (route present, channel off).
	app.svc.SetFeishuConfig(config.Feishu{Enabled: false})
	resp = postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback",
		`{"type":"url_verification","challenge":"chk-3","token":"router-token"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled channel must 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	app.svc.SetFeishuConfig(newFeishuRouterConfig(false))

	// A valid text event is acked with code 0; the question runs in the
	// background and the audit trail records the accepted callback.
	now := time.Now().UnixMilli()
	content, _ := json.Marshal(map[string]string{"text": "router event hello"})
	event, _ := json.Marshal(map[string]any{
		"header": map[string]any{
			"event_id": "evt-router-1", "event_type": "im.message.receive_v1",
			"token": "router-token", "create_time": now,
		},
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou-router"}},
		"message": map[string]any{"message_type": "text", "content": string(content)},
	})
	resp = postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback", string(event))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event must ack 200, got %d", resp.StatusCode)
	}
	var ack struct {
		Code int `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ack.Code != 0 {
		t.Fatalf("event ack must carry code 0: %+v", ack)
	}

	// The background execution fails on the mock provider (no chat configured)
	// but the audit trail must still record the callback lifecycle.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if auditActionExists(t, app, appTenantID(t, app), "im.feishu.error") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !auditActionExists(t, app, appTenantID(t, app), "im.feishu.error") {
		t.Fatal("im.feishu.error audit action missing after background execution")
	}
}

func TestFeishuCallbackEncryptedHandshakeRouterContract(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	cfg := newFeishuRouterConfig(true)
	app.svc.SetFeishuConfig(cfg)

	inner := `{"type":"url_verification","challenge":"chk-enc","token":"router-token"}`
	sealed := `{"type":"url_verification","encrypt":"` + encryptFeishuValueForTest(t, cfg, inner) + `"}`
	resp := postJSON(t, app.ts.URL+"/api/v1/im/feishu/callback", sealed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("encrypted handshake must 200, got %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || envelope.Encrypt == "" {
		t.Fatalf("encrypted echo must be an AES envelope: %v", err)
	}
}

func TestDingTalkCallbackRouterContract(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	seedIMIdentity(t, app, "dingtalk:router", "staff-router")
	cfg := dingTalkRouterConfig()
	app.svc.SetDingTalkConfig(cfg)

	sign := func(secret string, ts int64) string {
		mac := hmacSHA256(t, secret, fmt.Sprintf("%d\n%s", ts, secret))
		return mac
	}
	now := time.Now().UnixMilli()

	// Invalid signature is rejected before any parsing.
	body := `{"conversationId":"cid-1","conversationType":"1","senderStaffId":"staff-router","msgtype":"text","text":{"content":"hello"}}`
	resp := postJSON(t, app.ts.URL+fmt.Sprintf("/api/v1/im/dingtalk/callback?timestamp=%d&sign=bad", now), body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad signature must 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A valid text event is acked with code 0. The base64 signature contains
	// '+', '/' and '=' — it must be query-escaped or the server-side
	// verification compares against a corrupted value.
	signature := sign(cfg.AppSecret, now)
	resp = postJSON(t, app.ts.URL+fmt.Sprintf("/api/v1/im/dingtalk/callback?timestamp=%d&sign=%s", now, url.QueryEscape(signature)), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid event must ack 200, got %d", resp.StatusCode)
	}
	var ack struct {
		Code int `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ack.Code != 0 {
		t.Fatalf("event ack must carry code 0: %+v", ack)
	}

	// Non-text messages are rejected before the ack: the 40324 business code
	// is raised via httperr.BadRequest and writeCallbackError passes the
	// mapped HTTP 400 through instead of collapsing it into 401.
	nonText := `{"conversationId":"cid-1","senderStaffId":"staff-router","msgtype":"audio","text":{"content":"x"}}`
	resp = postJSON(t, app.ts.URL+fmt.Sprintf("/api/v1/im/dingtalk/callback?timestamp=%d&sign=%s", now, url.QueryEscape(signature)), nonText)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-text message must 400 (40324 BadRequest), got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Disabled channel fails closed.
	app.svc.SetDingTalkConfig(config.DingTalk{Enabled: false})
	resp = postJSON(t, app.ts.URL+fmt.Sprintf("/api/v1/im/dingtalk/callback?timestamp=%d&sign=%s", now, url.QueryEscape(signature)), body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled channel must 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	app.svc.SetDingTalkConfig(cfg)

	// The accepted event leaves the DingTalk audit trail after the background
	// goroutine settles (execution fails on the mock, error path still audits).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if auditActionExists(t, app, appTenantID(t, app), "im.dingtalk.error") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !auditActionExists(t, app, appTenantID(t, app), "im.dingtalk.error") {
		t.Fatal("im.dingtalk.error audit action missing after background execution")
	}
}

// TestIMCallbackRateLimited exercises the 120/min limiter on the public IM
// callback groups without waiting for 120 real requests: the limiter counts
// every attempt, so 121 plain junk posts (fast 401s) must end with 429. The
// limiter key is "http:"+clientIP — a shared per-IP bucket across routes —
// so the DingTalk callback from the same address is rate limited too.
func TestIMCallbackRateLimited(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	app.svc.SetFeishuConfig(newFeishuRouterConfig(false))
	app.svc.SetDingTalkConfig(dingTalkRouterConfig())

	target := app.ts.URL + "/api/v1/im/feishu/callback"
	var last int
	for i := 0; i < 121; i++ {
		resp := postJSON(t, target, `{"type":"url_verification","challenge":"x","token":"wrong"}`)
		last = resp.StatusCode
		_, _ = io_CopyDiscard(resp)
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("121st request must be rate limited (429), got %d", last)
	}

	// The per-IP bucket is shared across routes, so the DingTalk callback
	// from the same address is limited as well (verified once for coverage).
	resp := postJSON(t, app.ts.URL+"/api/v1/im/dingtalk/callback", `{}`)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("dingtalk callback shares the per-IP bucket and must be 429, got %d", resp.StatusCode)
	}
	_, _ = io_CopyDiscard(resp)
}

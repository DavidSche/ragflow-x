package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newDingTalkConfig() config.DingTalk {
	return config.DingTalk{
		Enabled: true, CorpID: "corp-1", AppKey: "app-key", AppSecret: "app-secret",
		IdentityIssuer: "dingtalk:test", HTTPTimeoutSec: 5, MaxMessageRunes: 2000,
	}
}

func dingTalkSignature(t *testing.T, secret string, timestampMillis int64) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestampMillis, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func dingTalkBody(staffID, conversationID, text string) string {
	return fmt.Sprintf(`{"conversationId":%q,"conversationType":"1","senderStaffId":%q,"senderNick":"nick",`+
		`"sessionWebhook":"","msgtype":"text","text":{"content":%q}}`, conversationID, staffID, text)
}

func TestDingTalkSignatureAndPayload(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newDingTalkConfig()
	now := time.Now().UnixMilli()
	sign := dingTalkSignature(t, cfg.AppSecret, now)
	body := dingTalkBody("staff-1", "cid-1", "采购流程是什么")
	callback, err := svc.DecryptDingTalkCallback(cfg, strconv.FormatInt(now, 10), sign, []byte(body))
	if err != nil || callback.Text.Content != "采购流程是什么" {
		t.Fatalf("callback accepted: %+v err=%v", callback, err)
	}
	// Wrong signature.
	if _, err := svc.DecryptDingTalkCallback(cfg, strconv.FormatInt(now, 10), "bad-sign", []byte(body)); err == nil {
		t.Fatal("wrong signature must fail")
	}
	// Future timestamp beyond the window must be rejected.
	future := now + 15*60_000
	if _, err := svc.DecryptDingTalkCallback(cfg, strconv.FormatInt(future, 10), dingTalkSignature(t, cfg.AppSecret, future), []byte(body)); err == nil {
		t.Fatal("future timestamp must fail")
	}
	// Stale timestamp must be rejected.
	past := now - 15*60_000
	if _, err := svc.DecryptDingTalkCallback(cfg, strconv.FormatInt(past, 10), dingTalkSignature(t, cfg.AppSecret, past), []byte(body)); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	// Non-text message type must be rejected.
	if _, err := svc.DecryptDingTalkCallback(cfg, strconv.FormatInt(now, 10), sign, []byte(`{"senderStaffId":"staff-1","msgtype":"audio","text":{"content":"x"}}`)); err == nil {
		t.Fatal("non-text message must fail")
	}
}

func TestDingTalkIdentityFailsClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newDingTalkConfig()
	ctx := context.Background()
	if _, err := svc.DingTalkIdentity(ctx, cfg, "staff-unknown"); err == nil {
		t.Fatal("unmapped DingTalk identity must fail closed")
	}
	tenant, err := svc.CreateTenant(ctx, "DingTalk Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "dingtalk-user", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertOidcIdentity(ctx, &model.OidcIdentity{
		ID: "identity-dingtalk-1", Issuer: "dingtalk:test", Subject: "staff-1", UserID: user.ID,
	}); err != nil {
		t.Fatal(err)
	}
	mapped, err := svc.DingTalkIdentity(ctx, cfg, "staff-1")
	if err != nil || mapped.ID != user.ID {
		t.Fatalf("mapped DingTalk identity: %v %v", mapped, err)
	}
}

func TestDingTalkReplyPrefersSessionWebhook(t *testing.T) {
	svc := newAuthzSvc(t)
	// The shared outbound client enforces the SSRF dialer policy; allow
	// loopback so the httptest webhook server is reachable (same as the
	// WeCom adapter tests).
	svc.SetProviderURLPolicy(true)
	cfg := newDingTalkConfig()
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "robot/send") {
			t.Errorf("reply must use sessionWebhook, got robot/send")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		_ = r.ParseForm()
		received = map[string]any{"path": r.URL.Path}
	}))
	defer server.Close()
	cfg.APIBaseURL = server.URL
	err := svc.SendDingTalkMarkdown(context.Background(), cfg, server.URL+"/webhook", "RAGFlow-X", "answer body")
	if err != nil {
		t.Fatalf("sessionWebhook reply: %v", err)
	}
	if received == nil {
		t.Fatal("webhook was not called")
	}
}

func TestDingTalkDisabledFailsClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newDingTalkConfig()
	cfg.Enabled = false
	if _, err := svc.DecryptDingTalkCallback(cfg, "0", "sign", []byte("{}")); err == nil {
		t.Fatal("disabled DingTalk callback must fail closed")
	}
}

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newWeComConfig(t *testing.T, baseURL string) config.WeCom {
	t.Helper()
	key := []byte(strings.Repeat("a", 32))
	return config.WeCom{
		Enabled: true, CorpID: "corp-1", AgentID: 1000001, Token: "callback-token",
		EncodingAESKey: base64.StdEncoding.EncodeToString(key)[:43],
		AppSecret:      "app-secret", TenantID: "", IdentityIssuer: "wecom:test",
		HTTPTimeoutSec: 2, MaxMessageRunes: 100, APIBaseURL: baseURL,
	}
}

func TestWeComCallbackSignatureReplayAndResponse(t *testing.T) {
	cfg := newWeComConfig(t, "")
	svc := New(nil, nil, nil, "test-key")
	svc.SetWeComConfig(cfg)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "nonce-1"
	plain := fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[user-1]]></FromUserName><MsgId>1001</MsgId><MsgType>text</MsgType><Content><![CDATA[hello]]></Content></xml>`, cfg.CorpID)
	encrypted, signature, err := encryptWeComValue(cfg, timestamp, nonce, plain, cfg.CorpID)
	if err != nil {
		t.Fatalf("encrypt callback: %v", err)
	}
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, encrypted)
	callback, err := svc.DecryptWeComCallback(cfg, timestamp, nonce, signature, body)
	if err != nil {
		t.Fatalf("decrypt callback: %v", err)
	}
	if callback.MsgType != "text" || callback.Content != "hello" || callback.MsgID != 1001 || callback.CorpID != cfg.CorpID {
		t.Fatalf("unexpected callback: %+v", callback)
	}
	if _, err := svc.DecryptWeComCallback(cfg, timestamp, nonce, signature, body); err == nil {
		t.Fatal("replayed callback must be rejected")
	}
	if _, err := svc.DecryptWeComCallback(cfg, timestamp, nonce, "bad-signature", body); err == nil {
		t.Fatal("invalid signature must be rejected")
	}

	response, err := svc.WeComResponseXML(cfg, timestamp, nonce, "ack")
	if err != nil {
		t.Fatalf("response xml: %v", err)
	}
	var envelope wecomEnvelope
	if err := xmlUnmarshalString(response, &envelope); err != nil {
		t.Fatalf("invalid response envelope: %v %s", err, response)
	}
	decrypted, err := decryptWeComValue(cfg, envelope.Encrypt, cfg.CorpID)
	if err != nil || string(decrypted) != "ack" {
		t.Fatalf("unexpected response: %s decrypted=%q err=%v", response, string(decrypted), err)
	}
}

func xmlUnmarshalString(value string, target any) error {
	return xml.Unmarshal([]byte(value), target)
}

func TestWeComIdentityFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	cfg := newWeComConfig(t, "")
	if _, err := svc.wecomIdentity(ctx, cfg, "unknown-user"); err == nil {
		t.Fatal("unmapped WeCom identity must fail closed")
	}
	tenant, err := svc.CreateTenant(ctx, "WeCom Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "wecom-user", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := &model.OidcIdentity{
		ID: "identity-1", Issuer: cfg.IdentityIssuer, Subject: "wecom-user",
		UserID: user.ID, LastLoginAt: time.Now().UTC(),
	}
	if err := svc.Store.UpsertOidcIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	cfg.TenantID = "other-tenant"
	if _, err := svc.wecomIdentity(ctx, cfg, "wecom-user"); err == nil {
		t.Fatal("tenant binding mismatch must fail closed")
	}
	cfg.TenantID = tenant.ID
	mapped, err := svc.wecomIdentity(ctx, cfg, "wecom-user")
	if err != nil || mapped.ID != user.ID || mapped.TenantID != tenant.ID {
		t.Fatalf("identity mapping failed: user=%+v err=%v", mapped, err)
	}

	viewer, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "wecom-viewer", Password: "secret123", Role: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertOidcIdentity(ctx, &model.OidcIdentity{
		ID: "identity-viewer", Issuer: cfg.IdentityIssuer, Subject: "wecom-viewer",
		UserID: viewer.ID, LastLoginAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ExecuteWeComQuestion(ctx, &WeComMessageTarget{}, "viewer question", "wecom-viewer-request", "wecom-viewer"); err == nil {
		t.Fatal("viewer must not execute IM chat")
	}
}

func TestWeComCompletionSummaryExtractsCitations(t *testing.T) {
	completion := map[string]any{
		"answer": "答案",
		"choices": []any{
			map[string]any{"message": map[string]any{"content": "fallback"}},
		},
		"references": []any{
			map[string]any{"document_name": "Policy A"},
			map[string]any{"doc_name": "Policy B"},
		},
	}
	answer, citations := wecomCompletionSummary(completion)
	if citations != 2 || !strings.Contains(answer, "答案") || !strings.Contains(answer, "Policy A") || !strings.Contains(answer, "Policy B") {
		t.Fatalf("unexpected summary: citations=%d answer=%q", citations, answer)
	}
}

func TestWeComRoutingInputStripsControlCommands(t *testing.T) {
	chatID, sessionID, question := wecomRoutingInput("use assistant-1 什么是知识库？", "default-chat")
	if chatID != "assistant-1" || sessionID != "" || question != "什么是知识库？" {
		t.Fatalf("unexpected use routing: chat=%q session=%q question=%q", chatID, sessionID, question)
	}
	chatID, sessionID, question = wecomRoutingInput("session s-1 继续这个问题", "default-chat")
	if chatID != "default-chat" || sessionID != "s-1" || question != "继续这个问题" {
		t.Fatalf("unexpected session routing: chat=%q session=%q question=%q", chatID, sessionID, question)
	}
	chatID, sessionID, question = wecomRoutingInput("普通问题", "default-chat")
	if chatID != "default-chat" || sessionID != "" || question != "普通问题" {
		t.Fatalf("unexpected plain routing: chat=%q session=%q question=%q", chatID, sessionID, question)
	}
}

func TestExecuteWeComQuestionUsesGatewayAndSendsCitationSummary(t *testing.T) {
	var sendText string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/cgi-bin/gettoken"):
			_ = json.NewEncoder(w).Encode(wecomTokenResponse{AccessToken: "token", ExpiresIn: 7200})
		case strings.Contains(r.URL.Path, "/cgi-bin/message/send"):
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			text, _ := payload["text"].(map[string]any)
			sendText, _ = text["content"].(string)
			_ = json.NewEncoder(w).Encode(wecomSendResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "wecom.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	svc := New(store, ragflow.NewMock(), jwt.NewManager("secret", 24), "test-key")
	svc.SetProviderURLPolicy(true)
	t.Cleanup(func() { _ = svc.Store.Close() })
	tenant, err := svc.CreateTenant(ctx, "WeCom Chat Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "wecom-chat-user", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "WeCom Assistant", []string{"dataset-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertOidcIdentity(ctx, &model.OidcIdentity{
		ID: "identity-chat", Issuer: "wecom:test", Subject: "wecom-chat-user",
		UserID: user.ID, LastLoginAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := newWeComConfig(t, apiServer.URL)
	cfg.TenantID = tenant.ID
	cfg.DefaultChatID = chat.ID
	svc.SetWeComConfig(cfg)

	target := &WeComMessageTarget{}
	requestID := "wecom-request-1"
	if err := svc.ExecuteWeComQuestion(ctx, target, "什么是报销流程？", requestID, "wecom-chat-user"); err != nil {
		t.Fatalf("execute WeCom question: %v", err)
	}
	if target.UserID != user.ID || target.TenantID != tenant.ID || target.ChatID != chat.ID {
		t.Fatalf("unexpected target: %+v", target)
	}
	if target.SessionID == "" || !strings.Contains(sendText, "Trace: "+requestID) ||
		!strings.Contains(sendText, "Session: "+target.SessionID) || !strings.Contains(sendText, "Message:") {
		t.Fatalf("response lacked trace metadata: target=%+v message=%q", target, sendText)
	}
}

// TestWeComCallbackRejectsFutureTimestamp covers doc/118 F-07: a callback
// signed with a timestamp in the future (negative replay offset) must be
// rejected just like one from the past.
func TestWeComCallbackRejectsFutureTimestamp(t *testing.T) {
	cfg := newWeComConfig(t, "")
	svc := New(nil, nil, nil, "test-key")
	svc.SetWeComConfig(cfg)
	timestamp := fmt.Sprintf("%d", time.Now().Add(20*time.Minute).Unix())
	nonce := "nonce-future"
	plain := fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[user-1]]></FromUserName><MsgId>2002</MsgId><MsgType>text</MsgType><Content><![CDATA[hello]]></Content></xml>`, cfg.CorpID)
	encrypted, signature, err := encryptWeComValue(cfg, timestamp, nonce, plain, cfg.CorpID)
	if err != nil {
		t.Fatalf("encrypt callback: %v", err)
	}
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, encrypted)
	if _, err := svc.DecryptWeComCallback(cfg, timestamp, nonce, signature, body); err == nil {
		t.Fatal("future-dated callback must be rejected")
	}
}

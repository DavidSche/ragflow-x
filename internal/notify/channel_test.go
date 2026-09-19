package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestWeComNotifierSendsMarkdownRobotPayload(t *testing.T) {
	var signature string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature = r.Header.Get("X-RagflowX-Signature")
		if err := decodeJSON(r, &body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	t.Cleanup(server.Close)

	notifier := NewChannel(config.Webhook{Name: "ops-wecom", Type: "wecom", URL: server.URL})
	err := notifier.Send(context.Background(), Event{
		ID: "alert-wecom", Title: "release failed", Severity: "critical",
		Type: "release.failed", TenantID: "tenant-1", Detail: "gate denied",
	})
	if err != nil {
		t.Fatalf("send WeCom: %v", err)
	}
	if signature != "" {
		t.Fatalf("WeCom must not use internal HMAC headers, got %q", signature)
	}
	if body["msgtype"] != "markdown" {
		t.Fatalf("WeCom msgtype = %v, want markdown", body["msgtype"])
	}
	markdown, ok := body["markdown"].(map[string]any)
	if !ok {
		t.Fatalf("WeCom markdown missing: %v", body["markdown"])
	}
	content, _ := markdown["content"].(string)
	for _, expected := range []string{"**release failed**", "**Severity:** critical", "release.failed", "gate denied"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("WeCom content missing %q: %q", expected, content)
		}
	}
}

func TestWeComNotifierRejectsRobotBusinessError(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 93000, "errmsg": "invalid webhook url"})
	}))
	t.Cleanup(server.Close)

	notifier := NewChannel(config.Webhook{Name: "ops-wecom", Type: "wecom", URL: server.URL}, WithRetryPolicy(3, 0, 0))
	err := notifier.Send(context.Background(), Event{ID: "alert-1"})
	if err == nil || attempts != 1 {
		t.Fatalf("WeCom business error must be terminal: err=%v attempts=%d", err, attempts)
	}
}

func TestDingTalkNotifierSignsMarkdownRobotPayload(t *testing.T) {
	var requestURL *url.URL
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL
		if err := decodeJSON(r, &body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	t.Cleanup(server.Close)
	secret := "dingtalk-secret"

	notifier := NewChannel(config.Webhook{Name: "ops-dingtalk", Type: "dingtalk", URL: server.URL, Secret: secret})
	err := notifier.Send(context.Background(), Event{
		ID: "alert-dingtalk", Title: "approval overdue", Severity: "warn",
		Type: "approval.overdue", TenantID: "tenant-2", Resource: "approval", Detail: "approver away",
	})
	if err != nil {
		t.Fatalf("send DingTalk: %v", err)
	}
	timestamp := requestURL.Query().Get("timestamp")
	sign := requestURL.Query().Get("sign")
	if timestamp == "" || sign == "" {
		t.Fatalf("DingTalk signing query is missing: %s", requestURL)
	}
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if sign != want {
		t.Fatalf("DingTalk signature = %q, want %q", sign, want)
	}
	if body["msgtype"] != "markdown" {
		t.Fatalf("DingTalk msgtype = %v, want markdown", body["msgtype"])
	}
	payload, ok := body["markdown"].(map[string]any)
	if !ok {
		t.Fatalf("DingTalk markdown missing: %v", body["markdown"])
	}
	if payload["title"] != "approval overdue" {
		t.Fatalf("DingTalk title = %v", payload["title"])
	}
	text, _ := payload["text"].(string)
	for _, expected := range []string{"**approval overdue**", "**Severity:** warn", "approval.overdue", "approver away"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("DingTalk text missing %q: %q", expected, text)
		}
	}
}

func TestHubBuildsEnterpriseChannelFromWebhookConfig(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "" {
			t.Errorf("unexpected query: %s", r.URL)
		}
		if err := decodeJSON(r, &body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0})
	}))
	t.Cleanup(server.Close)

	hub := NewHub(config.Alerting{
		Enabled: true,
		Webhooks: []config.Webhook{{
			Name: "ops", Type: "wecom", URL: server.URL, Enabled: true,
		}},
	}, config.Approval{})
	hub.Notify(context.Background(), Event{ID: "hub-wecom", Title: "gateway quota", Severity: "error"})
	hub.Shutdown()
	if body["msgtype"] != "markdown" {
		t.Fatalf("Hub WeCom payload = %v", body)
	}
}

func TestChannelNormalizesUnknownTypeToGenericWebhook(t *testing.T) {
	notifier := NewChannel(config.Webhook{Name: "legacy", Type: "slack", URL: "https://hooks.example.test"})
	webhook, ok := notifier.(*WebhookNotifier)
	if !ok || webhook.format != webhookFormatGeneric {
		t.Fatalf("unknown webhook type must use generic format: %#v", notifier)
	}
}

func TestChannelOptionsPreserveRetryPolicy(t *testing.T) {
	for _, cfg := range []config.Webhook{
		{Name: "wecom", Type: "wecom"},
		{Name: "dingtalk", Type: "dingtalk"},
	} {
		notifier := NewChannel(cfg, WithRetryPolicy(2, 3*time.Millisecond, 4*time.Millisecond))
		webhook, ok := notifier.(*WebhookNotifier)
		if !ok || webhook.maxAttempts != 2 || webhook.initialBackoff != 3*time.Millisecond {
			t.Fatalf("channel %s retry policy = %+v", cfg.Type, webhook)
		}
	}
}

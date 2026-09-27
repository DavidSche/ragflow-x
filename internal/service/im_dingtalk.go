package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// DingTalk adapter (doc/125 §2.3). The enterprise robot callback is verified
// with timestamp + HMAC-SHA256(appSecret, timestamp + "\n" + appSecret);
// replies go to the caller-supplied sessionWebhook when present and fall back
// to the robot/send API otherwise.

type dingTalkCallback struct {
	ConversationID   string `json:"conversationId"`
	ConversationType string `json:"conversationType"`
	SenderStaffID    string `json:"senderStaffId"`
	SenderNick       string `json:"senderNick"`
	SessionWebhook   string `json:"sessionWebhook"`
	MsgType          string `json:"msgtype"`
	Text             struct {
		Content string `json:"content"`
	} `json:"text"`
}

type dingTalkTokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (s *Service) SetDingTalkConfig(cfg config.DingTalk) {
	cfg = normalizeDingTalkConfig(cfg)
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	s.dingTalkConfig = cfg
}

func (s *Service) CurrentDingTalkConfig() config.DingTalk {
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	return s.dingTalkConfig
}

func normalizeDingTalkConfig(cfg config.DingTalk) config.DingTalk {
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = 10
	}
	if cfg.MaxMessageRunes <= 0 {
		cfg.MaxMessageRunes = 2000
	}
	return cfg
}

func dingTalkConfigured(cfg config.DingTalk) error {
	switch {
	case !cfg.Enabled:
		return httperr.NotFound("DingTalk callback is disabled")
	case strings.TrimSpace(cfg.AppKey) == "" || strings.TrimSpace(cfg.AppSecret) == "":
		return httperr.New(503, 50324, "DingTalk callback is not configured")
	default:
		return nil
	}
}

func dingTalkAPIBaseURL(cfg config.DingTalk) string {
	value := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if value == "" {
		return "https://oapi.dingtalk.com"
	}
	return value
}

// verifyDingTalkSignature checks the inbound timestamp + sign header pair.
// Timestamps outside the replay window are rejected in both directions, so a
// crafted future timestamp cannot extend a replay (doc/118 F-07 parity).
func verifyDingTalkSignature(cfg config.DingTalk, timestamp, sign string) error {
	if strings.TrimSpace(sign) == "" {
		return httperr.Unauthorized("invalid DingTalk callback signature")
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return httperr.Unauthorized("invalid DingTalk callback signature")
	}
	now := time.Now()
	callbackTime := time.UnixMilli(ts)
	if now.Sub(callbackTime) > wecomReplayWindow || callbackTime.After(now.Add(wecomReplayWindow)) {
		return httperr.Unauthorized("DingTalk callback timestamp expired")
	}
	mac := hmac.New(sha256.New, []byte(cfg.AppSecret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + cfg.AppSecret))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sign)) {
		return httperr.Unauthorized("invalid DingTalk callback signature")
	}
	return nil
}

// DecryptDingTalkCallback validates the signature and normalizes the text
// payload; identity resolution stays in PrepareDingTalkCallback.
func (s *Service) DecryptDingTalkCallback(cfg config.DingTalk, timestamp, sign string, rawBody []byte) (*dingTalkCallback, error) {
	if err := dingTalkConfigured(cfg); err != nil {
		return nil, err
	}
	if len(rawBody) == 0 || len(rawBody) > 1<<20 {
		return nil, httperr.Unauthorized("invalid DingTalk callback body")
	}
	if err := verifyDingTalkSignature(cfg, timestamp, sign); err != nil {
		return nil, err
	}
	var callback dingTalkCallback
	if err := json.Unmarshal(rawBody, &callback); err != nil || strings.TrimSpace(callback.SenderStaffID) == "" {
		return nil, httperr.Unauthorized("invalid DingTalk callback message")
	}
	if callback.MsgType != "text" {
		return nil, httperr.BadRequest(40324, "unsupported DingTalk message type")
	}
	text := strings.TrimSpace(callback.Text.Content)
	if text == "" {
		return nil, httperr.Unauthorized("invalid DingTalk callback message")
	}
	if len([]rune(text)) > cfg.MaxMessageRunes {
		return nil, httperr.BadRequest(40320, "DingTalk message is too long")
	}
	callback.Text.Content = text
	return &callback, nil
}

// DingTalkIdentity resolves the staff id through the pre-provisioned mapping
// (issuer dingtalk:corp:<CorpID> by default); unknown subjects fail closed.
func (s *Service) DingTalkIdentity(ctx context.Context, cfg config.DingTalk, staffID string) (*model.User, error) {
	issuer := strings.TrimSpace(cfg.IdentityIssuer)
	if issuer == "" {
		issuer = "dingtalk:corp:" + cfg.CorpID
	}
	return s.imIdentity(ctx, issuer, staffID, cfg.TenantID)
}

// PrepareDingTalkCallback persists the dedupe record and resolves the sender.
// DingTalk callbacks carry no global event id, so the idempotency key is
// derived from the sender, conversation, second-granularity timestamp and
// content fingerprint (doc/125 §2.3).
func (s *Service) PrepareDingTalkCallback(ctx context.Context, cfg config.DingTalk, timestamp string, callback *dingTalkCallback) (*WeComCallbackContext, error) {
	user, err := s.DingTalkIdentity(ctx, cfg, callback.SenderStaffID)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256([]byte(strings.TrimSpace(timestamp) + "|" + callback.SenderStaffID + "|" +
		callback.ConversationID + "|" + callback.Text.Content))
	idempotencyKey := "dingtalk:" + cfg.CorpID + ":" + hexEncode(fingerprint[:])[:48]
	record := &model.GatewayIdempotency{
		ID: id.New(), TenantID: user.TenantID, PrincipalID: user.ID,
		IdempotencyKey: idempotencyKey, RequestFingerprint: hexEncode(fingerprint[:]),
		Status: model.IdempotencyInProgress, RequestID: id.New(),
		ExpiresAt: time.Now().Add(wecomReplayWindow).UTC(),
	}
	created, err := s.Store.CreateGatewayIdempotency(ctx, record)
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, httperr.New(409, 40924, "duplicate DingTalk callback")
	}
	return &WeComCallbackContext{User: user, Idempotency: record}, nil
}

// SendDingTalkMarkdown replies on the sessionWebhook when the platform
// supplies one, otherwise through the robot/send API with an access token.
func (s *Service) SendDingTalkMarkdown(ctx context.Context, cfg config.DingTalk, sessionWebhook, title, message string) error {
	if err := dingTalkConfigured(cfg); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]any{
			"title": title,
			"text":  message,
		},
	})
	if err != nil {
		return err
	}
	target := strings.TrimSpace(sessionWebhook)
	if target == "" {
		token, err := s.dingTalkAccessToken(ctx, cfg)
		if err != nil {
			return err
		}
		target = fmt.Sprintf("%s/robot/send?access_token=%s", dingTalkAPIBaseURL(cfg), url.QueryEscape(token))
	}
	return s.dingTalkPost(ctx, target, payload)
}

func (s *Service) dingTalkAccessToken(ctx context.Context, cfg config.DingTalk) (string, error) {
	s.wecomMu.Lock()
	if s.dingTalkToken != "" && time.Now().Before(s.dingTalkTokenExpiresAt) {
		token := s.dingTalkToken
		s.wecomMu.Unlock()
		return token, nil
	}
	s.wecomMu.Unlock()
	target := fmt.Sprintf("%s/gettoken?appkey=%s&appsecret=%s",
		dingTalkAPIBaseURL(cfg), url.QueryEscape(cfg.AppKey), url.QueryEscape(cfg.AppSecret))
	resp, err := s.httpTimeoutClient().Get(target)
	if err != nil {
		return "", httperr.New(502, 50244, "DingTalk authorization failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", httperr.New(502, 50244, "DingTalk authorization failed")
	}
	var result dingTalkTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", httperr.New(502, 50244, "DingTalk authorization failed")
	}
	if result.ErrCode != 0 || result.AccessToken == "" {
		return "", httperr.New(502, 50244, "DingTalk authorization failed")
	}
	expiresIn := result.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	s.wecomMu.Lock()
	s.dingTalkToken = result.AccessToken
	s.dingTalkTokenExpiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - 2*time.Minute)
	s.wecomMu.Unlock()
	return result.AccessToken, nil
}

func (s *Service) dingTalkPost(ctx context.Context, target string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return httperr.New(502, 50244, "DingTalk send failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpTimeoutClient().Do(req)
	if err != nil {
		return httperr.New(502, 50244, "DingTalk send failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return httperr.New(502, 50244, "DingTalk send failed")
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return httperr.New(502, 50244, "DingTalk send failed")
	}
	if result.ErrCode != 0 {
		return httperr.New(502, 50244, "DingTalk send failed")
	}
	return nil
}

// ExecuteDingTalkQuestion runs the shared IM question pipeline and replies
// through DingTalk. The reply contract matches the WeCom adapter.
func (s *Service) ExecuteDingTalkQuestion(ctx context.Context, cfg config.DingTalk, target *WeComMessageTarget, question, requestID, staffID, sessionWebhook string) error {
	answer, err := s.executeIMQuestion(ctx, cfg.DefaultChatID, target, question, requestID, func(ctx context.Context) (*model.User, error) {
		return s.DingTalkIdentity(ctx, cfg, staffID)
	})
	if err != nil {
		return err
	}
	return s.SendDingTalkMarkdown(ctx, cfg, sessionWebhook, "RAGFlow-X", answer)
}

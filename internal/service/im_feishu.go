package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// Feishu adapter (doc/125 §2.3). The event subscription is verified with the
// app VerificationToken; encrypted payloads use AES-256-CBC (key =
// SHA256(EncryptKey), IV = first 16 key bytes). Only im.message.receive_v1
// text messages are answered; everything else is acknowledged as an event.

type feishuURLVerification struct {
	Encrypt   string `json:"encrypt"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Type      string `json:"type"`
}

type feishuEventHeader struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Token     string `json:"token"`
	CreatedAt int64  `json:"create_time"`
}

type feishuSenderID struct {
	OpenID  string `json:"open_id"`
	UserID  string `json:"user_id"`
	UnionID string `json:"union_id"`
}

type feishuMessageBody struct {
	ChatID      string `json:"chat_id"`
	MessageID   string `json:"message_id"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
}

type feishuEvent struct {
	Header feishuEventHeader `json:"header"`
	Sender struct {
		SenderID feishuSenderID `json:"sender_id"`
	} `json:"sender"`
	Message feishuMessageBody `json:"message"`
}

func (s *Service) SetFeishuConfig(cfg config.Feishu) {
	cfg = normalizeFeishuConfig(cfg)
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	s.feishuConfig = cfg
}

func (s *Service) CurrentFeishuConfig() config.Feishu {
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	return s.feishuConfig
}

func normalizeFeishuConfig(cfg config.Feishu) config.Feishu {
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = 10
	}
	if cfg.MaxMessageRunes <= 0 {
		cfg.MaxMessageRunes = 2000
	}
	return cfg
}

func feishuConfigured(cfg config.Feishu) error {
	switch {
	case !cfg.Enabled:
		return httperr.NotFound("Feishu callback is disabled")
	case strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.VerificationToken) == "":
		return httperr.New(503, 50322, "Feishu callback is not configured")
	default:
		return nil
	}
}

func feishuAPIBaseURL(cfg config.Feishu) string {
	value := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if value == "" {
		return "https://open.feishu.cn"
	}
	return value
}

// decryptFeishuValue decrypts an AES-256-CBC event payload and verifies its
// PKCS7 padding. The plaintext envelope is random(16) || 4-byte big-endian
// payload length || payload, matching encryptFeishuValue and the official
// Feishu SDK format.
func decryptFeishuValue(cfg config.Feishu, encrypted string) ([]byte, error) {
	if strings.TrimSpace(cfg.EncryptKey) == "" {
		return nil, httperr.Unauthorized("Feishu callback is not configured for encryption")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, httperr.Unauthorized("invalid Feishu callback payload")
	}
	key := sha256.Sum256([]byte(cfg.EncryptKey))
	plaintext := make([]byte, len(ciphertext))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, httperr.Unauthorized("invalid Feishu callback credentials")
	}
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plaintext, ciphertext)
	unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil || len(unpadded) < 20 {
		return nil, httperr.Unauthorized("invalid Feishu callback payload")
	}
	length := int(binary.BigEndian.Uint32(unpadded[16:20]))
	if length < 0 || length > len(unpadded)-20 {
		return nil, httperr.Unauthorized("invalid Feishu callback payload")
	}
	return unpadded[20 : 20+length], nil
}

// encryptFeishuValue produces the encrypted response payload required when
// the subscription is configured with an EncryptKey. The envelope matches the
// decrypt side: random(16) || 4-byte big-endian payload length || payload.
func encryptFeishuValue(cfg config.Feishu, plaintext string) (string, error) {
	if strings.TrimSpace(cfg.EncryptKey) == "" {
		return "", httperr.Unauthorized("Feishu callback is not configured for encryption")
	}
	key := sha256.Sum256([]byte(cfg.EncryptKey))
	random := make([]byte, aes.BlockSize)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	data := make([]byte, 0, aes.BlockSize+4+len(plaintext))
	data = append(data, random...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(plaintext)))
	data = append(data, length...)
	data = append(data, []byte(plaintext)...)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	for index := 0; index < padding; index++ {
		data = append(data, byte(padding))
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", httperr.Unauthorized("invalid Feishu callback credentials")
	}
	encrypted := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(encrypted, data)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// DecryptFeishuURLVerification validates the subscription handshake and
// returns the reply body: the challenge is echoed as {"challenge": ...} JSON
// in plain mode and as an AES envelope in encrypted mode (doc/125 §2.3).
func (s *Service) DecryptFeishuURLVerification(cfg config.Feishu, rawBody []byte) (string, error) {
	if err := feishuConfigured(cfg); err != nil {
		return "", err
	}
	verification, err := decodeFeishuVerification(cfg, rawBody)
	if err != nil {
		return "", err
	}
	if verification.Token != cfg.VerificationToken {
		return "", httperr.Unauthorized("invalid Feishu verification token")
	}
	challenge := strings.TrimSpace(verification.Challenge)
	if challenge == "" {
		return "", httperr.Unauthorized("invalid Feishu verification challenge")
	}
	reply, err := feishuReplyEnvelope(cfg, challenge)
	if err != nil {
		return "", err
	}
	return string(reply), nil
}

func decodeFeishuVerification(cfg config.Feishu, rawBody []byte) (*feishuURLVerification, error) {
	if len(rawBody) == 0 || len(rawBody) > 1<<20 {
		return nil, httperr.Unauthorized("invalid Feishu callback body")
	}
	var verification feishuURLVerification
	if err := json.Unmarshal(rawBody, &verification); err != nil {
		return nil, httperr.Unauthorized("invalid Feishu callback body")
	}
	if verification.Type != "url_verification" {
		return nil, httperr.Unauthorized("invalid Feishu verification type")
	}
	if strings.TrimSpace(cfg.EncryptKey) != "" {
		plaintext, err := decryptFeishuValue(cfg, verification.Encrypt)
		if err != nil {
			return nil, err
		}
		var inner feishuURLVerification
		if err := json.Unmarshal(plaintext, &inner); err != nil || inner.Type != "url_verification" {
			return nil, httperr.Unauthorized("invalid Feishu callback payload")
		}
		return &inner, nil
	}
	return &verification, nil
}

// DecryptFeishuEvent verifies the encrypted/signed event, enforces the replay
// window in both directions, dedupes on event_id and returns only the bounded
// fields the adapter needs (doc/125 §2.3).
func (s *Service) DecryptFeishuEvent(cfg config.Feishu, rawBody []byte) (*feishuEvent, error) {
	if err := feishuConfigured(cfg); err != nil {
		return nil, err
	}
	payload := rawBody
	if len(payload) == 0 || len(payload) > 1<<20 {
		return nil, httperr.Unauthorized("invalid Feishu callback body")
	}
	if strings.TrimSpace(cfg.EncryptKey) != "" {
		var sealed struct {
			Encrypt string `json:"encrypt"`
		}
		if err := json.Unmarshal(payload, &sealed); err != nil || strings.TrimSpace(sealed.Encrypt) == "" {
			return nil, httperr.Unauthorized("invalid Feishu callback body")
		}
		plaintext, err := decryptFeishuValue(cfg, sealed.Encrypt)
		if err != nil {
			return nil, err
		}
		payload = plaintext
	}
	var event feishuEvent
	if err := json.Unmarshal(payload, &event); err != nil || strings.TrimSpace(event.Header.EventID) == "" {
		return nil, httperr.Unauthorized("invalid Feishu callback message")
	}
	if strings.TrimSpace(event.Header.Token) != strings.TrimSpace(cfg.VerificationToken) {
		return nil, httperr.Unauthorized("invalid Feishu verification token")
	}
	if event.Header.EventType != "im.message.receive_v1" {
		return nil, httperr.BadRequest(40322, "unsupported Feishu event type")
	}
	now := time.Now()
	eventTime := time.UnixMilli(event.Header.CreatedAt)
	if event.Header.CreatedAt <= 0 || now.Sub(eventTime) > wecomReplayWindow || eventTime.After(now.Add(wecomReplayWindow)) {
		return nil, httperr.Unauthorized("Feishu callback timestamp expired")
	}
	if !s.rememberCallback("feishu:" + cfg.AppID + ":" + event.Header.EventID) {
		return nil, httperr.New(409, 40922, "duplicate Feishu callback")
	}
	if event.Message.MessageType != "" && event.Message.MessageType != "text" {
		return nil, httperr.BadRequest(40323, "unsupported Feishu message type")
	}
	text, err := feishuMessageText(event.Message.Content)
	if err != nil {
		return nil, err
	}
	if len([]rune(text)) > cfg.MaxMessageRunes {
		return nil, httperr.BadRequest(40320, "Feishu message is too long")
	}
	event.Message.Content = text
	return &event, nil
}

// feishuMessageText extracts the text field from the message content JSON.
func feishuMessageText(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", httperr.Unauthorized("invalid Feishu callback message")
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", httperr.Unauthorized("invalid Feishu callback message")
	}
	if strings.TrimSpace(payload.Text) == "" {
		return "", httperr.Unauthorized("invalid Feishu callback message")
	}
	return payload.Text, nil
}

// FeishuIdentity resolves the sender through the pre-provisioned mapping
// (issuer feishu:app:<AppID> by default); unknown subjects fail closed.
func (s *Service) FeishuIdentity(ctx context.Context, cfg config.Feishu, openID string) (*model.User, error) {
	issuer := strings.TrimSpace(cfg.IdentityIssuer)
	if issuer == "" {
		issuer = "feishu:app:" + cfg.AppID
	}
	return s.imIdentity(ctx, issuer, openID, cfg.TenantID)
}

// PrepareFeishuCallback persists the dedupe record and resolves the sender.
func (s *Service) PrepareFeishuCallback(ctx context.Context, cfg config.Feishu, event *feishuEvent) (*WeComCallbackContext, error) {
	subject := event.Sender.SenderID.OpenID
	if subject == "" {
		subject = event.Sender.SenderID.UserID
	}
	if subject == "" {
		subject = event.Sender.SenderID.UnionID
	}
	user, err := s.FeishuIdentity(ctx, cfg, subject)
	if err != nil {
		return nil, err
	}
	idempotencyKey := "feishu:" + cfg.AppID + ":" + event.Header.EventID
	if len(idempotencyKey) > 128 {
		idempotencyKey = idempotencyKey[:128]
	}
	fingerprint := sha256.Sum256([]byte(event.Header.EventID + "\n" + event.Message.Content))
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
		return nil, httperr.New(409, 40922, "duplicate Feishu callback")
	}
	return &WeComCallbackContext{User: user, Idempotency: record}, nil
}

// SendFeishuText replies via the im/v1/messages API using a
// tenant_access_token cached in-process (doc/125 §2.3).
func (s *Service) SendFeishuText(ctx context.Context, cfg config.Feishu, openID, message string) error {
	if err := feishuConfigured(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		return httperr.New(503, 50322, "Feishu callback is not configured")
	}
	token, err := s.feishuTenantAccessToken(ctx, cfg)
	if err != nil {
		return err
	}
	content, err := json.Marshal(map[string]string{"text": message})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"receive_id": openID,
		"msg_type":   "text",
		"content":    string(content),
	})
	if err != nil {
		return err
	}
	target := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=open_id", feishuAPIBaseURL(cfg))
	body, err := s.feishuPostRaw(ctx, target, token, payload)
	if err != nil {
		return err
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &result); err == nil && result.Code != 0 {
		return httperr.New(502, 50242, "Feishu send failed")
	}
	return nil
}

func (s *Service) feishuTenantAccessToken(ctx context.Context, cfg config.Feishu) (string, error) {
	s.wecomMu.Lock()
	if s.feishuToken != "" && time.Now().Before(s.feishuTokenExpiresAt) {
		token := s.feishuToken
		s.wecomMu.Unlock()
		return token, nil
	}
	s.wecomMu.Unlock()
	payload, err := json.Marshal(map[string]any{
		"app_id": cfg.AppID, "app_secret": cfg.AppSecret,
	})
	if err != nil {
		return "", err
	}
	body, err := s.feishuPostRaw(ctx, feishuAPIBaseURL(cfg)+"/open-apis/auth/v3/tenant_access_token/internal", "", payload)
	if err != nil {
		return "", httperr.New(502, 50242, "Feishu authorization failed")
	}
	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code != 0 || result.TenantAccessToken == "" {
		return "", httperr.New(502, 50242, "Feishu authorization failed")
	}
	expiresIn := result.Expire
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	s.wecomMu.Lock()
	s.feishuToken = result.TenantAccessToken
	s.feishuTokenExpiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - 2*time.Minute)
	s.wecomMu.Unlock()
	return result.TenantAccessToken, nil
}

func (s *Service) feishuPostRaw(ctx context.Context, target, token string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, httperr.New(502, 50242, "Feishu API call failed")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.httpTimeoutClient().Do(req)
	if err != nil {
		return nil, httperr.New(502, 50242, "Feishu API call failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, httperr.New(502, 50242, "Feishu API call failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, httperr.New(502, 50242, "Feishu API call failed")
	}
	return body, nil
}

// ExecuteFeishuQuestion runs the shared IM question pipeline and replies
// through Feishu. The reply contract matches the WeCom adapter so the S24
// cross-channel Snapshot checks stay consistent (doc/125 §2.3).
func (s *Service) ExecuteFeishuQuestion(ctx context.Context, cfg config.Feishu, target *WeComMessageTarget, question, requestID, openID string) error {
	answer, err := s.executeIMQuestion(ctx, cfg.DefaultChatID, target, question, requestID, func(ctx context.Context) (*model.User, error) {
		return s.FeishuIdentity(ctx, cfg, openID)
	})
	if err != nil {
		return err
	}
	return s.SendFeishuText(ctx, cfg, openID, answer)
}

func feishuReplyEnvelope(cfg config.Feishu, challenge string) ([]byte, error) {
	if strings.TrimSpace(cfg.EncryptKey) == "" {
		return json.Marshal(map[string]string{"challenge": challenge})
	}
	encrypted, err := encryptFeishuValue(cfg, challenge)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"encrypt": encrypted})
}

// hexEncode is a small local alias kept for readability.
func hexEncode(data []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(data)*2)
	for _, item := range data {
		out = append(out, digits[item>>4], digits[item&0x0f])
	}
	return string(out)
}

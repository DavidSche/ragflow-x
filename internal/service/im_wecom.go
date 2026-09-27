package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const wecomReplayWindow = 10 * time.Minute

type wecomEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}

type wecomPlaintext struct {
	XMLName  xml.Name `xml:"xml"`
	CorpID   string   `xml:"ToUserName"`
	AgentID  int64    `xml:"AgentID"`
	FromUser string   `xml:"FromUserName"`
	MsgID    int64    `xml:"MsgId"`
	MsgType  string   `xml:"MsgType"`
	Event    string   `xml:"Event"`
	Content  string   `xml:"Content"`
}

type wecomTokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type wecomSendResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type WeComMessageTarget struct {
	UserID      string
	TenantID    string
	ChatID      string
	SessionID   string
	MessageID   string
	Rating      string
	Attribution string
	Comment     string
}

func (s *Service) SetWeComConfig(cfg config.WeCom) {
	cfg = normalizeWeComConfig(cfg)
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	s.wecomConfig = cfg
}

func (s *Service) CurrentWeComConfig() config.WeCom {
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	return s.wecomConfig
}

func normalizeWeComConfig(cfg config.WeCom) config.WeCom {
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = 10
	}
	if cfg.MaxMessageRunes <= 0 {
		cfg.MaxMessageRunes = 2000
	}
	return cfg
}

func wecomConfigured(cfg config.WeCom) error {
	switch {
	case !cfg.Enabled:
		return httperr.NotFound("WeCom callback is disabled")
	case strings.TrimSpace(cfg.CorpID) == "" || strings.TrimSpace(cfg.Token) == "" ||
		strings.TrimSpace(cfg.EncodingAESKey) == "" || cfg.AgentID <= 0 ||
		strings.TrimSpace(cfg.AppSecret) == "":
		return httperr.New(503, 50320, "WeCom callback is not configured")
	default:
		return nil
	}
}

func wecomAESKey(encodingAESKey string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, httperr.Unauthorized("invalid WeCom callback credentials")
	}
	if len(key) != 32 {
		return nil, httperr.Unauthorized("invalid WeCom callback credentials")
	}
	return key, nil
}

func wecomSignature(token, timestamp, nonce, encrypted string) string {
	values := []string{token, timestamp, nonce, encrypted}
	sort.Strings(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return fmt.Sprintf("%x", sum)
}

func verifyWeComSignature(cfg config.WeCom, timestamp, nonce, signature, encrypted string) error {
	if signature == "" || wecomSignature(cfg.Token, timestamp, nonce, encrypted) != signature {
		return httperr.Unauthorized("invalid WeCom callback signature")
	}
	return nil
}

func decryptWeComValue(cfg config.WeCom, encrypted, expectedCorpID string) ([]byte, error) {
	if err := validateWeComSignatureInput(cfg, encrypted); err != nil {
		return nil, err
	}
	key, err := wecomAESKey(cfg.EncodingAESKey)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, httperr.Unauthorized("invalid WeCom callback payload")
	}
	plaintext := make([]byte, len(ciphertext))
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, httperr.Unauthorized("invalid WeCom callback credentials")
	}
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plaintext, ciphertext)
	unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil || len(unpadded) < 20 {
		return nil, httperr.Unauthorized("invalid WeCom callback payload")
	}
	length := int(binary.BigEndian.Uint32(unpadded[16:20]))
	if length < 0 || length > len(unpadded)-20 {
		return nil, httperr.Unauthorized("invalid WeCom callback payload")
	}
	message := unpadded[20 : 20+length]
	corpID := string(unpadded[20+length:])
	if expectedCorpID != "" && corpID != expectedCorpID {
		return nil, httperr.Unauthorized("WeCom callback tenant mismatch")
	}
	return message, nil
}

func validateWeComSignatureInput(cfg config.WeCom, encrypted string) error {
	if strings.TrimSpace(cfg.CorpID) == "" || strings.TrimSpace(cfg.Token) == "" || strings.TrimSpace(cfg.EncodingAESKey) == "" {
		return httperr.New(503, 50320, "WeCom callback is not configured")
	}
	if len(encrypted) == 0 || len(encrypted) > 1<<20 {
		return httperr.Unauthorized("invalid WeCom callback payload")
	}
	return nil
}

func pkcs7Unpad(value []byte, blockSize int) ([]byte, error) {
	if len(value) == 0 || len(value)%blockSize != 0 {
		return nil, errors.New("invalid pkcs7 padding")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > blockSize || padding > len(value) {
		return nil, errors.New("invalid pkcs7 padding")
	}
	for _, item := range value[len(value)-padding:] {
		if int(item) != padding {
			return nil, errors.New("invalid pkcs7 padding")
		}
	}
	return value[:len(value)-padding], nil
}

func encryptWeComValue(cfg config.WeCom, timestamp, nonce, plaintext, corpID string) (string, string, error) {
	key, err := wecomAESKey(cfg.EncodingAESKey)
	if err != nil {
		return "", "", err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	payload := []byte(plaintext)
	data := make([]byte, 0, 20+len(payload)+len(corpID)+aes.BlockSize)
	data = append(data, random...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(payload)))
	data = append(data, length...)
	data = append(data, payload...)
	data = append(data, corpID...)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	for index := 0; index < padding; index++ {
		data = append(data, byte(padding))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	encrypted := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(encrypted, data)
	value := base64.StdEncoding.EncodeToString(encrypted)
	return value, wecomSignature(cfg.Token, timestamp, nonce, value), nil
}

func parseWeComEnvelope(body []byte) (string, error) {
	if len(body) == 0 || len(body) > 1<<20 {
		return "", httperr.Unauthorized("invalid WeCom callback body")
	}
	var envelope wecomEnvelope
	if err := xml.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Encrypt) == "" {
		return "", httperr.Unauthorized("invalid WeCom callback body")
	}
	return strings.TrimSpace(envelope.Encrypt), nil
}

// DecryptWeComEcho handles the encrypted verification challenge. Returning the
// decrypted plaintext is the official WeCom verification contract.
func (s *Service) DecryptWeComEcho(cfg config.WeCom, timestamp, nonce, signature, encrypted string) (string, error) {
	if err := wecomConfigured(cfg); err != nil {
		return "", err
	}
	if err := verifyWeComSignature(cfg, timestamp, nonce, signature, encrypted); err != nil {
		return "", err
	}
	plaintext, err := decryptWeComValue(cfg, encrypted, cfg.CorpID)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DecryptWeComCallback verifies the signed encrypted callback, rejects a
// repeated signature and returns only the bounded message fields the adapter
// needs. XML payload itself is untrusted input.
func (s *Service) DecryptWeComCallback(cfg config.WeCom, timestamp, nonce, signature, body string) (*wecomPlaintext, error) {
	if err := wecomConfigured(cfg); err != nil {
		return nil, err
	}
	encrypted, err := parseWeComEnvelope([]byte(body))
	if err != nil {
		return nil, err
	}
	if err := verifyWeComSignature(cfg, timestamp, nonce, signature, encrypted); err != nil {
		return nil, err
	}
	callbackSecond, parseError := strconv.ParseInt(timestamp, 10, 64)
	callbackTime := time.Unix(callbackSecond, 0)
	now := time.Now()
	// Reject timestamps outside the replay window in both directions: a
	// future timestamp (negative offset) is equally replay-craftable
	// (doc/118 F-07).
	if parseError != nil || now.Sub(callbackTime) > wecomReplayWindow || callbackTime.After(now.Add(wecomReplayWindow)) {
		return nil, httperr.Unauthorized("WeCom callback timestamp expired")
	}
	plaintext, decryptError := decryptWeComValue(cfg, encrypted, cfg.CorpID)
	if decryptError != nil {
		return nil, decryptError
	}
	var callback wecomPlaintext
	if err := xml.Unmarshal(plaintext, &callback); err != nil || strings.TrimSpace(callback.FromUser) == "" {
		return nil, httperr.Unauthorized("invalid WeCom callback message")
	}
	if callback.AgentID > 0 && callback.AgentID != cfg.AgentID {
		return nil, httperr.Unauthorized("WeCom callback agent mismatch")
	}
	if len([]rune(callback.Content)) > cfg.MaxMessageRunes {
		return nil, httperr.BadRequest(40320, "WeCom message is too long")
	}
	signatureKey := signature
	if callback.MsgID > 0 {
		signatureKey = fmt.Sprintf("msg:%d:%s", callback.MsgID, cfg.CorpID)
	}
	if !s.rememberWeComCallback(signatureKey) {
		return nil, httperr.New(409, 40920, "duplicate WeCom callback")
	}
	return &callback, nil
}

func (s *Service) rememberWeComCallback(signatureKey string) bool {
	return s.rememberCallback(signatureKey)
}

type WeComCallbackContext struct {
	Callback    *wecomPlaintext
	User        *model.User
	Idempotency *model.GatewayIdempotency
}

func (s *Service) PrepareWeComCallback(ctx context.Context, cfg config.WeCom, timestamp, nonce, signature, body string) (*WeComCallbackContext, error) {
	callback, err := s.DecryptWeComCallback(cfg, timestamp, nonce, signature, body)
	if err != nil {
		return nil, err
	}
	user, err := s.wecomIdentity(ctx, cfg, callback.FromUser)
	if err != nil {
		return nil, err
	}
	idempotencyKey := fmt.Sprintf("wecom:%s:%d", cfg.CorpID, callback.MsgID)
	if callback.MsgID <= 0 {
		idempotencyKey = "wecom:" + cfg.CorpID + ":" + signature
	}
	if len(idempotencyKey) > 128 {
		idempotencyKey = idempotencyKey[:128]
	}
	fingerprint := sha256.Sum256([]byte(signature + "\n" + body))
	record := &model.GatewayIdempotency{
		ID: id.New(), TenantID: user.TenantID, PrincipalID: user.ID,
		IdempotencyKey: idempotencyKey, RequestFingerprint: hex.EncodeToString(fingerprint[:]),
		Status: model.IdempotencyInProgress, RequestID: id.New(),
		ExpiresAt: time.Now().Add(wecomReplayWindow).UTC(),
	}
	created, err := s.Store.CreateGatewayIdempotency(ctx, record)
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, httperr.New(409, 40920, "duplicate WeCom callback")
	}
	return &WeComCallbackContext{Callback: callback, User: user, Idempotency: record}, nil
}

func (s *Service) CompleteWeComCallback(ctx context.Context, callbackContext *WeComCallbackContext, responseRef string) error {
	return s.Store.CompleteGatewayIdempotency(ctx, callbackContext.Idempotency.ID, callbackContext.Idempotency.RequestID, responseRef)
}

func (s *Service) FailWeComCallback(ctx context.Context, callbackContext *WeComCallbackContext, reason string) error {
	return s.Store.FailGatewayIdempotency(ctx, callbackContext.Idempotency.ID, callbackContext.Idempotency.RequestID, reason)
}

// rememberCallback is the channel-neutral in-process replay guard shared by
// the WeCom/Feishu/DingTalk adapters (10-minute sliding window).
func (s *Service) rememberCallback(signatureKey string) bool {
	now := time.Now()
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	if s.wecomSeen == nil {
		s.wecomSeen = map[string]time.Time{}
	}
	if seenAt, exists := s.wecomSeen[signatureKey]; exists && now.Sub(seenAt) < wecomReplayWindow {
		return false
	}
	for key, seenAt := range s.wecomSeen {
		if now.Sub(seenAt) >= wecomReplayWindow {
			delete(s.wecomSeen, key)
		}
	}
	s.wecomSeen[signatureKey] = now
	return true
}

type wecomResponse struct {
	XMLName      xml.Name `xml:"xml"`
	Encrypt      string   `xml:"Encrypt"`
	MsgSignature string   `xml:"MsgSignature"`
	TimeStamp    string   `xml:"TimeStamp"`
	Nonce        string   `xml:"Nonce"`
}

func (s *Service) WeComResponseXML(cfg config.WeCom, timestamp, nonce, plaintext string) (string, error) {
	encrypted, signature, err := encryptWeComValue(cfg, timestamp, nonce, plaintext, cfg.CorpID)
	if err != nil {
		return "", err
	}
	response := wecomResponse{Encrypt: encrypted, MsgSignature: signature, TimeStamp: timestamp, Nonce: nonce}
	data, err := xml.Marshal(response)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Service) wecomIdentity(ctx context.Context, cfg config.WeCom, externalUserID string) (*model.User, error) {
	issuer := strings.TrimSpace(cfg.IdentityIssuer)
	if issuer == "" {
		issuer = "wecom:corp:" + cfg.CorpID
	}
	return s.imIdentity(ctx, issuer, externalUserID, cfg.TenantID)
}

// imIdentity resolves a channel subject through the pre-provisioned identity
// mapping. It fails closed: unknown subjects, inactive users and tenant
// mismatches are all rejected, and identities are never provisioned here
// (doc/125 §2.3; shared by the WeCom/Feishu/DingTalk adapters).
func (s *Service) imIdentity(ctx context.Context, issuer, subject, tenantConstraint string) (*model.User, error) {
	identity, err := s.Store.GetOidcIdentity(ctx, issuer, subject)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, httperr.Forbidden("IM identity is not provisioned")
	}
	user, err := s.Store.GetUser(ctx, identity.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.Status != model.UserStatusActive {
		return nil, httperr.Forbidden("IM identity is not provisioned")
	}
	if tenantConstraint != "" && user.TenantID != tenantConstraint {
		return nil, httperr.Forbidden("IM identity is not provisioned")
	}
	tenant, err := s.Store.GetTenant(ctx, user.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.Forbidden("IM workspace is disabled")
	}
	identity.LastLoginAt = time.Now().UTC()
	if err := s.Store.UpsertOidcIdentity(ctx, identity); err != nil {
		return nil, err
	}
	return user, nil
}

func parseWeComCommand(text string) *WeComMessageTarget {
	return parseIMCommand(text)
}

// parseIMCommand parses the channel-neutral feedback command
// ("feedback <rating> <session> <message> [attribution] [comment]").
func parseIMCommand(text string) *WeComMessageTarget {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 4 || !strings.EqualFold(fields[0], "feedback") {
		return nil
	}
	switch strings.ToLower(fields[1]) {
	case model.FeedbackPositive:
		if len(fields) != 4 {
			return nil
		}
		return &WeComMessageTarget{Rating: model.FeedbackPositive, SessionID: fields[2], MessageID: fields[3]}
	case model.FeedbackNegative:
		if len(fields) < 5 || len(fields) > 6 {
			return nil
		}
		target := &WeComMessageTarget{Rating: model.FeedbackNegative, Attribution: fields[4], SessionID: fields[2], MessageID: fields[3]}
		if len(fields) == 6 {
			target.Comment = fields[5]
		}
		return target
	default:
		return nil
	}
}

func wecomRoutingInput(text, defaultChatID string) (chatID, sessionID, question string) {
	return imRoutingInput(text, defaultChatID)
}

// imRoutingInput parses the channel-neutral "use <chatID> ..." /
// "session <sessionID> ..." routing prefixes.
func imRoutingInput(text, defaultChatID string) (chatID, sessionID, question string) {
	fields := strings.Fields(strings.TrimSpace(text))
	switch {
	case len(fields) >= 3 && strings.EqualFold(fields[0], "use"):
		return fields[1], "", strings.Join(fields[2:], " ")
	case len(fields) >= 3 && strings.EqualFold(fields[0], "session"):
		return defaultChatID, fields[1], strings.Join(fields[2:], " ")
	default:
		return defaultChatID, "", strings.TrimSpace(text)
	}
}

func (s *Service) RecordWeComFeedback(ctx context.Context, target *WeComMessageTarget) error {
	_, err := s.RecordMessageFeedback(ctx, target.TenantID, target.UserID, FeedbackRequest{
		ChatID: target.ChatID, SessionID: target.SessionID, MessageID: target.MessageID,
		Rating: target.Rating, Attribution: target.Attribution, Comment: target.Comment,
		RequestID: id.New(),
	})
	return err
}

func (s *Service) ExecuteWeComQuestion(ctx context.Context, target *WeComMessageTarget, question, requestID, externalUserID string) error {
	if strings.TrimSpace(question) == "" {
		return httperr.BadRequest(40060, "question is required")
	}
	cfg := s.CurrentWeComConfig()
	user, err := s.wecomIdentity(ctx, cfg, externalUserID)
	if err != nil {
		return err
	}
	target.UserID = user.ID
	target.TenantID = user.TenantID
	if err := s.Authorize(ctx, user.ID, "execute", "chat"); err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(question), "feedback") {
		command := parseWeComCommand(question)
		if command == nil {
			return httperr.BadRequest(40090, "invalid feedback command")
		}
		command.UserID, command.ChatID = user.ID, target.ChatID
		return s.RecordWeComFeedback(ctx, command)
	}
	chatID, sessionID, questionText := wecomRoutingInput(question, cfg.DefaultChatID)
	if chatID == "" {
		return httperr.BadRequest(40060, "assistant is required")
	}
	if strings.TrimSpace(questionText) == "" {
		return httperr.BadRequest(40060, "question is required")
	}
	target.ChatID = chatID
	target.SessionID = sessionID
	request := ragflow.CompletionRequest{Messages: []ragflow.Message{{Role: "user", Content: strings.TrimSpace(questionText)}}}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return err
	}
	key := &model.APIKey{TenantID: user.TenantID, UserID: user.ID}
	if err := s.AuthorizeChatTarget(ctx, key, chatID, sessionID); err != nil {
		return err
	}
	result, err := s.ChatAppCompletion(ctx, key, chatID, sessionID, rawRequest, requestID)
	if err != nil {
		return err
	}
	if result.Status >= 300 {
		return httperr.New(502, 50230, "ragflow chat completion failed")
	}
	var completion map[string]any
	if err := json.Unmarshal(result.Body, &completion); err != nil {
		return httperr.New(502, 50222, "read provider response failed")
	}
	answer, citations := wecomCompletionSummary(completion)
	if target.SessionID == "" {
		target.SessionID = stringFromAny(completion["session_id"])
	}
	target.MessageID = stringFromAny(completion["id"])
	s.recordCitationReferences(ctx, user.TenantID, chatID, target.SessionID, requestID, wecomCitations(completion))
	message := answer + "\n\nTrace: " + requestID + "\nSession: " + target.SessionID + "\nMessage: " + target.MessageID + "\nCitations: " + fmt.Sprintf("%d", citations)
	return s.SendWeComText(ctx, externalUserID, message)
}

func wecomCompletionSummary(completion map[string]any) (string, int) {
	answer := stringFromAny(completion["answer"])
	if answer == "" {
		if choices, ok := completion["choices"].([]any); ok && len(choices) > 0 {
			choice, _ := choices[0].(map[string]any)
			message, _ := choice["message"].(map[string]any)
			answer = stringFromAny(message["content"])
		}
	}
	citations := wecomCitations(completion)
	if len(citations) == 0 {
		return strings.TrimSpace(answer), 0
	}
	summary := strings.Builder{}
	summary.WriteString(strings.TrimSpace(answer))
	summary.WriteString("\n\n引用摘要：")
	for index, citation := range citations {
		if index >= 3 {
			break
		}
		document := stringFromAny(citation["document_name"])
		if document == "" {
			document = stringFromAny(citation["doc_name"])
		}
		if document == "" {
			document = stringFromAny(citation["document_id"])
		}
		if document == "" {
			document = stringFromAny(citation["doc_id"])
		}
		fmt.Fprintf(&summary, "\n%d. %s", index+1, document)
	}
	if len(citations) > 3 {
		fmt.Fprintf(&summary, "\n…共 %d 条引用", len(citations))
	}
	return summary.String(), len(citations)
}

func wecomCitations(completion map[string]any) []map[string]any {
	var result []map[string]any
	appendValues := func(values []any) {
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				result = append(result, item)
			}
		}
	}
	if reference, ok := completion["reference"].([]any); ok {
		appendValues(reference)
	} else if references, ok := completion["references"].([]any); ok {
		appendValues(references)
	} else if reference, ok := completion["reference"].(map[string]any); ok {
		if chunks, ok := reference["chunks"].([]any); ok {
			appendValues(chunks)
		}
	}
	return result
}

func (s *Service) SendWeComText(ctx context.Context, externalUserID, message string) error {
	cfg := s.CurrentWeComConfig()
	if err := wecomConfigured(cfg); err != nil {
		return err
	}
	token, err := s.weComAccessToken(ctx, cfg, true)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"touser": externalUserID, "msgtype": "text", "agentid": cfg.AgentID,
		"text": map[string]string{"content": message},
	})
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Post(
		wecomAPIBaseURL(cfg)+"/cgi-bin/message/send?access_token="+url.QueryEscape(token),
		"application/json", bytes.NewReader(payload),
	)
	if err != nil {
		return httperr.New(502, 50240, "WeCom send failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return httperr.New(502, 50240, "WeCom send failed")
	}
	var result wecomSendResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return httperr.New(502, 50240, "WeCom send failed")
	}
	if result.ErrCode == 40014 {
		token, err = s.weComAccessToken(ctx, cfg, false)
		if err != nil {
			return err
		}
		resp, err = s.httpClient.Post(
			wecomAPIBaseURL(cfg)+"/cgi-bin/message/send?access_token="+url.QueryEscape(token),
			"application/json", bytes.NewReader(payload),
		)
		if err != nil {
			return httperr.New(502, 50240, "WeCom send failed")
		}
		defer resp.Body.Close()
		result = wecomSendResponse{}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
			return httperr.New(502, 50240, "WeCom send failed")
		}
	}
	if result.ErrCode != 0 {
		logger.Warn("wecom message rejected", "errcode", result.ErrCode, "errmsg", result.ErrMsg)
		return httperr.New(502, 50240, "WeCom send failed")
	}
	return nil
}

func (s *Service) weComAccessToken(ctx context.Context, cfg config.WeCom, useCache bool) (string, error) {
	s.wecomMu.Lock()
	defer s.wecomMu.Unlock()
	if useCache && s.wecomToken != "" && time.Now().Before(s.wecomTokenExpiresAt) {
		return s.wecomToken, nil
	}
	target := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		wecomAPIBaseURL(cfg), url.QueryEscape(cfg.CorpID), url.QueryEscape(cfg.AppSecret))
	resp, err := s.httpClient.Get(target)
	if err != nil {
		return "", httperr.New(502, 50241, "WeCom authorization failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", httperr.New(502, 50241, "WeCom authorization failed")
	}
	var result wecomTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", httperr.New(502, 50241, "WeCom authorization failed")
	}
	if result.ErrCode != 0 || result.AccessToken == "" {
		logger.Warn("wecom token rejected", "errcode", result.ErrCode, "errmsg", result.ErrMsg)
		return "", httperr.New(502, 50241, "WeCom authorization failed")
	}
	s.wecomToken = result.AccessToken
	expiresIn := result.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	s.wecomTokenExpiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - 2*time.Minute)
	return s.wecomToken, nil
}

func wecomAPIBaseURL(cfg config.WeCom) string {
	value := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if value == "" {
		return "https://qyapi.weixin.qq.com"
	}
	return value
}

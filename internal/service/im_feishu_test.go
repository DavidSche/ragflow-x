package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newFeishuConfig(verificationToken string) config.Feishu {
	return config.Feishu{
		Enabled: true, AppID: "cli-test", AppSecret: "app-secret",
		VerificationToken: verificationToken, IdentityIssuer: "feishu:test",
		HTTPTimeoutSec: 5, MaxMessageRunes: 2000,
	}
}

func encryptFeishuEvent(t *testing.T, cfg config.Feishu, payload string) string {
	// Feishu's AES envelope is random(16) || 4-byte big-endian length ||
	// payload || padding; decryptFeishuValue expects the same layout as the
	// WeCom decrypt helper.
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

func feishuEventPayload(eventID string, createdAt int64, token, text string) string {
	// Feishu serializes message.content as a JSON *string* containing the
	// inner text payload; the adapter parses that inner JSON (doc/125 §2.3).
	content, _ := json.Marshal(map[string]string{"text": text})
	event, _ := json.Marshal(map[string]any{
		"header": map[string]any{
			"event_id": eventID, "event_type": "im.message.receive_v1",
			"token": token, "create_time": createdAt,
		},
		"sender": map[string]any{
			"sender_id": map[string]any{"open_id": "ou-123"},
		},
		"message": map[string]any{
			"message_type": "text", "content": string(content),
		},
	})
	return string(event)
}

func TestFeishuURLVerificationPlainAndEncrypted(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newFeishuConfig("token-1")
	body := `{"type":"url_verification","challenge":"chk-1","token":"token-1"}`
	reply, err := svc.DecryptFeishuURLVerification(cfg, []byte(body))
	if err != nil || reply != `{"challenge":"chk-1"}` {
		t.Fatalf("plain verification must echo the challenge JSON envelope: reply=%q err=%v", reply, err)
	}
	bad := strings.Replace(body, "token-1", "wrong", 1)
	if _, err := svc.DecryptFeishuURLVerification(cfg, []byte(bad)); err == nil {
		t.Fatal("wrong verification token must fail")
	}
	encryptedCfg := cfg
	encryptedCfg.EncryptKey = "enc-key"
	sealed := `{"type":"url_verification","encrypt":"` + encryptFeishuEvent(t, encryptedCfg, `{"type":"url_verification","challenge":"chk-2","token":"token-1"}`) + `"}`
	echo, err := svc.DecryptFeishuURLVerification(encryptedCfg, []byte(sealed))
	if err != nil {
		t.Fatalf("encrypted verification: %v", err)
	}
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal([]byte(echo), &envelope); err != nil || envelope.Encrypt == "" {
		t.Fatalf("encrypted echo must be an AES envelope: %q err=%v", echo, err)
	}
	plaintext, err := decryptFeishuValue(encryptedCfg, envelope.Encrypt)
	if err != nil || !strings.Contains(string(plaintext), "chk-2") {
		t.Fatalf("encrypted echo roundtrip failed: %q err=%v", string(plaintext), err)
	}
}

func TestFeishuEventTokenReplayAndType(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newFeishuConfig("token-1")
	now := time.Now().UnixMilli()
	payload := feishuEventPayload("evt-1", now, "token-1", "隐患整改的期限要求")
	event, err := svc.DecryptFeishuEvent(cfg, []byte(payload))
	if err != nil || event.Message.Content != "隐患整改的期限要求" {
		t.Fatalf("event accepted: %+v err=%v", event, err)
	}
	// Wrong token.
	if _, err := svc.DecryptFeishuEvent(cfg, []byte(feishuEventPayload("evt-2", now, "wrong", "hello"))); err == nil {
		t.Fatal("wrong token must fail")
	}
	// Future timestamp beyond the window must be rejected (doc/118 F-07 parity).
	if _, err := svc.DecryptFeishuEvent(cfg, []byte(feishuEventPayload("evt-3", now+15*60_000, "token-1", "hello"))); err == nil {
		t.Fatal("future timestamp must fail")
	}
	if _, err := svc.DecryptFeishuEvent(cfg, []byte(feishuEventPayload("evt-4", now-15*60_000, "token-1", "hello"))); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	// Replay of the same event id must conflict.
	if _, err := svc.DecryptFeishuEvent(cfg, []byte(payload)); err == nil {
		t.Fatal("replayed event id must conflict")
	}
	// Unsupported event type.
	other := `{"header":{"event_id":"evt-5","event_type":"im.message.updated_v1","token":"token-1","create_time":` + fmt.Sprint(now) + `}}`
	if _, err := svc.DecryptFeishuEvent(cfg, []byte(other)); err == nil {
		t.Fatal("unsupported event type must fail")
	}
}

func TestFeishuIdentityFailsClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	cfg := newFeishuConfig("token-1")
	ctx := context.Background()
	if _, err := svc.FeishuIdentity(ctx, cfg, "ou-unknown"); err == nil {
		t.Fatal("unmapped Feishu identity must fail closed")
	}
	tenant, err := svc.CreateTenant(ctx, "Feishu Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "feishu-user", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertOidcIdentity(ctx, &model.OidcIdentity{
		ID: "identity-feishu-1", Issuer: "feishu:test", Subject: "ou-123", UserID: user.ID,
	}); err != nil {
		t.Fatal(err)
	}
	mapped, err := svc.FeishuIdentity(ctx, cfg, "ou-123")
	if err != nil || mapped.ID != user.ID {
		t.Fatalf("mapped Feishu identity: %v %v", mapped, err)
	}
	// Tenant constraint must reject users from other tenants.
	otherTenant, err := svc.CreateTenant(ctx, "Feishu Other Tenant")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TenantID = otherTenant.ID
	if _, err := svc.FeishuIdentity(ctx, cfg, "ou-123"); err == nil {
		t.Fatal("tenant mismatch must fail closed")
	}
}

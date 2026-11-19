package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// A14 pinned the stream half of the attachment contract in `chat_app.go`
// ("resolved before the first byte"), but the handler wrote `200 +
// text/event-stream` *before* calling the service, so that promise was only true
// inside the service. Live verification (full42, R25) caught the difference: an
// unknown key in `report_context` came back as HTTP 200 with an error frame.
//
// These tests pin the HTTP half: a refusal before any byte is a refusable status
// with the ordinary envelope, and a stream that does produce bytes still commits
// 200 + event-stream exactly once.

type chatStreamHarness struct {
	svc    *service.Service
	engine *reportHandlerEngine
	chatID string
	sessID string
	path   string
	router *gin.Engine
}

func newChatStreamHarness(t *testing.T) *chatStreamHarness {
	t.Helper()
	svc, engine := newReportService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "chat-stream")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "chat-stream-user", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := engine.Mock.CreateChat(ctx, ragflow.CreateChatRequest{
		Name: "kb-assistant", DatasetIDs: []string{"d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{
		ID: chat.ID, TenantID: tenant.ID, Name: chat.Name,
		Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	session, err := engine.Mock.CreateChatSession(ctx, chat.ID, "session")
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, user.ID)
		c.Set("request_id", "req-chat-stream")
		c.Next()
	})
	group.POST("/chat/completions", New(svc).UserChatCompletion)
	return &chatStreamHarness{
		svc: svc, engine: engine, chatID: chat.ID, sessID: session.ID,
		path: "/api/v1/chat/completions", router: router,
	}
}

func (h *chatStreamHarness) stream(t *testing.T, reportContext map[string]interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{
		"chat_id":    h.chatID,
		"session_id": h.sessID,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "这份报告站得住吗？"}},
	}
	if reportContext != nil {
		body["report_context"] = reportContext
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, h.path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	var envelope map[string]interface{}
	if strings.Contains(rec.Header().Get("Content-Type"), "json") && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("refusal must be an envelope, got %q: %v", rec.Body.String(), err)
		}
	}
	return rec, envelope
}

func TestChatStreamStillCommitsTwoHundredOnTheFirstByte(t *testing.T) {
	h := newChatStreamHarness(t)
	rec, _ := h.stream(t, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a stream that produces bytes must be 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("stream content-type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("stream body has no SSE frame: %q", rec.Body.String())
	}
	// The writer is single-shot: a second commit would inject a second header
	// block into the middle of the stream.
	if strings.Count(rec.Body.String(), "data: [DONE]") != 1 {
		t.Fatalf("stream must carry exactly one terminal frame: %q", rec.Body.String())
	}
}

func TestChatStreamRefusesAnUnknownAttachmentKeyBeforeTheFirstByte(t *testing.T) {
	h := newChatStreamHarness(t)
	rec, envelope := h.stream(t, map[string]interface{}{
		"report_id": "rep-1",
		"revision":  1,
		// `section` is the plausible typo: ignoring it would silently widen the
		// question from one section to the whole report.
		"section": "compliance",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad attachment must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "event-stream") {
		t.Fatalf("a refused request must not look like a started stream (content-type %q)", ct)
	}
	if code := envelopeCode(t, envelope); code != 44105 {
		t.Fatalf("bad attachment must answer 44105, got %d", code)
	}
}

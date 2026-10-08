package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestOutboxEventRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	_ = svc.SetupWorker(service.DefaultWorkerConfig())
	tenant, err := svc.CreateTenant(t.Context(), "outbox-event-handler")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := tenant.ID
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, "admin")
		c.Next()
	})
	RegisterOutboxEventRoutes(group)

	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload: `{"secret":"do-not-project"}`, LastError: "sensitive diagnostic",
		Attempts: 1, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err = svc.Store.CreateOutboxEvent(t.Context(), event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	published := &model.OutboxEvent{
		ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload: `{"secret":"do-not-project"}`, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	publishedAt := now
	published.PublishedAt = &publishedAt
	published.Result = `{"status":"published","affected_eval_case_dependencies":0}`
	if err = svc.Store.CreateOutboxEvent(t.Context(), published); err != nil {
		t.Fatalf("create published event: %v", err)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/outbox-events?status=retrying", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list outbox events: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	page := decodeObject[struct {
		Items []repository.OutboxEventView `json:"items"`
		Total int64                        `json:"total"`
	}](t, recorder)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != event.ID {
		t.Fatalf("unexpected outbox page: %+v", page)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/outbox-events/"+event.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get outbox event: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/outbox-events/"+published.ID+"/retry", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("published retry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/outbox-events/"+event.ID+"/retry", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry outbox event: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/outbox-events/"+id.New(), nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing outbox event: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

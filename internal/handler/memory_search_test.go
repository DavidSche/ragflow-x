package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func memorySearchRouter(t *testing.T) (*gin.Engine, *ragflow.Mock, *service.Service, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	provider := ragflow.NewMock()
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), provider, nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "memory-search-handler")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(t.Context(), tenant.ID, "", service.CreateUserRequest{
		Username: "memory-search-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, admin.ID)
		c.Next()
	})
	group.GET("/memories/:id/messages/search", New(svc).SearchMemoryMessages)
	group.POST("/memories/:id/governed-search", New(svc).SearchGovernedMemory)
	return router, provider, svc, tenant.ID, admin.ID
}

func TestMemorySearchSendsExplicitANDFilters(t *testing.T) {
	router, provider, svc, tenantID, _ := memorySearchRouter(t)
	memory, err := svc.CreateMemory(t.Context(), tenantID, "search memory", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.AddMemoryMessage(t.Context(), ragflow.AddMessageRequest{
		MemoryIDs: []string{memory.ID}, AgentID: "agent-1", SessionID: "session-1",
		UserID: "user-1", UserInput: "pineapple", AgentResponse: "answer",
	}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/memories/"+memory.ID+"/messages/search?query=pineapple&agent_id=agent-1&session_id=session-1&user_id=user-1&top_n=5",
		nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("filtered search: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	items := decodeObject[[]map[string]interface{}](t, recorder)
	if len(items) != 1 || items[0]["user_input"] != "pineapple" {
		t.Fatalf("unexpected filtered search results: %+v", items)
	}
}

func TestMemorySearchRejectsUnknownQueryParameters(t *testing.T) {
	router, _, svc, tenantID, _ := memorySearchRouter(t)
	memory, err := svc.CreateMemory(t.Context(), tenantID, "unknown search", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/memories/"+memory.ID+"/messages/search?query=q&untrusted_filter=1", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown query parameter: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGovernedMemorySearchRequiresActivePolicy(t *testing.T) {
	router, provider, svc, tenantID, adminID := memorySearchRouter(t)
	template := `{"purpose":"assistant recall","read_policy":"owner","write_policy":"owner","retention_days":30,"embedding_model":"embedding-1","llm_model":"chat-1"}`
	if _, err := svc.UpdateSelfMemoryPolicyTemplate(t.Context(), tenantID, adminID, service.SelfMemoryPolicyTemplateInput{Template: template}); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateSelfMemoryPolicy(t.Context(), tenantID, adminID, "assistant-1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err = svc.SetMemoryPolicyConsent(t.Context(), tenantID, adminID, policy.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.AddMemoryMessage(t.Context(), ragflow.AddMessageRequest{
		MemoryIDs: []string{policy.RagflowMemoryID}, AgentID: "assistant-1", SessionID: "session-1",
		UserID: "rgx:" + tenantID + ":" + adminID, UserInput: "governed", AgentResponse: "answer",
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"query":"governed","assistant_id":"assistant-1","session_id":"session-1","request_id":"request-handler","top_n":5}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost,
		"/api/v1/memories/"+policy.RagflowMemoryID+"/governed-search", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("governed search: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMemorySearchRejectsInvalidParameters(t *testing.T) {
	router, _, svc, tenantID, _ := memorySearchRouter(t)
	memory, err := svc.CreateMemory(t.Context(), tenantID, "invalid search", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"top_n=0",
		"top_n=101",
		"threshold=-0.1",
		"threshold=1.1",
		"weight=-0.1",
		"weight=1.1",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
			"/api/v1/memories/"+memory.ID+"/messages/search?query=q&"+query, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", query, recorder.Code, recorder.Body.String())
		}
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/memories/"+memory.ID+"/messages/search?query=%20", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("blank query: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

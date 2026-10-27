package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestListUsersWithAllScopeAndTenantFilter(t *testing.T) {
	env := setupTestEnv(t)
	tenantUser, err := env.svc.CreateUser(t.Context(), env.tenantID, env.platformID, service.CreateUserRequest{
		Username: "tenant-user-" + env.tenantID[:8],
		Password: "password123",
		Role:     "business_user",
	})
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(middleware.Auth(env.svc.JWT, env.svc))
	router.Use(middleware.RBAC(env.svc, []middleware.Rule{
		{Method: "GET", Path: "/api/v1/users", Action: "read", Resource: "user"},
	}))
	router.GET("/api/v1/users", env.h.ListUsers)
	token := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users?scope=all&tenant_id="+env.tenantID+"&page=1&page_size=10", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, user := range body.Data.Items {
		if user["id"] == tenantUser.ID && user["tenant_id"] == env.tenantID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tenant user missing in %v", body.Data.Items)
	}
}

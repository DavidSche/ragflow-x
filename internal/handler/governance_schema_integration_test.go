package handler

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
)

// governanceAccessRulesT1 是 doc/224 T1 治理字典/schema/自动投影路由子集。
// 与 internal/router/access.go 中 document-governance 资源声明保持一致。
func governanceAccessRulesT1() []middleware.Rule {
	return []middleware.Rule{
		{Method: "GET", Path: "/api/v1/governance/dictionary", Action: "read", Resource: "document-governance"},
		{Method: "POST", Path: "/api/v1/governance/dictionary", Action: "manage", Resource: "document-governance"},
		{Method: "POST", Path: "/api/v1/governance/dictionary/:id/:decision", Action: "manage", Resource: "document-governance"},
		{Method: "DELETE", Path: "/api/v1/governance/dictionary/:id", Action: "manage", Resource: "document-governance"},
		{Method: "GET", Path: "/api/v1/governance/schemas", Action: "read", Resource: "document-governance"},
		{Method: "GET", Path: "/api/v1/governance/schemas/:id", Action: "read", Resource: "document-governance"},
		{Method: "POST", Path: "/api/v1/governance/schemas", Action: "manage", Resource: "document-governance"},
		{Method: "PUT", Path: "/api/v1/governance/schemas/:id", Action: "manage", Resource: "document-governance"},
		{Method: "DELETE", Path: "/api/v1/governance/schemas/:id", Action: "manage", Resource: "document-governance"},
		{Method: "POST", Path: "/api/v1/datasets/:id/governance/auto-project", Action: "execute", Resource: "document-governance"},
	}
}

// applyT1Rules 用 T1 路由子集重建 engine（复用 setupGovernanceTestEnv 的用户
// 与数据集底座，避免重复引导）。
func (e *governanceTestEnv) applyT1Rules(t *testing.T) {
	t.Helper()
	r := gin.New()
	r.Use(middleware.Auth(e.svc.JWT, e.svc))
	r.Use(middleware.RBAC(e.svc, governanceAccessRulesT1()))
	protected := r.Group("/api/v1")
	{
		protected.GET("/governance/dictionary", e.h.ListGovernanceDictionary)
		protected.POST("/governance/dictionary", e.h.CreateGovernanceDictionaryEntry)
		protected.POST("/governance/dictionary/:id/:decision", e.h.DecideGovernanceDictionaryEntry)
		protected.DELETE("/governance/dictionary/:id", e.h.DeleteGovernanceDictionaryEntry)
		protected.GET("/governance/schemas", e.h.ListGovernanceSchemas)
		protected.GET("/governance/schemas/:id", e.h.GetGovernanceSchema)
		protected.POST("/governance/schemas", e.h.CreateGovernanceSchema)
		protected.PUT("/governance/schemas/:id", e.h.UpdateGovernanceSchema)
		protected.DELETE("/governance/schemas/:id", e.h.DeleteGovernanceSchema)
		protected.POST("/datasets/:id/governance/auto-project", e.h.AutoProjectGovernance)
	}
	e.router = r
}

func (e *governanceTestEnv) decodedListFirstPlatformID(t *testing.T, token string) string {
	t.Helper()
	rec := e.doRequest(t, http.MethodGet, "/api/v1/governance/dictionary", nil, token)
	dict := decodeOK(t, rec)
	levelArr, ok := dict["level"].([]interface{})
	if !ok || len(levelArr) == 0 {
		t.Fatalf("dictionary level terms missing, got %v", dict)
	}
	first := levelArr[0].(map[string]interface{})
	return first["id"].(string)
}

func TestGovernanceDictionaryHTTPLifecycle(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	env.applyT1Rules(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	// Member can read the seeded vocabulary; platform seed present.
	rec := env.doRequest(t, http.MethodGet, "/api/v1/governance/dictionary", nil, memberToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("member dictionary read expected 200, got %d", rec.Code)
	}
	dict := decodeOK(t, rec)
	levelArr, _ := dict["level"].([]interface{})
	if len(levelArr) != 3 {
		t.Fatalf("expected 3 platform level terms, got %d", len(levelArr))
	}

	// Member lacks manage → POST is 403 (default-deny style RBAC).
	rec = env.doRequest(t, http.MethodPost, "/api/v1/governance/dictionary",
		map[string]interface{}{"category": "level", "key": "restricted", "label": "受控"}, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member create dictionary expected 403, got %d", rec.Code)
	}

	// Governor creates a tenant extension → pending.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/governance/dictionary",
		map[string]interface{}{"category": "level", "key": "restricted", "label": "受控"}, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("governor create expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	entry := decodeOK(t, rec)
	entryID := entry["id"].(string)

	// Approve activates it; effective vocabulary grows to 4.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/governance/dictionary/"+entryID+"/approve", nil, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("governor approve expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = env.doRequest(t, http.MethodGet, "/api/v1/governance/dictionary", nil, memberToken)
	dict2 := decodeOK(t, rec)
	levelArr2, _ := dict2["level"].([]interface{})
	if len(levelArr2) != 4 {
		t.Fatalf("expected 4 level terms after approve, got %d", len(levelArr2))
	}

	// Member cannot delete (403); governor can.
	rec = env.doRequest(t, http.MethodDelete, "/api/v1/governance/dictionary/"+entryID, nil, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member delete expected 403, got %d", rec.Code)
	}
	rec = env.doRequest(t, http.MethodDelete, "/api/v1/governance/dictionary/"+entryID, nil, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("governor delete expected 200, got %d", rec.Code)
	}

	// Platform term delete → 404 (immutable).
	platformID := env.decodedListFirstPlatformID(t, memberToken)
	rec = env.doRequest(t, http.MethodDelete, "/api/v1/governance/dictionary/"+platformID, nil, govToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("platform term delete expected 404, got %d", rec.Code)
	}

	// Bad decision verb → 400.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/governance/dictionary/"+entryID+"/maybe", nil, govToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision expected 400, got %d", rec.Code)
	}
}

func TestGovernanceSchemaHTTPLifecycle(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	env.applyT1Rules(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	payload := map[string]interface{}{
		"name": "绝密识别", "category": "level", "matcher_type": "regex",
		"pattern": `(?i)top.?secret`, "target_key": "top_secret", "quality_min": 60,
		"bind_scope": "dataset", "dataset_id": env.datasetID,
	}

	// Member lacks manage → 403.
	rec := env.doRequest(t, http.MethodPost, "/api/v1/governance/schemas", payload, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member create schema expected 403, got %d", rec.Code)
	}

	// Governor creates.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/governance/schemas", payload, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("governor create schema expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	schema := decodeOK(t, rec)
	schemaID := schema["id"].(string)

	// List filtered by dataset.
	rec = env.doRequest(t, http.MethodGet, "/api/v1/governance/schemas?dataset_id="+env.datasetID, nil, memberToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list schemas expected 200, got %d", rec.Code)
	}
	// update
	payload["quality_min"] = 80
	rec = env.doRequest(t, http.MethodPut, "/api/v1/governance/schemas/"+schemaID, payload, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("update schema expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	updated := decodeOK(t, rec)
	if updated["quality_min"].(float64) != 80 {
		t.Fatalf("update not applied: %v", updated["quality_min"])
	}
	// delete
	rec = env.doRequest(t, http.MethodDelete, "/api/v1/governance/schemas/"+schemaID, nil, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete schema expected 200, got %d", rec.Code)
	}
}

func TestAutoProjectEndpointRBAC(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	env.applyT1Rules(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	// Member lacks execute on document-governance → 403.
	rec := env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+env.datasetID+"/governance/auto-project", nil, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member auto-project expected 403, got %d", rec.Code)
	}

	// Governor runs (empty dataset → Scanned 0, no error).
	rec = env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+env.datasetID+"/governance/auto-project", nil, govToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("governor auto-project expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	res := decodeOK(t, rec)
	if res["scanned"].(float64) != 0 {
		t.Fatalf("empty dataset scan expected 0, got %v", res["scanned"])
	}
}

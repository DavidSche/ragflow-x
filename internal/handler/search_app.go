package handler

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// searchAppDatasetList accepts either a JSON array of dataset ids or a single
// comma-separated string so both the admin UI and API clients can bind datasets.
type searchAppDatasetList []string

func (s *searchAppDatasetList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" || string(b) == "" {
		*s = nil
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*s = arr
		return nil
	}
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	*s = parts
	return nil
}

type searchAppConfigPayload struct {
	KbIDs                  []string                         `json:"kb_ids"`
	DocIDs                 []string                         `json:"doc_ids"`
	SimilarityThreshold    *float64                         `json:"similarity_threshold"`
	VectorSimilarityWeight *float64                         `json:"vector_similarity_weight"`
	UseKG                  *bool                            `json:"use_kg"`
	RerankID               string                           `json:"rerank_id"`
	TopK                   *int                             `json:"top_k"`
	Summary                *bool                            `json:"summary"`
	ChatID                 string                           `json:"chat_id"`
	LLMSetting             map[string]interface{}           `json:"llm_setting"`
	UseRerank              *bool                            `json:"use_rerank"`
	RelatedSearch          *bool                            `json:"related_search"`
	QueryMindmap           *bool                            `json:"query_mindmap"`
	WebSearch              *bool                            `json:"web_search"`
	ReferenceMetadata      *ragflow.SearchReferenceMetadata `json:"reference_metadata"`
	MetaDataFilter         map[string]interface{}           `json:"meta_data_filter"`
}

type searchAppPayload struct {
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	SearchConfig *searchAppConfigPayload `json:"search_config"`
	DatasetIDs   searchAppDatasetList    `json:"dataset_ids"`
}

func (p *searchAppPayload) toSearchConfig() *ragflow.SearchConfig {
	cfg := &ragflow.SearchConfig{KbIDs: []string{}}
	if p.SearchConfig != nil {
		cfg.DocIDs = p.SearchConfig.DocIDs
		cfg.RerankID = p.SearchConfig.RerankID
		cfg.ChatID = p.SearchConfig.ChatID
		if p.SearchConfig.KbIDs != nil {
			cfg.KbIDs = p.SearchConfig.KbIDs
		}
		if p.SearchConfig.LLMSetting != nil {
			cfg.LLMSetting = p.SearchConfig.LLMSetting
		}
		if p.SearchConfig.SimilarityThreshold != nil {
			cfg.SimilarityThreshold = *p.SearchConfig.SimilarityThreshold
		}
		if p.SearchConfig.VectorSimilarityWeight != nil {
			cfg.VectorSimilarityWeight = *p.SearchConfig.VectorSimilarityWeight
		}
		if p.SearchConfig.UseKG != nil {
			cfg.UseKG = *p.SearchConfig.UseKG
		}
		if p.SearchConfig.TopK != nil {
			cfg.TopK = *p.SearchConfig.TopK
		}
		if p.SearchConfig.Summary != nil {
			cfg.Summary = *p.SearchConfig.Summary
		}
	}
	if p.SearchConfig != nil {
		cfg.UseRerank = p.SearchConfig.UseRerank
		cfg.RelatedSearch = p.SearchConfig.RelatedSearch
		cfg.QueryMindmap = p.SearchConfig.QueryMindmap
		cfg.WebSearch = p.SearchConfig.WebSearch
		cfg.ReferenceMetadata = p.SearchConfig.ReferenceMetadata
		cfg.MetaDataFilter = p.SearchConfig.MetaDataFilter
	}
	if len(cfg.KbIDs) == 0 && len(p.DatasetIDs) > 0 {
		cfg.KbIDs = p.DatasetIDs
	}
	return cfg
}

// ListSearchApps returns the caller's Search Apps (platform scope=all for all).
// SearchAppModelOptions returns the tenant's chat/rerank models and defaults so
// the Search App config can pick a real LLM (chat_id) and rerank model.
func (h *Handler) SearchAppModelOptions(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	opts, err := h.Service.GetModelOptions(ctx)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, opts)
}
func (h *Handler) ListSearchApps(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	filter := repository.SearchAppFilter{
		Name: c.Query("name"), Status: c.Query("status"), OwnerID: c.Query("owner_id"),
	}
	scope, ok := h.resolveGovernanceScope(c, "search-app")
	if !ok {
		return
	}
	items, total, err := h.Service.ListSearchAppsForScope(ctx, scope, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// CreateSearchApp creates a Search App owned by the caller's tenant.
func (h *Handler) CreateSearchApp(c *gin.Context) {
	var req searchAppPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid search app payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	if policy := activePromptPolicy(h, c, model.PromptScopeSearch, ""); policy != nil {
		applyPromptPolicyToSearch(&req, policy)
	}
	sa, err := h.Service.CreateSearchApp(ctx, tenantID, req.Name, req.toSearchConfig())
	if err != nil {
		response.Err(c, err)
		return
	}
	sa.OwnerID = userID
	_ = h.Service.SetSearchAppOwner(ctx, sa, userID)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "search_app.create", Resource: "search-app", ResourceID: sa.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, sa)
}

// GetSearchApp returns a Search App.
func (h *Handler) GetSearchApp(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "search-app")
	if !ok {
		return
	}
	sa, err := h.Service.GetSearchAppForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, sa)
}

// GetSearchAppConfig returns the live RAGFlow-side search_config for prefill.
func (h *Handler) GetSearchAppConfig(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "search-app")
	if !ok {
		return
	}
	live, err := h.Service.GetSearchAppConfigForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, live)
}

// UpdateSearchApp updates a Search App (name and search_config).
func (h *Handler) UpdateSearchApp(c *gin.Context) {
	var req searchAppPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid search app payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	searchAppID := c.Param("id")
	if policy := activePromptPolicy(h, c, model.PromptScopeSearch, searchAppID); policy != nil {
		applyPromptPolicyToSearch(&req, policy)
	}
	sa, err := h.Service.UpdateSearchApp(ctx, tenantID, searchAppID, req.Name, req.toSearchConfig(), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "search_app.update", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, sa)
}

// DeleteSearchApp deletes a Search App.
func (h *Handler) DeleteSearchApp(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	searchAppID := c.Param("id")
	if err := h.Service.DeleteSearchApp(ctx, tenantID, searchAppID, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "search_app.delete", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": searchAppID})
}

type searchAppCompletePayload struct {
	Question string `json:"question"`
}

// SearchAppCompletion runs a retrieval against a Search App for debugging.
func (h *Handler) SearchAppCompletion(c *gin.Context) {
	var req searchAppCompletePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "question is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	searchAppID := c.Param("id")
	resp, err := h.Service.SearchAppCompletion(ctx, tenantID, searchAppID, req.Question, c.GetString("request_id"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "search_app.completion", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, resp)
}

// StreamSearchAppCompletion proxies the Search App SSE completion to the client.
func (h *Handler) StreamSearchAppCompletion(c *gin.Context) {
	var req searchAppCompletePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "question is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "search-app"); err != nil {
		response.Err(c, err)
		return
	}
	searchAppID := c.Param("id")
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(200)
	if err := h.Service.StreamSearchAppCompletion(ctx, tenantID, searchAppID, req.Question, c.GetString("request_id"), false, c.Writer); err != nil {
		// Stream already started; send an OpenAI-style error frame.
		message := "completion request failed"
		if he, ok := err.(*httperr.Error); ok {
			message = he.Message
		}
		_, _ = c.Writer.WriteString("data: {\"code\":500,\"message\":" + jsonQuote(message) + ",\"data\":null}\n\n")
		c.Writer.Flush()
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "search_app.completion.stream", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

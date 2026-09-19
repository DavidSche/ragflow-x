package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type chatPayload struct {
	Name                   string                 `json:"name"`
	DatasetIDs             []string               `json:"dataset_ids"`
	Language               string                 `json:"language"`
	PromptConfig           map[string]interface{} `json:"prompt_config"`
	LLMID                  string                 `json:"llm_id"`
	RerankID               string                 `json:"rerank_id"`
	TopN                   int                    `json:"top_n"`
	TopK                   int                    `json:"top_k"`
	SimilarityThreshold    float64                `json:"similarity_threshold"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight"`
	System                 string                 `json:"system"`
	Prologue               string                 `json:"prologue"`
	EmptyResponse          string                 `json:"empty_response"`
	LLMSetting             map[string]interface{} `json:"llm_setting"`
}

func chatAuthoring(req chatPayload) service.ChatAuthoring {
	a := service.ChatAuthoring{
		Language:               req.Language,
		PromptConfig:           req.PromptConfig,
		LLMID:                  req.LLMID,
		RerankID:               req.RerankID,
		TopN:                   req.TopN,
		TopK:                   req.TopK,
		SimilarityThreshold:    req.SimilarityThreshold,
		VectorSimilarityWeight: req.VectorSimilarityWeight,
		LLMSetting:             req.LLMSetting,
	}
	if req.System != "" || req.Prologue != "" || req.EmptyResponse != "" {
		if a.PromptConfig == nil {
			a.PromptConfig = map[string]interface{}{}
		}
		if req.System != "" {
			a.PromptConfig["system"] = req.System
		}
		if req.Prologue != "" {
			a.PromptConfig["prologue"] = req.Prologue
		}
		if req.EmptyResponse != "" {
			a.PromptConfig["empty_response"] = req.EmptyResponse
		}
	}
	return a
}

// ListChats returns the caller's chat assistants (platform scope=all to list all).
func (h *Handler) ListChats(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	filter := repository.ChatFilter{Name: c.Query("name"), Status: c.Query("status"), OwnerID: c.Query("owner_id")}
	if v := c.Query("min_messages"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.MinMessages = n
		}
	}
	scope, ok := h.resolveGovernanceScope(c, "chat")
	if !ok {
		return
	}
	items, total, err := h.Service.ListChatsForScope(ctx, scope, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// CreateChat creates a chat assistant owned by the caller's tenant.
func (h *Handler) CreateChat(c *gin.Context) {
	var req chatPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid chat payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "chat")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	if policy := activePromptPolicy(h, c, model.PromptScopeChat, ""); policy != nil {
		applyPromptPolicyToChat(&req, policy)
	}
	createPayload, payloadErr := payloadFromRequest(map[string]any{
		"name": req.Name, "dataset_ids": req.DatasetIDs, "authoring": chatAuthoring(req),
	})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectChat, model.ApprovalActionCreate, "new:"+req.Name, createPayload) {
		return
	}
	cs, err := h.Service.CreateChatWithConfig(ctx, tenantID, req.Name, req.DatasetIDs, chatAuthoring(req))
	if err != nil {
		response.Err(c, err)
		return
	}
	cs.OwnerID = userID
	_ = h.Service.SetChatOwner(ctx, cs, userID)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.create", Resource: "chat", ResourceID: cs.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, cs)
}

// GetChat returns a chat assistant.
func (h *Handler) GetChat(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "chat")
	if !ok {
		return
	}
	cs, err := h.Service.GetChatForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, cs)
}

// GetChatConfig returns the RAGFlow-side editable config for prefill.
func (h *Handler) GetChatConfig(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "chat")
	if !ok {
		return
	}
	cfg, err := h.Service.GetChatConfigForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, cfg)
}

// GetChatModelOptions returns RAGFlow's concrete model catalog and defaults so
// the Chat config editor lists real model names for LLM / rerank pickers.
func (h *Handler) GetChatModelOptions(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
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

// UpdateChat updates a chat assistant (name, scope and authoring config).
func (h *Handler) UpdateChat(c *gin.Context) {
	var req chatPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid chat payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "chat")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	chatID := c.Param("id")
	if policy := activePromptPolicy(h, c, model.PromptScopeChat, chatID); policy != nil {
		applyPromptPolicyToChat(&req, policy)
	}
	updatePayload, payloadErr := payloadFromRequest(map[string]any{
		"name": req.Name, "dataset_ids": req.DatasetIDs, "authoring": chatAuthoring(req),
	})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectChat, model.ApprovalActionUpdate, chatID, updatePayload) {
		return
	}
	cs, err := h.Service.UpdateChatWithConfig(ctx, tenantID, chatID, req.Name, req.DatasetIDs, chatAuthoring(req), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.update", Resource: "chat", ResourceID: chatID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, cs)
}

// DeleteChat deletes a chat assistant.
func (h *Handler) DeleteChat(c *gin.Context) {
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "chat")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	chatID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectChat, model.ApprovalActionDelete, chatID, map[string]any{}) {
		return
	}
	if err := h.Service.DeleteChat(ctx, tenantID, chatID, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.delete", Resource: "chat", ResourceID: chatID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": chatID})
}

// ListChatSessions lists the sessions of a chat assistant.
func (h *Handler) ListChatSessions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	sessions, err := h.Service.ListChatSessions(ctx, tenantID, c.Param("id"), false, ragflow.SessionListOptions{Page: page, PageSize: pageSize, Name: c.Query("name")})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, sessions, int64(len(sessions)), page, pageSize)
}

// GetChatSession returns a session with its messages.
func (h *Handler) GetChatSession(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	sess, err := h.Service.GetChatSession(ctx, tenantID, c.Param("id"), c.Param("sessionId"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, sess)
}

// ListChatSessionMessages returns a bounded page of session messages.
func (h *Handler) ListChatSessionMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		limit = 50
	}
	items, next, err := h.Service.ListChatSessionMessages(ctx, tenantID, c.Param("id"), c.Param("sessionId"), false, ragflow.SessionMessagePageOptions{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "next_cursor": next})
}

type createSessionPayload struct {
	Name string `json:"name"`
}

// CreateChatSession creates a session on a chat assistant.
func (h *Handler) CreateChatSession(c *gin.Context) {
	var req createSessionPayload
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	sess, err := h.Service.CreateChatSession(ctx, tenantID, c.Param("id"), req.Name, false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.session.create", Resource: "chat", ResourceID: c.Param("id"), DetailJSON: sess.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, sess)
}

// UpdateChatSession renames a session.
func (h *Handler) UpdateChatSession(c *gin.Context) {
	var req createSessionPayload
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		response.Fail(c, 400, 40000, "name required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	sess, err := h.Service.UpdateChatSession(ctx, tenantID, c.Param("id"), c.Param("sessionId"), req.Name, false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.session.rename", Resource: "chat", ResourceID: c.Param("id"), DetailJSON: sess.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, sess)
}

type deleteSessionPayload struct {
	IDs []string `json:"ids"`
}

// DeleteChatSessions deletes sessions of a chat assistant (batch).
func (h *Handler) DeleteChatSessions(c *gin.Context) {
	var req deleteSessionPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid delete sessions payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	if len(req.IDs) == 0 {
		response.Fail(c, 400, 40000, "ids required")
		return
	}
	if err := h.Service.DeleteChatSessions(ctx, tenantID, c.Param("id"), req.IDs, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.session.delete", Resource: "chat", ResourceID: c.Param("id"), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"deleted": len(req.IDs)})
}

// DeleteChatSession deletes a single session.
func (h *Handler) DeleteChatSession(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	sessionID := c.Param("sessionId")
	if err := h.Service.DeleteChatSessions(ctx, tenantID, c.Param("id"), []string{sessionID}, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.session.delete", Resource: "chat", ResourceID: c.Param("id"), DetailJSON: sessionID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": sessionID})
}

type batchChatStatusPayload struct {
	IDs    []string `json:"ids"`
	Status string   `json:"status"`
}

// BatchUpdateChatStatus enables/disables multiple chats.
func (h *Handler) BatchUpdateChatStatus(c *gin.Context) {
	var req batchChatStatusPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid batch status payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	if len(req.IDs) == 0 || (req.Status != "active" && req.Status != "disabled") {
		response.Fail(c, 400, 40000, "ids and valid status required")
		return
	}
	if err := h.Service.BatchUpdateChatStatus(ctx, tenantID, false, req.IDs, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.status.batch", Resource: "chat", DetailJSON: req.Status, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"updated": len(req.IDs)})
}

// BatchDeleteChats deletes multiple chats.
func (h *Handler) BatchDeleteChats(c *gin.Context) {
	var req deleteSessionPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid delete payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.BatchDeleteChats(ctx, tenantID, req.IDs, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chat.delete.batch", Resource: "chat", DetailJSON: "", IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"deleted": len(req.IDs)})
}

// ChatUsage returns the metered usage/cost of a chat.
func (h *Handler) ChatUsage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	agg, err := h.Service.ChatUsage(ctx, tenantID, c.Param("id"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, agg)
}

package handler

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type agentCreatePayload struct {
	Title          string                 `json:"title"`
	Dsl            map[string]interface{} `json:"dsl"`
	Release        bool                   `json:"release"`
	CanvasCategory string                 `json:"canvas_category"`
}

type agentUpdatePayload struct {
	Title   string                 `json:"title"`
	Dsl     map[string]interface{} `json:"dsl"`
	Release *bool                  `json:"release"`
}

type agentPublishPayload struct {
	Release bool `json:"release"`
}

type agentSessionPayload struct {
	Name string `json:"name"`
}

type agentChatPayload struct {
	SessionID string                   `json:"session_id"`
	Messages  []ragflow.Message        `json:"messages"`
	Files     []map[string]interface{} `json:"files,omitempty"`
}

// ListAgents returns the caller's agents.
func (h *Handler) ListAgents(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	filter := repository.AgentFilter{Title: c.Query("title"), Status: c.Query("status"), OwnerID: c.Query("owner_id"), TenantID: c.Query("tenant_id")}
	scope, ok := h.resolveGovernanceScope(c, "agent")
	if !ok {
		return
	}
	items, total, err := h.Service.ListAgentsForScope(ctx, scope, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// CreateAgent creates an agent owned by the caller's tenant.
func (h *Handler) CreateAgent(c *gin.Context) {
	var req agentCreatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid agent payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "agent")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	if policy := activePromptPolicy(h, c, model.PromptScopeAgent, ""); policy != nil {
		applyPromptPolicyToAgent(&req, policy)
	}
	createPayload, payloadErr := payloadFromRequest(req)
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectAgent, model.ApprovalActionCreate, "new:"+req.Title, createPayload) {
		return
	}
	a, err := h.Service.CreateAgent(ctx, tenantID, req.Title, req.Dsl, req.Release, req.CanvasCategory)
	if err != nil {
		response.Err(c, err)
		return
	}
	a.OwnerID = userID
	_ = h.Service.SetAgentOwner(ctx, a, userID)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.create", Resource: "agent", ResourceID: a.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, a)
}

// GetAgent returns an agent shadow.
func (h *Handler) GetAgent(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "agent")
	if !ok {
		return
	}
	a, err := h.Service.GetAgentForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, a)
}

// GetAgentDetail returns the live RAGFlow-side agent canvas.
func (h *Handler) GetAgentDetail(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "agent")
	if !ok {
		return
	}
	live, err := h.Service.GetAgentDetailForScope(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, live)
}

// UpdateAgent edits an agent (title/dsl/release).
func (h *Handler) UpdateAgent(c *gin.Context) {
	var req agentUpdatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid agent payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "agent")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	updatePayload, payloadErr := payloadFromRequest(req)
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectAgent, model.ApprovalActionUpdate, agentID, updatePayload) {
		return
	}
	a, err := h.Service.UpdateAgent(ctx, tenantID, agentID, req.Title, req.Dsl, req.Release, false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.update", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, a)
}

// PublishAgent toggles an agent's publish/unpublish state.
func (h *Handler) PublishAgent(c *gin.Context) {
	var req agentPublishPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "release is required")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "agent")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectAgent, model.ApprovalActionUpdate, agentID, map[string]any{"release": req.Release}) {
		return
	}
	a, err := h.Service.PublishAgent(ctx, tenantID, agentID, req.Release, false)
	if err != nil {
		response.Err(c, err)
		return
	}
	action := "agent.unpublish"
	if req.Release {
		action = "agent.publish"
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: action, Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, a)
}

// DeleteAgent deletes an agent.
func (h *Handler) DeleteAgent(c *gin.Context) {
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "agent")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectAgent, model.ApprovalActionDelete, agentID, map[string]any{}) {
		return
	}
	if err := h.Service.DeleteAgent(ctx, tenantID, agentID, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.delete", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": agentID})
}

// ListAgentVersions lists an agent's versions.
func (h *Handler) ListAgentVersions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	versions, err := h.Service.ListAgentVersions(ctx, tenantID, c.Param("id"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, versions)
}

// GetAgentVersion returns a single version of an agent.
func (h *Handler) GetAgentVersion(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	ver, err := h.Service.GetAgentVersion(ctx, tenantID, c.Param("id"), c.Param("versionId"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ver)
}

// RollbackAgentVersion restores an agent's DSL from a saved version.
func (h *Handler) RollbackAgentVersion(c *gin.Context) {
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "agent")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectAgent, model.ApprovalActionUpdate, agentID, map[string]any{"rollback_version_id": c.Param("versionId")}) {
		return
	}
	if err := h.Service.RollbackAgentVersion(ctx, tenantID, agentID, c.Param("versionId"), false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.version.rollback", Resource: "agent", ResourceID: agentID, DetailJSON: c.Param("versionId"), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": agentID})
}

// ListAgentSessions lists an agent's sessions.
func (h *Handler) ListAgentSessions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	sessions, total, err := h.Service.ListAgentSessions(ctx, tenantID, c.Param("id"), false, ragflow.SessionListOptions{Page: page, PageSize: pageSize, Keywords: c.Query("keywords")})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, sessions, total, page, pageSize)
}

// CreateAgentSession starts an agent session.
func (h *Handler) CreateAgentSession(c *gin.Context) {
	var req agentSessionPayload
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	sess, err := h.Service.CreateAgentSession(ctx, tenantID, c.Param("id"), req.Name, false)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.session.create", Resource: "agent", ResourceID: c.Param("id"), DetailJSON: sess.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, sess)
}

// GetAgentSession returns an agent session.
func (h *Handler) GetAgentSession(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	sess, err := h.Service.GetAgentSession(ctx, tenantID, c.Param("id"), c.Param("sessionId"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, sess)
}

// ListAgentSessionMessages returns a bounded page of agent session messages.
func (h *Handler) ListAgentSessionMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		limit = 50
	}
	items, next, err := h.Service.ListAgentSessionMessages(ctx, tenantID, c.Param("id"), c.Param("sessionId"), false, ragflow.SessionMessagePageOptions{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "next_cursor": next})
}

// DeleteAgentSession deletes an agent session.
func (h *Handler) DeleteAgentSession(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	sessionID := c.Param("sessionId")
	if err := h.Service.DeleteAgentSession(ctx, tenantID, agentID, sessionID, false); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.session.delete", Resource: "agent", ResourceID: agentID, DetailJSON: sessionID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": sessionID})
}

// UploadAgentFile uploads a temporary attachment for an owned Agent.
func (h *Handler) UploadAgentFile(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Fail(c, 400, 40000, "file is required")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		response.Fail(c, 400, 40000, "empty file")
		return
	}
	if header.Size > 20*1024*1024 {
		response.Fail(c, 400, 40000, "file too large (max 20MB)")
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	uploaded, err := h.Service.UploadAgentFile(ctx, tenantID, agentID, userID, header.Filename, data)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "agent.upload", Resource: "agent",
		ResourceID: agentID, DetailJSON: uploaded.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, uploaded)
}

// AgentChatCompletion runs an agent completion (non-streaming + metered).
func (h *Handler) AgentChatCompletion(c *gin.Context) {
	var req agentChatPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "messages are required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	resp, err := h.Service.AgentChatCompletion(ctx, tenantID, agentID, userID, req.Messages, req.Files, c.GetString("request_id"), req.SessionID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.completion", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, resp)
}

// StreamAgentChatCompletion proxies an agent completion (SSE).
func (h *Handler) StreamAgentChatCompletion(c *gin.Context) {
	var req agentChatPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "messages are required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "agent"); err != nil {
		response.Err(c, err)
		return
	}
	agentID := c.Param("id")
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(200)
	if err := h.Service.StreamAgentChatCompletion(ctx, tenantID, agentID, userID, req.Messages, req.Files, c.GetString("request_id"), req.SessionID, c.Writer); err != nil {
		message := "agent completion failed"
		if he, ok := err.(*httperr.Error); ok {
			message = he.Message
		}
		msg, _ := json.Marshal(message)
		_, _ = c.Writer.WriteString("data: {\"error\":" + string(msg) + "}\n\n")
		c.Writer.Flush()
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "agent.completion.stream", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
}

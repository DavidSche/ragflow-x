package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func (h *Handler) memoryScopeAll(c *gin.Context) bool {
	return c.Query("scope") == "all" && h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.read", "tenant") == nil
}

type memoryCreatePayload struct {
	Name       string   `json:"name"`
	MemoryType []string `json:"memory_type"`
	EmbdID     string   `json:"embd_id"`
	LLMID      string   `json:"llm_id"`
}

type memoryUpdatePayload struct {
	Name             *string  `json:"name,omitempty"`
	Permissions      *string  `json:"permissions,omitempty"`
	LLMID            *string  `json:"llm_id,omitempty"`
	EmbdID           *string  `json:"embd_id,omitempty"`
	MemoryType       []string `json:"memory_type,omitempty"`
	MemorySize       *int     `json:"memory_size,omitempty"`
	ForgettingPolicy *string  `json:"forgetting_policy,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	Description      *string  `json:"description,omitempty"`
	SystemPrompt     *string  `json:"system_prompt,omitempty"`
	UserPrompt       *string  `json:"user_prompt,omitempty"`
}

func (p memoryUpdatePayload) toProvider() ragflow.UpdateMemoryRequest {
	return ragflow.UpdateMemoryRequest{
		Name: p.Name, Permissions: p.Permissions, LLMID: p.LLMID, EmbdID: p.EmbdID,
		MemoryType: p.MemoryType, MemorySize: p.MemorySize, ForgettingPolicy: p.ForgettingPolicy,
		Temperature: p.Temperature, Description: p.Description, SystemPrompt: p.SystemPrompt, UserPrompt: p.UserPrompt,
	}
}

type memoryMessagePayload struct {
	AgentID       string `json:"agent_id"`
	SessionID     string `json:"session_id"`
	UserInput     string `json:"user_input"`
	AgentResponse string `json:"agent_response"`
}

type memoryMessageStatusPayload struct {
	Status bool `json:"status"`
}

// ListMemories returns the caller's memories.
func (h *Handler) ListMemories(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	filter := repository.MemoryFilter{
		Name: c.Query("name"), MemoryType: c.Query("memory_type"), OwnerID: c.Query("owner_id"),
	}
	items, total, err := h.Service.ListMemories(ctx, tenantID, h.memoryScopeAll(c), filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// CreateMemory creates a memory owned by the caller's tenant.
func (h *Handler) CreateMemory(c *gin.Context) {
	var req memoryCreatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid memory payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	mem, err := h.Service.CreateMemory(ctx, tenantID, req.Name, req.MemoryType, req.EmbdID, req.LLMID)
	if err != nil {
		response.Err(c, err)
		return
	}
	mem.OwnerID = userID
	_ = h.Service.SetMemoryOwner(ctx, mem, userID)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.create", Resource: "memory", ResourceID: mem.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, mem)
}

// GetMemory returns a memory.
func (h *Handler) GetMemory(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	mem, err := h.Service.GetMemory(ctx, tenantID, c.Param("id"), h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, mem)
}

// GetMemoryConfig returns the live memory config for the editor.
func (h *Handler) GetMemoryConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	live, err := h.Service.GetMemoryConfig(ctx, tenantID, c.Param("id"), h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, live)
}

// UpdateMemory updates a memory.
func (h *Handler) UpdateMemory(c *gin.Context) {
	var req memoryUpdatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid memory payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	memoryID := c.Param("id")
	mem, err := h.Service.UpdateMemory(ctx, tenantID, memoryID, req.toProvider(), h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.update", Resource: "memory", ResourceID: memoryID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, mem)
}

// DeleteMemory deletes a memory.
func (h *Handler) DeleteMemory(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	memoryID := c.Param("id")
	if err := h.Service.DeleteMemory(ctx, tenantID, memoryID, h.memoryScopeAll(c)); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.delete", Resource: "memory", ResourceID: memoryID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": memoryID})
}

// ListMemoryMessages lists the messages of a memory.
func (h *Handler) ListMemoryMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	msgs, err := h.Service.ListMemoryMessages(ctx, tenantID, c.Param("id"), h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, msgs)
}

// AddMemoryMessage stores a message into a memory.
func (h *Handler) AddMemoryMessage(c *gin.Context) {
	var req memoryMessagePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid message payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	memoryID := c.Param("id")
	if err := h.Service.AddMemoryMessage(ctx, tenantID, memoryID, req.AgentID, req.SessionID, req.UserInput, req.AgentResponse, h.memoryScopeAll(c)); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.message.add", Resource: "memory", ResourceID: memoryID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"ok": true})
}

// DeleteMemoryMessage forgets a memory message.
func (h *Handler) DeleteMemoryMessage(c *gin.Context) {
	messageID, err := strconv.ParseInt(c.Param("messageId"), 10, 64)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid message id")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	memoryID := c.Param("id")
	if err := h.Service.DeleteMemoryMessage(ctx, tenantID, memoryID, messageID, h.memoryScopeAll(c)); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.message.delete", Resource: "memory", ResourceID: memoryID, DetailJSON: strconv.FormatInt(messageID, 10), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"message_id": messageID})
}

// UpdateMemoryMessageStatus sets a message's status.
func (h *Handler) UpdateMemoryMessageStatus(c *gin.Context) {
	messageID, err := strconv.ParseInt(c.Param("messageId"), 10, 64)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid message id")
		return
	}
	var req memoryMessageStatusPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "status is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	memoryID := c.Param("id")
	if err := h.Service.UpdateMemoryMessageStatus(ctx, tenantID, memoryID, messageID, req.Status, h.memoryScopeAll(c)); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "memory.message.status", Resource: "memory", ResourceID: memoryID, DetailJSON: strconv.FormatInt(messageID, 10), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"message_id": messageID, "status": req.Status})
}

// SearchMemoryMessages runs a semantic retrieval against a memory.
func (h *Handler) SearchMemoryMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "execute", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	threshold := 0.2
	weight := 0.7
	topN := 5
	if v := c.Query("threshold"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			threshold = f
		}
	}
	if v := c.Query("weight"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			weight = f
		}
	}
	if v := c.Query("top_n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topN = n
		}
	}
	rows, err := h.Service.SearchMemoryMessages(ctx, tenantID, c.Param("id"), c.Query("query"), threshold, weight, topN, h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: c.GetString(middleware.ContextUserID), Action: "memory.message.search", Resource: "memory", ResourceID: c.Param("id"), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, rows)
}

// GetMemoryMessageContent returns a memory message's full content.
func (h *Handler) GetMemoryMessageContent(c *gin.Context) {
	messageID, err := strconv.ParseInt(c.Param("messageId"), 10, 64)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid message id")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
		response.Err(c, err)
		return
	}
	content, err := h.Service.GetMemoryMessageContent(ctx, tenantID, c.Param("id"), messageID, h.memoryScopeAll(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, content)
}

// MemoryModelOptions returns the tenant's embedding/chat models and defaults so
// the memory create/edit dialogs can pick real models.
func (h *Handler) MemoryModelOptions(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "memory"); err != nil {
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

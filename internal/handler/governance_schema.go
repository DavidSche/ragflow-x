package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ---------------------------------------------------------------
// 受控治理字典（T1 治理元数据注册，doc/224 §2.2）。
// ---------------------------------------------------------------

// ListGovernanceDictionary handles GET /api/v1/governance/dictionary.
// Returns the effective vocabulary (platform builtins + approved tenant
// extensions) grouped by category. Read across governance roles.
func (h *Handler) ListGovernanceDictionary(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	dictionary, err := h.Service.ListGovernanceDictionary(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dictionary)
}

// CreateGovernanceDictionaryEntry handles POST /api/v1/governance/dictionary.
// A tenant extension lands in approval pending (governor decides next).
func (h *Handler) CreateGovernanceDictionaryEntry(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	var req service.GovernanceDictionaryEntryInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid dictionary payload")
		return
	}
	entry, err := h.Service.CreateGovernanceDictionaryEntry(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, entry)
}

// DecideGovernanceDictionaryEntry handles POST /api/v1/governance/dictionary/:id/:decision.
// :decision ∈ {approve, reject}; governor only.
func (h *Handler) DecideGovernanceDictionaryEntry(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	entryID := c.Param("id")
	switch c.Param("decision") {
	case "approve":
		entry, err := h.Service.DecideGovernanceDictionaryEntry(c.Request.Context(), tenantID, userID, entryID, true)
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OK(c, entry)
	case "reject":
		entry, err := h.Service.DecideGovernanceDictionaryEntry(c.Request.Context(), tenantID, userID, entryID, false)
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OK(c, entry)
	default:
		response.Fail(c, 400, 40000, "decision must be approve|reject")
	}
}

// DeleteGovernanceDictionaryEntry handles DELETE /api/v1/governance/dictionary/:id.
// Removes a tenant extension; platform terms are immutable (404).
func (h *Handler) DeleteGovernanceDictionaryEntry(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.DeleteGovernanceDictionaryEntry(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ---------------------------------------------------------------
// 治理 Schema 注册与绑定（T1，doc/224 §2.2）。
// ---------------------------------------------------------------

// ListGovernanceSchemas handles GET /api/v1/governance/schemas.
// Optional ?dataset_id= filter.
func (h *Handler) ListGovernanceSchemas(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	filter := repository.GovernanceSchemaFilter{DatasetID: c.Query("dataset_id")}
	schemas, err := h.Service.ListGovernanceSchemas(c.Request.Context(), tenantID, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, schemas)
}

// GetGovernanceSchema handles GET /api/v1/governance/schemas/:id.
func (h *Handler) GetGovernanceSchema(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	schema, err := h.Service.GetGovernanceSchema(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if schema == nil {
		response.Fail(c, 404, 40441, "governance schema not found")
		return
	}
	response.OK(c, schema)
}

// CreateGovernanceSchema handles POST /api/v1/governance/schemas.
func (h *Handler) CreateGovernanceSchema(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	var req service.GovernanceSchemaInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid governance schema payload")
		return
	}
	schema, err := h.Service.CreateGovernanceSchema(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, schema)
}

// UpdateGovernanceSchema handles PUT /api/v1/governance/schemas/:id.
func (h *Handler) UpdateGovernanceSchema(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	var req service.GovernanceSchemaInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid governance schema payload")
		return
	}
	schema, err := h.Service.UpdateGovernanceSchema(c.Request.Context(), tenantID, userID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, schema)
}

// DeleteGovernanceSchema handles DELETE /api/v1/governance/schemas/:id.
func (h *Handler) DeleteGovernanceSchema(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.DeleteGovernanceSchema(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ---------------------------------------------------------------
// 自动投影（T1，doc/224 §2.2）：schema 匹配 → auto 标注 → 回填。
// ---------------------------------------------------------------

// AutoProjectGovernance handles POST /api/v1/datasets/:id/governance/auto-project.
// governor only (execute:document-governance).
func (h *Handler) AutoProjectGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	result, err := h.Service.AutoProjectGovernance(c.Request.Context(), tenantID, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

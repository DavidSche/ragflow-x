package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// SetDocumentGovernance handles PUT /api/v1/datasets/:id/documents/:docId/governance
// (T2 文档级治理标注; doc/224 §2.1 结构性新建顶点)。
func (h *Handler) SetDocumentGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	documentID := c.Param("docId")
	var req service.DocumentGovernanceInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid governance payload")
		return
	}
	record, err := h.Service.SetDocumentGovernance(c.Request.Context(), tenantID, userID, datasetID, documentID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, record)
}

// GetDocumentGovernance handles GET /api/v1/datasets/:id/documents/:docId/governance.
func (h *Handler) GetDocumentGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	datasetID := c.Param("id")
	documentID := c.Param("docId")
	record, err := h.Service.GetDocumentGovernance(c.Request.Context(), tenantID, datasetID, documentID)
	if err != nil {
		response.Err(c, err)
		return
	}
	if record == nil {
		response.Fail(c, 404, 40421, "document governance not found")
		return
	}
	response.OK(c, record)
}

// DeleteDocumentGovernance handles DELETE /api/v1/datasets/:id/documents/:docId/governance.
func (h *Handler) DeleteDocumentGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	documentID := c.Param("docId")
	if err := h.Service.DeleteDocumentGovernance(c.Request.Context(), tenantID, userID, datasetID, documentID); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ListDocumentGovernance handles GET /api/v1/documents/governance (tenant-wide
// governance ledger, includes the approval workbench filter).
func (h *Handler) ListDocumentGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	filter := repository.DocumentGovernanceFilter{
		DatasetID:     c.Query("dataset_id"),
		DocumentID:    c.Query("document_id"),
		Level:         c.Query("level"),
		Status:        c.Query("status"),
		ApprovalState: c.Query("approval_state"),
	}
	if c.Query("approve_pending") == "1" || c.Query("approve_pending") == "true" {
		filter.ApprovePending = true
	}
	items, total, err := h.Service.ListDocumentGovernance(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// ApproveDocumentGovernance handles
// POST /api/v1/datasets/:id/documents/:docId/governance/:decision
// where :decision ∈ {approve, reject}.
func (h *Handler) ApproveDocumentGovernance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	decision := c.Param("decision") == "approve"
	datasetID := c.Param("id")
	objectID := c.Param("docId")
	record, err := h.Service.ApproveDocumentGovernance(
		c.Request.Context(), tenantID, userID, datasetID, objectID, decision,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, record)
}

// GetUserClearance handles GET /api/v1/users/:id/clearance.
func (h *Handler) GetUserClearance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	targetID := c.Param("id")
	selfID := c.GetString(middleware.ContextUserID)
	if targetID == "" || targetID == "me" {
		targetID = selfID
	}
	rec, err := h.Service.GetUserClearance(c.Request.Context(), tenantID, targetID)
	if err != nil {
		response.Err(c, err)
		return
	}
	level := model.GovernanceLevelInternal
	if rec != nil && rec.Level != "" {
		level = rec.Level
	}
	response.OK(c, gin.H{"user_id": targetID, "level": level, "recorded": rec != nil})
}

// SetUserClearance handles PUT /api/v1/users/:id/clearance (governor only;
// the service re-checks the caller is platform_admin/tenant_admin).
func (h *Handler) SetUserClearance(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	operatorID := c.GetString(middleware.ContextUserID)
	targetID := c.Param("id")
	var req struct {
		Level string `json:"level" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid clearance payload")
		return
	}
	rec, err := h.Service.SetUserClearance(c.Request.Context(), tenantID, targetID, operatorID, req.Level)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rec)
}

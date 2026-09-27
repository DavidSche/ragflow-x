package handler

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func (h *Handler) GetAnswerSnapshot(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	snapshot, projection, err := h.Service.GetAnswerSnapshot(
		c.Request.Context(), userID, tenantID, c.Param("snapshotId"), "web",
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"answer_snapshot":          snapshot,
		"authorization_projection": projection,
	})
}

func (h *Handler) GetAnswerSnapshotByRequest(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	snapshot, projection, err := h.Service.GetAnswerSnapshotByRequest(
		c.Request.Context(), userID, tenantID, c.Param("requestId"), "web",
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"answer_snapshot":          snapshot,
		"authorization_projection": projection,
	})
}

func (h *Handler) CreateAnswerExport(c *gin.Context) {
	var req struct {
		Format         string `json:"format"`
		Scope          string `json:"scope"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40130, "invalid answer export payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	traceID := c.GetString("request_id")
	result, err := h.Service.CreateExportForAnswer(c.Request.Context(), userID, tenantID, service.ExportRequest{
		AnswerSnapshotID: c.Param("snapshotId"),
		Format:           req.Format,
		Scope:            req.Scope,
		IdempotencyKey:   req.IdempotencyKey,
		TraceID:          traceID,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "answer.export", Resource: "answer",
		ResourceID: result.Job.AnswerSnapshotID,
		DetailJSON: fmt.Sprintf(`{"format":%q,"export_job_id":%q}`, result.Job.Format, result.Job.ID),
		IP:         c.ClientIP(), TraceID: traceID,
	})
	response.OK(c, gin.H{"export_job": result.Job, "export_artifact": result.Artifact})
}

// GetAnswerSnapshotAuditExport serves the audit-grade forensic export
// (doc/100 §7.3 contract 2, doc/124 §4). The middleware gate is
// audit-export:read; the handler re-checks per doc/11 §6.1 item 3.
func (h *Handler) GetAnswerSnapshotAuditExport(c *gin.Context) {
	if err := authorizeResource(c, "audit-export", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	traceID := c.GetString("request_id")
	result, err := h.Service.CreateAuditExportForAnswer(c.Request.Context(), userID, tenantID, service.AuditExportInput{
		AnswerSnapshotID: c.Param("snapshotId"),
		IdempotencyKey:   c.Query("idempotency_key"),
		TraceID:          traceID,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "answer.export_audit", Resource: "answer",
		ResourceID: result.Job.AnswerSnapshotID,
		DetailJSON: fmt.Sprintf(`{"format":%q,"export_job_id":%q,"audit_scope":"full","policy_input_hash":%q}`,
			result.Job.Format, result.Job.ID, result.Job.PolicyInputHash),
		IP: c.ClientIP(), TraceID: traceID,
	})
	response.OK(c, gin.H{"export_job": result.Job, "export_artifact": result.Artifact})
}

func (h *Handler) GetAnswerExportJob(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	job, err := h.Service.GetExportJob(c.Request.Context(), userID, tenantID, c.Param("jobId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"export_job": job})
}

func (h *Handler) RetryAnswerExportJob(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	traceID := c.GetString("request_id")
	result, err := h.Service.RetryExportJob(c.Request.Context(), userID, tenantID, c.Param("jobId"), traceID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "answer.export.retry", Resource: "answer",
		ResourceID: result.Job.AnswerSnapshotID,
		DetailJSON: fmt.Sprintf(`{"format":%q,"export_job_id":%q}`, result.Job.Format, result.Job.ID),
		IP:         c.ClientIP(), TraceID: traceID,
	})
	response.OK(c, gin.H{"export_job": result.Job, "export_artifact": result.Artifact})
}

func (h *Handler) DownloadAnswerExport(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	traceID := c.GetString("request_id")
	download, err := h.Service.OpenExportArtifact(c.Request.Context(), userID, tenantID, c.Param("artifactId"), traceID)
	if err != nil {
		response.Err(c, err)
		return
	}
	content, err := os.ReadFile(download.Path)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=\""+download.Artifact.Filename+"\"")
	c.Data(200, download.Artifact.MimeType, content)
}

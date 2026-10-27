package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func (h *Handler) CreateKnowledgeImpactReport(c *gin.Context) {
	var req struct {
		ProjectID     string `json:"project_id"`
		TargetType    string `json:"target_type"`
		TargetID      string `json:"target_id"`
		TargetVersion string `json:"target_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40140, "invalid knowledge impact payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	report, err := h.Service.CreateKnowledgeImpactReport(c.Request.Context(), userID, tenantID, req.ProjectID, req.TargetType, req.TargetID, req.TargetVersion)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "knowledge-impact.create",
		Resource: "knowledge-ops", ResourceID: report.ID, DetailJSON: report.ImpactLevel,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, report)
}

func (h *Handler) ListKnowledgeImpactReports(c *gin.Context) {
	page, pageSize := pageParams(c)
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	reports, total, err := h.Service.ListKnowledgeImpactReports(c.Request.Context(), userID, tenantID, page, pageSize, repository.KnowledgeImpactFilter{
		TargetType: c.Query("target_type"), TargetID: c.Query("target_id"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, reports, total, page, pageSize)
}

func (h *Handler) CreateDuplicateCandidate(c *gin.Context) {
	var req service.DuplicateCandidateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40141, "invalid duplicate candidate payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	candidate, err := h.Service.CreateDuplicateCandidate(c.Request.Context(), userID, tenantID, c.Query("project_id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "knowledge-duplicate.create",
		Resource: "knowledge-ops", ResourceID: candidate.ID, DetailJSON: fmt.Sprintf("%f", candidate.SimilarityScore),
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, candidate)
}

func (h *Handler) ListDuplicateCandidates(c *gin.Context) {
	page, pageSize := pageParams(c)
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	candidates, total, err := h.Service.ListDuplicateCandidates(c.Request.Context(), userID, tenantID, page, pageSize, repository.KnowledgeImpactFilter{
		Status: c.Query("status"), Relation: c.Query("relation"), SourceType: c.Query("source_type"),
		SourceID: c.Query("source_id"), CandidateType: c.Query("candidate_type"), CandidateID: c.Query("candidate_id"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, candidates, total, page, pageSize)
}

func (h *Handler) DecideDuplicateCandidate(c *gin.Context) {
	var req service.DuplicateCandidateDecision
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40142, "invalid duplicate decision payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	candidate, err := h.Service.DecideDuplicateCandidate(c.Request.Context(), userID, tenantID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "knowledge-duplicate.decide",
		Resource: "knowledge-ops", ResourceID: candidate.ID, DetailJSON: candidate.FinalRelation,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, candidate)
}

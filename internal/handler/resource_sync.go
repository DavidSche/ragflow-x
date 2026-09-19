package handler

import (
	"github.com/gin-gonic/gin"

	"encoding/json"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func (h *Handler) ResourceSyncSettingView(c *gin.Context) {
	setting, err := h.Service.GetResourceSyncSetting(c.Request.Context(), c.GetString(middleware.ContextTenantID))
	if err != nil {
		response.Err(c, err)
		return
	}
	view, err := service.NewResourceSyncSettingView(setting)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func recordResourceSyncAudit(h *Handler, c *gin.Context, action, resourceID, detail string) {
	_ = h.Service.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: c.GetString(middleware.ContextUserID),
		ActorTenantID: c.GetString(middleware.ContextTenantID), TargetTenantID: c.GetString(middleware.ContextTenantID),
		Action: action, Resource: "ragflow-sync", ResourceID: resourceID, DetailJSON: detail,
		AuthorizationPermission: "manage:ragflow-sync", IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
}

func (h *Handler) ResourceSyncSettingUpdate(c *gin.Context) {
	var request service.ResourceSyncSettingUpdate
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	setting, err := h.Service.UpdateResourceSyncSetting(c.Request.Context(), c.GetString(middleware.ContextTenantID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	view, err := service.NewResourceSyncSettingView(setting)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) ListResourceSyncTenantMappings(c *gin.Context) {
	items, err := h.Service.ListTenantMappings(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

func (h *Handler) UpsertResourceSyncTenantMapping(c *gin.Context) {
	var request service.ResourceSyncMappingRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	mapping, err := h.Service.UpsertTenantMapping(c.Request.Context(), c.GetString(middleware.ContextUserID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, mapping)
}

func (h *Handler) DeleteResourceSyncTenantMapping(c *gin.Context) {
	if err := h.Service.DeleteTenantMapping(c.Request.Context(), c.Param("externalTenantId")); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

func (h *Handler) ScanResourceSync(c *gin.Context) {
	var request struct {
		ResourceTypes []string `json:"resource_types"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	run, err := h.Service.ScanResourceSync(c.Request.Context(), c.GetString(middleware.ContextUserID), request.ResourceTypes)
	if err != nil {
		response.Err(c, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"resource_types": request.ResourceTypes})
	recordResourceSyncAudit(h, c, "ragflow_sync.scan.initiated", run.ID, string(detail))
	response.OK(c, run)
}

func (h *Handler) ListResourceSyncRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceSyncRuns(c.Request.Context(), page, pageSize, c.Query("status"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) GetResourceSyncRun(c *gin.Context) {
	item, err := h.Service.GetResourceSyncRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func (h *Handler) ListResourceSyncRunItems(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceSyncItems(c.Request.Context(), c.Param("id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) ImportResourceSync(c *gin.Context) {
	var request service.ResourceSyncImportRequest
	if err := c.ShouldBindJSON(&request); err != nil && err.Error() != "EOF" {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	run, err := h.Service.ImportResourceSync(c.Request.Context(), c.Param("id"), c.GetString(middleware.ContextUserID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"run_id": run.ID, "async": request.Async})
	recordResourceSyncAudit(h, c, "ragflow_sync.import.initiated", run.ID, string(detail))
	response.OK(c, run)
}

func (h *Handler) AssignResourceSyncItems(c *gin.Context) {
	var request service.ResourceSyncItemAssignmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	result, err := h.Service.AssignResourceSyncItems(
		c.Request.Context(), c.Param("id"), request,
		c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func (h *Handler) ReconcileResourceSync(c *gin.Context) {
	var request struct {
		ResourceTypes []string `json:"resource_types"`
	}
	_ = c.ShouldBindJSON(&request)
	run, err := h.Service.ReconcileResourceSync(c.Request.Context(), c.GetString(middleware.ContextUserID), model.SyncTriggerManualReconcile, request.ResourceTypes)
	if err != nil {
		response.Err(c, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"resource_types": request.ResourceTypes})
	recordResourceSyncAudit(h, c, "ragflow_sync.reconcile.initiated", run.ID, string(detail))
	response.OK(c, run)
}

func (h *Handler) ListResourceSyncResources(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceSyncResources(c.Request.Context(), c.Query("type"), c.Query("lifecycle"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) ListResourceSyncRelinkTenantOptions(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceSyncRelinkTenantOptions(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Query("scope"), c.Query("query"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) ListResourceSyncRelinkResourceOptions(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceSyncRelinkResourceOptions(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Query("type"), c.Query("scope"), c.Query("tenant_id"), c.Query("query"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) ListResourceBindingVersions(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListResourceBindingVersions(c.Request.Context(), c.Param("type"), c.Param("externalTenantId"), c.Param("id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) GetResourceSyncArtifact(c *gin.Context) {
	artifact, err := h.Service.GetResourceSyncArtifact(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, artifact)
}

func (h *Handler) ResolveResourceSyncConflict(c *gin.Context) {
	var request service.ResourceSyncConflictRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	item, err := h.Service.ResolveResourceSyncConflict(
		c.Request.Context(), c.Param("type"), c.Param("externalTenantId"), c.Param("id"), request,
		c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func (h *Handler) RelinkResourceSync(c *gin.Context) {
	var request service.ResourceSyncRelinkRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40000, "invalid request")
		return
	}
	item, err := h.Service.RelinkResourceSync(
		c.Request.Context(), c.Param("type"), c.Param("externalTenantId"), c.Param("id"), request,
		c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

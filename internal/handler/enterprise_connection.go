package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func pageAndSize(c *gin.Context) (int, int) { return pageParams(c) }

func (h *Handler) ListEnterpriseConnections(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	page, pageSize := pageAndSize(c)
	var tenantIDs []string
	if !scope.ScopeAll() {
		tenantIDs = scope.AllowedTenantIDs
	}
	if c.Query("cursor") != "" {
		list, err := h.Service.ListEnterpriseConnectionsByCursor(c.Request.Context(), tenantIDs, c.Query("provider"), c.Query("lifecycle_status"), c.Query("cursor"), pageSize)
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OK(c, list)
		return
	}
	views, total, err := h.Service.ListEnterpriseConnections(c.Request.Context(), tenantIDs, c.Query("provider"), c.Query("lifecycle_status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, views, total, page, pageSize)
}

func (h *Handler) CreateEnterpriseConnection(c *gin.Context) {
	var req service.CreateEnterpriseConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40060, "invalid enterprise connection payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseConnection, model.ApprovalActionCreate, "new:"+req.ProviderName, payload) {
		return
	}
	view, err := h.Service.CreateEnterpriseConnection(ctx, userID, tenantID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "enterprise-connection.create",
		Resource: "enterprise-connection", ResourceID: view.Connection.ID,
		DetailJSON: view.Version.ConnectionConfigHash, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) GetEnterpriseConnection(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.GetEnterpriseConnectionForScope(c.Request.Context(), scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) UpdateEnterpriseConnection(c *gin.Context) {
	var req service.UpdateEnterpriseConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40060, "invalid enterprise connection payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	payload["connection_id"] = c.Param("id")
	if h.approvalHold(c, model.ApprovalObjectEnterpriseConnection, model.ApprovalActionUpdate, c.Param("id"), payload) {
		return
	}
	view, err := h.Service.UpdateEnterpriseConnection(ctx, userID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "enterprise-connection.update",
		Resource: "enterprise-connection", ResourceID: view.Connection.ID,
		DetailJSON: view.Version.ConnectionConfigHash, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) ListEnterpriseConnectionVersions(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	if _, err := h.Service.GetEnterpriseConnectionForScope(c.Request.Context(), scope, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	views, err := h.Service.ListEnterpriseConnectionVersions(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, views)
}

func (h *Handler) RotateEnterpriseConnectionCredential(c *gin.Context) {
	var req service.RotateEnterpriseConnectionCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40069, "invalid credential rotation payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseConnection, model.ApprovalActionRotateCredential, c.Param("id"), payload) {
		return
	}
	view, err := h.Service.RotateEnterpriseConnectionCredential(ctx, c.Param("id"), userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: userID,
		Action: "enterprise-connection.rotate-credential", Resource: "enterprise-connection",
		ResourceID: view.Connection.ID,
		DetailJSON: view.Version.CredentialVersion, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) RetireEnterpriseConnection(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseConnection, model.ApprovalActionRetire, c.Param("id"), map[string]any{}) {
		return
	}
	view, err := h.Service.RetireEnterpriseConnection(ctx, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: userID,
		Action: "enterprise-connection.retire", Resource: "enterprise-connection",
		ResourceID: view.Connection.ID, DetailJSON: view.Connection.LifecycleStatus,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) DeprecateEnterpriseConnection(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseConnection, model.ApprovalActionDeprecate, c.Param("id"), map[string]any{}) {
		return
	}
	view, err := h.Service.DeprecateEnterpriseConnection(ctx, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: userID,
		Action: "enterprise-connection.deprecate", Resource: "enterprise-connection",
		ResourceID: view.Connection.ID, DetailJSON: view.Connection.LifecycleStatus,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) TestEnterpriseConnection(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "test", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.TestEnterpriseConnection(ctx, scope, c.Param("id"), userID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: userID,
		Action: "enterprise-connection.test", Resource: "enterprise-connection",
		ResourceID: view.Connection.ID,
		DetailJSON: enterpriseHealthSummary(view.Latest), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) GetEnterpriseConnectionHealth(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.GetEnterpriseConnectionHealth(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) GetEnterpriseConnectionAvailability(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.GetEnterpriseConnectionAvailability(ctx, scope, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func enterpriseHealthSummary(check *model.EnterpriseConnectionHealthCheck) string {
	if check == nil {
		return ""
	}
	return check.Health + ":" + strconv.Itoa(check.HTTPStatus) + ":" + strconv.FormatInt(check.LatencyMS, 10) + "ms"
}

func (h *Handler) ListEnterpriseBindings(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	page, pageSize := pageAndSize(c)
	var tenantIDs []string
	if !scope.ScopeAll() {
		tenantIDs = scope.AllowedTenantIDs
	}
	views, total, err := h.Service.ListEnterpriseBindings(c.Request.Context(), tenantIDs, c.Param("id"), c.Query("lifecycle_status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, views, total, page, pageSize)
}

func (h *Handler) CreateEnterpriseBinding(c *gin.Context) {
	var req service.CreateEnterpriseBindingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40064, "invalid enterprise binding payload")
		return
	}
	req.ConnectionID = c.Param("id")
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseBinding, model.ApprovalActionBind, c.Param("id"), payload) {
		return
	}
	view, err := h.Service.CreateEnterpriseBinding(ctx, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "enterprise-binding.create",
		Resource: "enterprise-connection", ResourceID: view.Binding.BindingID,
		DetailJSON: view.Binding.TenantID, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) UpdateEnterpriseBinding(c *gin.Context) {
	var req service.UpdateEnterpriseBindingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40064, "invalid enterprise binding payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	payload["connection_id"] = c.Param("id")
	if h.approvalHold(c, model.ApprovalObjectEnterpriseBinding, model.ApprovalActionUpdate, c.Param("bindingId"), payload) {
		return
	}
	view, err := h.Service.UpdateEnterpriseBinding(ctx, userID, c.Param("bindingId"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "enterprise-binding.update",
		Resource: "enterprise-connection", ResourceID: view.Binding.BindingID,
		DetailJSON: strconv.FormatInt(view.Binding.CurrentBindingVersion, 10), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) GetEnterpriseBinding(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.GetEnterpriseBindingForScope(c.Request.Context(), scope, c.Param("bindingId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) RevokeEnterpriseBinding(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHold(c, model.ApprovalObjectEnterpriseBinding, model.ApprovalActionRevoke, c.Param("bindingId"), map[string]any{
		"connection_id": c.Param("id"),
	}) {
		return
	}
	view, err := h.Service.RevokeEnterpriseBinding(ctx, userID, c.Param("bindingId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "enterprise-binding.revoke",
		Resource: "enterprise-connection", ResourceID: view.Binding.BindingID,
		DetailJSON: view.Binding.LifecycleStatus, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) ListEnterpriseBindingVersions(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "enterprise-connection"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "enterprise-connection")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	if _, err := h.Service.GetEnterpriseBindingForScope(c.Request.Context(), scope, c.Param("bindingId")); err != nil {
		response.Err(c, err)
		return
	}
	views, err := h.Service.ListEnterpriseBindingVersions(c.Request.Context(), c.Param("bindingId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, views)
}

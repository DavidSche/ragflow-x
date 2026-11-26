package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type createAPIKeyRequest struct {
	Name         string                `json:"name" binding:"required"`
	ExpiresAt    *time.Time            `json:"expires_at"`
	TokenQuota   *int64                `json:"token_quota"`   // monthly token budget; nil/0 = unlimited
	RequestQuota *int64                `json:"request_quota"` // monthly request budget; nil/0 = unlimited
	Scopes       *service.APIKeyScopes `json:"scopes"`
	AllowedIPs   *[]string             `json:"allowed_ips"`
}

// ListAPIKeys lists the tenant's gateway keys.
func (h *Handler) ListAPIKeys(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "api-key"); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListAPIKeys(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// CreateAPIKey creates a gateway key and returns the raw secret once.
func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req createAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid api key payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "api-key"); err != nil {
		response.Err(c, err)
		return
	}
	var tokenQuota int64
	if req.TokenQuota != nil {
		tokenQuota = *req.TokenQuota
	}
	var requestQuota int64
	if req.RequestQuota != nil {
		requestQuota = *req.RequestQuota
	}
	payload := map[string]any{"name": req.Name, "token_quota": tokenQuota, "request_quota": requestQuota, "scopes": req.Scopes, "allowed_ips": req.AllowedIPs}
	if req.ExpiresAt != nil {
		payload["expiry_at"] = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if h.approvalHold(c, model.ApprovalObjectAPIKey, model.ApprovalActionCreate, "new:"+req.Name, payload) {
		return
	}
	raw, key, err := h.Service.CreateAPIKey(ctx, tenantID, userID, req.Name, req.ExpiresAt, tokenQuota, requestQuota, req.Scopes, req.AllowedIPs)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "api-key.create", Resource: "api-key", ResourceID: key.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, CreateAPIKeyResponse{Key: key, Secret: raw})
}

type rotateAPIKeyRequest struct {
	// GraceHours is how long the retired key keeps working. Omitted = server
	// default; the service clamps whatever it gets, because an unbounded grace is an
	// unbounded lifetime for a credential that was supposed to be replaced.
	GraceHours *int `json:"grace_hours"`
}

// RotateAPIKey issues a successor key and retires the old one after a grace window
// (doc/301 GW-4). The raw secret is returned exactly once, like create.
func (h *Handler) RotateAPIKey(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "api-key"); err != nil {
		response.Err(c, err)
		return
	}
	keyID := c.Param("id")
	// The body is optional: "rotate with the default grace" is the common call and
	// must not require a JSON payload. Only a *malformed* body is a 400 — an absent
	// one is a valid request.
	var req rotateAPIKeyRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Fail(c, 400, 40000, "invalid api key rotation payload")
			return
		}
	}
	payload := map[string]any{}
	if req.GraceHours != nil {
		payload["grace_hours"] = *req.GraceHours
	}
	// Rotation is gated by the same policy as create/revoke: it mints a credential, so a
	// workspace that requires approval to create a key must approve this too, or
	// rotation becomes the way around its own policy.
	if h.approvalHold(c, model.ApprovalObjectAPIKey, model.ApprovalActionRotateCredential, keyID, payload) {
		return
	}
	raw, successor, retired, err := h.Service.RotateAPIKey(ctx, tenantID, keyID, req.GraceHours)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "api-key.rotate", Resource: "api-key",
		ResourceID: keyID,
		DetailJSON: `{"successor_id":"` + successor.ID + `"}`,
		IP:         c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, RotateAPIKeyResponse{Key: successor, Secret: raw, Retired: retired})
}

// RevokeAPIKey disables a tenant's gateway key.
func (h *Handler) RevokeAPIKey(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "api-key"); err != nil {
		response.Err(c, err)
		return
	}
	keyID := c.Param("id")
	if h.approvalHold(c, model.ApprovalObjectAPIKey, model.ApprovalActionRevoke, keyID, map[string]any{}) {
		return
	}
	if err := h.Service.RevokeAPIKey(ctx, tenantID, keyID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "api-key.revoke", Resource: "api-key", ResourceID: keyID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, RevokeAPIKeyResponse{ID: keyID, Enabled: false})
}

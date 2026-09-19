// Package handler is the thin HTTP layer: parse input, call a service,
// render a response.
package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/rsaseal"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// Handler bundles dependencies for HTTP handlers.
type Handler struct {
	Service  *service.Service
	rsa      *rsaseal.KeyPair
	setupMgr SetupApplier
}

// SetupApplier is the subset of the setup manager the first-run wizard needs on
// the main router: exposing the RSA public key and applying the submitted config.
// Declared as an interface so handler does not import setup (no import cycle).
type SetupApplier interface {
	PublicKeyPEM() string
	ApplyRaw(ctx context.Context, raw []byte) error
	ConfigView() (map[string]interface{}, error)
	PreflightRaw(ctx context.Context, raw []byte) (map[string]interface{}, error)
}

// New builds a Handler.
func New(svc *service.Service) *Handler {
	return &Handler{Service: svc}
}

// RecordAudit writes an audit entry and logs the failure when the underlying
// write fails, so audit gaps are observable instead of silently discarded.
func (h *Handler) RecordAudit(ctx context.Context, entry *model.AuditLog) {
	if err := h.Service.RecordAudit(ctx, entry); err != nil {
		logger.Warn("audit write failed",
			"tenant_id", entry.TenantID, "user_id", entry.UserID,
			"action", entry.Action, "resource", entry.Resource,
			"resource_id", entry.ResourceID, "error", err,
		)
	}
}

// RequireAudit writes a governance-sensitive audit record and fails closed if
// persistence fails; it must be called before returning governed data.
func (h *Handler) RequireAudit(ctx context.Context, entry *model.AuditLog) error {
	return h.Service.RecordAudit(ctx, entry)
}

// SetSetupRSA injects the RSA private key used to decrypt password fields
// submitted by the first-run setup wizard.
func (h *Handler) SetSetupRSA(kp *rsaseal.KeyPair) { h.rsa = kp }

// SetSetupManager injects the first-run setup manager so the main router can
// serve the RSA public key and apply the wizard configuration (hot-swap).
func (h *Handler) SetSetupManager(m SetupApplier) { h.setupMgr = m }

// Health is an unauthenticated liveness endpoint.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

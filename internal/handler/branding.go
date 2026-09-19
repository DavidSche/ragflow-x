package handler

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// PublicBranding returns platform branding for unauthenticated pages (login).
func (h *Handler) PublicBranding(c *gin.Context) {
	response.OK(c, h.Service.PublicBranding(c.Request.Context()))
}

// GetBranding returns the caller's effective tenant branding.
func (h *Handler) GetBranding(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "branding"); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, h.Service.GetBranding(c.Request.Context(), tenantID))
}

// SetBranding updates the caller tenant's branding.
func (h *Handler) SetBranding(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Logo string `json:"logo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid branding payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "branding"); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.SetBranding(ctx, tenantID, req.Name, req.Logo); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "branding.set", Resource: "branding", ResourceID: tenantID, DetailJSON: req.Name, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, h.Service.GetBranding(ctx, tenantID))
}

// UploadBrandingLogo accepts an image upload, stores it under the data dir, and
// returns the served URL which is then persisted as the tenant logo.
func (h *Handler) UploadBrandingLogo(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "branding"); err != nil {
		response.Err(c, err)
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Fail(c, 400, 40000, "file is required")
		return
	}
	defer file.Close()
	if header.Size > 2*1024*1024 {
		response.Fail(c, 400, 40000, "logo file too large (max 2MB)")
		return
	}
	ext := sanitizeExt(filepath.Ext(header.Filename))
	if ext == "" {
		response.Fail(c, 400, 40000, "unsupported image type")
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		response.Err(c, err)
		return
	}
	logosDir := filepath.Join(h.Service.DataDir, "logos")
	if err := os.MkdirAll(logosDir, 0o755); err != nil {
		response.Err(c, err)
		return
	}
	filename := id.New() + ext
	if err := os.WriteFile(filepath.Join(logosDir, filename), data, 0o644); err != nil {
		response.Err(c, err)
		return
	}
	url := "/api/v1/static/logos/" + filename
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "branding.logo", Resource: "branding", ResourceID: tenantID, DetailJSON: url, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"url": url})
}

func sanitizeExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".svg", ".webp", ".gif":
		return strings.ToLower(ext)
	default:
		return ""
	}
}

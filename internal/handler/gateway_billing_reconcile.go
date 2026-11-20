package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// Gateway billing reconciliation surface (doc/301 §5.10 GW-3 / T21).
//
// Four thin handlers and no decisions: who may call, which day, which tenant and what
// a run means all live below this file. A handler that re-derived any of it would be a
// second answer waiting to disagree with the first.

// ReconcileGatewayBilling runs the daily check on demand for one UTC day (default: the
// previous completed day). It is `manage/system` because it walks every tenant's
// ledgers, not one tenant's.
func (h *Handler) ReconcileGatewayBilling(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "system"); err != nil {
		response.Err(c, err)
		return
	}
	day := c.Query("day")
	reconciled, tenants, err := h.Service.ReconcileGatewayBillingAllDay(c.Request.Context(), day)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"day": reconciled, "tenants": tenants})
}

// GatewayBillingReconcileRuns lists the caller's tenant's recent daily runs.
func (h *Handler) GatewayBillingReconcileRuns(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "14"))
	runs, err := h.Service.TenantGatewayBillingReconcileRuns(c.Request.Context(), tenantID, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"runs": runs})
}

// GatewayBillingReconcileFindings pages one run's disagreements, optionally by kind.
func (h *Handler) GatewayBillingReconcileFindings(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	findings, err := h.Service.GatewayBillingReconcileFindings(
		c.Request.Context(), tenantID, c.Param("runId"), c.Query("kind"), limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"findings": findings})
}

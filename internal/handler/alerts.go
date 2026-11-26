package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func alertScope(h *Handler, c *gin.Context, requestedTenant string) (string, bool) {
	if h.Service.KnowledgeOpsScopeAll(c.Request.Context(), c.GetString(middleware.ContextUserID)) {
		return requestedTenant, true
	}
	return c.GetString(middleware.ContextTenantID), false
}

// ListAlerts returns the persisted operational alert worklist.
func (h *Handler) ListAlerts(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "alert"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	tenantID, scopeAll := alertScope(h, c, c.Query("tenant_id"))
	out, total, err := h.Service.ListAlerts(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.AlertFilter{
		Type:     c.Query("type"),
		Severity: c.Query("severity"),
		Status:   c.DefaultQuery("status", model.AlertStatusOpen),
		Search:   c.Query("search"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	// The worklist renders a person for "处理人": resolve acked_by to a name and
	// keep the raw ID as the fallback for deleted users.
	ids := make([]string, 0, len(out))
	for _, alert := range out {
		if alert.AckedBy != "" {
			ids = append(ids, alert.AckedBy)
		}
	}
	names := h.Service.AlertActorNames(c.Request.Context(), ids)
	rows := make([]alertRow, len(out))
	for i, alert := range out {
		rows[i] = alertRow{AlertEvent: alert, AckedByName: names[alert.AckedBy]}
	}
	response.OKPage(c, rows, total, page, pageSize)
}

// alertRow is the worklist row: the persisted alert plus the resolved name of
// whoever claimed it.
type alertRow struct {
	model.AlertEvent
	AckedByName string `json:"acked_by_name"`
}

// ListAlertDeliveries returns the restartable outbound delivery lifecycle.
func (h *Handler) ListAlertDeliveries(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "alert"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	tenantID, scopeAll := alertScope(h, c, c.Query("tenant_id"))
	out, total, err := h.Service.ListAlertDeliveries(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.AlertDeliveryFilter{
		Channel: c.Query("channel"),
		Status:  c.Query("status"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

// RetryAlertDelivery manually redelivers one pending/failed/abandoned channel.
func (h *Handler) RetryAlertDelivery(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "alert"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := alertScope(h, c, c.Query("tenant_id"))
	summary, err := h.Service.RetryAlertDelivery(
		c.Request.Context(), tenantID, scopeAll,
		c.Param("id"), c.Param("channel"),
		c.GetString(middleware.ContextUserID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, RetryAlertDeliveryResponse{Summary: summary})
}

// MarkAlertRead marks an alert as read.
func (h *Handler) MarkAlertRead(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "alert"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := alertScope(h, c, c.Query("tenant_id"))
	if err := h.Service.UpdateAlertStatus(c.Request.Context(), tenantID, c.Param("id"), scopeAll, repository.AlertReview{
		Status: model.AlertStatusRead,
		Actor:  c.GetString(middleware.ContextUserID),
	}); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, MarkAlertReadResponse{ID: c.Param("id"), Status: model.AlertStatusRead})
}

// ClaimAlert claims an alert for operations follow-up.
func (h *Handler) ClaimAlert(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "alert"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := alertScope(h, c, c.Query("tenant_id"))
	if err := h.Service.UpdateAlertStatus(c.Request.Context(), tenantID, c.Param("id"), scopeAll, repository.AlertReview{
		Status: model.AlertStatusClaimed,
		Actor:  c.GetString(middleware.ContextUserID),
	}); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ClaimAlertResponse{ID: c.Param("id"), Status: model.AlertStatusClaimed})
}

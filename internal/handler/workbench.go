package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

func (h *Handler) RecentCrossAppSessions(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 {
		response.Fail(c, http.StatusBadRequest, 40000, "invalid limit")
		return
	}
	sessions, err := h.Service.ListRecentCrossAppSessions(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.Query("search"), limit,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, sessions)
}

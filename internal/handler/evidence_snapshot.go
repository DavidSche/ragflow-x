package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func evidenceSnapshotContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listEvidenceSnapshots(c *gin.Context) {
	tenantID, userID := evidenceSnapshotContext(c)
	page, pageSize := pageParams(c)
	filter := repository.EvidenceSnapshotFilter{
		EvalCaseID:   c.Query("eval_case_id"),
		StaleStatus:  c.Query("stale_status"),
		SnapshotHash: c.Query("snapshot_hash"),
	}
	items, total, err := handlerFrom(c).Service.ListEvidenceSnapshots(
		c.Request.Context(), tenantID, userID, filter, page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func getEvidenceSnapshot(c *gin.Context) {
	tenantID, userID := evidenceSnapshotContext(c)
	bundle, err := handlerFrom(c).Service.GetEvidenceSnapshot(
		c.Request.Context(), tenantID, userID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, bundle)
}

func RegisterEvidenceSnapshotRoutes(group *gin.RouterGroup) {
	group.GET("/evidence-snapshots", listEvidenceSnapshots)
	group.GET("/evidence-snapshots/:id", getEvidenceSnapshot)
}

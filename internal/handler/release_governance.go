package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func releaseGovernanceContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func bind[T any](c *gin.Context) (T, bool) {
	var input T
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Fail(c, 400, 40070, "invalid request body")
		return input, false
	}
	return input, true
}

func handlerFrom(c *gin.Context) *Handler {
	return c.MustGet("handler").(*Handler)
}

func listReleaseCandidates(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListReleaseCandidates(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listExecutionSnapshots(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListExecutionSnapshots(c.Request.Context(), tenantID, c.Query("candidate_id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listEvaluationRuns(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListEvaluationRuns(c.Request.Context(), tenantID, c.Query("candidate_id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listEvidenceBundles(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListEvidenceBundles(c.Request.Context(), tenantID, c.Query("candidate_id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listReleaseGates(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListGateDecisions(
		c.Request.Context(), tenantID, c.Query("candidate_id"), parseCandidateVersion(c.Query("candidate_version")), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listReleases(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListReleases(c.Request.Context(), tenantID, c.Query("candidate_id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createReleaseCandidate(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.ReleaseCandidateInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateReleaseCandidate(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "release_candidate.create", "release-governance", item.ID, item.CandidateHash)
	response.OK(c, item)
}

func markReleaseCandidateReady(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	if err := handlerFrom(c).Service.MarkReleaseCandidateReady(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "release_candidate.ready", "release-governance", c.Param("id"), "")
	response.OK(c, gin.H{"status": "READY_FOR_EVALUATION"})
}

func createExecutionSnapshot(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.ExecutionSnapshotInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateExecutionSnapshot(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "execution_snapshot.create", "release-governance", item.ID, item.SnapshotHash)
	response.OK(c, item)
}

func createEvaluationSetVersion(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.EvaluationSetVersionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateEvaluationSetVersion(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func createEvaluationCaseVersion(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.EvaluationCaseVersionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateEvaluationCaseVersion(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func createEvaluationRun(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.EvaluationRunInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateEvaluationRun(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func addEvaluationCaseResult(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.EvaluationCaseResultInput](c)
	if !ok {
		return
	}
	input.RunID = c.Param("id")
	item, err := handlerFrom(c).Service.AddEvaluationCaseResult(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func completeEvaluationRun(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	input, ok := bind[struct {
		Failed bool   `json:"failed"`
		Reason string `json:"reason"`
	}](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CompleteEvaluationRun(c.Request.Context(), tenantID, c.Param("id"), input.Failed, input.Reason)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func createEvidenceBundle(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.EvidenceBundleInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateEvidenceBundle(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func evaluateReleaseGate(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.GateDecisionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.EvaluateReleaseGate(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "release_gate.decide", "release-governance", item.ID, item.Decision)
	response.OK(c, item)
}

func createRelease(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.ReleaseInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateRelease(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "release.create", "release-governance", item.ID, item.Status)
	response.OK(c, item)
}

func completeRelease(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	input, ok := bind[struct {
		Failed bool `json:"failed"`
	}](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CompleteRelease(c.Request.Context(), tenantID, c.Param("id"), input.Failed)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func rollbackRelease(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[struct {
		Reason string `json:"reason"`
	}](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.RollbackRelease(c.Request.Context(), tenantID, userID, c.Param("id"), input.Reason)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "release.rollback", "release-governance", c.Param("id"), item.ID)
	response.OK(c, item)
}

func listQualityIssues(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListQualityIssues(c.Request.Context(), tenantID, c.Query("status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createQualityIssue(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.QualityIssueInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateQualityIssue(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "quality_issue.create", "release-governance", item.ID, item.Status)
	response.OK(c, item)
}

func updateQualityIssue(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.QualityIssueStateInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateQualityIssue(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "quality_issue.update", "release-governance", item.ID, item.Status)
	response.OK(c, item)
}

func verifyQualityIssue(c *gin.Context) {
	tenantID, _ := releaseGovernanceContext(c)
	input, ok := bind[struct {
		RunID string `json:"run_id"`
	}](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.VerifyQualityIssue(c.Request.Context(), tenantID, c.Param("id"), input.RunID)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "quality_issue.verify", "release-governance", item.ID, item.Status)
	response.OK(c, item)
}

func RegisterReleaseGovernanceRoutes(group *gin.RouterGroup, handler *Handler) {
	group.Use(func(c *gin.Context) {
		c.Set("handler", handler)
		c.Next()
	})
	group.GET("/release-candidates", listReleaseCandidates)
	group.GET("/execution-snapshots", listExecutionSnapshots)
	group.POST("/release-candidates", createReleaseCandidate)
	group.POST("/release-candidates/:id/ready", markReleaseCandidateReady)
	group.POST("/execution-snapshots", createExecutionSnapshot)
	group.POST("/evaluation-set-versions", createEvaluationSetVersion)
	group.POST("/evaluation-case-versions", createEvaluationCaseVersion)
	group.POST("/evaluation-runs", createEvaluationRun)
	group.POST("/evaluation-runs/:id/case-results", addEvaluationCaseResult)
	group.POST("/evaluation-runs/:id/complete", completeEvaluationRun)
	group.GET("/evaluation-runs", listEvaluationRuns)
	group.POST("/evidence-bundles", createEvidenceBundle)
	group.GET("/evidence-bundles", listEvidenceBundles)
	group.POST("/release-gates", evaluateReleaseGate)
	group.GET("/release-gates", listReleaseGates)
	group.POST("/releases", createRelease)
	group.GET("/releases", listReleases)
	group.POST("/releases/:id/complete", completeRelease)
	group.POST("/releases/:id/rollback", rollbackRelease)
	group.GET("/quality-issues", listQualityIssues)
	group.POST("/quality-issues", createQualityIssue)
	group.POST("/quality-issues/:id/state", updateQualityIssue)
	group.POST("/quality-issues/:id/verify", verifyQualityIssue)
}

func parseCandidateVersion(value string) int64 {
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 0 {
		return 0
	}
	return version
}

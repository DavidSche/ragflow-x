package handler

// The assurance surface of a report revision (doc/301 §7.1): trigger, history,
// single run, review projection and finding adjudication.
//
// Five handlers, and each one is thin for the same reason: the service owns every
// decision (who may trigger, what policy was frozen, whether the allowance covers a
// call), and a handler that re-derived any of them would be a second answer waiting
// to disagree with the first.

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// reportRevisionParam parses `:revision` as a positive revision number. A
// non-positive value is 44105 rather than "latest": a client that asked for
// `revision=0` after a regenerate must not silently get a different revision than
// the one it meant.
func reportRevisionParam(c *gin.Context) (int64, bool) {
	revision, err := strconv.ParseInt(strings.TrimSpace(c.Param("revision")), 10, 64)
	if err != nil || revision <= 0 {
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return 0, false
	}
	return revision, true
}

// postReportAssuranceRun starts one run (T06).
//
// The `Idempotency-Key` header is required and its absence is 44114, decided in the
// service so the same rule covers every caller. 202, not 200: the run exists and has
// not measured anything yet, and a 200 would invite a client to read
// `release_eligibility` from a run that cannot have one (it is `pending`).
func postReportAssuranceRun(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision, ok := reportRevisionParam(c)
	if !ok {
		return
	}
	input, ok := bind[service.ReportAssuranceTriggerInput](c)
	if !ok {
		return
	}
	view, err := handlerFrom(c).Service.TriggerReportAssurance(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision,
		c.GetHeader("Idempotency-Key"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	if view.Replayed {
		// A replay answers 200: the run already existed, so nothing was accepted now.
		// The `replayed` flag is in the body as well, because a client may have
		// forgotten the header and still be looking at a stale run.
		response.OK(c, view)
		return
	}
	response.AcceptedAt(c, "/api/v1/reports/"+view.ReportID+"/revisions/"+strconv.FormatInt(view.Revision, 10)+
		"/assurance-runs/"+view.ID, view)
}

// listReportAssuranceRuns is the history read (T06). The limit is a page size with a
// hard ceiling, not a caller-controlled scan: a revision accumulates runs, and
// "give me all of them" is not a request a dashboard needs.
func listReportAssuranceRuns(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision, ok := reportRevisionParam(c)
	if !ok {
		return
	}
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			response.Fail(c, 400, 44105, "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	runs, err := handlerFrom(c).Service.ListReportAssuranceRuns(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision, limit,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ListReportAssuranceRunsResponse{Items: runs})
}

// getReportAssuranceRun is the single-run read. It is polled by the workbench while
// a run is `queued`/`running`, so it returns the counters as they stand: a progress
// read that answered "not ready" would force the client to invent a poll cadence.
func getReportAssuranceRun(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision, ok := reportRevisionParam(c)
	if !ok {
		return
	}
	view, err := handlerFrom(c).Service.GetReportAssuranceRun(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision, c.Param("runId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// getReportReview is the workbench projection (T08/T09). Pagination is by cursor
// (`section_cursor`, `finding_cursor`), never by offset: an offset shifts when a
// partial retry rewrites a section, and the reviewer would silently scroll past
// claims.
func getReportReview(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision, ok := reportRevisionParam(c)
	if !ok {
		return
	}
	view, err := handlerFrom(c).Service.ReportReview(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision,
		strings.TrimSpace(c.Query("run_id")),
		strings.TrimSpace(c.Query("section_cursor")),
		strings.TrimSpace(c.Query("finding_cursor")),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// patchReportReviewFinding adjudicates one finding (T09). Owner-only, decided in the
// service; the handler's job is only to pass the three body fields through.
func patchReportReviewFinding(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision, ok := reportRevisionParam(c)
	if !ok {
		return
	}
	input, ok := bind[service.ReportFindingDecision](c)
	if !ok {
		return
	}
	view, err := handlerFrom(c).Service.UpdateReviewFinding(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision,
		c.Param("findingId"), input.Status, input.Note, input.ResolvedRevisionID,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

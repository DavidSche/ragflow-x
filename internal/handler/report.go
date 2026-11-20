package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// reportContext reads the two things every report handler needs. The tenant always
// comes from the session, never from the request: a report id is user-supplied, so
// a tenant in the body would be a second, weaker authority next to the RBAC rule.
func reportContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

// createReport starts one report run.
//
// The `Idempotency-Key` header is request identity, not a content fingerprint
// (doc/288 §7.7): the same key must return the same report and job, a different
// key must be allowed to ask the same question again. The service owns the
// validation (44114 for missing/malformed) and the replay, so the handler passes
// the raw header through and reports whatever the service decided — including
// `created=false` on a replay, which the UI needs to distinguish from a fresh run.
//
// 202, not 200: the aggregate exists but the content does not. Answering 200
// would claim a finished report and make the first `GET /reports/:id` look like a
// regression.
func createReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	input, ok := bind[service.CreateReportRequest](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.CreateReport(
		c.Request.Context(), userID, tenantID, c.GetHeader("Idempotency-Key"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.AcceptedAt(c, "/api/v1/reports/"+result.Report.ID, result)
}

// listReports returns the caller's own live reports. The owner narrowing is the
// service's decision, not a query parameter: P0-A has no shared reports, so
// `?owner_user_id=` would be an invitation to add one without a policy.
//
// `?status=` selects the lifecycle view (doc/297 §2.3): absent means `active`, so an
// archived report leaves the daily list by default. The value is passed through
// unvalidated and the service answers 400/44105 for anything outside its closed set —
// the same split every other report parameter uses (shape is the service's contract).
func listReports(c *gin.Context) {
	tenantID, userID := reportContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListReports(
		c.Request.Context(), userID, tenantID, page, pageSize,
		strings.TrimSpace(c.Query("status")),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// archiveReportRequest is the archive body. `reason` is free text for the audit
// trail and optional for the same reason the delete body's is: a required field the
// caller has nothing to say about gets filled with "x".
type archiveReportRequest struct {
	Reason string `json:"reason"`
}

// archiveReport puts a finished report away (doc/297 §2). 200 with the detail view:
// the state changed on a resource that already existed, and the client's next action
// is to render it — the same answer `restore` gives.
func archiveReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	var input archiveReportRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			response.Fail(c, 400, 40070, "invalid request body")
			return
		}
	}
	detail, err := handlerFrom(c).Service.ArchiveReport(
		c.Request.Context(), userID, tenantID, c.Param("id"), input.Reason,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

// unarchiveReport returns a report to the daily list. No body: the only thing to say
// is "not archived any more".
func unarchiveReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	detail, err := handlerFrom(c).Service.UnarchiveReport(
		c.Request.Context(), userID, tenantID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

func getReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	detail, err := handlerFrom(c).Service.GetReport(
		c.Request.Context(), userID, tenantID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

// deleteReportRequest is the soft-delete body. `reason` is free text that ends up
// in the audit trail; it is not required, because forcing an operator to type
// something to archive their own report produces "x" in the audit log.
type deleteReportRequest struct {
	Reason string `json:"reason"`
}

// deleteReport closes a report and opens its 7-day recovery window.
func deleteReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	// An empty body is legal (a plain DELETE), so binding is skipped rather than
	// failing with 40070 on a request that has nothing to say.
	var input deleteReportRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			response.Fail(c, 400, 40070, "invalid request body")
			return
		}
	}
	// `force=true` skips the 7-day recovery window (A47). It is a query parameter
	// rather than a body field because it changes what the DELETE *means* (reversible
	// or not), and a body-shaped flag is the kind of thing a client copies along with
	// the `reason` and never notices. The service additionally requires
	// `manage:report` for it; the route action stays `delete:own`.
	force := false
	if raw := strings.TrimSpace(c.Query("force")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			response.Fail(c, 400, 44105, "force must be true or false")
			return
		}
		force = parsed
	}
	if err := handlerFrom(c).Service.SoftDeleteReport(
		c.Request.Context(), userID, tenantID, c.Param("id"), input.Reason, force,
	); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true, "recoverable": !force})
}

// restoreReport re-opens a soft-deleted report inside its recovery window (A47).
//
// 200 with the report detail: the restore has already happened by the time this
// answers, so there is nothing asynchronous to acknowledge. 409/44104 means the
// window closed (or the row is not deleted), which is a state answer, not a missing
// route — that is why it is not a 404.
func restoreReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	detail, err := handlerFrom(c).Service.RestoreReport(
		c.Request.Context(), userID, tenantID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

// cancelReport records the user's cancel intent for the report's current job.
//
// 202 rather than 200: the answer is "your intent is recorded", not "the report is
// cancelled". The terminal state is derived by finalize (§8.4), so a 200 here would
// be a promise the server cannot keep yet.
func cancelReport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	result, err := handlerFrom(c).Service.CancelReport(
		c.Request.Context(), userID, tenantID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.AcceptedAt(c, "/api/v1/reports/"+result.ReportID, result)
}

// listReportTemplates returns one row per template key (workspace fork first,
// platform baseline otherwise) with its full payload.
func listReportTemplates(c *gin.Context) {
	tenantID, userID := reportContext(c)
	views, err := handlerFrom(c).Service.ListReportTemplatesView(
		c.Request.Context(), userID, tenantID,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	// The catalog is small, bounded by the number of template keys, so it is a
	// bare list rather than a paged envelope — the same shape as /projects.
	response.OK(c, views)
}

// capabilityCheckReportTemplate probes the supplied datasets against the report
// template's knowledge contract. Read-only, mirroring the scenario-wizard check:
// it discloses verdicts (satisfied / repairable / probe_limited / blocked) and
// never repairs anything. Only `blocked` blocks creation — the create-time
// recheck re-runs the same evaluation server-side and answers 44102 then.
func capabilityCheckReportTemplate(c *gin.Context) {
	if err := authorizeResource(c, "report-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.CapabilityCheckInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid capability check payload")
		return
	}
	tenantID, userID := reportContext(c)
	view, err := handlerFrom(c).Service.CheckReportTemplateCapabilities(
		c.Request.Context(), userID, tenantID, c.Param("key"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// cloneReportTemplate forks the platform baseline into the workspace's private
// template version sequence (§11.4 / D4). The fork keeps its own version numbers
// and is never overwritten by a later platform upgrade; re-forking after an
// upgrade mints the workspace's next version instead. `created=false` on the
// body means the request replayed an already-committed identical fork.
// publishReportTemplate creates a workspace-authored template version (doc/295
// R19). POST is the create form; PUT :key is the edit form, and both land on the
// same service call because "edit" means "publish version N+1" — there is no
// in-place update of a version a report may already have pinned.
func publishReportTemplate(c *gin.Context) {
	republishReportTemplate(c, "")
}

func updateReportTemplate(c *gin.Context) {
	republishReportTemplate(c, c.Param("key"))
}

func republishReportTemplate(c *gin.Context, key string) {
	if err := authorizeResource(c, "report-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.ReportTemplatePublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// A malformed body is the caller's structural error, the same code the
		// outline and reuse validators answer with (44105 / 400).
		response.Fail(c, 400, 44105, "template payload must be a JSON object")
		return
	}
	if key != "" {
		if req.Key != "" && req.Key != key {
			// The path is the address; a body key that disagrees would publish under
			// a name the caller did not address.
			response.Fail(c, 400, 44105, "template key in body must match the path")
			return
		}
		req.Key = key
	}
	tenantID, userID := reportContext(c)
	result, err := handlerFrom(c).Service.PublishReportTemplate(
		c.Request.Context(), userID, tenantID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

// retireReportTemplate takes the newest workspace version out of service. It is
// not a delete: the row stays so historical reports keep their provenance.
func retireReportTemplate(c *gin.Context) {
	if err := authorizeResource(c, "report-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID := reportContext(c)
	result, err := handlerFrom(c).Service.RetireReportTemplate(
		c.Request.Context(), userID, tenantID, c.Param("key"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func cloneReportTemplate(c *gin.Context) {
	if err := authorizeResource(c, "report-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID := reportContext(c)
	result, err := handlerFrom(c).Service.CloneReportTemplate(
		c.Request.Context(), userID, tenantID, c.Param("key"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

// confirmReportOutline records the reviewer's outline and arms the writing attempt.
//
// The revision is in the path rather than inferred from "the latest revision": a
// confirmation is addressed to one specific structure, and inferring it would let a
// client that lagged behind a regeneration approve the wrong outline.
//
// 202, not 200: sections exist but no content does. The same reasoning as
// `createReport` — 200 would claim a written report.
func confirmReportOutline(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revisionNo, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revisionNo <= 0 {
		// 44105 is the report layer's "the structure you addressed is not valid"; the
		// number is inlined because `report_codes.go` keeps its constants unexported.
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return
	}
	input, ok := bind[service.ConfirmReportOutlineRequest](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ConfirmReportOutline(
		c.Request.Context(), userID, tenantID, c.Param("id"), revisionNo, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.AcceptedAt(c, "/api/v1/reports/"+result.ReportID, result)
}

// createReportExport freezes one revision into a downloadable artifact (T11,
// doc/288 §7.7 / §12). The format comes from the body; there is deliberately no
// client-supplied idempotency key here, because the server derives it from the
// frozen content — the same revision exported twice is the same artifact (§7.7
// rule 2), so a duplicate click can never mint two versions.
func createReportExport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revisionNo, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revisionNo <= 0 {
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return
	}
	input, ok := bind[service.ReportExportRequest](c)
	if !ok {
		return
	}
	input.ReportID = c.Param("id")
	input.Revision = revisionNo
	input.TraceID = c.GetString("request_id")
	result, err := handlerFrom(c).Service.CreateReportExport(
		c.Request.Context(), userID, tenantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"export_job":      result.Job,
		"export_artifact": result.Artifact,
		"canonical_hash":  result.Canonical,
		// Server-made decision (doc/288 §7.7): the panel's download button follows
		// this flag instead of re-deriving readiness from the row.
		"downloadable": result.Downloadable,
	})
}

// downloadReportExport streams one frozen artifact. The route carries report id +
// revision + artifact id (§11.4): the id alone would leak through the access
// layer, and addressing the artifact under its revision means a stale URL fails
// on the revision, not on a guessable artifact id.
func downloadReportExport(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revisionNo, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revisionNo <= 0 {
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return
	}
	artifact, err := handlerFrom(c).Service.OpenReportArtifact(
		c.Request.Context(), userID, tenantID, c.Param("id"), revisionNo, c.Param("artifactId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	content, err := os.ReadFile(artifact.FileRef)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=\""+artifact.Filename+"\"")
	c.Data(http.StatusOK, artifact.MimeType, content)
}

// createReportRevision starts one new run on an already-published report.
//
// Same conditions, same report: it mints revision `current+1` from the published
// base and a fresh `report_generate` job in one transaction (doc/288 §7.10). The
// `Idempotency-Key` header is required (44114) and lives in its OWN namespace — a
// creation key can never replay here, and a regeneration key can never collide
// with the key that minted the report (§7.10 "不受创建幂等键拦截"). There is no
// body: P0-A's "重新生成（同条件）" has nothing a caller may change.
func createReportRevision(c *gin.Context) {
	tenantID, userID := reportContext(c)
	// The body is optional and only names the sections to inherit (doc/288 §9.8).
	// An absent body is the P0-B "same conditions" rerun; a malformed one is a 400
	// rather than a silently ignored reuse request.
	var req service.RegenerateReportRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Fail(c, 400, 44105, "reuse_sections must be a list of section numbers")
			return
		}
	}
	result, err := handlerFrom(c).Service.RegenerateReport(
		c.Request.Context(), userID, tenantID, c.Param("id"), c.GetHeader("Idempotency-Key"), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	location := "/api/v1/reports/" + c.Param("id") +
		"/revisions/" + strconv.FormatInt(result.Revision.Revision, 10)
	response.AcceptedAt(c, location, result)
}

// getReportSchedule answers the report's recurring plan, or `{"schedule":null}`
// when it has none. A null is not a 404: "this report does not run on a timer" is a
// successful answer to "when does it run", and a client should not have to catch an
// error to render an empty card.
func getReportSchedule(c *gin.Context) {
	tenantID, userID := reportContext(c)
	view, err := handlerFrom(c).Service.GetReportSchedule(
		c.Request.Context(), userID, tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"schedule": view})
}

// putReportSchedule creates or replaces the plan (idempotent on the report). 201 on
// the first arming, 200 afterwards, so a client can tell "armed" from "changed".
func putReportSchedule(c *gin.Context) {
	tenantID, userID := reportContext(c)
	var req service.ReportScheduleRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Fail(c, 400, 44105, "invalid schedule body")
			return
		}
	}
	view, created, err := handlerFrom(c).Service.UpsertReportSchedule(
		c.Request.Context(), userID, tenantID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if created {
		response.Created(c, view)
		return
	}
	response.OK(c, view)
}

// deleteReportSchedule removes the plan. 204 either way: the caller's intent was
// "this report does not run on a timer", and after the call it does not. Deleting
// twice is therefore not an error, which keeps a retried request harmless.
func deleteReportSchedule(c *gin.Context) {
	tenantID, userID := reportContext(c)
	if _, err := handlerFrom(c).Service.DeleteReportSchedule(
		c.Request.Context(), userID, tenantID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	c.Status(204)
}

// listReportSections is the reading surface: one revision's sections with bodies.
//
// Without it a report could only be read as an exported file, which is why the
// centre had a drawer with statuses and no text (doc/44 「好用」 gap #4).
func listReportSections(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revisionNo, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revisionNo <= 0 {
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return
	}
	sections, err := handlerFrom(c).Service.ListReportSections(
		c.Request.Context(), userID, tenantID, c.Param("id"), revisionNo,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"sections": sections})
}

// listReportRevisions answers the report's version history (doc/288 §7.10).
//
// A superseded revision is reachable today only by guessing its number: the detail
// page resolves `current_revision_id`, and nothing told a client which other
// numbers exist. That made A41's "historical revisions stay readable" true of the
// API and false of the product.
func listReportRevisions(c *gin.Context) {
	tenantID, userID := reportContext(c)
	rows, err := handlerFrom(c).Service.ListReportRevisions(
		c.Request.Context(), userID, tenantID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"revisions": rows})
}

// getReportRevision reads "second revision of report X" by its number, the
// historical-run view a client refreshes after reconnecting. The report row is
// carried in the answer (same shape as GET /reports/:id) so a caller paging
// through a report's revisions never has to fetch the report twice.
func getReportRevision(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revisionNo, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revisionNo <= 0 {
		response.Fail(c, 400, 44105, "revision must be a positive number")
		return
	}
	detail, err := handlerFrom(c).Service.GetReportRevisionByNumber(
		c.Request.Context(), userID, tenantID, c.Param("id"), revisionNo,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

// getReportRetention reads the effective report TTLs for this workspace (T20b).
//
// The tenant is taken from the session, never from the path: a `:tenantId`
// parameter would turn "which numbers apply to you" into an enumeration oracle for
// other workspaces' retention policies, and the caller has exactly one workspace.
func getReportRetention(c *gin.Context) {
	tenantID, _ := reportContext(c)
	view, err := handlerFrom(c).Service.ReportRetention(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// putReportAccess replaces one report's sharing policy (doc/296 §2.1).
//
// `execute:report` is the route's RBAC gate — sharing is an action on a report you
// own, not a governance decision — and the row-level rule (owner, or platform
// `manage`) lives in the service, so a colleague's report answers 44109 rather than
// an existence oracle.
//
// The response is the new sharing view, not 204: the panel must show what the server
// actually stored (a grantee the server skipped, a policy it normalised) instead of
// echoing what the client sent.
func putReportAccess(c *gin.Context) {
	tenantID, userID := reportContext(c)
	var request service.SetReportAccessRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		// 44105: the body itself is wrong, which is a 400, not the 409 that a
		// disallowed state gets.
		response.Fail(c, 400, 44105, "invalid report access payload")
		return
	}
	view, err := handlerFrom(c).Service.SetReportAccess(
		c.Request.Context(), userID, tenantID, c.Param("id"), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// postReportBudgetExtension asks for more budget on a running report (doc/296 §2.2).
//
// `execute:report` is the gate and ownership is the row-level rule, the same union the
// rest of the surface uses: raising a limit is an action on your own run, not a
// governance decision. When the workspace has an approval policy the answer is 200 with
// `status=pending_approval` — the request was accepted and nothing has changed yet,
// which is a different thing from a failure and must not be reported as one.
func postReportBudgetExtension(c *gin.Context) {
	tenantID, userID := reportContext(c)
	var request service.ReportBudgetExtensionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 44105, "invalid report budget extension payload")
		return
	}
	view, err := handlerFrom(c).Service.RequestReportBudgetExtension(
		c.Request.Context(), userID, tenantID, c.Param("id"), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// putReportRetention sets the tenant-level overlay. `manage` is the right action:
// retention decides when compliance evidence disappears, which is governance, not
// a per-user preference (`execute` is not enough).
func putReportRetention(c *gin.Context) {
	tenantID, userID := reportContext(c)
	var request service.TenantReportPolicyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		// 44105, not 44104: 44104 is a 409 "the state forbids this", and a body that
		// does not parse is a 400 "the request itself is wrong" (§8.7).
		response.Fail(c, 400, 44105, "invalid report retention payload")
		return
	}
	view, err := handlerFrom(c).Service.SetTenantReportPolicy(
		c.Request.Context(), userID, tenantID, request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// getReportMetricsTrend answers A15: the stored per-revision quality verdicts of a
// window, rolled up by day or week (doc/288 §14.1).
//
// The query is parsed permissively — an absent `days`/`bucket`/`view` takes the
// documented default — but a *present and wrong* value is refused with 44105 instead
// of being coerced: a caller that asked for 400 days and silently received 180 would
// chart a window it did not request. 44105 is the report surface's payload code; it
// is not reused for a model or total failure (§8.7).
func getReportMetricsTrend(c *gin.Context) {
	tenantID, userID := reportContext(c)
	request := service.ReportMetricsTrendRequest{
		Bucket:      c.Query("bucket"),
		View:        c.Query("view"),
		TemplateKey: c.Query("template_key"),
	}
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil {
			response.Fail(c, 400, 44105, "days must be an integer")
			return
		}
		if days < 1 {
			// `days=0` is refused at the edge rather than read as "absent": the service
			// uses 0 for "caller did not ask", and letting an explicit zero fall through
			// would answer a 30-day window to someone who asked for none.
			response.Fail(c, 400, 44105, "days must be at least 1")
			return
		}
		request.Days = days
	}
	view, err := handlerFrom(c).Service.ReportMetricsTrend(
		c.Request.Context(), userID, tenantID, request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// getReportFollowUps answers A14's read half: the chips a reader may click to start
// the next turn.
//
// `revision` absent/0 means "whatever this report currently shows", resolved from
// `current_revision_id` by the service — never "the highest number". A present but
// non-positive revision is refused with 44105 instead of being read as "current":
// a client that asked for `revision=0` after a regenerate would silently get the
// published base (or the running draft) and never learn it asked for nothing.
func getReportFollowUps(c *gin.Context) {
	tenantID, userID := reportContext(c)
	revision := int64(0)
	if raw := strings.TrimSpace(c.Query("revision")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			response.Fail(c, 400, 44105, "revision must be a positive number")
			return
		}
		revision = value
	}
	view, err := handlerFrom(c).Service.ReportFollowUpSuggestions(
		c.Request.Context(), userID, tenantID, c.Param("id"), revision,
		strings.TrimSpace(c.Query("section_key")))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// RegisterReportRoutes wires the P0-A/P0-B report surface. The remaining §11.4
// routes (capability-check) land with the capability-probe slice; registering a
// route before its handler exists would be a 500 waiting to happen.
func RegisterReportRoutes(group *gin.RouterGroup) {
	group.POST("/reports", createReport)
	group.GET("/reports", listReports)
	group.GET("/reports/:id", getReport)
	group.DELETE("/reports/:id", deleteReport)
	group.POST("/reports/:id/restore", restoreReport)
	// Lifecycle (doc/297 §2): putting a report away and taking it back out. Two verbs
	// rather than one `PUT ?archived=`, because "archive" and "unarchive" are the two
	// sentences the audit log needs, and a boolean in a body is the kind of flag a
	// client copies along with something else.
	group.POST("/reports/:id/archive", archiveReport)
	group.POST("/reports/:id/unarchive", unarchiveReport)
	group.POST("/reports/:id/cancel", cancelReport)
	group.POST("/reports/:id/revisions", createReportRevision)
	group.GET("/reports/:id/revisions", listReportRevisions)
	group.GET("/reports/:id/revisions/:revision", getReportRevision)
	group.GET("/reports/:id/revisions/:revision/sections", listReportSections)
	// A14 follow-up closed loop: the suggestion chips under a section. Cheap, derived
	// from stored facts and cached by the client per revision — see
	// service.ReportFollowUpSuggestions for why this is not a model call.
	group.GET("/reports/:id/follow-ups", getReportFollowUps)
	// A recurring plan (doc/295 §2.4 / R20): one per report, addressed by report id.
	group.GET("/reports/:id/schedule", getReportSchedule)
	group.PUT("/reports/:id/schedule", putReportSchedule)
	group.DELETE("/reports/:id/schedule", deleteReportSchedule)
	// Sharing (doc/296 §2.1): the four access policies of §13. Read is part of the
	// detail aggregate; only the write has its own route.
	group.PUT("/reports/:id/access", putReportAccess)
	// The one governed decision of this surface: raising a frozen token envelope.
	group.POST("/reports/:id/budget-extension", postReportBudgetExtension)
	group.POST("/reports/:id/revisions/:revision/outline/confirm", confirmReportOutline)
	group.POST("/reports/:id/revisions/:revision/artifacts", createReportExport)
	// Assurance surface (doc/301 §7.1). Five routes, all scoped by revision because
	// an audit is a statement about one immutable text: a run addressed only by
	// report id would leave "which revision was reviewed" to a body field.
	//
	// Read routes are `read:report`; triggering and adjudicating are `execute` plus
	// an owner check in the service (44109 for a grantee), so no new permission row
	// or role is introduced (doc/301 D10).
	group.POST("/reports/:id/revisions/:revision/assurance-runs", postReportAssuranceRun)
	group.GET("/reports/:id/revisions/:revision/assurance-runs", listReportAssuranceRuns)
	group.GET("/reports/:id/revisions/:revision/assurance-runs/:runId", getReportAssuranceRun)
	group.GET("/reports/:id/revisions/:revision/review", getReportReview)
	group.PATCH("/reports/:id/revisions/:revision/findings/:findingId", patchReportReviewFinding)
	group.GET("/reports/:id/revisions/:revision/artifacts/:artifactId/download", downloadReportExport)
	group.GET("/report-templates", listReportTemplates)
	group.POST("/report-templates/:key/capability-check", capabilityCheckReportTemplate)
	group.POST("/report-templates/:key/clone", cloneReportTemplate)
	// R19 authoring: a workspace may publish its own versions and retire the
	// newest one. Both are `manage:report-template`; the platform baseline is not
	// editable through these routes (that is a release, not a runtime action).
	group.POST("/report-templates", publishReportTemplate)
	group.PUT("/report-templates/:key", updateReportTemplate)
	group.POST("/report-templates/:key/retire", retireReportTemplate)
	// Job surface (§11.4 / T5): the snapshot carries `event_seq`, and the stream
	// replays from `?after=`. Both are `read:report`; the row-level owner check lives
	// in the service, so a colleague's job id is 44109 rather than an existence
	// oracle. The section retry (§8.7) is `execute`: it is the one repair channel
	// of a published-but-partial report and mints a `section_retry` job.
	group.GET("/report-jobs/:jobId", getReportJob)
	group.GET("/report-jobs/:jobId/events", streamReportJobEvents)
	group.POST("/report-jobs/:jobId/sections/:ord/retry", retryReportSection)
	// Retention policy (T20b): the tenant-level overlay on the four report clocks.
	// Read is `read:report`; writing it is `manage:report` (tenant_admin and above).
	group.GET("/report-retention", getReportRetention)
	group.PUT("/report-retention", putReportRetention)
	// A15 quality trend: `read:report`, scoped to the caller's own reports unless
	// `scope=tenant` (which the service gates on `manage:report`). It sits under
	// `/report-metrics` rather than `/reports/metrics` so the path cannot be confused
	// with a report id — no route in this surface should ever need to prove that a
	// static segment wins over `:id`.
	group.GET("/report-metrics/trend", getReportMetricsTrend)
}

// retryReportSection is the single-section repair route (§8.7 / §11.4, P0-B).
// The job and ord name one failed section of the current published revision; the
// service mints a `section_retry` job, marks the old row superseded and writes a
// fresh pending section row. The response body carries both the new job and the
// new section (with `job_kind`/`rewrite_reason`) so a client can render in-flight
// state without a follow-up GET.
func retryReportSection(c *gin.Context) {
	tenantID, userID := reportContext(c)
	ord, err := strconv.Atoi(c.Param("ord"))
	if err != nil || ord <= 0 {
		// 44105 is the report layer's boundary-addressing error, the same code a
		// malformed revision number gets; the report_codes.go constant is
		// unexported, so it is inlined here as everywhere in this file.
		response.Fail(c, 400, 44105, "section ord must be a positive number")
		return
	}
	result, err := handlerFrom(c).Service.RetryReportSection(
		c.Request.Context(), userID, tenantID, c.Param("jobId"), ord)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

// getReportJob returns the live snapshot of one report job: the truth a client
// refreshes with, plus the `event_seq` cursor to resume the stream from.
//
// The job is addressed by its own id rather than "the report's latest job" because
// a client that reconnects is talking about one specific run: inferring it would let
// a snapshot of run #2 be paired with a cursor from run #1.
func getReportJob(c *gin.Context) {
	tenantID, userID := reportContext(c)
	snapshot, err := handlerFrom(c).Service.GetReportJobSnapshot(
		c.Request.Context(), userID, tenantID, c.Param("jobId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, snapshot)
}

// reportStreamHeartbeat and reportStreamPoll are variables rather than constants so
// the handler tests can drive the loop without sleeping for 15 seconds. Production
// values are §8.3's: a 15s comment frame keeps proxies from closing an idle stream,
// a 500ms poll bounds the delay between "event committed" and "event delivered"
// when there is no in-memory broadcast to hook into.
var (
	reportStreamHeartbeat = 15 * time.Second
	reportStreamPoll      = 500 * time.Millisecond
)

// reportEventCursor reads the resume cursor. `?after=` is the primary protocol
// (§8.3): the frontend uses `fetch` + a `data:`-line parser, so the browser never
// sends `Last-Event-ID` on its own. The header is accepted as a fallback for
// EventSource clients and proxies that do send it.
//
// A value that is not a non-negative integer is treated as "no cursor" (full
// replay) rather than an error: replaying from the start is always a correct
// answer, and a 400 would need a new code in the frozen 44101–44116 set.
func reportEventCursor(c *gin.Context) int64 {
	raw := strings.TrimSpace(c.Query("after"))
	if raw == "" {
		raw = strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	}
	if raw == "" {
		return 0
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		return 0
	}
	return cursor
}

// streamReportJobEvents is the SSE endpoint (§8.3 / T5).
//
// The stream reads the event table, never an in-memory bus: a broadcast that only
// exists in RAM would lose the events a reconnect is asking about, which is the
// whole point of the sequence.
//
// Frames follow §8.8 exactly — `id: <seq>`, `event: report.job`, one `data:` line
// carrying `seq/type/job_id/revision_id/at/payload`. `seq` is in the body because
// the only parser the frontend has reads `data:` lines.
func streamReportJobEvents(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, userID := reportContext(c)
	jobID := c.Param("jobId")

	stream, err := handlerFrom(c).Service.OpenReportJobEventStream(
		ctx, userID, tenantID, jobID, reportEventCursor(c),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	if stream.Reset != nil {
		// 44115 is a *refusal*, not a stream: nothing is written with text/event-stream
		// headers first, because a client that already committed to reading frames would
		// treat an empty stream as "no new events" and skip the terminal one.
		c.JSON(http.StatusConflict, response.Body{
			Code:    44115,
			Message: "report event cursor has expired, fetch the snapshot and resubscribe",
			Data: gin.H{
				"reset_required": true,
				"min_seq":        stream.Reset.MinSeq,
				"event_seq":      stream.Reset.EventSeq,
			},
			TraceID: c.GetString("request_id"),
		})
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// No buffering proxy may hold frames back: without this, a report looks
	// "stuck" for as long as the proxy's buffer is not full.
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	reportStreamExtendDeadline(c)
	c.Writer.Flush()

	heartbeat := time.NewTicker(reportStreamHeartbeat)
	defer heartbeat.Stop()
	poll := time.NewTicker(reportStreamPoll)
	defer poll.Stop()

	cursor := stream.After
	for {
		events, err := handlerFrom(c).Service.ReadReportJobEvents(ctx, tenantID, stream.JobID, cursor)
		if err != nil {
			// The headers are already out, so there is no status left to send. Closing
			// is the honest end: the client reconnects with its cursor and either gets
			// the page or fails its snapshot read, which does have a status.
			logger.Error("report event stream read failed",
				"job_id", stream.JobID, "after", cursor, "error", err)
			return
		}
		for _, event := range events {
			frame, err := reportEventFrameJSON(stream, event)
			if err != nil {
				logger.Error("report event frame encode failed", "job_id", stream.JobID, "seq", event.Seq, "error", err)
				return
			}
			// `id` and the body's `seq` must be the same number (§8.8 contract test).
			if _, err := fmt.Fprintf(c.Writer, "id: %d\nevent: report.job\ndata: %s\n\n", event.Seq, frame); err != nil {
				return
			}
			cursor = event.Seq
			if service.ReportEventTerminal(event.Type) {
				// A terminal event is the last one by definition; holding the connection
				// open would make every finished report look like it is still running.
				c.Writer.Flush()
				return
			}
		}
		if len(events) > 0 {
			c.Writer.Flush()
			reportStreamExtendDeadline(c)
		}
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.WriteString(": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
			reportStreamExtendDeadline(c)
		case <-poll.C:
		}
	}
}

// reportEventFrameJSON renders one frame body. The payload is passed through as
// written by the producing transaction: the stream is a transport, and re-marshalling
// it here would be a second definition of every event's shape.
func reportEventFrameJSON(stream *service.ReportEventStream, event model.ReportJobEvent) ([]byte, error) {
	payload := json.RawMessage("{}")
	if strings.TrimSpace(event.PayloadJSON) != "" {
		payload = json.RawMessage(event.PayloadJSON)
		if !json.Valid(payload) {
			// An unreadable payload must not poison the whole stream: the seq and type
			// are the contract, and a client can still advance its cursor.
			payload = json.RawMessage("{}")
		}
	}
	return json.Marshal(reportEventFrameBody{
		Seq:        event.Seq,
		Type:       event.Type,
		JobID:      stream.JobID,
		RevisionID: stream.RevisionID,
		At:         event.CreatedAt.UTC().Format(time.RFC3339),
		Payload:    payload,
	})
}

// reportEventFrameBody is the `data:` object of §8.8.
type reportEventFrameBody struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	JobID      string          `json:"job_id"`
	RevisionID string          `json:"revision_id"`
	At         string          `json:"at"`
	Payload    json.RawMessage `json:"payload"`
}

// reportStreamExtendDeadline pushes the connection's write deadline forward.
//
// `http.Server.WriteTimeout` is an absolute deadline from the start of the request,
// so without this a report stream is severed mid-run at whatever `write_timeout`
// says (60s by default) — heartbeats do not reset it. Extending per frame keeps a
// long stream alive while still bounding a client that stopped reading. The call is
// best-effort: `httptest.ResponseRecorder` does not support deadlines, and a
// transport that cannot be adjusted is not a reason to refuse the stream.
func reportStreamExtendDeadline(c *gin.Context) {
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(2 * reportStreamHeartbeat))
}

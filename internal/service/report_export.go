package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report delivery (doc/288 §7.7 / §12 / T11).
//
// The writer never produced "the report as one document"; that is what
// `assembleReportDocument` does. This file wraps it in the delivery chain:
// a click renders the document to Markdown or DOCX *first*, then mints the
// export snapshot/job/artifact (version allocated under the revision row lock)
// in one atomic insert. Every piece of disclosure (completeness, gaps) is read
// from the frozen payload, never from the live read API, so a file that is
// forwarded is the report the page showed (A39).
//
// One constraint shapes the code: `rgx_export_artifact` is insert-only — the
// answer-delivery immutability anchors (migrate.go:1875, migration 108) raise on
// any UPDATE. §7.7's “pending → ready backfill” describes an async worker; the
// answer path this doc points at (answer_export.go) already renders first and
// inserts the finished artifact once. The synchronous P0-A report path does the
// same: a failed render inserts a `failed` artifact (version consumed, honest
// replay), never a write-then-patch. Three more rules align with §7.7:
//
//  1. The version number is "the Nth export request": a failed render consumes
//     its version instead of recycling it (A40), and the idempotent replay of
//     the same request returns the same artifact, whatever state it is in.
//  2. The idempotency key is `report:<revision_id>:<format>:<canonical_hash>`,prefixed on purpose: `rgx_export_job.idempotency_key` is a global unique
//     index, and an unprefixed client-supplied key would collide across users.
//  3. Only `published` revisions can be exported (44106); a `superseded` revision
//     keeps its artifacts downloadable, because a delivered file must not 404
//     when a newer revision lands (doc/288 §7.10 / §12).
const reportDefaultArtifactDays = 30

// reportExportIdempotencyKey names one export request: this revision, this format,
// this frozen content, **and this renderer**.
//
// The artifact is the delivered *file*, and `rgx_export_artifact` is insert-only, so
// the key must change whenever the bytes would. Numbering was wrong (every section
// exported as `ord+1`, 2026-11-18); fixing the renderer left the broken file in place
// because the content hash was unchanged, and a re-export only replayed it. The
// renderer version is the identity of "how these bytes were produced", so it belongs
// in the key next to the content it renders. Two consequences, both intended: a
// renderer fix mints one new version for an already exported revision, and the old
// artifact stays downloadable and truthful (A41).
//
// The tag is a 4-hex digest, not the version string: the key column is 128 bytes and
// the renewal suffix `:renew10` has to fit too.
func reportExportIdempotencyKey(revisionID, format, canonicalHash string) string {
	tag := sha256Hex(reportExportRendererVersion)
	if len(tag) > 4 {
		tag = tag[:4]
	}
	return "report:" + tag + ":" + revisionID + ":" + format + ":" + canonicalHash
}

// reportArtifactRenewalLimit bounds the replay→renew loop: each turn asks the store
// for one more key, so an unbounded loop would be a query storm behind a page reload.
const reportArtifactRenewalLimit = 10

// ReportExportRequest is the POST /reports/:id/revisions/:revision/artifacts
// input. The idempotency key is server-derived from the frozen content, so the
// client never supplies it: re-exporting the same revision+format+content returns
// the same artifact (§7.7 rule 2, A30b).
type ReportExportRequest struct {
	ReportID string
	Revision int64
	Format   string `json:"format"`
	TraceID  string `json:"-"`
	// AcknowledgeAssurance is the explicit "I have seen the unresolved assurance
	// conflicts and still want to deliver this" (doc/301 §7.2). It is never implied
	// by anything else in the request: a retry of an export must not silently
	// inherit the acknowledgement, because the conflict set may have grown.
	AcknowledgeAssurance bool `json:"acknowledge_assurance"`
}

// gateReportExportOnAssurance is §7.2: 44122 (the template wants assurance and the
// run is not done) and 44121 (something the caller must acknowledge, unacknowledged).
//
// Two refusals, in this order, because they need different actions from the caller:
// 44122 says "wait", 44121 says "decide". Returning 44121 for an unfinished run
// would tell the reader to acknowledge conflicts that have not been found yet.
//
// 44121 has two reasons and they are different sentences (doc/301 D6/J6, T18):
// open conflicts (a finding to decide about) and an unusable run — `tier_0` or
// `blocked`, which is what "the semantic layer was not calibrated" looks like from
// the reader's side. Neither refuses the export in v0.7.0: uncalibrated semantics
// must not become a hard gate, but the delivery still needs an explicit, audited
// acknowledgement.
func (s *Service) gateReportExportOnAssurance(
	ctx context.Context, actorID, tenantID string, revision *model.ReportRevision,
	request ReportExportRequest,
) error {
	gate := reportAssuranceGateState(revision)
	if gate.Policy == model.ReportAssurancePolicyRequired &&
		!model.ReportAssuranceTerminal(revision.AssuranceStatus) {
		return errReportAssuranceNotReady()
	}
	if !gate.needsAck() {
		return nil
	}
	if request.AcknowledgeAssurance {
		// Recorded on the revision, and recorded *before* the render: if the render
		// fails, the acknowledgement still stands as "what the caller was shown", and
		// the next attempt is an ordinary retry rather than a second decision.
		ack := reportRevisionAckPayload(gate.Conflicts, gate.Eligibility, actorID, time.Now().UTC())
		if ack != "" {
			if err := s.Store.SetRevisionAssuranceAck(ctx, tenantID, revision.ID, ack); err != nil {
				return err
			}
			revision.AssuranceAckJSON = ack
		}
		// §7.4 `report.assurance_ack`. `acknowledged` carries the conflict count that was
		// shown to the caller, which is the number that made the decision meaningful.
		//
		// Argument order is `(actorID, tenantID, …)`: swapped, the row lands on a chain
		// keyed by a *user* id, where the tenant can never read the acknowledgement that
		// authorised its own export. The gate test caught exactly that.
		s.recordReportAudit(ctx, actorID, tenantID, "report.assurance_ack",
			revision.ReportID, map[string]any{
				"run_id": revision.AssuranceRunID, "revision": revision.Revision,
				"acknowledged": gate.Conflicts, "format": request.Format,
				"release_eligibility": gate.Eligibility, "ack_reason": gate.ackReason(),
			})
		return nil
	}
	return errReportAssuranceAckRequired(gate)
}

// reportAssuranceGate is the frozen §7.2 answer for one revision.
type reportAssuranceGate struct {
	Policy      string
	Conflicts   int
	Eligibility string
}

// ackReason names the sentence the refusal has to say, or "" when no
// acknowledgement is required.
//
// Eligibility is checked only after conflicts, because a conflict set is the
// stronger statement: it names things the reviewer can go and fix, while
// `tier_0`/`blocked` only says the run cannot be used as a release input.
// `pending` never asks for anything — a run that has not produced a verdict has
// nothing to acknowledge, and 44122 is the code that answers for it.
func (g reportAssuranceGate) ackReason() string {
	if g.Conflicts > 0 {
		return "conflicts"
	}
	switch g.Eligibility {
	case model.ReportAssuranceEligibilityTier0, model.ReportAssuranceEligibilityBlocked:
		return "release_eligibility"
	}
	return ""
}

func (g reportAssuranceGate) needsAck() bool { return g.ackReason() != "" }

// reportAssuranceGateState reads the gate inputs from the revision projection.
//
// The conflict count comes from `assurance_summary_json` rather than a live finding
// query: the gate must answer the same way every time for the same revision, and a
// live count would let a reviewer dismiss a finding after the preview and change
// what an *already started* export was checked against.
//
// `release_eligibility` rides on the same frozen summary, so "was this run usable"
// is answered from the row the run wrote, not from a second derivation that could
// drift from what the workbench showed the reviewer.
func reportAssuranceGateState(revision *model.ReportRevision) reportAssuranceGate {
	gate := reportAssuranceGate{Policy: model.ReportAssurancePolicyAdvisory}
	var summary struct {
		RequestedPolicy    string `json:"requested_policy"`
		Conflicts          int    `json:"conflicts"`
		HighFindings       int    `json:"high_findings"`
		ReleaseEligibility string `json:"release_eligibility"`
	}
	if strings.TrimSpace(revision.AssuranceSummaryJSON) != "" {
		if err := json.Unmarshal([]byte(revision.AssuranceSummaryJSON), &summary); err == nil {
			if summary.RequestedPolicy != "" {
				gate.Policy = summary.RequestedPolicy
			}
			gate.Eligibility = summary.ReleaseEligibility
			if summary.Conflicts > 0 {
				gate.Conflicts = summary.Conflicts
				return gate
			}
			gate.Conflicts = summary.HighFindings
			return gate
		}
	}
	return gate
}

// ReportExportResult is the successful export answer: the job and the artifact
// the caller can immediately download. Document is the frozen model.
type ReportExportResult struct {
	Job       *model.ExportJob
	Artifact  *model.ExportArtifact
	Document  *reportDocument
	Canonical string
	// Downloadable is the same decision the history read and the download endpoint
	// make. It rides on this response so a client that has just exported does not
	// have to re-derive "status == ready && size > 0" locally — two derivations drift
	// the day a status is added.
	Downloadable bool
}

// ReportArtifactView is one export-history row with the `downloadable` decision
// already made server-side (doc/288 §7.7: the frontend never derives it).
type ReportArtifactView struct {
	*model.ExportArtifact `json:"artifact"`
	Downloadable          bool `json:"downloadable"`
}

// validReportExportFormat closes the P0-A delivery set. DOCX and Markdown are the
// two formats §12 assigns to P0-A; HTML/JSON belong to the answer side, PDF is a
// P0-B renderer. Closing the set here keeps a random `format=pdf` from silently
// returning a "please wait" job that can never finish.
//
// The check runs on the *normalized* value (see normalizeReportExportFormat), so
// the catalogue's `md` spelling is already `markdown` by the time it arrives.
func validReportExportFormat(format string) bool {
	switch format {
	case model.ExportFormatMarkdown, model.ExportFormatDOCX, model.ExportFormatPDF,
		model.ExportFormatHTML, model.ExportFormatXLSX:
		return true
	default:
		// `json` stays out on purpose: the frozen payload is already persisted in
		// `rgx_export_snapshot.payload_json`, and a second JSON delivery would be a
		// second schema for the same bytes.
		return false
	}
}

// normalizeReportExportFormat maps the spelling a client legitimately sends onto
// the one canonical value the artifact stores.
//
// Two vocabularies existed for one closed set: the template catalogue advertises
// `output_formats: [docx, md, pdf]` (doc/288 §11.3, the panel's export buttons
// read it), while the artifact format vocabulary is `markdown`
// (`model.ExportFormatMarkdown`, mirrored in `web/src/.../report-contract.ts`).
// A client exporting exactly what the catalogue advertised was answered
// 40130 "unsupported report export format" for a supported format — found live
// on 4.151 (R10). `md` is therefore accepted as the catalogue's alias and stored
// as `markdown`, which keeps the canonical hash, the layout id
// (`<key>-v<n>-markdown`) and the renderer switch single-valued: one format, one
// identity. Aliasing happens here rather than by rewriting the seeded template
// because template payloads are versioned and already-served rows say `md`; an
// alias is backward compatible, a channel-wide payload rewrite is not.
func normalizeReportExportFormat(format string) string {
	normalized := strings.ToLower(strings.TrimSpace(format))
	if normalized == "md" {
		return model.ExportFormatMarkdown
	}
	return normalized
}

// reportPolicyInputHash freezes the access decision that let this export happen.
// The artifact is minted under `report_access_v1`; the hash stabilises what that
// decision depended on (the report's row policy and the revision's content
// template), so an audit trail can say "under this policy" without re-deriving it.
func reportPolicyInputHash(report *model.Report, revision *model.ReportRevision) string {
	return sha256Hex(strings.Join([]string{
		"report_access_v1", report.AccessPolicy, revision.TemplateKey,
		fmt.Sprintf("%d", revision.TemplateVersion), revision.OutlineConfirmation,
	}, "\n"))
}

// CreateReportExport freezes one published revision and returns a downloadable
// artifact.
//
// Ownership is checked like a read (44109 for a colleague's report) even though
// the route is `execute`: exporting is an operation on the caller's own
// deliverable (doc/288 §7.6; the same split as CancelReport).
func (s *Service) CreateReportExport(
	ctx context.Context, actorID, tenantID string, request ReportExportRequest,
) (*ReportExportResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	if request.ReportID == "" || request.Revision <= 0 {
		return nil, errReportNotFound()
	}
	request.Format = normalizeReportExportFormat(request.Format)
	if !validReportExportFormat(request.Format) {
		return nil, httperr.BadRequest(40130, "unsupported report export format")
	}
	report, err := s.Store.GetReport(ctx, tenantID, request.ReportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, request.ReportID, request.Revision)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(request.Revision)
	}
	if revision.Status != model.ReportRevisionPublished {
		return nil, errReportExportRequiresPublished()
	}

	// The assurance gate runs *before* the document is assembled: rendering a
	// document we are about to refuse wastes the work, and the refusal must not
	// depend on whether the renderer happened to succeed (doc/301 §7.2).
	if err := s.gateReportExportOnAssurance(ctx, actorID, tenantID, revision, request); err != nil {
		return nil, err
	}

	document, payload, err := s.assembleReportDocument(ctx, tenantID, report, revision)
	if err != nil {
		return nil, err
	}
	canonicalHash := sha256Hex(string(payload))
	idempotencyKey := reportExportIdempotencyKey(revision.ID, request.Format, canonicalHash)
	if len(idempotencyKey) > 128 {
		return nil, httperr.BadRequest(40130, "report export idempotency key is too long")
	}

	// A repeated export of the same frozen content is the same request, not a new
	// version (§7.7 / A30b). What "the same content" does *not* cover is the
	// renderer: see reportExportIdempotencyKey. The replay returns whatever state the earlier
	// artifact is in — pending, ready or failed — because the artifact's own
	// status is the honest progress signal.
	//
	// One exception, and it is A47: an artifact that expired (or was soft-deleted)
	// is a delivered file whose clock ran out, not a request we can replay. Handing
	// back its id would render a download button that 404s, so the same frozen
	// content is re-rendered under a renewal key and takes the next version. The old
	// row keeps its `expired` clock untouched — re-export never revives a version
	// (§7.7 three-way rule).
	baseKey := idempotencyKey
	for renewal := 0; ; renewal++ {
		existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			break
		}
		result, err := s.readBackArtifactResult(ctx, existing, document, canonicalHash)
		if err != nil {
			return nil, err
		}
		if !reportArtifactNeedsRenewal(result.Artifact, time.Now().UTC()) {
			return result, nil
		}
		if renewal >= reportArtifactRenewalLimit {
			// Ten consecutive clock expiries of the same frozen content (~10 artifact
			// TTLs). Answering with the exhausted row is honest: it is not
			// downloadable, and the caller learns that instead of getting a fresh
			// version every time someone reloads the page.
			return result, nil
		}
		idempotencyKey = fmt.Sprintf("%s:renew%d", baseKey, renewal+1)
		if len(idempotencyKey) > 128 {
			return nil, httperr.BadRequest(40130, "report export idempotency key is too long")
		}
	}

	now := time.Now().UTC()
	template := &model.ExportTemplateVersion{
		ID:                 id.New(),
		TenantID:           tenantID,
		TemplateID:         reportExportLayoutID(revision.TemplateKey, request.Format),
		Version:            reportExportTemplateVersion,
		Format:             request.Format,
		RendererVersion:    reportExportRendererVersion,
		SchemaVersionsJSON: `["` + reportDocumentSchema + `"]`,
		TemplateJSON:       `{"title":"RAGFlow-X Report","integrity_banner":true}`,
	}
	if err := s.Store.UpsertDefaultExportTemplate(ctx, template); err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("report-%s-r%d.%s", report.ID, revision.Revision, exportExtension(request.Format))
	dir := filepath.Join(s.DataDir, "exports", tenantID)
	path := filepath.Join(dir, id.New()+filepath.Ext(filename))
	// The report lives in a tenant, not a project (doc/288 §7: report objects have
	// no project binding), so both the snapshot and the job carry an empty
	// ProjectID — the answer path's project scope has no analog here.
	policyInputHash := reportPolicyInputHash(report, revision)

	// Render first: `rgx_export_artifact` is insert-only (the answer-delivery
	// immutability anchors at migrate.go:1875 / migration 108 raise on any UPDATE),
	// so the artifact's status/sha256/byte_size must be final when the row is
	// written. §7.7's “pending → ready backfill” describes an async worker; the
	// synchronous P0-A path inserts the finished artifact once, exactly like
	// answer_export.go does for answers. A failed render still consumes a version
	// (A40): it inserts a `failed` artifact instead of recycling the number.
	content, renderErr := renderReportExport(document, request.Format, now)
	byteSize, fileSHA := int64(0), ""
	artifactStatus := model.ExportArtifactReady
	jobStatus, errorCode := model.ExportJobSucceeded, ""
	if renderErr == nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			renderErr, errorCode = err, "export_persist_failed"
		} else if err := os.WriteFile(path, content, 0o600); err != nil {
			renderErr, errorCode = err, "export_persist_failed"
		} else {
			byteSize, fileSHA = int64(len(content)), sha256Hex(string(content))
		}
	}
	if renderErr != nil {
		if errorCode == "" {
			errorCode = "export_render_failed"
		}
		artifactStatus, jobStatus = model.ExportArtifactFailed, model.ExportJobFailed
		logger.Warn("report export failed", "export_job_key", idempotencyKey,
			"report_revision_id", revision.ID, "format", request.Format,
			"error_code", errorCode, "error", renderErr)
	}

	snapshot := &model.ExportSnapshot{
		ID: id.New(), TenantID: tenantID,
		SubjectType:       model.ExportSubjectReport,
		ReportRevisionID:  revision.ID,
		AnswerSnapshotIDs: "[]",
		TemplateID:        template.TemplateID, TemplateVersion: template.Version,
		PolicyVersion: reportExportPolicyVersion, PolicyEvaluatedAt: now,
		PolicyInputHash: policyInputHash, CanonicalHash: canonicalHash,
		PayloadJSON: string(payload), CreatedAt: now,
	}
	job := &model.ExportJob{
		ID: id.New(), TenantID: tenantID,
		ExportSnapshotID: snapshot.ID,
		SubjectType:      model.ExportSubjectReport,
		ReportRevisionID: revision.ID,
		AnswerSnapshotID: "",
		RequestedBy:      actorID,
		Format:           request.Format,
		Status:           jobStatus,
		ErrorCode:        errorCode,
		CompletedAt:      &now,
		TemplateVersion:  template.Version,
		PolicyVersion:    reportExportPolicyVersion, PolicyEvaluatedAt: now,
		PolicyInputHash: policyInputHash,
		TraceID:         request.TraceID,
		IdempotencyKey:  idempotencyKey,
		CreatedAt:       now,
	}
	artifact := &model.ExportArtifact{
		ID: id.New(), TenantID: tenantID,
		ExportJobID:      job.ID,
		SubjectType:      model.ExportSubjectReport,
		ReportRevisionID: revision.ID,
		ArtifactStatus:   artifactStatus,
		Format:           request.Format,
		RendererVersion:  reportExportRendererVersion,
		FileRef:          path,
		Filename:         filename,
		MimeType:         exportMime(request.Format),
		ByteSize:         byteSize,
		SHA256:           fileSHA,
		CreatedAt:        now,
		ExpiresAt:        reportExpiry(now, s.ReportRetentionFor(ctx, tenantID).ReportArtifactDays, reportDefaultArtifactDays),
		CreatedBy:        actorID,
	}
	stored, err := s.Store.CreateReportExport(ctx, repository.ReportExportRows{
		TenantID: tenantID, RevisionID: revision.ID,
		Snapshot: snapshot, Job: job, Artifact: artifact,
	})
	if err != nil {
		if errors.Is(err, repository.ErrReportRevisionNotPublished) {
			// The revision was superseded between the service's read and the write
			// (or the repository's lock re-read refused it): same answer as below.
			return nil, errReportExportRequiresPublished()
		}
		if repository.IsReportExportConflict(err) {
			// The global idempotency index (or the version index) won the race with an
			// identical request: the earlier writer's rows are the answer, not a second
			// version. This slice's rendered file is a duplicate nobody can reach, so it
			// is dropped here rather than left in the tenant's export directory (A30b).
			if existing, e2 := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey); e2 == nil && existing != nil {
				_ = os.Remove(path)
				return s.readBackArtifactResult(ctx, existing, document, canonicalHash)
			}
			return nil, errReportArtifactVersionConflict(
				"concurrent export claimed the same artifact version")
		}
		if renderErr == nil {
			// The row transaction failed after the file landed: drop the orphan file
			// so the sandbox does not accumulate deliveries nobody can reach.
			_ = os.Remove(path)
		}
		return nil, err
	}
	// `report.artifact.exported` names the file that was produced, so the audit
	// chain answers "which bytes did this export request deliver" without a join
	// (doc/288 §11.3 / A8). A failed render is a delivery too (A40): it consumed
	// the version, so it is recorded with Result=FAILURE and the error code rather
	// than being left out of the chain.
	result := "SUCCESS"
	if artifactStatus != model.ExportArtifactReady {
		result = "FAILURE"
	}
	s.recordReportAuditResult(ctx, actorID, tenantID, "report.artifact.exported", report.ID, result,
		map[string]interface{}{
			"artifact_id":      stored.ID,
			"artifact_version": stored.ArtifactVersion,
			"artifact_status":  stored.ArtifactStatus,
			"format":           stored.Format,
			"revision_id":      revision.ID,
			"revision":         revision.Revision,
			"sha256":           stored.SHA256,
			"byte_size":        stored.ByteSize,
			"error_code":       errorCode,
		})
	return &ReportExportResult{
		Job: job, Artifact: stored, Document: document, Canonical: canonicalHash,
		Downloadable: reportArtifactDownloadable(stored, time.Now().UTC()),
	}, nil
}

// readBackArtifactResult rebuilds the answer of an already-exported request. The
// artifact is loaded from the job because the caller's copy may predate the
// delivery; a missing artifact row is the only case where the replay answers with
// a job and no artifact. Whatever state the earlier attempt ended in (ready,
// failed) is reported as-is — replay never silently succeeds (§7.7 / A30b).
func (s *Service) readBackArtifactResult(ctx context.Context, job *model.ExportJob, document *reportDocument, canonical string) (*ReportExportResult, error) {
	if job.SubjectType != model.ExportSubjectReport {
		return nil, errReportNotFound()
	}
	artifact, err := s.Store.GetExportArtifactByJob(ctx, job.TenantID, job.ID)
	if err != nil {
		return nil, err
	}
	return &ReportExportResult{
		Job: job, Artifact: artifact, Document: document, Canonical: canonical,
		// Replay answers about the delivery as it stands now, not as it stood when it
		// was minted: a clock that ran out must not be replayed as a live file.
		Downloadable: reportArtifactDownloadable(artifact, time.Now().UTC()),
	}, nil
}

// ListReportArtifacts returns the export history of one revision and derives each
// row's `downloadable` flag. It is part of the revision detail read, so the UI
// never computes availability itself (doc/288 §7.7; the same discipline as
// `requires_reparse`).
func (s *Service) ListReportArtifacts(ctx context.Context, tenantID, revisionID string) ([]ReportArtifactView, error) {
	rows, err := s.Store.ListReportArtifacts(ctx, tenantID, revisionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	views := make([]ReportArtifactView, 0, len(rows))
	for index := range rows {
		views = append(views, ReportArtifactView{
			ExportArtifact: &rows[index],
			Downloadable:   reportArtifactDownloadable(&rows[index], now),
		})
	}
	return views, nil
}

// reportArtifactNeedsRenewal answers the narrower question the export path asks:
// this artifact exists for exactly this content, but it is no longer a file anyone
// can open, so the request must mint a new version instead of replaying a dead one.
//
// `failed` is deliberately outside the set: a failed render is the honest result of
// that request (A40), and replaying it tells the caller the render failed. Only the
// clock (`ready` + expired), an explicit `expired` row, and a soft delete qualify.
func reportArtifactNeedsRenewal(artifact *model.ExportArtifact, now time.Time) bool {
	if artifact == nil {
		return false
	}
	if artifact.ArtifactStatus == model.ExportArtifactExpired || artifact.DeletedAt != nil {
		return true
	}
	return artifact.ArtifactStatus == model.ExportArtifactReady && !now.Before(artifact.ExpiresAt)
}

// reportArtifactDownloadable is the one place "can this file be downloaded" is
// decided: its state is `ready`, its clock has not run out, and it has not been
// soft-deleted (doc/288 §7.7 three-way rule / A47). Callers on the read side and
// the download side both go through it, so the page and the endpoint cannot
// disagree.
func reportArtifactDownloadable(artifact *model.ExportArtifact, now time.Time) bool {
	if artifact == nil {
		return false
	}
	return artifact.ArtifactStatus == model.ExportArtifactReady &&
		now.Before(artifact.ExpiresAt) &&
		artifact.DeletedAt == nil
}

// OpenReportArtifact opens a report artifact for download.
//
// The three gates are deliberately separate answers: a report that cannot be seen
// is 44109 (no existence oracle, §7.6), a pending artifact is 409/44104 (it is
// yours, it is just not done), and an unavailable artifact is 44109 (there used
// to be a file here, there is not anymore, so nothing extra is disclosed).
//
// The artifact is addressed through report id + revision so a stale URL fails on
// the revision, not on a guessable artifact id.
func (s *Service) OpenReportArtifact(ctx context.Context, actorID, tenantID, reportID string, revisionNo int64, artifactID string) (*model.ExportArtifact, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	artifact, err := s.Store.ReportExportArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, errReportNotFound()
	}
	job, err := s.Store.GetExportJob(ctx, tenantID, artifact.ExportJobID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.SubjectType != model.ExportSubjectReport || job.ReportRevisionID != revision.ID {
		return nil, errReportNotFound()
	}
	now := time.Now().UTC()
	if !reportArtifactDownloadable(artifact, now) {
		if artifact.ArtifactStatus == model.ExportArtifactPending {
			return nil, errReportArtifactUnavailable("artifact is still being generated")
		}
		return nil, errReportNotFound()
	}
	cleanPath := filepath.Clean(artifact.FileRef)
	exportsRoot := filepath.Join(s.DataDir, "exports", tenantID)
	if !strings.HasPrefix(cleanPath, exportsRoot+string(os.PathSeparator)) {
		// The artifact row is trusted to come from this service, but the file
		// system is the final authority on what path is inside the sandbox.
		return nil, errReportNotFound()
	}
	if _, err := os.Stat(cleanPath); err != nil {
		return nil, errReportNotFound()
	}
	// Reuse the answer download trail: reports write an empty answer id and name
	// the frozen revision directly, so the audit row is traceable without a join
	// (doc/288 §12 chain table). One audit row per download, event semantics (A8).
	if err := s.Store.CreateExportDownloadAudit(ctx, &model.ExportDownloadAudit{
		TenantID:         tenantID,
		ProjectID:        job.ProjectID,
		ExportArtifactID: artifact.ID,
		ExportJobID:      job.ID,
		AnswerSnapshotID: "",
		ReportRevisionID: revision.ID,
		DownloadedBy:     actorID,
		DecisionHash:     job.PolicyInputHash,
	}); err != nil {
		return nil, err
	}
	// The download trail above is the event ledger the reconciler counts; the
	// `report.artifact.downloaded` audit row is what a tenant admin reading the
	// audit chain sees next to every other report action (doc/288 §11.3). The two
	// answer different questions ("how often was this file taken" vs "what did
	// this actor do"), so both are written and neither is derived from the other.
	s.recordReportAudit(ctx, actorID, tenantID, "report.artifact.downloaded", reportID,
		map[string]interface{}{
			"artifact_id":      artifact.ID,
			"artifact_version": artifact.ArtifactVersion,
			"format":           artifact.Format,
			"revision_id":      revision.ID,
			"revision":         revision.Revision,
			"sha256":           artifact.SHA256,
			"export_job_id":    job.ID,
		})
	return artifact, nil
}

// recordReportAudit appends one report audit row with its detail payload.
//
// A failing audit write must not fail the business action it describes: the
// report tables are the source of truth for what happened, and the audit chain
// is a secondary ledger (doc/288 §11.3 lists both `report.artifact.downloaded`
// and the `rgx_export_download_audit` trail for exactly this reason). It is
// logged loudly instead of swallowed, so a broken audit chain is visible.
func (s *Service) recordReportAudit(
	ctx context.Context, actorID, tenantID, action, reportID string, detail map[string]interface{},
) {
	s.recordReportAuditResult(ctx, actorID, tenantID, action, reportID, "SUCCESS", detail)
}

// recordReportAuditResult is `recordReportAudit` for an action that can end in a
// delivered failure (a failed render still consumes an artifact version, A40).
func (s *Service) recordReportAuditResult(
	ctx context.Context, actorID, tenantID, action, reportID, result string,
	detail map[string]interface{},
) {
	payload, err := json.Marshal(detail)
	if err != nil {
		logger.Warn("report audit detail is not encodable", "action", action, "error", err)
		payload = []byte(`{}`)
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: action,
		Resource: "report", ResourceID: reportID, Result: result,
		DetailJSON: string(payload),
	}); err != nil {
		logger.Warn("report audit append failed", "action", action,
			"report_id", reportID, "error", err)
	}
}

// renderReportExport dispatches the frozen document to a format renderer. The
// P0-A pair is Markdown (the source of truth for the structured §12 compare) and
// the self-built DOCX (V10 spike). DOCX is the operator deliverable; Markdown
// exists so the structured compare and the E2E can read the same frozen content.
func renderReportExport(document *reportDocument, format string, now time.Time) ([]byte, error) {
	switch format {
	case model.ExportFormatMarkdown:
		return []byte(renderReportMarkdown(document, now)), nil
	case model.ExportFormatDOCX:
		return renderReportDocx(document, now)
	case model.ExportFormatPDF:
		return renderReportPdf(document, now)
	case model.ExportFormatHTML:
		return []byte(renderReportHTML(document, now)), nil
	case model.ExportFormatXLSX:
		return renderReportXlsx(document, now)
	default:
		return nil, httperr.BadRequest(40130, "unsupported report export format")
	}
}

// reportSectionNumber turns a stored ord into the number a reader sees.
//
// `ord` is 1-based in the model and on disk (`rgx_report_section.ord` starts at 1,
// `rgx_report_citation.ord` likewise). The renderers used to print `ord+1`, so
// every DOCX/PDF/Markdown export numbered its first section "2" while the reader
// in the product said "1." — and the evidence appendix numbered its first citation
// "[2]" while the body cited "[1]" (live, 2026-11-18). The reader was right; the
// renderers were not. Keep the rule in one place so no renderer re-derives it.
func reportSectionNumber(ord int) int {
	if ord < 1 {
		return 1
	}
	return ord
}

// renderReportMarkdown produces the report as one Markdown document. The
// disclosure banner is rendered from the frozen gaps, never from the live API, so
// a forwarded file carries the same honesty the page showed (doc/288 §12 / D14).
func renderReportMarkdown(document *reportDocument, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("# " + document.Title + "\n\n")
	builder.WriteString(fmt.Sprintf("- 报告版本：r%d（模板 %s v%d）\n",
		document.Revision, document.TemplateKey, document.TemplateVersion))
	builder.WriteString(fmt.Sprintf("- 生成时间：%s\n", now.UTC().Format("2006-01-02 15:04:05Z")))
	builder.WriteString(fmt.Sprintf("- 完整性：%s\n", reportCompletenessLabel(document.Completeness)))
	// doc/301 §5.3.1: `scan_scope` is mandatory disclosure next to the verdict, so
	// it rides in the metadata block every format prints rather than in an appendix a
	// claim-less report would not have.
	if document.ScanScope != "" {
		builder.WriteString("- 复核范围：" + document.ScanScope + "\n")
	}
	builder.WriteString("\n")

	if document.Completeness == model.ReportCompletenessPartial || len(document.Gaps) > 0 {
		builder.WriteString("> **完整性声明**：本报告为部分完成。\n>\n")
		for _, gap := range document.Gaps {
			builder.WriteString(fmt.Sprintf("> - 第 %d 节「%s」未生成：%s（影响：%s）\n",
				reportSectionNumber(gap.Ord), gap.Title, gap.Reason, gap.Impact))
			if gap.ErrorCode != "" {
				builder.WriteString(fmt.Sprintf(">   - 错误码：`%s`\n", gap.ErrorCode))
			}
		}
		builder.WriteString("\n")
	}

	// 目录 (§12 封面/目录). Markdown has no pagination, so the entries carry titles
	// only — the same rule the DOCX follows, for the same reason: a number nobody
	// can compute is a number that will be wrong.
	builder.WriteString("## 目录\n\n")
	for _, section := range document.Sections {
		builder.WriteString(fmt.Sprintf("%d. %s\n", reportSectionNumber(section.Ord), section.Title))
	}
	if len(document.EvidenceAppendix) > 0 {
		builder.WriteString("证据附录\n")
	}
	if len(document.Claims) > 0 {
		builder.WriteString("结论清单\n")
	}
	builder.WriteString("\n")

	for index, section := range document.Sections {
		if index > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("## %d. %s\n\n", reportSectionNumber(section.Ord), section.Title))
		builder.WriteString(section.ContentMD + "\n")
		if line := reportLocationHintsLine(section.LocationHints); line != "" {
			builder.WriteString("\n*" + line + "*\n")
		}
		if plan := reportResearchPlanLine(section.ResearchPlan); plan != "" {
			builder.WriteString("\n*" + plan + "*\n")
		}
		if section.EvidenceStatus != "" && section.CitationCount > 0 {
			builder.WriteString(fmt.Sprintf("\n*依据：%d 条引用，证据状态 %s。*\n",
				section.CitationCount, reportEvidenceStatusLabel(section.EvidenceStatus)))
		}
	}
	builder.WriteString(renderReportEvidenceAppendixMarkdown(document))
	builder.WriteString(renderReportClaimAppendixMarkdown(document))
	builder.WriteString(renderReportConsistencyMarkdown(document))
	builder.WriteString("\n---\n")
	builder.WriteString(fmt.Sprintf("\n_本报告由 RAGFlow-X 报告渲染器（%s，%s）生成。_\n",
		reportExportLayoutID(document.TemplateKey, model.ExportFormatMarkdown), reportExportRendererVersion))
	return builder.String()
}

// reportConsistencyLine renders one R16 issue in the words a reader needs: the
// label, the chapters that disagree, and the values each of them states.
func reportConsistencyLine(issue reportConsistencyIssue) string {
	ords := make([]string, 0, len(issue.Ords))
	for _, ord := range issue.Ords {
		ords = append(ords, fmt.Sprintf("第 %d 节", reportSectionNumber(ord)))
	}
	return fmt.Sprintf("「%s」：%s 分别为 %s",
		issue.Label, strings.Join(ords, "、"), strings.Join(issue.Values, "、"))
}

// renderReportClaimAppendixMarkdown prints R11's claim list (doc/288 §9.2).
//
// Every claim is printed, not a sample: a 5–10 section report produces tens of
// sentences, and "we showed you the sentences we happened to pick" is exactly the
// opacity the claim table exists to remove. The preamble is fixed wording, because
// what the support status does *not* certify matters more than what it does.
func renderReportClaimAppendixMarkdown(document *reportDocument) string {
	if len(document.Claims) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n## 结论清单\n\n")
	builder.WriteString(reportClaimAppendixPreamble + "\n\n")
	builder.WriteString(reportClaimSummary(document) + "\n")
	for _, group := range reportClaimGroups(document) {
		builder.WriteString(fmt.Sprintf("\n### %d. %s\n\n", reportSectionNumber(group.Ord), group.Title))
		for _, line := range group.Headings {
			builder.WriteString("- " + line + "\n")
		}
	}
	return builder.String()
}

// renderReportConsistencyMarkdown prints R16's cross-section disagreements.
func renderReportConsistencyMarkdown(document *reportDocument) string {
	if len(document.ConsistencyIssues) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n## 跨章节一致性提示\n\n")
	builder.WriteString("以下同一事实在不同章节出现不同说法，请核实后再对外使用。\n\n")
	for _, issue := range document.ConsistencyIssues {
		builder.WriteString("- " + reportConsistencyLine(issue) + "\n")
	}
	return builder.String()
}

// claimSupportedCount is the numerator of the printed rate. It is derived from the
// list rather than stored separately so the two can never disagree.
func claimSupportedCount(document *reportDocument) int {
	supported := 0
	for _, claim := range document.Claims {
		if claim.Support == model.ReportClaimSupportSupported {
			supported++
		}
	}
	return supported
}

// reportClaimLines groups the claim appendix into per-chapter lines, so the three
// renderers print the same words. Each group is a chapter title plus its claims;
// keeping this shared is what stops the DOCX and the PDF from disagreeing about
// what a 部分可核实 sentence says.
type reportClaimGroup struct {
	Ord      int
	Title    string
	Headings []string
}

// reportClaimGroups is the claim list as chapters. A claim with no title still gets
// a group: dropping it would make the appendix shorter than the report.
func reportClaimGroups(document *reportDocument) []reportClaimGroup {
	groups := make([]reportClaimGroup, 0, 6)
	for _, claim := range document.Claims {
		if len(groups) == 0 || groups[len(groups)-1].Ord != claim.SectionOrd {
			title := claim.SectionTitle
			if title == "" {
				title = fmt.Sprintf("第 %d 节", reportSectionNumber(claim.SectionOrd))
			}
			groups = append(groups, reportClaimGroup{Ord: claim.SectionOrd, Title: title})
		}
		markers := make([]string, 0, len(claim.Markers))
		for _, marker := range claim.Markers {
			markers = append(markers, fmt.Sprintf("[%d]", reportSectionNumber(marker)))
		}
		line := fmt.Sprintf("%s · %s：%s", reportClaimTypeLabel(claim.Type),
			reportClaimSupportLabel(claim.Support), strings.TrimSpace(claim.Text))
		if len(markers) > 0 {
			line += " " + strings.Join(markers, " ")
		} else if claim.Support != model.ReportClaimSupportNotRequired {
			// A factual claim with no markers is the case the appendix exists for:
			// say so rather than leaving a blank where a reference should be.
			line += " （未引用证据）"
		}
		groups[len(groups)-1].Headings = append(groups[len(groups)-1].Headings, line)
	}
	return groups
}

// reportClaimSummary is the one-line denominator statement printed above the list.
func reportClaimSummary(document *reportDocument) string {
	if document.ClaimSupportRate == nil {
		return fmt.Sprintf("本报告共 %d 句结论，其中 %d 句无需核实。",
			len(document.Claims), document.ClaimNotRequired)
	}
	return fmt.Sprintf("可核实的结论句 %d / %d（%.0f%%），另有 %d 句无需核实。",
		claimSupportedCount(document), document.ClaimScored,
		*document.ClaimSupportRate*100, document.ClaimNotRequired)
}

// renderReportEvidenceAppendixMarkdown prints the frozen appendix (doc/288 §12 /
// A13). It reads `document.EvidenceAppendix` only — never the database — because a
// delivered file must survive the snapshots it cites (A43).
func renderReportEvidenceAppendixMarkdown(document *reportDocument) string {
	if len(document.EvidenceAppendix) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n## 证据附录\n\n")
	if document.EvidenceVerifiable {
		builder.WriteString("以下为生成时冻结的证据副本，可与原始文档逐条核对。\n\n")
	} else {
		builder.WriteString("以下为生成时冻结的证据副本；原始快照已过期，当前无法复核。\n\n")
	}
	currentSection := -1
	for _, entry := range document.EvidenceAppendix {
		if entry.SectionOrd != currentSection {
			currentSection = entry.SectionOrd
			title := entry.SectionTitle
			if title == "" {
				title = fmt.Sprintf("第 %d 节", reportSectionNumber(entry.SectionOrd))
			}
			builder.WriteString(fmt.Sprintf("### %d. %s\n\n", reportSectionNumber(entry.SectionOrd), title))
		}
		locator := entry.DocumentName
		if locator == "" {
			locator = entry.DocumentID
		}
		if locator == "" {
			locator = entry.ChunkID
		}
		builder.WriteString(fmt.Sprintf("- **[%d] %s**（%s）\n", reportSectionNumber(entry.Ord), locator, scoreLabel(entry)))
		if entry.Truncated {
			builder.WriteString("  - 快照已按策略截断，仅保留关键片段\n")
		}
		for _, line := range strings.Split(entry.Content, "\n") {
			builder.WriteString("  > " + line + "\n")
		}
	}
	return builder.String()
}

package service

import (
	"context"
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

// ReportExportRequest is the POST /reports/:id/revisions/:revision/artifacts
// input. The idempotency key is server-derived from the frozen content, so the
// client never supplies it: re-exporting the same revision+format+content returns
// the same artifact (§7.7 rule 2, A30b).
type ReportExportRequest struct {
	ReportID string
	Revision int64
	Format   string
	TraceID  string
}

// ReportExportResult is the successful export answer: the job and the artifact
// the caller can immediately download. Document is the frozen model.
type ReportExportResult struct {
	Job       *model.ExportJob
	Artifact  *model.ExportArtifact
	Document  *reportDocument
	Canonical string
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
func validReportExportFormat(format string) bool {
	return format == model.ExportFormatMarkdown || format == model.ExportFormatDOCX
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

	document, payload, err := s.assembleReportDocument(ctx, tenantID, report, revision)
	if err != nil {
		return nil, err
	}
	canonicalHash := sha256Hex(string(payload))
	idempotencyKey := "report:" + revision.ID + ":" + request.Format + ":" + canonicalHash
	if len(idempotencyKey) > 128 {
		return nil, httperr.BadRequest(40130, "report export idempotency key is too long")
	}

	// A repeated export of the same frozen content is the same request, not a new
	// version (§7.7 / A30b). The replay returns whatever state the earlier
	// artifact is in — pending, ready or failed — because the artifact's own
	// status is the honest progress signal.
	if existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		return s.readBackArtifactResult(ctx, existing, document, canonicalHash)
	}

	now := time.Now().UTC()
	template := &model.ExportTemplateVersion{
		ID:                 id.New(),
		TenantID:           tenantID,
		TemplateID:         reportExportTemplateID + "-" + request.Format,
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
		ExpiresAt:        reportExpiry(now, s.currentRetentionPolicy().ReportArtifactDays, reportDefaultArtifactDays),
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
			// The global idempotency index won the race with an identical request:
			// the earlier writer's rows are the answer, not a second version.
			if existing, e2 := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey); e2 == nil && existing != nil {
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
	return &ReportExportResult{Job: job, Artifact: stored, Document: document, Canonical: canonicalHash}, nil
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
	return &ReportExportResult{Job: job, Artifact: artifact, Document: document, Canonical: canonical}, nil
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
	return artifact, nil
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
	default:
		return nil, httperr.BadRequest(40130, "unsupported report export format")
	}
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
	builder.WriteString(fmt.Sprintf("- 完整性：%s\n\n", document.Completeness))

	if document.Completeness == model.ReportCompletenessPartial || len(document.Gaps) > 0 {
		builder.WriteString("> **完整性声明**：本报告为部分完成。\n>\n")
		for _, gap := range document.Gaps {
			builder.WriteString(fmt.Sprintf("> - 第 %d 节「%s」未生成：%s（影响：%s）\n",
				gap.Ord+1, gap.Title, gap.Reason, gap.Impact))
			if gap.ErrorCode != "" {
				builder.WriteString(fmt.Sprintf(">   - 错误码：`%s`\n", gap.ErrorCode))
			}
		}
		builder.WriteString("\n")
	}

	for index, section := range document.Sections {
		if index > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("## %d. %s\n\n", section.Ord+1, section.Title))
		builder.WriteString(section.ContentMD + "\n")
		if section.EvidenceStatus != "" && section.CitationCount > 0 {
			builder.WriteString(fmt.Sprintf("\n*依据：%d 条引用，证据状态 %s。*\n",
				section.CitationCount, section.EvidenceStatus))
		}
	}
	builder.WriteString("\n---\n")
	builder.WriteString(fmt.Sprintf("\n_本报告由 RAGFlow-X 报告渲染器（%s，%s）生成。_\n",
		reportExportTemplateID, reportExportRendererVersion))
	return builder.String()
}

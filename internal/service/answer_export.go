package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const defaultExportTemplateID = "answer-business-v1"
const defaultExportTemplateVersion int64 = 1
const defaultExportRendererVersion = "export.renderer.v1"
const asyncExportThresholdBytes = 256 * 1024
const maxExportPDFImageBytes = 8 << 20
const maxExportPDFImageDimension = 8192

type ExportResult struct {
	Job      *model.ExportJob
	Artifact *model.ExportArtifact
}

func validExportFormat(format string) bool {
	switch format {
	case model.ExportFormatMarkdown, model.ExportFormatHTML, model.ExportFormatJSON:
		return true
	case model.ExportFormatPDF:
		return true
	case model.ExportFormatDOCX:
		return true
	default:
		return false
	}
}

func validExportScope(scope string) bool {
	switch scope {
	case ExportScopeAnswer, ExportScopeConversation:
		return true
	default:
		return false
	}
}

type conversationTurn struct {
	snapshot   *model.AnswerSnapshot
	projection *model.AuthorizationProjection
}

func conversationCanonicalHash(turns []conversationTurn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		parts = append(parts, turn.snapshot.ID+":"+turn.snapshot.CanonicalHash)
	}
	return sha256Hex("conversation.v1\n" + strings.Join(parts, "\n"))
}

func buildConversationSnapshot(turns []conversationTurn) (*model.AnswerSnapshot, error) {
	if len(turns) == 0 {
		return nil, httperr.BadRequest(40130, "conversation export requires at least one answer snapshot")
	}
	var builder strings.Builder
	citations := make([]model.AnswerCitation, 0)
	answerStatus := model.AnswerStatusAnswered
	completionReason := model.CompletionReasonNormal
	var completedAt *time.Time
	for index, turn := range turns {
		snapshot := turn.snapshot
		builder.WriteString(fmt.Sprintf("## Turn %d\n\n", index+1))
		builder.WriteString(fmt.Sprintf("- Answer Snapshot: `%s`\n- Answer Status: `%s`\n- Completion Reason: `%s`\n\n",
			snapshot.ID, snapshot.AnswerStatus, snapshot.CompletionReason))
		builder.WriteString("### Question\n\n" + snapshot.UserMessage + "\n\n")
		builder.WriteString("### Answer\n\n" + snapshot.Content + "\n\n")
		if answerStatus != model.AnswerStatusAnswered || snapshot.AnswerStatus != model.AnswerStatusAnswered {
			answerStatus = model.AnswerStatusPartial
		}
		if snapshot.CompletionReason != model.CompletionReasonNormal && completionReason == model.CompletionReasonNormal {
			completionReason = snapshot.CompletionReason
		}
		if snapshot.CompletedAt != nil && (completedAt == nil || snapshot.CompletedAt.After(*completedAt)) {
			completedAt = snapshot.CompletedAt
		}
		var turnCitations []model.AnswerCitation
		if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &turnCitations); err != nil {
			return nil, err
		}
		if turn.projection.CitationVisibility != model.AuthorizationVisibilityVisible ||
			turn.projection.ArtifactVisibility != model.AuthorizationVisibilityVisible {
			turnCitations = nil
		}
		for _, citation := range turnCitations {
			citation.ID = fmt.Sprintf("turn-%d-%s", index+1, citation.ID)
			citations = append(citations, citation)
		}
	}
	first := turns[0].snapshot
	citationsJSON, err := mustJSON(citations)
	if err != nil {
		return nil, err
	}
	// The conversation pseudo-snapshot never touches the database, so its ID
	// must not collide with a real AnswerSnapshot primary key. Reusing the
	// first turn's ID made audit trails hit both the "single answer" and
	// "whole conversation" semantics (doc/118 F-05). Derive a deterministic
	// synthetic ID from the canonical hash instead: "conv-" + 27 hex chars
	// stays within the size:32 column and is recomputable from SnapshotIDs +
	// CanonicalHash.
	canonicalHash := conversationCanonicalHash(turns)
	pseudoID := "conv-" + canonicalHash[:27]
	return &model.AnswerSnapshot{
		ID:                  pseudoID,
		TenantID:            first.TenantID,
		ProjectID:           first.ProjectID,
		AnswerRunID:         first.AnswerRunID,
		AnswerSchemaVersion: model.AnswerSchemaVersion,
		AnswerStatus:        answerStatus,
		CompletionReason:    completionReason,
		Summary:             fmt.Sprintf("Conversation export (%d turns)", len(turns)),
		Content:             builder.String(),
		CitationsJSON:       citationsJSON,
		ArtifactsJSON:       "[]",
		ExecutionJSON:       "[]",
		LimitationsJSON:     "[]",
		ActionsJSON:         "[]",
		CanonicalHash:       canonicalHash,
		HashAlgorithm:       model.AnswerHashAlgorithm,
		CreatedAt:           first.CreatedAt,
		CompletedAt:         completedAt,
	}, nil
}

func (s *Service) CreateExportForAnswer(ctx context.Context, actorID, tenantID string, request ExportRequest) (*ExportResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, err
	}
	scope := request.Scope
	if scope == "" {
		scope = ExportScopeAnswer
	}
	if request.AnswerSnapshotID == "" || !validExportFormat(request.Format) || !validExportScope(scope) {
		return nil, httperr.BadRequest(40130, "answer snapshot and supported format are required")
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = id.New()
	}
	idempotencyKey := request.IdempotencyKey
	if scope == ExportScopeConversation {
		idempotencyKey = "conversation:" + request.IdempotencyKey
	}
	if len(idempotencyKey) > 128 {
		return nil, httperr.BadRequest(40130, "export idempotency key is too long")
	}
	if existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Format != request.Format || existing.AnswerSnapshotID != request.AnswerSnapshotID {
			return nil, httperr.New(409, 40930, "export idempotency key already used for a different request")
		}
		artifact, err := s.Store.GetExportArtifactByJob(ctx, tenantID, existing.ID)
		if err == nil {
			return &ExportResult{Job: existing, Artifact: artifact}, nil
		}
	}
	if idempotencyKey != request.IdempotencyKey {
		if existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, request.IdempotencyKey); err != nil {
			return nil, err
		} else if existing != nil {
			return nil, httperr.New(409, 40930, "export idempotency key already used for a different request")
		}
	} else if len("conversation:"+request.IdempotencyKey) <= 128 {
		if existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, "conversation:"+request.IdempotencyKey); err != nil {
			return nil, err
		} else if existing != nil {
			return nil, httperr.New(409, 40930, "export idempotency key already used for a different request")
		}
	}
	snapshot, projection, err := s.GetAnswerSnapshot(ctx, actorID, tenantID, request.AnswerSnapshotID, "web")
	if err != nil {
		return nil, err
	}
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, err
	}
	if snapshot.CanonicalHash == "" || projection.AnswerVisibility != model.AuthorizationVisibilityVisible {
		return nil, httperr.Forbidden("answer snapshot is not authorized for export")
	}
	answerSnapshotIDs := []string{snapshot.ID}
	exportAnswerSnapshots := []*model.AnswerSnapshot{snapshot}
	exportProjections := []*model.AuthorizationProjection{projection}
	if scope == ExportScopeConversation {
		run, err := s.Store.GetAnswerRun(ctx, tenantID, snapshot.AnswerRunID)
		if err != nil {
			return nil, err
		}
		snapshots, err := s.Store.ListAnswerSnapshots(ctx, tenantID, run.SessionID)
		if err != nil {
			return nil, err
		}
		turns := make([]conversationTurn, 0, len(snapshots))
		for _, item := range snapshots {
			itemSnapshot, itemProjection, err := s.GetAnswerSnapshot(ctx, actorID, tenantID, item.ID, "web")
			if err != nil {
				return nil, err
			}
			if itemProjection.AnswerVisibility != model.AuthorizationVisibilityVisible ||
				itemProjection.ContentVisibility != model.AuthorizationVisibilityVisible {
				continue
			}
			turns = append(turns, conversationTurn{snapshot: itemSnapshot, projection: itemProjection})
			answerSnapshotIDs = append(answerSnapshotIDs, itemSnapshot.ID)
			exportAnswerSnapshots = append(exportAnswerSnapshots, itemSnapshot)
			exportProjections = append(exportProjections, itemProjection)
		}
		if len(turns) == 0 {
			return nil, httperr.Forbidden("conversation export has no authorized answers")
		}
		snapshot, err = buildConversationSnapshot(turns)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	template := &model.ExportTemplateVersion{
		ID:       id.New(),
		TenantID: tenantID, TemplateID: defaultExportTemplateID + "-" + request.Format,
		Version: defaultExportTemplateVersion, Format: request.Format,
		RendererVersion:    defaultExportRendererVersion,
		SchemaVersionsJSON: `["` + model.AnswerSchemaVersion + `"]`,
		TemplateJSON:       `{"title":"RAGFlow-X Business Answer","show_citations":true,"show_disclaimer":true}`,
	}
	if err := s.Store.UpsertDefaultExportTemplate(ctx, template); err != nil {
		return nil, err
	}
	exportPayload := map[string]interface{}{
		"answer_snapshot":           snapshot,
		"authorization_projection":  projection,
		"export_scope":              scope,
		"answer_snapshots":          exportAnswerSnapshots,
		"authorization_projections": exportProjections,
		"template_id":               template.TemplateID,
		"template_version":          template.Version,
	}
	payloadJSON, err := json.Marshal(exportPayload)
	if err != nil {
		return nil, err
	}
	answerSnapshotIDsJSON, err := mustJSON(answerSnapshotIDs)
	if err != nil {
		return nil, err
	}
	exportSnapshot := &model.ExportSnapshot{
		TenantID: tenantID, ProjectID: snapshot.ProjectID,
		AnswerSnapshotIDs: answerSnapshotIDsJSON,
		TemplateID:        template.TemplateID, TemplateVersion: template.Version,
		PolicyVersion: projection.PolicyVersion, PolicyEvaluatedAt: projection.PolicyEvaluatedAt,
		PolicyInputHash: projection.PolicyInputHash, CanonicalHash: snapshot.CanonicalHash,
		PayloadJSON: string(payloadJSON), CreatedAt: now,
	}
	if err := s.Store.CreateExportSnapshot(ctx, exportSnapshot); err != nil {
		return nil, err
	}
	job := &model.ExportJob{
		TenantID: tenantID, ProjectID: snapshot.ProjectID,
		ExportSnapshotID: exportSnapshot.ID, AnswerSnapshotID: snapshot.ID,
		RequestedBy: actorID, Format: request.Format, Status: model.ExportJobQueued,
		TemplateVersion: template.Version, PolicyVersion: projection.PolicyVersion,
		PolicyEvaluatedAt: projection.PolicyEvaluatedAt, PolicyInputHash: projection.PolicyInputHash,
		TraceID: request.TraceID, IdempotencyKey: idempotencyKey, CreatedAt: now,
	}
	if err := s.Store.CreateExportJob(ctx, job); err != nil {
		return nil, err
	}
	if exportPayloadSize(snapshot) > asyncExportThresholdBytes {
		go s.processQueuedExport(snapshot, projection, job, exportSnapshot.ID)
		return &ExportResult{Job: job}, nil
	}
	if err := s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{"status": model.ExportJobRunning}); err != nil {
		return nil, err
	}
	job.Status = model.ExportJobRunning
	artifact, err := s.renderExportArtifact(ctx, snapshot, projection, job, exportSnapshot.ID)
	if err != nil {
		_ = s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{
			"status": model.ExportJobFailed, "error_code": "export_render_failed", "completed_at": time.Now().UTC(),
		})
		return nil, err
	}
	if err := s.Store.CreateExportArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	completedAt := time.Now().UTC()
	if err := s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{
		"status": model.ExportJobSucceeded, "completed_at": completedAt,
	}); err != nil {
		return nil, err
	}
	job.Status = model.ExportJobSucceeded
	job.CompletedAt = &completedAt
	return &ExportResult{Job: job, Artifact: artifact}, nil
}

// exportPayloadSize decides the synchronous vs queued export path. It must
// account for every rendered field, not only content/citations/execution, or a
// payload carrying large artifacts/limitations/actions slips under the 256KiB
// threshold and blocks the request (doc/118 F-12).
func exportPayloadSize(snapshot *model.AnswerSnapshot) int {
	return len(snapshot.Content) + len(snapshot.CitationsJSON) + len(snapshot.ExecutionJSON) +
		len(snapshot.ArtifactsJSON) + len(snapshot.LimitationsJSON) + len(snapshot.ActionsJSON)
}

func (s *Service) processQueuedExport(
	snapshot *model.AnswerSnapshot, projection *model.AuthorizationProjection,
	job *model.ExportJob, exportSnapshotID string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := s.Store.UpdateExportJob(ctx, job.TenantID, job.ID, map[string]interface{}{"status": model.ExportJobRunning}); err != nil {
		logger.Warn("async export state update failed", "export_job_id", job.ID, "error", err)
		s.emitExportFailure(ctx, job, "export_state_update_failed", err)
		return
	}
	artifact, err := s.renderExportArtifact(ctx, snapshot, projection, job, exportSnapshotID)
	if err != nil {
		_ = s.Store.UpdateExportJob(ctx, job.TenantID, job.ID, map[string]interface{}{
			"status": model.ExportJobFailed, "error_code": "export_render_failed", "completed_at": time.Now().UTC(),
		})
		s.emitExportFailure(ctx, job, "export_render_failed", err)
		return
	}
	if err := s.Store.CreateExportArtifact(ctx, artifact); err != nil {
		_ = s.Store.UpdateExportJob(ctx, job.TenantID, job.ID, map[string]interface{}{
			"status": model.ExportJobFailed, "error_code": "export_artifact_persist_failed", "completed_at": time.Now().UTC(),
		})
		s.emitExportFailure(ctx, job, "export_artifact_persist_failed", err)
		return
	}
	completedAt := time.Now().UTC()
	if err := s.Store.UpdateExportJob(ctx, job.TenantID, job.ID, map[string]interface{}{
		"status": model.ExportJobSucceeded, "completed_at": completedAt,
	}); err != nil {
		logger.Warn("async export completion update failed", "export_job_id", job.ID, "error", err)
		s.emitExportFailure(ctx, job, "export_completion_update_failed", err)
	}
}

func (s *Service) emitExportFailure(ctx context.Context, job *model.ExportJob, errorCode string, cause error) {
	notify.Emit(ctx, notify.Event{
		Title: "business answer export failed", Severity: "error",
		TenantID: job.TenantID, Resource: "answer-export", Type: "export_failed",
		ResourceID: job.ID, Detail: errorCode + "|" + job.ID,
		Fields: map[string]string{
			"format": job.Format, "answer_snapshot_id": job.AnswerSnapshotID,
			"requested_by": job.RequestedBy, "trace_id": job.TraceID,
		},
	})
	logger.Warn("business answer export failed", "export_job_id", job.ID, "error_code", errorCode, "error", cause)
}

func (s *Service) GetExportJob(ctx context.Context, actorID, tenantID, jobID string) (*model.ExportJob, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, err
	}
	job, err := s.Store.GetExportJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	if job.RequestedBy != actorID {
		return nil, httperr.Forbidden("export job belongs to another principal")
	}
	return job, nil
}

func (s *Service) RetryExportJob(ctx context.Context, actorID, tenantID, jobID, traceID string) (*ExportResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, err
	}
	job, err := s.Store.GetExportJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	if job.RequestedBy != actorID {
		return nil, httperr.Forbidden("export job belongs to another principal")
	}
	if job.Status != model.ExportJobFailed {
		return nil, httperr.New(409, 40933, "only failed export jobs can be retried")
	}
	snapshot, projection, err := s.GetAnswerSnapshot(ctx, actorID, tenantID, job.AnswerSnapshotID, "web")
	if err != nil {
		return nil, err
	}
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, err
	}
	if snapshot.CanonicalHash == "" || projection.AnswerVisibility != model.AuthorizationVisibilityVisible {
		return nil, httperr.Forbidden("answer snapshot is not authorized for export")
	}
	updates := map[string]interface{}{
		"status": model.ExportJobRunning, "error_code": "", "trace_id": traceID,
	}
	if err := s.Store.UpdateExportJob(ctx, tenantID, job.ID, updates); err != nil {
		return nil, err
	}
	job.Status = model.ExportJobRunning
	job.ErrorCode = ""
	job.TraceID = traceID
	artifact, err := s.renderExportArtifact(ctx, snapshot, projection, job, job.ExportSnapshotID)
	if err != nil {
		_ = s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{
			"status": model.ExportJobFailed, "error_code": "export_render_failed",
			"completed_at": time.Now().UTC(),
		})
		s.emitExportFailure(ctx, job, "export_render_failed", err)
		return nil, err
	}
	if err := s.Store.CreateExportArtifact(ctx, artifact); err != nil {
		_ = s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{
			"status": model.ExportJobFailed, "error_code": "export_artifact_persist_failed",
			"completed_at": time.Now().UTC(),
		})
		s.emitExportFailure(ctx, job, "export_artifact_persist_failed", err)
		return nil, err
	}
	completedAt := time.Now().UTC()
	if err := s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{
		"status": model.ExportJobSucceeded, "completed_at": completedAt,
	}); err != nil {
		return nil, err
	}
	job.Status = model.ExportJobSucceeded
	job.CompletedAt = &completedAt
	return &ExportResult{Job: job, Artifact: artifact}, nil
}

func (s *Service) renderExportArtifact(ctx context.Context, snapshot *model.AnswerSnapshot, projection *model.AuthorizationProjection, job *model.ExportJob, exportSnapshotID string) (*model.ExportArtifact, error) {
	filename := fmt.Sprintf("answer-%s.%s", snapshot.ID, exportExtension(job.Format))
	dir := filepath.Join(s.DataDir, "exports", job.TenantID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, job.ID+filepath.Ext(filename))
	var content []byte
	var err error
	switch job.Format {
	case model.ExportFormatDOCX:
		content, err = renderDocx(snapshot, projection, job, exportSnapshotID)
	case model.ExportFormatPDF:
		content, err = s.renderPDF(ctx, snapshot, projection, job, exportSnapshotID)
	default:
		var text string
		text, err = renderExport(snapshot, projection, job, exportSnapshotID)
		content = []byte(text)
	}
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, err
	}
	return &model.ExportArtifact{
		TenantID: job.TenantID, ProjectID: job.ProjectID, ExportJobID: job.ID,
		Format: job.Format, RendererVersion: defaultExportRendererVersion,
		FileRef: path, Filename: filename, MimeType: exportMime(job.Format),
		ByteSize: int64(len(content)), SHA256: sha256Hex(string(content)),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}, nil
}

func renderExport(snapshot *model.AnswerSnapshot, projection *model.AuthorizationProjection, job *model.ExportJob, exportSnapshotID string) (string, error) {
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return "", err
	}
	if projection.AnswerVisibility != model.AuthorizationVisibilityVisible ||
		projection.ContentVisibility != model.AuthorizationVisibilityVisible {
		return "", httperr.Forbidden("export denied by authorization projection")
	}
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &citations); err != nil {
		return "", err
	}
	switch job.Format {
	case model.ExportFormatJSON:
		payload := map[string]interface{}{
			"answer_snapshot_id": snapshot.ID, "export_snapshot_id": exportSnapshotID,
			"template_version": job.TemplateVersion, "policy_version": job.PolicyVersion,
			"policy_input_hash": job.PolicyInputHash, "canonical_hash": snapshot.CanonicalHash,
			"answer_status": snapshot.AnswerStatus, "completion_reason": snapshot.CompletionReason,
			"summary": snapshot.Summary, "content": snapshot.Content, "citations": citations,
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case model.ExportFormatMarkdown:
		var builder strings.Builder
		builder.WriteString("# RAGFlow-X Business Answer\n\n")
		builder.WriteString(fmt.Sprintf("- Answer Snapshot: `%s`\n- Export Snapshot: `%s`\n- Canonical Hash: `%s`\n", snapshot.ID, exportSnapshotID, snapshot.CanonicalHash))
		builder.WriteString(fmt.Sprintf("- Answer Status: `%s`\n- Completion Reason: `%s`\n- Generated At: `%s`\n\n", snapshot.AnswerStatus, snapshot.CompletionReason, time.Now().UTC().Format(time.RFC3339)))
		if strings.TrimSpace(snapshot.Summary) != "" {
			builder.WriteString("## Summary\n\n" + snapshot.Summary + "\n\n")
		}
		builder.WriteString("## Answer\n\n" + snapshot.Content + "\n\n")
		builder.WriteString("## Citations\n\n")
		if len(citations) == 0 {
			builder.WriteString("_No citations._\n")
		}
		for _, citation := range citations {
			builder.WriteString(fmt.Sprintf("- **%s** — %s\n", citation.ID, citation.CitationLocator))
			if strings.TrimSpace(citation.CitedContentExcerpt) != "" {
				builder.WriteString("  > " + strings.ReplaceAll(citation.CitedContentExcerpt, "\n", " ") + "\n")
			}
		}
		builder.WriteString("\n_Generated by RAGFlow-X Export Renderer v1._\n")
		return builder.String(), nil
	case model.ExportFormatHTML:
		var builder strings.Builder
		builder.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>RAGFlow-X Business Answer</title></head><body><main>`)
		builder.WriteString(`<h1>RAGFlow-X Business Answer</h1><dl>`)
		builder.WriteString(fmt.Sprintf(`<dt>Answer Snapshot</dt><dd><code>%s</code></dd>`, html.EscapeString(snapshot.ID)))
		builder.WriteString(fmt.Sprintf(`<dt>Export Snapshot</dt><dd><code>%s</code></dd>`, html.EscapeString(exportSnapshotID)))
		builder.WriteString(fmt.Sprintf(`<dt>Canonical Hash</dt><dd><code>%s</code></dd>`, html.EscapeString(snapshot.CanonicalHash)))
		builder.WriteString(fmt.Sprintf(`<dt>Answer Status</dt><dd>%s</dd>`, html.EscapeString(snapshot.AnswerStatus)))
		builder.WriteString(fmt.Sprintf(`<dt>Completion Reason</dt><dd>%s</dd>`, html.EscapeString(snapshot.CompletionReason)))
		builder.WriteString(`</dl>`)
		if strings.TrimSpace(snapshot.Summary) != "" {
			builder.WriteString(`<h2>Summary</h2><p>` + html.EscapeString(snapshot.Summary) + `</p>`)
		}
		builder.WriteString(`<h2>Answer</h2><pre>` + html.EscapeString(snapshot.Content) + `</pre><h2>Citations</h2><ol>`)
		for _, citation := range citations {
			builder.WriteString(fmt.Sprintf(`<li><strong>%s</strong> — %s<blockquote>%s</blockquote></li>`,
				html.EscapeString(citation.ID), html.EscapeString(citation.CitationLocator), html.EscapeString(citation.CitedContentExcerpt)))
		}
		builder.WriteString(`</ol><p>Generated by RAGFlow-X Export Renderer v1.</p></main></body></html>`)
		return builder.String(), nil
	case model.ExportFormatDOCX:
		return "", httperr.BadRequest(40131, "docx uses the structured renderer")
	default:
		return "", httperr.BadRequest(40131, "unsupported export format")
	}
}

func exportExtension(format string) string {
	switch format {
	case model.ExportFormatMarkdown:
		return "md"
	case model.ExportFormatHTML:
		return "html"
	case model.ExportFormatJSON:
		return "json"
	case model.ExportFormatPDF:
		return "pdf"
	default:
		return "docx"
	}
}

func exportMime(format string) string {
	switch format {
	case model.ExportFormatMarkdown:
		return "text/markdown; charset=utf-8"
	case model.ExportFormatHTML:
		return "text/html; charset=utf-8"
	case model.ExportFormatJSON:
		return "application/json; charset=utf-8"
	case model.ExportFormatPDF:
		return "application/pdf"
	default:
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func docxParagraph(text string, style string) string {
	paragraphProperties := ""
	if style != "" {
		paragraphProperties = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + paragraphProperties + `<w:r><w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r></w:p>`
}

func renderDocx(snapshot *model.AnswerSnapshot, projection *model.AuthorizationProjection, job *model.ExportJob, exportSnapshotID string) ([]byte, error) {
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, err
	}
	if projection.AnswerVisibility != model.AuthorizationVisibilityVisible ||
		projection.ContentVisibility != model.AuthorizationVisibilityVisible {
		return nil, httperr.Forbidden("export denied by authorization projection")
	}
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &citations); err != nil {
		return nil, err
	}
	executionSteps := parseExecutionSteps(snapshot.ExecutionJSON)
	var document strings.Builder
	document.WriteString(docxParagraph("RAGFlow-X Business Answer", "Title"))
	document.WriteString(docxParagraph("Answer Snapshot: "+snapshot.ID, ""))
	document.WriteString(docxParagraph("Export Snapshot: "+exportSnapshotID, ""))
	document.WriteString(docxParagraph("Canonical Hash: "+snapshot.CanonicalHash, ""))
	document.WriteString(docxParagraph("Answer Status: "+snapshot.AnswerStatus+" / Completion: "+snapshot.CompletionReason, ""))
	document.WriteString(docxParagraph("Generated At: "+time.Now().UTC().Format(time.RFC3339), ""))
	if strings.TrimSpace(snapshot.Summary) != "" {
		document.WriteString(docxParagraph("Summary", "Heading1"))
		document.WriteString(docxParagraph(snapshot.Summary, ""))
	}
	document.WriteString(docxParagraph("Answer", "Heading1"))
	for _, block := range parseAnswerContent(snapshot.Content) {
		document.WriteString(docxBlock(block))
	}
	if len(executionSteps) > 0 {
		document.WriteString(docxParagraph("Agent Execution Timeline", "Heading1"))
		for index, step := range executionSteps {
			document.WriteString(docxParagraph(fmt.Sprintf("%s [%d] %s (%d ms)", executionStepSymbol(step.status), index+1, step.label, step.durationMs), ""))
			if strings.TrimSpace(step.summary) != "" {
				document.WriteString(docxParagraph("    "+step.summary, ""))
			}
		}
	}
	document.WriteString(docxParagraph("Citations", "Heading1"))
	if len(citations) == 0 {
		document.WriteString(docxParagraph("No citations.", ""))
	}
	for _, citation := range citations {
		document.WriteString(docxParagraph(citation.ID+" - "+citation.CitationLocator, ""))
		if strings.TrimSpace(citation.CitedContentExcerpt) != "" {
			document.WriteString(docxParagraph(citation.CitedContentExcerpt, ""))
		}
	}
	document.WriteString(docxParagraph("DOCX renderer v1 renders structured blocks for tables, code, formulas, image metadata and the agent execution timeline (safe summary fields only); formulas are rendered as plain text.", ""))
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	files := []struct{ name, content string }{
		{name: "[Content_Types].xml", content: `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{name: "_rels/.rels", content: `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
	}
	for _, file := range files {
		entry, err := writer.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(file.content)); err != nil {
			return nil, err
		}
	}
	entry, err := writer.Create("word/document.xml")
	if err != nil {
		return nil, err
	}
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + document.String() + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134"/></w:sectPr></w:body></w:document>`
	if _, err := entry.Write([]byte(documentXML)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func pdfFontPath() string {
	if path := strings.TrimSpace(os.Getenv("RGX_EXPORT_PDF_FONT")); path != "" {
		return path
	}
	candidates := []string{
		`C:\Windows\Fonts\Deng.ttf`,
		`C:\Windows\Fonts\msyh.ttc`,
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (s *Service) renderPDF(ctx context.Context, snapshot *model.AnswerSnapshot, projection *model.AuthorizationProjection, job *model.ExportJob, exportSnapshotID string) ([]byte, error) {
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, err
	}
	if projection.AnswerVisibility != model.AuthorizationVisibilityVisible ||
		projection.ContentVisibility != model.AuthorizationVisibilityVisible {
		return nil, httperr.Forbidden("export denied by authorization projection")
	}
	fontPath := pdfFontPath()
	if fontPath == "" {
		return nil, httperr.New(400, 40132, "pdf renderer requires RGX_EXPORT_PDF_FONT")
	}
	executionSteps := parseExecutionSteps(snapshot.ExecutionJSON)
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddUTF8Font("cjk", "", fontPath)
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("load pdf font: %w", err)
	}
	pdf.SetHeaderFunc(func() {
		pdf.SetFont("cjk", "", 8)
		pdf.SetY(8)
		pdf.CellFormat(0, 5, "RAGFlow-X Business Answer | "+snapshot.ID, "0", 1, "R", false, 0, "")
		pdf.Ln(3)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("cjk", "", 8)
		pdf.CellFormat(0, 8, fmt.Sprintf("Page %d", pdf.PageNo()), "0", 0, "C", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont("cjk", "", 18)
	pdf.MultiCell(0, 9, "RAGFlow-X Business Answer", "", "L", false)
	pdf.SetFont("cjk", "", 9)
	pdf.MultiCell(0, 5, fmt.Sprintf("Answer Snapshot: %s\nExport Snapshot: %s\nCanonical Hash: %s\nAnswer Status: %s\nCompletion Reason: %s\nGenerated At: %s",
		snapshot.ID, exportSnapshotID, snapshot.CanonicalHash, snapshot.AnswerStatus, snapshot.CompletionReason,
		time.Now().UTC().Format(time.RFC3339)), "", "L", false)
	pdf.Ln(5)
	if strings.TrimSpace(snapshot.Summary) != "" {
		pdf.SetFont("cjk", "", 13)
		pdf.MultiCell(0, 7, "Summary", "", "L", false)
		pdf.SetFont("cjk", "", 10)
		pdf.MultiCell(0, 6, snapshot.Summary, "", "L", false)
	}
	pdf.SetFont("cjk", "", 13)
	pdf.MultiCell(0, 7, "Answer", "", "L", false)
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &citations); err != nil {
		return nil, err
	}
	for _, block := range parseAnswerContent(snapshot.Content) {
		pdfBlockWithImages(ctx, s, pdf, block, citations, projection)
	}
	pdf.Ln(3)
	if len(executionSteps) > 0 {
		pdf.SetFont("cjk", "", 13)
		pdf.MultiCell(0, 7, "Agent Execution Timeline", "", "L", false)
		for index, step := range executionSteps {
			pdf.SetFont("cjk", "", 10)
			pdf.CellFormat(0, 6, fmt.Sprintf("%s [%d] %s", executionStepSymbol(step.status), index+1, step.label), "", 0, "L", false, 0, "")
			pdf.CellFormat(0, 6, fmt.Sprintf("%d ms", step.durationMs), "", 0, "R", false, 0, "")
			pdf.Ln(-1)
			if strings.TrimSpace(step.summary) != "" {
				pdf.SetFont("cjk", "", 9)
				pdf.SetTextColor(110, 110, 110)
				pdf.MultiCell(0, 5, "    "+step.summary, "", "L", false)
				pdf.SetTextColor(0, 0, 0)
			}
		}
		pdf.Ln(3)
	}
	pdf.SetFont("cjk", "", 13)
	pdf.MultiCell(0, 7, "Citations", "", "L", false)
	pdf.SetFont("cjk", "", 9)
	if len(citations) == 0 {
		pdf.MultiCell(0, 5, "No citations.", "", "L", false)
	}
	for _, citation := range citations {
		pdf.MultiCell(0, 5, citation.ID+" - "+citation.CitationLocator, "", "L", false)
		if strings.TrimSpace(citation.CitedContentExcerpt) != "" {
			pdf.MultiCell(0, 5, citation.CitedContentExcerpt, "", "L", false)
		}
	}
	pdf.Ln(4)
	pdf.MultiCell(0, 5, "PDF renderer v1 renders structured blocks for tables, code, formulas, image metadata and the agent execution timeline (safe summary fields only); formulas are rendered as plain text.", "", "L", false)
	pdf.MultiCell(0, 5, "Template version "+fmt.Sprint(job.TemplateVersion)+"; policy "+job.PolicyVersion+".", "", "L", false)
	buf := &bytes.Buffer{}
	if err := pdf.Output(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type exportImageSource struct {
	datasetID  string
	documentID string
	chunkID    string
	imageID    string
}

func authorizedExportImageSource(source string, citations []model.AnswerCitation, artifactVisible bool) *exportImageSource {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" ||
		parsed.Path != "/api/v1/chat/image" || parsed.Fragment != "" ||
		parsed.RawFragment != "" || !artifactVisible {
		return nil
	}
	query := parsed.Query()
	if len(query) != 4 {
		return nil
	}
	for _, name := range []string{"dataset", "doc", "chunk", "image"} {
		if len(query[name]) != 1 {
			return nil
		}
	}
	result := &exportImageSource{
		datasetID: query.Get("dataset"), documentID: query.Get("doc"),
		chunkID: query.Get("chunk"), imageID: query.Get("image"),
	}
	if result.datasetID == "" || result.documentID == "" || result.chunkID == "" || result.imageID == "" {
		return nil
	}
	for _, citation := range citations {
		if citation.DatasetID == result.datasetID && citation.DocumentID == result.documentID && citation.ChunkID == result.chunkID {
			return result
		}
	}
	return nil
}

func pdfBlockWithImages(ctx context.Context, s *Service, pdf *fpdf.Fpdf, block exportBlock, citations []model.AnswerCitation, projection *model.AuthorizationProjection) {
	if block.kind != exportBlockImage {
		pdfBlock(pdf, block)
		return
	}
	parts := strings.SplitN(block.text, "|", 2)
	alt, source := parts[0], ""
	if len(parts) == 2 {
		source = parts[1]
	}
	imageSource := authorizedExportImageSource(source, citations, projection.ArtifactVisibility == model.AuthorizationVisibilityVisible)
	var matchedCitation *model.AnswerCitation
	if imageSource != nil {
		for index := range citations {
			citation := &citations[index]
			if citation.DatasetID == imageSource.datasetID && citation.DocumentID == imageSource.documentID && citation.ChunkID == imageSource.chunkID {
				matchedCitation = citation
				break
			}
		}
	}
	if imageSource == nil || matchedCitation == nil || !s.validateExportImageChunk(ctx, imageSource, matchedCitation) {
		pdf.SetFillColor(245, 245, 245)
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, "Image: "+alt+" Source: "+source+" (blocked: source is not in the snapshot citation allowlist)", "1", "L", true)
		pdf.Ln(2)
		return
	}
	data, _, err := s.RAGFlow.GetChunkImage(ctx, imageSource.imageID)
	if err != nil {
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("Image %s unavailable: %v", alt, err), "1", "L", false)
		return
	}
	if len(data) > maxExportPDFImageBytes {
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("Image %s exceeds %d bytes and was not embedded", alt, maxExportPDFImageBytes), "1", "L", false)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width <= 0 || config.Height <= 0 || config.Width > maxExportPDFImageDimension || config.Height > maxExportPDFImageDimension {
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("Image %s has unsupported dimensions and was not embedded", alt), "1", "L", false)
		return
	}
	if format == "jpeg" {
		format = "jpg"
	}
	imageName := "answer-image-" + imageSource.imageID
	info := pdf.RegisterImageOptionsReader(imageName, fpdf.ImageOptions{ImageType: format, ReadDpi: true}, bytes.NewReader(data))
	if pdf.Error() != nil || info == nil {
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("Image %s could not be decoded", alt), "1", "L", false)
		return
	}
	width, height := info.Extent()
	const maxWidth = 160.0
	if width > maxWidth {
		height = height * maxWidth / width
		width = maxWidth
	}
	pdf.Image(imageName, -1, 0, width, height, true, format, 0, "")
	if pdf.Error() != nil {
		pdf.SetFont("cjk", "", 9)
		pdf.MultiCell(0, 5, fmt.Sprintf("Image %s could not be placed", alt), "1", "L", false)
		return
	}
	pdf.SetFont("cjk", "", 8)
	pdf.MultiCell(0, 5, "Image: "+alt+" Source: "+source, "", "L", false)
}

func (s *Service) validateExportImageChunk(ctx context.Context, source *exportImageSource, citation *model.AnswerCitation) bool {
	chunk, err := s.RAGFlow.GetChunk(ctx, source.datasetID, source.documentID, source.chunkID)
	if err != nil || chunk == nil || chunk.ID != source.chunkID ||
		chunk.DocumentID != source.documentID || chunk.ImageID != source.imageID {
		return false
	}
	return citation.CitationContentHash == "" || sha256Hex(chunk.Content) == citation.CitationContentHash
}

func (s *Service) OpenExportArtifact(ctx context.Context, actorID, tenantID, artifactID, traceID string) (*ExportDownload, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, err
	}
	artifact, err := s.Store.GetExportArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return nil, err
	}
	job, err := s.Store.GetExportJob(ctx, tenantID, artifact.ExportJobID)
	if err != nil {
		return nil, err
	}
	if job.RequestedBy != actorID {
		return nil, httperr.Forbidden("export artifact belongs to another principal")
	}
	if time.Now().UTC().After(artifact.ExpiresAt) {
		_ = s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{"status": model.ExportJobExpired})
		return nil, httperr.Forbidden("export artifact has expired")
	}
	cleanPath := filepath.Clean(artifact.FileRef)
	if !strings.HasPrefix(cleanPath, filepath.Join(s.DataDir, "exports", tenantID)+string(os.PathSeparator)) {
		return nil, httperr.Forbidden("invalid export artifact path")
	}
	if _, err := os.Stat(cleanPath); err != nil {
		return nil, httperr.NotFound("export artifact file not found")
	}
	if err := s.Store.CreateExportDownloadAudit(ctx, &model.ExportDownloadAudit{
		TenantID: tenantID, ProjectID: job.ProjectID, ExportArtifactID: artifact.ID,
		ExportJobID: job.ID, AnswerSnapshotID: job.AnswerSnapshotID, DownloadedBy: actorID,
		DecisionHash: job.PolicyInputHash, TraceID: traceID,
	}); err != nil {
		return nil, err
	}
	return &ExportDownload{Artifact: artifact, Path: cleanPath}, nil
}

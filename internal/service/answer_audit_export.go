package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

// auditExportTemplateID pins the audit-grade export template. It is separate
// from the business template so the two channels never share a canonical
// payload shape (doc/124 §4.2).
const auditExportTemplateID = "answer-audit-v1"

// auditExportIdempotencyPrefix keeps audit export jobs from colliding with
// business export jobs that reuse the same caller key.
const auditExportIdempotencyPrefix = "audit:"

// AuditExportInput drives GET /answer-snapshots/:snapshotId/audit-export.
type AuditExportInput struct {
	AnswerSnapshotID string
	IdempotencyKey   string
	TraceID          string
}

// CreateAuditExportForAnswer renders the audit-grade forensic JSON for one
// answer snapshot (doc/100 §7.3 contract 2, doc/124 §4). Unlike the business
// export it carries the full governance payload: AnswerRun model metadata,
// the AuthorizationProjection (including pushdown evidence and redaction
// reasons) and the execution trace. The authorization projection is NOT used
// to redact here: the audit channel is the authoritative forensic view and is
// gated by its own permission (audit-export:read).
func (s *Service) CreateAuditExportForAnswer(ctx context.Context, actorID, tenantID string, input AuditExportInput) (*ExportResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "audit-export", tenantID, ""); err != nil {
		return nil, err
	}
	if input.AnswerSnapshotID == "" {
		return nil, httperr.BadRequest(40130, "answer snapshot id is required")
	}
	snapshot, err := s.Store.GetAnswerSnapshot(ctx, tenantID, input.AnswerSnapshotID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httperr.NotFound("answer snapshot not found")
		}
		return nil, httperr.New(502, 50200, "answer snapshot lookup failed")
	}
	if snapshot == nil {
		return nil, httperr.NotFound("answer snapshot not found")
	}
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, err
	}
	run, err := s.Store.GetAnswerRun(ctx, tenantID, snapshot.AnswerRunID)
	if err != nil {
		return nil, httperr.New(502, 50200, "answer run lookup failed")
	}
	_ = run
	// The audit channel reads the projection regardless of the requesting
	// principal: the delivery-time projection is evidence, not a visibility
	// filter for this export.
	projection, err := s.Store.GetAuthorizationProjection(ctx, tenantID, snapshot.ID, snapshot.TenantID, "web")
	if err != nil || projection == nil {
		// Fall back to any projection row for the snapshot.
		if projection, err = s.Store.GetAuthorizationProjectionAny(ctx, tenantID, snapshot.ID); err != nil || projection == nil {
			return nil, httperr.New(502, 50200, "authorization projection lookup failed")
		}
	}

	idempotencyKey := auditExportIdempotencyPrefix + input.IdempotencyKey
	if input.IdempotencyKey == "" {
		idempotencyKey = auditExportIdempotencyPrefix + id.New()
	}
	if len(idempotencyKey) > 128 {
		return nil, httperr.BadRequest(40130, "export idempotency key is too long")
	}
	if existing, err := s.Store.GetExportJobByIdempotencyKey(ctx, tenantID, idempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.AnswerSnapshotID != snapshot.ID {
			return nil, httperr.New(409, 40930, "export idempotency key already used for a different request")
		}
		artifact, err := s.Store.GetExportArtifactByJob(ctx, tenantID, existing.ID)
		if err == nil {
			return &ExportResult{Job: existing, Artifact: artifact}, nil
		}
	}

	payload := map[string]interface{}{
		"audit_scope":           "full",
		"exported_at":           time.Now().UTC().Format(time.RFC3339),
		"answer_snapshot":       snapshot,
		"answer_run":            run,
		"authorization":         projection,
		"policy_version":        projection.PolicyVersion,
		"policy_input_hash":     projection.PolicyInputHash,
		"decision_hash":         projection.DecisionHash,
		"retrieval_pushdown":    json.RawMessage(projection.RetrievalPushdownJSON),
		"redaction_reasons":     json.RawMessage(projection.RedactionReasonsJSON),
		"canonical_hash":        snapshot.CanonicalHash,
		"answer_schema_version": snapshot.AnswerSchemaVersion,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, httperr.New(502, 50200, "audit export serialization failed")
	}

	now := time.Now().UTC()
	template := &model.ExportTemplateVersion{
		ID: id.New(), TenantID: tenantID, TemplateID: auditExportTemplateID,
		Version: defaultExportTemplateVersion, Format: model.ExportFormatJSON,
		RendererVersion:    defaultExportRendererVersion,
		SchemaVersionsJSON: `["` + model.AnswerSchemaVersion + `"]`,
		TemplateJSON:       `{"title":"RAGFlow-X Audit Answer","audit_scope":"full"}`,
	}
	if err := s.Store.UpsertDefaultExportTemplate(ctx, template); err != nil {
		return nil, err
	}
	exportSnapshot := &model.ExportSnapshot{
		TenantID: tenantID, ProjectID: snapshot.ProjectID,
		AnswerSnapshotIDs: `["` + snapshot.ID + `"]`,
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
		RequestedBy: actorID, Format: model.ExportFormatJSON, Status: model.ExportJobQueued,
		TemplateVersion: template.Version, PolicyVersion: projection.PolicyVersion,
		PolicyEvaluatedAt: projection.PolicyEvaluatedAt, PolicyInputHash: projection.PolicyInputHash,
		TraceID: input.TraceID, IdempotencyKey: idempotencyKey, CreatedAt: now,
	}
	if err := s.Store.CreateExportJob(ctx, job); err != nil {
		return nil, err
	}
	if err := s.Store.UpdateExportJob(ctx, tenantID, job.ID, map[string]interface{}{"status": model.ExportJobRunning}); err != nil {
		return nil, err
	}
	job.Status = model.ExportJobRunning
	artifact, err := s.writeAuditExportArtifact(ctx, job, payloadJSON)
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

// writeAuditExportArtifact persists the forensic JSON through the same file
// layout as the business renderer but with an audit filename.
func (s *Service) writeAuditExportArtifact(ctx context.Context, job *model.ExportJob, content []byte) (*model.ExportArtifact, error) {
	filename := fmt.Sprintf("answer-audit-%s.json", job.AnswerSnapshotID)
	dir := filepath.Join(s.DataDir, "exports", job.TenantID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, job.ID+".json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, err
	}
	return &model.ExportArtifact{
		TenantID: job.TenantID, ProjectID: job.ProjectID, ExportJobID: job.ID,
		Format: model.ExportFormatJSON, RendererVersion: defaultExportRendererVersion,
		FileRef: path, Filename: filename, MimeType: exportMime(model.ExportFormatJSON),
		ByteSize: int64(len(content)), SHA256: sha256Hex(string(content)),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}, nil
}

package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report artifacts (doc/288 §7.7 / §12).
//
// Two invariants are enforced here rather than in the service, because both are
// facts about rows and both would survive a service-level check that ran a moment
// too early:
//
//  1. An artifact is only ever minted from a *published* revision. The status is
//     re-read under the revision row lock inside the transaction, so a revision
//     that was superseded between the service's read and this write cannot slip a
//     file through.
//  2. `artifact_version` is allocated as MAX+1 for `(revision, format)` under that
//     same lock. The partial unique index
//     `uidx_rgx_export_artifact_version` is the backstop for a second process:
//     losing the race is a 44113, never two rows claiming v2.
//
// The three rows (snapshot, job, artifact) are written together in the caller's
// service transaction. The artifact table is insert-only — the answer-delivery
// immutability anchors raise on any UPDATE (migrate.go:1875 / migration 108) — so
// the service renders the bytes *before* this call and passes an artifact whose
// status/sha256/byte_size are already final (matching answer_export.go). A failed
// render passes a `failed` artifact: it still consumes a version, and the
// idempotent replay of the same request returns it as-is (§7.7 / A30b / A40).

// ErrReportRevisionNotPublished is the repository-level answer for "this revision
// cannot be exported". The service maps it to 409/44106; it is a distinct error
// because `gorm.ErrRecordNotFound` already means "no such revision" (44108).
var ErrReportRevisionNotPublished = errors.New("report revision is not published")

// IsReportExportConflict reports whether err is the artifact-version unique
// constraint firing. It exists so the dialect matching stays in this package
// (`isDuplicateKeyErr`), where the "no TranslateError" caveat is documented.
func IsReportExportConflict(err error) bool {
	if err == nil {
		return false
	}
	return isDuplicateKeyErr(err)
}

// ReportExportRows is the write set of one export attempt. ArtifactVersion is
// assigned by CreateReportExport, not by the caller: a version chosen outside the
// lock is a guess.
type ReportExportRows struct {
	TenantID   string
	RevisionID string
	Snapshot   *model.ExportSnapshot
	Job        *model.ExportJob
	Artifact   *model.ExportArtifact
}

// CreateReportExport allocates the next artifact version and writes the attempt.
func (s *store) CreateReportExport(
	ctx context.Context, rows ReportExportRows,
) (*model.ExportArtifact, error) {
	// The per-tenant mutex is held outside the transaction on purpose: SQLite has
	// no row locks, so on that dialect this mutex is the only thing that serialises
	// two 导出 clicks in the same process (same reasoning as DecideApprovalStep).
	unlock := s.lockTenant(rows.TenantID)
	defer unlock()

	var artifact model.ExportArtifact
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var revision model.ReportRevision
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", rows.TenantID, rows.RevisionID).
			First(&revision).Error; err != nil {
			return err
		}
		if revision.Status != model.ReportRevisionPublished {
			return ErrReportRevisionNotPublished
		}
		var maxVersion int64
		if err := tx.Model(&model.ExportArtifact{}).
			Where("report_revision_id = ? AND format = ?", rows.RevisionID, rows.Artifact.Format).
			Select("COALESCE(MAX(artifact_version), 0)").
			Row().Scan(&maxVersion); err != nil {
			return err
		}
		rows.Artifact.ArtifactVersion = maxVersion + 1
		if err := tx.Create(rows.Snapshot).Error; err != nil {
			return err
		}
		if err := tx.Create(rows.Job).Error; err != nil {
			return err
		}
		if err := tx.Create(rows.Artifact).Error; err != nil {
			return err
		}
		artifact = *rows.Artifact
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

// ListReportCitations returns the citation rows of one revision, ordered by
// section then by the citation's own position inside it. The export payload keeps
// the ids (doc/288 §12: "含每节 section_version、引用 id"), so the frozen copy can
// be traced back to `rgx_report_citation` even after the evidence snapshots expire.
func (s *store) ListReportCitations(
	ctx context.Context, tenantID, revisionID string,
) ([]model.ReportCitation, error) {
	var citations []model.ReportCitation
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND revision_id = ?", tenantID, revisionID).
		Order("section_ord ASC, ord ASC").
		Find(&citations).Error
	return citations, err
}

// CountUnverifiableReportEvidence answers "can the citations of this revision still
// be re-checked?" in one query (doc/288 §7.6 / A43).
//
// It counts citations whose frozen snapshot is either gone (purged by the evidence
// clock) or already past its own `expires_at`. Both mean the same thing to a reader:
// the body and the judgement stand, but the evidence can no longer be re-opened. A
// LEFT JOIN keeps this to a single round trip — hydrating every citation to count
// them would put the whole evidence corpus on the read path of a page view.
func (s *store) CountUnverifiableReportEvidence(
	ctx context.Context, tenantID, revisionID string, now time.Time,
) (int64, error) {
	var unverifiable int64
	err := s.WithContext(ctx).
		Table("rgx_report_citation AS c").
		Joins("LEFT JOIN rgx_report_evidence_snapshot AS e ON e.id = c.evidence_snapshot_id AND e.tenant_id = c.tenant_id").
		Where("c.tenant_id = ? AND c.revision_id = ?", tenantID, revisionID).
		Where("e.id IS NULL OR e.expires_at <= ?", now).
		Count(&unverifiable).Error
	return unverifiable, err
}

// ListReportEvidenceByIDs loads the evidence snapshots a citation set points at.
// Missing ids are simply absent from the result: the appendix builder decides what
// absence means (no citations vs. a purged snapshot, A43), because those two cases
// have different honest answers.
func (s *store) ListReportEvidenceByIDs(
	ctx context.Context, tenantID string, ids []string,
) ([]model.ReportEvidenceSnapshot, error) {
	if len(ids) == 0 {
		return []model.ReportEvidenceSnapshot{}, nil
	}
	var snapshots []model.ReportEvidenceSnapshot
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Find(&snapshots).Error
	return snapshots, err
}

// ReportExportArtifact loads one artifact row within one tenant. It intentionally
// does not filter on subject_type: the caller must pair it with the export job
// (whose `subject_type` is authoritative) so a misdirected artifact id cannot be
// validated by coincidence.
func (s *store) ReportExportArtifact(ctx context.Context, tenantID, artifactID string) (*model.ExportArtifact, error) {
	var artifact model.ExportArtifact
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, artifactID).First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

// ListReportArtifacts returns the export history of one revision, newest delivery
// first. Soft-deleted rows are excluded (they are gone from the user's view and
// re-exporting is the honest next step); failed/expired rows stay because the UI
// explains each one with a distinct label (doc/288 §7.7).
//
// The order is `created_at`, not `artifact_version`: versions are allocated per
// (revision, format) — a DOCX and a Markdown delivery of the same revision are both
// "第 1 次导出" of their own format — so version alone cannot order a mixed-format
// history. The newest file is the one written last.
func (s *store) ListReportArtifacts(ctx context.Context, tenantID, revisionID string) ([]model.ExportArtifact, error) {
	var artifacts []model.ExportArtifact
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND report_revision_id = ? AND subject_type = ?",
			tenantID, revisionID, model.ExportSubjectReport).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Order("artifact_version DESC").
		Find(&artifacts).Error
	return artifacts, err
}

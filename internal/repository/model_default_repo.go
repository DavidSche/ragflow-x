package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm/clause"
)

// ModelDefaultRepo is the default-model recovery claim store (doc/269 §4.2).
type ModelDefaultRepo interface {
	// CreateModelDefaultRecoveryClaim atomically claims a pending operation by
	// operation_ref. ON CONFLICT DO NOTHING guarantees exactly one worker wins;
	// inserted=false means another worker (or a previous run) already holds the
	// claim. The audit table is never used as the coordination point and never
	// mutated here (append-only hash chain, v9 self-audit ⑧(a)).
	CreateModelDefaultRecoveryClaim(ctx context.Context, claim *model.ModelDefaultRecoveryClaim) (inserted bool, err error)
	// FinishModelDefaultRecoveryClaim stamps finished_at after the terminal
	// audit row B has been appended.
	FinishModelDefaultRecoveryClaim(ctx context.Context, operationRef string) error
	// ListPendingModelDefaultAudits returns default-model audit rows still in
	// "pending" whose operation started before pendingBefore. Only the scan
	// threshold (e.g. 5 minutes) — not proof the operation died; callers must
	// confirm no worker holds the model-default lock and no claim is in flight.
	ListPendingModelDefaultAudits(ctx context.Context, pendingBefore time.Time) ([]model.AuditLog, error)
	// HasDefaultModelTerminalAudit reports whether a terminal row (succeeded /
	// failed / unknown) already exists for the operation.
	HasDefaultModelTerminalAudit(ctx context.Context, operationRef string) (bool, error)
}

// operationRefLikePattern builds the detail_json LIKE pattern anchoring the
// operation_ref field (% is the SQL wildcard, kept literal outside the JSON
// key so the JSON string itself is matched verbatim). The operation_ref value
// is escaped with the package's canonical LIKE escaper (filters.go).
func operationRefLikePattern(operationRef string) string {
	return "%" + `"operation_ref":"` + escapeLike(operationRef) + "\"%"
}

func (s *store) CreateModelDefaultRecoveryClaim(ctx context.Context, claim *model.ModelDefaultRecoveryClaim) (bool, error) {
	if claim.ClaimedAt.IsZero() {
		claim.ClaimedAt = time.Now().UTC()
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(claim)
	return res.RowsAffected > 0, res.Error
}

func (s *store) FinishModelDefaultRecoveryClaim(ctx context.Context, operationRef string) error {
	now := time.Now().UTC()
	return s.WithContext(ctx).Model(&model.ModelDefaultRecoveryClaim{}).
		Where("operation_ref = ?", operationRef).
		Update("finished_at", now).Error
}

func (s *store) ListPendingModelDefaultAudits(ctx context.Context, pendingBefore time.Time) ([]model.AuditLog, error) {
	var rows []model.AuditLog
	err := s.WithContext(ctx).
		Where("action = ? AND result = ? AND at < ?", "model-default.write", "pending", pendingBefore).
		Order("at ASC").
		Find(&rows).Error
	return rows, err
}

func (s *store) HasDefaultModelTerminalAudit(ctx context.Context, operationRef string) (bool, error) {
	var n int64
	pattern := operationRefLikePattern(operationRef)
	err := s.WithContext(ctx).Model(&model.AuditLog{}).
		Where("action = ? AND result IN ? AND detail_json LIKE ?",
			"model-default.write", []string{"succeeded", "failed", "unknown"}, pattern).
		Count(&n).Error
	return n > 0, err
}

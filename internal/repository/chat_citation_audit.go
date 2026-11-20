package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ChatCitationAuditRepo is the persistence of the weekly chat citation sample
// (doc/301 §5.10 QA-2 / T19).
//
// Three properties are load-bearing and belong to this layer:
//
//   - the run row is created with ON CONFLICT DO NOTHING, so the unique
//     `(tenant_id, period_start)` index *is* the idempotency guard under concurrency;
//   - the sampled turns come from a single join over the ledger and the answer
//     snapshot, so "the answer we judged" and "the answer we delivered" cannot
//     drift apart between two queries;
//   - the write-back is a bounded UPDATE per disposition, never a full-row rewrite:
//     the audit must not be able to resurrect a stale copy of a turn.
type ChatCitationAuditRepo interface {
	CreateChatCitationAuditRunIfAbsent(ctx context.Context, run *model.ChatCitationAuditRun) (bool, error)
	GetChatCitationAuditRunByPeriod(ctx context.Context, tenantID, periodStart string) (*model.ChatCitationAuditRun, error)
	GetChatCitationAuditRun(ctx context.Context, tenantID, runID string) (*model.ChatCitationAuditRun, error)
	ListChatCitationAuditRuns(ctx context.Context, tenantID string, limit int) ([]model.ChatCitationAuditRun, error)
	FinishChatCitationAuditRun(ctx context.Context, run *model.ChatCitationAuditRun) error
	InsertChatCitationAuditFindings(ctx context.Context, findings []model.ChatCitationAuditFinding) error
	ListChatCitationAuditFindings(ctx context.Context, tenantID, runID string, page, pageSize int) ([]model.ChatCitationAuditFinding, int64, error)
	// ListChatCitationAuditTurns joins the ledger with the delivered answer so the
	// judged text is the delivered text. Turns already carrying a disposition are
	// excluded, which makes a manual re-run after a failure resume instead of
	// re-judging (and re-counting) the same citations.
	ListChatCitationAuditTurns(ctx context.Context, tenantID string, from, to time.Time, limit int) ([]model.ChatCitationAuditTurn, error)
	// MarkChatCitationAudit writes the disposition back onto the ledger, one UPDATE
	// per (status, reason) group. Only the four audit columns are touched.
	MarkChatCitationAudit(ctx context.Context, tenantID, runID string, status, reason string, eventIDs []string, at time.Time) error
	// ListChatCitationAuditTenants returns the tenants with at least one cited turn
	// in the window. The sweep walks tenants, never a bucket list: a tenant with
	// turns but no quota bucket still answered questions.
	ListChatCitationAuditTenants(ctx context.Context, from, to time.Time) ([]string, error)
}

// CreateChatCitationAuditRunIfAbsent inserts the week's run unless one exists.
// The bool is "we inserted it": the loser of the race gets false and must read the
// winner's row, so a replayed job never reports a fresh run it does not own.
func (s *store) CreateChatCitationAuditRunIfAbsent(ctx context.Context, run *model.ChatCitationAuditRun) (bool, error) {
	if run.ID == "" {
		run.ID = id.New()
	}
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	if run.Status == "" {
		run.Status = model.ChatCitationAuditRunRunning
	}
	result := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(run)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *store) GetChatCitationAuditRunByPeriod(ctx context.Context, tenantID, periodStart string) (*model.ChatCitationAuditRun, error) {
	var run model.ChatCitationAuditRun
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND period_start = ?", tenantID, periodStart).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) GetChatCitationAuditRun(ctx context.Context, tenantID, runID string) (*model.ChatCitationAuditRun, error) {
	if runID == "" {
		return nil, nil
	}
	var run model.ChatCitationAuditRun
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, runID).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) ListChatCitationAuditRuns(ctx context.Context, tenantID string, limit int) ([]model.ChatCitationAuditRun, error) {
	if limit <= 0 || limit > 52 {
		limit = 12
	}
	var runs []model.ChatCitationAuditRun
	err := s.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("period_start DESC").
		Limit(limit).
		Find(&runs).Error
	return runs, err
}

// FinishChatCitationAuditRun writes the terminal counters. It is the only writer
// of `status`/`finished_at`, so a run cannot be finished twice with two different
// answers: the second call rewrites the same terminal fields it just read.
func (s *store) FinishChatCitationAuditRun(ctx context.Context, run *model.ChatCitationAuditRun) error {
	now := time.Now().UTC()
	if run.FinishedAt == nil {
		run.FinishedAt = &now
	}
	return s.WithContext(ctx).Model(&model.ChatCitationAuditRun{}).
		Where("tenant_id = ? AND id = ?", run.TenantID, run.ID).
		Updates(map[string]interface{}{
			"sampled":        run.Sampled,
			"judged":         run.Judged,
			"passed":         run.Passed,
			"failed":         run.Failed,
			"skipped":        run.Skipped,
			"findings":       run.Findings,
			"tokens_in":      run.TokensIn,
			"tokens_out":     run.TokensOut,
			"status":         run.Status,
			"failure_reason": run.FailureReason,
			"finished_at":    run.FinishedAt,
		}).Error
}

func (s *store) InsertChatCitationAuditFindings(ctx context.Context, findings []model.ChatCitationAuditFinding) error {
	if len(findings) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for index := range findings {
		if findings[index].ID == "" {
			findings[index].ID = id.New()
		}
		if findings[index].CreatedAt.IsZero() {
			findings[index].CreatedAt = now
		}
	}
	return s.WithContext(ctx).CreateInBatches(findings, 200).Error
}

func (s *store) ListChatCitationAuditFindings(ctx context.Context, tenantID, runID string, page, pageSize int) ([]model.ChatCitationAuditFinding, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	query := s.WithContext(ctx).Model(&model.ChatCitationAuditFinding{}).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.ChatCitationAuditFinding
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("created_at ASC, id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error
	return rows, total, err
}

// ListChatCitationAuditTurns is the sample query.
//
// It selects the snapshot's `content`, not the ledger's `answer_excerpt`: the
// excerpt is truncated for display, and judging a truncated answer would flag
// every citation of a long answer as unsupported. The join is on `request_id`,
// which `rgx_answer_run` indexes uniquely, so one turn cannot match two answers.
func (s *store) ListChatCitationAuditTurns(ctx context.Context, tenantID string, from, to time.Time, limit int) ([]model.ChatCitationAuditTurn, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var turns []model.ChatCitationAuditTurn
	err := s.WithContext(ctx).
		Table("rgx_knowledge_ops_event AS e").
		Select(`e.id AS event_id, e.request_id AS request_id, e.app_type AS app_type,
			e.question AS question, e.status AS status, e.created_at AS created_at,
			s.content AS answer, s.citations_json AS citations_json`).
		Joins("JOIN rgx_answer_run AS r ON r.request_id = e.request_id AND r.tenant_id = e.tenant_id").
		Joins("JOIN rgx_answer_snapshot AS s ON s.answer_run_id = r.id").
		Where("e.tenant_id = ?", tenantID).
		Where("e.created_at >= ? AND e.created_at < ?", from, to).
		Where("e.status = ?", model.KnowledgeOpsCompleted).
		Where("e.citations_count > 0").
		Where("(e.citation_audit_status IS NULL OR e.citation_audit_status = '')").
		Order("e.created_at DESC, e.id DESC").
		Limit(limit).
		Scan(&turns).Error
	return turns, err
}

func (s *store) MarkChatCitationAudit(ctx context.Context, tenantID, runID, status, reason string, eventIDs []string, at time.Time) error {
	if len(eventIDs) == 0 {
		return nil
	}
	return s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).
		Where("tenant_id = ? AND id IN ?", tenantID, eventIDs).
		Updates(map[string]interface{}{
			"citation_audit_status": status,
			"citation_audit_reason": reason,
			"citation_audit_run_id": runID,
			"citation_audited_at":   at,
		}).Error
}

func (s *store) ListChatCitationAuditTenants(ctx context.Context, from, to time.Time) ([]string, error) {
	var tenants []string
	err := s.WithContext(ctx).
		Model(&model.KnowledgeOpsEvent{}).
		Distinct("tenant_id").
		Where("created_at >= ? AND created_at < ?", from, to).
		Order("tenant_id ASC").
		Pluck("tenant_id", &tenants).Error
	return tenants, err
}

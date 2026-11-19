package repository

// Report schedules (doc/295 §2.4 / R20).
//
// The scheduler's only hard problem is that a tick can crash at any point. The
// resolution here is the same one the charge ledger uses: make the expensive step
// idempotent and write the claim down first.
//
//	ClaimDueReportSchedules  selects due rows, advances `next_run_at` for each one
//	                         and commits — all in one transaction, before the caller
//	                         starts any report run.
//
// So a crash after the claim loses one occurrence (the run is late by a day), and a
// crash before it costs nothing. The opposite ordering — run then advance — turns a
// crash into an infinite retrigger of the same minute. `schedule:<id>:<due>` on the
// run's idempotency key is the second line of defence for the window where the
// claim committed but the enqueue did not.

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ReportScheduleFinish is one tick's outcome, written back after the run was
// attempted. `Status` is the closed set on the model (`created` / the four
// `skipped_*` / `failed`); `Revision` is what the run minted, 0 when nothing ran.
type ReportScheduleFinish struct {
	TenantID  string
	ReportID  string
	Now       time.Time
	Status    string
	Revision  int64
	Error     string
	NextRunAt time.Time
}

func (s *store) GetReportSchedule(
	ctx context.Context, tenantID, reportID string,
) (*model.ReportSchedule, error) {
	var row model.ReportSchedule
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *store) CreateReportSchedule(ctx context.Context, schedule *model.ReportSchedule) error {
	return s.WithContext(ctx).Create(schedule).Error
}

func (s *store) UpdateReportSchedule(ctx context.Context, schedule *model.ReportSchedule) error {
	// `last_*` and `created_*` are excluded on purpose: a plan edit must not be
	// able to claim a run happened or did not happen.
	return s.WithContext(ctx).Model(&model.ReportSchedule{}).
		Where("tenant_id = ? AND report_id = ?", schedule.TenantID, schedule.ReportID).
		Updates(map[string]interface{}{
			"cadence":       schedule.Cadence,
			"hour":          schedule.Hour,
			"minute":        schedule.Minute,
			"weekday":       schedule.Weekday,
			"tz_offset_min": schedule.TZOffsetMin,
			"update_mode":   schedule.UpdateMode,
			"enabled":       schedule.Enabled,
			"next_run_at":   schedule.NextRunAt,
			"updated_at":    schedule.UpdatedAt,
		}).Error
}

func (s *store) DeleteReportSchedule(ctx context.Context, tenantID, reportID string) (bool, error) {
	result := s.WithContext(ctx).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		Delete(&model.ReportSchedule{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *store) ListReportSchedules(
	ctx context.Context, tenantID string,
) ([]model.ReportSchedule, error) {
	var rows []model.ReportSchedule
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).
		Order("next_run_at ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ClaimDueReportSchedules claims and advances in one transaction.
//
// `skipLocked` is not used: SQLite has no `FOR UPDATE SKIP LOCKED`, and a single
// tick worker claims at most `limit` rows per tenant. Two workers would serialize on
// the row locks and the second would find `next_run_at` already in the future, which
// is exactly the desired outcome (no double run) — just not the fastest one.
func (s *store) ClaimDueReportSchedules(
	ctx context.Context, now time.Time, limit int,
	advance func(row *model.ReportSchedule, from time.Time) time.Time,
) ([]model.ReportSchedule, error) {
	if limit <= 0 {
		limit = 50
	}
	claimed := make([]model.ReportSchedule, 0, limit)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.ReportSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("enabled = ? AND next_run_at <= ?", true, now).
			Order("next_run_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for index := range rows {
			row := &rows[index]
			// The advance is computed by the service (a pure function over the
			// cadence), then written here. `next_run_at` must move forward even if the
			// run fails, otherwise a failing report retriggers every tick forever.
			advanced := row.NextRunAt
			if row.NextRunAt.After(now) {
				// Already advanced by a concurrent claim; skip it rather than run it.
				continue
			}
			if advance == nil {
				// A nil advance still claims (and stops) rather than guessing a cadence
				// here: the arithmetic belongs to the service, and a repository that
				// invented it would be a second definition of `daily`.
				advanced = now.Add(24 * time.Hour)
			} else {
				advanced = advance(row, now)
			}
			if !advanced.After(now) {
				// A pure function that did not move forward would retrigger forever;
				// refuse the claim instead of writing a fixed point.
				continue
			}
			if err := tx.Model(&model.ReportSchedule{}).
				Where("id = ? AND next_run_at = ?", row.ID, row.NextRunAt).
				Updates(map[string]interface{}{
					"next_run_at": advanced, "updated_at": now,
				}).Error; err != nil {
				return err
			}
			row.NextRunAt = advanced
			claimed = append(claimed, *row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *store) FinishReportScheduleRun(ctx context.Context, finish ReportScheduleFinish) (bool, error) {
	updates := map[string]interface{}{
		"last_run_at":   finish.Now,
		"last_status":   finish.Status,
		"last_error":    finish.Error,
		"last_revision": finish.Revision,
		"updated_at":    finish.Now,
	}
	if !finish.NextRunAt.IsZero() {
		updates["next_run_at"] = finish.NextRunAt
	}
	result := s.WithContext(ctx).Model(&model.ReportSchedule{}).
		Where("tenant_id = ? AND report_id = ?", finish.TenantID, finish.ReportID).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// MarkReportRevisionScheduled stamps a robot-started revision. It is a plain
// update because the row was just created by the regeneration transaction and no
// other writer may touch it until it is armed.
func (s *store) MarkReportRevisionScheduled(
	ctx context.Context, tenantID, revisionID, scheduleID string, now time.Time,
) error {
	return s.WithContext(ctx).Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND id = ?", tenantID, revisionID).
		Updates(map[string]interface{}{
			"trigger":     model.ReportTriggerSchedule,
			"schedule_id": scheduleID,
			"updated_at":  now,
		}).Error
}

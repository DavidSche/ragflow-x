package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ConnectorRepo persists data-source connector bindings and their run history
// (doc/225 §5). Connector rows are soft-deleted (gorm.DeletedAt) so approved
// lifecycle changes stay auditable; run rows are never hard-deleted by sadpath
// flows and are only removed by the retention janitor.
type ConnectorRepo interface {
	CreateConnector(ctx context.Context, c *model.Connector) error
	GetConnector(ctx context.Context, tenantID, id string) (*model.Connector, error)
	GetConnectorForScope(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*model.Connector, error)
	ListConnectorsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, status, datasetID string, page, pageSize int) ([]model.Connector, int64, error)
	// ListConnectorsForDataset returns all active bindings feeding one dataset.
	// It is unpaginated because source-identity conflict checks must see legacy
	// rows whose ownership key predates the unique index (doc/227 C2.3).
	ListConnectorsForDataset(ctx context.Context, tenantID, datasetID string) ([]model.Connector, error)
	ListEnabledConnectorsForTenant(ctx context.Context, tenantIDs []string) ([]model.Connector, error)
	UpdateConnector(ctx context.Context, c *model.Connector) (bool, error)
	SetConnectorEnabled(ctx context.Context, tenantID, id string, disabled bool) (bool, error)
	// SaveConnectorCursor durably advances the incremental cursor mid-run so a
	// cancelled/timed-out run keeps the pages it already applied (doc/225 §6.2).
	SaveConnectorCursor(ctx context.Context, tenantID, id string, cursorJSON string) (bool, error)
	SetConnectorCursor(ctx context.Context, tenantID, id string, cursorJSON string, lastRunAt *time.Time, lastRunStatus, status string) (bool, error)
	// SetConnectorRunState updates only run bookkeeping (status + last run),
	// leaving the incremental cursor untouched: the cursor is advanced by
	// SaveConnectorCursor during the run and must not be rolled back by the
	// terminal status write (doc/225 F5).
	SetConnectorRunState(ctx context.Context, tenantID, id string, lastRunAt *time.Time, lastRunStatus, status string) (bool, error)
	DeleteConnector(ctx context.Context, tenantID, id string) (bool, error)
	// ListSchedulableConnectors returns enabled connectors whose next scheduled
	// run is due at or before now (manual kind never matches). Used by the
	// self-rescheduling connector scheduler.
	ListSchedulableConnectors(ctx context.Context, now time.Time) ([]model.Connector, error)
	// ConnectorScheduleRunAfter resolves when a connector's next run is due
	// based on its schedule kind (UTC minute-of-day per doc/225 F7).
	ConnectorScheduleRunAfter(ctx context.Context, c *model.Connector, now time.Time) (time.Time, error)

	CreateConnectorRun(ctx context.Context, run *model.ConnectorRun) error
	GetConnectorRun(ctx context.Context, tenantID, runID string) (*model.ConnectorRun, error)
	GetConnectorRunForScope(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID, runID string) (*model.ConnectorRun, error)
	ListConnectorRunsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID string, page, pageSize int) ([]model.ConnectorRun, int64, error)
	ListActiveConnectorRuns(ctx context.Context, connectorID string) (int64, error)
	SetConnectorRunResult(ctx context.Context, runID string, outcome ConnectorRunOutcome) (bool, error)
	// DeleteExpiredConnectorRuns prunes run rows finished before cutoff
	// (doc/225 §6.2 F4), returning removed rows grouped by provider so the
	// runs_pruned_total metric keeps its provider label.
	DeleteExpiredConnectorRuns(ctx context.Context, cutoff time.Time) (map[string]int64, error)
}

// ConnectorRunOutcome carries the terminal fields of one sync run.
type ConnectorRunOutcome struct {
	Status       string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	Added        int
	Changed      int
	Deleted      int
	Skipped      int
	Failed       int
	DurationMS   int64
	ErrorSummary string
	DetailJSON   string
	TraceID      string
}

func (s *store) CreateConnector(ctx context.Context, c *model.Connector) error {
	return s.WithContext(ctx).Create(c).Error
}

func (s *store) GetConnector(ctx context.Context, tenantID, id string) (*model.Connector, error) {
	var c model.Connector
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *store) GetConnectorForScope(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*model.Connector, error) {
	var c model.Connector
	q := s.WithContext(ctx).Where("id = ?", id)
	if !scopeAll {
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *store) ListConnectorsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, status, datasetID string, page, pageSize int) ([]model.Connector, int64, error) {
	q := s.WithContext(ctx).Model(&model.Connector{})
	if !scopeAll {
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if datasetID != "" {
		q = q.Where("target_dataset_id = ?", datasetID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	var rows []model.Connector
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *store) ListConnectorsForDataset(ctx context.Context, tenantID, datasetID string) ([]model.Connector, error) {
	var rows []model.Connector
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND target_dataset_id = ?", tenantID, datasetID).
		Find(&rows).Error
	return rows, err
}

func (s *store) ListEnabledConnectorsForTenant(ctx context.Context, tenantIDs []string) ([]model.Connector, error) {
	var rows []model.Connector
	err := s.WithContext(ctx).
		Where("tenant_id IN ? AND enabled = ? AND status <> ?", tenantIDs, true, model.ConnectorStatusDisabled).
		Find(&rows).Error
	return rows, err
}

func (s *store) UpdateConnector(ctx context.Context, c *model.Connector) (bool, error) {
	result := s.WithContext(ctx).Model(&model.Connector{}).
		Where("tenant_id = ? AND id = ?", c.TenantID, c.ID).
		Select("name", "provider", "adapter_kind", "source_ownership_key",
			"external_connector_id", "target_dataset_id", "spec_json",
			"credential_crypted", "has_credential", "credential_version",
			"schedule_kind", "schedule_value", "schedule_tz_offset_min",
			"sensitive", "enabled", "status").
		Updates(c)
	return result.RowsAffected > 0, result.Error
}

func (s *store) SetConnectorEnabled(ctx context.Context, tenantID, id string, disabled bool) (bool, error) {
	status := model.ConnectorStatusIdle
	if disabled {
		status = model.ConnectorStatusDisabled
	}
	result := s.WithContext(ctx).Model(&model.Connector{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]any{"enabled": !disabled, "status": status})
	return result.RowsAffected > 0, result.Error
}

func (s *store) SaveConnectorCursor(ctx context.Context, tenantID, id string, cursorJSON string) (bool, error) {
	result := s.WithContext(ctx).Model(&model.Connector{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("cursor_json", cursorJSON)
	return result.RowsAffected > 0, result.Error
}

func (s *store) SetConnectorCursor(ctx context.Context, tenantID, id string, cursorJSON string, lastRunAt *time.Time, lastRunStatus, status string) (bool, error) {
	if status == "" {
		status = model.ConnectorStatusIdle
	}
	result := s.WithContext(ctx).Model(&model.Connector{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]any{
			"cursor_json":     cursorJSON,
			"last_run_at":     lastRunAt,
			"last_run_status": lastRunStatus,
			// A run finishing after an operator disabled the connector must not
			// resurrect it: enabled is the source of truth for scheduling.
			"status": gorm.Expr("CASE WHEN enabled THEN ? ELSE ? END", status, model.ConnectorStatusDisabled),
		})
	return result.RowsAffected > 0, result.Error
}

func (s *store) SetConnectorRunState(ctx context.Context, tenantID, id string, lastRunAt *time.Time, lastRunStatus, status string) (bool, error) {
	if status == "" {
		status = model.ConnectorStatusIdle
	}
	result := s.WithContext(ctx).Model(&model.Connector{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]any{
			"last_run_at":     lastRunAt,
			"last_run_status": lastRunStatus,
			"status":          gorm.Expr("CASE WHEN enabled THEN ? ELSE ? END", status, model.ConnectorStatusDisabled),
		})
	return result.RowsAffected > 0, result.Error
}

func (s *store) DeleteConnector(ctx context.Context, tenantID, id string) (bool, error) {
	deleted := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c model.Connector
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&c).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		// Release the unique source identity before soft-deleting so the same
		// source can be re-registered without resurrecting history. The row
		// itself stays auditable under deleted_at.
		if err := tx.Model(&model.Connector{}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			Update("source_ownership_key", "deleted:"+id).Error; err != nil {
			return err
		}
		result := tx.Delete(&c)
		deleted = result.RowsAffected > 0
		return result.Error
	})
	return deleted, err
}

func (s *store) ListSchedulableConnectors(ctx context.Context, now time.Time) ([]model.Connector, error) {
	var rows []model.Connector
	err := s.WithContext(ctx).
		Where("enabled = ? AND schedule_kind <> ? AND status <> ?", true, model.ConnectorScheduleManual, model.ConnectorStatusDisabled).
		Find(&rows).Error
	return rows, err
}

func (s *store) ConnectorScheduleRunAfter(ctx context.Context, c *model.Connector, now time.Time) (time.Time, error) {
	switch c.ScheduleKind {
	case model.ConnectorScheduleManual:
		return time.Time{}, nil
	case model.ConnectorScheduleInterval:
		minutes := c.ScheduleValue
		if minutes < 5 {
			minutes = 30
		}
		return now.Add(time.Duration(minutes) * time.Minute).UTC(), nil
	case model.ConnectorScheduleDaily:
		// ScheduleValue is UTC minute-of-day (0..1439); display offset is
		// applied only at read time. Schedule in UTC always.
		m := c.ScheduleValue % 1440
		base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
			Add(time.Duration(m) * time.Minute)
		if !base.After(now) {
			base = base.AddDate(0, 0, 1)
		}
		return base, nil
	default:
		return time.Time{}, nil
	}
}

func (s *store) CreateConnectorRun(ctx context.Context, run *model.ConnectorRun) error {
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) GetConnectorRun(ctx context.Context, tenantID, runID string) (*model.ConnectorRun, error) {
	var run model.ConnectorRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, runID).First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) GetConnectorRunForScope(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID, runID string) (*model.ConnectorRun, error) {
	var run model.ConnectorRun
	q := s.WithContext(ctx).Where("id = ?", runID)
	if connectorID != "" {
		q = q.Where("connector_id = ?", connectorID)
	}
	if !scopeAll {
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) ListConnectorRunsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID string, page, pageSize int) ([]model.ConnectorRun, int64, error) {
	q := s.WithContext(ctx).Model(&model.ConnectorRun{})
	if connectorID != "" {
		q = q.Where("connector_id = ?", connectorID)
	}
	if !scopeAll {
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	var rows []model.ConnectorRun
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *store) ListActiveConnectorRuns(ctx context.Context, connectorID string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.ConnectorRun{}).
		Where("connector_id = ? AND status IN ?", connectorID,
			[]string{model.ConnectorRunStatusPending, model.ConnectorRunStatusRunning}).
		Count(&n).Error
	return n, err
}

func (s *store) SetConnectorRunResult(ctx context.Context, runID string, outcome ConnectorRunOutcome) (bool, error) {
	result := s.WithContext(ctx).Model(&model.ConnectorRun{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"status":        outcome.Status,
			"started_at":    outcome.StartedAt,
			"finished_at":   outcome.FinishedAt,
			"added":         outcome.Added,
			"changed":       outcome.Changed,
			"deleted":       outcome.Deleted,
			"skipped":       outcome.Skipped,
			"failed":        outcome.Failed,
			"duration_ms":   outcome.DurationMS,
			"error_summary": outcome.ErrorSummary,
			"detail_json":   outcome.DetailJSON,
			"trace_id":      outcome.TraceID,
		})
	return result.RowsAffected > 0, result.Error
}

func (s *store) DeleteExpiredConnectorRuns(ctx context.Context, cutoff time.Time) (map[string]int64, error) {
	counts := map[string]int64{}
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			Provider string
			Count    int64
		}
		if err := tx.Model(&model.ConnectorRun{}).
			Select("COALESCE(c.provider, 'unknown') AS provider, COUNT(*) AS count").
			Joins("LEFT JOIN rgx_connector AS c ON c.id = rgx_connector_run.connector_id").
			Where("rgx_connector_run.finished_at IS NOT NULL AND rgx_connector_run.finished_at < ?", cutoff).
			Group("COALESCE(c.provider, 'unknown')").
			Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			counts[row.Provider] = row.Count
		}
		return tx.Where("finished_at IS NOT NULL AND finished_at < ?", cutoff).
			Delete(&model.ConnectorRun{}).Error
	})
	return counts, err
}

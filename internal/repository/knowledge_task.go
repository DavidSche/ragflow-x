package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type KnowledgeTaskFilter struct {
	Status        string
	Category      string
	Attribution   string
	OwnerID       string
	SourceEventID string
	Search        string
	Overdue       bool
}

func (s *store) CreateKnowledgeTask(ctx context.Context, task *model.KnowledgeTask) error {
	if task.ID == "" {
		task.ID = id.New()
	}
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	return s.WithContext(ctx).Create(task).Error
}

func (s *store) GetKnowledgeTask(ctx context.Context, tenantID, taskID string, scopeAll bool) (*model.KnowledgeTask, error) {
	var task model.KnowledgeTask
	q := s.WithContext(ctx)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Where("id = ?", taskID).First(&task).Error
	return &task, err
}

func (s *store) ListKnowledgeTasks(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter KnowledgeTaskFilter) ([]model.KnowledgeTask, int64, error) {
	var list []model.KnowledgeTask
	var total int64
	q := s.WithContext(ctx).Model(&model.KnowledgeTask{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Category != "" {
		q = q.Where("category = ?", filter.Category)
	}
	if filter.Attribution != "" {
		q = q.Where("source_attribution = ?", filter.Attribution)
	}
	if filter.OwnerID != "" {
		q = q.Where("owner_id = ?", filter.OwnerID)
	}
	if filter.SourceEventID != "" {
		q = q.Where("source_event_id = ?", filter.SourceEventID)
	}
	if filter.Overdue {
		q = q.Where("due_at < ? AND status IN ?", time.Now().UTC(), []string{
			model.KnowledgeTaskOpen, model.KnowledgeTaskInProgress, model.KnowledgeTaskBlocked,
		})
	}
	if filter.Search != "" {
		pattern := likePattern(filter.Search)
		q = q.Where("title LIKE ? ESCAPE '\\'", pattern)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (s *store) UpdateKnowledgeTask(ctx context.Context, tenantID, taskID string, scopeAll bool, updates map[string]interface{}) (bool, error) {
	if updates == nil {
		return false, nil
	}
	updates["updated_at"] = time.Now().UTC()
	q := s.WithContext(ctx).Model(&model.KnowledgeTask{}).Where("id = ?", taskID)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	result := q.Updates(updates)
	return result.RowsAffected > 0, result.Error
}

func (s *store) KnowledgeTaskSummary(ctx context.Context, tenantID string, scopeAll bool) (*model.KnowledgeTaskSummary, error) {
	var row struct {
		Open            int64 `gorm:"column:open"`
		InProgress      int64 `gorm:"column:in_progress"`
		Blocked         int64 `gorm:"column:blocked"`
		PendingApproval int64 `gorm:"column:pending_approval"`
		Resolved        int64 `gorm:"column:resolved"`
		Canceled        int64 `gorm:"column:canceled"`
		Overdue         int64 `gorm:"column:overdue"`
	}
	q := s.WithContext(ctx).Model(&model.KnowledgeTask{}).Select(`
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS open,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS in_progress,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS blocked,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS pending_approval,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS resolved,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS canceled,
		SUM(CASE WHEN due_at < ? AND status IN (?, ?, ?) THEN 1 ELSE 0 END) AS overdue
	`, model.KnowledgeTaskOpen, model.KnowledgeTaskInProgress, model.KnowledgeTaskBlocked,
		model.KnowledgeTaskPendingApproval, model.KnowledgeTaskResolved, model.KnowledgeTaskCanceled,
		time.Now().UTC(), model.KnowledgeTaskOpen, model.KnowledgeTaskInProgress, model.KnowledgeTaskBlocked,
	)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.Scan(&row).Error; err != nil {
		return nil, err
	}
	return &model.KnowledgeTaskSummary{
		Open: row.Open, InProgress: row.InProgress, Blocked: row.Blocked,
		PendingApproval: row.PendingApproval, Resolved: row.Resolved, Canceled: row.Canceled,
		Overdue: row.Overdue,
	}, nil
}

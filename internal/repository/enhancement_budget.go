package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const enhancementQuotaKeyPrefix = "rgx-enhancement:"

// EnhancementBudgetRequest reserves an estimated token amount for one X-governed
// downstream task. All limits are monthly token budgets; zero means unlimited.
type EnhancementBudgetRequest struct {
	RequestID     string
	TenantID      string
	ProjectID     string
	DatasetID     string
	ProfileID     string
	AssistantID   string
	RunID         string
	PeriodStart   string
	Estimated     int64
	TenantBudget  int64
	DatasetBudget int64
	ProfileBudget int64
	RunBudget     int64
}

// EnhancementBudgetSnapshot freezes the observed preflight state and limits.
type EnhancementBudgetSnapshot struct {
	PeriodStart   string `json:"period_start"`
	QuotaKey      string `json:"quota_key"`
	TenantBudget  int64  `json:"tenant_budget"`
	DatasetBudget int64  `json:"dataset_budget"`
	ProfileBudget int64  `json:"profile_budget"`
	RunBudget     int64  `json:"run_budget"`
	TenantUsed    int64  `json:"tenant_used"`
	DatasetUsed   int64  `json:"dataset_used"`
	ProfileUsed   int64  `json:"profile_used"`
	RunUsed       int64  `json:"run_used"`
	Estimated     int64  `json:"estimated"`
}

func (s *store) ReserveEnhancementQuota(
	ctx context.Context,
	request EnhancementBudgetRequest,
) (*EnhancementBudgetSnapshot, string, error) {
	if request.RequestID == "" || request.TenantID == "" || request.Estimated < 0 {
		return nil, "", fmt.Errorf("request_id, tenant_id and a non-negative estimate are required")
	}
	if request.PeriodStart == "" {
		request.PeriodStart = time.Now().UTC().Format("2006-01")
	}
	quotaKey := enhancementQuotaKeyPrefix + request.TenantID
	var snapshot *EnhancementBudgetSnapshot
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.QuotaLimit{
			ID: id.New(), TenantID: request.TenantID, KeyID: quotaKey,
			PeriodStart: request.PeriodStart, TokenLimit: request.TenantBudget,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		if request.TenantBudget > 0 {
			if err := tx.Model(&model.QuotaLimit{}).
				Where("key_id = ? AND period_start = ?", quotaKey, request.PeriodStart).
				Update("token_limit", request.TenantBudget).Error; err != nil {
				return err
			}
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key_id = ? AND period_start = ?", quotaKey, request.PeriodStart).
			First(&model.QuotaLimit{}).Error; err != nil {
			return err
		}

		usage, err := enhancementBudgetUsage(tx, request)
		if err != nil {
			return err
		}
		snapshot = &EnhancementBudgetSnapshot{
			PeriodStart: request.PeriodStart, QuotaKey: quotaKey,
			TenantBudget: request.TenantBudget, DatasetBudget: request.DatasetBudget,
			ProfileBudget: request.ProfileBudget, RunBudget: request.RunBudget,
			TenantUsed: usage.Tenant, DatasetUsed: usage.Dataset,
			ProfileUsed: usage.Profile, RunUsed: usage.Run,
			Estimated: request.Estimated,
		}
		switch {
		case request.TenantBudget > 0 && usage.Tenant+request.Estimated > request.TenantBudget:
			return errEnhancementBudgetExceeded("tenant")
		case request.DatasetBudget > 0 && usage.Dataset+request.Estimated > request.DatasetBudget:
			return errEnhancementBudgetExceeded("dataset")
		case request.ProfileBudget > 0 && usage.Profile+request.Estimated > request.ProfileBudget:
			return errEnhancementBudgetExceeded("profile")
		case request.RunBudget > 0 && usage.Run+request.Estimated > request.RunBudget:
			return errEnhancementBudgetExceeded("run")
		}

		reservation := &model.QuotaReservation{
			RequestID: request.RequestID, TenantID: request.TenantID, KeyID: quotaKey,
			PeriodStart: request.PeriodStart, Estimated: request.Estimated,
			Status: model.QuotaReservationPending, ProjectID: request.ProjectID,
			DatasetID: request.DatasetID, ProfileID: request.ProfileID,
			AssistantID: request.AssistantID, RunID: request.RunID,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(reservation).Error; err != nil {
			return err
		}
		if reservation.Estimated > 0 {
			if err := tx.Model(&model.QuotaLimit{}).
				Where("key_id = ? AND period_start = ?", quotaKey, request.PeriodStart).
				Update("pending", gorm.Expr("pending + ?", reservation.Estimated)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.QuotaReservation{}).
			Where("request_id = ?", reservation.RequestID).
			Update("applied", true).Error
	})
	if err != nil {
		if reason, ok := err.(*enhancementBudgetExceededError); ok {
			return nil, reason.scope, nil
		}
		return nil, "", err
	}
	return snapshot, "", nil
}

func (s *store) ReleaseEnhancementQuota(ctx context.Context, requestID string) error {
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation model.QuotaReservation
		if err := tx.Where("request_id = ?", requestID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.Status != model.QuotaReservationPending {
			return nil
		}
		if err := tx.Model(&model.QuotaReservation{}).
			Where("request_id = ?", requestID).
			Update("status", model.QuotaReservationReleased).Error; err != nil {
			return err
		}
		if !reservation.Applied || reservation.Estimated <= 0 {
			return nil
		}
		return tx.Model(&model.QuotaLimit{}).
			Where("key_id = ? AND period_start = ?", reservation.KeyID, reservation.PeriodStart).
			Update("pending", gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END",
				reservation.Estimated, reservation.Estimated)).Error
	})
}

func (s *store) SummarizeCostAttribution(
	ctx context.Context,
	tenantID string,
	scopeAll bool,
	dateFrom,
	dateTo string,
	datasetID string,
) ([]model.KnowledgeOpsCostAttribution, error) {
	var rows []model.KnowledgeOpsCostAttribution
	query := s.WithContext(ctx).Model(&model.CostMetric{}).
		Select(`
			tenant_id,
			COALESCE(project_id, '') AS project_id,
			COALESCE(x_dataset_id, '') AS dataset_id,
			COALESCE(x_profile_id, '') AS profile_id,
			COALESCE(x_assistant_id, '') AS assistant_id,
			COALESCE(x_run_id, '') AS run_id,
			COUNT(*) AS requests,
			COALESCE(SUM(tokens_in), 0) AS tokens_in,
			COALESCE(SUM(tokens_out), 0) AS tokens_out,
			COALESCE(SUM(cost), 0) AS estimated_cost
		`).Group(`
			tenant_id, project_id, x_dataset_id, x_profile_id,
			x_assistant_id, x_run_id
		`)
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if dateFrom != "" {
		query = query.Where("date >= ?", dateFrom)
	}
	if dateTo != "" {
		query = query.Where("date <= ?", dateTo)
	}
	if datasetID != "" {
		query = query.Where("x_dataset_id = ?", datasetID)
	}
	if err := query.Order("tokens_in + tokens_out DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type enhancementUsage struct {
	Tenant  int64
	Dataset int64
	Profile int64
	Run     int64
}

func enhancementBudgetUsage(tx *gorm.DB, request EnhancementBudgetRequest) (*enhancementUsage, error) {
	periodPattern := request.PeriodStart + "%"
	keyPattern := enhancementQuotaKeyPrefix + request.TenantID + "%"
	usage := &enhancementUsage{}
	if err := tx.Model(&model.CostMetric{}).Select(`
		COALESCE(SUM(tokens_in + tokens_out), 0) AS tenant,
		COALESCE(SUM(CASE WHEN x_dataset_id = ? THEN tokens_in + tokens_out ELSE 0 END), 0) AS dataset,
		COALESCE(SUM(CASE WHEN x_profile_id = ? THEN tokens_in + tokens_out ELSE 0 END), 0) AS profile,
		COALESCE(SUM(CASE WHEN x_run_id = ? THEN tokens_in + tokens_out ELSE 0 END), 0) AS run
	`, request.DatasetID, request.ProfileID, request.RunID).
		Where("tenant_id = ? AND date LIKE ? AND (x_run_id <> '' OR x_profile_id <> '')",
			request.TenantID, periodPattern).
		Scan(usage).Error; err != nil {
		return nil, err
	}
	var reservationUsage enhancementUsage
	if err := tx.Model(&model.QuotaReservation{}).Select(`
		COALESCE(SUM(CASE WHEN key_id LIKE ? THEN estimated ELSE 0 END), 0) AS tenant,
		COALESCE(SUM(CASE WHEN x_dataset_id = ? THEN estimated ELSE 0 END), 0) AS dataset,
		COALESCE(SUM(CASE WHEN x_profile_id = ? THEN estimated ELSE 0 END), 0) AS profile,
		COALESCE(SUM(CASE WHEN x_run_id = ? THEN estimated ELSE 0 END), 0) AS run
	`, keyPattern, request.DatasetID, request.ProfileID, request.RunID).
		Where("tenant_id = ? AND status = ? AND applied = ?", request.TenantID,
			model.QuotaReservationPending, true).
		Scan(&reservationUsage).Error; err != nil {
		return nil, err
	}
	usage.Tenant += reservationUsage.Tenant
	usage.Dataset += reservationUsage.Dataset
	usage.Profile += reservationUsage.Profile
	usage.Run += reservationUsage.Run
	return usage, nil
}

type enhancementBudgetExceededError struct {
	scope string
}

func errEnhancementBudgetExceeded(scope string) error {
	return &enhancementBudgetExceededError{scope: scope}
}

func (e *enhancementBudgetExceededError) Error() string {
	return "enhancement budget exceeded: " + e.scope
}

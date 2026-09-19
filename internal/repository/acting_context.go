package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// ActingContextRepo stores server-issued execution authorization for async
// governance work. Claim is a CAS so only one worker can hold an attempt.
type ActingContextRepo interface {
	CreateActingContext(ctx context.Context, context *model.ActingContext) error
	GetActingContextByApproval(ctx context.Context, approvalID string) (*model.ActingContext, error)
	CountActingContextsByApproval(ctx context.Context, approvalID string) (int64, error)
	GetActingContext(ctx context.Context, id string) (*model.ActingContext, error)
	ClaimActingContext(ctx context.Context, id, claimedBy string, now, claimExpiresAt time.Time) (*model.ActingContext, bool, error)
	CompleteActingContext(ctx context.Context, id, from, to string) (bool, error)
}

func (s *store) CreateActingContext(ctx context.Context, context *model.ActingContext) error {
	if err := s.WithContext(ctx).Create(context).Error; err != nil {
		if isDuplicateKeyErr(err) {
			return gorm.ErrDuplicatedKey
		}
		return err
	}
	return nil
}

func (s *store) GetActingContextByApproval(ctx context.Context, approvalID string) (*model.ActingContext, error) {
	var context model.ActingContext
	err := s.WithContext(ctx).Where("approval_id = ?", approvalID).
		Order("attempt_no DESC").First(&context).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &context, nil
}

func (s *store) CountActingContextsByApproval(ctx context.Context, approvalID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.ActingContext{}).
		Where("approval_id = ?", approvalID).
		Count(&count).Error
	return count, err
}

func (s *store) GetActingContext(ctx context.Context, id string) (*model.ActingContext, error) {
	var context model.ActingContext
	err := s.WithContext(ctx).Where("id = ?", id).First(&context).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &context, nil
}

// ClaimActingContext atomically moves ACTIVE to CLAIMED. Expired CLAIMED rows
// are deliberately not reclaimed here: recovery requires the business result
// check at the service layer, not a blind authorization refresh.
func (s *store) ClaimActingContext(ctx context.Context, id, claimedBy string, now, claimExpiresAt time.Time) (*model.ActingContext, bool, error) {
	result := s.WithContext(ctx).Model(&model.ActingContext{}).
		Where("id = ? AND status = ? AND expires_at > ?", id, model.ActingContextActive, now).
		Updates(map[string]interface{}{
			"status":           model.ActingContextClaimed,
			"claimed_at":       now,
			"claimed_by":       claimedBy,
			"claim_expires_at": claimExpiresAt,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, nil
	}
	context, err := s.GetActingContext(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if context == nil {
		return nil, false, nil
	}
	return context, true, nil
}

func (s *store) CompleteActingContext(ctx context.Context, id, from, to string) (bool, error) {
	now := time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.ActingContext{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]interface{}{"status": to, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

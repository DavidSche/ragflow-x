package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SettingRevisionRepo owns append-only desired configuration history. It
// intentionally has no UpdateRevision or DeleteRevision methods.
type SettingRevisionRepo interface {
	GetSettingCurrentPointer(ctx context.Context) (*model.SettingCurrent, error)
	GetSettingRevision(ctx context.Context, id string) (*model.SettingRevision, error)
	GetCurrentSettingRevision(ctx context.Context) (*model.SettingRevision, error)
	ListSettingRevisions(ctx context.Context, page, pageSize int) ([]model.SettingRevision, int64, error)
	CreateSettingRevisionWithPointer(ctx context.Context, revision *model.SettingRevision, expectedRevisionID string) error
	CreateSettingSecretVersionWithPointer(ctx context.Context, secret *model.SettingSecretVersion, revision *model.SettingRevision, expectedRevisionID string) error
	GetLatestSettingSecretVersion(ctx context.Context, secretKey string) (*model.SettingSecretVersion, error)
	UpsertRuntimeSettingInstanceState(ctx context.Context, state *model.RuntimeSettingInstanceState) error
	ReportRuntimeSettingInstanceState(ctx context.Context, state *model.RuntimeSettingInstanceState) error
	ListRuntimeSettingInstanceStates(ctx context.Context) ([]model.RuntimeSettingInstanceState, error)
}

func (s *store) GetSettingCurrentPointer(ctx context.Context) (*model.SettingCurrent, error) {
	var current model.SettingCurrent
	err := s.WithContext(ctx).First(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &current, nil
}

func (s *store) GetSettingRevision(ctx context.Context, id string) (*model.SettingRevision, error) {
	var revision model.SettingRevision
	err := s.WithContext(ctx).Where("id = ?", id).First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func (s *store) GetCurrentSettingRevision(ctx context.Context) (*model.SettingRevision, error) {
	current, err := s.GetSettingCurrentPointer(ctx)
	if err != nil || current == nil {
		return nil, err
	}
	return s.GetSettingRevision(ctx, current.DesiredRevisionID)
}

func (s *store) ListSettingRevisions(ctx context.Context, page, pageSize int) ([]model.SettingRevision, int64, error) {
	var list []model.SettingRevision
	var total int64
	q := s.WithContext(ctx).Model(&model.SettingRevision{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("revision DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) CreateSettingRevisionWithPointer(ctx context.Context, revision *model.SettingRevision, expectedRevisionID string) error {
	return s.Transaction(func(tx *gorm.DB) error {
		var current model.SettingCurrent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current).Error
		currentExists := true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			currentExists = false
		} else if err != nil {
			return err
		}
		if currentExists && current.DesiredRevisionID != expectedRevisionID {
			return gorm.ErrForeignKeyViolated
		}
		if !currentExists && expectedRevisionID != "" {
			return gorm.ErrForeignKeyViolated
		}

		nextRevision := int64(1)
		if currentExists {
			var previous model.SettingRevision
			if err := tx.Where("id = ?", current.DesiredRevisionID).First(&previous).Error; err != nil {
				return err
			}
			nextRevision = previous.Revision + 1
		}
		revision.ID = id.New()
		revision.Revision = nextRevision
		revision.RevisionStatus = model.SettingRevisionStatusCurrent
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		now := revision.CreatedAt
		if currentExists {
			if err := tx.Model(&model.SettingCurrent{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
				"desired_revision_id": revision.ID,
				"updated_by":          revision.CreatedBy,
				"updated_at":          now,
			}).Error; err != nil {
				return err
			}
			return tx.Model(&model.SettingRevision{}).Where("id = ?", current.DesiredRevisionID).
				Update("revision_status", model.SettingRevisionStatusSuperseded).Error
		}
		return tx.Create(&model.SettingCurrent{
			ID:                "current",
			DesiredRevisionID: revision.ID,
			UpdatedBy:         revision.CreatedBy,
			CreatedAt:         now,
			UpdatedAt:         now,
		}).Error
	})
}

func (s *store) CreateSettingSecretVersionWithPointer(ctx context.Context, secret *model.SettingSecretVersion, revision *model.SettingRevision, expectedRevisionID string) error {
	return s.Transaction(func(tx *gorm.DB) error {
		var current model.SettingCurrent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("system settings are not initialized")
		}
		if err != nil {
			return err
		}
		if current.DesiredRevisionID != expectedRevisionID {
			return gorm.ErrForeignKeyViolated
		}
		var previous model.SettingRevision
		if err := tx.Where("id = ?", current.DesiredRevisionID).First(&previous).Error; err != nil {
			return err
		}
		var latest model.SettingSecretVersion
		err = tx.Where("secret_key = ?", secret.SecretKey).Order("version DESC").First(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UTC()
		secret.ID = id.New()
		secret.Version = latest.Version + 1
		secret.CreatedAt = now
		secret.UpdatedAt = now
		if err := tx.Create(secret).Error; err != nil {
			return err
		}

		var snapshot map[string]interface{}
		if err := json.Unmarshal([]byte(previous.SnapshotJSON), &snapshot); err != nil {
			return err
		}
		references, _ := snapshot["secret_refs"].(map[string]interface{})
		if references == nil {
			references = map[string]interface{}{}
		}
		references[secret.SecretKey] = map[string]interface{}{"id": secret.ID, "version": secret.Version}
		snapshot["secret_refs"] = references
		snapshotJSON, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(snapshotJSON)
		revision.ID = id.New()
		revision.Revision = previous.Revision + 1
		revision.RevisionStatus = model.SettingRevisionStatusCurrent
		revision.SnapshotJSON = string(snapshotJSON)
		revision.Checksum = hex.EncodeToString(sum[:])
		revision.CreatedAt = now
		revision.UpdatedAt = now
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.SettingCurrent{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
			"desired_revision_id": revision.ID,
			"updated_by":          revision.CreatedBy,
			"updated_at":          now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.SettingRevision{}).Where("id = ?", current.DesiredRevisionID).
			Update("revision_status", model.SettingRevisionStatusSuperseded).Error
	})
}

func (s *store) UpsertRuntimeSettingInstanceState(ctx context.Context, state *model.RuntimeSettingInstanceState) error {
	return s.WithContext(ctx).Save(state).Error
}

func (s *store) ReportRuntimeSettingInstanceState(ctx context.Context, state *model.RuntimeSettingInstanceState) error {
	return s.Transaction(func(tx *gorm.DB) error {
		var existing model.RuntimeSettingInstanceState
		err := tx.Where("runtime_instance_id = ?", state.RuntimeInstanceID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var identityOwner model.RuntimeSettingInstanceState
			if err := tx.Where("instance_identity = ?", state.InstanceIdentity).First(&identityOwner).Error; err == nil && identityOwner.RuntimeInstanceID != state.RuntimeInstanceID {
				return gorm.ErrDuplicatedKey
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return tx.Create(state).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&model.RuntimeSettingInstanceState{}).Where("runtime_instance_id = ?", existing.RuntimeInstanceID).
			Updates(map[string]interface{}{
				"instance_identity":   state.InstanceIdentity,
				"runtime_revision_id": state.RuntimeRevisionID,
				"apply_status":        state.ApplyStatus,
				"apply_error":         state.ApplyError,
				"last_seen_at":        state.LastSeenAt,
				"updated_at":          state.UpdatedAt,
			}).Error
	})
}
func (s *store) GetLatestSettingSecretVersion(ctx context.Context, secretKey string) (*model.SettingSecretVersion, error) {
	var secret model.SettingSecretVersion
	err := s.WithContext(ctx).Where("secret_key = ?", secretKey).
		Order("version DESC").First(&secret).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

func (s *store) ListRuntimeSettingInstanceStates(ctx context.Context) ([]model.RuntimeSettingInstanceState, error) {
	var list []model.RuntimeSettingInstanceState
	err := s.WithContext(ctx).Order("runtime_instance_id ASC").Find(&list).Error
	return list, err
}

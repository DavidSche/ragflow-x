package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// ModelRoutePinRepo persists immutable enterprise runtime pins and the exact
// route pointer used by execution. It never resolves "latest" versions.
type ModelRoutePinRepo interface {
	CreateModelRouteEnterprisePin(ctx context.Context, pin *model.ModelRouteEnterprisePin, route *model.ModelRoute) error
	GetModelRouteEnterprisePin(ctx context.Context, tenantID, routeID, pinID string, version int64) (*model.ModelRouteEnterprisePin, error)
	GetModelRouteEnterprisePinByIdentity(ctx context.Context, tenantID, pinID string, version int64) (*model.ModelRouteEnterprisePin, error)
	ListModelRouteEnterprisePins(ctx context.Context, tenantID, routeID string) ([]model.ModelRouteEnterprisePin, error)
}

func (s *store) CreateModelRouteEnterprisePin(ctx context.Context, pin *model.ModelRouteEnterprisePin, route *model.ModelRoute) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(pin).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ModelRoute{}).
			Where("id = ? AND tenant_id = ? AND current_pin_id = ? AND current_pin_version = ?",
				route.ID, route.TenantID, route.CurrentPinID, route.CurrentPinVersion).
			Updates(map[string]interface{}{
				"current_pin_id":      pin.PinID,
				"current_pin_version": pin.Version,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrDuplicatedKey
		}
		return nil
	})
}

func (s *store) GetModelRouteEnterprisePin(ctx context.Context, tenantID, routeID, pinID string, version int64) (*model.ModelRouteEnterprisePin, error) {
	var pin model.ModelRouteEnterprisePin
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND route_id = ? AND pin_id = ? AND version = ?",
		tenantID, routeID, pinID, version,
	).First(&pin).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pin, nil
}

func (s *store) GetModelRouteEnterprisePinByIdentity(ctx context.Context, tenantID, pinID string, version int64) (*model.ModelRouteEnterprisePin, error) {
	var pin model.ModelRouteEnterprisePin
	err := s.WithContext(ctx).Where("tenant_id = ? AND pin_id = ? AND version = ?", tenantID, pinID, version).
		First(&pin).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pin, nil
}

func (s *store) ListModelRouteEnterprisePins(ctx context.Context, tenantID, routeID string) ([]model.ModelRouteEnterprisePin, error) {
	var pins []model.ModelRouteEnterprisePin
	err := s.WithContext(ctx).Where("tenant_id = ? AND route_id = ?", tenantID, routeID).
		Order("version ASC").Find(&pins).Error
	return pins, err
}

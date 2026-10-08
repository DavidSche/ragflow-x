package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type DBConnectionFilter struct {
	Driver       string
	HealthStatus string
}

type DBConnectionRepo interface {
	CreateDBConnection(ctx context.Context, connection *model.DBConnection) error
	GetDBConnection(ctx context.Context, tenantID, id string) (*model.DBConnection, error)
	UpdateDBConnection(ctx context.Context, connection *model.DBConnection) error
	DeleteDBConnection(ctx context.Context, tenantID, id string) error
	ListDBConnections(ctx context.Context, tenantID string, filter DBConnectionFilter, page, pageSize int) ([]model.DBConnection, int64, error)
}

func (s *store) CreateDBConnection(ctx context.Context, connection *model.DBConnection) error {
	return s.WithContext(ctx).Create(connection).Error
}

func (s *store) GetDBConnection(ctx context.Context, tenantID, id string) (*model.DBConnection, error) {
	var connection model.DBConnection
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&connection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &connection, nil
}

func (s *store) UpdateDBConnection(ctx context.Context, connection *model.DBConnection) error {
	return s.WithContext(ctx).Save(connection).Error
}

func (s *store) DeleteDBConnection(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&model.DBConnection{}).Error
}

func (s *store) ListDBConnections(
	ctx context.Context, tenantID string, filter DBConnectionFilter, page, pageSize int,
) ([]model.DBConnection, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.Driver != "" {
		query += " AND driver = ?"
		args = append(args, filter.Driver)
	}
	if filter.HealthStatus != "" {
		query += " AND health_status = ?"
		args = append(args, filter.HealthStatus)
	}
	return listTenantPage(ctx, s.DB, &[]model.DBConnection{}, query, "name ASC, id ASC", page, pageSize, args...)
}

package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

// OIDCRepo persists verified enterprise IdP subject-to-user links.
type OIDCRepo interface {
	GetOidcIdentity(ctx context.Context, issuer, subject string) (*model.OidcIdentity, error)
	UpsertOidcIdentity(ctx context.Context, identity *model.OidcIdentity) error
}

func (s *store) GetOidcIdentity(ctx context.Context, issuer, subject string) (*model.OidcIdentity, error) {
	var identity model.OidcIdentity
	err := s.WithContext(ctx).Where("issuer = ? AND subject = ?", issuer, subject).First(&identity).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

func (s *store) UpsertOidcIdentity(ctx context.Context, identity *model.OidcIdentity) error {
	if identity.ID == "" {
		identity.ID = id.New()
	}
	return s.WithContext(ctx).Save(identity).Error
}

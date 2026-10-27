package service

import (
	"context"
	"regexp"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// GovernanceSchemaService 声明治理抽取规则（T1，doc/224 §2.2）。目标必须落在
// 生效字典（平台内置 或 本租户已审批）上，保证投影结果始终落回受控词汇。

const governanceSchemaPolicyVersion = "t1-schema-v1"

type GovernanceSchemaInput struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	MatcherType string `json:"matcher_type"`
	Pattern     string `json:"pattern"`
	TargetKey   string `json:"target_key"`
	QualityMin  int    `json:"quality_min"`
	BindScope   string `json:"bind_scope"`
	DatasetID   string `json:"dataset_id"`
	DocumentID  string `json:"document_id"`
	Active      *bool  `json:"active"`
}

func (s *Service) validateGovernanceSchemaInput(ctx context.Context, tenantID string, in GovernanceSchemaInput) error {
	if in.Name == "" {
		return httperr.BadRequest(40141, "name is required")
	}
	if !validGovernanceDictionaryCategory(in.Category) {
		return httperr.BadRequest(40141, "category must be level|status")
	}
	if in.MatcherType != model.SchemaMatcherRegex && in.MatcherType != model.SchemaMatcherKeyword {
		return httperr.BadRequest(40141, "matcher_type must be regex|keyword")
	}
	if in.Pattern == "" {
		return httperr.BadRequest(40141, "pattern is required")
	}
	if in.MatcherType == model.SchemaMatcherRegex {
		if _, err := regexp.Compile(in.Pattern); err != nil {
			return httperr.BadRequest(40141, "invalid regex pattern: "+err.Error())
		}
	}
	if in.QualityMin < 0 || in.QualityMin > 100 {
		return httperr.BadRequest(40141, "quality_min must be in 0..100")
	}
	if in.BindScope != model.SchemaBindDataset && in.BindScope != model.SchemaBindDocument {
		return httperr.BadRequest(40141, "bind_scope must be dataset|document")
	}
	if in.BindScope == model.SchemaBindDocument && in.DocumentID == "" {
		return httperr.BadRequest(40141, "document_id is required for document bind")
	}
	if in.DatasetID == "" {
		return httperr.BadRequest(40141, "dataset_id is required")
	}
	// 目标必须落在生效字典词条上（平台或本租户已审批），否则投影悬空。
	term, err := s.Store.GetGovernanceDictionaryEntry(ctx, tenantID, in.Category, in.TargetKey)
	if err != nil {
		return err
	}
	if term == nil || !term.Active {
		return httperr.BadRequest(40142, "target key must be an active controlled-vocabulary term")
	}
	return nil
}

func (s *Service) ListGovernanceSchemas(
	ctx context.Context, tenantID string, filter repository.GovernanceSchemaFilter,
) ([]model.GovernanceSchema, error) {
	return s.Store.ListGovernanceSchemas(ctx, tenantID, filter)
}

func (s *Service) GetGovernanceSchema(ctx context.Context, tenantID, id string) (*model.GovernanceSchema, error) {
	return s.Store.GetGovernanceSchema(ctx, tenantID, id)
}

func (s *Service) CreateGovernanceSchema(
	ctx context.Context, tenantID, userID string, in GovernanceSchemaInput,
) (*model.GovernanceSchema, error) {
	if err := s.validateGovernanceSchemaInput(ctx, tenantID, in); err != nil {
		return nil, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	now := time.Now().UTC()
	schema := &model.GovernanceSchema{
		ID: id.New(), TenantID: tenantID, Name: in.Name, Category: in.Category,
		MatcherType: in.MatcherType, Pattern: in.Pattern, TargetKey: in.TargetKey,
		QualityMin: in.QualityMin, BindScope: in.BindScope, DatasetID: in.DatasetID,
		DocumentID: in.DocumentID, Active: active, CreatedBy: userID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateGovernanceSchema(ctx, schema); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, auditForGovernanceSchema(schema, "governance_schema.created", userID, now, tenantID)); err != nil {
		return nil, err
	}
	return schema, nil
}

func (s *Service) UpdateGovernanceSchema(
	ctx context.Context, tenantID, userID, id string, in GovernanceSchemaInput,
) (*model.GovernanceSchema, error) {
	existing, err := s.Store.GetGovernanceSchema(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, httperr.NotFound("governance schema not found")
	}
	if err := s.validateGovernanceSchemaInput(ctx, tenantID, in); err != nil {
		return nil, err
	}
	active := existing.Active
	if in.Active != nil {
		active = *in.Active
	}
	now := time.Now().UTC()
	existing.Name = in.Name
	existing.Category = in.Category
	existing.MatcherType = in.MatcherType
	existing.Pattern = in.Pattern
	existing.TargetKey = in.TargetKey
	existing.QualityMin = in.QualityMin
	existing.BindScope = in.BindScope
	existing.DatasetID = in.DatasetID
	existing.DocumentID = in.DocumentID
	existing.Active = active
	existing.UpdatedAt = now
	if err := s.Store.UpdateGovernanceSchema(ctx, existing); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, auditForGovernanceSchema(existing, "governance_schema.updated", userID, now, tenantID)); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *Service) DeleteGovernanceSchema(ctx context.Context, tenantID, userID, id string) error {
	existing, err := s.Store.GetGovernanceSchema(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return httperr.NotFound("governance schema not found")
	}
	if err := s.Store.DeleteGovernanceSchema(ctx, tenantID, id); err != nil {
		return err
	}
	return s.Store.CreateAudit(ctx, auditForGovernanceSchema(existing, "governance_schema.deleted", userID, time.Now().UTC(), tenantID))
}

func auditForGovernanceSchema(
	s *model.GovernanceSchema, action, userID string, at time.Time, tenantID string,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: userID, Action: action,
		Resource: resourceGovernanceDictionary, ResourceID: s.DatasetID + "/" + s.Name,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"name": s.Name, "category": s.Category, "matcher_type": s.MatcherType,
			"pattern": s.Pattern, "target_key": s.TargetKey, "quality_min": s.QualityMin,
			"bind_scope": s.BindScope, "dataset_id": s.DatasetID, "document_id": s.DocumentID,
			"active": s.Active,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "manage:document-governance", AuthorizationPolicyVersion: governanceSchemaPolicyVersion,
	}
}

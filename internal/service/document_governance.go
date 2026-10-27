package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// DocumentGovernanceInput is the payload for SetDocumentGovernance.
type DocumentGovernanceInput struct {
	// Level is the 密级 (internal / confidential / top_secret).
	Level string `json:"level" binding:"required"`
	// Authority is the 权威度 等级分 (0-100, default 50).
	Authority *int `json:"authority"`
	// Status is the 发布状态 (draft / published, default published).
	Status string `json:"status"`
	// Source records the annotation channel (manual/approval/auto). Empty is
	// normalized to manual; approval records are stamped automatically.
	Source string `json:"source"`
	// Reason is an optional human-readable justification (audit trail).
	Reason string `json:"reason"`
	// ExpiresAt optionally disables the annotation after a time.
	ExpiresAt *time.Time `json:"expires_at"`
}

func normalizeAuthority(input *int) int {
	if input == nil {
		return model.GovernanceDefaultAuthority
	}
	return *input
}

// SetDocumentGovernance creates or updates a document-level governance
// annotation (T2, doc/224 §2.1). High-sensitivity (绝密) annotations from a
// non-governor go through the approval loop (approval_state=pending); governor
// writes land directly as approved. Un-annotated documents remain visible.
func (s *Service) SetDocumentGovernance(
	ctx context.Context, tenantID, userID, datasetID, documentID string, input DocumentGovernanceInput,
) (*model.DocumentGovernance, error) {
	if documentID == "" {
		return nil, httperr.BadRequest(40121, "document_id is required")
	}
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	if !model.ValidGovernanceLevel(input.Level) {
		return nil, httperr.BadRequest(40122, "level must be internal|confidential|top_secret")
	}
	status := input.Status
	if status == "" {
		status = model.GovernanceStatusPublished
	}
	if !model.ValidGovernanceStatus(status) {
		return nil, httperr.BadRequest(40123, "status must be draft|published")
	}
	authority := normalizeAuthority(input.Authority)
	if !model.ValidAuthoritativeRank(authority) {
		return nil, httperr.BadRequest(40124, "authority must be in 0..100")
	}
	source := input.Source
	if source == "" {
		source = model.GovernanceSourceManual
	}
	if !model.ValidGovernanceSource(source) {
		return nil, httperr.BadRequest(40125, "source must be manual|approval|auto")
	}

	governor, err := s.isDocumentGovernanceGovernor(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	existing, err := s.Store.GetDocumentGovernance(ctx, tenantID, datasetID, documentID)
	if err != nil {
		return nil, err
	}

	requiresApproval := input.Level == model.GovernanceApprovalRequiredLevel && !governor
	if requiresApproval {
		source = model.GovernanceSourceApproval // 审批通道身份
	}

	record := &model.DocumentGovernance{
		ID:         id.New(),
		TenantID:   tenantID,
		DatasetID:  datasetID,
		DocumentID: documentID,
		Level:      input.Level,
		Authority:  authority,
		Status:     status,
		Source:     source,
		Reason:     input.Reason,
		ExpiresAt:  input.ExpiresAt,
	}
	if existing != nil {
		record.ID = existing.ID
		record.CreatedAt = existing.CreatedAt
	}
	if requiresApproval {
		record.ApprovalState = model.GovernanceApprovalPending
		record.CreatedBy = userID
	} else {
		record.ApprovalState = model.GovernanceApprovalApproved
		approvedBy := userID
		record.CreatedBy = userID
		record.ApprovedBy = &approvedBy
		record.ApprovedAt = &now
	}
	record.CreatedAt = coalesceTime(record.CreatedAt, now)
	record.UpdatedAt = now

	if err := s.Store.UpsertDocumentGovernance(ctx, record); err != nil {
		return nil, err
	}
	action := "document_governance.created"
	if existing != nil {
		action = "document_governance.updated"
	}
	if err := s.Store.CreateAudit(ctx, auditForDocumentGovernance(record, action, userID, now)); err != nil {
		return nil, err
	}
	return record, nil
}

// GetDocumentGovernance returns one annotation (nil if absent).
func (s *Service) GetDocumentGovernance(
	ctx context.Context, tenantID, datasetID, documentID string,
) (*model.DocumentGovernance, error) {
	return s.Store.GetDocumentGovernance(ctx, tenantID, datasetID, documentID)
}

// ListDocumentGovernance returns paged annotations.
func (s *Service) ListDocumentGovernance(
	ctx context.Context, tenantID string, filter repository.DocumentGovernanceFilter, page, pageSize int,
) ([]model.DocumentGovernance, int64, error) {
	return s.Store.ListDocumentGovernance(ctx, tenantID, filter, page, pageSize)
}

// DeleteDocumentGovernance removes an annotation (governor only).
func (s *Service) DeleteDocumentGovernance(
	ctx context.Context, tenantID, userID, datasetID, documentID string,
) error {
	governor, err := s.isDocumentGovernanceGovernor(ctx, userID)
	if err != nil {
		return err
	}
	if !governor {
		return httperr.New(403, 40321, "document governance requires a governor role")
	}
	existing, err := s.Store.GetDocumentGovernance(ctx, tenantID, datasetID, documentID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if err := s.Store.DeleteDocumentGovernance(ctx, tenantID, datasetID, documentID); err != nil {
		return err
	}
	return s.Store.CreateAudit(ctx, auditForDocumentGovernance(existing, "document_governance.deleted", userID, time.Now().UTC()))
}

// ApproveDocumentGovernance decides a pending annotation (governor only).
func (s *Service) ApproveDocumentGovernance(
	ctx context.Context, tenantID, approverID, datasetID, documentID string, approve bool,
) (*model.DocumentGovernance, error) {
	governor, err := s.isDocumentGovernanceGovernor(ctx, approverID)
	if err != nil {
		return nil, err
	}
	if !governor {
		return nil, httperr.New(403, 40321, "document governance approval requires a governor role")
	}
	existing, err := s.Store.GetDocumentGovernance(ctx, tenantID, datasetID, documentID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, httperr.NotFound("document governance not found")
	}
	if existing.ApprovalState != model.GovernanceApprovalPending {
		return nil, httperr.New(409, 40921, "document governance is not pending approval")
	}
	now := time.Now().UTC()
	if approve {
		existing.ApprovalState = model.GovernanceApprovalApproved
		existing.ApprovedBy = &approverID
		existing.ApprovedAt = &now
		existing.UpdatedAt = now
	} else {
		existing.ApprovalState = model.GovernanceApprovalRejected
		existing.UpdatedAt = now
	}
	if err := s.Store.UpsertDocumentGovernance(ctx, existing); err != nil {
		return nil, err
	}
	action := "document_governance.approved"
	if !approve {
		action = "document_governance.rejected"
	}
	return existing, s.Store.CreateAudit(ctx, auditForDocumentGovernance(existing, action, approverID, now))
}

// isDocumentGovernanceGovernor reports whether userID can bypass/approve
// high-sensitivity annotations. MVP: tenant_admin / platform_admin. This is the
// X-side control plane gate; the RBAC middleware enforces dataset visibility
// separately.
func (s *Service) isDocumentGovernanceGovernor(ctx context.Context, userID string) (bool, error) {
	roles, err := s.Store.ListRolesByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		if role.ID == model.RolePlatformAdmin || role.ID == model.RoleTenantAdmin {
			return true, nil
		}
	}
	return false, nil
}

// GetUserClearance returns a user's 密级 clearance (nil → default internal).
func (s *Service) GetUserClearance(ctx context.Context, tenantID, userID string) (*model.UserClearance, error) {
	return s.Store.GetUserClearance(ctx, tenantID, userID)
}

// SetUserClearance sets a user's clearance (governor only — raising another
// user's clearance is a privilege change and must not be self-service).
func (s *Service) SetUserClearance(
	ctx context.Context, tenantID, userID, operatorID, level string,
) (*model.UserClearance, error) {
	if !model.ValidGovernanceLevel(level) {
		return nil, httperr.BadRequest(40122, "level must be internal|confidential|top_secret")
	}
	governor, err := s.isDocumentGovernanceGovernor(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if !governor {
		return nil, httperr.New(403, 40321, "clearance assignment requires a governor role")
	}
	now := time.Now().UTC()
	rec := &model.UserClearance{
		ID: id.New(), TenantID: tenantID, UserID: userID, Level: level,
		UpdatedBy: operatorID, UpdatedAt: now,
	}
	if err := s.Store.SetUserClearance(ctx, rec); err != nil {
		return nil, err
	}
	return rec, s.Store.CreateAudit(ctx, auditForUserClearance(rec, operatorID, now))
}

func nilString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nilTime(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.UTC().Format(time.RFC3339)
}

// ResolveClearanceLevel returns the effective clearance for a user, defaulting
// to internal (least access) when no explicit record exists.
func (s *Service) ResolveClearanceLevel(ctx context.Context, tenantID, userID string) (string, error) {
	rec, err := s.Store.GetUserClearance(ctx, tenantID, userID)
	if err != nil {
		return "", err
	}
	if rec == nil || rec.Level == "" {
		return model.GovernanceLevelInternal, nil
	}
	return rec.Level, nil
}

func coalesceTime(t time.Time, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func auditForDocumentGovernance(
	g *model.DocumentGovernance, action, userID string, at time.Time,
) *model.AuditLog {
	detail, _ := json.Marshal(map[string]interface{}{
		"dataset_id": g.DatasetID, "document_id": g.DocumentID,
		"level": g.Level, "authority": g.Authority, "status": g.Status,
		"source": g.Source, "approval_state": g.ApprovalState,
		"approved_by": nilString(g.ApprovedBy), "expires_at": nilTime(g.ExpiresAt),
	})
	return &model.AuditLog{
		ID: id.New(), TenantID: g.TenantID, ActorTenantID: g.TenantID,
		TargetTenantID: g.TenantID, UserID: userID, Action: action,
		Resource: "document-governance", ResourceID: g.DatasetID + "/" + g.DocumentID,
		DetailJSON: string(detail),
		At:         at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "execute:document-governance", AuthorizationPolicyVersion: "t2-governance-v1",
	}
}

func auditForUserClearance(c *model.UserClearance, operatorID string, at time.Time) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: c.TenantID, ActorTenantID: c.TenantID,
		TargetTenantID: c.TenantID, UserID: c.UserID, Action: "user_clearance.updated",
		Resource: "user-clearance", ResourceID: c.UserID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"user_id": c.UserID, "level": c.Level, "updated_by": c.UpdatedBy,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "execute:user-clearance", AuthorizationPolicyVersion: "t2-governance-v1",
	}
}

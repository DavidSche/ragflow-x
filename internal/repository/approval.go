package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ApprovalFilter struct {
	TenantID    string
	Status      string
	ObjectType  string
	Action      string
	RequesterID string
	Approver    *ApprovalApproverFilter
	PolicyID    string
	Search      string
	From        time.Time
	To          time.Time
}

type ApprovalApproverFilter struct {
	UserID      string
	RoleIDs     []string
	TeamIDs     []string
	DelegatedBy []string
}

type ApprovalListQuery struct {
	Page     int
	PageSize int
	SortBy   string
	Order    string
}

const expiryBatchSize = 500

type ApprovalRepo interface {
	CreateApprovalWithAudit(ctx context.Context, approval *model.Approval, steps []model.ApprovalStep, credential *model.ApprovalCredential, audit *model.AuditLog) error
	GetApproval(ctx context.Context, tenantID, approvalID string) (*model.Approval, error)
	GetApprovalByID(ctx context.Context, approvalID string) (*model.Approval, error)
	GetApprovalByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.Approval, error)
	ListApprovalSteps(ctx context.Context, tenantID, approvalID string) ([]model.ApprovalStep, error)
	ListApprovals(ctx context.Context, tenantID string, filter ApprovalFilter, query ApprovalListQuery) ([]model.Approval, int64, error)
	ListAllApprovals(ctx context.Context, filter ApprovalFilter, query ApprovalListQuery) ([]model.Approval, int64, error)
	ListApprovalExportIDs(ctx context.Context, allTenants bool, tenantID string, filter ApprovalFilter, query ApprovalListQuery) ([]string, int64, error)
	ListApprovalsByIDs(ctx context.Context, allTenants bool, tenantID string, ids []string) ([]model.Approval, error)
	TransitionApproval(ctx context.Context, tenantID, approvalID, from, to string, updates map[string]interface{}) (bool, error)
	DecideApprovalStep(ctx context.Context, tenantID, approvalID, actorID, delegatedBy, decision, comment string, now time.Time, audit *model.AuditLog) (*model.Approval, bool, error)
	CurrentApprovalStep(ctx context.Context, tenantID, approvalID string) (*model.ApprovalStep, error)
	ListApprovalPolicies(ctx context.Context, tenantID string) ([]model.ApprovalPolicy, error)
	ListAllApprovalPolicies(ctx context.Context) ([]model.ApprovalPolicy, error)
	GetApprovalPolicyByID(ctx context.Context, policyID string) (*model.ApprovalPolicy, error)
	UpsertApprovalPolicy(ctx context.Context, policy *model.ApprovalPolicy) error
	CountPendingByPolicy(ctx context.Context, tenantID, policyID string) (int64, error)
	CountApprovalsByStatus(ctx context.Context, status string) (int64, error)
	ExpireApprovals(ctx context.Context, now time.Time) (int64, error)
	ListApprovedApprovals(ctx context.Context, limit int, before time.Time) ([]model.Approval, error)
	GetConsumableApprovalCredential(ctx context.Context, tenantID, approvalID string) (*model.ApprovalCredential, error)
	GetRetainedApprovalCredential(ctx context.Context, tenantID, approvalID string) (*model.ApprovalCredential, error)
	ReplaceApprovalResult(ctx context.Context, tenantID, approvalID, resultJSON string) error
	SettleApprovalExecution(ctx context.Context, tenantID, approvalID string, success bool, resultJSON, lastError string, credentialID string, retainUntil time.Time, audit *model.AuditLog) error
	DeleteExpiredApprovalCredentials(ctx context.Context, now time.Time, limit int) (int64, error)
	DeleteExpiredApprovalRecords(ctx context.Context, now time.Time, retentionDays int, limit int) (int64, error)
	CreateApprovalDelegation(ctx context.Context, delegation *model.ApprovalDelegation) error
	DeleteApprovalDelegation(ctx context.Context, tenantID, delegationID, principalID string) (bool, error)
	ListApprovalDelegations(ctx context.Context, tenantID, userID string) ([]model.ApprovalDelegation, error)
	ListActiveApprovalDelegations(ctx context.Context, tenantID, delegateID, objectType, action string, now time.Time) ([]model.ApprovalDelegation, error)
	ListApprovalStepActors(ctx context.Context, tenantID, approvalID, stepID string) ([]model.ApprovalStepActor, error)
}

func appendAuditTx(tx *gorm.DB, entry *model.AuditLog) error {
	if entry == nil {
		return nil
	}
	if entry.ID == "" {
		entry.ID = id.New()
	}
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	last, err := lastAuditTx(tx, entry.TenantID)
	if err != nil {
		return err
	}
	if last != nil {
		entry.Seq = last.Seq + 1
		entry.PrevHash = last.Hash
	} else {
		entry.Seq = 1
		entry.PrevHash = ""
	}
	entry.Hash = model.AuditHash(entry.PrevHash, entry)
	return tx.Create(entry).Error
}

func approvalSortColumn(sortBy string) string {
	switch sortBy {
	case "created_at", "expires_at", "decided_at", "updated_at":
		return sortBy
	default:
		return "created_at"
	}
}

func applyApprovalApproverFilter(db *gorm.DB, approver ApprovalApproverFilter) *gorm.DB {
	if approver.UserID == "" {
		return db
	}
	return db.Where(`EXISTS (
		SELECT 1 FROM rgx_approval_step step
		WHERE step.approval_id = rgx_approval.id
		  AND step.status = ?
		  AND (
			(step.approver_type = ? AND step.approver_value = ?)
			OR (step.approver_type = ? AND step.approver_value IN ?)
			OR (step.approver_type = ? AND step.approver_value IN ?)
			OR (step.approver_type = ? AND step.approver_value IN ?)
		  )
	)`, model.ApprovalStepCurrent, model.ApprovalApproverUser, approver.UserID,
		model.ApprovalApproverUser, approver.DelegatedBy,
		model.ApprovalApproverRole, approver.RoleIDs, model.ApprovalApproverTeam, approver.TeamIDs)
}

func (s *store) CreateApprovalWithAudit(ctx context.Context, approval *model.Approval, steps []model.ApprovalStep, credential *model.ApprovalCredential, audit *model.AuditLog) error {
	unlock := s.lockTenant(approval.TenantID)
	defer unlock()
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if credential != nil {
			if credential.ID == "" {
				credential.ID = id.New()
			}
			if err := tx.Create(credential).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(approval).Error; err != nil {
			return err
		}
		if len(steps) > 0 {
			for i := range steps {
				steps[i].ApprovalID = approval.ID
			}
			if err := tx.Create(&steps).Error; err != nil {
				return err
			}
		}
		return appendAuditTx(tx, audit)
	})
}

func (s *store) GetApproval(ctx context.Context, tenantID, approvalID string) (*model.Approval, error) {
	var approval model.Approval
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, approvalID).First(&approval).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &approval, err
}

func (s *store) GetApprovalByID(ctx context.Context, approvalID string) (*model.Approval, error) {
	var approval model.Approval
	err := s.WithContext(ctx).Where("id = ?", approvalID).First(&approval).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &approval, err
}

func (s *store) GetApprovalByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.Approval, error) {
	var approval model.Approval
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).
		First(&approval).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &approval, err
}

func (s *store) ListApprovalSteps(ctx context.Context, tenantID, approvalID string) ([]model.ApprovalStep, error) {
	var steps []model.ApprovalStep
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ?", tenantID, approvalID).
		Order("step_no ASC").
		Find(&steps).Error
	return steps, err
}

func (s *store) ListApprovals(ctx context.Context, tenantID string, filter ApprovalFilter, query ApprovalListQuery) ([]model.Approval, int64, error) {
	db := s.WithContext(ctx).Model(&model.Approval{}).Where("tenant_id = ?", tenantID)
	db = applyApprovalListFilter(db, filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := " DESC"
	if query.Order == "asc" {
		order = " ASC"
	}
	offset, limit := paginate(query.Page, query.PageSize)
	var approvals []model.Approval
	err := db.Order(approvalSortColumn(query.SortBy) + order).Offset(offset).Limit(limit).Find(&approvals).Error
	return approvals, total, err
}

func (s *store) ListAllApprovals(ctx context.Context, filter ApprovalFilter, query ApprovalListQuery) ([]model.Approval, int64, error) {
	var approvals []model.Approval
	var total int64
	db := s.WithContext(ctx).Model(&model.Approval{})
	if filter.TenantID != "" {
		db = db.Where("tenant_id = ?", filter.TenantID)
	}
	db = applyApprovalListFilter(db, filter)
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := " DESC"
	if query.Order == "asc" {
		order = " ASC"
	}
	offset, limit := paginate(query.Page, query.PageSize)
	err := db.Order(approvalSortColumn(query.SortBy) + order).Offset(offset).Limit(limit).Find(&approvals).Error
	return approvals, total, err
}

func applyApprovalListFilter(db *gorm.DB, filter ApprovalFilter) *gorm.DB {
	if filter.Status != "" {
		db = db.Where("status = ?", filter.Status)
	}
	if filter.ObjectType != "" {
		db = db.Where("object_type = ?", filter.ObjectType)
	}
	if filter.Action != "" {
		db = db.Where("action = ?", filter.Action)
	}
	if filter.RequesterID != "" {
		db = db.Where("requester_id = ?", filter.RequesterID)
	}
	if filter.Approver != nil {
		db = applyApprovalApproverFilter(db, *filter.Approver)
	}
	if filter.PolicyID != "" {
		db = db.Where("policy_id = ?", filter.PolicyID)
	}
	if !filter.From.IsZero() {
		db = db.Where("created_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		db = db.Where("created_at < ?", filter.To)
	}
	if filter.Search != "" {
		pattern := likePattern(filter.Search)
		db = db.Where("(title LIKE ? ESCAPE '\\' OR reason LIKE ? ESCAPE '\\' OR request_no LIKE ? ESCAPE '\\')", pattern, pattern, pattern)
	}
	return db
}

func (s *store) ListApprovalExportIDs(
	ctx context.Context, allTenants bool, tenantID string, filter ApprovalFilter, query ApprovalListQuery,
) ([]string, int64, error) {
	db := s.WithContext(ctx).Model(&model.Approval{})
	if !allTenants {
		db = db.Where("tenant_id = ?", tenantID)
	}
	db = applyApprovalListFilter(db, filter)
	order := " DESC"
	if query.Order == "asc" {
		order = " ASC"
	}
	if query.PageSize > 0 {
		db = db.Limit(query.PageSize)
	}
	var ids []string
	err := db.Order(approvalSortColumn(query.SortBy)+order).Pluck("id", &ids).Error
	if err != nil {
		return nil, 0, err
	}
	return ids, int64(len(ids)), nil
}

func (s *store) ListApprovalsByIDs(
	ctx context.Context, allTenants bool, tenantID string, ids []string,
) ([]model.Approval, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	db := s.WithContext(ctx).Model(&model.Approval{}).Where("id IN ?", ids)
	if !allTenants {
		db = db.Where("tenant_id = ?", tenantID)
	}
	var approvals []model.Approval
	err := db.Find(&approvals).Error
	return approvals, err
}

func (s *store) TransitionApproval(ctx context.Context, tenantID, approvalID, from, to string, updates map[string]interface{}) (bool, error) {
	changes := map[string]interface{}{"status": to, "updated_at": time.Now().UTC()}
	for key, value := range updates {
		changes[key] = value
	}
	result := s.WithContext(ctx).Model(&model.Approval{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, approvalID, from).
		Updates(changes)
	return result.RowsAffected == 1, result.Error
}

func (s *store) DecideApprovalStep(ctx context.Context, tenantID, approvalID, actorID, delegatedBy, decision, comment string, now time.Time, audit *model.AuditLog) (*model.Approval, bool, error) {
	unlock := s.lockTenant(tenantID)
	defer unlock()
	var approval model.Approval
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, approvalID, model.ApprovalStatusPendingApproval).
			First(&approval).Error; err != nil {
			return err
		}
		var step model.ApprovalStep
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND approval_id = ? AND step_no = ? AND status = ?", tenantID, approvalID, approval.CurrentStep, model.ApprovalStepCurrent).
			First(&step).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND approval_id = ? AND step_id = ? AND actor_id = ?", tenantID, approvalID, step.ID, actorID).
			First(&model.ApprovalStepActor{}).Error; err == nil {
			return gorm.ErrDuplicatedKey
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		actor := model.ApprovalStepActor{
			ID: id.New(), TenantID: tenantID, ApprovalID: approvalID, StepID: step.ID,
			ActorID: actorID, DelegatedBy: delegatedBy, Decision: decision, Comment: comment,
			ActedAt: now, CreatedAt: now,
		}
		if err := tx.Create(&actor).Error; err != nil {
			return err
		}
		approvalStatus := model.ApprovalStatusPendingApproval
		stepCompleted := false
		if decision == model.ApprovalDecisionReject {
			step.Status = model.ApprovalStepRejected
			stepCompleted = true
		} else {
			var approved int64
			if err := tx.Model(&model.ApprovalStepActor{}).
				Where("tenant_id = ? AND approval_id = ? AND step_id = ? AND decision = ?", tenantID, approvalID, step.ID, model.ApprovalDecisionApprove).
				Count(&approved).Error; err != nil {
				return err
			}
			if approved >= int64(model.ApprovalStepRequiredApprovals(&step)) {
				step.Status = model.ApprovalStepApproved
				stepCompleted = true
			}
		}
		if stepCompleted {
			var next model.ApprovalStep
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("tenant_id = ? AND approval_id = ? AND step_no = ?", tenantID, approvalID, step.StepNo+1).
				First(&next).Error; err == nil {
				if err := tx.Model(&next).Updates(map[string]interface{}{
					"status": model.ApprovalStepCurrent, "updated_at": now,
				}).Error; err != nil {
					return err
				}
			} else if err == gorm.ErrRecordNotFound {
				approvalStatus = model.ApprovalStatusApproved
			} else {
				return err
			}
		} else if decision == model.ApprovalDecisionApprove {
			step.Status = model.ApprovalStepCurrent
		}
		step.Decision = decision
		step.Comment = comment
		step.ActedBy = &actorID
		step.ActedAt = &now
		step.UpdatedAt = now
		if err := tx.Model(&step).Updates(map[string]interface{}{
			"status": step.Status, "decision": step.Decision, "comment": step.Comment,
			"acted_by": actorID, "acted_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"updated_at": now}
		if decision == model.ApprovalDecisionReject {
			updates["status"] = model.ApprovalStatusRejected
			updates["decided_at"] = now
		} else if stepCompleted && approvalStatus == model.ApprovalStatusApproved {
			updates["status"] = model.ApprovalStatusApproved
			updates["decided_at"] = now
		} else if stepCompleted {
			updates["status"] = model.ApprovalStatusPendingApproval
			updates["current_step"] = step.StepNo + 1
		}
		result := tx.Model(&model.Approval{}).
			Where("tenant_id = ? AND id = ? AND status = ? AND current_step = ?", tenantID, approvalID, model.ApprovalStatusPendingApproval, approval.CurrentStep).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.First(&approval, "tenant_id = ? AND id = ?", tenantID, approvalID).Error; err != nil {
			return err
		}
		return appendAuditTx(tx, audit)
	})
	if err != nil {
		return nil, false, err
	}
	return &approval, approval.Status == model.ApprovalStatusApproved, nil
}

func (s *store) CurrentApprovalStep(ctx context.Context, tenantID, approvalID string) (*model.ApprovalStep, error) {
	var approval model.Approval
	if err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, approvalID).First(&approval).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	var step model.ApprovalStep
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ? AND step_no = ?", tenantID, approvalID, approval.CurrentStep).
		First(&step).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &step, err
}

func (s *store) ListApprovalPolicies(ctx context.Context, tenantID string) ([]model.ApprovalPolicy, error) {
	var policies []model.ApprovalPolicy
	err := s.WithContext(ctx).
		Where("tenant_id = '' OR tenant_id = ?", tenantID).
		Order("priority ASC, tenant_id DESC, version DESC").
		Find(&policies).Error
	return policies, err
}

func (s *store) ListAllApprovalPolicies(ctx context.Context) ([]model.ApprovalPolicy, error) {
	var policies []model.ApprovalPolicy
	err := s.WithContext(ctx).Order("tenant_id ASC, priority ASC, version DESC").Find(&policies).Error
	return policies, err
}

func (s *store) GetApprovalPolicyByID(ctx context.Context, policyID string) (*model.ApprovalPolicy, error) {
	var policy model.ApprovalPolicy
	err := s.WithContext(ctx).Where("id = ?", policyID).First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *store) UpsertApprovalPolicy(ctx context.Context, policy *model.ApprovalPolicy) error {
	return s.WithContext(ctx).Save(policy).Error
}

func (s *store) CreateApprovalDelegation(ctx context.Context, delegation *model.ApprovalDelegation) error {
	if delegation.ID == "" {
		delegation.ID = id.New()
	}
	return s.WithContext(ctx).Create(delegation).Error
}

func (s *store) DeleteApprovalDelegation(ctx context.Context, tenantID, delegationID, principalID string) (bool, error) {
	result := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ? AND principal_id = ?", tenantID, delegationID, principalID).
		Delete(&model.ApprovalDelegation{})
	return result.RowsAffected == 1, result.Error
}

func (s *store) ListApprovalDelegations(ctx context.Context, tenantID, userID string) ([]model.ApprovalDelegation, error) {
	var delegations []model.ApprovalDelegation
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND (principal_id = ? OR delegate_id = ?)", tenantID, userID, userID).
		Order("starts_at DESC").
		Find(&delegations).Error
	return delegations, err
}

func (s *store) ListActiveApprovalDelegations(ctx context.Context, tenantID, delegateID, objectType, action string, now time.Time) ([]model.ApprovalDelegation, error) {
	var delegations []model.ApprovalDelegation
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND delegate_id = ? AND starts_at <= ? AND ends_at > ?", tenantID, delegateID, now, now).
		Find(&delegations).Error
	if err != nil {
		return nil, err
	}
	filtered := make([]model.ApprovalDelegation, 0, len(delegations))
	for _, delegation := range delegations {
		if delegation.ObjectType != "" && delegation.ObjectType != objectType {
			continue
		}
		if delegation.Action != "" && delegation.Action != action {
			continue
		}
		filtered = append(filtered, delegation)
	}
	return filtered, nil
}

func (s *store) ListApprovalStepActors(ctx context.Context, tenantID, approvalID, stepID string) ([]model.ApprovalStepActor, error) {
	var actors []model.ApprovalStepActor
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ? AND step_id = ?", tenantID, approvalID, stepID).
		Order("acted_at ASC").
		Find(&actors).Error
	return actors, err
}

func (s *store) CountPendingByPolicy(ctx context.Context, tenantID, policyID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.Approval{}).
		Where("tenant_id = ? AND policy_id = ? AND status = ?", tenantID, policyID, model.ApprovalStatusPendingApproval).
		Count(&count).Error
	return count, err
}

func (s *store) ExpireApprovals(ctx context.Context, now time.Time) (int64, error) {
	var expired int64
	for {
		var approvals []model.Approval
		if err := s.WithContext(ctx).
			Where("status = ? AND expires_at < ?", model.ApprovalStatusPendingApproval, now).
			Order("expires_at ASC").
			Limit(expiryBatchSize).
			Find(&approvals).Error; err != nil {
			return expired, err
		}
		if len(approvals) == 0 {
			break
		}
		for _, approval := range approvals {
			unlock := s.lockTenant(approval.TenantID)
			err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				result := tx.Model(&model.Approval{}).
					Where("tenant_id = ? AND id = ? AND status = ?", approval.TenantID, approval.ID, model.ApprovalStatusPendingApproval).
					Updates(map[string]interface{}{
						"status": model.ApprovalStatusExpired, "decided_at": now, "updated_at": now,
					})
				if result.Error != nil || result.RowsAffected != 1 {
					return result.Error
				}
				detail, _ := json.Marshal(map[string]interface{}{"approval_id": approval.ID})
				targetTenantID := approval.TargetTenantID
				if targetTenantID == "" {
					targetTenantID = approval.TenantID
				}
				return appendAuditTx(tx, &model.AuditLog{
					TenantID: approval.TenantID, UserID: "system", Action: "approval.expired", Resource: "approval",
					ResourceID: approval.ID, DetailJSON: string(detail), At: now,
					ActorTenantID: approval.TenantID, TargetTenantID: targetTenantID,
					ApprovalID: approval.ID, ApprovalActionHash: approval.ApprovalActionHash,
					Result: "EXPIRED", AuthorizationDecision: "ALLOW",
					AuthorizationPermission:    "system:approval-maintenance",
					AuthorizationPolicyVersion: "explicit-rbac-v1",
				})
			})
			unlock()
			if err != nil {
				return expired, err
			}
			expired++
		}
		if len(approvals) < expiryBatchSize {
			break
		}
	}
	return expired, nil
}

func (s *store) CountApprovalsByStatus(ctx context.Context, status string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.Approval{}).
		Where("status = ?", status).
		Count(&n).Error
	return n, err
}

func (s *store) ListApprovedApprovals(ctx context.Context, limit int, before time.Time) ([]model.Approval, error) {
	var approvals []model.Approval
	query := s.WithContext(ctx).Where("status = ?", model.ApprovalStatusApproved)
	if !before.IsZero() {
		query = query.Where("decided_at < ?", before)
	}
	err := query.
		Order("decided_at ASC").Limit(limit).Find(&approvals).Error
	return approvals, err
}

func (s *store) GetConsumableApprovalCredential(ctx context.Context, tenantID, approvalID string) (*model.ApprovalCredential, error) {
	var credential model.ApprovalCredential
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ? AND consumed_at IS NULL", tenantID, approvalID).
		First(&credential).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &credential, err
}

func (s *store) GetRetainedApprovalCredential(ctx context.Context, tenantID, approvalID string) (*model.ApprovalCredential, error) {
	var credential model.ApprovalCredential
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ?", tenantID, approvalID).
		Order("created_at DESC").
		First(&credential).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if credential.ConsumedAt != nil && (credential.RetainUntilAt == nil || credential.RetainUntilAt.Before(time.Now().UTC())) {
		return nil, nil
	}
	return &credential, nil
}

func (s *store) ReplaceApprovalResult(ctx context.Context, tenantID, approvalID, resultJSON string) error {
	result := s.WithContext(ctx).Model(&model.Approval{}).
		Where("tenant_id = ? AND id = ?", tenantID, approvalID).
		Updates(map[string]interface{}{"result_json": resultJSON, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *store) SettleApprovalExecution(ctx context.Context, tenantID, approvalID string, success bool, resultJSON, lastError string, credentialID string, retainUntil time.Time, audit *model.AuditLog) error {
	now := time.Now().UTC()
	status := model.ApprovalStatusExecutionFailed
	if success {
		status = model.ApprovalStatusCompleted
	}
	unlock := s.lockTenant(tenantID)
	defer unlock()
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status": status, "result_json": resultJSON, "updated_at": now,
		}
		if success {
			updates["executed_at"] = now
			updates["last_error"] = ""
		} else {
			updates["last_error"] = lastError
		}
		result := tx.Model(&model.Approval{}).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, approvalID, model.ApprovalStatusExecuting).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if success {
			if err := tx.Where("tenant_id = ? AND approval_id = ?", tenantID, approvalID).Delete(&model.ApprovalCredential{}).Error; err != nil {
				return err
			}
		} else if credentialID != "" {
			if err := tx.Model(&model.ApprovalCredential{}).
				Where("tenant_id = ? AND id = ?", tenantID, credentialID).
				Updates(map[string]interface{}{
					"consumed_at": now, "retain_until_at": retainUntil, "updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		return appendAuditTx(tx, audit)
	})
}

func (s *store) DeleteExpiredApprovalCredentials(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	result := s.WithContext(ctx).
		Where("consumed_at IS NOT NULL AND retain_until_at < ?", now).
		Limit(limit).
		Delete(&model.ApprovalCredential{})
	return result.RowsAffected, result.Error
}

func (s *store) DeleteExpiredApprovalRecords(ctx context.Context, now time.Time, retentionDays int, limit int) (int64, error) {
	if retentionDays <= 0 || limit <= 0 {
		return 0, nil
	}
	if limit > 1000 {
		limit = 1000
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	var approvals []model.Approval
	err := s.WithContext(ctx).
		Where("status IN ? AND updated_at < ?", []string{
			model.ApprovalStatusRejected, model.ApprovalStatusCanceled, model.ApprovalStatusExpired,
			model.ApprovalStatusCompleted, model.ApprovalStatusExecutionFailed,
		}, cutoff).
		Limit(limit).
		Find(&approvals).Error
	if err != nil {
		return 0, err
	}
	var deleted int64
	for _, approval := range approvals {
		unlock := s.lockTenant(approval.TenantID)
		err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id = ? AND approval_id = ?", approval.TenantID, approval.ID).Delete(&model.ApprovalCredential{}).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id = ? AND approval_id = ?", approval.TenantID, approval.ID).Delete(&model.ApprovalStep{}).Error; err != nil {
				return err
			}
			result := tx.Where("tenant_id = ? AND id = ?", approval.TenantID, approval.ID).Delete(&model.Approval{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			return appendAuditTx(tx, &model.AuditLog{
				TenantID: approval.TenantID, UserID: "system", Action: "approval.purged",
				Resource: "approval", ResourceID: approval.ID,
				DetailJSON: fmt.Sprintf(`{"retention_days":%d}`, retentionDays),
			})
		})
		unlock()
		if err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

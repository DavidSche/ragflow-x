package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// GovernanceDictionaryService 实现受控治理字典（T1，doc/224 §2.2 / 223 §3 T1）。
// 平台内置三档 seed 只读；租户扩展条目一律先进入 pending，governor 审批后才
// Active（198 记忆策略治理模式的轻量内联版，与 T2 治理记录一致）。

const (
	governanceDictionaryPolicyVersion = "t1-dictionary-v1"
	resourceGovernanceDictionary      = "document-governance"
)

// GovernanceDictionaryCategory 合法字典轴。
func validGovernanceDictionaryCategory(category string) bool {
	return category == model.DictCategoryLevel || category == model.DictCategoryStatus
}

type GovernanceDictionaryEntryInput struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	Rank     int    `json:"rank"`
}

// ListGovernanceDictionary returns the effective vocabulary (platform builtins
// merged with this tenant's approved extensions), grouped by category.
func (s *Service) ListGovernanceDictionary(ctx context.Context, tenantID string) (map[string][]model.GovernanceDictionaryEntry, error) {
	entries, err := s.Store.ListGovernanceDictionary(ctx, tenantID, "")
	if err != nil {
		return nil, err
	}
	out := map[string][]model.GovernanceDictionaryEntry{
		model.DictCategoryLevel:  {},
		model.DictCategoryStatus: {},
	}
	for _, e := range entries {
		if !e.Active {
			continue
		}
		if _, ok := out[e.Category]; !ok {
			out[e.Category] = []model.GovernanceDictionaryEntry{}
		}
		out[e.Category] = append(out[e.Category], e)
	}
	return out, nil
}

// CreateGovernanceDictionaryEntry registers a tenant extension. It lands in
// approval pending and only becomes active after a governor approves it.
// Tenants can never shadow a platform builtin term.
func (s *Service) CreateGovernanceDictionaryEntry(
	ctx context.Context, tenantID, userID string, input GovernanceDictionaryEntryInput,
) (*model.GovernanceDictionaryEntry, error) {
	if !validGovernanceDictionaryCategory(input.Category) {
		return nil, httperr.BadRequest(40131, "category must be level|status")
	}
	if input.Key == "" || input.Label == "" {
		return nil, httperr.BadRequest(40131, "key and label are required")
	}
	existing, err := s.Store.GetGovernanceDictionaryEntry(ctx, tenantID, input.Category, input.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, httperr.BadRequest(40132, "dictionary term already exists for this category")
	}
	now := time.Now().UTC()
	entry := &model.GovernanceDictionaryEntry{
		ID:            id.New(),
		TenantID:      tenantID,
		Category:      input.Category,
		Key:           input.Key,
		Label:         input.Label,
		Rank:          input.Rank,
		Origin:        model.DictOriginTenant,
		ApprovalState: model.DictApprovalPending,
		Active:        false,
		CreatedBy:     userID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.Store.UpsertGovernanceDictionaryEntry(ctx, entry); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, auditForGovernanceDictionary(entry, "governance_dictionary.created", userID, now, tenantID, true)); err != nil {
		return nil, err
	}
	return entry, nil
}

// DecideGovernanceDictionaryEntry approves or rejects a pending tenant
// extension (governor only — enforced by RBAC manage on caller path).
func (s *Service) DecideGovernanceDictionaryEntry(
	ctx context.Context, tenantID, actorID, id string, approve bool,
) (*model.GovernanceDictionaryEntry, error) {
	entry, err := s.Store.GetGovernanceDictionaryEntryByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Origin != model.DictOriginTenant {
		return nil, httperr.NotFound("dictionary term not found")
	}
	if entry.ApprovalState != model.DictApprovalPending {
		return nil, httperr.BadRequest(40133, "only pending terms can be decided")
	}
	state := model.DictApprovalRejected
	if approve {
		state = model.DictApprovalApproved
	}
	now := time.Now().UTC()
	var approvedBy string
	var approvedAt *time.Time
	if approve {
		approvedBy = actorID
		approvedAt = &now
	}
	if err := s.Store.SetGovernanceDictionaryApproval(ctx, entry.ID, state, approvedBy, approvedAt); err != nil {
		return nil, err
	}
	entry.ApprovalState = state
	entry.Active = approve
	if err := s.Store.CreateAudit(ctx, auditForGovernanceDictionary(entry, "governance_dictionary.decided", actorID, now, tenantID, approve)); err != nil {
		return nil, err
	}
	return entry, nil
}

// DeleteGovernanceDictionaryEntry removes a tenant extension (platform terms
// are immutable). If it was active, downstream schemas stop matching it.
func (s *Service) DeleteGovernanceDictionaryEntry(
	ctx context.Context, tenantID, actorID, id string,
) error {
	entry, err := s.Store.GetGovernanceDictionaryEntryByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if entry == nil || entry.Origin != model.DictOriginTenant {
		return httperr.NotFound("dictionary term not found")
	}
	if err := s.Store.DeleteGovernanceDictionaryEntry(ctx, tenantID, entry.ID); err != nil {
		return err
	}
	return s.Store.CreateAudit(ctx, auditForGovernanceDictionary(entry, "governance_dictionary.deleted", actorID, time.Now().UTC(), tenantID, false))
}

func auditForGovernanceDictionary(
	e *model.GovernanceDictionaryEntry, action, userID string, at time.Time, tenantID string, allow bool,
) *model.AuditLog {
	decision := "DENY"
	if allow {
		decision = "ALLOW"
	}
	approvedBy := ""
	if e.ApprovedBy != nil {
		approvedBy = *e.ApprovedBy
	}
	return &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: userID, Action: action,
		Resource: resourceGovernanceDictionary, ResourceID: e.Category + "/" + e.Key,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"category": e.Category, "key": e.Key, "label": e.Label, "rank": e.Rank,
			"origin": e.Origin, "approval_state": e.ApprovalState,
			"active": e.Active, "approved_by": approvedBy,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: decision,
		AuthorizationPermission: "manage:document-governance", AuthorizationPolicyVersion: governanceDictionaryPolicyVersion,
	}
}

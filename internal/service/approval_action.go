package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

type ApprovalListRequest struct {
	TenantID    string
	Status      string
	ObjectType  string
	Action      string
	PolicyID    string
	RequesterID string
	ApproverID  string
	Search      string
	From        time.Time
	To          time.Time
	Page        int
	PageSize    int
	SortBy      string
	Order       string
}

func (s *Service) buildApprovalApproverFilter(ctx context.Context, tenantID, actorID string) (*repository.ApprovalApproverFilter, error) {
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return nil, err
	}
	roleIDs := make([]string, 0, len(roles))
	for _, role := range roles {
		if role.ID != "" {
			roleIDs = append(roleIDs, role.ID)
		}
	}
	teams, err := s.Store.ListUserTeams(ctx, actorID)
	if err != nil {
		return nil, err
	}
	teamIDs := make([]string, 0, len(teams))
	for _, team := range teams {
		if team.ID != "" {
			teamIDs = append(teamIDs, team.ID)
		}
	}
	now := time.Now().UTC()
	delegations, err := s.Store.ListActiveApprovalDelegations(ctx, tenantID, actorID, "", "", now)
	if err != nil {
		return nil, err
	}
	delegatedBy := make([]string, 0, len(delegations))
	for _, delegation := range delegations {
		if delegation.PrincipalID != "" && delegation.PrincipalID != actorID {
			delegatedBy = append(delegatedBy, delegation.PrincipalID)
		}
	}
	return &repository.ApprovalApproverFilter{
		UserID: actorID, RoleIDs: roleIDs, TeamIDs: teamIDs, DelegatedBy: delegatedBy,
	}, nil
}

func (s *Service) approvalScope(ctx context.Context, actorID, actorTenantID string) (bool, bool, error) {
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return false, false, err
	}
	manage := ac.platform
	if !manage {
		manage = ac.evaluate("manage", "approval") == nil
	}
	return ac.platform, manage, nil
}

func (s *Service) ListApprovals(ctx context.Context, actorID, actorTenantID string, req ApprovalListRequest) ([]model.Approval, int64, error) {
	platform, filter, err := s.approvalListFilter(ctx, actorID, actorTenantID, req)
	if err != nil {
		return nil, 0, err
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		req.PageSize = 20
	}
	query := repository.ApprovalListQuery{
		Page: req.Page, PageSize: req.PageSize, SortBy: req.SortBy, Order: req.Order,
	}
	if platform {
		filter.TenantID = req.TenantID
		return s.Store.ListAllApprovals(ctx, filter, query)
	}
	return s.Store.ListApprovals(ctx, actorTenantID, filter, query)
}

func (s *Service) approvalListFilter(
	ctx context.Context, actorID, actorTenantID string, req ApprovalListRequest,
) (bool, repository.ApprovalFilter, error) {
	if err := s.Authorize(ctx, actorID, "read", "approval"); err != nil {
		return false, repository.ApprovalFilter{}, err
	}
	platform, manage, err := s.approvalScope(ctx, actorID, actorTenantID)
	if err != nil {
		return false, repository.ApprovalFilter{}, err
	}
	var approverFilter *repository.ApprovalApproverFilter
	if req.ApproverID != "" {
		approverFilter, err = s.buildApprovalApproverFilter(ctx, actorTenantID, req.ApproverID)
		if err != nil {
			return false, repository.ApprovalFilter{}, err
		}
	}
	filter := repository.ApprovalFilter{
		Status: req.Status, ObjectType: req.ObjectType, Action: req.Action,
		PolicyID: req.PolicyID, RequesterID: req.RequesterID, Approver: approverFilter,
		Search: req.Search, From: req.From, To: req.To,
	}
	if platform {
		filter.TenantID = req.TenantID
		return true, filter, nil
	}
	filter.TenantID = actorTenantID
	if !manage {
		filter.RequesterID = actorID
	}
	return false, filter, nil
}

func (s *Service) getVisibleApproval(ctx context.Context, actorID, actorTenantID, approvalID string) (*model.Approval, error) {
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return nil, err
	}
	var approval *model.Approval
	if ac.platform {
		approval, err = s.Store.GetApprovalByID(ctx, approvalID)
	} else {
		approval, err = s.Store.GetApproval(ctx, actorTenantID, approvalID)
	}
	if err != nil {
		return nil, err
	}
	if approval == nil {
		return nil, httperr.NotFound("approval not found")
	}
	if !ac.platform && approval.TenantID != actorTenantID {
		return nil, httperr.Forbidden("approval not found")
	}
	if !ac.platform {
		if err := s.Authorize(ctx, actorID, "read", "approval"); err != nil {
			return nil, err
		}
	}
	return approval, nil
}

type ApprovalDetail struct {
	Approval model.Approval       `json:"approval"`
	Steps    []model.ApprovalStep `json:"steps"`
}

type ApprovalDelegationRecord = model.ApprovalDelegation

func (s *Service) GetApproval(ctx context.Context, actorID, actorTenantID, approvalID string) (*ApprovalDetail, error) {
	approval, err := s.getVisibleApproval(ctx, actorID, actorTenantID, approvalID)
	if err != nil {
		return nil, err
	}
	steps, err := s.Store.ListApprovalSteps(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return nil, err
	}
	for i := range steps {
		actors, actorErr := s.Store.ListApprovalStepActors(ctx, approval.TenantID, approval.ID, steps[i].ID)
		if actorErr != nil {
			return nil, actorErr
		}
		steps[i].Actors = actors
	}
	if approval.Action == model.ApprovalActionCreate && approval.ObjectType == model.ApprovalObjectAPIKey &&
		approval.Status == model.ApprovalStatusCompleted && approval.RequesterID == actorID {
		secret, modified, err := s.consumeApprovalSecret(approval)
		if err != nil {
			return nil, err
		}
		if modified != nil {
			if err := s.Store.ReplaceApprovalResult(ctx, approval.TenantID, approval.ID, *modified); err != nil {
				return nil, err
			}
		}
		if secret != "" {
			var result map[string]any
			_ = json.Unmarshal([]byte(*modified), &result)
			if result == nil {
				result = map[string]any{}
			}
			result["api_key"] = secret
			if raw, marshalErr := json.Marshal(result); marshalErr == nil {
				approval.ResultJSON = string(raw)
			}
		}
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: approval.TenantID, UserID: actorID, Action: "approval.viewed",
		Resource: "approval", ResourceID: approval.ID, DetailJSON: `{"view":"detail"}`,
	}); err != nil {
		return nil, err
	}
	return &ApprovalDetail{Approval: *approval, Steps: steps}, nil
}

func (s *Service) consumeApprovalSecret(approval *model.Approval) (string, *string, error) {
	var result map[string]any
	if err := json.Unmarshal([]byte(approval.ResultJSON), &result); err != nil {
		return "", nil, err
	}
	ciphertext, _ := result["api_key_ciphertext"].(string)
	if ciphertext == "" {
		return "", nil, nil
	}
	secret, err := crypto.Decrypt(s.EncryptKey, ciphertext)
	if err != nil {
		return "", nil, httperr.Internal("failed to decrypt approval secret")
	}
	delete(result, "api_key_ciphertext")
	result["api_key_revealed"] = true
	raw, err := json.Marshal(result)
	if err != nil {
		return "", nil, err
	}
	text := string(raw)
	return secret, &text, nil
}

func (s *Service) ExportApprovals(
	ctx context.Context, actorID, actorTenantID string, req ApprovalListRequest, output io.Writer,
) (int64, error) {
	platform, filter, err := s.approvalListFilter(ctx, actorID, actorTenantID, req)
	if err != nil {
		return 0, err
	}
	ids, total, err := s.Store.ListApprovalExportIDs(
		ctx, platform, actorTenantID, filter,
		repository.ApprovalListQuery{SortBy: req.SortBy, Order: req.Order, PageSize: approvalExportLimit + 1},
	)
	if err != nil {
		return 0, err
	}
	if total > approvalExportLimit {
		return 0, httperr.BadRequest(40095, "导出结果超过 10,000 行，请缩小筛选范围")
	}
	writer := csv.NewWriter(output)
	if err := writer.Write([]string{"request_no", "object_type", "object_id", "action", "title", "status", "requester_id", "created_at", "decided_at", "expires_at"}); err != nil {
		return 0, err
	}
	formatTime := func(value time.Time) string {
		if value.IsZero() {
			return ""
		}
		return value.UTC().Format(time.RFC3339)
	}
	var exported int64
	const pageSize = 100
	for start := 0; start < len(ids); start += pageSize {
		end := min(start+pageSize, len(ids))
		batchIDs := ids[start:end]
		byID := make(map[string]model.Approval, len(batchIDs))
		batch, err := s.Store.ListApprovalsByIDs(ctx, platform, actorTenantID, batchIDs)
		if err != nil {
			return exported, err
		}
		for _, approval := range batch {
			byID[approval.ID] = approval
		}
		if len(batch) == 0 {
			break
		}
		for _, approvalID := range batchIDs {
			approval, ok := byID[approvalID]
			if !ok {
				continue
			}
			if err := writer.Write([]string{
				sanitizeCSVCell(approval.RequestNo),
				sanitizeCSVCell(approval.ObjectType),
				sanitizeCSVCell(approval.ObjectID),
				sanitizeCSVCell(approval.Action),
				sanitizeCSVCell(approval.Title),
				sanitizeCSVCell(approval.Status),
				sanitizeCSVCell(approval.RequesterID),
				formatTime(approval.CreatedAt),
				formatTime(approval.DecidedAt),
				formatTime(approval.ExpiresAt),
			}); err != nil {
				return exported, err
			}
			exported++
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return exported, err
	}
	scope, targetTenantID := approvalExportScope(platform, actorTenantID, req.TenantID)
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: actorTenantID, UserID: actorID, Action: "approval.exported", Resource: "approval",
		ResourceID: "csv", DetailJSON: fmt.Sprintf(
			`{"rows":%d,"limit":%d,"scope":%q,"target_tenant_id":%q}`,
			exported, approvalExportLimit, scope, targetTenantID,
		),
		ActorTenantID: actorTenantID, TargetTenantID: targetTenantID, Scope: scope,
		AuthorizationDecision: "ALLOW", AuthorizationPermission: "read:approval",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}); err != nil {
		return exported, err
	}
	return exported, nil
}

func approvalExportScope(platform bool, actorTenantID, requestedTenantID string) (string, string) {
	if !platform {
		return string(TenantScopeCurrent), actorTenantID
	}
	if requestedTenantID == "" {
		return string(TenantScopeAllAuthorized), actorTenantID
	}
	return string(TenantScopeSpecific), requestedTenantID
}

func sanitizeCSVCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func (s *Service) approvalUserRoles(ctx context.Context, actorID string) (map[string]bool, map[string]bool, error) {
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return nil, nil, err
	}
	roleIDs, err := s.expandRoles(ctx, roles)
	if err != nil {
		return nil, nil, err
	}
	roleSet := make(map[string]bool, len(roleIDs))
	for _, roleID := range roleIDs {
		roleSet[roleID] = true
	}
	teams, err := s.Store.ListUserTeams(ctx, actorID)
	if err != nil {
		return nil, nil, err
	}
	teamSet := make(map[string]bool, len(teams))
	for _, team := range teams {
		teamSet[team.ID] = true
	}
	return roleSet, teamSet, nil
}

func (s *Service) DecideApproval(ctx context.Context, actorID, actorTenantID, approvalID, decision, comment string) (*model.Approval, error) {
	if decision != "approve" && decision != "reject" {
		return nil, httperr.BadRequest(40096, "decision must be approve or reject")
	}
	if strings.TrimSpace(comment) == "" {
		return nil, httperr.BadRequest(40097, "comment is required")
	}
	if len(comment) > 512 {
		return nil, httperr.BadRequest(40098, "comment must be at most 512 characters")
	}
	if count, err := s.approvalDecisionLimiter.Count(ctx, actorID, time.Minute); err != nil {
		return nil, err
	} else if count >= approvalDecisionLimit {
		return nil, httperr.New(429, 42941, "too many approval decisions; try later")
	}
	if _, err := s.approvalDecisionLimiter.Incr(ctx, actorID, time.Minute); err != nil {
		return nil, err
	}
	approval, err := s.getVisibleApproval(ctx, actorID, actorTenantID, approvalID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if approval.Status != model.ApprovalStatusPendingApproval {
		return nil, httperr.New(409, 40909, "approval state changed")
	}
	if now.After(approval.ExpiresAt) {
		return nil, httperr.New(409, 40902, "approval has expired")
	}
	if approval.RequesterID == actorID {
		return nil, httperr.Forbidden("requester cannot approve")
	}
	step, err := s.Store.CurrentApprovalStep(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return nil, err
	}
	if step == nil || step.Status != model.ApprovalStepCurrent {
		return nil, httperr.New(409, 40903, "approval step state changed")
	}
	if step.DueAt != nil && now.After(*step.DueAt) {
		return nil, httperr.New(409, 40904, "approval step has expired")
	}
	roles, teams, err := s.approvalUserRoles(ctx, actorID)
	if err != nil {
		return nil, err
	}
	delegatedBy := ""
	if !approvalApproverMatches(step, actorID, roles, teams) {
		delegations, err := s.Store.ListActiveApprovalDelegations(
			ctx, approval.TenantID, actorID, approval.ObjectType, approval.Action, now,
		)
		if err != nil {
			return nil, err
		}
		for _, delegation := range delegations {
			stepApprovers, err := model.ApprovalStepApprovers(step)
			if err != nil {
				return nil, httperr.Internal("invalid approval step approvers")
			}
			for _, approver := range stepApprovers {
				if approver.Type == model.ApprovalApproverUser && approver.Value == delegation.PrincipalID {
					delegatedBy = delegation.PrincipalID
					break
				}
			}
			if delegatedBy != "" {
				break
			}
		}
		if delegatedBy == "" {
			return nil, httperr.Forbidden("not authorized for this approval step")
		}
	}
	steps, err := s.Store.ListApprovalSteps(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return nil, err
	}
	for _, previous := range steps {
		if previous.StepNo >= step.StepNo {
			continue
		}
		actors, err := s.Store.ListApprovalStepActors(ctx, approval.TenantID, approval.ID, previous.ID)
		if err != nil {
			return nil, err
		}
		for _, actor := range actors {
			if actor.ActorID == actorID {
				return nil, httperr.Forbidden("user cannot approve multiple steps")
			}
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"step_no": step.StepNo, "actor": actorID, "delegated_by": delegatedBy, "object_type": approval.ObjectType,
		"action": approval.Action, "comment": comment,
	})
	action := "approval.approved"
	decisionResult := "APPROVED"
	if decision == "reject" {
		action = "approval.rejected"
		decisionResult = "REJECTED"
	}
	updated, final, err := s.Store.DecideApprovalStep(ctx, approval.TenantID, approval.ID, actorID, delegatedBy, decision, comment, now, &model.AuditLog{
		TenantID: approval.TenantID, UserID: actorID, Action: action, Resource: "approval",
		ResourceID: approval.ID, DetailJSON: string(detail), At: now,
		ActorTenantID: actorTenantID, TargetTenantID: approval.TargetTenantID,
		ApprovalID: approval.ID, ApprovalActionHash: approval.ApprovalActionHash,
		Result:                decisionResult,
		AuthorizationDecision: "ALLOW", AuthorizationPermission: "execute:approval",
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40905, "approval actor already decided")
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httperr.New(409, 40909, "approval state changed")
		}
		return nil, err
	}
	s.emitApprovalEvent(ctx, action, "approval decision", updated, "info")
	if final {
		if _, err := s.enqueueApprovalExecution(ctx, updated); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

func (s *Service) ListApprovalDelegations(ctx context.Context, actorID, actorTenantID string) ([]model.ApprovalDelegation, error) {
	if err := s.Authorize(ctx, actorID, "read", "approval"); err != nil {
		return nil, err
	}
	return s.Store.ListApprovalDelegations(ctx, actorTenantID, actorID)
}

func (s *Service) CreateApprovalDelegation(ctx context.Context, actorID, actorTenantID string, req ApprovalDelegationRequest) (*model.ApprovalDelegation, error) {
	if err := s.Authorize(ctx, actorID, "read", "approval"); err != nil {
		return nil, err
	}
	if req.DelegateID == "" || req.DelegateID == actorID {
		return nil, httperr.BadRequest(40100, "delegate_id must identify another user")
	}
	if req.EndsAt.Before(req.StartsAt) || req.EndsAt.Before(time.Now().UTC()) {
		return nil, httperr.BadRequest(40100, "delegation window is invalid")
	}
	if req.EndsAt.Sub(req.StartsAt) > 90*24*time.Hour {
		return nil, httperr.BadRequest(40100, "delegation window is too long")
	}
	delegate, err := s.Store.GetUser(ctx, req.DelegateID)
	if err != nil {
		return nil, err
	}
	if delegate == nil || delegate.TenantID != actorTenantID {
		return nil, httperr.BadRequest(40100, "delegate must belong to the same tenant")
	}
	switch {
	case req.ObjectType == "":
	case req.ObjectType == model.ApprovalObjectDataset ||
		req.ObjectType == model.ApprovalObjectDocument ||
		req.ObjectType == model.ApprovalObjectDocumentChunk ||
		req.ObjectType == model.ApprovalObjectAPIKey ||
		req.ObjectType == model.ApprovalObjectChat ||
		req.ObjectType == model.ApprovalObjectAgent ||
		req.ObjectType == model.ApprovalObjectModelProvider ||
		req.ObjectType == model.ApprovalObjectModelInstance ||
		req.ObjectType == model.ApprovalObjectModelModel:
	default:
		return nil, httperr.BadRequest(40100, "unsupported delegation object type")
	}
	if req.Action != "" && req.Action != model.ApprovalActionCreate &&
		req.Action != model.ApprovalActionUpdate &&
		req.Action != model.ApprovalActionDelete &&
		req.Action != model.ApprovalActionRevoke &&
		req.Action != model.ApprovalActionTest &&
		req.Action != model.ApprovalActionParse &&
		req.Action != model.ApprovalActionStop &&
		req.Action != model.ApprovalActionEnable &&
		req.Action != model.ApprovalActionDisable {
		return nil, httperr.BadRequest(40100, "unsupported delegation action")
	}
	now := time.Now().UTC()
	delegation := &model.ApprovalDelegation{
		TenantID: actorTenantID, PrincipalID: actorID, DelegateID: req.DelegateID,
		ObjectType: req.ObjectType, Action: req.Action,
		StartsAt: req.StartsAt.UTC(), EndsAt: req.EndsAt.UTC(),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateApprovalDelegation(ctx, delegation); err != nil {
		return nil, err
	}
	return delegation, nil
}

func (s *Service) DeleteApprovalDelegation(ctx context.Context, actorID, actorTenantID, delegationID string) (bool, error) {
	if err := s.Authorize(ctx, actorID, "read", "approval"); err != nil {
		return false, err
	}
	return s.Store.DeleteApprovalDelegation(ctx, actorTenantID, delegationID, actorID)
}

func (s *Service) BatchDecideApprovals(ctx context.Context, actorID, actorTenantID, decision string, req ApprovalBatchDecisionRequest) ([]ApprovalBatchDecisionResult, error) {
	if decision != model.ApprovalDecisionApprove && decision != model.ApprovalDecisionReject {
		return nil, httperr.BadRequest(40101, "decision must be approve or reject")
	}
	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		return nil, httperr.BadRequest(40101, "batch size must be between 1 and 100")
	}
	if strings.TrimSpace(req.Comment) == "" {
		return nil, httperr.BadRequest(40097, "comment is required")
	}
	if len(req.Comment) > 512 {
		return nil, httperr.BadRequest(40098, "comment must be at most 512 characters")
	}
	seen := map[string]bool{}
	for _, approvalID := range req.IDs {
		if approvalID == "" || seen[approvalID] {
			return nil, httperr.BadRequest(40101, "batch ids must be unique and non-empty")
		}
		seen[approvalID] = true
	}
	results := make([]ApprovalBatchDecisionResult, 0, len(req.IDs))
	for _, approvalID := range req.IDs {
		approval, err := s.DecideApproval(ctx, actorID, actorTenantID, approvalID, decision, req.Comment)
		result := ApprovalBatchDecisionResult{ID: approvalID}
		if err != nil {
			result.Error = err.Error()
			if httpErr, ok := err.(*httperr.Error); ok {
				result.Code = httpErr.Status
			}
		} else {
			result.OK = true
			result.Approval = approval
		}
		results = append(results, result)
	}
	return results, nil
}

func approvalApproverMatches(step *model.ApprovalStep, actorID string, roles, teams map[string]bool) bool {
	switch step.ApproverType {
	case model.ApprovalApproverUser:
		return step.ApproverValue == actorID
	case model.ApprovalApproverRole:
		return roles[step.ApproverValue]
	case model.ApprovalApproverTeam:
		return teams[step.ApproverValue]
	default:
		return false
	}
}

func (s *Service) CancelApproval(ctx context.Context, actorID, actorTenantID, approvalID, comment string) (*model.Approval, error) {
	if strings.TrimSpace(comment) == "" {
		return nil, httperr.BadRequest(40099, "comment is required")
	}
	approval, err := s.getVisibleApproval(ctx, actorID, actorTenantID, approvalID)
	if err != nil {
		return nil, err
	}
	if approval.Status != model.ApprovalStatusPendingApproval {
		return nil, httperr.New(409, 40909, "approval state changed")
	}
	if approval.RequesterID != actorID {
		if err := s.Authorize(ctx, actorID, "manage", "approval"); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	updated, err := s.cancelApproval(ctx, approval, actorID, actorTenantID, comment, now)
	if err != nil {
		return nil, err
	}
	s.emitApprovalEvent(ctx, "approval.canceled", "approval canceled", updated, "info")
	return updated, nil
}

func (s *Service) cancelApproval(ctx context.Context, approval *model.Approval, actorID, actorTenantID, comment string, now time.Time) (*model.Approval, error) {
	detail, _ := json.Marshal(map[string]any{"actor": actorID, "comment": comment})
	ok, err := s.Store.TransitionApproval(ctx, approval.TenantID, approval.ID, model.ApprovalStatusPendingApproval, model.ApprovalStatusCanceled, map[string]interface{}{
		"decided_at": now,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httperr.New(409, 40909, "approval state changed")
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: approval.TenantID, UserID: actorID, Action: "approval.canceled",
		Resource: "approval", ResourceID: approval.ID, DetailJSON: string(detail), At: now,
		ActorTenantID: actorTenantID, TargetTenantID: approval.TargetTenantID,
		ApprovalID: approval.ID, ApprovalActionHash: approval.ApprovalActionHash,
		Result: "CANCELED", AuthorizationDecision: "ALLOW", AuthorizationPermission: "execute:approval",
	}); err != nil {
		return nil, err
	}
	return s.Store.GetApproval(ctx, approval.TenantID, approval.ID)
}

func (s *Service) RetryApprovalExecution(ctx context.Context, actorID, actorTenantID, approvalID, comment string) (*model.Approval, error) {
	if strings.TrimSpace(comment) == "" {
		return nil, httperr.BadRequest(40099, "comment is required")
	}
	if err := s.Authorize(ctx, actorID, "manage", "approval"); err != nil {
		return nil, err
	}
	approval, err := s.getVisibleApproval(ctx, actorID, actorTenantID, approvalID)
	if err != nil {
		return nil, err
	}
	if approval.Status != model.ApprovalStatusExecutionFailed {
		return nil, httperr.New(409, 40909, "approval state changed")
	}
	now := time.Now().UTC()
	detail, _ := json.Marshal(map[string]any{"actor": actorID, "comment": comment})
	ok, err := s.Store.TransitionApproval(ctx, approval.TenantID, approval.ID, model.ApprovalStatusExecutionFailed, model.ApprovalStatusApproved, map[string]interface{}{
		"last_error": "",
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httperr.New(409, 40909, "approval state changed")
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: approval.TenantID, UserID: actorID, Action: "approval.retry", Resource: "approval",
		ResourceID: approval.ID, DetailJSON: string(detail), At: now,
		ActorTenantID: actorTenantID, TargetTenantID: approval.TargetTenantID,
		ApprovalID: approval.ID, ApprovalActionHash: approval.ApprovalActionHash,
		Result: "RETRY_SCHEDULED", AuthorizationDecision: "ALLOW", AuthorizationPermission: "manage:approval",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}); err != nil {
		return nil, err
	}
	updated, err := s.Store.GetApproval(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.enqueueApprovalExecution(ctx, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) enqueueApprovalExecution(ctx context.Context, approval *model.Approval) (bool, error) {
	if s.Runner == nil {
		return false, nil
	}
	key := model.ApprovalExecutionJobKeyPrefix + approval.ID
	active, err := s.Store.CountActiveJobsByKey(ctx, model.JobKindApprovalExecute, key)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	context, err := s.prepareActingContext(ctx, approval)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]string{
		"approval_id":          approval.ID,
		"target_tenant_id":     context.TargetTenantID,
		"acting_context_id":    context.ID,
		"approval_action_hash": context.ApprovalActionHash,
		"idempotency_key":      context.IdempotencyKey,
		"request_id":           context.RequestID,
	})
	if err != nil {
		return false, err
	}
	inserted, err := s.Runner.Enqueue(ctx, model.JobKindApprovalExecute, key, approval.TenantID, string(payload), time.Time{}, s.currentApprovalConfig().ExecutionMaxRetries)
	if err != nil {
		return false, err
	}
	if inserted {
		return true, nil
	}
	return s.Store.ResetFailedJobByKey(ctx, model.JobKindApprovalExecute, key, string(payload), s.currentApprovalConfig().ExecutionMaxRetries)
}

func (s *Service) RunApprovalMaintenance(ctx context.Context) error {
	if _, err := s.Store.ExpireApprovals(ctx, time.Now().UTC()); err != nil {
		return err
	}
	if err := s.emitApprovalReminders(ctx); err != nil {
		return err
	}
	approved, err := s.Store.ListApprovedApprovals(ctx, 200, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		return err
	}
	for _, approval := range approved {
		if _, err := s.enqueueApprovalExecution(ctx, &approval); err != nil {
			return err
		}
	}
	if _, err := s.Store.DeleteExpiredApprovalCredentials(ctx, time.Now().UTC(), 1000); err != nil {
		return err
	}
	if _, err := s.Store.DeleteExpiredApprovalRecords(
		ctx, time.Now().UTC(), s.currentApprovalConfig().RetentionDays, 1000,
	); err != nil {
		return err
	}
	if _, err := s.Store.DeleteExpiredRouteData(ctx, time.Now().UTC(), 1000); err != nil {
		return err
	}
	pending, err := s.Store.CountApprovalsByStatus(ctx, model.ApprovalStatusPendingApproval)
	if err != nil {
		return err
	}
	obs.Get().SetApprovalPending(pending)
	return nil
}

func (s *Service) emitApprovalReminders(ctx context.Context) error {
	pending, _, err := s.Store.ListAllApprovals(ctx, repository.ApprovalFilter{
		Status: model.ApprovalStatusPendingApproval,
	}, repository.ApprovalListQuery{Page: 1, PageSize: 200, SortBy: "expires_at", Order: "asc"})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	window := time.Duration(s.currentApprovalConfig().ReminderBeforeHours) * time.Hour
	for i := range pending {
		approval := &pending[i]
		step, err := s.Store.CurrentApprovalStep(ctx, approval.TenantID, approval.ID)
		if err != nil || step == nil {
			continue
		}
		due := now
		if step.DueAt != nil {
			due = *step.DueAt
		}
		if due.Before(now) {
			s.emitApprovalEventWithFields(ctx, "approval.overdue", "approval overdue", approval, "warn", map[string]string{
				"recipients":  "platform_admin",
				"escalation":  "true",
				"overdue_for": now.Sub(due).Round(time.Minute).String(),
			})
			continue
		}
		if due.Before(now.Add(window)) {
			s.emitApprovalEventWithFields(ctx, "approval.due_soon", "approval due soon", approval, "warn", map[string]string{
				"reminder_window_hours": strconv.Itoa(s.currentApprovalConfig().ReminderBeforeHours),
			})
		}
	}
	return nil
}

func (s *Service) sanitizeApprovalError(err error) string {
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	replacer := strings.NewReplacer("\n", " ", "\r", " ")
	return replacer.Replace(message)
}

func (s *Service) emitApprovalEventWithFields(ctx context.Context, eventType, title string, approval *model.Approval, severity string, overrides map[string]string) {
	fields := map[string]string{
		"approval_id":  approval.ID,
		"request_no":   approval.RequestNo,
		"object_type":  approval.ObjectType,
		"action":       approval.Action,
		"status":       approval.Status,
		"current_step": strconv.Itoa(approval.CurrentStep),
		"expire_at":    approval.ExpiresAt.UTC().Format(time.RFC3339),
		"action_url":   "/approvals/" + approval.ID + "/show",
	}
	if step, err := s.Store.CurrentApprovalStep(ctx, approval.TenantID, approval.ID); err == nil && step != nil {
		fields["approver_type"] = step.ApproverType
		fields["approver_value"] = step.ApproverValue
		if step.DueAt != nil {
			fields["step_due_at"] = step.DueAt.UTC().Format(time.RFC3339)
		}
		switch step.ApproverType {
		case model.ApprovalApproverUser:
			fields["recipients"] = step.ApproverValue
		case model.ApprovalApproverRole:
			fields["recipients"] = "role:" + step.ApproverValue
		case model.ApprovalApproverTeam:
			fields["recipients"] = "team:" + step.ApproverValue
		}
	}
	for key, value := range overrides {
		fields[key] = value
	}
	notify.Emit(ctx, notify.Event{
		Title: title, Severity: severity, Type: eventType, TenantID: approval.TenantID,
		Resource: "approval", ResourceID: approval.ID,
		Detail: fmt.Sprintf("%s %s (%s)", approval.ObjectType, approval.Action, approval.Status),
		Fields: fields,
	})
}

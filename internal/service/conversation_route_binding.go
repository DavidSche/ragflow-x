package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const routeSelectionTTL = 10 * time.Minute

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type SelectRouteCandidateRequest struct {
	CandidateID   string `json:"candidate_id"`
	CandidateKind string `json:"candidate_kind"`
}

type RouteSelectionResponse struct {
	RouteSelectionID string `json:"route_selection_id"`
	Kind             string `json:"kind"`
	TargetID         string `json:"target_id"`
	ExpiresAt        string `json:"expires_at"`
	State            string `json:"state"`
	RequiresSession  bool   `json:"requires_session"`
}

type ConversationBootstrapResponse struct {
	RouteSelectionID     string `json:"route_selection_id"`
	BootstrapOperationID string `json:"bootstrap_operation_id"`
	BootstrapType        string `json:"bootstrap_type"`
	Kind                 string `json:"kind"`
	TargetID             string `json:"target_id"`
	SessionID            string `json:"session_id"`
	SelectionState       string `json:"selection_state"`
	BootstrapState       string `json:"bootstrap_state"`
}

// SelectRouteCandidate reserves the user's one-time choice. It does not create
// a session; the separate bootstrap operation performs and records creation.
func (s *Service) SelectRouteCandidate(ctx context.Context, actorID, tenantID, routeID, idempotencyKey string, request SelectRouteCandidateRequest) (*RouteSelectionResponse, error) {
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return nil, httperr.BadRequest(40094, "Idempotency-Key must be 1-128 URL-safe characters")
	}
	candidateID, candidateKind := strings.TrimSpace(request.CandidateID), strings.TrimSpace(request.CandidateKind)
	if candidateID == "" || (candidateKind != model.AssistantKindChat && candidateKind != model.AssistantKindAgent) {
		return nil, httperr.BadRequest(40095, "candidate_id and candidate_kind are required")
	}
	decision, err := s.getRouteDecision(ctx, tenantID, actorID, routeID)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, httperr.NotFound("route decision not found")
	}
	now := time.Now().UTC()
	if !decision.ExpiresAt.After(now) {
		return nil, httperr.New(410, 41091, "route decision expired")
	}
	candidates, err := decodeRouteSnapshot(decision.CandidatesJSON)
	if err != nil {
		return nil, err
	}
	var selected *RouteCandidate
	for index := range candidates {
		if candidates[index].Kind == candidateKind && candidates[index].TargetID == candidateID {
			selected = &candidates[index]
			break
		}
	}
	if selected == nil {
		return nil, httperr.BadRequest(40096, "candidate is not in the route decision")
	}
	if existing, err := s.Store.GetRouteSelectionByScope(ctx, tenantID, actorID, routeID, idempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Kind != candidateKind || existing.TargetID != candidateID {
			return nil, httperr.New(409, 40990, "route selection idempotency conflict")
		}
		return routeSelectionResponse(existing), nil
	}
	catalog, err := s.validateSelectableAssistant(ctx, actorID, tenantID, candidateKind, candidateID)
	if err != nil {
		return nil, err
	}
	if catalog.CatalogVersion != selected.CatalogVersion {
		return nil, httperr.New(409, 40991, "assistant catalog changed")
	}
	selection := &model.RouteSelection{
		ID: id.New(), TenantID: tenantID, UserID: actorID, RouteID: routeID,
		Kind: candidateKind, TargetID: candidateID, CatalogVersion: selected.CatalogVersion,
		State: model.RouteSelectionNew, IdempotencyKey: idempotencyKey,
		ExpiresAt: now.Add(routeSelectionTTL), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateRouteSelection(ctx, selection); err != nil {
		if existing, lookupErr := s.Store.GetRouteSelectionByScope(ctx, tenantID, actorID, routeID, idempotencyKey); lookupErr == nil && existing != nil {
			return routeSelectionResponse(existing), nil
		}
		return nil, err
	}
	if err := recordRouteAudit(ctx, s, tenantID, actorID, selection.ID, "conversation.route.selection_created", map[string]interface{}{
		"route_id": routeID, "candidate_kind": candidateKind, "candidate_id": candidateID,
		"catalog_version": selected.CatalogVersion, "state": selection.State,
	}); err != nil {
		return nil, err
	}
	return routeSelectionResponse(selection), nil
}

// BootstrapRouteSelection revalidates ownership, permission, status, catalog
// version and candidate membership before creating the target session.
func (s *Service) BootstrapRouteSelection(ctx context.Context, actorID, tenantID, selectionID, idempotencyKey string) (*ConversationBootstrapResponse, error) {
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return nil, httperr.BadRequest(40097, "Idempotency-Key must be 1-128 URL-safe characters")
	}
	selection, err := s.Store.GetRouteSelection(ctx, tenantID, actorID, selectionID)
	if err != nil {
		return nil, err
	}
	if selection == nil {
		return nil, httperr.NotFound("route selection not found")
	}
	now := time.Now().UTC()
	if operation, err := s.Store.GetBootstrapOperationByScope(ctx, tenantID, actorID, selectionID, idempotencyKey); err != nil {
		return nil, err
	} else if operation != nil {
		return bootstrapResponse(selection, operation), nil
	}
	if !selection.ExpiresAt.After(now) {
		_ = s.Store.CompleteBootstrap(ctx, selection.ID, id.New(), model.BootstrapExpired, model.RouteSelectionExpired, "", "route selection expired", now)
		return nil, httperr.New(410, 41092, "route selection expired")
	}
	if selection.State != model.RouteSelectionNew {
		return nil, httperr.New(409, 40992, "route selection consumed")
	}
	decision, err := s.getRouteDecision(ctx, tenantID, actorID, selection.RouteID)
	if err != nil {
		return nil, err
	}
	if decision == nil || !decision.ExpiresAt.After(now) {
		return nil, httperr.New(410, 41093, "route decision expired")
	}
	candidates, err := decodeRouteSnapshot(decision.CandidatesJSON)
	if err != nil {
		return nil, err
	}
	if !routeSnapshotContains(candidates, selection.Kind, selection.TargetID, selection.CatalogVersion) {
		return nil, httperr.New(409, 40993, "route candidate is stale")
	}
	if err := s.validateBootstrapPermission(ctx, actorID, selection.Kind); err != nil {
		return nil, err
	}
	if _, err := s.validateSelectableAssistant(ctx, actorID, tenantID, selection.Kind, selection.TargetID); err != nil {
		return nil, err
	}
	operation := &model.BootstrapOperation{
		ID: id.New(), TenantID: tenantID, UserID: actorID, RouteSelectionID: selection.ID,
		IdempotencyKey: idempotencyKey, BootstrapType: model.BootstrapTypeCreateSession,
		Kind: selection.Kind, TargetID: selection.TargetID,
		State: model.BootstrapPending, ExpiresAt: selection.ExpiresAt,
		CreatedAt: now, UpdatedAt: now,
	}
	err = s.Store.ReserveSelectionForBootstrap(ctx, selection, operation, now)
	if errors.Is(err, repository.ErrRouteTransitionMiss) {
		if concurrent, lookupErr := s.Store.GetBootstrapOperationByScope(ctx, tenantID, actorID, selectionID, idempotencyKey); lookupErr == nil && concurrent != nil {
			return bootstrapResponse(selection, concurrent), nil
		}
		return nil, httperr.New(409, 40994, "route selection is already being consumed")
	}
	if err != nil {
		return nil, err
	}
	sessionID, createErr := s.createRouteSession(ctx, tenantID, selection.Kind, selection.TargetID)
	bootstrapState, selectionState := model.BootstrapSucceeded, model.RouteSelectionConsumed
	lastError := ""
	if createErr != nil {
		bootstrapState, selectionState, lastError = model.BootstrapFailed, model.RouteSelectionFailed, "session bootstrap failed"
	}
	if err := s.Store.CompleteBootstrap(ctx, selection.ID, operation.ID, bootstrapState, selectionState, sessionID, lastError, time.Now().UTC()); err != nil {
		return nil, err
	}
	if createErr != nil {
		_ = recordRouteAudit(ctx, s, tenantID, actorID, selection.ID, "conversation.route.bootstrap_failed", map[string]interface{}{
			"bootstrap_operation_id": operation.ID, "candidate_kind": selection.Kind, "candidate_id": selection.TargetID,
		})
		return nil, createErr
	}
	selection.State = selectionState
	operation.State = bootstrapState
	operation.SessionID = sessionID
	if err := recordRouteAudit(ctx, s, tenantID, actorID, selection.ID, "conversation.route.bootstrap_succeeded", map[string]interface{}{
		"bootstrap_operation_id": operation.ID, "candidate_kind": selection.Kind, "candidate_id": selection.TargetID, "session_created": true,
	}); err != nil {
		return nil, err
	}
	return bootstrapResponse(selection, operation), nil
}

func (s *Service) validateSelectableAssistant(ctx context.Context, actorID, tenantID, kind, targetID string) (*model.AssistantCatalog, error) {
	if err := s.Authorize(ctx, actorID, "execute", kind); err != nil {
		return nil, err
	}
	catalog, err := s.Store.GetAssistantCatalog(ctx, tenantID, kind, targetID)
	if err != nil {
		return nil, err
	}
	if catalog == nil || catalog.EffectiveStatus != model.AssistantEffectiveActive || !catalog.Discoverable {
		return nil, httperr.NotFound("assistant is unavailable")
	}
	switch kind {
	case model.AssistantKindChat:
		shadow, err := s.Store.GetChatShadow(ctx, tenantID, targetID, false)
		if err != nil {
			return nil, err
		}
		if shadow == nil || shadow.Status != "active" {
			return nil, httperr.NotFound("assistant is unavailable")
		}
	case model.AssistantKindAgent:
		shadow, err := s.Store.GetAgentShadow(ctx, tenantID, targetID, false)
		if err != nil {
			return nil, err
		}
		if shadow == nil || shadow.Status != "active" {
			return nil, httperr.NotFound("assistant is unavailable")
		}
	default:
		return nil, httperr.BadRequest(40099, "unsupported assistant kind")
	}
	return catalog, nil
}

func (s *Service) validateBootstrapPermission(ctx context.Context, actorID, kind string) error {
	if err := s.Authorize(ctx, actorID, "execute", kind); err != nil {
		return err
	}
	if kind == model.AssistantKindAgent {
		return s.Authorize(ctx, actorID, "session:create", "agent")
	}
	return nil
}

func (s *Service) createRouteSession(ctx context.Context, tenantID, kind, targetID string) (string, error) {
	name := "Conversation Center"
	switch kind {
	case model.AssistantKindChat:
		session, err := s.CreateChatSession(ctx, tenantID, targetID, name, false)
		if err != nil {
			return "", err
		}
		return session.ID, nil
	case model.AssistantKindAgent:
		session, err := s.CreateAgentSession(ctx, tenantID, targetID, name, false)
		if err != nil {
			return "", err
		}
		return session.ID, nil
	default:
		return "", httperr.BadRequest(40098, "unsupported assistant kind")
	}
}

func routeSnapshotContains(candidates []RouteCandidate, kind, targetID string, catalogVersion int64) bool {
	for _, candidate := range candidates {
		if candidate.Kind == kind && candidate.TargetID == targetID && candidate.CatalogVersion == catalogVersion {
			return true
		}
	}
	return false
}

func routeSelectionResponse(selection *model.RouteSelection) *RouteSelectionResponse {
	return &RouteSelectionResponse{
		RouteSelectionID: selection.ID, Kind: selection.Kind, TargetID: selection.TargetID,
		ExpiresAt: selection.ExpiresAt.UTC().Format(time.RFC3339Nano), State: selection.State,
		RequiresSession: true,
	}
}

func bootstrapResponse(selection *model.RouteSelection, operation *model.BootstrapOperation) *ConversationBootstrapResponse {
	return &ConversationBootstrapResponse{
		RouteSelectionID: selection.ID, BootstrapOperationID: operation.ID,
		BootstrapType: operation.BootstrapType, Kind: selection.Kind, TargetID: selection.TargetID,
		SessionID: operation.SessionID, SelectionState: selection.State, BootstrapState: operation.State,
	}
}

func recordRouteAudit(ctx context.Context, service *Service, tenantID, actorID, resourceID, action string, detail map[string]interface{}) error {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID, Action: action,
		Resource: "conversation_route", ResourceID: resourceID,
		DetailJSON: encodeRouteJSON(detail), At: time.Now().UTC(),
	}
	return service.RecordAudit(ctx, entry)
}

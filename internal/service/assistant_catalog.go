package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const assistantCatalogPageSize = 100

const catalogReconcileTTL = 5 * time.Minute

// ConversationAssistant is the API projection of an executable chat or
// workflow agent. RAGFlow and the ownership shadows remain execution facts.
type ConversationAssistant struct {
	ID                     string            `json:"id"`
	Kind                   string            `json:"kind"`
	Name                   string            `json:"name"`
	Description            string            `json:"description,omitempty"`
	Categories             []string          `json:"categories"`
	Capabilities           []string          `json:"capabilities"`
	Intents                []string          `json:"intents"`
	Keywords               []string          `json:"keywords"`
	Examples               []string          `json:"examples"`
	OwnerID                string            `json:"owner_id,omitempty"`
	GovernanceStatus       string            `json:"governance_status"`
	Discoverable           bool              `json:"discoverable"`
	RoutingWeight          float64           `json:"routing_weight"`
	UpstreamStatus         string            `json:"upstream_status"`
	Status                 string            `json:"status"`
	EffectiveStatus        string            `json:"effective_status"`
	StatusReason           string            `json:"status_reason"`
	EffectiveStatusReason  string            `json:"effective_status_reason"`
	RoutingReadiness       float64           `json:"routing_readiness"`
	RoutingReadinessType   string            `json:"routing_readiness_type"`
	AgentFlowReadiness     float64           `json:"agent_flow_readiness"`
	AssistantRiskLevel     string            `json:"assistant_risk_level"`
	CapabilityRisk         map[string]string `json:"capability_risk"`
	WorkflowRiskUpperBound string            `json:"workflow_risk_upper_bound"`
	CatalogVersion         int64             `json:"catalog_version"`
	CatalogUpdatedAt       string            `json:"catalog_updated_at,omitempty"`
	AutoSelectEnabled      bool              `json:"auto_select_enabled"`
	UpdatedAt              string            `json:"updated_at"`
}

func decodeStringArray(value string) []string {
	if value == "" {
		return []string{}
	}
	items := []string{}
	if err := json.Unmarshal([]byte(value), &items); err != nil {
		return []string{}
	}
	return items
}

func decodeStringMap(value string) map[string]string {
	values := map[string]string{}
	if value != "" && json.Unmarshal([]byte(value), &values) == nil {
		return values
	}
	return values
}

func assistantStatus(shadowStatus string) (string, string) {
	if shadowStatus == "active" {
		return model.AssistantEffectiveActive, model.AssistantStatusReasonActive
	}
	return model.AssistantEffectiveInactive, model.AssistantStatusReasonUpstreamArchived
}

func chatAssistant(shadow *model.ChatShadow) *model.AssistantCatalog {
	description := ""
	var config struct {
		Description string `json:"description"`
	}
	if shadow.ConfigJSON != "" && json.Unmarshal([]byte(shadow.ConfigJSON), &config) == nil {
		description = config.Description
	}
	now := time.Now().UTC()
	effective, reason := assistantStatus(shadow.Status)
	return &model.AssistantCatalog{
		ID:       shadow.TenantID + ":" + model.AssistantKindChat + ":" + shadow.ID,
		TenantID: shadow.TenantID, Kind: model.AssistantKindChat, TargetID: shadow.ID,
		Name: shadow.Name, Description: description,
		UpstreamStatus: shadow.Status, GovernanceStatus: model.AssistantGovernanceEnabled,
		EffectiveStatus: effective, EffectiveStatusReason: reason,
		OwnerID: shadow.OwnerID, CategoriesJSON: "[]", CapabilitiesJSON: `["conversation"]`,
		IntentsJSON: "[]", KeywordsJSON: "[]", ExamplesJSON: "[]",
		RoutingWeight: 1, AssistantRiskLevel: model.AssistantRiskLow,
		CapabilityRiskJSON: "{}", WorkflowRiskJSON: "{}",
		WorkflowRiskUpperBound: model.AssistantRiskLow, Discoverable: true,
		AutoSelectEnabled: false, CatalogVersion: 1, RoutingReadiness: 0.25, AgentFlowReadiness: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}

func agentAssistant(shadow *model.AgentShadow, liveDescription string) *model.AssistantCatalog {
	now := time.Now().UTC()
	effective, reason := assistantStatus(shadow.Status)
	return &model.AssistantCatalog{
		ID:       shadow.TenantID + ":" + model.AssistantKindAgent + ":" + shadow.ID,
		TenantID: shadow.TenantID, Kind: model.AssistantKindAgent, TargetID: shadow.ID,
		Name: shadow.Title, Description: liveDescription,
		UpstreamStatus: shadow.Status, GovernanceStatus: model.AssistantGovernanceEnabled,
		EffectiveStatus: effective, EffectiveStatusReason: reason,
		OwnerID: shadow.OwnerID, CategoriesJSON: "[]", CapabilitiesJSON: `["workflow","conversation"]`,
		IntentsJSON: "[]", KeywordsJSON: "[]", ExamplesJSON: "[]",
		RoutingWeight: 1, AssistantRiskLevel: model.AssistantRiskMedium,
		CapabilityRiskJSON: "{}", WorkflowRiskJSON: "{}",
		WorkflowRiskUpperBound: model.AssistantRiskMedium, Discoverable: true,
		AutoSelectEnabled: false, CatalogVersion: 1, RoutingReadiness: 0.25, AgentFlowReadiness: 0,
		CreatedAt: now, UpdatedAt: now,
	}
}

// ReconcileAssistantCatalog refreshes basic facts from ownership shadows. Agent
// data pipelines are backend objects and never become conversation assistants.
func (s *Service) ReconcileAssistantCatalog(ctx context.Context, tenantID string) error {
	page := 1
	for {
		chats, total, err := s.Store.ListChatShadows(ctx, tenantID, false, repository.ChatFilter{}, page, assistantCatalogPageSize)
		if err != nil {
			return err
		}
		for index := range chats {
			if err := s.Store.UpsertAssistantCatalog(ctx, chatAssistant(&chats[index])); err != nil {
				return err
			}
		}
		if int64(page*assistantCatalogPageSize) >= total || len(chats) == 0 {
			break
		}
		page++
	}

	liveAgents := map[string]string{}
	liveTotal := int64(0)
	for page := 1; ; page++ {
		agents, total, err := s.RAGFlow.ListAgents(ctx, repository.ConversationAgentListFilter(page, assistantCatalogPageSize))
		if err != nil {
			// Keep existing catalog facts if the engine is briefly unavailable;
			// shadow-only reconciliation must not delete valid entries.
			liveAgents = nil
			break
		}
		liveTotal = total
		for _, agent := range agents {
			if agent.CanvasCategory == model.AgentCanvasCategoryWorkflow {
				liveAgents[agent.ID] = agent.Description
			}
		}
		if int64(page*assistantCatalogPageSize) >= liveTotal || len(agents) == 0 {
			break
		}
	}

	for page := 1; ; page++ {
		agents, total, err := s.Store.ListAgentShadows(ctx, tenantID, false, repository.AgentFilter{}, page, assistantCatalogPageSize)
		if err != nil {
			return err
		}
		for index := range agents {
			if liveAgents == nil {
				continue
			}
			description, isWorkflow := liveAgents[agents[index].ID]
			if !isWorkflow {
				if err := s.Store.DeleteAssistantCatalog(ctx, tenantID, model.AssistantKindAgent, agents[index].ID); err != nil {
					return err
				}
				continue
			}
			if err := s.Store.UpsertAssistantCatalog(ctx, agentAssistant(&agents[index], description)); err != nil {
				return err
			}
		}
		if int64(page*assistantCatalogPageSize) >= total || len(agents) == 0 {
			break
		}
	}
	return nil
}

// ListConversationAssistants returns the tenant's executable assistants after
// refreshing the discovery read model.
func (s *Service) ListConversationAssistants(ctx context.Context, actorID, tenantID string, query string, requestedKinds []string, page, pageSize int) ([]ConversationAssistant, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	executableKinds := []string{}
	if err := s.Authorize(ctx, actorID, "execute", "chat"); err == nil {
		executableKinds = append(executableKinds, model.AssistantKindChat)
	}
	if err := s.Authorize(ctx, actorID, "execute", "agent"); err == nil {
		executableKinds = append(executableKinds, model.AssistantKindAgent)
	}
	if len(executableKinds) == 0 {
		return []ConversationAssistant{}, 0, nil
	}
	kinds := requestedKinds
	if len(kinds) > 0 {
		allowed := make(map[string]bool, len(executableKinds))
		for _, kind := range executableKinds {
			allowed[kind] = true
		}
		kinds = make([]string, 0, len(requestedKinds))
		for _, kind := range requestedKinds {
			if allowed[kind] {
				kinds = append(kinds, kind)
			}
		}
		if len(kinds) == 0 {
			return []ConversationAssistant{}, 0, nil
		}
	} else {
		kinds = executableKinds
	}
	if err := s.reconcileAssistantCatalogTTL(ctx, tenantID); err != nil {
		return nil, 0, err
	}
	items, total, err := s.Store.ListAssistantCatalogs(ctx, tenantID, repository.AssistantCatalogFilter{Query: query, Kinds: kinds}, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	result := make([]ConversationAssistant, 0, len(items))
	for _, item := range items {
		result = append(result, *toConversationAssistant(&item))
	}
	return result, total, nil
}

// reconcileAssistantCatalogTTL gates the upstream reconciliation behind a
// per-tenant TTL so the route hot path does not trigger a RAGFlow page pull
// on every read. The next reconcile is forced when the TTL expires.
func (s *Service) reconcileAssistantCatalogTTL(ctx context.Context, tenantID string) error {
	s.catalogReconcileMu.Lock()
	if last, ok := s.catalogReconcileLast[tenantID]; ok && time.Since(last) < catalogReconcileTTL {
		s.catalogReconcileMu.Unlock()
		return nil
	}
	s.catalogReconcileLast[tenantID] = time.Now().UTC()
	s.catalogReconcileMu.Unlock()
	if err := s.ReconcileAssistantCatalog(ctx, tenantID); err != nil {
		s.catalogReconcileMu.Lock()
		delete(s.catalogReconcileLast, tenantID)
		s.catalogReconcileMu.Unlock()
		return err
	}
	return nil
}

type UpdateConversationAssistantRequest struct {
	Description            *string            `json:"description"`
	Categories             *[]string          `json:"categories"`
	Capabilities           *[]string          `json:"capabilities"`
	Intents                *[]string          `json:"intents"`
	Keywords               *[]string          `json:"keywords"`
	Examples               *[]string          `json:"examples"`
	AssistantRiskLevel     *string            `json:"assistant_risk_level"`
	CapabilityRisk         *map[string]string `json:"capability_risk"`
	WorkflowRiskUpperBound *string            `json:"workflow_risk_upper_bound"`
	AgentFlowReadiness     *float64           `json:"agent_flow_readiness"`
	AutoSelectEnabled      *bool              `json:"auto_select_enabled"`
}

type UpdateConversationAssistantGovernanceRequest struct {
	GovernanceStatus *string  `json:"governance_status"`
	Discoverable     *bool    `json:"discoverable"`
	RoutingWeight    *float64 `json:"routing_weight"`
	OwnerID          *string  `json:"owner_id"`
	WorkflowRisk     *string  `json:"workflow_risk"`
}

// UpdateConversationAssistant governs M3 route metadata. RAGFlow execution
// facts remain authoritative; this only updates the discovery read model.
func (s *Service) UpdateConversationAssistant(ctx context.Context, actorID, tenantID, kind, targetID string, request UpdateConversationAssistantRequest) (*ConversationAssistant, error) {
	if err := s.Authorize(ctx, actorID, "manage", "assistant"); err != nil {
		return nil, err
	}
	if kind != model.AssistantKindChat && kind != model.AssistantKindAgent {
		return nil, httperr.BadRequest(40110, "unsupported assistant kind")
	}
	if strings.TrimSpace(targetID) == "" {
		return nil, httperr.BadRequest(40111, "assistant target id is required")
	}
	if err := s.ReconcileAssistantCatalog(ctx, tenantID); err != nil {
		return nil, err
	}
	item, err := s.Store.GetAssistantCatalog(ctx, tenantID, kind, targetID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, httperr.NotFound("assistant not found")
	}
	if request.Description != nil {
		item.Description = strings.TrimSpace(*request.Description)
	}
	if request.Categories != nil {
		item.CategoriesJSON = encodeRouteJSON(*request.Categories)
	}
	if request.Capabilities != nil {
		item.CapabilitiesJSON = encodeRouteJSON(*request.Capabilities)
	}
	if request.Intents != nil {
		item.IntentsJSON = encodeRouteJSON(*request.Intents)
	}
	if request.Keywords != nil {
		item.KeywordsJSON = encodeRouteJSON(*request.Keywords)
	}
	if request.Examples != nil {
		item.ExamplesJSON = encodeRouteJSON(*request.Examples)
	}
	if request.AssistantRiskLevel != nil {
		risk, err := normalizeRouteRisk(*request.AssistantRiskLevel)
		if err != nil {
			return nil, err
		}
		item.AssistantRiskLevel = risk
	}
	if request.CapabilityRisk != nil {
		riskJSON, err := json.Marshal(*request.CapabilityRisk)
		if err != nil {
			return nil, err
		}
		item.CapabilityRiskJSON = string(riskJSON)
	}
	if request.WorkflowRiskUpperBound != nil {
		risk, err := normalizeRouteRisk(*request.WorkflowRiskUpperBound)
		if err != nil {
			return nil, err
		}
		item.WorkflowRiskUpperBound = risk
	}
	if request.AgentFlowReadiness != nil {
		if *request.AgentFlowReadiness < 0 || *request.AgentFlowReadiness > 1 {
			return nil, httperr.BadRequest(40113, "agent flow readiness must be between 0 and 1")
		}
		item.AgentFlowReadiness = *request.AgentFlowReadiness
	}
	if request.AutoSelectEnabled != nil {
		item.AutoSelectEnabled = *request.AutoSelectEnabled
	}
	item.CatalogVersion++
	item.UpdatedAt = time.Now().UTC()
	item.RoutingReadiness = deriveRoutingReadiness(item)
	if err := s.Store.UpdateAssistantGovernanceFields(ctx, item); err != nil {
		return nil, err
	}
	if err := recordRouteAudit(ctx, s, tenantID, actorID, targetID, "conversation.assistant.catalog.update", map[string]interface{}{
		"assistant_kind": kind, "catalog_version": item.CatalogVersion,
		"auto_select_enabled":  item.AutoSelectEnabled,
		"agent_flow_readiness": item.AgentFlowReadiness,
	}); err != nil {
		return nil, err
	}
	return &ConversationAssistant{
		ID: item.TargetID, Kind: item.Kind, Name: item.Name, Description: item.Description,
		Categories: decodeStringArray(item.CategoriesJSON), Capabilities: decodeStringArray(item.CapabilitiesJSON),
		Intents: decodeStringArray(item.IntentsJSON), Keywords: decodeStringArray(item.KeywordsJSON),
		Examples: decodeStringArray(item.ExamplesJSON), OwnerID: item.OwnerID,
		UpstreamStatus: item.UpstreamStatus, Status: item.EffectiveStatus,
		EffectiveStatus: item.EffectiveStatus, StatusReason: item.EffectiveStatusReason,
		EffectiveStatusReason: item.EffectiveStatusReason,
		RoutingReadiness:      item.RoutingReadiness, RoutingReadinessType: "METADATA_COMPLETENESS",
		AssistantRiskLevel: item.AssistantRiskLevel,
		AgentFlowReadiness: item.AgentFlowReadiness,
		CapabilityRisk:     decodeStringMap(item.CapabilityRiskJSON), WorkflowRiskUpperBound: item.WorkflowRiskUpperBound,
		CatalogVersion: item.CatalogVersion, CatalogUpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
		AutoSelectEnabled: item.AutoSelectEnabled, UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func toConversationAssistant(item *model.AssistantCatalog) *ConversationAssistant {
	return &ConversationAssistant{
		ID: item.TargetID, Kind: item.Kind, Name: item.Name, Description: item.Description,
		Categories: decodeStringArray(item.CategoriesJSON), Capabilities: decodeStringArray(item.CapabilitiesJSON),
		Intents: decodeStringArray(item.IntentsJSON), Keywords: decodeStringArray(item.KeywordsJSON),
		Examples: decodeStringArray(item.ExamplesJSON), OwnerID: item.OwnerID,
		GovernanceStatus: item.GovernanceStatus, Discoverable: item.Discoverable, RoutingWeight: item.RoutingWeight,
		UpstreamStatus: item.UpstreamStatus, Status: item.EffectiveStatus,
		EffectiveStatus: item.EffectiveStatus, StatusReason: item.EffectiveStatusReason,
		EffectiveStatusReason: item.EffectiveStatusReason,
		RoutingReadiness:      item.RoutingReadiness, RoutingReadinessType: "METADATA_COMPLETENESS",
		AssistantRiskLevel: item.AssistantRiskLevel,
		AgentFlowReadiness: item.AgentFlowReadiness,
		CapabilityRisk:     decodeStringMap(item.CapabilityRiskJSON), WorkflowRiskUpperBound: item.WorkflowRiskUpperBound,
		CatalogVersion: item.CatalogVersion, CatalogUpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
		AutoSelectEnabled: item.AutoSelectEnabled, UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// UpdateConversationAssistantGovernance manages governance-only fields that
// are never overwritten by upstream reconciliation. Only tenant admins may
// disable, archive, or hide an assistant from routing.
func (s *Service) UpdateConversationAssistantGovernance(ctx context.Context, actorID, tenantID, kind, targetID string, request UpdateConversationAssistantGovernanceRequest) (*ConversationAssistant, error) {
	if err := s.Authorize(ctx, actorID, "manage", "assistant"); err != nil {
		return nil, err
	}
	if kind != model.AssistantKindChat && kind != model.AssistantKindAgent {
		return nil, httperr.BadRequest(40110, "unsupported assistant kind")
	}
	if strings.TrimSpace(targetID) == "" {
		return nil, httperr.BadRequest(40111, "assistant target id is required")
	}
	item, err := s.Store.GetAssistantCatalog(ctx, tenantID, kind, targetID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, httperr.NotFound("assistant not found")
	}
	if request.GovernanceStatus != nil {
		status := strings.TrimSpace(*request.GovernanceStatus)
		switch status {
		case model.AssistantGovernanceEnabled, model.AssistantGovernanceDisabled, model.AssistantGovernanceArchived:
			item.GovernanceStatus = status
		default:
			return nil, httperr.BadRequest(40112, "governance_status must be enabled, disabled, or archived")
		}
	}
	if request.Discoverable != nil {
		item.Discoverable = *request.Discoverable
	}
	if request.RoutingWeight != nil {
		if *request.RoutingWeight < 0 || *request.RoutingWeight > 10 {
			return nil, httperr.BadRequest(40112, "routing_weight must be between 0 and 10")
		}
		item.RoutingWeight = *request.RoutingWeight
	}
	if request.OwnerID != nil {
		item.OwnerID = strings.TrimSpace(*request.OwnerID)
	}
	if request.WorkflowRisk != nil {
		risk := strings.TrimSpace(*request.WorkflowRisk)
		switch risk {
		case model.AssistantRiskLow, model.AssistantRiskMedium, model.AssistantRiskHigh:
			item.WorkflowRiskUpperBound = risk
		default:
			return nil, httperr.BadRequest(40112, "workflow_risk must be low, medium, or high")
		}
	}
	item.CatalogVersion++
	item.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateAssistantGovernanceFields(ctx, item); err != nil {
		return nil, err
	}
	if err := recordRouteAudit(ctx, s, tenantID, actorID, targetID, "conversation.assistant.governance_update", map[string]interface{}{
		"kind": kind, "governance_status": item.GovernanceStatus,
		"discoverable": item.Discoverable, "catalog_version": item.CatalogVersion,
	}); err != nil {
		return nil, err
	}
	return toConversationAssistant(item), nil
}

func normalizeRouteRisk(value string) (string, error) {
	risk := strings.ToLower(strings.TrimSpace(value))
	switch risk {
	case model.AssistantRiskLow, model.AssistantRiskMedium, model.AssistantRiskHigh:
		return risk, nil
	default:
		return "", httperr.BadRequest(40112, "assistant risk must be low, medium or high")
	}
}

func deriveRoutingReadiness(item *model.AssistantCatalog) float64 {
	score := 0.20
	if strings.TrimSpace(item.Description) != "" {
		score += 0.15
	}
	for _, value := range []string{item.KeywordsJSON, item.ExamplesJSON, item.IntentsJSON, item.CapabilitiesJSON, item.CategoriesJSON} {
		if strings.TrimSpace(strings.Trim(value, "[]")) != "" {
			score += 0.13
		}
	}
	return math.Min(score, 1)
}

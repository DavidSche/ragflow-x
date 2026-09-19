package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

const (
	RouteModeDisabled      = "disabled"
	RouteModeRecommendOnly = "recommend_only"
	RouteModeAutoLowRisk   = "auto_low_risk"
)

type routePolicyRuntime struct {
	Mode          string
	EffectiveMode string
	PolicyVersion string
	Calibration   RouteCalibration
	Calibrated    bool
	RerankStatus  string
}

func (s *Service) SetRoutePolicy(policy config.ConversationRouting) {
	s.RoutePolicy = normalizeRoutePolicy(policy)
}

type UpdateRoutePolicyRequest struct {
	AutoRouteMode string `json:"auto_route_mode"`
}

type RoutePolicyResponse struct {
	AutoRouteMode string `json:"auto_route_mode"`
}

// GetTenantRoutePolicy returns the tenant-level rollout mode. The system mode
// remains an outer boundary and is intentionally not writable through this API.
func (s *Service) GetTenantRoutePolicy(ctx context.Context, actorID, tenantID string) (*RoutePolicyResponse, error) {
	if err := s.Authorize(ctx, actorID, "manage", "assistant"); err != nil {
		return nil, err
	}
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, httperr.NotFound("tenant not found")
	}
	return &RoutePolicyResponse{AutoRouteMode: normalizeRouteMode(tenant.AutoRouteMode)}, nil
}

// SetTenantRoutePolicy updates the tenant rollout mode and records the change.
func (s *Service) SetTenantRoutePolicy(ctx context.Context, actorID, tenantID string, request UpdateRoutePolicyRequest) (*RoutePolicyResponse, error) {
	if err := s.Authorize(ctx, actorID, "manage", "assistant"); err != nil {
		return nil, err
	}
	mode := normalizeRouteMode(request.AutoRouteMode)
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, httperr.NotFound("tenant not found")
	}
	tenant.AutoRouteMode = mode
	if err := s.Store.UpdateTenant(ctx, tenant); err != nil {
		return nil, err
	}
	if err := recordRouteAudit(ctx, s, tenantID, actorID, tenantID, "conversation.route.policy.update", map[string]interface{}{
		"auto_route_mode": mode,
	}); err != nil {
		return nil, err
	}
	return &RoutePolicyResponse{AutoRouteMode: mode}, nil
}

func normalizeRoutePolicy(policy config.ConversationRouting) config.ConversationRouting {
	if policy.Mode == "" {
		policy.Mode = RouteModeRecommendOnly
	}
	policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
	switch policy.Mode {
	case RouteModeDisabled, RouteModeAutoLowRisk:
	default:
		policy.Mode = RouteModeRecommendOnly
	}
	if policy.ConfidenceThreshold < 0.86 {
		policy.ConfidenceThreshold = 0.86
	}
	if policy.ConfidenceMargin < 0.12 {
		policy.ConfidenceMargin = 0.12
	}
	if policy.MinimumCandidates < 2 {
		policy.MinimumCandidates = 2
	}
	if policy.ReadinessThreshold < 0.80 {
		policy.ReadinessThreshold = 0.80
	}
	if policy.TotalTimeoutMs <= 0 {
		policy.TotalTimeoutMs = 1500
	}
	if policy.TotalTimeoutMs < 100 {
		policy.TotalTimeoutMs = 100
	}
	if policy.CatalogTTLSec <= 0 {
		policy.CatalogTTLSec = 300
	}
	if policy.CandidateLimit <= 0 {
		policy.CandidateLimit = 5
	}
	if policy.ResponseTopK <= 0 {
		policy.ResponseTopK = 3
	}
	if policy.ResponseTopK > policy.CandidateLimit {
		policy.ResponseTopK = policy.CandidateLimit
	}
	if policy.Rerank.TimeoutMs <= 0 {
		policy.Rerank.TimeoutMs = 1200
	}
	if policy.Rerank.TimeoutMs >= policy.TotalTimeoutMs {
		policy.Rerank.TimeoutMs = policy.TotalTimeoutMs / 2
	}
	if policy.Rerank.TopK < 2 {
		policy.Rerank.TopK = 2
	}
	if policy.Rerank.TopK > policy.CandidateLimit {
		policy.Rerank.TopK = policy.CandidateLimit
	}
	if policy.Rerank.MinBaseScore <= 0 {
		policy.Rerank.MinBaseScore = 0.05
	}
	policy.Gate = normalizeRouteGate(policy.Gate)
	return policy
}

func normalizeRouteGate(gate config.RouteGate) config.RouteGate {
	gate.Pilot = normalizeRouteEvidenceThreshold(gate.Pilot, 24, 0.85, 0.07)
	gate.Production = normalizeRouteEvidenceThreshold(gate.Production, 24, 0.99, 0.05)
	return gate
}

func normalizeRouteEvidenceThreshold(
	threshold config.RouteEvidenceThreshold,
	defaultSamples int,
	defaultWilson float64,
	defaultECE float64,
) config.RouteEvidenceThreshold {
	if threshold.MinHighConfidence <= 0 {
		threshold.MinHighConfidence = defaultSamples
	}
	if threshold.MinWilsonLower <= 0 || threshold.MinWilsonLower >= 1 {
		threshold.MinWilsonLower = defaultWilson
	}
	if threshold.MaxECE <= 0 || threshold.MaxECE >= 1 {
		threshold.MaxECE = defaultECE
	}
	return threshold
}

func (s *Service) loadRoutePolicy(ctx context.Context, tenantID string) (routePolicyRuntime, error) {
	policy := normalizeRoutePolicy(s.RoutePolicy)
	state := routePolicyRuntime{
		Mode:          policy.Mode,
		PolicyVersion: "route-policy-v1:unavailable",
		RerankStatus:  "not_applicable",
	}
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return routePolicyRuntime{}, err
	}
	if tenant == nil {
		return routePolicyRuntime{}, httperr.NotFound("tenant not found")
	}
	systemMode := normalizeRouteMode(s.RoutePolicy.Mode)
	tenantMode := normalizeRouteMode(tenant.AutoRouteMode)
	switch {
	case systemMode == RouteModeDisabled || tenantMode == RouteModeDisabled:
		policy.Mode = RouteModeDisabled
	case systemMode != RouteModeAutoLowRisk:
		policy.Mode = RouteModeRecommendOnly
	default:
		policy.Mode = tenantMode
	}
	policy = normalizeRoutePolicy(policy)
	state.Mode = policy.Mode
	if policy.Mode == RouteModeDisabled {
		state.EffectiveMode = RouteModeDisabled
		state.PolicyVersion = fmt.Sprintf("route-policy-v2:%s:disabled", tenantID)
		return state, nil
	}

	runs, _, err := s.Store.ListRouteEvaluationRuns(ctx, tenantID, 1, 1)
	if err != nil {
		return routePolicyRuntime{}, err
	}
	if len(runs) == 0 || runs[0].RouterVersion != conversationRouterVersion ||
		runs[0].GateState != model.RouteEvalGatePassed || !runs[0].AllowAutoLowRisk {
		state.EffectiveMode = RouteModeRecommendOnly
		state.PolicyVersion = fmt.Sprintf("route-policy-v2:%s:gate_not_passed", tenantID)
		return state, nil
	}

	var calibration RouteCalibration
	if err := json.Unmarshal([]byte(runs[0].CalibrationJSON), &calibration); err != nil {
		state.EffectiveMode = RouteModeRecommendOnly
		state.PolicyVersion = fmt.Sprintf("route-policy-v2:%s:calibration_invalid", tenantID)
		return state, nil
	}
	if len(calibration.Weights) == 0 || len(calibration.Weights) != len(calibration.FeatureNames) {
		state.EffectiveMode = RouteModeRecommendOnly
		state.PolicyVersion = fmt.Sprintf("route-policy-v2:%s:calibration_invalid", tenantID)
		return state, nil
	}
	state.Calibration = calibration
	state.Calibrated = true
	state.EffectiveMode = policy.Mode
	state.PolicyVersion = fmt.Sprintf("route-policy-v2:%s:%s:%s:%s", tenantID, policy.Mode, runs[0].ID, calibration.Version)
	return state, nil
}

func normalizeRouteMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RouteModeDisabled:
		return RouteModeDisabled
	case RouteModeAutoLowRisk:
		return RouteModeAutoLowRisk
	default:
		return RouteModeRecommendOnly
	}
}

func applyRouteCalibration(candidates []RouteCandidate, calibration RouteCalibration, query string) {
	confidences := make([]float64, len(candidates))
	for index := range candidates {
		features := routeEvalFeatureRow{
			normalizedScore: candidates[index].NormalizedScore,
			candidates:      len(candidates),
			competitor:      len(candidates) > 1,
			readiness:       candidates[index].RoutingReadiness,
			length:          queryLengthFeature(query),
			chinese:         containsHan(query),
		}
		if index == 0 && len(candidates) > 1 && candidates[0].NormalizedMargin != nil {
			features.margin = *candidates[0].NormalizedMargin
		}
		confidences[index] = routeCalibrationConfidence(calibration, features)
		candidates[index].Confidence = &confidences[index]
		candidates[index].ConfidenceStatus = "calibrated"
	}
	for index := range candidates {
		if index == 0 && len(candidates) > 1 {
			margin := confidences[0] - confidences[1]
			candidates[index].ConfidenceMargin = &margin
		}
	}
}

func routeAutoCandidate(candidates []RouteCandidate, policy config.ConversationRouting) *RouteCandidate {
	if policy.Mode != RouteModeAutoLowRisk {
		return nil
	}
	if len(candidates) < policy.MinimumCandidates {
		return nil
	}
	top := candidates[0]
	if top.Confidence == nil || top.ConfidenceStatus != "calibrated" || *top.Confidence < policy.ConfidenceThreshold {
		return nil
	}
	if top.ConfidenceMargin == nil || *top.ConfidenceMargin < policy.ConfidenceMargin {
		return nil
	}
	if !top.AutoSelectEnabled || top.PreExecutionRisk != model.AssistantRiskLow {
		return nil
	}
	if top.RoutingReadiness < policy.ReadinessThreshold {
		return nil
	}
	if top.Kind == model.AssistantKindAgent && top.AgentFlowReadiness < model.AgentFlowReadinessThreshold {
		return nil
	}
	if top.CatalogFreshnessSec < 0 || top.CatalogFreshnessSec > int64(policy.CatalogTTLSec) {
		return nil
	}
	return &candidates[0]
}

func rerankAllowed(policy config.ConversationRouting, candidates []RouteCandidate) bool {
	if !policy.Rerank.Enabled || !policy.Rerank.DistributionValidated || len(candidates) < 2 {
		return false
	}
	return policy.Rerank.ProviderName != "" && policy.Rerank.InstanceName != "" && policy.Rerank.ModelName != ""
}

func (s *Service) rerankRouteCandidates(ctx context.Context, policy config.ConversationRouting, query string, candidates []RouteCandidate) bool {
	if !rerankAllowed(policy, candidates) {
		return false
	}
	rerankCandidates := make([]RouteCandidate, 0, policy.Rerank.TopK)
	for _, candidate := range candidates {
		if candidate.BaseScore < policy.Rerank.MinBaseScore {
			continue
		}
		rerankCandidates = append(rerankCandidates, candidate)
		if len(rerankCandidates) == policy.Rerank.TopK {
			break
		}
	}
	if len(rerankCandidates) < 2 {
		return false
	}

	timeout := time.Duration(policy.Rerank.TimeoutMs) * time.Millisecond
	rerankCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := s.RAGFlow.ChatToModel(
		rerankCtx, policy.Rerank.ProviderName, policy.Rerank.InstanceName, policy.Rerank.ModelName,
		routeRerankPrompt(query, rerankCandidates), false, false,
	)
	if err != nil {
		return false
	}
	ordered, ok := decodeRouteRerank(raw, rerankCandidates)
	if !ok {
		return false
	}
	total := float64(len(ordered))
	for index := range ordered {
		rankScore := (total - float64(index)) / total
		base := ordered[index].BaseScore
		ordered[index].BaseScore = 0.7*base + 0.3*rankScore
		ordered[index].NormalizedScore = routeNormalizedScore(ordered[index].BaseScore)
	}
	reranked := make([]RouteCandidate, 0, len(candidates))
	reranked = append(reranked, ordered...)
	used := make(map[string]bool, len(ordered))
	for _, item := range ordered {
		used[item.Kind+":"+item.TargetID] = true
	}
	for _, candidate := range candidates {
		if !used[candidate.Kind+":"+candidate.TargetID] {
			reranked = append(reranked, candidate)
		}
	}
	copy(candidates, reranked)
	return true
}

func routeRerankPrompt(query string, candidates []RouteCandidate) string {
	var builder strings.Builder
	builder.WriteString("Return strict JSON only: {\"ranking\":[\"<candidate_id>\"]}. ")
	builder.WriteString("Rank every candidate id exactly once for the question.\nQuestion: ")
	builder.WriteString(query)
	builder.WriteString("\nCandidates:\n")
	for _, candidate := range candidates {
		builder.WriteString("- ")
		builder.WriteString(candidate.TargetID)
		builder.WriteString(": ")
		builder.WriteString(candidate.Name)
		builder.WriteString("\n")
	}
	return builder.String()
}

func decodeRouteRerank(raw string, candidates []RouteCandidate) ([]RouteCandidate, bool) {
	var payload struct {
		Ranking []string `json:"ranking"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err != nil {
		return nil, false
	}
	if len(payload.Ranking) != len(candidates) {
		return nil, false
	}
	byID := make(map[string]RouteCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.TargetID] = candidate
	}
	ordered := make([]RouteCandidate, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, targetID := range payload.Ranking {
		candidate, found := byID[targetID]
		if !found || seen[targetID] {
			return nil, false
		}
		seen[targetID] = true
		ordered = append(ordered, candidate)
	}
	return ordered, true
}

func queryLengthFeature(query string) float64 {
	if length := float64(len([]rune(query))) / 100; length < 1 {
		return length
	}
	return 1
}

func catalogFreshnessSeconds(updatedAt string) int64 {
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return -1
	}
	return int64(time.Since(parsed).Seconds())
}

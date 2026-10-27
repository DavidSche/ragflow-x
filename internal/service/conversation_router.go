package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const (
	conversationRouterVersion = "v1-m3"
	queryFingerprintVersion   = "hmac-v1"
	routeTTL                  = 10 * time.Minute
	maxRouteQueryLength       = 4000
)

type RouteRequest struct {
	Query         string   `json:"query"`
	IncludeKinds  []string `json:"include_kinds"`
	RequestedMode string   `json:"requested_mode"`
	DisplayLimit  int      `json:"display_limit"`
}

type RouteCandidate struct {
	Kind                 string   `json:"kind"`
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	NormalizedScore      float64  `json:"normalized_score"`
	NormalizedMargin     *float64 `json:"normalized_margin"`
	Confidence           *float64 `json:"confidence"`
	ConfidenceStatus     string   `json:"confidence_status"`
	ConfidenceMargin     *float64 `json:"confidence_margin"`
	CandidateCount       int      `json:"candidate_count"`
	HasCompetitor        bool     `json:"has_competitor"`
	RoutingReadiness     float64  `json:"routing_readiness"`
	RoutingReadinessType string   `json:"routing_readiness_type"`
	AgentFlowReadiness   float64  `json:"agent_flow_readiness"`
	PreExecutionRisk     string   `json:"pre_execution_risk"`
	AutoSelectEnabled    bool     `json:"auto_select_enabled"`
	CatalogFreshnessSec  int64    `json:"catalog_freshness_sec"`
	Reasons              []string `json:"reasons"`
	TargetID             string   `json:"target_id"`
	CatalogVersion       int64    `json:"catalog_version"`
	BaseScore            float64  `json:"-"`
}

type RouteDecisionResponse struct {
	RouteID        string           `json:"route_id"`
	ScoreStatus    string           `json:"score_status"`
	CandidateCount int              `json:"candidate_count"`
	RequestedMode  string           `json:"requested_mode"`
	EffectiveMode  string           `json:"effective_mode"`
	Selected       *RouteCandidate  `json:"selected"`
	Candidates     []RouteCandidate `json:"candidates"`
	ExpiresAt      string           `json:"expires_at"`
	RouterVersion  string           `json:"router_version"`
	PolicyMode     string           `json:"policy_mode"`
	PolicyVersion  string           `json:"policy_version"`
	RerankStatus   string           `json:"rerank_status"`
	RouteBudgetMs  int              `json:"route_budget_ms"`
	BudgetExceeded bool             `json:"budget_exceeded"`
	LatencyMs      int64            `json:"latency_ms"`
}

type routeDecisionSnapshot struct {
	Candidates       []RouteCandidate `json:"candidates"`
	QueryFingerprint string           `json:"query_fingerprint"`
	PolicyVersion    string           `json:"policy_version"`
}

// RouteConversation applies the M3 controlled policy. It can auto-select a
// low-risk calibrated candidate, but never executes a tool or creates a
// session by itself.
func (s *Service) RouteConversation(ctx context.Context, actorID, tenantID string, request RouteRequest) (*RouteDecisionResponse, error) {
	started := time.Now()
	now := time.Now().UTC()
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return nil, httperr.BadRequest(40091, "query is required")
	}
	if len(query) > maxRouteQueryLength {
		return nil, httperr.BadRequest(40092, "query is too long")
	}
	requestedKinds := normalizeConversationKinds(request.IncludeKinds)
	requestedMode := "suggest"
	if request.RequestedMode != "" {
		if request.RequestedMode != "suggest" && request.RequestedMode != "auto-request" {
			return nil, httperr.BadRequest(40093, "invalid requested_mode")
		}
		requestedMode = request.RequestedMode
	}
	effectivePolicy := normalizeRoutePolicy(s.RoutePolicy)
	routeBudgetCtx, cancelRouteBudget := context.WithTimeout(
		ctx, time.Duration(effectivePolicy.TotalTimeoutMs)*time.Millisecond)
	defer cancelRouteBudget()
	candidates, err := s.routeCandidates(routeBudgetCtx, actorID, tenantID, query, requestedKinds)
	if err != nil {
		return nil, err
	}
	policy, err := s.loadRoutePolicy(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rerankStatus := "not_applicable"
	if rerankAllowed(normalizeRoutePolicy(s.RoutePolicy), candidates) {
		if s.rerankRouteCandidates(routeBudgetCtx, normalizeRoutePolicy(s.RoutePolicy), query, candidates) {
			rerankStatus = "applied"
		} else {
			rerankStatus = "degraded"
		}
	}
	policy.RerankStatus = rerankStatus
	applyRouteCompetitors(candidates)
	if policy.Calibrated && policy.Mode == RouteModeAutoLowRisk {
		applyRouteCalibration(candidates, policy.Calibration, query)
	}
	effectivePolicy.Mode = policy.Mode
	autoCandidate := routeAutoCandidate(candidates, effectivePolicy)
	if autoCandidate == nil && requestedMode == "auto-request" {
		if err := recordRouteAudit(ctx, s, tenantID, actorID, "", "conversation.route.policy_denied", map[string]interface{}{
			"query_fingerprint": s.QueryFingerprint(query), "query_fingerprint_version": queryFingerprintVersion,
			"candidate_count": len(candidates), "policy_version": policy.PolicyVersion,
		}); err != nil {
			return nil, err
		}
	}
	if autoCandidate != nil {
		selected := *autoCandidate
		if err := recordRouteAudit(ctx, s, tenantID, actorID, selected.TargetID, "conversation.route.auto_selected", map[string]interface{}{
			"candidate_kind": selected.Kind, "target_id": selected.TargetID,
			"catalog_version": selected.CatalogVersion, "policy_version": policy.PolicyVersion,
		}); err != nil {
			return nil, err
		}
	}
	displayLimit := request.DisplayLimit
	responseTopK := normalizeRoutePolicy(s.RoutePolicy).ResponseTopK
	if responseTopK <= 0 {
		responseTopK = 3
	}
	if displayLimit < 1 || displayLimit > responseTopK {
		displayLimit = responseTopK
	}
	budgetExceeded := time.Since(started) > time.Duration(effectivePolicy.TotalTimeoutMs)*time.Millisecond
	candidateCount := len(candidates)
	if len(candidates) > displayLimit {
		candidates = candidates[:displayLimit]
	}
	scoreStatus := "provisional"
	if policy.Calibrated && policy.Mode == RouteModeAutoLowRisk {
		scoreStatus = "calibrated"
	}
	response := &RouteDecisionResponse{
		RouteID:        id.New(),
		ScoreStatus:    scoreStatus,
		CandidateCount: candidateCount,
		RequestedMode:  requestedMode,
		EffectiveMode:  policy.EffectiveMode,
		Candidates:     candidates,
		ExpiresAt:      now.Add(routeTTL).Format(time.RFC3339Nano),
		RouterVersion:  conversationRouterVersion,
		PolicyMode:     policy.Mode,
		PolicyVersion:  policy.PolicyVersion,
		RerankStatus:   rerankStatus,
		RouteBudgetMs:  effectivePolicy.TotalTimeoutMs,
		BudgetExceeded: budgetExceeded,
		LatencyMs:      time.Since(started).Milliseconds(),
	}
	if autoCandidate != nil && len(candidates) > 0 && autoCandidate.CatalogVersion == candidates[0].CatalogVersion {
		selected := *autoCandidate
		response.Selected = &selected
	}
	auditDetail := map[string]interface{}{
		"query_fingerprint":         s.QueryFingerprint(query),
		"query_fingerprint_version": queryFingerprintVersion,
		"query_length":              len(query),
		"candidate_count":           response.CandidateCount,
		"requested_mode":            requestedMode,
		"effective_mode":            response.EffectiveMode,
		"router_version":            conversationRouterVersion,
		"policy_version":            policy.PolicyVersion,
		"rerank_status":             rerankStatus,
		"budget_exceeded":           budgetExceeded,
	}
	if err := recordRouteAudit(ctx, s, tenantID, actorID, "", "conversation.route.requested", auditDetail); err != nil {
		return nil, err
	}
	if err := s.Store.CreateRouteDecision(ctx, &model.RouteDecision{
		ID: response.RouteID, TenantID: tenantID, UserID: actorID,
		QueryFingerprint: s.QueryFingerprint(query), QueryFingerprintVersion: queryFingerprintVersion,
		QueryLength: len(query), CandidatesJSON: encodeRouteJSON(routeDecisionSnapshot{
			Candidates: candidates, QueryFingerprint: s.QueryFingerprint(query), PolicyVersion: policy.PolicyVersion,
		}),
		RouterVersion: conversationRouterVersion, EffectiveMode: response.EffectiveMode,
		ExpiresAt: now.Add(routeTTL), CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if s.RouteState != nil {
		data, err := json.Marshal(model.RouteDecision{
			ID: response.RouteID, TenantID: tenantID, UserID: actorID,
			QueryFingerprint: s.QueryFingerprint(query), QueryFingerprintVersion: queryFingerprintVersion,
			QueryLength: len(query), CandidatesJSON: encodeRouteJSON(routeDecisionSnapshot{
				Candidates: response.Candidates, QueryFingerprint: s.QueryFingerprint(query), PolicyVersion: policy.PolicyVersion,
			}),
			RouterVersion: conversationRouterVersion, EffectiveMode: response.EffectiveMode,
			ExpiresAt: now.Add(routeTTL), CreatedAt: now,
		})
		if err != nil {
			return nil, err
		}
		if err := s.RouteState.Save(ctx, decisionStateKey(tenantID, response.RouteID), data, routeTTL); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func decisionStateKey(tenantID, routeID string) string {
	return tenantID + ":" + routeID
}

func (s *Service) getRouteDecision(ctx context.Context, tenantID, userID, routeID string) (*model.RouteDecision, error) {
	if s.RouteState != nil {
		value, found, err := s.RouteState.Load(ctx, decisionStateKey(tenantID, routeID))
		if err != nil {
			return nil, err
		}
		if found {
			var decision model.RouteDecision
			if err := json.Unmarshal(value, &decision); err != nil {
				return nil, httperr.Internal("route decision state is invalid")
			}
			if decision.TenantID != tenantID || decision.UserID != userID {
				return nil, httperr.NotFound("route decision not found")
			}
			return &decision, nil
		}
	}
	return s.Store.GetRouteDecision(ctx, tenantID, userID, routeID)
}

// QueryFingerprint returns a versioned non-reversible prompt fingerprint.
func (s *Service) QueryFingerprint(query string) string {
	mac := hmac.New(sha256.New, s.HMACKey)
	_, _ = mac.Write([]byte(query))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) routeCandidates(ctx context.Context, actorID, tenantID, query string, requestedKinds []string) ([]RouteCandidate, error) {
	catalog, _, err := s.ListConversationAssistants(ctx, actorID, tenantID, "", requestedKinds, 1, 100)
	if err != nil {
		return nil, err
	}
	if err := s.Authorize(ctx, actorID, "session:create", "agent"); err != nil {
		catalog = filterCatalogByKind(catalog, model.AssistantKindAgent)
	}
	return routeCandidatesFromCatalog(catalog, query, normalizeRoutePolicy(s.RoutePolicy).CandidateLimit), nil
}

func filterCatalogByKind(catalog []ConversationAssistant, kind string) []ConversationAssistant {
	filtered := make([]ConversationAssistant, 0, len(catalog))
	for _, item := range catalog {
		if item.Kind != kind {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func routeCandidatesFromCatalog(catalog []ConversationAssistant, query string, candidateLimit int) []RouteCandidate {
	queryTokens := conversationTokens(query)
	candidates := make([]RouteCandidate, 0, len(catalog))
	for _, item := range catalog {
		base, reasons := scoreRouteCandidate(query, queryTokens, item)
		if base <= 0 {
			continue
		}
		candidates = append(candidates, RouteCandidate{
			Kind: item.Kind, ID: item.ID, Name: item.Name,
			NormalizedScore:      routeNormalizedScore(base),
			ConfidenceStatus:     "not_calibrated",
			RoutingReadiness:     item.RoutingReadiness,
			RoutingReadinessType: model.RoutingReadinessTypeMetadataCompleteness,
			AgentFlowReadiness:   item.AgentFlowReadiness,
			PreExecutionRisk:     routePreExecutionRisk(item),
			AutoSelectEnabled:    item.AutoSelectEnabled,
			CatalogFreshnessSec:  catalogFreshnessSeconds(item.CatalogUpdatedAt),
			Reasons:              reasons,
			TargetID:             item.ID,
			CatalogVersion:       item.CatalogVersion,
			BaseScore:            base,
		})
	}
	if len(candidates) > 1 {
		for i := 1; i < len(candidates); i++ {
			for j := i; j > 0 && candidates[j-1].BaseScore < candidates[j].BaseScore; j-- {
				candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
			}
		}
	}
	if candidateLimit > 0 && len(candidates) > candidateLimit {
		candidates = candidates[:candidateLimit]
	}
	return candidates
}

func scoreRouteCandidate(query string, queryTokens []string, item ConversationAssistant) (float64, []string) {
	reasons := make([]string, 0, 4)
	lowerQuery := strings.ToLower(query)
	name := strings.ToLower(item.Name)
	description := strings.ToLower(item.Description)
	capability := 0.0
	keywordScore := 0.0
	intentScore := 0.0
	capabilityScore := 0.0
	lexical := 0.0
	switch {
	case name == lowerQuery || strings.Contains(name, lowerQuery):
		lexical = 1
		reasons = append(reasons, item.Name)
	case description != "" && strings.Contains(description, lowerQuery):
		lexical = 0.85
		reasons = append(reasons, "description")
	default:
		lexical = tokenOverlap(queryTokens, item.Name, 0.62)
		keywordScore = tokenOverlap(queryTokens, strings.Join(item.Keywords, " "), 1)
		if keywordScore > 0 {
			keywordMatches := keywordScore * float64(len(queryTokens))
			if len(queryTokens) > 1 && keywordMatches < 2 {
				keywordScore = 0
			}
		}
		if keywordScore > 0 {
			lexical = math.Max(lexical, keywordScore*0.95)
			reasons = append(reasons, item.Keywords...)
		}
		intentScore = tokenOverlap(queryTokens, strings.Join(item.Intents, " "), 0.92)
		if intentScore > 0 {
			lexical = math.Max(lexical, intentScore*0.9)
			reasons = append(reasons, item.Intents...)
		}
		capabilityScore = tokenOverlap(queryTokens, strings.Join(item.Capabilities, " "), 0.85)
		if capabilityScore > 0 {
			lexical = math.Max(lexical, capabilityScore*0.82)
			reasons = append(reasons, item.Capabilities...)
		}
	}
	if intentScore > 0 || capabilityScore > 0 {
		capability = 1
	}
	example := 0.0
	for _, candidate := range item.Examples {
		score := exampleScore(query, candidate)
		if score > 0 && len(queryTokens) > 1 && score*float64(len(queryTokens))/0.8 < 2 {
			continue
		}
		example = math.Max(example, score)
	}
	behavior := 0.0
	governance := 0.0
	if strings.EqualFold(item.Status, model.AssistantEffectiveActive) {
		governance = 1
	}
	if lexical == 0 && capability == 0 && example == 0 && behavior == 0 {
		return 0, nil
	}
	base := 0.35*lexical + 0.30*capability + 0.20*example + 0.10*behavior + 0.05*governance
	if base < 0.146 {
		// Weak lexical-only matches are not actionable routing evidence.
		// Returning no candidate lets the conversation center ask for context
		// instead of confidently suggesting an unrelated assistant.
		return 0, nil
	}
	return base, reasons
}

func applyRouteCompetitors(candidates []RouteCandidate) {
	for index := range candidates {
		candidate := &candidates[index]
		candidate.Confidence = nil
		candidate.ConfidenceMargin = nil
		candidate.CandidateCount = len(candidates)
		candidate.HasCompetitor = len(candidates) > 1
		if len(candidates) > 1 {
			margin := math.Max(0, candidates[0].NormalizedScore-candidates[1].NormalizedScore)
			candidate.NormalizedMargin = &margin
		}
	}
}

func routeNormalizedScore(base float64) float64 {
	// M2 uses a fixed absolute sigmoid mapping so a candidate's score does not
	// change when competitors enter or leave the route decision.
	return 1 / (1 + math.Exp(-6*(base-0.5)))
}

func routePreExecutionRisk(item ConversationAssistant) string {
	rank := map[string]int{model.AssistantRiskLow: 0, model.AssistantRiskMedium: 1, model.AssistantRiskHigh: 2}
	risk := rank[item.AssistantRiskLevel]
	name := item.AssistantRiskLevel
	for _, level := range append([]string{item.WorkflowRiskUpperBound}, valuesOf(item.CapabilityRisk)...) {
		if rank[level] > risk {
			risk = rank[level]
			name = level
		}
	}
	return name
}

func valuesOf(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func tokenOverlap(queryTokens []string, haystack string, weight float64) float64 {
	if haystack == "" || len(queryTokens) == 0 {
		return 0
	}
	haystackTokens := make(map[string]struct{}, len(queryTokens))
	for _, token := range conversationTokens(haystack) {
		haystackTokens[token] = struct{}{}
	}
	matched := 0
	for _, token := range queryTokens {
		if _, ok := haystackTokens[token]; ok {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	return weight * float64(matched) / float64(len(queryTokens))
}

func exampleScore(query, example string) float64 {
	lowerQuery, lowerExample := strings.ToLower(query), strings.ToLower(example)
	if lowerQuery == lowerExample || strings.Contains(lowerExample, lowerQuery) || strings.Contains(lowerQuery, lowerExample) {
		return 1
	}
	return tokenOverlap(conversationTokens(query), example, 0.8)
}

// conversationTokens preserves Latin/number words and CJK bigrams so mixed
// language enterprise queries produce stable deterministic features.
func conversationTokens(value string) []string {
	value = strings.ToLower(value)
	var latin []rune
	var cjk []rune
	tokens := []string{}
	flush := func() {
		if latin != nil {
			tokens = append(tokens, string(latin))
			latin = nil
		}
	}
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) && isHan(r):
			flush()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			latin = append(latin, r)
		default:
			flush()
		}
	}
	flush()
	for i := 0; i+1 < len(cjk); i++ {
		tokens = append(tokens, string(cjk[i:i+2]))
	}
	return tokens
}

func isHan(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

func normalizeConversationKinds(requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	allowed := map[string]struct{}{model.AssistantKindChat: {}, model.AssistantKindAgent: {}}
	kinds := make([]string, 0, len(requested))
	for _, kind := range requested {
		kind = strings.TrimSpace(strings.ToLower(kind))
		if _, ok := allowed[kind]; !ok {
			continue
		}
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func encodeRouteJSON(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeRouteSnapshot(value string) ([]RouteCandidate, error) {
	var snapshot routeDecisionSnapshot
	if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
		return nil, httperr.Internal("route decision snapshot is invalid")
	}
	return snapshot.Candidates, nil
}

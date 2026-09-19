package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/sseutil"
)

// ChatCompletionResult is a completed non-streaming gateway response.
type ChatCompletionResult struct {
	Status int
	Body   []byte
}

// ChatCompletion forwards an OpenAI-compatible chat/completions request to the
// routed provider and records metered usage on success.
func (s *Service) ChatCompletion(ctx context.Context, key *model.APIKey, rawReq []byte, requestID string) (*ChatCompletionResult, error) {
	route, provider, err := s.resolveRoute(ctx, key.TenantID, rawReq)
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, err
	}
	apiKey, err := crypto.Decrypt(s.EncryptKey, provider.APIKeyEnc)
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, httperr.New(502, 50220, "failed to decrypt provider credential")
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(rawReq))
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		notify.Emit(ctx, notify.Event{Title: "gateway provider request failed", Severity: "error", TenantID: key.TenantID, Resource: "chat", Type: "provider_error", ResourceID: provider.ID, Detail: err.Error()})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, httperr.New(502, 50221, "provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, httperr.New(502, 50222, "read provider response failed")
	}
	if resp.StatusCode >= 300 {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return &ChatCompletionResult{Status: resp.StatusCode, Body: body}, nil
	}
	s.recordUsage(ctx, key, route.ModelAlias, body, requestID)
	return &ChatCompletionResult{Status: resp.StatusCode, Body: body}, nil
}

// WriteStreamCompletion forwards a streaming request, passing the SSE stream
// through to w while scanning usage chunks to meter the request.
func (s *Service) WriteStreamCompletion(ctx context.Context, key *model.APIKey, rawReq []byte, w io.Writer, requestID string) (int, string, error) {
	route, provider, err := s.resolveRoute(ctx, key.TenantID, rawReq)
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", err
	}
	apiKey, err := crypto.Decrypt(s.EncryptKey, provider.APIKeyEnc)
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", httperr.New(502, 50220, "failed to decrypt provider credential")
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(rawReq))
	if err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := s.streamClient.Do(req)
	if err != nil {
		notify.Emit(ctx, notify.Event{Title: "gateway stream failed to connect", Severity: "error", TenantID: key.TenantID, Resource: "chat", Type: "provider_error", ResourceID: provider.ID, Detail: err.Error()})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", httperr.New(502, 50221, "provider request failed")
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/event-stream"
	}
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(w, resp.Body)
		return resp.StatusCode, ct, nil
	}

	br := bufio.NewReader(resp.Body)
	var tIn, tOut int64
	// Meter on every path, including client disconnects mid-stream, so partial
	// usage is never silently lost. Idempotent by request_id.
	defer func() {
		estimated := tIn+tOut == 0
		if estimated {
			tIn, tOut = estimateRequestTokens(rawReq), 64
		}
		_, _ = s.Store.RecordUsage(ctx, &model.QuotaUsage{
			TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
			Date: time.Now().UTC().Format("2006-01-02"), TokensIn: tIn, TokensOut: tOut, Requests: 1,
			Estimated: estimated, EstimationPolicyVersion: "v1",
		})
		s.writeCostMetric(ctx, key, route.ModelAlias, "chat", tIn, tOut, requestID, estimated)
		s.FinalizeGatewayQuota(ctx, key, requestID, tIn+tOut)
	}()
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if _, werr := io.WriteString(w, line); werr != nil {
				return 0, ct, werr
			}
			sseutil.TrackUsage(line, &tIn, &tOut)
			if strings.TrimSpace(line) == "data: [DONE]" {
				break
			}
		}
		if err != nil {
			break
		}
	}
	return 200, ct, nil
}

func (s *Service) resolveRoute(ctx context.Context, tenantID string, rawReq []byte) (*model.ModelRoute, *model.ModelProvider, error) {
	var reqBody struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rawReq, &reqBody); err != nil {
		return nil, nil, httperr.BadRequest(40060, "invalid request body")
	}
	if reqBody.Model == "" {
		return nil, nil, httperr.BadRequest(40061, "model is required")
	}
	routes, err := s.Store.ListModelRoutes(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	var route *model.ModelRoute
	for i := range routes {
		if routes[i].Enabled && routes[i].ModelAlias == reqBody.Model {
			route = &routes[i]
			break
		}
	}
	if route == nil {
		return nil, nil, httperr.NotFound(fmt.Sprintf("no route for model %q", reqBody.Model))
	}
	if _, err := s.ValidateEnterpriseModelRoutePin(ctx, route); err != nil {
		return nil, nil, err
	}
	provider, err := s.Store.GetModelProvider(ctx, tenantID, route.ProviderID)
	if err != nil {
		return nil, nil, err
	}
	if provider == nil || !provider.Enabled {
		return nil, nil, httperr.NotFound("model provider not found or disabled")
	}
	return route, provider, nil
}

func (s *Service) recordUsage(ctx context.Context, key *model.APIKey, modelName string, body []byte, requestID string) {
	var resp struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &resp)
	tIn, tOut := int64(0), int64(0)
	if resp.Usage != nil {
		tIn, tOut = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	estimated := tIn+tOut == 0
	if estimated {
		tIn, tOut = estimateRequestTokens(body), 64
	}
	_, _ = s.Store.RecordUsage(ctx, &model.QuotaUsage{
		TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
		Date: time.Now().UTC().Format("2006-01-02"), TokensIn: tIn, TokensOut: tOut, Requests: 1,
		Estimated: estimated, EstimationPolicyVersion: "v1",
	})
	s.writeCostMetric(ctx, key, modelName, "chat", tIn, tOut, requestID, estimated)
	s.FinalizeGatewayQuota(ctx, key, requestID, tIn+tOut)
}

// AuthorizeGatewaySearchAppTarget ensures an API Key can execute only the
// Search App owned by its principal within the same tenant.
func (s *Service) AuthorizeGatewaySearchAppTarget(ctx context.Context, key *model.APIKey, searchAppID string) error {
	if key == nil {
		return httperr.Unauthorized("missing api key")
	}
	resource, err := s.Store.GetSearchAppShadow(ctx, key.TenantID, searchAppID, false)
	if err != nil {
		return err
	}
	if resource == nil || resource.OwnerID != key.UserID {
		return httperr.NotFound("search app not found")
	}
	return nil
}

// AuthorizeGatewayAgentTarget applies the same ownership boundary to Agent
// gateway execution before quota or provider work is performed.
func (s *Service) AuthorizeGatewayAgentTarget(ctx context.Context, key *model.APIKey, agentID string) error {
	if key == nil {
		return httperr.Unauthorized("missing api key")
	}
	resource, err := s.Store.GetAgentShadow(ctx, key.TenantID, agentID, false)
	if err != nil {
		return err
	}
	if resource == nil || resource.OwnerID != key.UserID {
		return httperr.NotFound("agent not found")
	}
	return nil
}

// writeCostMetric records the per-request metering detail row used for cost
// breakdown (C1). It is idempotent by request_id and independent from the
// daily aggregate.
func (s *Service) writeCostMetric(ctx context.Context, key *model.APIKey, modelName, scenario string, tIn, tOut int64, requestID string, estimated bool) {
	if _, err := s.Store.RecordCostMetric(ctx, &model.CostMetric{
		TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
		Date: time.Now().UTC().Format("2006-01-02"), Model: modelName, Scenario: scenario,
		TokensIn: tIn, TokensOut: tOut,
		EstimatedCost: (float64(tIn) + float64(tOut)) / 1000 * s.EstimatedCostPer1K,
		Estimated:     estimated, EstimationPolicyVersion: "v1",
	}); err != nil {
		logger.Warn("metering detail write failed", "tenant_id", key.TenantID, "user_id", key.UserID, "request_id", requestID, "error", err)
	}
}

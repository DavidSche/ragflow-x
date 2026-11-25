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
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, err
	}
	upstreamReq, err := rewriteModelForUpstream(rawReq, route)
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, err
	}
	apiKey, err := crypto.Decrypt(s.EncryptKey, provider.APIKeyEnc)
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, httperr.New(502, 50220, "failed to decrypt provider credential")
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(upstreamReq))
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		notify.Emit(ctx, notify.Event{Title: "gateway provider request failed", Severity: "error", TenantID: key.TenantID, Resource: "chat", Type: "provider_error", ResourceID: provider.ID, Detail: err.Error()})
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, httperr.New(502, 50221, "provider request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return nil, httperr.New(502, 50222, "read provider response failed")
	}
	if resp.StatusCode >= 300 {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
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
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return 0, "", err
	}
	upstreamReq, err := rewriteModelForUpstream(rawReq, route)
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return 0, "", err
	}
	apiKey, err := crypto.Decrypt(s.EncryptKey, provider.APIKeyEnc)
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return 0, "", httperr.New(502, 50220, "failed to decrypt provider credential")
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(upstreamReq))
	if err != nil {
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := s.streamClient.Do(req)
	if err != nil {
		notify.Emit(ctx, notify.Event{Title: "gateway stream failed to connect", Severity: "error", TenantID: key.TenantID, Resource: "chat", Type: "provider_error", ResourceID: provider.ID, Detail: err.Error()})
		s.ReleaseGatewayQuotaWithBucket(ctx, key, requestID)
		return 0, "", httperr.New(502, 50221, "provider request failed")
	}
	defer func() { _ = resp.Body.Close() }()
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
		s.meterGatewayModelRoute(ctx, key, route.ModelAlias, "chat", tIn, tOut, requestID, estimated)
		s.FinalizeGatewayQuotaWithBucket(ctx, key, requestID, tIn+tOut)
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

// countTopLevelKeys 数请求体顶层对象里某个键出现了几次。
//
// 用 `json.Decoder` 逐个读顶层键、再把值整体跳过，而不是 `map` 反序列化：后者
// 对重复键只会留下最后一个，正好把要判的情况吃掉。
func countTopLevelKeys(raw []byte, key string) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return 0, fmt.Errorf("request body is not a JSON object")
	}
	count := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return count, err
		}
		name, ok := keyTok.(string)
		if !ok {
			return count, fmt.Errorf("request body has a non-string key")
		}
		if name == key {
			count++
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return count, err
		}
	}
	return count, nil
}

// rewriteModelForUpstream replaces the `model` field the client sent with the
// route's target model, and only that field: every other member of the body is
// carried through as raw JSON so numbers, nulls and nested objects keep their
// exact spelling.
//
// Why (E91, 304 §4): the route decision belongs to the gateway configuration,
// not to the caller's request body. Before this, resolveRoute matched
// ModelAlias and then forwarded the client body verbatim, so TargetModel only
// participated in the enterprise pin check and never reached the provider — an
// alias that was not spelled exactly like the upstream model name died with
// the provider's own `404 model_not_found`, and a caller could pick any
// upstream model by writing it into `model`.
//
// A route without a target model is refused instead of falling back to the
// client's spelling: guessing here would silently restore the old behaviour
// (502/50223, non-retryable, re-save the route).
func rewriteModelForUpstream(rawReq []byte, route *model.ModelRoute) ([]byte, error) {
	// 空白 target 与缺失一样是「路由没说要跑哪个模型」：`"  "` 会被原样写进
	// 请求体，上游再以 400 或 404 的形式把它变成一次难排查的失败。
	if route == nil || strings.TrimSpace(route.TargetModel) == "" {
		return nil, httperr.New(502, 50223, "model route has no target model")
	}
	// 重复键：`{"model":"a","model":"b"}` 在标准库里是「后者胜」——静默选一个，
	// 于是「跑哪个模型」取决于键的书写顺序，而密钥/审计看到的是另一个。
	// 这里不猜：数与 1 不等就拒绝（规范化会把歧义变成不报错的行为差异）。
	modelKeys, err := countTopLevelKeys(rawReq, "model")
	if err != nil {
		return nil, httperr.BadRequest(40060, "invalid request body")
	}
	if modelKeys != 1 {
		return nil, httperr.BadRequest(40060, "request body must contain exactly one top-level model field")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawReq, &fields); err != nil {
		return nil, httperr.BadRequest(40060, "invalid request body")
	}
	encoded, err := json.Marshal(route.TargetModel)
	if err != nil {
		return nil, httperr.New(502, 50223, "model route has no target model")
	}
	fields["model"] = encoded
	upstreamReq, err := json.Marshal(fields)
	if err != nil {
		return nil, httperr.BadRequest(40060, "invalid request body")
	}
	if route.ModelAlias != route.TargetModel {
		logger.Info("gateway model route rewrote upstream model",
			"route_id", route.ID, "client_model", route.ModelAlias, "upstream_model", route.TargetModel)
	}
	return upstreamReq, nil
}

func (s *Service) resolveRoute(ctx context.Context, tenantID string, rawReq []byte) (*model.ModelRoute, *model.ModelProvider, error) {
	var reqBody struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rawReq, &reqBody); err != nil {
		return nil, nil, httperr.BadRequest(40060, "invalid request body")
	}
	// 重复的顶层 `model` 必须在选路由**之前**就拒掉：`map` 反序列化只留下最后
	// 一个，于是路由按「b」选中、审计看到「a」，两边都自洽却指向不同模型。
	// 放在这里而不是只放在转发前，是因为选错路由就已经是一次错误的分发。
	if modelKeys, err := countTopLevelKeys(rawReq, "model"); err != nil {
		return nil, nil, httperr.BadRequest(40060, "invalid request body")
	} else if modelKeys > 1 {
		return nil, nil, httperr.BadRequest(40060, "request body must contain exactly one top-level model field")
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
	s.meterGatewayModelRoute(ctx, key, modelName, "chat", tIn, tOut, requestID, estimated)
	s.FinalizeGatewayQuotaWithBucket(ctx, key, requestID, tIn+tOut)
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

// meterGatewayModelRoute records a model-route gateway request (streaming or not)
// through GatewayMetering, so the aggregate and the cost detail are written by the
// same path that persists the intent. It replaced a pair of fire-and-forget writes
// whose failures only reached a log line (doc/304 §4 E146/E147).
func (s *Service) meterGatewayModelRoute(ctx context.Context, key *model.APIKey, modelName, scenario string, tIn, tOut int64, requestID string, estimated bool) {
	s.GatewayMetering(ctx, &model.MeteringIntent{
		TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
		Date: time.Now().UTC().Format("2006-01-02"), TokensIn: tIn, TokensOut: tOut, Requests: 1,
		Model: modelName, Scenario: scenario,
		EstimatedCost: (float64(tIn) + float64(tOut)) / 1000 * s.EstimatedCostPer1K,
		Estimated:     estimated,
	})
}

// Package contract freezes the customer-facing contract of the RAGFlow-X
// gateway (`/v1/*`, API-key authenticated). Doc/227 WS3 (C3.1/C3.2) requires two
// reviewable facts before v0.4.0 ships the gateway as "1.0": every managed route
// carries a stability label, and every business error the gateway can emit is
// classified (status, code, retryability, audit action, operator hint).
//
// The registries below are code-resident on purpose: they are the review
// surface, and the contract tests fail CI when a route or a code is added
// without classification. They are read-only data, not behaviour - the gateway
// itself is unchanged by this package.
package contract

import (
	"fmt"
	"strings"
)

// Version identifies the contract revision. Bump it when a route's stability
// changes or when a response/error semantic changes in a way clients can
// observe; the version is quoted by the release evidence package (doc/227 §7.3)
// and by the developer onboarding material (doc/228 T7.6).
const Version = "gateway.v1"

// Stability classifies how far a gateway route can be relied on.
type Stability string

const (
	// StabilityStable means the route, its request fields and its error
	// semantics are frozen for the contract version. Breaking it requires a
	// version bump plus a deprecation window.
	StabilityStable Stability = "stable"
	// StabilityExperimental means the route is usable in production but its
	// shape may follow upstream changes without a deprecation window. Callers
	// must not build irreversible automation on it.
	StabilityExperimental Stability = "experimental"
	// StabilityDeprecated means the route keeps working only until the
	// announced removal; Successor names the replacement.
	StabilityDeprecated Stability = "deprecated"
)

// API is one gateway route exposed to API-key clients.
type API struct {
	// ID is the stable identifier used by docs, audit and the onboarding
	// material (e.g. "chat.completions").
	ID string
	// Method and Path are the externally visible route. Path uses gin's
	// `:param` convention.
	Method string
	Path   string
	// Stability is the label required by doc/227 C3.1.
	Stability Stability
	// Capability is the RAGFlow-X capability this route consumes; it matches
	// the Capability Matrix keys so a capability regression maps to routes.
	Capability string
	// Streaming reports whether `stream:true` is supported (SSE pass-through).
	Streaming bool
	// Idempotent reports whether the route honours the `Idempotency-Key`
	// header through the gateway retry ledger.
	Idempotent bool
	// QuotaScopes lists the API-key scope dimensions checked for the route.
	QuotaScopes []string
	// Upstream lists the upstream RAGFlow endpoints the route can reach, as
	// "METHOD path" in registry form (internal/provider/ragflow). The contract
	// test asserts each entry is registered, so an upstream-path edit cannot
	// silently detach the gateway from its anchor.
	Upstream []string
	// Successor names the replacement route for deprecated entries.
	Successor string
	// Evidence anchors the classification to code and docs.
	Evidence string
}

var gatewayAPIs = []API{
	{
		ID: "chat.completions", Method: "POST", Path: "/v1/chat/completions",
		Stability: StabilityStable, Capability: "chat", Streaming: true, Idempotent: true,
		QuotaScopes: []string{"routes:chat", "models", "chat-apps"},
		Upstream:    []string{"POST /chat/completions"},
		Evidence:    "internal/handler/gateway.go#GatewayChat; internal/service/gateway.go#ChatCompletion; doc/227 §4.3 C3.1",
	},
	{
		ID: "search-apps.completions", Method: "POST", Path: "/v1/search-apps/:id/completions",
		Stability: StabilityStable, Capability: "search_app", Streaming: true, Idempotent: true,
		QuotaScopes: []string{"routes:search-app"},
		Upstream:    []string{"POST /searches/{search_id}/completions"},
		Evidence:    "internal/handler/gateway.go#GatewaySearchApp; internal/service/search_app.go#SearchAppCompletion; doc/227 §4.3 C3.1",
	},
	{
		ID: "agents.completions", Method: "POST", Path: "/v1/agents/:id/completions",
		Stability: StabilityExperimental, Capability: "agent", Streaming: true, Idempotent: true,
		QuotaScopes: []string{"routes:agent"},
		Upstream:    []string{"POST /agents/chat/completions"},
		Evidence:    "internal/handler/gateway.go#GatewayAgent; internal/service/agent.go#AgentChatCompletion; upstream Agentic-mode alias still settling (doc/227 C1.2/C1.5)",
	},
}

// APIs returns a copy of the gateway route registry.
func APIs() []API {
	out := make([]API, len(gatewayAPIs))
	copy(out, gatewayAPIs)
	return out
}

// APIByPath resolves a concrete request path (or a gin route pattern) to its
// contract entry. Parameters are ignored, so both "/v1/agents/a1/completions"
// and "/v1/agents/:id/completions" resolve.
func APIByPath(method, path string) (API, bool) {
	for _, api := range gatewayAPIs {
		if api.Method == method && pathMatches(api.Path, path) {
			return api, true
		}
	}
	return API{}, false
}

// ErrorCategory groups gateway errors by the operator action they imply.
type ErrorCategory string

const (
	// CategoryAuth: the credential itself is missing, invalid or expired.
	CategoryAuth ErrorCategory = "auth"
	// CategoryAuthorization: the credential is valid but not allowed to do this
	// (scope, IP allowlist, workspace state, RBAC).
	CategoryAuthorization ErrorCategory = "authorization"
	// CategoryValidation: the request body or its parameters are unusable.
	CategoryValidation ErrorCategory = "validation"
	// CategoryQuota: a budget was exhausted (rate window or token quota).
	CategoryQuota ErrorCategory = "quota"
	// CategoryIdempotency: the retry ledger rejected or replayed the request.
	CategoryIdempotency ErrorCategory = "idempotency"
	// CategoryUpstream: the provider/RAGFlow call failed on our side.
	CategoryUpstream ErrorCategory = "upstream"
	// CategoryUnavailable: a platform dependency is degraded.
	CategoryUnavailable ErrorCategory = "unavailable"
	// CategoryUpstreamPassThrough marks responses that forward the upstream
	// status and envelope verbatim (any upstream 4xx/5xx). They are classified
	// as a family instead of per-code so a new upstream code is not silently
	// mislabelled as ours.
	CategoryUpstreamPassThrough ErrorCategory = "upstream_passthrough"
)

// ErrorContract is one classified gateway error.
type ErrorContract struct {
	// Status is the HTTP status code returned to the caller.
	Status int
	// Code is the business code in the response envelope.
	Code int
	// Category drives the operator hint and the retry policy.
	Category ErrorCategory
	// Retryable reports whether the same request (with the same
	// Idempotency-Key, when used) may be retried automatically.
	Retryable bool
	// AuditActions lists the audit actions that can accompany this error
	// (empty = not audited; credential failures are counted, not attributed).
	AuditActions []string
	// Hint is the operator-facing guidance rendered by the developer docs
	// (doc/228 T7.6). English only: this is an API surface, not UI copy.
	Hint string
	// Evidence anchors the entry.
	Evidence string
}

var gatewayErrors = []ErrorContract{
	{Status: 400, Code: 40000, Category: CategoryValidation, Hint: "Request body is not JSON or violates the route schema. Do not retry without fixing the payload.", Evidence: "internal/handler/gateway.go#GatewayChat"},
	{Status: 400, Code: 40042, Category: CategoryValidation, Hint: "API key scope dimension is unknown. Fix the key definition; retrying cannot help.", Evidence: "internal/service/api_key_scope.go#AuthorizeAPIKeyScope"},
	{Status: 400, Code: 40060, Category: CategoryValidation, Hint: "Model-route request body is invalid JSON.", Evidence: "internal/service/gateway.go#resolveRoute"},
	{Status: 400, Code: 40061, Category: CategoryValidation, Hint: "Model-route request is missing `model`.", Evidence: "internal/service/gateway.go#resolveRoute"},
	{Status: 400, Code: 40094, Category: CategoryValidation, Hint: "Search App request is missing `question`.", Evidence: "internal/service/search_app.go#SearchAppCompletion"},
	{Status: 401, Code: 401, Category: CategoryAuth, Hint: "Bearer key is missing, unknown, disabled or expired. Re-issue the key; do not retry the old one.", Evidence: "internal/service/keys.go#ResolveAPIKey"},
	{Status: 403, Code: 403, Category: CategoryAuthorization, AuditActions: []string{"gateway.ip_denied", "gateway.denied"}, Hint: "Key is valid but the workspace is disabled, the source IP is not in the key allowlist, the key scope does not cover the target, or the principal lost RBAC. Fix the authorization; retrying cannot help.", Evidence: "internal/service/keys.go#ResolveAPIKey; internal/service/api_key_ip.go#AuthorizeAPIKeyIP; internal/service/api_key_scope.go#AuthorizeAPIKeyScope"},
	{Status: 404, Code: 404, Category: CategoryValidation, Hint: "Chat/assistant, session, search app or model route does not exist in this tenant (cross-tenant reads answer 404, never 403).", Evidence: "internal/service/chat_app.go#AuthorizeChatTarget; internal/service/gateway.go#resolveRoute"},
	{Status: 409, Code: 40980, Category: CategoryIdempotency, Hint: "Idempotency-Key was reused with a different request body/target. Use a fresh key per distinct request.", Evidence: "internal/service/gateway_idempotency.go#BeginGatewayIdempotency"},
	{Status: 409, Code: 40981, Category: CategoryIdempotency, Retryable: true, Hint: "The original request with this Idempotency-Key is still running. Retry after a short backoff.", Evidence: "internal/handler/gateway.go#gatewayIdempotencyReplay"},
	{Status: 409, Code: 40982, Category: CategoryIdempotency, Retryable: true, Hint: "The retry-ledger record for this Idempotency-Key expired (24h). Re-send with a new key.", Evidence: "internal/handler/gateway.go#gatewayIdempotencyReplay"},
	{Status: 429, Code: 429, Category: CategoryQuota, Retryable: true, Hint: "Per-key request-rate window exhausted (`Gateway.RateLimitPerMin`). Honour Retry-After and back off.", Evidence: "internal/middleware/ratelimit.go#rateLimit"},
	{Status: 429, Code: 42930, Category: CategoryQuota, AuditActions: []string{"gateway.quota_exceeded"}, Hint: "Monthly token quota for the key/tenant is exhausted. Raise the quota; retrying cannot help.", Evidence: "internal/service/gateway_quota_bucket.go#PreauthorizeGatewayQuotaWithBucket; internal/pkg/httperr/httperr.go#QuotaExceeded"},
	{Status: 502, Code: 50220, Category: CategoryUpstream, Hint: "Stored provider credential cannot be decrypted (key rotation or corrupted row). Re-save the provider.", Evidence: "internal/service/gateway.go#ChatCompletion"},
	{Status: 502, Code: 50231, Category: CategoryUpstream, Retryable: true, Hint: "Chat assistant stream failed after the 200 text/event-stream response was committed. The client always receives exactly one terminal SSE signal: the upstream error frame verbatim when the provider reported the failure in-band, otherwise a gateway_error frame carrying this code. A failed stream releases the quota reservation and records a failed answer delivery; retry with backoff.", Evidence: "internal/service/chat_app.go#failChatAppStream; internal/handler/gateway.go#gatewayStreamError"},
	{Status: 502, Code: 50221, Category: CategoryUpstream, Retryable: true, Hint: "Provider request failed at transport level. Retry with backoff; check provider health if it persists.", Evidence: "internal/service/gateway.go#ChatCompletion"},
	{Status: 502, Code: 50222, Category: CategoryUpstream, Retryable: true, Hint: "Provider response could not be read. Retry; escalate if repeated.", Evidence: "internal/service/gateway.go#ChatCompletion"},
	{Status: 502, Code: 50232, Category: CategoryUpstream, Retryable: true, Hint: "Chat ownership lookup failed inside the platform. Retry; escalate with request_id if repeated.", Evidence: "internal/service/chat_app.go#AuthorizeChatTarget"},
	{Status: 502, Code: 50240, Category: CategoryUpstream, Retryable: true, Hint: "Gateway retry ledger is unavailable. Retry later; omit Idempotency-Key only if duplicates are acceptable.", Evidence: "internal/service/gateway_idempotency.go#BeginGatewayIdempotency"},
	{Status: 502, Code: 50241, Category: CategoryUpstream, Hint: "The replayed request previously failed; the original failure is returned instead of re-executing. Fix the cause and use a new Idempotency-Key.", Evidence: "internal/handler/gateway.go#gatewayIdempotencyReplay"},
	{Status: 502, Code: 50265, Category: CategoryUpstream, Retryable: true, Hint: "Search App completion failed upstream. Retry; check upstream /searches completion health.", Evidence: "internal/service/search_app.go#SearchAppCompletion"},
	{Status: 502, Code: 50289, Category: CategoryUpstream, Retryable: true, Hint: "Agent completion failed upstream (or returned no content). Retry; agent mode is experimental.", Evidence: "internal/service/agent.go#AgentChatCompletion"},
	{Status: 503, Code: 50300, Category: CategoryUnavailable, Retryable: true, Hint: "Distributed rate limiter is unavailable and strict mode rejects rather than fail-open. Retry after the platform recovers.", Evidence: "internal/middleware/ratelimit.go#rateLimit"},
}

// Errors returns a copy of the gateway error registry.
func Errors() []ErrorContract {
	out := make([]ErrorContract, len(gatewayErrors))
	copy(out, gatewayErrors)
	return out
}

// ErrorByCode resolves an emitted (status, code) pair to its contract entry.
func ErrorByCode(status, code int) (ErrorContract, bool) {
	for _, e := range gatewayErrors {
		if e.Status == status && e.Code == code {
			return e, true
		}
	}
	return ErrorContract{}, false
}

// Passthrough describes the one deliberate unclassified family: upstream
// 4xx/5xx responses on the chat model-route path are forwarded with the
// upstream status and body (internal/service/gateway.go). Clients must read the
// upstream envelope in that case; X adds no code of its own.
type Passthrough struct {
	Category    ErrorCategory
	StatusRange string
	Body        string
	Evidence    string
}

// StreamFailureClause documents the client-visible failure semantics of the
// streaming routes (/v1/chat/completions with stream=true, search-app and agent
// SSE). Doc/227 C3.3 asks for the "双拼" case explicitly: retrieval succeeded
// (content already on the wire) but generation failed.
type StreamFailureClause struct {
	Category  ErrorCategory
	Terminal  string
	InBand    string
	Transport string
	Billing   string
	Reconnect string
	Evidence  string
}

// StreamFailure returns the streaming-failure clause of the contract.
func StreamFailure() StreamFailureClause {
	return StreamFailureClause{
		Category:  CategoryUpstream,
		Terminal:  "exactly one terminal signal per stream: either the upstream error frame passed through verbatim or the gateway_error frame",
		InBand:    "upstream `data: {\"error\": ...}` on a committed 200: forwarded verbatim, never duplicated by a second gateway frame; audit gateway.stream_error, failed answer delivery, no metering",
		Transport: "connection drop or read error mid-stream: gateway appends `data: {\"error\": {...}}` with code 50231 and the upstream cause in the audit detail",
		Billing:   "a failed stream releases the gateway quota reservation and writes no usage row; do not charge for a partial answer",
		Reconnect: "no stream resume: a retry is a new request and a new answer snapshot, because RAGFlow rc1 exposes no resume token",
		Evidence:  "internal/service/gateway_stream_status.go#reportedStreamFailure; internal/service/chat_app.go#failChatAppStream; internal/service/search_app.go#StreamSearchAppCompletion; internal/service/agent.go#StreamAgentChatCompletion",
	}
}

// UpstreamPassthrough returns the pass-through clause of the contract.
func UpstreamPassthrough() Passthrough {
	return Passthrough{
		Category:    CategoryUpstreamPassThrough,
		StatusRange: ">=300",
		Body:        "upstream response body forwarded verbatim (no X envelope, no X code)",
		Evidence:    "internal/service/gateway.go#ChatCompletion; internal/service/gateway.go#WriteStreamCompletion",
	}
}

// pathMatches compares a gin route pattern with a concrete request path or the
// same pattern. A `:param` segment matches exactly one segment; everything else
// must be equal.
func pathMatches(pattern, path string) bool {
	patternSegments := strings.Split(pattern, "/")
	pathSegments := strings.Split(path, "/")
	if len(patternSegments) != len(pathSegments) {
		return false
	}
	for i, segment := range patternSegments {
		if strings.HasPrefix(segment, ":") {
			if pathSegments[i] == "" {
				return false
			}
			continue
		}
		if segment != pathSegments[i] {
			return false
		}
	}
	return true
}

// Describe renders one entry for the generated contract document, keeping the
// docs and the registry from drifting (doc/228 T7.6 uses the same text).
func (e ErrorContract) Describe() string {
	return fmt.Sprintf("%d/%d %s retryable=%t", e.Status, e.Code, e.Category, e.Retryable)
}

package contract

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestGatewayAPIInvariants pins the shape of the route registry so a new entry
// cannot be added half-classified (doc/227 §4.3 C3.1).
func TestGatewayAPIInvariants(t *testing.T) {
	if Version == "" {
		t.Fatal("contract version must be set")
	}
	seenPath := map[string]string{}
	seenID := map[string]bool{}
	for _, api := range APIs() {
		key := api.Method + " " + api.Path
		if prev, ok := seenPath[key]; ok {
			t.Fatalf("duplicate gateway route %s (%s and %s)", key, prev, api.ID)
		}
		seenPath[key] = api.ID
		if seenID[api.ID] {
			t.Fatalf("duplicate gateway route id %q", api.ID)
		}
		seenID[api.ID] = true

		switch api.Stability {
		case StabilityStable, StabilityExperimental, StabilityDeprecated:
		default:
			t.Errorf("%s: unknown stability %q", api.ID, api.Stability)
		}
		if api.Stability == StabilityDeprecated && api.Successor == "" {
			t.Errorf("%s: deprecated route must name its successor", api.ID)
		}
		if api.Capability == "" || api.Evidence == "" {
			t.Errorf("%s: capability and evidence are required", api.ID)
		}
		if len(api.Upstream) == 0 {
			t.Errorf("%s: upstream endpoints must be declared", api.ID)
		}
		for _, scope := range api.QuotaScopes {
			if scope == "" {
				t.Errorf("%s: empty quota scope", api.ID)
			}
		}
	}
}

// TestGatewayAPIsAnchorToRegisteredUpstreamEndpoints keeps the gateway contract
// attached to the provider endpoint registry: if an upstream path is renamed,
// this fails instead of the gateway silently pointing at nothing.
func TestGatewayAPIsAnchorToRegisteredUpstreamEndpoints(t *testing.T) {
	registered := map[string]bool{}
	for _, endpoint := range ragflow.EndpointRegistry() {
		registered[endpoint.Method+" "+endpoint.Path] = true
	}
	for _, api := range APIs() {
		for _, upstream := range api.Upstream {
			if !registered[upstream] {
				t.Errorf("%s: upstream %q is not in the provider endpoint registry (internal/provider/ragflow/endpoint_registry.go)", api.ID, upstream)
			}
		}
	}
}

// TestGatewayErrorInvariants pins the error contract (doc/227 §4.3 C3.2): unique
// pairs, a category per entry, retryability limited to the transient families,
// and English operator hints (this is an API surface, not UI copy).
func TestGatewayErrorInvariants(t *testing.T) {
	seen := map[string]bool{}
	knownActions := map[string]bool{"gateway.ip_denied": true, "gateway.denied": true, "gateway.quota_exceeded": true}
	for _, item := range Errors() {
		key := strconv.Itoa(item.Status) + "/" + strconv.Itoa(item.Code)
		if seen[key] {
			t.Fatalf("duplicate error contract %s", key)
		}
		seen[key] = true

		switch item.Status {
		case 400, 401, 403, 404, 409, 429, 502, 503:
		default:
			t.Errorf("%s: unexpected gateway status %d", key, item.Status)
		}
		switch item.Category {
		case CategoryAuth, CategoryAuthorization, CategoryValidation, CategoryQuota,
			CategoryIdempotency, CategoryUpstream, CategoryUnavailable, CategoryUpstreamPassThrough:
		default:
			t.Errorf("%s: unknown category %q", key, item.Category)
		}
		if item.Hint == "" || item.Evidence == "" {
			t.Errorf("%s: hint and evidence are required", key)
		}
		if containsCJK(item.Hint) {
			t.Errorf("%s: hint must be English (API surface); got %q", key, item.Hint)
		}
		for _, action := range item.AuditActions {
			if !knownActions[action] {
				t.Errorf("%s: audit action %q is not an emitted gateway action", key, action)
			}
		}
		if item.Retryable {
			switch item.Status {
			case 409, 429, 502, 503:
			default:
				t.Errorf("%s: status %d must not be retryable", key, item.Status)
			}
		}
	}
}

// TestGatewayErrorLookupRoundTrips guards the lookup used by the onboarding
// material and future /gateway/contract consumers.
func TestGatewayErrorLookupRoundTrips(t *testing.T) {
	for _, item := range Errors() {
		got, ok := ErrorByCode(item.Status, item.Code)
		if !ok || got.Code != item.Code || got.Category != item.Category {
			t.Fatalf("ErrorByCode(%d, %d) = %+v, %t", item.Status, item.Code, got, ok)
		}
	}
	if _, ok := ErrorByCode(200, 0); ok {
		t.Fatal("success must not resolve to an error contract")
	}
	if _, ok := ErrorByCode(418, 41800); ok {
		t.Fatal("unknown pair must not resolve")
	}
}

// TestGatewayAPIPathLookupAcceptsConcretePaths proves the lookup works for a
// real request path, which is what doc generation and runtime classification use.
func TestGatewayAPIPathLookupAcceptsConcretePaths(t *testing.T) {
	cases := []struct{ method, path, id string }{
		{"POST", "/v1/chat/completions", "chat.completions"},
		{"POST", "/v1/search-apps/sa-1/completions", "search-apps.completions"},
		{"POST", "/v1/agents/ag-1/completions", "agents.completions"},
	}
	for _, tc := range cases {
		api, ok := APIByPath(tc.method, tc.path)
		if !ok || api.ID != tc.id {
			t.Fatalf("APIByPath(%s, %s) = %+v, %t; want %s", tc.method, tc.path, api, ok, tc.id)
		}
	}
	if _, ok := APIByPath("GET", "/v1/chat/completions"); ok {
		t.Fatal("method must be part of the route identity")
	}
}

// TestGatewayErrorContractCoversHandlerGatewayCodes reads the gateway handler
// source and fails when it emits a (status, code) pair the contract does not
// classify. Doc/227 C3.2: "no unclassified error".
func TestGatewayErrorContractCoversHandlerGatewayCodes(t *testing.T) {
	raw, err := os.ReadFile("../../handler/gateway.go")
	if err != nil {
		t.Fatalf("read gateway handler: %v", err)
	}
	source := string(raw)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`response\.Fail\(c,\s*(\d{3}),\s*(\d+),`),
		regexp.MustCompile(`httperr\.New\((\d{3}),\s*(\d+),`),
	}
	checked := 0
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(source, -1) {
			status, _ := strconv.Atoi(match[1])
			code, _ := strconv.Atoi(match[2])
			checked++
			if _, ok := ErrorByCode(status, code); !ok {
				t.Errorf("gateway handler emits %d/%d with no error contract entry", status, code)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no error literals found in the gateway handler; the scan is stale")
	}
}

// TestUpstreamPassthroughClauseIsDocumented keeps the one intentional
// unclassified family visible (upstream >=300 is forwarded verbatim).
func TestUpstreamPassthroughClauseIsDocumented(t *testing.T) {
	clause := UpstreamPassthrough()
	if clause.Category != CategoryUpstreamPassThrough {
		t.Fatalf("unexpected category %q", clause.Category)
	}
	if clause.StatusRange == "" || clause.Body == "" || clause.Evidence == "" {
		t.Fatal("passthrough clause must document status range, body handling and evidence")
	}
	if !strings.Contains(clause.Evidence, "internal/service/gateway.go") {
		t.Fatalf("passthrough evidence must anchor to the forwarding code: %q", clause.Evidence)
	}
}

func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// TestGatewayErrorContractCoversStreamFunctions extends the unclassified-error
// guard to the streaming service entry points. A stream commits the HTTP status
// before the failure happens, so these codes can only travel in-band: leaving
// them unregistered would make the documented error catalogue incomplete in the
// one family clients cannot debug from the status code (doc/227 C3.3).
func TestGatewayErrorContractCoversStreamFunctions(t *testing.T) {
	targets := []struct {
		file  string
		funcs []string
	}{
		{"../../service/chat_app.go", []string{"StreamChatAppCompletion", "failChatAppStream"}},
		{"../../service/search_app.go", []string{"StreamSearchAppCompletion"}},
		{"../../service/agent.go", []string{"StreamAgentChatCompletion"}},
	}
	pattern := regexp.MustCompile(`httperr\.New\((\d{3}),\s*(\d+),`)
	checked := 0
	for _, target := range targets {
		raw, err := os.ReadFile(target.file)
		if err != nil {
			t.Fatalf("read %s: %v", target.file, err)
		}
		lines := strings.Split(string(raw), "\n")
		for _, fn := range target.funcs {
			body := extractGoFunc(t, target.file, lines, fn)
			for _, match := range pattern.FindAllStringSubmatch(body, -1) {
				status, _ := strconv.Atoi(match[1])
				code, _ := strconv.Atoi(match[2])
				checked++
				if _, ok := ErrorByCode(status, code); !ok {
					t.Errorf("%s#%s emits %d/%d with no error contract entry", target.file, fn, status, code)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no error literals found in the streaming service functions; the scan is stale")
	}
}

// extractGoFunc returns the source body of a top-level function, including its
// signature, by scanning until the closing brace in the first column.
func extractGoFunc(t *testing.T, file string, lines []string, name string) string {
	t.Helper()
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func ") && strings.Contains(line, name+"(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: function %s not found", file, name)
	}
	for i := start; i < len(lines); i++ {
		if lines[i] == "}" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatalf("%s: function %s has no closing brace", file, name)
	return ""
}

// TestStreamFailureClauseIsDocumented pins the streaming-failure contract: the
// clauses are what the release evidence package quotes, so an empty clause means
// the stream semantics silently regressed to undocumented behaviour.
func TestStreamFailureClauseIsDocumented(t *testing.T) {
	clause := StreamFailure()
	if clause.Category != CategoryUpstream {
		t.Fatalf("unexpected category %q", clause.Category)
	}
	fields := map[string]string{
		"terminal": clause.Terminal, "in-band": clause.InBand, "transport": clause.Transport,
		"billing": clause.Billing, "reconnect": clause.Reconnect, "evidence": clause.Evidence,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			t.Errorf("stream failure clause %q must be documented", name)
		}
		if containsCJK(value) {
			t.Errorf("stream failure clause %q must stay English: %q", name, value)
		}
	}
	for _, anchor := range []string{"gateway_stream_status.go", "chat_app.go", "search_app.go", "agent.go"} {
		if !strings.Contains(clause.Evidence, anchor) {
			t.Errorf("stream failure evidence must anchor to %s: %q", anchor, clause.Evidence)
		}
	}
}

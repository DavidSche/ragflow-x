package handler

// GW-1 header contract at the handler level (doc/301 §5.10).
//
// The stream-writer tests cover the governance *frame*; these cover the two headers.
// They are pinned per route because the routes resolve their key differently: the
// classic chat path resolves inline (its IP-denial audit carries the `chat` resource)
// while the two knowledge routes go through `resolveGatewayKey`. That difference is
// exactly how `/v1/chat/completions` came to answer without `X-RGX-Policy` at all —
// the header existed on two of the three routes, and a client cannot see which.
//
// The header is asserted on a *denied* request on purpose: a client whose first call
// is refused still has to learn which contract it was refused under.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/gateway/contract"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// gatewayHeaderHarness serves the three gateway routes with a real API key held by a
// principal that may not execute them, so every call stops right after key resolution.
type gatewayHeaderHarness struct {
	router *gin.Engine
	rawKey string
}

func newGatewayHeaderHarness(t *testing.T) *gatewayHeaderHarness {
	t.Helper()
	svc, _ := newReportService(t)
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "gateway-header")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	// `viewer` cannot execute any of the three routes, so the request ends at the
	// authorization step — after the key is resolved and the header is written.
	user, err := svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "gateway-header-viewer", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, _, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "gateway-header", nil, 0, 0, nil, nil)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	h := New(svc)
	router := gin.New()
	router.POST("/v1/chat/completions", h.GatewayChat)
	router.POST("/v1/search-apps/:id/completions", h.GatewaySearchApp)
	router.POST("/v1/agents/:id/completions", h.GatewayAgent)
	return &gatewayHeaderHarness{router: router, rawKey: raw}
}

func (fx *gatewayHeaderHarness) post(path string) *httptest.ResponseRecorder {
	body := strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Authorization", "Bearer "+fx.rawKey)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	fx.router.ServeHTTP(recorder, req)
	return recorder
}

// TestGatewayRoutesAdvertiseContractVersion: all three routes, including the one that
// resolves its key inline, answer with the same `X-RGX-Policy` value.
func TestGatewayRoutesAdvertiseContractVersion(t *testing.T) {
	fx := newGatewayHeaderHarness(t)
	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/search-apps/app-1/completions",
		"/v1/agents/agent-1/completions",
	} {
		recorder := fx.post(path)
		if got := recorder.Header().Get("X-RGX-Policy"); got != contract.Version {
			t.Fatalf("%s: X-RGX-Policy = %q, want %q", path, got, contract.Version)
		}
		// The request is refused (viewer), which is the point: the header is not a
		// reward for a successful call.
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403 for a viewer key", path, recorder.Code)
		}
		// `X-RGX-Degraded` is absent when nothing is degraded. A header present on every
		// response and almost always "false" stops being read.
		if got := recorder.Header().Get("X-RGX-Degraded"); got != "" {
			t.Fatalf("%s: X-RGX-Degraded = %q on a non-degraded refusal", path, got)
		}
	}
}

// TestGatewayDegradedHeaderOnlyWhenDegraded: the writer is conditional, so a block that
// is merely *empty* must not raise it. Empty means "nothing to say"; degraded means "we
// could not compute what we had to say".
func TestGatewayDegradedHeaderOnlyWhenDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		governance *service.GatewayGovernance
		wantHeader string
	}{
		{"nil", nil, ""},
		{"complete", &service.GatewayGovernance{Schema: service.GovernanceSchema}, ""},
		{"degraded", &service.GatewayGovernance{Schema: service.GovernanceSchema, Degraded: true,
			DegradedReason: "answerability disabled"}, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			setGatewayGovernanceHeaders(c, tc.governance)
			if got := recorder.Header().Get("X-RGX-Degraded"); got != tc.wantHeader {
				t.Fatalf("X-RGX-Degraded = %q, want %q", got, tc.wantHeader)
			}
		})
	}
}

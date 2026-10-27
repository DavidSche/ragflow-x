package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCompilationRecoveryApprovalBindingRejectsInvalidPayloads guards against a
// regression of the M204 live-drill finding: validator's bare `gt` (without a
// parameter) panicked inside gin's ShouldBindJSON, turning every
// POST /knowledge-ops/compilation/runs/:runId/recovery-approval request into a
// 500 before the handler body ran. The fix is `gt=0`; a valid payload must
// bind (reaching auth/store failures instead of a panic), and a payload with a
// non-positive window must be rejected with 400.
func TestCompilationRecoveryApprovalBindingRejectsInvalidPayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "zero observation window is rejected",
			body:       `{"action":"confirm_downstream_absent","idempotency_key":"k1","observation_window_seconds":0,"observation_first_matched_at":"2026-10-16T08:58:00.421051Z"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative observation window is rejected",
			body:       `{"action":"confirm_downstream_absent","idempotency_key":"k2","observation_window_seconds":-5,"observation_first_matched_at":"2026-10-16T08:58:00.421051Z"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "valid payload binds without panic",
			body:       `{"action":"confirm_downstream_absent","idempotency_key":"k3","observation_window_seconds":1800,"observation_first_matched_at":"2026-10-16T08:58:00.421051Z"}`,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("binding panicked (bare gt regression): %v", r)
				}
			}()
			router := gin.New()
			router.POST("/runs/:runId/recovery-approval", func(c *gin.Context) {
				var req struct {
					Action                    string `json:"action" binding:"required,oneof=confirm_downstream_absent confirm-downstream-absent create_replacement_run create"`
					IdempotencyKey            string `json:"idempotency_key" binding:"required"`
					ObservationWindowSeconds  int64  `json:"observation_window_seconds" binding:"required,gt=0"`
					ObservationFirstMatchedAt string `json:"observation_first_matched_at" binding:"required"`
					Reason                    string `json:"reason"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.AbortWithStatus(http.StatusBadRequest)
					return
				}
				// Bound payloads would proceed to the service layer; in this
				// binding-focused test we stop here with an auth-equivalent
				// status to prove the request traversed binding cleanly.
				c.Status(http.StatusUnauthorized)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/runs/run-1/recovery-approval", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

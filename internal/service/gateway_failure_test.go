package service

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioID: SC-GW-007 (doc/224 §2.5 T5.3 — failure classification table)
func TestClassifyGatewayFailureTable(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		disconnected bool
		wantClass    string
		wantTerminal bool
		wantRetry    bool
	}{
		{"provider 500 upstream", httperr.New(502, 50239, "upstream"), false, GatewayFailureUpstream, false, true},
		{"provider 503 upstream", httperr.New(503, 0, "unavailable"), false, GatewayFailureUpstream, false, true},
		{"deadline timeout", context.DeadlineExceeded, false, GatewayFailureTimeout, false, true},
		{"client disconnected", errors.New("sse closed"), true, GatewayFailureDisconnect, true, false},
		{"key quota 429", httperr.New(429, 42930, "quota"), false, GatewayFailureQuota, true, true},
		{"tenant bucket quota 430", httperr.New(430, gatewayBucketQuotaExhausted, "bucket"), false, GatewayFailureQuota, true, true},
		{"approval rate 429 approval", httperr.New(429, 42940, "rate"), false, GatewayFailureQuota, true, true},
		{"fact-guard approval 40996", httperr.New(409, 40996, "blocked"), false, GatewayFailureApproval, true, false},
		{"terminal business code 40132", httperr.New(409, 40132, "exists"), false, GatewayFailureQuota, true, false},
		{"nil no error", nil, false, GatewayFailureOther, true, false},
		{"EOF without disconnect flag", io.EOF, false, GatewayFailureUpstream, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyGatewayFailure(tc.err, tc.disconnected)
			if got.Class != tc.wantClass {
				t.Fatalf("want class %q, got %q", tc.wantClass, got.Class)
			}
			if got.Terminal != tc.wantTerminal {
				t.Fatalf("want terminal %v, got %v", tc.wantTerminal, got.Terminal)
			}
			if got.Retryable != tc.wantRetry {
				t.Fatalf("want retryable %v, got %v", tc.wantRetry, got.Retryable)
			}
		})
	}
}

package service

import (
	"context"
	"errors"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Gateway failure classes (doc/224 §2.5 T5.3). Every gateway terminal state —
// provider response, timeout, client/upstream disconnect, quota rejection or
// approval interception — maps into one of these five classes so metering,
// alerting and retry policy can be keyed on a stable vocabulary instead of
// bespoke per-branch checks.
const (
	GatewayFailureUpstream   = "upstream"   // provider returned a non-quota error
	GatewayFailureTimeout    = "timeout"    // request aborted by a deadline
	GatewayFailureDisconnect = "disconnect" // upstream or client closed mid-flight
	GatewayFailureQuota      = "quota"      // 429/430 budget exhaustion (any dimension)
	GatewayFailureApproval   = "approval"   // policy/approval gate intercepted the call
	GatewayFailureOther      = "other"      // uncategorized terminal state
)

// GatewayFailure is the classification of a single terminal gateway request.
// Terminal marks states that must not be replayed (the consumer already saw a
// definitive outcome or the connection is gone); Retryable marks states that
// may be retried safely given the idempotent request_id ledger.
type GatewayFailure struct {
	Class     string `json:"class"`
	Terminal  bool   `json:"terminal"`
	Retryable bool   `json:"retryable"`
}

// terminalGatewayErrorCodes are business error codes (application-level) that
// represent terminal, consumer-visible outcomes rather than transport faults.
// 429xx = budget (key/tenant/approval-rate), 409xx = policy intercept.
func isTerminalBusinessCode(code int) bool {
	switch code {
	case 429, 40996, 40960, 40131, 40132, 40133, 40141, 40142:
		return true
	}
	return code >= 42900 && code <= 42999
}

// ClassifyGatewayFailure maps a terminal outcome onto the failure vocabulary.
// disconnected short-circuits to disconnect (the caller is gone; neither a
// retry nor a metering mutation is useful). Deadline contexts and 504-style
// timeouts classify as timeout (retryable under the idempotent ledger). Quota
// codes (429/430) and approval intercepts (40996) are terminal business
// outcomes. Everything else that reached the provider classifies as upstream.
func ClassifyGatewayFailure(err error, disconnected bool) GatewayFailure {
	if disconnected {
		return GatewayFailure{Class: GatewayFailureDisconnect, Terminal: true, Retryable: false}
	}
	if err == nil {
		return GatewayFailure{Class: GatewayFailureOther, Terminal: true, Retryable: false}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return GatewayFailure{Class: GatewayFailureTimeout, Terminal: false, Retryable: true}
	}
	var herr *httperr.Error
	if errors.As(err, &herr) {
		if herr.Status == 429 || herr.Status == 430 {
			return GatewayFailure{Class: GatewayFailureQuota, Terminal: true, Retryable: true}
		}
		if herr.Code == 40996 {
			return GatewayFailure{Class: GatewayFailureApproval, Terminal: true, Retryable: false}
		}
		if isTerminalBusinessCode(herr.Code) && herr.Status >= 400 && herr.Status < 500 {
			return GatewayFailure{Class: GatewayFailureQuota, Terminal: true, Retryable: false}
		}
		if herr.Status >= 500 {
			return GatewayFailure{Class: GatewayFailureUpstream, Terminal: false, Retryable: true}
		}
	}
	// Unclassified transport error: treat conservatively as upstream fault.
	return GatewayFailure{Class: GatewayFailureUpstream, Terminal: false, Retryable: true}
}

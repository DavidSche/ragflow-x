package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Upstream business rejections (live drill 2026-11-13).
//
// RAGFlow answers a rejected request with HTTP 200 plus a non-zero envelope code,
// because the transport succeeded and the *engine* refused. Mapping that to 502
// tells the operator "the platform is down" and throws away the only actionable
// text in the response. Two live cases:
//
//	POST /chats     -> 200 {code:102, "the dataset ... doesn't own parsed file"}
//	POST /datasets  -> 200 {code:500, "model configuration unavailable: ..."}
//
// Both are 400-class answers about the request/environment, so they surface as
// 40085 with the engine's reason attached; real transport and 5xx failures stay
// 502 with the gateway code.
const upstreamReasonLimit = 200

// upstreamRejection extracts the engine's reason when the engine deliberately
// refused (HTTP 200 + code != 0).
func upstreamRejection(err error) (string, bool) {
	var upstream *ragflow.Error
	if !errors.As(err, &upstream) {
		return "", false
	}
	if upstream.HTTPStatus != 200 || upstream.Code == 0 {
		return "", false
	}
	reason := strings.TrimSpace(upstream.Message)
	if len(reason) > upstreamReasonLimit {
		reason = reason[:upstreamReasonLimit]
	}
	if reason == "" {
		reason = fmt.Sprintf("upstream code %d", upstream.Code)
	}
	return reason, true
}

// upstreamWriteFailure maps a write that the engine refused onto the caller's
// error contract: actionable reason as 400-class validation, otherwise 502.
func upstreamWriteFailure(err error, action string, gatewayCode int) error {
	if reason, ok := upstreamRejection(err); ok {
		return httperr.BadRequest(40085, "ragflow rejected "+action+": "+reason)
	}
	return httperr.New(502, gatewayCode, "ragflow "+action+" failed")
}

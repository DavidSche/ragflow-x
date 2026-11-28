package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// The live run of 2026-11-28 (image ragflow-x:test-deefdec-20261128) logged one
// ERROR line and one `gateway.stream_error` audit row for a request the product
// deliberately refuses: `report_context` carried the plausible typo `section`
// instead of `section_key` (probe R25 case ②, expecting 400/44105). The 400 was
// correct; the bookkeeping was not.
//
// Two things were wrong, and they were wrong in the same direction as the six
// truncated streams of 2026-11-27:
//
//  1. Severity. A 4xx is the caller's problem (bad field, unknown section,
//     someone else's report). Logging it at ERROR makes a client mistake
//     indistinguishable from a broken upstream — which is exactly the
//     "six truncations filed as gateway failures" defect in a new shape.
//  2. The audit action. doc/architecture/233 defines `gateway.stream_error` as
//     "the assistant stream failed **after 200 was committed**". Nothing was
//     committed here, so the row asserted something that did not happen.
//
// The refusal branch existed all along, but it ran *after* the logging, so its
// own comment ("this was a refused request, not a broken stream") described the
// code without controlling it.

func streamAudits(t *testing.T, h *chatStreamHarness, action string) int64 {
	t.Helper()
	_, total, err := h.svc.Store.ListAudits(context.Background(), h.tenantID, 1, 50,
		repository.AuditFilter{Action: action})
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	return total
}

// TestChatStreamRefusalIsNotFiledAsAStreamError pins both halves at once: the
// request is refused with its own code, and no delivery record claims a stream
// failed. On the pre-fix shape the assertion that bites is the audit count (the
// 400 and the code were already right).
func TestChatStreamRefusalIsNotFiledAsAStreamError(t *testing.T) {
	h := newChatStreamHarness(t)
	rec, envelope := h.stream(t, map[string]interface{}{
		"report_id": "rep-1",
		"revision":  1,
		"section":   "compliance", // the typo R25 sends on purpose
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad attachment must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, envelope); code != 44105 {
		t.Fatalf("bad attachment must answer 44105, got %d", code)
	}
	if n := streamAudits(t, h, "gateway.stream_error"); n != 0 {
		t.Fatalf("a refusal before the first byte wrote %d gateway.stream_error row(s); "+
			"doc/architecture/233 defines that action as a failure after 200 was committed", n)
	}
}

// TestChatStreamStillAuditsAFailureAfterTheFirstByte is the control: without it,
// deleting the audit call altogether would also make the test above pass. The
// mock's transport fault fails the stream after content, so the delivery really
// did start and the row is owed.
func TestChatStreamStillAuditsAFailureAfterTheFirstByte(t *testing.T) {
	h := newChatStreamHarness(t)
	h.engine.Mock.ConfigureStreamFault(ragflow.StreamFault{
		ContentBeforeFault: "合规那节",
		TransportError:     "upstream connection dropped",
	})
	rec, _ := h.stream(t, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a stream that produced bytes must have committed 200, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if n := streamAudits(t, h, "gateway.stream_error"); n != 1 {
		t.Fatalf("a failure after the first byte must leave exactly 1 gateway.stream_error row, got %d", n)
	}
}

// TestStreamRefusalSeveritySeparatesTheCallersMistakeFromOurs pins the level
// decision itself. A pre-commit refusal of a 4xx is the caller's; a 5xx or an
// unclassified error is still ours and must stay at ERROR. Without this, "log
// everything at INFO" would satisfy the audit-count test above.
func TestStreamRefusalSeveritySeparatesTheCallersMistakeFromOurs(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		isErr bool
		code  int
	}{
		{"bad field", httperr.BadRequest(44105, "report_context must be {report_id, revision, section_key}"), false, 44105},
		{"unknown section", httperr.New(404, 44107, "unknown section"), false, 44107},
		{"quota", httperr.QuotaExceeded("budget"), false, 42930},
		{"internal", httperr.Internal("something broke"), true, 500},
		{"unclassified", errUnclassifiedForSeverityTest{}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isErr, code := streamRefusalSeverity(tc.err)
			if isErr != tc.isErr || code != tc.code {
				t.Fatalf("streamRefusalSeverity(%v) = (%v, %d), want (%v, %d)",
					tc.err, isErr, code, tc.isErr, tc.code)
			}
		})
	}
}

type errUnclassifiedForSeverityTest struct{}

func (errUnclassifiedForSeverityTest) Error() string { return "dial tcp: connection refused" }

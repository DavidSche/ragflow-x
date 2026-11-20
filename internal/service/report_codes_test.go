package service

import (
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// The 23 report codes are a public contract (doc/288 §8.7 + doc/301 §7.3). This
// test is the registry guard: no gaps, no extras, no renumbering, no 44124.
//
// It grew from 16 to 23 when the assurance surface landed: 44117-44123 are
// *appended*, and the assertion that 44124 does not exist is what keeps the next
// feature from assuming "there is room".
func TestReportErrorCodeSetIsClosedAndContiguous(t *testing.T) {
	if len(reportErrorCodes) != 23 {
		t.Fatalf("the report code set is 23 values, got %d", len(reportErrorCodes))
	}
	for code := 44101; code <= 44123; code++ {
		status, ok := reportErrorCodes[code]
		if !ok {
			t.Fatalf("code %d is missing from the closed set", code)
		}
		if status < 400 || status > 500 {
			t.Fatalf("code %d has a non-error HTTP status %d", code, status)
		}
	}
	if _, exists := reportErrorCodes[44124]; exists {
		t.Fatal("44124 must not exist: the set stops at 44123")
	}
	// The seven assurance codes have their own statuses; 44123 is the only 429 in
	// the family (a rate condition, not a state conflict).
	for code, want := range map[int]int{
		codeReportRevisionNotAssurable: 400, codeReportAssuranceRunConflict: 409,
		codeReportAssuranceBudgetExhausted: 409, codeReportFindingNotFound: 404,
		codeReportAssuranceAckRequired: 409, codeReportAssuranceNotReady: 409,
		codeReportAssuranceQuotaExhausted: 429,
	} {
		if got := reportErrorCodes[code]; got != want {
			t.Fatalf("assurance code %d must be %d, got %d", code, want, got)
		}
	}
}

func TestReportErrorCodeHTTPSemantics(t *testing.T) {
	// Object-level "missing" and "not yours" are both 404: a 403 would leak
	// existence, which is the whole point of the access model (§8.7).
	for _, code := range []int{codeReportTemplateNotFound, codeReportSectionNotFound,
		codeReportRevisionNotFound, codeReportAccessDenied} {
		if got := reportErrorCodes[code]; got != 404 {
			t.Fatalf("code %d must be 404, got %d", code, got)
		}
	}
	// Business conflicts are 409.
	for _, code := range []int{codeReportInvalidTransition, codeReportExportRequiresPublished,
		codeReportActiveJobConflict, codeReportBudgetExhausted, codeReportEventCursorExpired,
		codeReportEvidenceExpired} {
		if got := reportErrorCodes[code]; got != 409 {
			t.Fatalf("code %d must be 409, got %d", code, got)
		}
	}
	// 500 is reserved for "a human must look". 44103/44112 never reach HTTP at all
	// (they only land in `rgx_report_job.error_code`), so only 44113 is a 500
	// response.
	for _, code := range []int{codeReportEvidenceSnapshotRejected, codeReportLedgerInconsistent} {
		if got := reportErrorCodes[code]; got != 500 {
			t.Fatalf("code %d must map to 500 for internal bookkeeping, got %d", code, got)
		}
	}
	if got := reportErrorCodes[codeReportArtifactVersionConflict]; got != 500 {
		t.Fatalf("44113 is the only 500 response, got %d", got)
	}
	if got := reportErrorCodes[codeReportIdempotencyKeyRequired]; got != 400 {
		t.Fatalf("44114 is a request-shape error (400), got %d", got)
	}
	if got := reportErrorCodes[codeReportCapabilityBlocked]; got != 400 {
		t.Fatalf("44102 is a refused pre-condition (400), got %d", got)
	}
}

func TestReportErrorHelpersCarryStatusAndCode(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   int
	}{
		{"template missing", errReportTemplateNotFound(), 404, codeReportTemplateNotFound},
		{"idempotency key", errReportIdempotencyKeyRequired("missing"), 400, codeReportIdempotencyKeyRequired},
		{"capability blocked", errReportCapabilityBlocked("ds-1", "vector:below_min_level"), 400, codeReportCapabilityBlocked},
		{"active job conflict", errReportActiveJobConflict(), 409, codeReportActiveJobConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var httpErr *httperr.Error
			if !errors.As(tc.err, &httpErr) {
				t.Fatalf("want *httperr.Error, got %T", tc.err)
			}
			if httpErr.Status != tc.status || httpErr.Code != tc.code {
				t.Fatalf("want %d/%d, got %d/%d", tc.status, tc.code, httpErr.Status, httpErr.Code)
			}
			if tc.code == codeReportCapabilityBlocked && httpErr.Message == "" {
				t.Fatal("a blocked capability must name the reason")
			}
		})
	}
}

func TestReportJobEventTypesAreTheBroadcastClosedSet(t *testing.T) {
	types := ReportJobEventTypes()
	if len(types) != 15 {
		t.Fatalf("§8.8 lists 15 broadcast events, got %d", len(types))
	}
	seen := make(map[string]struct{}, len(types))
	for _, eventType := range types {
		if _, dup := seen[eventType]; dup {
			t.Fatalf("duplicate event type %q", eventType)
		}
		seen[eventType] = struct{}{}
	}
	// The outbox delivery type is *not* a broadcast event: mixing the two
	// registries would let a mechanical retry leak into the UI contract.
	if _, exists := seen["report_job_finalize"]; exists {
		t.Fatal("report_job_finalize is an outbox type and must not be broadcastable")
	}
	// Terminal events can only be written by finalize (§8.2).
	for _, terminal := range []string{reportEventJobSucceeded, reportEventJobPartial,
		reportEventJobFailed, reportEventJobCancelled} {
		if !isReportTerminalEvent(terminal) {
			t.Fatalf("%q must be terminal", terminal)
		}
	}
	for _, nonTerminal := range []string{reportEventJobQueued, reportEventJobFinalizing, reportEventJobProgress} {
		if isReportTerminalEvent(nonTerminal) {
			// `job.finalizing` is emphatically not a terminal state: the revision is
			// not published yet and the runtime job may still be running.
			t.Fatalf("%q must not be terminal", nonTerminal)
		}
	}
}

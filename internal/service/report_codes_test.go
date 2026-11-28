package service

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

// TestReportCodesStayInsideTheReportBlock is the *other half* of the closed-set
// guard above: the set is a contiguous 44101-44123 today, but the band it lives in
// (44100-44199, doc/319 §5.3 S-5) is what the next module reads — and 44124 is
// inside that band, so "no 44124" is a decision, not a boundary. The boundary is
// the band, and the band is registered in exactly one place.
//
// The failure this prevents: doc/306 P0a's freshness refusals being appended to
// 441xx because it was the most recently edited map. That would make a citation
// audit answer with a sentence about source staleness, and neither side's tests
// would notice — each closed set was still closed.
func TestReportCodesStayInsideTheReportBlock(t *testing.T) {
	var problems []string
	for code := range reportErrorCodes {
		block, ok := httperr.BlockOf(code)
		if !ok {
			problems = append(problems, itoaService(code)+" is outside every registered error block")
			continue
		}
		if block.Owner != "report" {
			problems = append(problems, itoaService(code)+" lives in the "+block.Owner+" block ("+
				itoaService(block.Min)+"-"+itoaService(block.Max)+"); report codes belong to 441xx")
		}
		if block.Reserved {
			problems = append(problems, itoaService(code)+" landed inside the reserved "+block.Owner+" block")
		}
	}
	if len(problems) > 0 {
		t.Fatalf("report codes left their block:\n  %s", strings.Join(problems, "\n  "))
	}

	// The knowledge band must still be empty of report codes — the reserved-band
	// check above is per-code, this is the one an author reads.
	knowledge, ok := httperr.BlockOf(46000)
	if !ok {
		t.Fatal("the knowledge-freshness band is not registered: doc/306 P0a has nowhere to declare codes")
	}
	for code := range reportErrorCodes {
		if code >= knowledge.Min && code <= knowledge.Max {
			t.Fatalf("report code %d is inside the knowledge band %d-%d", code, knowledge.Min, knowledge.Max)
		}
	}
}

// repoRootForContractTest is the package-relative way to reach `web/src`: tests run
// with the package directory as cwd, and a hard-coded absolute path would make the
// gate pass on exactly one machine.
func repoRootForContractTest() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(dir, "..", ".."))
}

func mustReadCodeFile(t *testing.T, root string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "internal", "service", "report_codes.go"))
	if err != nil {
		t.Fatalf("read report_codes.go: %v", err)
	}
	return body
}

func itoaService(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

// TestReportCodeTableMatchesTheFrontendContract closes the fourth corner of doc/319
// §8.7 P-06: "错误码定义、HTTP 状态、API 文档、测试、前端类型五处一致".
//
// The report family is declared in four places — the Go constants, the Go status
// table, the swagger `x-error-codes` (produced from the `@Failure` annotations and
// audited by `cmd/apicontract`), and `web/src/.../report-contract.ts`. Until this
// test the frontend copy was checked by a *frontend* test that asserted the same
// closed set the frontend itself declared — which is how a copy drifts: both sides
// stay self-consistent while disagreeing with each other.
//
// The status is compared, not just the numbers, because `httpStatus` is what the UI
// branches on: a `409` rendered as `400` turns "this conflicts with the current
// state, refresh" into "your request was malformed".
func TestReportCodeTableMatchesTheFrontendContract(t *testing.T) {
	root, err := repoRootForContractTest()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(root, "web", "src", "resources", "conversation-center", "report", "report-contract.ts"))
	if err != nil {
		t.Fatalf("read report-contract.ts: %v", err)
	}

	entry := regexp.MustCompile(`(?m)^\s*(\d{5}): \{ name: "([A-Za-z0-9]+)", httpStatus: (\d{3}) \},`)
	matches := entry.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("no REPORT_ERROR_CODES entries found: the scanner is blind")
	}

	documented := map[int]string{}    // code -> frontend name
	documentedStatus := map[int]int{} // code -> frontend http status
	for _, match := range matches {
		code, convErr := strconv.Atoi(match[1])
		if convErr != nil {
			t.Fatalf("code %q: %v", match[1], convErr)
		}
		status, convErr := strconv.Atoi(match[3])
		if convErr != nil {
			t.Fatalf("status %q: %v", match[3], convErr)
		}
		documented[code] = match[2]
		documentedStatus[code] = status
	}

	// (1) same set, both directions.
	for code := range reportErrorCodes {
		if _, ok := documented[code]; !ok {
			t.Fatalf("code %d is declared in Go but missing from report-contract.ts, so the UI has no wording for it", code)
		}
	}
	for code := range documented {
		if _, ok := reportErrorCodes[code]; !ok {
			t.Fatalf("report-contract.ts declares %d, which the server never returns", code)
		}
	}

	// (2) same HTTP status.
	for code, status := range reportErrorCodes {
		if documentedStatus[code] != status {
			t.Fatalf("code %d: Go says HTTP %d, report-contract.ts says %d", code, status, documentedStatus[code])
		}
	}

	// (3) same name: the frontend key is the Go constant minus `codeReport`.
	constant := regexp.MustCompile(`(?m)^\s*codeReport([A-Za-z0-9]+)\s*=\s*(\d{5})\s*$`)
	named := 0
	for _, match := range constant.FindAllStringSubmatch(string(mustReadCodeFile(t, root)), -1) {
		named++
		code, convErr := strconv.Atoi(match[2])
		if convErr != nil {
			t.Fatalf("constant %q: %v", match[1], convErr)
		}
		if documented[code] != match[1] {
			t.Fatalf("code %d: Go constant suffix is %q, report-contract.ts calls it %q", code, match[1], documented[code])
		}
	}
	if named != len(reportErrorCodes) {
		t.Fatalf("read %d `codeReport…` constants but the status table has %d entries: a code was added to one place only", named, len(reportErrorCodes))
	}
}

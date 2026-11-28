// Package httperr's second invariant, and the one `Declared` cannot express.
//
// `codes.go` says "one code, one meaning" and registers every 41xxx code with a slug and a
// Chinese sentence — that is what lets `web/src/lib/errors.ts` map a code to wording without
// guessing. What it cannot express is the *HTTP status*: a code's status is written at the
// construction site (`httperr.New(409, 40331, …)`), so the same code can be raised as a 409
// on one path and a 400 on another and every existing test still passes. The `40085` incident
// (doc/294) is the same disease in its message form: eighteen unrelated refusals shared one
// code, and the live drill rendered a wizard failure as "角色不能是自身的父角色".
//
// This test makes the status half of "one semantic" checkable over the whole server, not just
// over the report family (`report_codes.go`'s table is the report family's own copy of it).
//
// Two rules, and they point in opposite directions on purpose:
//
//  1. a code raised with more than one status is a defect — the client branches on the status
//     before it reads the code, so `409` "refresh and retry" and `400` "your request was
//     malformed" cannot be the same code;
//  2. `statusDriftDebt` freezes the five that are already like this, and it fails **both** when
//     a new one appears and when a frozen one is repaired. A debt table that only fails in one
//     direction becomes a list of things nobody is allowed to fix.
package httperr

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// statusDriftDebt is the frozen list of codes that are raised with two different statuses
// today. Every entry is a real defect, not a judgement call; the reason says what would have
// to change, and none of them are repaired here because repaying one means either renumbering
// a code the web client already has wording for, or deciding which of the two statuses is the
// truthful one for the other site.
var statusDriftDebt = map[int][]int{
	// 40076 is a grab-bag: "invalid attachment reference", "unsupported release environment",
	// "release evidence chain mismatch" and then a 403 for "production release blocked by
	// version preflight". Repair: the 403 site wants its own code — a preflight veto is not a
	// malformed request, and the client should say "blocked" rather than "check your input".
	40076: {400, 403},
	// 40098 collects setting/approval/memory validation at 400 and a state-machine refusal at
	// 409 (`invalid knowledge task status transition`). Repair: split the transition refusal
	// out; `memory.go`'s "embedding model is required" and `setting_revision.go`'s "unsupported
	// system setting secret" do not belong to the same sentence either.
	40098: {400, 409},
	// 40101 is `invalid username or password` at 401 and approval-request validation at 400.
	// This is the worst of the five: a 401 makes the client drop the session, so a malformed
	// batch decision currently logs the user out. Repair: the approval validation sites move to
	// a request-precondition code (411xx); 40101 keeps the credential meaning.
	40101: {400, 401},
	// 40132 is "mcp tool execution is not enabled yet" at 501, dictionary/arithmetic refusals at
	// 400. Repair: the 501 site gets its own code, so "not implemented" stops being reachable
	// from a `percentage_diff` denominator of zero.
	40132: {400, 501},
	// 40331 is "connector source is already bound" at 409 and "connector provider is immutable"
	// at 400. Repair: the immutable-field refusals need their own code — they are a different
	// client action ("remove the field") than "detach the other connector first".
	40331: {400, 409},
}

var (
	codeWithStatus = regexp.MustCompile(`httperr\.New\(\s*(\d{3})\s*,\s*(\d{5})\b`)
	codeBadRequest = regexp.MustCompile(`httperr\.BadRequest\(\s*(\d{5})\b`)
	// statusBearingName maps the positional constructors that take a code to their status.
	// The message-only ones (`Forbidden`, `NotFound`, …) carry no business code, so they cannot
	// create a second status for one.
	statusBearingName = map[string]int{}
)

type codeRaisedAt struct {
	code   int
	status int
	file   string
}

// scanCodeStatuses walks every non-test Go file under the repository's `internal/` tree and
// records the (code, status) pairs the server can construct.
func scanCodeStatuses(root string) ([]codeRaisedAt, error) {
	var out []codeRaisedAt
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "tmp", "test-results":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(body)
		rel, _ := filepath.Rel(root, path)
		for _, m := range codeWithStatus.FindAllStringSubmatch(text, -1) {
			status, convErr := strconv.Atoi(m[1])
			if convErr != nil {
				return convErr
			}
			code, convErr := strconv.Atoi(m[2])
			if convErr != nil {
				return convErr
			}
			out = append(out, codeRaisedAt{code: code, status: status, file: rel})
		}
		for _, m := range codeBadRequest.FindAllStringSubmatch(text, -1) {
			code, convErr := strconv.Atoi(m[1])
			if convErr != nil {
				return convErr
			}
			out = append(out, codeRaisedAt{code: code, status: 400, file: rel})
		}
		return nil
	})
	return out, walkErr
}

// statusesPerCode collapses the observations into code -> sorted status list.
func statusesPerCode(observations []codeRaisedAt) map[int][]int {
	seen := map[int]map[int]bool{}
	for _, obs := range observations {
		if seen[obs.code] == nil {
			seen[obs.code] = map[int]bool{}
		}
		seen[obs.code][obs.status] = true
	}
	out := map[int][]int{}
	for code, set := range seen {
		var statuses []int
		for status := range set {
			statuses = append(statuses, status)
		}
		sort.Ints(statuses)
		out[code] = statuses
	}
	return out
}

func sameStatuses(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sitesFor is what a failure message needs to be actionable: which files raise this code with
// which status, so the reader does not have to grep for it.
func sitesFor(observations []codeRaisedAt, code int) []string {
	seen := map[string]bool{}
	var out []string
	for _, obs := range observations {
		if obs.code != code {
			continue
		}
		label := obs.file + " (" + strconv.Itoa(obs.status) + ")"
		if !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

// driftReport is the pure core: new drifts, repaid debts, and everything else fine.
type driftReport struct {
	unexpected map[int][]int
	repaid     []int
	observed   int
}

func classifyDrift(observations []codeRaisedAt, debt map[int][]int) driftReport {
	byCode := statusesPerCode(observations)
	report := driftReport{unexpected: map[int][]int{}, observed: len(byCode)}
	for code, statuses := range byCode {
		frozen, isFrozen := debt[code]
		switch {
		case len(statuses) > 1 && !isFrozen:
			report.unexpected[code] = statuses
		case len(statuses) > 1 && isFrozen && !sameStatuses(statuses, frozen):
			// The shape changed: a third status appeared, or one was dropped.
			report.unexpected[code] = statuses
		case len(statuses) == 1 && isFrozen:
			report.repaid = append(report.repaid, code)
		case isFrozen && len(statuses) == 0:
			report.repaid = append(report.repaid, code)
		}
	}
	// A frozen code that no longer exists at all is repaid as well; without this the loop
	// above never sees it.
	for code := range debt {
		if _, stillRaised := byCode[code]; stillRaised {
			continue
		}
		already := false
		for _, paid := range report.repaid {
			if paid == code {
				already = true
			}
		}
		if !already {
			report.repaid = append(report.repaid, code)
		}
	}
	sort.Ints(report.repaid)
	return report
}

// TestEveryBusinessCodeKeepsOneHTTPStatus is the gate.
func TestEveryBusinessCodeKeepsOneHTTPStatus(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	observations, err := scanCodeStatuses(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	report := classifyDrift(observations, statusDriftDebt)

	// A scanner that finds nothing is green for the wrong reason: the server has hundreds of
	// constructors, so a floor is the difference between "no drift" and "no measurement".
	if report.observed < 250 {
		t.Fatalf("scanned %d codes, expected at least 250: the scanner went blind (a parser that only knows today's spelling goes quiet the next time someone wraps the call)", report.observed)
	}

	for _, code := range sortedKeys(report.unexpected) {
		t.Fatalf("code %d is raised with more than one HTTP status %v, at %s\nthe client branches on the status first, so one code cannot mean both; either unify the status or give the other site its own code",
			code, report.unexpected[code], strings.Join(sitesFor(observations, code), ", "))
	}
	if len(report.repaid) > 0 {
		t.Fatalf("code(s) %v no longer drift but are still listed in statusDriftDebt: a frozen debt table that is not checked in both directions becomes a list of things nobody may fix — delete the row",
			report.repaid)
	}
}

func sortedKeys(m map[int][]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// TestStatusDriftCheckerHasAFailureDirection is the control: the classifier is proved to bite
// on a code raised twice with different statuses, to stay quiet on a code raised twice with the
// same one, and to notice when a frozen entry is repaired. Without it, the gate above would be
// measuring "did the regex compile".
func TestStatusDriftCheckerHasAFailureDirection(t *testing.T) {
	observations := []codeRaisedAt{
		{code: 40101, status: 401, file: "a.go"},
		{code: 40101, status: 400, file: "b.go"},
		{code: 44119, status: 409, file: "c.go"},
		{code: 44119, status: 409, file: "d.go"},
	}
	// 44119 is here as the "same status twice" control: it must never be reported, whether or
	// not it is frozen — the difference between "used at two sites" and "raised two ways".
	frozen := map[int][]int{40101: {400, 401}}

	report := classifyDrift(observations, frozen)
	if len(report.unexpected) != 0 {
		t.Fatalf("frozen drift reported as new: %v", report.unexpected)
	}
	if len(report.repaid) != 0 {
		t.Fatalf("repayment reported for codes that still drift or are single-status: %v", report.repaid)
	}

	// A code that was not in the debt table starts drifting.
	report = classifyDrift(append(observations, codeRaisedAt{code: 40331, status: 409, file: "e.go"},
		codeRaisedAt{code: 40331, status: 400, file: "f.go"}), frozen)
	if got, ok := report.unexpected[40331]; !ok || !sameStatuses(got, []int{400, 409}) {
		t.Fatalf("a new drift was not reported: %v", report.unexpected)
	}

	// A frozen entry that gained a third status is not the shape it was frozen at.
	report = classifyDrift(append(observations, codeRaisedAt{code: 40101, status: 403, file: "g.go"}), frozen)
	if got, ok := report.unexpected[40101]; !ok || !sameStatuses(got, []int{400, 401, 403}) {
		t.Fatalf("a changed drift shape was not reported: %v", report.unexpected)
	}

	// A frozen entry that was repaired has to leave the table.
	report = classifyDrift([]codeRaisedAt{{code: 40101, status: 401, file: "a.go"}}, frozen)
	if len(report.repaid) != 1 || report.repaid[0] != 40101 {
		t.Fatalf("a repaired frozen code was not reported as repaid: %v", report.repaid)
	}
	// A frozen code that disappeared entirely is repaid too, not "still drifting".
	report = classifyDrift(nil, frozen)
	if len(report.repaid) != 1 || report.repaid[0] != 40101 {
		t.Fatalf("a frozen code that no longer exists was not reported as repaid: %v", report.repaid)
	}

	if _, err := scanCodeStatuses(filepath.Join("..", "..", "..", "internal")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scanner on the real tree: %v", err)
	}
}

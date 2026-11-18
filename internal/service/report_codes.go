package service

import (
	"strconv"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Report answer error codes (doc/288 §8.7, frozen by T0). The set is closed:
// sixteen codes, one trigger each. Tests assert the numbers literally — the
// same discipline as `codeParseProfileRequiresRewrite` — because these values
// are part of the public contract and a "helpful" renumbering would silently
// break the frontend `reports.errors.*` mapping.
//
// Three rules make this table safe to read:
//
//   - object-level "missing" and "not yours" are both 404 (44107/44108/44109);
//     a 403 would leak existence.
//   - business conflicts are 409 (44104/44106/44110/44111/44115/44116).
//   - 500 is reserved for "this cannot happen, a human must look" (44103,
//     44112, 44113). 44103 and 44112 are produced inside the worker
//     transaction and only ever surface as `rgx_report_job.error_code`; they
//     have no HTTP response. Only 44113 is an HTTP 500.
//
// 44114 is the one "the request itself is malformed" code, so it is 400, and
// the frontend must never paper over it by minting a key: that would turn a
// double click into two reports (doc/288 §8.7 rule 5).
const (
	codeReportTemplateNotFound         = 44101
	codeReportCapabilityBlocked        = 44102
	codeReportEvidenceSnapshotRejected = 44103
	codeReportInvalidTransition        = 44104
	codeReportOutlineInvalid           = 44105
	codeReportExportRequiresPublished  = 44106
	codeReportSectionNotFound          = 44107
	codeReportRevisionNotFound         = 44108
	codeReportAccessDenied             = 44109
	codeReportActiveJobConflict        = 44110
	codeReportBudgetExhausted          = 44111
	codeReportLedgerInconsistent       = 44112
	codeReportArtifactVersionConflict  = 44113
	codeReportIdempotencyKeyRequired   = 44114
	codeReportEventCursorExpired       = 44115
	codeReportEvidenceExpired          = 44116
)

// reportErrorCodes is the closed set with its HTTP status. It exists so a
// contract test can assert "no gaps, no extras, no 44117" instead of trusting
// the const block, and so error mapping never has to be re-derived by hand.
var reportErrorCodes = map[int]int{
	codeReportTemplateNotFound:         404,
	codeReportCapabilityBlocked:        400,
	codeReportEvidenceSnapshotRejected: 500,
	codeReportInvalidTransition:        409,
	codeReportOutlineInvalid:           400,
	codeReportExportRequiresPublished:  409,
	codeReportSectionNotFound:          404,
	codeReportRevisionNotFound:         404,
	codeReportAccessDenied:             404,
	codeReportActiveJobConflict:        409,
	codeReportBudgetExhausted:          409,
	codeReportLedgerInconsistent:       500,
	codeReportArtifactVersionConflict:  500,
	codeReportIdempotencyKeyRequired:   400,
	codeReportEventCursorExpired:       409,
	codeReportEvidenceExpired:          409,
}

// errReportTemplateNotFound is the creation-time "key unknown, retired, or not
// visible to this tenant" answer. It never distinguishes the three, because the
// distinction would leak another tenant's template catalog.
func errReportTemplateNotFound() error {
	return httperr.New(404, codeReportTemplateNotFound, "report template not found")
}

// errReportIdempotencyKeyRequired rejects a missing or malformed
// `Idempotency-Key`. A key is request identity, not a cache hint: without it the
// same double click creates two reports (doc/288 §8.7 rule 5).
func errReportIdempotencyKeyRequired(reason string) error {
	return httperr.New(400, codeReportIdempotencyKeyRequired, reason)
}

// errReportCapabilityBlocked is the creation-time recheck failure. Repairable
// and probe-limited verdicts do *not* block: they are disclosed on the report,
// not turned into a refusal (doc/288 §13).
func errReportCapabilityBlocked(datasetID, reason string) error {
	return httperr.New(400, codeReportCapabilityBlocked,
		"dataset "+datasetID+" cannot serve this report template: "+reason)
}

// errReportActiveJobConflict means the report already has a queued or running
// job. Regeneration is an explicit transition, not a second job (doc/288 §7.10).
func errReportActiveJobConflict() error {
	return httperr.New(409, codeReportActiveJobConflict, "report already has an active job")
}

// errReportNotFound is the single answer for "missing", "someone else's", and
// "not in this workspace". §7.6 requires the isolation tests to be unable to
// tell those three apart, so there is deliberately no variant that says more.
func errReportNotFound() error {
	return httperr.New(404, codeReportAccessDenied, "report not found")
}

// errReportInvalidTransition is §8.7's 44104: the object exists and the caller may
// act on it, but the current state does not allow this operation (validating an
// outline that is not awaiting confirmation, cancelling a finished job, publishing
// twice). 409 rather than 400 because the request is well formed — the state is
// the problem, and a retry after the state moves is legitimate.
func errReportInvalidTransition(reason string) error {
	return httperr.New(409, codeReportInvalidTransition, reason)
}

// errReportBudgetExhausted is 44111: the envelope frozen at creation cannot cover
// another call. It is a 409 rather than a 429 because nothing is rate limited — the
// report really has spent its budget — and because the work already paid for must
// still be published, with the missing sections disclosed as `budget_exhausted`
// gaps (doc/288 §7.10), not thrown away.
func errReportBudgetExhausted(reason string) error {
	return httperr.New(409, codeReportBudgetExhausted, reason)
}

// errReportLedgerInconsistent is 44112: the ledger and the job disagree in a way
// only a human can settle (a double settle, a negative actual, a job with no
// envelope). It is produced inside the worker transaction and normally surfaces as
// `rgx_report_job.error_code`, not as an HTTP response.
func errReportLedgerInconsistent(reason string) error {
	return httperr.New(500, codeReportLedgerInconsistent, reason)
}

// errReportRevisionNotFound is 44108: the report exists, the revision number does
// not. It is separate from 44109 so a client that followed a stale link can tell
// "wrong version" from "not allowed to look".
func errReportRevisionNotFound(revision int64) error {
	return httperr.New(404, codeReportRevisionNotFound,
		"report revision "+strconv.FormatInt(revision, 10)+" not found")
}

// errReportExportRequiresPublished is 44106: a revision that is not published has
// no stable content to freeze. `superseded` is refused for a *new* export but its
// existing artifacts stay downloadable — a delivered file must not 404 because a
// newer revision was published (doc/288 §12).
func errReportExportRequiresPublished() error {
	return httperr.New(409, codeReportExportRequiresPublished,
		"only a published report revision can be exported")
}

// errReportArtifactVersionConflict is 44113: the artifact-version unique index won
// the race, which means this attempt rendered a version number another writer had
// already taken. It is a 500 because the lock should have prevented it — a human
// needs to look at the row, not retry.
func errReportArtifactVersionConflict(reason string) error {
	return httperr.New(500, codeReportArtifactVersionConflict, reason)
}

// errReportEvidenceExpired is 44116: re-exporting this revision would produce an
// appendix whose evidence no longer exists. It is a 409 because the request is valid
// and the *state* is the problem — the already-delivered artifact keeps its frozen
// appendix, and an older revision that still has its evidence can be exported instead
// (doc/288 §7.6 / A43). Rendering an empty appendix here would be a silent lie.
func errReportEvidenceExpired(sectionOrd, citationOrd int) error {
	return httperr.New(409, codeReportEvidenceExpired,
		"evidence for section "+strconv.Itoa(sectionOrd+1)+
			" citation "+strconv.Itoa(citationOrd+1)+
			" has expired and cannot be re-verified; the previously delivered artifact keeps its frozen appendix")
}

// errReportArtifactUnavailable is 44104 for a *download*: the row exists and is
// yours, but its state (expired/deleted, or past its `expires_at`) does not allow
// handing out a file. Re-exporting the same revision is the legitimate next step,
// which is why this is a conflict rather than a 404.
func errReportArtifactUnavailable(reason string) error {
	return httperr.New(409, codeReportInvalidTransition, reason)
}

// errReportOutlineInvalid is 44105: the outline (or the confirmation of it) does not
// describe a writable structure. It is also the terminal `error_code` of a report
// that failed its structure gate, which is why it stays available outside the
// handler layer.
func errReportOutlineInvalid(reason string) error {
	return httperr.New(400, codeReportOutlineInvalid, reason)
}

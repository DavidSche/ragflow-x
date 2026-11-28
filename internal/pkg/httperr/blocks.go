package httperr

// Error-code block registry (doc/319 §5.3 S-5, decision P-06 in §8.7).
//
// The repository rule is "one code, one meaning, append only". That rule needs a
// second rule to survive: **a module must know which numbers it may use before it
// starts writing them**. Without it, the next module picks the first free-looking
// number inside someone else's band, and the "one meaning" invariant is broken by
// arithmetic rather than by a decision — which is exactly how the Role block's
// first code ended up carrying eighteen unrelated sentences (see codes.go).
//
// So the bands are declared here, once, with an owner and a purpose. A band is a
// *reservation of numbers*, not a promise of codes: `Reserved: true` means the
// band belongs to a feature that has not started, and `blocks_test.go` fails if
// any code appears in it. That is the same lesson as R-01's `evidence_expired`:
// a declared value with no producer is a tombstone, and a tombstone that is
// indistinguishable from a live value is worse than no value at all.
//
// Adding a code:
//  1. find the band whose owner owns the semantics (not the file you happen to be
//     editing);
//  2. if no band fits, add one here first, with a real owner and a doc link —
//     never "misc" and never a gap of free numbers;
//  3. declare the constant in the owning package, register it in that package's
//     closed set, add the Chinese sentence to `web/src/lib/errors.ts`, and let
//     `cmd/apicontract -audit-hand` prove the API doc agrees.
type Block struct {
	// Min/Max are inclusive.
	Min int
	Max int
	// Owner is the module that may emit codes in this band.
	Owner string
	// Purpose is the one sentence a reader needs to decide whether their new
	// code belongs here.
	Purpose string
	// Doc names the file or specification that defines the band's contract.
	Doc string
	// Reserved marks a band that has been granted but has no codes yet. A
	// reserved band must contain zero declared codes (doc/319 §8.7 forbids
	// minting codes without a production return point).
	Reserved bool
}

// Blocks is the whole registry, ordered by Min. Ranges must not overlap and must
// not be adjacent-with-a-shared-owner (a band is a boundary, so touching bands
// with different owners are fine, but a code must resolve to exactly one band).
var Blocks = []Block{
	{
		Min: 40000, Max: 40999,
		Owner:   "core",
		Purpose: "request and object errors that predate the block discipline (40000, 40044, 40105, 40305, 40915 …)",
		Doc:     "internal/pkg/httperr/httperr.go",
	},
	{
		Min: 41100, Max: 41199,
		Owner:   "request-precondition",
		Purpose: "a well-formed request that cannot be honoured (scenario template, dataset capability, approval, upstream rejection, credentials, role inheritance, tenant lifecycle)",
		Doc:     "internal/pkg/httperr/codes.go",
	},
	{
		Min: 42000, Max: 42999,
		Owner:   "gateway",
		Purpose: "upstream/gateway conditions and quota: 422xx upstream failure, 429xx rate and allowance",
		Doc:     "internal/service/gateway_failure.go",
	},
	{
		Min: 44100, Max: 44199,
		Owner:   "report",
		Purpose: "report answer and assurance lifecycle (44101-44123 today), appended only",

		Doc: "internal/service/report_codes.go",
	},
	{
		Min: 46000, Max: 46999,
		Owner:    "knowledge-freshness",
		Purpose:  "knowledge update and freshness (doc/306 P0a/P0b: source head, change event, freshness policy, evaluation gate)",
		Doc:      "doc/develop/306_知识更新与外部数据源同步闭环方案（三系统对比与RAGFlow-X升级设计）.md",
		Reserved: true,
	},
	{
		Min: 50000, Max: 50999,
		Owner:   "infrastructure",
		Purpose: "5xx: the server could not complete the work (engine, storage, migrations); 502xx engine calls, 503xx drill/support",
		Doc:     "internal/pkg/httperr/httperr.go",
	},
}

// BlockOf resolves a code to its band. The second result is false for a code
// outside every band — a new band is the answer, not a nearby free number.
func BlockOf(code int) (Block, bool) {
	for _, b := range Blocks {
		if code >= b.Min && code <= b.Max {
			return b, true
		}
	}
	return Block{}, false
}

// ReservedBlocks returns the bands that are granted but must stay empty.
func ReservedBlocks() []Block {
	var reserved []Block
	for _, b := range Blocks {
		if b.Reserved {
			reserved = append(reserved, b)
		}
	}
	return reserved
}

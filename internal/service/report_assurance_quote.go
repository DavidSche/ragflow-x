package service

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// quoteCaseFolder is real case folding, not `strings.ToLower`: folding expands `ß` to
// `ss` and `İ` to a two-rune sequence, and those expansions are exactly what makes
// a normalized offset drift away from the original. Lowercasing would leave `ß`
// intact, so the paths this file's tests pin could never be reached.
var quoteCaseFolder = cases.Fold()

// Quote traceability (doc/301 §5.1.1 layer ①).
//
// This layer answers exactly one question: **is this text present in the snapshot
// we cited?** It is pure code, it is cheap, and it is the strongest single defence
// against a fabricated quote — a model can invent a justification but it cannot
// invent text that exists in the source.
//
// It answers nothing about meaning. A verified quote can still contradict the
// claim it was cited for (doc/301's worked example: claim says "all 12
// subsidiaries finished remediation", quote says "9 of the subsidiaries finished
// remediation" — the quote is verbatim and it refutes the claim). So `verified`
// *unlocks* the semantic verdict, it never implies `supported`.

// Quote statuses surfaced by this layer. `ambiguous` exists because guessing is
// worse than admitting uncertainty: a highlight drawn at a drifted offset points
// the reviewer at the wrong sentence, which is more damaging than no highlight.
//
// The four values are *bound* to the model's closed set rather than repeated as
// literals: `quote_status` is a `size:16` column validated against
// `model.ReportQuoteStatuses()`, so a spelling that only agrees with itself would
// be rejected by the store instead of by the compiler.
//
// `evidence_expired` and `evidence_missing` (doc/319 R-01) are the two states of a
// citation whose snapshot cannot be read at all. They are not quote verdicts: no
// quote was ever checked, because the snapshot is a tombstone or is absent.
const (
	reportQuoteVerified        = model.ReportQuoteStatusVerified
	reportQuoteNotFound        = model.ReportQuoteStatusNotFound
	reportQuoteAmbiguous       = model.ReportQuoteStatusAmbiguous
	reportQuoteNotProvided     = model.ReportQuoteStatusNotProvided
	reportQuoteEvidenceExpired = model.ReportQuoteStatusEvidenceExpired
	reportQuoteEvidenceMissing = model.ReportQuoteStatusEvidenceMissing
)

// reportQuoteBlocksSemantic answers "may the semantic layer be paid for?".
//
// Two different reasons, one answer. `not_found` means the snapshot is readable and
// the quote is not in it; the other two mean there is no snapshot content to read.
// In all three an empty 【证据原文】 would be sent, and a verdict produced from it is
// unattributable — it cannot be reproduced and it cannot be blamed on the model
// (doc/319 §8 invariant 2).
func reportQuoteBlocksSemantic(status string) bool {
	return model.ReportQuoteStatusUnreadable(status)
}

// Span map states. `partial` means the normalized match cut through an expansion
// (casefolding `ß` → `ss`, NFKC collapsing a compatibility char), so the original
// range cannot be reconstructed unambiguously.
const (
	reportSpanMapExact   = "exact"
	reportSpanMapPartial = "partial"
	reportSpanMapNone    = "none"
)

// reportQuoteCheck is the result of layer ①. Span is measured in **runes of the
// original text**, never in runes of the normalized text: the UI highlights the
// original, and normalization is exactly what makes offsets drift.
//
// `MatchedText` is always a bounded slice of the original snapshot: the exact hit for
// `verified`, and the smallest original interval that covers the ambiguous match for
// `partial`. It never runs to the end of the snapshot (doc/319 R-03): that value is
// fed to the semantic layer as 【证据原文】, so an unbounded slice silently changed
// the question the model was answering from "does this quote support the claim" to
// "does the rest of this document support the claim".
type reportQuoteCheck struct {
	Status        string `json:"status"`
	SpanStart     int    `json:"span_start"`
	SpanEnd       int    `json:"span_end"`
	MatchedText   string `json:"matched_text"`
	SpanMap       string `json:"span_map"`
	Normalization string `json:"normalization"`
	// Candidates counts equal-length normalized matches. More than one means the
	// quote is not locatable without guessing, which is `ambiguous` even though
	// the text exists somewhere.
	Candidates int `json:"candidates"`
}

// runeSpan is one entry of the normalization map: a range of the normalized text
// and the original rune range it came from.
type runeSpan struct {
	normStart int
	normLen   int
	origStart int
	origLen   int
}

// reportCheckQuote locates `quote` inside `snapshot`.
//
// Order matters and is specified (doc/301 §5.1.1):
//  1. exact hit on the raw text wins outright — no normalization, no mapping,
//     and only when it is the *unique* hit (a twice-occurring raw hit is still a
//     hit we cannot point at);
//  2. normalized hit whose both ends land on map boundaries → `verified` + exact;
//  3. normalized hit cutting through an expansion → `ambiguous` + partial;
//  4. no map, or a map that does not cover the match → `ambiguous` + none.
func reportCheckQuote(quote, snapshot string) reportQuoteCheck {
	trimmed := strings.TrimSpace(quote)
	if trimmed == "" {
		return reportQuoteCheck{Status: reportQuoteNotProvided, SpanMap: reportSpanMapNone, Normalization: "raw"}
	}

	// ① exact, raw offsets.
	if offset := strings.Index(snapshot, trimmed); offset >= 0 {
		// Two identical raw occurrences: report the ambiguity instead of highlighting
		// the first one, which would silently point the reviewer at the wrong
		// sentence.
		if occurrences := strings.Count(snapshot, trimmed); occurrences > 1 {
			return reportQuoteCheck{
				Status: reportQuoteAmbiguous, SpanMap: reportSpanMapNone,
				Normalization: "raw", Candidates: occurrences,
			}
		}
		start := utf8RuneCount(snapshot[:offset])
		return reportQuoteCheck{
			Status: reportQuoteVerified, SpanStart: start,
			SpanEnd:     start + utf8RuneCount(trimmed),
			MatchedText: trimmed, SpanMap: reportSpanMapExact,
			Normalization: "raw", Candidates: 1,
		}
	}

	normSnapshot, spans := normalizeWithMap(snapshot)
	normQuote, _ := normalizeWithMap(trimmed)
	if normQuote == "" || normSnapshot == "" {
		// Nothing survived normalization (whitespace-only quote, or a snapshot of
		// punctuation). Treat as unlocatable rather than "not found": the difference
		// matters because `not_found` raises a finding against the report.
		return reportQuoteCheck{Status: reportQuoteAmbiguous, SpanMap: reportSpanMapNone, Normalization: "nfkc_cf"}
	}

	first, candidates := indexAll(normSnapshot, normQuote)
	if first < 0 {
		return reportQuoteCheck{Status: reportQuoteNotFound, SpanMap: reportSpanMapNone, Normalization: "nfkc_cf"}
	}
	if candidates > 1 {
		// Equal-length multiple matches: the quote exists but its location is
		// ambiguous. We deliberately do not pick the first one — a wrong highlight
		// is worse than none.
		return reportQuoteCheck{
			Status: reportQuoteAmbiguous, SpanMap: reportSpanMapNone,
			Normalization: "nfkc_cf", Candidates: candidates,
		}
	}

	origStart, startAligned := mapNormBoundary(spans, first)
	origEnd, endAligned := mapNormBoundary(spans, first+utf8RuneCount(normQuote))
	original := []rune(snapshot)
	if !startAligned || !endAligned {
		// ③ the match starts or ends inside an expansion (e.g. it covers the first
		// `s` of `ss` from `ß`). Refuse to guess: no span, because a highlight drawn
		// at a reconstructed offset points the reviewer at the wrong sentence.
		//
		// `MatchedText` is still reported, but bounded to the original interval the
		// normalized match actually covers. Slicing to the end of the snapshot was
		// both a wrong answer and an expensive one: this text is what layer ③ sends as
		// 【证据原文】, so the model was asked whether a whole tail of the document
		// supported the claim instead of whether the quote did (doc/319 R-03).
		coveredStart, coveredEnd := mapNormCoverage(spans, first, first+utf8RuneCount(normQuote))
		return reportQuoteCheck{
			Status: reportQuoteAmbiguous, SpanMap: reportSpanMapPartial,
			Normalization: "nfkc_cf", Candidates: 1,
			MatchedText: string(original[clamp(coveredStart, 0, len(original)):clamp(coveredEnd, 0, len(original))]),
		}
	}
	return reportQuoteCheck{
		Status: reportQuoteVerified, SpanStart: origStart, SpanEnd: origEnd,
		MatchedText: string(original[clamp(origStart, 0, len(original)):clamp(origEnd, 0, len(original))]),
		SpanMap:     reportSpanMapExact, Normalization: "nfkc_cf", Candidates: 1,
	}
}

// normalizeWithMap applies NFKC + casefold per original rune and records where each
// normalized rune came from.
//
// Per rune, not over the whole string, on purpose: NFKC applied globally can
// compose across boundaries, and the composed output then has no single original
// range. Composing per rune yields a strictly *larger* normalized string in the
// rare cases where the two differ, which biases this layer towards `ambiguous`
// instead of towards a wrong highlight — the correct direction to fail.
func normalizeWithMap(s string) (string, []runeSpan) {
	var builder strings.Builder
	spans := make([]runeSpan, 0, len(s))
	normIndex := 0
	for origIndex, r := range []rune(s) {
		normalized, _ := normalizeWithMapRune(r)
		if normalized == "" {
			continue
		}
		spans = append(spans, runeSpan{
			normStart: normIndex, normLen: utf8RuneCount(normalized),
			origStart: origIndex, origLen: 1,
		})
		builder.WriteString(normalized)
		normIndex += utf8RuneCount(normalized)
	}
	return builder.String(), spans
}

// normalizeWithMapRune normalizes one rune. Whitespace runs collapse to a single
// space so a quote that re-wraps across lines still matches, and casefolding is
// approximated by `unicode.ToLower` plus the special cases that actually matter
// for German/Turkish text found in enterprise documents.
func normalizeWithMapRune(r rune) (string, bool) {
	if unicode.IsSpace(r) {
		// Every whitespace character becomes one space, so a quote copied out of a
		// PDF (line break) still matches the snapshot (newline).
		return " ", true
	}
	// Zero-width characters are dropped: they are invisible in the reader but would
	// shift every offset after them.
	if r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' {
		return "", false
	}
	normalized := norm.NFKC.String(string(r))
	return quoteCaseFolder.String(normalized), true
}

// mapNormBoundary resolves a normalized rune index to the original rune index. The
// second return value reports whether the index sits exactly on an entry boundary;
// an index inside an entry cannot be mapped and must not be approximated.
func mapNormBoundary(spans []runeSpan, normIndex int) (int, bool) {
	for _, span := range spans {
		if normIndex == span.normStart {
			return span.origStart, true
		}
		if normIndex > span.normStart && normIndex < span.normStart+span.normLen {
			// Inside an expansion (`ß` → `ss`): the caller must degrade.
			return span.origStart, false
		}
	}
	if len(spans) == 0 {
		return 0, false
	}
	last := spans[len(spans)-1]
	if normIndex == last.normStart+last.normLen {
		return last.origStart + last.origLen, true
	}
	return 0, false
}

// mapNormCoverage returns the smallest original rune interval [start, end) that
// covers every normalized rune in [from, to), and `0, 0` when nothing intersects.
//
// It is the honest half of `mapNormBoundary`: where a boundary that falls inside an
// expansion cannot be *resolved*, the interval containing the expansion can still be
// *bounded*, and a bounded slice is what `MatchedText` needs (doc/319 R-03).
func mapNormCoverage(spans []runeSpan, from, to int) (int, int) {
	start, end := -1, -1
	for _, span := range spans {
		if span.normStart+span.normLen <= from || span.normStart >= to {
			continue
		}
		if start < 0 || span.origStart < start {
			start = span.origStart
		}
		if span.origStart+span.origLen > end {
			end = span.origStart + span.origLen
		}
	}
	if start < 0 {
		return 0, 0
	}
	return start, end
}

// indexAll returns the first match index and the number of non-overlapping
// matches.
func indexAll(haystack, needle string) (int, int) {
	first := strings.Index(haystack, needle)
	if first < 0 {
		return -1, 0
	}
	count := 0
	offset := 0
	for {
		found := strings.Index(haystack[offset:], needle)
		if found < 0 {
			break
		}
		count++
		offset += found + len(needle)
	}
	return utf8RuneCount(haystack[:first]), count
}

func utf8RuneCount(s string) int {
	return len([]rune(s))
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// reportClampVerdict applies the §5.1.1 degrade matrix: the quote layer can only
// ever *lower* a verdict, never raise it.
//
// The point of the matrix is that a failed quote check must not be silently
// rewritten into `insufficient_evidence`. "We could not find the quote" and "we
// found no evidence" are different problems with different fixes: the first means
// the model fabricated a citation, the second means the corpus does not answer the
// question.
func reportClampVerdict(verdict string, check reportQuoteCheck, claimType string, requiresEvidence bool) (string, string) {
	switch check.Status {
	case reportQuoteVerified:
		return verdict, ""
	case reportQuoteNotFound:
		// A fabricated quote is a finding against the citation, never a licence to
		// delete a verdict the evaluator reached. A `conflicting` claim whose quote
		// was not in the snapshot is still contradicted by the passage the reviewer
		// will open: turning that into `abstained` removes the conclusion AND the
		// finding that would have flagged the fabrication (doc/319 R-02).
		if reportVerdictClampProtects(verdict) {
			return verdict, ""
		}
		return "abstained", "quote_not_found"
	case reportQuoteAmbiguous:
		// Two places in the snapshot match, so "an excerpt exists" is true and "this
		// excerpt is the one" is not. That is enough to rule out a full `supported`
		// and not enough to run the semantic layer against a guessed passage, so an
		// abstaining evaluator keeps its abstain and `input_invalid` names why.
		return capVerdict(verdict, "partially_supported"), reportAbstainFor(verdict, "input_invalid")
	case reportQuoteEvidenceExpired, reportQuoteEvidenceMissing:
		// Unreachable from the pipeline: `executeReportAssuranceWith` closes these
		// before the semantic layer is chosen. Kept explicit so a future caller cannot
		// route an unreadable snapshot through the weak-quote caps and get a
		// `partially_supported` out of evidence nobody could open.
		if reportVerdictClampProtects(verdict) {
			return verdict, ""
		}
		return "abstained", "no_quote"
	default: // not_provided
		// Same rule as `not_found`, and this is the reachable half: the semantic
		// layer runs on `not_provided`, so an evaluator that judged `unsupported`
		// from the claim's own context used to be rewritten into `abstained` **and**
		// lose its finding, leaving a `succeeded` run with zero findings that
		// nonetheless contained a claim the model had rejected (doc/319 R-02).
		if reportVerdictClampProtects(verdict) {
			return verdict, ""
		}
		if claimType == "fact" && requiresEvidence {
			return "abstained", "no_quote"
		}
		return capVerdict(verdict, "partially_supported"), reportAbstainFor(verdict, "no_quote")
	}
}

// reportVerdictClampProtects reports whether the quote layer must leave a verdict
// exactly as the evaluator returned it (doc/319 R-02, invariant 3).
//
// The clamp exists to stop a weak quote from *certifying*. It has no business
// touching a negative conclusion: `unsupported`, `conflicting` and `outdated` are
// judgements about the claim that a missing quote cannot undo, and `insufficient`
// is already the honest "the evidence does not support this" answer. Lowering any
// of them to `abstained` also silently drops the finding that `reportFindingOfFor`
// derives from the verdict, which is how a rejected claim disappeared from the
// review workspace.
//
// The pre-R-02 guard could not see this: it compared positions on a single support
// axis where `abstained` sits *below* `unsupported`, so "judged" → "did not judge"
// registered as a legal decrease.
func reportVerdictClampProtects(verdict string) bool {
	switch verdict {
	case model.ReportVerdictUnsupported, model.ReportVerdictConflicting,
		model.ReportVerdictOutdated, model.ReportVerdictInsufficient:
		return true
	}
	return false
}

// reportAbstainFor returns `reason` only when `verdict` is already an abstain.
//
// It exists so the clamp never *invents* an abstain reason for a verdict that was
// reached: attaching `no_quote` to a `partially_supported` row would claim the
// evaluator refused to judge when it in fact judged. The reason belongs to the
// abstain, not to the quote layer.
func reportAbstainFor(verdict, reason string) string {
	if verdict == "abstained" {
		return reason
	}
	return ""
}

// capVerdict lowers `verdict` to at most `ceiling` along the support axis.
//
// Only `supported` sits above `partially_supported` on that axis; `unsupported`,
// `conflicting`, `outdated` and `abstained` are already at or below it and must not
// be *raised* by a weak quote (a claim that contradicts its source does not become
// partially supported because the quote was fuzzy).
func capVerdict(verdict, ceiling string) string {
	if verdict == "supported" && ceiling == "partially_supported" {
		return "partially_supported"
	}
	return verdict
}

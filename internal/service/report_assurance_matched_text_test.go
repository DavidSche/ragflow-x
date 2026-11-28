package service

import (
	"strings"
	"testing"
)

// TestReportCheckQuoteMatchedTextStaysInsideTheOriginalMatch is doc/319 R-03.
//
// The `partial` branch could not resolve the original offset, and it reported
// `MatchedText` as "the snapshot from the resolved start" — i.e. everything to the
// end of the document. Two consequences, one of them silent: the reviewer got a
// matched text that was not the match, and `evaluateReportClaim` passes exactly that
// string to the semantic layer as 【证据原文】, so the model was asked whether the
// tail of the document supported the claim.
//
// The property is boundedness, not a number: the matched text must sit inside the
// smallest original interval the normalized match can possibly cover. A long tail is
// what makes the old shape visible — with a short snapshot "slice to the end" and
// "bounded slice" differ by a character or two.
func TestReportCheckQuoteMatchedTextStaysInsideTheOriginalMatch(t *testing.T) {
	// The tail is deliberate: without it the old implementation and the new one agree.
	const tail = " Danach folgt ein langer Anhang mit vielen Sätzen, die mit dem Zitat nichts zu tun haben."
	cases := []struct {
		name     string
		snapshot string
		quote    string
		want     string
		// wantStatus/wantMap are stated so this test cannot pass by degrading every
		// case to `ambiguous` for the wrong reason.
		wantStatus string
		wantMap    string
	}{
		{
			// `ß` folds to two runes (`ss`), so a quote ending between them has no
			// resolvable end offset.
			name:     "casefold expansion cut in the middle",
			snapshot: "Die Straße ist breit." + tail,
			quote:    "Die Stras",
			// Runes 0..8 of the snapshot ("Die Straß"): the expansion's own rune is the
			// last one inside the interval, and the sentence after it is not.
			want:       "Die Straß",
			wantStatus: reportQuoteAmbiguous, wantMap: reportSpanMapPartial,
		},
		{
			// NFKC expands the compatibility char `ﬁ` (U+FB01) to `fi`.
			name:     "nfkc compatibility expansion cut in the middle",
			snapshot: "The ﬁle is here." + tail,
			quote:    "The f",
			want:     "The ﬁ",
			// A rune-bounded answer, not a status answer: `ambiguous` is expected, the
			// text reported with it must be the expansion and not the document.
			wantStatus: reportQuoteAmbiguous, wantMap: reportSpanMapPartial,
		},
		{
			// Control: a fully aligned normalized match still resolves exactly. If the
			// fix had been "report nothing for partial matches", this case would still
			// pass — which is why the two cases above assert the text too.
			name:       "aligned match still resolves",
			snapshot:   "Die Straße ist breit." + tail,
			quote:      "strasse ist",
			want:       "Straße ist",
			wantStatus: reportQuoteVerified, wantMap: reportSpanMapExact,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			check := reportCheckQuote(testCase.quote, testCase.snapshot)
			if check.Status != testCase.wantStatus || check.SpanMap != testCase.wantMap {
				t.Fatalf("status/span_map = %s/%s, want %s/%s",
					check.Status, check.SpanMap, testCase.wantStatus, testCase.wantMap)
			}
			if check.MatchedText != testCase.want {
				t.Fatalf("matched_text = %q, want %q", check.MatchedText, testCase.want)
			}
			if !strings.Contains(testCase.snapshot, check.MatchedText) {
				t.Fatalf("matched_text %q is not a slice of the snapshot", check.MatchedText)
			}
			// The bound the old shape violated: a match can only be longer than the
			// quote by the expansions its two ends cut through (at most one rune each),
			// never by the rest of the document.
			if limit := utf8RuneCount(testCase.quote) + 2; utf8RuneCount(check.MatchedText) > limit {
				t.Fatalf("matched_text is %d runes, quote is %d — the match may exceed the quote by at most 2 runes",
					utf8RuneCount(check.MatchedText), utf8RuneCount(testCase.quote))
			}
			// Fails on the old shape for the long-tail cases, and is stated separately
			// from the exact expectation because "returned the whole document" is the
			// bug, not a value that happened to be unlucky.
			if check.Status == reportQuoteAmbiguous && check.MatchedText == testCase.snapshot {
				t.Fatal("matched_text is the entire snapshot: the match was not bounded")
			}
		})
	}
}

// TestReportCheckQuotePartialMatchDoesNotBecomeASpan pins the other half of the
// `partial` contract: the text is reported, the *highlight* is not.
//
// A bounded slice is not a resolved offset. Reporting `SpanStart/SpanEnd` for a match
// that cut through an expansion would draw a highlight the pipeline cannot justify,
// and a wrong highlight is worse than none (the rule the ambiguous branch exists for).
func TestReportCheckQuotePartialMatchDoesNotBecomeASpan(t *testing.T) {
	check := reportCheckQuote("Die Stras", "Die Straße ist breit.")
	if check.Status != reportQuoteAmbiguous {
		t.Fatalf("status = %s, want %s", check.Status, reportQuoteAmbiguous)
	}
	if check.SpanStart != 0 || check.SpanEnd != 0 {
		t.Fatalf("span = %d..%d, want 0..0: an unresolved offset must not be reported as a span",
			check.SpanStart, check.SpanEnd)
	}
	if check.MatchedText == "" {
		t.Fatal("a bounded match text is still owed to the reviewer")
	}
}

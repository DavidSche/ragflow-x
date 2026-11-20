package service

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// doc/301 §5.1.1 names this test explicitly: pin the three span paths with a
// snapshot containing a full-width digit (NFKC folds it) and `ß` (casefold expands
// it to `ss`), because both make normalized offsets drift away from the original.

// The line break is deliberate: it is what a PDF copy produces, and it is the only
// way to exercise the whitespace-normalizing path (a raw hit short-circuits it).
const quoteFixtureSnapshot = "２０２３年，Müller 集团的 Straße 业务由 12 家子公司\n承担，其中 9 家已完成整改。"

func TestReportAssuranceQuoteSpanMapsBackToOriginalRunes(t *testing.T) {
	t.Run("raw exact hit keeps original offsets", func(t *testing.T) {
		quote := "其中 9 家已完成整改"
		got := reportCheckQuote(quote, quoteFixtureSnapshot)
		if got.Status != reportQuoteVerified || got.SpanMap != reportSpanMapExact {
			t.Fatalf("want verified/exact, got %s/%s", got.Status, got.SpanMap)
		}
		if got.Normalization != "raw" {
			t.Fatalf("an exact hit must not normalize, got %s", got.Normalization)
		}
		if sel := sliceRunes(quoteFixtureSnapshot, got.SpanStart, got.SpanEnd); sel != quote {
			t.Fatalf("span must select the original text; got %q", sel)
		}
	})

	t.Run("full-width digits verify through NFKC and map back", func(t *testing.T) {
		// The quote uses half-width digits; the snapshot uses full-width ones. NFKC
		// folds them, so the match exists only in the normalized text — and the
		// reported span must still select the full-width original.
		quote := "2023年，Müller 集团的 Straße 业务"
		got := reportCheckQuote(quote, quoteFixtureSnapshot)
		if got.Status != reportQuoteVerified || got.SpanMap != reportSpanMapExact {
			t.Fatalf("want verified/exact, got %s/%s (candidates %d)", got.Status, got.SpanMap, got.Candidates)
		}
		if got.Normalization != "nfkc_cf" {
			t.Fatalf("want nfkc_cf path, got %s", got.Normalization)
		}
		sel := sliceRunes(quoteFixtureSnapshot, got.SpanStart, got.SpanEnd)
		if sel != "２０２３年，Müller 集团的 Straße 业务" {
			t.Fatalf("span must select the full-width original, got %q", sel)
		}
	})

	t.Run("a match cutting through the ss expansion is ambiguous, never guessed", func(t *testing.T) {
		// `Straße` normalizes to `strasse`; this quote covers only the first `s` of
		// the expansion, so no original range maps to it exactly. The correct answer
		// is "ambiguous", not a highlight at a drifted offset.
		quote := "stras"
		got := reportCheckQuote(quote, quoteFixtureSnapshot)
		if got.Status != reportQuoteAmbiguous {
			t.Fatalf("want ambiguous, got %s", got.Status)
		}
		if got.SpanMap != reportSpanMapPartial && got.SpanMap != reportSpanMapNone {
			t.Fatalf("want partial or none, got %s", got.SpanMap)
		}
	})
}

func TestReportAssuranceQuoteNotFoundAndNotProvidedAreDifferentAnswers(t *testing.T) {
	notFound := reportCheckQuote("这句话不在快照里", quoteFixtureSnapshot)
	if notFound.Status != reportQuoteNotFound {
		t.Fatalf("want not_found, got %s", notFound.Status)
	}
	notProvided := reportCheckQuote("   ", quoteFixtureSnapshot)
	if notProvided.Status != reportQuoteNotProvided {
		t.Fatalf("want not_provided, got %s", notProvided.Status)
	}
	// The distinction is load-bearing: not_found raises a finding against the
	// report (the model fabricated a citation), not_provided does not (the model
	// declined to quote).
	if verdict, reason := reportClampVerdict(model.ReportVerdictSupported, notFound, "fact", true); verdict != model.ReportVerdictAbstained || reason != model.ReportAbstainQuoteNotFound {
		t.Fatalf("not_found must abstain as quote_not_found, got %s/%s", verdict, reason)
	}
	if verdict, reason := reportClampVerdict(model.ReportVerdictSupported, notProvided, "inference", false); verdict != model.ReportVerdictPartiallySupported || reason != "" {
		t.Fatalf("not_provided without requires_evidence caps at partially_supported, got %s/%s", verdict, reason)
	}
}

func TestReportAssuranceVerifiedQuoteCanStillContradict(t *testing.T) {
	// The §5.1.1 counter-example, as a test: the quote is verbatim (layer ①
	// passes at the strongest possible level) and it refutes the claim. This is why
	// `verified` unlocks the semantic verdict instead of implying `supported`.
	quote := "其中 9 家已完成整改"
	check := reportCheckQuote(quote, quoteFixtureSnapshot)
	if check.Status != reportQuoteVerified {
		t.Fatalf("the quote must verify, got %s", check.Status)
	}
	// Layer ③/④ would return `conflicting`; the clamp must pass it through
	// untouched rather than downgrade it to `partially_supported`.
	verdict, reason := reportClampVerdict(model.ReportVerdictConflicting, check, "fact", true)
	if verdict != model.ReportVerdictConflicting || reason != "" {
		t.Fatalf("a verified quote must not alter a semantic verdict, got %s/%s", verdict, reason)
	}
}

func TestReportAssuranceQuoteDegradeMatrix(t *testing.T) {
	cases := []struct {
		name       string
		check      reportQuoteCheck
		verdict    string
		claimType  string
		require    bool
		want       string
		wantReason string
	}{
		{"verified passes supported", reportQuoteCheck{Status: reportQuoteVerified},
			model.ReportVerdictSupported, "fact", true, model.ReportVerdictSupported, ""},
		{"ambiguous caps supported", reportQuoteCheck{Status: reportQuoteAmbiguous},
			model.ReportVerdictSupported, "fact", true, model.ReportVerdictPartiallySupported, ""},
		{"ambiguous does not raise unsupported", reportQuoteCheck{Status: reportQuoteAmbiguous},
			model.ReportVerdictUnsupported, "fact", true, model.ReportVerdictUnsupported, ""},
		{"not_found abstains", reportQuoteCheck{Status: reportQuoteNotFound},
			model.ReportVerdictSupported, "fact", true, model.ReportVerdictAbstained, model.ReportAbstainQuoteNotFound},
		{"missing quote on a fact abstains", reportQuoteCheck{Status: reportQuoteNotProvided},
			model.ReportVerdictSupported, "fact", true, model.ReportVerdictAbstained, model.ReportAbstainNoQuote},
		{"missing quote on an inference caps", reportQuoteCheck{Status: reportQuoteNotProvided},
			model.ReportVerdictSupported, "inference", false, model.ReportVerdictPartiallySupported, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, reason := reportClampVerdict(tc.verdict, tc.check, tc.claimType, tc.require)
			if verdict != tc.want || reason != tc.wantReason {
				t.Fatalf("want %s/%q, got %s/%q", tc.want, tc.wantReason, verdict, reason)
			}
		})
	}
}

func TestReportAssuranceQuoteAmbiguousWhenMultipleEqualMatches(t *testing.T) {
	snapshot := "结论如下。风险可控。结论如下。"
	got := reportCheckQuote("结论如下。", snapshot)
	if got.Status != reportQuoteAmbiguous || got.Candidates != 2 {
		t.Fatalf("two equal-length matches must be ambiguous, got %s with %d candidates", got.Status, got.Candidates)
	}
	if got.SpanMap != reportSpanMapNone {
		t.Fatalf("an ambiguous match must not carry a span map, got %s", got.SpanMap)
	}
}

func TestReportAssuranceQuoteWhitespaceIsNotAHallucination(t *testing.T) {
	// A quote re-wrapped by a PDF copy must still verify: collapsing whitespace is
	// the whole reason a normalized path exists.
	got := reportCheckQuote("12 家子公司\n承担", quoteFixtureSnapshot)
	if got.Status != reportQuoteVerified {
		t.Fatalf("whitespace-insensitive match must verify, got %s", got.Status)
	}
}

func sliceRunes(s string, start, end int) string {
	runes := []rune(s)
	if start < 0 || end > len(runes) || start > end {
		return ""
	}
	return string(runes[start:end])
}

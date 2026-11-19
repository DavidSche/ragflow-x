package service

import (
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The claim appendix and cross-section disclosure in the delivered files (doc/288
// §9.2 / §9.7 / §12, A43).
//
// All three renderers (Markdown, DOCX, PDF) consume the same builders, so these
// tests pin the *words* once: the enumeration, the denominator, the "（未引用证据）"
// marker, and the fixed preamble that says a rule check is not a truth claim.

func TestBuildReportClaimAppendixCountsItsOwnDenominator(t *testing.T) {
	titles := map[string]string{"sec-1": "逐条符合性判定", "sec-2": "整改建议"}
	claims := []model.ReportClaim{
		{SectionID: "sec-1", SectionOrd: 3, Ord: 1, ClaimText: "住宿标准为每晚 600 元[1]。",
			ClaimType: model.ReportClaimTypeFact, SupportStatus: model.ReportClaimSupportSupported},
		{SectionID: "sec-1", SectionOrd: 3, Ord: 2, ClaimText: "保留期为 90 天[1]、[2]。",
			ClaimType: model.ReportClaimTypeFact, SupportStatus: model.ReportClaimSupportPartiallySupported},
		{SectionID: "sec-1", SectionOrd: 3, Ord: 3, ClaimText: "执行记录缺失。",
			ClaimType: model.ReportClaimTypeLimitation, SupportStatus: model.ReportClaimSupportNotRequired},
		{SectionID: "sec-2", SectionOrd: 5, Ord: 1, ClaimText: "建议每季度复核一次[1]。",
			ClaimType: model.ReportClaimTypeRecommendation, SupportStatus: model.ReportClaimSupportNotRequired},
	}
	entries, scored, notRequired := buildReportClaimAppendix(claims, titles)
	if len(entries) != len(claims) {
		t.Fatalf("every claim is printed, not a sample: want %d, got %d", len(claims), len(entries))
	}
	// `not_required` is counted separately and never enters the rate: those sentences
	// never claimed support, and including them would manufacture a number.
	if scored != 2 || notRequired != 2 {
		t.Fatalf("want 2 scored / 2 not required, got %d / %d", scored, notRequired)
	}
	if entries[0].SectionTitle != "逐条符合性判定" {
		t.Fatalf("the appendix names the chapter, got %q", entries[0].SectionTitle)
	}
	if entries[3].SectionTitle != "整改建议" {
		t.Fatalf("the title follows the claim's own section, got %q", entries[3].SectionTitle)
	}
	// Markers are re-read from the stored text: sorted, deduped, and never invented.
	if len(entries[1].Markers) != 2 || entries[1].Markers[0] != 1 || entries[1].Markers[1] != 2 {
		t.Fatalf("markers must come from the text: %+v", entries[1].Markers)
	}
	if len(entries[2].Markers) != 0 {
		t.Fatalf("a claim without markers has none, got %+v", entries[2].Markers)
	}

	// A claim whose section row is gone still gets an entry: a shorter appendix would
	// silently hide a sentence of the report.
	orphan, _, _ := buildReportClaimAppendix([]model.ReportClaim{
		{SectionID: "sec-missing", SectionOrd: 7, Ord: 1, ClaimText: "结论句。",
			ClaimType: model.ReportClaimTypeFact, SupportStatus: model.ReportClaimSupportUnsupported},
	}, titles)
	if len(orphan) != 1 || orphan[0].SectionTitle != "" {
		t.Fatalf("a missing title is left empty rather than guessed: %+v", orphan)
	}
}

func TestReportClaimGroupsSpeakForAllThreeRenderers(t *testing.T) {
	rate := 1.0
	document := &reportDocument{
		Claims: []reportClaimEntry{
			{SectionOrd: 3, SectionTitle: "逐条符合性判定", Ord: 1,
				Text: "住宿标准为每晚 600 元[1]。", Type: model.ReportClaimTypeFact,
				Support: model.ReportClaimSupportSupported, Markers: []int{1}},
			{SectionOrd: 3, SectionTitle: "逐条符合性判定", Ord: 2,
				Text: "住宿标准为每晚 900 元。", Type: model.ReportClaimTypeFact,
				Support: model.ReportClaimSupportUnsupported},
			{SectionOrd: 5, Ord: 1, Text: "建议每季度复核一次。",
				Type: model.ReportClaimTypeRecommendation, Support: model.ReportClaimSupportNotRequired},
		},
		ClaimScored: 1, ClaimNotRequired: 1, ClaimSupportRate: &rate,
	}
	groups := reportClaimGroups(document)
	if len(groups) != 2 {
		t.Fatalf("claims are grouped per chapter, got %+v", groups)
	}
	if groups[1].Title != "第 5 节" {
		t.Fatalf("a chapter without a title still gets one from its ord, got %q", groups[1].Title)
	}
	first := strings.Join(groups[0].Headings, "\n")
	// The enum never reaches the file: the reader sees words.
	if strings.Contains(first, "partially_supported") || strings.Contains(first, "not_required") {
		t.Fatalf("raw enum leaked into the appendix: %q", first)
	}
	if !strings.Contains(first, "事实陈述 · 可核实") {
		t.Fatalf("each line carries type and support, got %q", first)
	}
	// A factual claim with no reference is the case the appendix exists for; leaving
	// the line bare is exactly the opacity it is meant to remove.
	if !strings.Contains(first, "（未引用证据）") {
		t.Fatalf("an uncited factual claim must say so: %q", first)
	}
	// Advice is not scored, so it carries no "uncited" accusation.
	if strings.Contains(groups[1].Headings[0], "（未引用证据）") {
		t.Fatalf("a recommendation is not a claim about the sources: %q", groups[1].Headings[0])
	}

	if got := reportClaimSummary(document); !strings.Contains(got, "1 / 1") {
		t.Fatalf("the summary carries its denominator, got %q", got)
	}
	// "0 of 0" is not "0%": with nothing scored the summary states the count only.
	document.ClaimSupportRate = nil
	if got := reportClaimSummary(document); !strings.Contains(got, "3 句结论") ||
		strings.Contains(got, "%") {
		t.Fatalf("an unscored appendix must not invent a percentage, got %q", got)
	}
}

func TestReportClaimLabelsFallBackToTheRawValue(t *testing.T) {
	// An unknown value is printed as-is rather than guessed at: a wrong Chinese word
	// for a support status is worse than the identifier a maintainer can grep.
	if got := reportClaimTypeLabel("future_type"); got != "future_type" {
		t.Fatalf("want the raw value, got %q", got)
	}
	if got := reportClaimSupportLabel("future_status"); got != "future_status" {
		t.Fatalf("want the raw value, got %q", got)
	}
	if !strings.Contains(reportClaimAppendixPreamble, "不代表") {
		t.Fatalf("the fixed preamble must say what support status does not certify: %q",
			reportClaimAppendixPreamble)
	}
}

func TestReportConsistencyLineNamesSectionsAndValues(t *testing.T) {
	line := reportConsistencyLine(reportConsistencyIssue{
		Kind: reportFactKindNumber, Label: "住宿标准", Ords: []int{3, 5}, Values: []string{"600元", "800元"},
	})
	for _, want := range []string{"住宿标准", "第 3 节", "第 5 节", "600元", "800元"} {
		if !strings.Contains(line, want) {
			t.Fatalf("consistency line must contain %q: %q", want, line)
		}
	}
}

func TestRenderReportMarkdownCarriesClaimsAndConsistency(t *testing.T) {
	rate := 0.5
	document := &reportDocument{
		Schema: reportDocumentSchema, Title: "季度合规检查", Revision: 1,
		Completeness: model.ReportCompletenessFull,
		Sections: []reportDocumentSection{
			{Ord: 3, SectionKey: "compliance", Title: "逐条符合性判定", ContentMD: "住宿标准为每晚 600 元[1]。",
				SectionVersion: 1, EvidenceStatus: model.ReportEvidenceConflicting},
		},
		Claims: []reportClaimEntry{
			{SectionOrd: 3, SectionTitle: "逐条符合性判定", Ord: 1,
				Text: "住宿标准为每晚 600 元[1]。", Type: model.ReportClaimTypeFact,
				Support: model.ReportClaimSupportSupported, Markers: []int{1}},
		},
		ClaimScored: 1, ClaimNotRequired: 0, ClaimSupportRate: &rate,
		ConsistencyIssues: []reportConsistencyIssue{
			{Kind: reportFactKindNumber, Label: "住宿标准", Ords: []int{3, 5}, Values: []string{"600元", "800元"}},
		},
		ConsistencyKeysChecked: 4,
	}
	rendered := renderReportMarkdown(document, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"## 结论清单", reportClaimAppendixPreamble, "## 跨章节一致性提示", "第 5 节",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("markdown must contain %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "结论清单") {
		t.Fatal("the table of contents must list the claim appendix")
	}
}

func TestScoreTypeLabelNeverPrintsABareNumber(t *testing.T) {
	// A score without its type is a number nobody can compare: vector, term and
	// hybrid scores are not on one scale (§7.5).
	cases := map[string]string{
		model.ReportRetrievalScoreVector: "向量",
		model.ReportRetrievalScoreTerm:   "词项",
		model.ReportRetrievalScoreHybrid: "混合",
		// An unknown value has no Chinese name: `scoreLabel` prints the raw word
		// instead of guessing which signal produced the number.
		"similarity": "",
		"":           "",
	}
	for scoreType, want := range cases {
		if got := scoreTypeLabel(scoreType); got != want {
			t.Fatalf("%q: want %q, got %q", scoreType, want, got)
		}
	}
}

func TestReportChunkScoreTypeReadsTheEnginesThreeScores(t *testing.T) {
	cases := []struct {
		name  string
		chunk map[string]interface{}
		want  string
	}{
		// RAGFlow v1.0.0 answers all three at once; the hybrid value is the one the
		// ranking used, so it is what the citation must record.
		{"all three present", map[string]interface{}{
			"similarity": 0.81, "vector_similarity": 0.72, "term_similarity": 0.35,
		}, model.ReportRetrievalScoreHybrid},
		{"vector only", map[string]interface{}{"vector_similarity": 0.72},
			model.ReportRetrievalScoreVector},
		{"term only", map[string]interface{}{"term_similarity": 0.35},
			model.ReportRetrievalScoreTerm},
		// A lone `similarity` is the engine's fused score. Filing it as `vector`
		// would claim a comparability the number does not have.
		{"lone similarity is the fused score", map[string]interface{}{"similarity": 0.9},
			model.ReportRetrievalScoreHybrid},
		// The closed set stays closed: an unrecorded type is filed as the fused score
		// rather than left blank, because a blank in the appendix reads as "unit
		// unknown" instead of "unrecorded".
		{"nothing scored", map[string]interface{}{}, model.ReportRetrievalScoreHybrid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reportScoreTypeOrHybrid(reportChunkScoreType(tc.chunk))
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

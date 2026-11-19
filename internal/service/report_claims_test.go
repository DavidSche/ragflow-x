package service

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Claim extraction and support scoring (doc/288 §9.2 R11 / §9.1 L3).
//
// Two things these tests exist to prevent:
//
//   - the extractor drifting into a second model call. The claim set must be a pure
//     function of the body and the evidence, or a published report's own appendix
//     changes when nobody edited the report;
//   - `support_status = supported` being read as "this sentence is true". It means
//     "every number and date in this sentence appears in a chunk it cites", and the
//     negative cases below are what keep that statement honest.

func TestReportClaimTypeOfReadsTheMostCautiousMatch(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		// A recommendation that cites a checkable number is still a recommendation:
		// scoring it would certify advice as fact.
		{"建议将住宿标准调整为每晚 500 元[1]。", model.ReportClaimTypeRecommendation},
		{"应尽快补齐执行记录。", model.ReportClaimTypeRecommendation},
		// A stated gap is not an assertion about the sources either.
		{"现有资料缺少 2024 年度的执行记录。", model.ReportClaimTypeLimitation},
		{"该条款尚未明确适用范围。", model.ReportClaimTypeLimitation},
		// An explicit hedge outranks the factual reading of the same sentence.
		{"预计 2025 年将新增 3 个审批环节[2]。", model.ReportClaimTypeInference},
		{"该做法可能带来合规风险。", model.ReportClaimTypeInference},
		// Only the unmarked sentence is a fact.
		{"住宿标准为每晚 600 元[1]。", model.ReportClaimTypeFact},
		{"审计日志保留 90 天。", model.ReportClaimTypeFact},
	}
	for _, tc := range cases {
		if got := reportClaimTypeOf(tc.text); got != tc.want {
			t.Fatalf("%q: want %q, got %q", tc.text, tc.want, got)
		}
	}
}

func TestReportClaimSegmentsDropStructureAndKeepContent(t *testing.T) {
	content := "# 第三章 差旅标准\n\n" +
		"- 住宿标准为每晚 600 元。\n" +
		"1. 交通标准为据实报销。\n" +
		"| 项目 | 标准 |\n" +
		"| --- | --- |\n" +
		"| 住宿 | 每晚 600 元 |\n" +
		"```\n" +
		"内网 IP 为 10.0.0.1，不得写入报告。\n" +
		"```\n" +
		"如下：\n"
	segments := reportClaimSegments(content)
	for _, segment := range segments {
		switch {
		case segment == "":
			t.Fatal("empty segment")
		case len([]rune(segment)) < reportClaimMinRunes:
			t.Fatalf("fragment kept as a claim: %q", segment)
		}
		// Headings restate the outline; counting them would inflate every section by
		// its own title.
		if segment == "第三章 差旅标准" || segment == "# 第三章 差旅标准" {
			t.Fatalf("heading leaked into the claim set: %q", segment)
		}
		// Code fences are not assertions about the corpus, and the deployment detail
		// inside one must never reach the appendix.
		if segment == "内网 IP 为 10.0.0.1，不得写入报告。" {
			t.Fatal("fenced code leaked into the claim set")
		}
		// Table separator rows carry no content.
		if segment == "--- ---" || segment == "---" {
			t.Fatalf("table separator leaked into the claim set: %q", segment)
		}
	}
	joined := ""
	for _, segment := range segments {
		joined += segment + "\n"
	}
	if !strings.Contains(joined, "住宿标准为每晚 600 元。") {
		t.Fatalf("list items must keep their assertion without the bullet: %q", joined)
	}
	if strings.Contains(joined, "- 住宿") {
		t.Fatalf("list marker must be stripped: %q", joined)
	}
	if !strings.Contains(joined, "住宿 每晚 600 元") {
		t.Fatalf("table content rows are claims, pipes flattened: %q", joined)
	}
}

func TestReportClaimSplitSentencesDoesNotSplitOnCommas(t *testing.T) {
	// Commas and semicolons join two halves of one assertion; splitting there would
	// leave the fact's label in the other half and break support scoring.
	got := reportClaimSplitSentences("住宿标准为每晚 600 元，交通费据实报销；超标部分自理。下一句在此！")
	want := []string{
		"住宿标准为每晚 600 元，交通费据实报销；超标部分自理。",
		"下一句在此！",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d sentences, got %d: %q", len(want), len(got), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("sentence %d: want %q, got %q", index, want[index], got[index])
		}
	}
}

func TestReportClaimSupportAnswersTheFourQuestionsHonestly(t *testing.T) {
	evidence := []reportEvidenceItem{
		{Index: 1, Content: "住宿标准为每晚 600 元人民币，超出部分由个人承担。"},
		{Index: 2, Content: "差旅报销须经部门负责人审批后提交财务。"},
	}
	cases := []struct {
		name      string
		claimType string
		text      string
		want      string
	}{
		{
			name:      "every stated fact is in the cited chunk",
			claimType: model.ReportClaimTypeFact,
			text:      "住宿标准为每晚 600 元[1]。",
			want:      model.ReportClaimSupportSupported,
		},
		{
			// One number present, one absent: the sentence is partly traceable, and
			// saying "supported" would certify the absent half too.
			name:      "some stated facts are missing",
			claimType: model.ReportClaimTypeFact,
			text:      "住宿标准为每晚 600 元，年累计不超过 12 次[1]。",
			want:      model.ReportClaimSupportPartiallySupported,
		},
		{
			name:      "the number is not in what it cites",
			claimType: model.ReportClaimTypeFact,
			text:      "住宿标准为每晚 900 元[1]。",
			want:      model.ReportClaimSupportUnsupported,
		},
		{
			// Qualitative claim: no numbers, decided by its own vocabulary.
			name:      "a qualitative claim is supported by its subject words",
			claimType: model.ReportClaimTypeFact,
			text:      "差旅报销须经部门审批[2]。",
			want:      model.ReportClaimSupportSupported,
		},
		{
			name:      "a factual claim citing nothing is unsupported",
			claimType: model.ReportClaimTypeFact,
			text:      "住宿标准为每晚 600 元。",
			want:      model.ReportClaimSupportUnsupported,
		},
		{
			// Advice is not a claim about the world, so it is never scored: folding it
			// into the rate would manufacture support out of sentences that never asked
			// for any.
			name:      "recommendations are not scored",
			claimType: model.ReportClaimTypeRecommendation,
			text:      "建议将住宿标准调整为每晚 500 元[1]。",
			want:      model.ReportClaimSupportNotRequired,
		},
		{
			name:      "inferences are not scored",
			claimType: model.ReportClaimTypeInference,
			text:      "预计 2025 年住宿成本上升 10%[1]。",
			want:      model.ReportClaimSupportNotRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fragments := make([]string, 0, 2)
			for _, marker := range reportClaimMarkers(tc.text, claimFragments(evidence)) {
				for _, item := range evidence {
					if item.Index == marker {
						fragments = append(fragments, item.Content)
					}
				}
			}
			if got := reportClaimSupport(tc.claimType, tc.text, fragments); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestReportClaimMarkersIgnoreDanglingNumbers: a marker with no matching evidence is
// a section defect A3c already reports. Linking a claim to a chunk that does not
// exist would hide it.
func TestReportClaimMarkersIgnoreDanglingNumbers(t *testing.T) {
	fragments := claimFragments([]reportEvidenceItem{
		{Index: 1, Content: "甲"}, {Index: 3, Content: "丙"},
	})
	got := reportClaimMarkers("见[3]、[1]、[7]与重复的[3]。", fragments)
	want := []int{1, 3}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("want %v (ascending, deduped), got %v", want, got)
		}
	}
}

func TestReportExtractClaimsNumbersItsSentencesAndCapsTheSection(t *testing.T) {
	evidence := []reportEvidenceItem{{Index: 1, Content: "住宿标准为每晚 600 元。"}}
	body := "住宿标准为每晚 600 元[1]。建议按季度复核一次[1]。本次排查未覆盖外包人员。"
	claims := reportExtractClaims(body, evidence)
	if len(claims) != 3 {
		t.Fatalf("want three claims, got %d: %+v", len(claims), claims)
	}
	for index, claim := range claims {
		if claim.Ord != index+1 {
			t.Fatalf("ord must be 1-based and sequential: %+v", claims)
		}
		if claim.Text == "" || claim.Type == "" || claim.Support == "" {
			t.Fatalf("claim %d is incomplete: %+v", index, claim)
		}
	}
	if claims[0].Support != model.ReportClaimSupportSupported {
		t.Fatalf("the cited fact must be supported, got %q", claims[0].Support)
	}
	if claims[1].Support != model.ReportClaimSupportNotRequired {
		t.Fatalf("the recommendation must not be scored, got %q", claims[1].Support)
	}

	long := ""
	for len([]rune(long)) < reportClaimMaxPerSection*20 {
		long += "住宿标准为每晚 600 元[1]。"
	}
	if capped := reportExtractClaims(long, evidence); len(capped) != reportClaimMaxPerSection {
		t.Fatalf("one section may not exceed %d claims, got %d", reportClaimMaxPerSection, len(capped))
	}
}

// TestReportClaimCitationLinksGiveAMarkerToOneClaim: `unique(section_id, ord)` forces
// a choice, and the first mention is the one whose sentence a reader will find the
// number in.
func TestReportClaimCitationLinksGiveAMarkerToOneClaim(t *testing.T) {
	links := reportClaimCitationLinks([]reportClaimDraft{
		{Ord: 1, Markers: []int{1, 2}},
		{Ord: 2, Markers: []int{2, 3}},
	})
	want := map[int]int{1: 1, 2: 1, 3: 2}
	if len(links) != len(want) {
		t.Fatalf("want %v, got %v", want, links)
	}
	for marker, ord := range want {
		if links[marker] != ord {
			t.Fatalf("marker %d: want claim %d, got %d", marker, ord, links[marker])
		}
	}
}

func TestReportMarkersInTextSortsAndDedupes(t *testing.T) {
	got := reportMarkersInText("见[3]、[1]、重复的[3]，以及[12]。")
	want := []int{1, 3, 12}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
	if markers := reportMarkersInText("无引用。"); len(markers) != 0 {
		t.Fatalf("a text without markers has none, got %v", markers)
	}
}

func claimFragments(items []reportEvidenceItem) map[int]string {
	fragments := make(map[int]string, len(items))
	for _, item := range items {
		fragments[item.Index] = item.Content
	}
	return fragments
}

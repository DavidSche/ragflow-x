package service

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Cross-section consistency and the conflict/outdated split (doc/288 §9.3 / §9.7 /
// R12 / R16).
//
// The detector is mechanical on purpose: no model, no clock, no database. These
// tests therefore pin the *judgement* rather than the plumbing — which values count
// as the same key, when two values are a conflict between sources instead of one
// document disagreeing with itself, and why the check reports its denominator.

func TestReportKeyedFactsPairValuesWithTheirLabel(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []reportFact
	}{
		{
			name: "written date without whitespace",
			text: "本制度自 2024年1月1日 起施行。",
			want: []reportFact{{Kind: reportFactKindDate, Label: "本制度", Value: "2024-01-01"}},
		},
		{
			name: "numeric date and written date are one key",
			text: "修订于 2024-01-01 生效。",
			want: []reportFact{{Kind: reportFactKindDate, Label: "修订于", Value: "2024-01-01"}},
		},
		{
			name: "duration keeps its unit",
			text: "报销须在 15 个工作日内提交。",
			want: []reportFact{{Kind: reportFactKindNumber, Label: "报销须", Value: "15个工作日"}},
		},
		{
			name: "日 folds into 天 but hours stay separate",
			text: "通知期为 30 日，审计保留 90 天，响应 2 小时。",
			want: []reportFact{
				{Kind: reportFactKindNumber, Label: "通知期", Value: "30天"},
				{Kind: reportFactKindNumber, Label: "审计保留", Value: "90天"},
				{Kind: reportFactKindNumber, Label: "响应", Value: "2小时"},
			},
		},
		{
			// "第 3 条" is a reference, not a fact: comparing references across
			// sections would report every outline as self-contradictory.
			name: "bare numbers are references, not facts",
			text: "详见第 3 条与第 12 款。",
			want: nil,
		},
		{
			// The date's own 年/月 must not be re-read as a duration: that turned
			// every dated document into a disagreement about "年 2024".
			name: "date units are consumed before durations",
			text: "依据 2023 年 11 月的通知。",
			want: []reportFact{{Kind: reportFactKindDate, Label: "依据", Value: "2023-11"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reportKeyedFacts(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("want %d facts, got %d: %+v", len(tc.want), len(got), got)
			}
			for index, want := range tc.want {
				if got[index] != want {
					t.Fatalf("fact %d: want %+v, got %+v", index, want, got[index])
				}
			}
			for _, fact := range got {
				if fact.Label == "" {
					continue
				}
				if runes := []rune(fact.Label); len(runes) > reportFactLabelRunes {
					t.Fatalf("label %q exceeds %d runes", fact.Label, reportFactLabelRunes)
				}
			}
		})
	}
}

func TestReportFactLabelStopsAtMarkdownBoundaries(t *testing.T) {
	// A label that starts with a pipe names a table cell, not a fact. Reading past
	// the boundary merged two columns into one key and reported table rows as
	// contradicting each other.
	facts := reportKeyedFacts("| 差旅标准 | 每晚 600 元 |")
	if len(facts) != 1 {
		t.Fatalf("want one fact from the table row, got %+v", facts)
	}
	// The cell itself has no label (the pipe is a boundary, and the quantifier is
	// stripped), so the row's first cell keys it — otherwise every table-authored
	// value would be invisible to the comparison.
	if !strings.HasPrefix(facts[0].Label, "差旅标准") {
		t.Fatalf("a table row is keyed by its first cell: %q", facts[0].Label)
	}
	// Two numbers in one row are two facts, not one key with two values: the
	// position suffix is what keeps a row from looking self-contradictory.
	row := reportKeyedFacts("| 住宿 | 每晚 600 元 | 每年 12 次 |")
	if len(row) != 2 || row[0].Label == row[1].Label {
		t.Fatalf("same-row values must not share a key: %+v", row)
	}
}

// TestReportEvidenceSignalsSplitConflictFromOutdated is the decision §9.3 leaves to
// the implementation: *conflicting* means two sources disagree, *outdated* means one
// document holds two versions' worth of text. Filing the second as the first blames
// the wrong thing, and the reviewer's next action differs (open two files vs.
// re-parse one).
func TestReportEvidenceSignalsSplitConflictFromOutdated(t *testing.T) {
	cases := []struct {
		name  string
		items []reportEvidenceItem
		want  []string
	}{
		{
			name: "two documents, two values",
			items: []reportEvidenceItem{
				{Index: 1, DocumentLabel: "制度A.md", Content: "审计日志保留期为 90 天。"},
				{Index: 2, DocumentLabel: "制度B.md", Content: "审计日志保留期为 180 天。"},
			},
			want: []string{reportSignalConflicting},
		},
		{
			name: "one document, two values",
			items: []reportEvidenceItem{
				{Index: 1, DocumentLabel: "制度A.md", Content: "审计日志保留期为 90 天。"},
				{Index: 2, DocumentLabel: "制度A.md", Content: "审计日志保留期为 180 天。"},
			},
			want: []string{reportSignalOutdated},
		},
		{
			name: "agreement is not a signal",
			items: []reportEvidenceItem{
				{Index: 1, DocumentLabel: "制度A.md", Content: "审计日志保留期为 90 天。"},
				{Index: 2, DocumentLabel: "制度B.md", Content: "审计日志须保留 90 天。"},
			},
			want: nil,
		},
		{
			// One chunk cannot disagree with itself, and a single source has
			// nothing to be compared against.
			name: "a single source never conflicts",
			items: []reportEvidenceItem{
				{Index: 1, DocumentLabel: "制度A.md", Content: "保留 90 天，亦保留 180 天。"},
			},
			want: nil,
		},
		{
			// An unkeyed value cannot be compared: "90 天" with no words naming it
			// could be a retention period or an approval deadline.
			name: "unkeyed values are not compared",
			items: []reportEvidenceItem{
				{Index: 1, DocumentLabel: "制度A.md", Content: "2024年1月1日。"},
				{Index: 2, DocumentLabel: "制度B.md", Content: "2025年1月1日。"},
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signals := reportEvidenceSignals(tc.items)
			got := reportEvidenceSignalKinds(signals)
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v (detail: %+v)", tc.want, got, signals)
			}
			for index := range tc.want {
				if got[index] != tc.want[index] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func TestReportEvidenceSignalCarriesSourcesAndDetail(t *testing.T) {
	signals := reportEvidenceSignals([]reportEvidenceItem{
		{Index: 1, DocumentLabel: "制度B.md", Content: "住宿标准为每晚 600 元。"},
		{Index: 2, DocumentLabel: "制度A.md", Content: "住宿标准为每晚 800 元。"},
	})
	if len(signals) != 1 {
		t.Fatalf("want one signal, got %+v", signals)
	}
	signal := signals[0]
	if signal.Detail == "" {
		t.Fatal("a conflict must name the competing values, or the reader cannot check it")
	}
	if !strings.Contains(signal.Detail, "600") || !strings.Contains(signal.Detail, "800") {
		t.Fatalf("detail must list both values: %q", signal.Detail)
	}
	if len(signal.Sources) != 2 || signal.Sources[0] != "制度A.md" || signal.Sources[1] != "制度B.md" {
		t.Fatalf("sources must be the cited documents, sorted: %+v", signal.Sources)
	}
}

// TestReportEvidenceStatusWithSignalsNeverUpgrades is the precedence rule: a
// conflict outranks the P0 verdict when the P0 verdict was a success, but nothing
// here may turn a gap into a pass.
func TestReportEvidenceStatusWithSignalsNeverUpgrades(t *testing.T) {
	signals := []reportEvidenceSignal{{Kind: reportSignalConflicting, Sources: []string{"A"}}}
	cases := []struct {
		base string
		want string
	}{
		{model.ReportEvidenceSupported, model.ReportEvidenceConflicting},
		{model.ReportEvidencePartiallySupported, model.ReportEvidenceConflicting},
		// Gaps stay gaps: an unsupported section cannot borrow severity from a
		// signal about evidence it never cited.
		{model.ReportEvidenceUnsupported, model.ReportEvidenceUnsupported},
		{model.ReportEvidenceNotRequired, model.ReportEvidenceNotRequired},
		// Signals never invent a status the caller did not ask about.
		{model.ReportEvidenceConflicting, model.ReportEvidenceConflicting},
	}
	for _, tc := range cases {
		if got := reportEvidenceStatusWithSignals(tc.base, signals); got != tc.want {
			t.Fatalf("base %q: want %q, got %q", tc.base, tc.want, got)
		}
	}
	if got := reportEvidenceStatusWithSignals(model.ReportEvidenceSupported, nil); got != model.ReportEvidenceSupported {
		t.Fatalf("no signals must not change the verdict, got %q", got)
	}
}

func TestReportEvidenceStatusSignalsSelectOutdatedWord(t *testing.T) {
	outdated := []reportEvidenceSignal{{Kind: reportSignalOutdated, Sources: []string{"A"}}}
	if got := reportEvidenceStatusWithSignals(model.ReportEvidenceSupported, outdated); got != model.ReportEvidenceOutdated {
		t.Fatalf("a stale-only section must read %q, got %q", model.ReportEvidenceOutdated, got)
	}
	both := []reportEvidenceSignal{
		{Kind: reportSignalOutdated, Sources: []string{"A"}},
		{Kind: reportSignalConflicting, Sources: []string{"A", "B"}},
	}
	if got := reportEvidenceStatusWithSignals(model.ReportEvidenceSupported, both); got != model.ReportEvidenceConflicting {
		t.Fatalf("conflict is the stronger accusation and must win, got %q", got)
	}
}

// TestReportCrossSectionConsistencyCountsItsDenominator pins the two claims the
// metric makes: which sections disagree, and how many keys were looked at. "0
// inconsistencies" over zero keys and over forty keys are different statements.
func TestReportCrossSectionConsistencyCountsItsDenominator(t *testing.T) {
	keys, issues := reportCrossSectionConsistency([]reportConsistencySource{
		{Ord: 1, Text: "住宿标准为每晚 600 元。"},
		{Ord: 2, Text: "住宿标准为每晚 600 元。"},
	})
	if len(issues) != 0 {
		t.Fatalf("identical values are not an inconsistency: %+v", issues)
	}
	if keys != 1 {
		t.Fatalf("the denominator must count the key that was compared, got %d", keys)
	}

	keys, issues = reportCrossSectionConsistency([]reportConsistencySource{
		{Ord: 1, Text: "住宿标准为每晚 600 元。"},
		{Ord: 2, Text: "住宿标准为每晚 800 元。"},
	})
	if keys != 1 || len(issues) != 1 {
		t.Fatalf("want one issue over one key, got %d keys / %+v", keys, issues)
	}
	issue := issues[0]
	if issue.Kind != reportFactKindNumber || !strings.Contains(issue.Label, "住宿标准") {
		t.Fatalf("issue must name the key: %+v", issue)
	}
	if len(issue.Ords) != 2 || issue.Ords[0] != 1 || issue.Ords[1] != 2 {
		t.Fatalf("issue must name both chapters: %+v", issue.Ords)
	}
	if len(issue.Values) != 2 || issue.Values[0] != "600元" || issue.Values[1] != "800元" {
		t.Fatalf("issue must list both values, sorted: %+v", issue.Values)
	}

	if keys, issues := reportCrossSectionConsistency(nil); keys != 0 || issues != nil {
		t.Fatalf("an empty report looks at nothing and reports nothing: %d / %+v", keys, issues)
	}
}

// TestReportCrossSectionConsistencyLeavesIntraSectionDisagreementToR12: one section
// holding both values is R12's finding (its own evidence signals), and reporting it
// twice would make a single stale chunk look like two chapters contradicting.
func TestReportCrossSectionConsistencyLeavesIntraSectionDisagreementToR12(t *testing.T) {
	keys, issues := reportCrossSectionConsistency([]reportConsistencySource{
		{Ord: 3, Text: "保留期为 90 天。保留期为 180 天。"},
	})
	if keys == 0 {
		t.Fatal("the key must still be compared")
	}
	if len(issues) != 0 {
		t.Fatalf("one section disagreeing with itself belongs to R12, got %+v", issues)
	}
}

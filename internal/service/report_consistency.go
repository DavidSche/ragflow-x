package service

// Evidence conflict / staleness detection and cross-section consistency for the
// report path (doc/288 §9.5 R12, §9.7 R16).
//
// R12 and R16 both answer a question P0 could only *ask*: "do the sources agree,
// and does the same report say the same thing twice?" P0 discharged that with a
// template declaration (`conflict_handling: disclose`) and a human reading the
// appendix. P1 has to actually look.
//
// Everything here is one idea applied twice: pull the *keyed facts* out of a text
// — a number or a date together with the words that name it — and then compare
// values that share a key. Two mechanisms fall out of it:
//
//   - inside one section's cited evidence, one key holding two values is either a
//     conflict (the values came from different documents) or staleness (one
//     document, two versions) — R12;
//   - across the sections of one report, one key holding two values is the report
//     contradicting itself — R16.
//
// Both are deliberately mechanical and deliberately conservative in what they
// *claim*. A keyed fact is not semantics: "住宿标准 500 元" and "住宿限额 500 元"
// are the same rule written twice and this detector sees two keys. That asymmetry
// is the right one for a disclosure surface — a missed conflict ships a report
// that reads confident about contradictory sources, while an extra "来源可能冲突"
// costs a reader ten seconds and no trust.
//
// What is *not* here: any model call, any clock, any database read. A detector that
// consulted "now" or a provider would make the same stored report disclose
// different things on different days; §7.4's whole premise is that a published
// report reads the same forever (§9.5 "P1 自动识别并进审计" is about *when* it is
// computed, not about making it unstable).

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Keyed-fact kinds. A date and a number of the same label ("修订日期 2024-01-01"
// vs "修订日期 90 天") are not comparable, so the kind is part of the key.
const (
	reportFactKindNumber = "number"
	reportFactKindDate   = "date"
)

// Detection kinds of one evidence signal. They are the two `evidence_status`
// values R12 adds to the P0 set (§9.3), reused verbatim so the badge on screen and
// the stored verdict cannot drift apart.
const (
	reportSignalConflicting = "conflicting"
	reportSignalOutdated    = "outdated"
)

// reportKeyedFactLimit caps how many facts one text contributes. The bound exists
// because consistency is quadratic in the number of values per key and a runaway
// body (or a pasted table) would make finalize expensive for no extra answer.
const reportKeyedFactLimit = 120

// reportFactLabelRunes is how much of the text in front of the value names it. Six
// runes covers the labels the corpus actually uses ("保留期", "住宿标准", "生效日期")
// without swallowing the previous clause; longer labels start merging unrelated
// facts into one key, which reports conflicts that are really two different rules.
const reportFactLabelRunes = 6

var (
	// reportDateFactPattern matches the written Chinese date forms the corpus uses
	// ("2024 年 1 月 1 日", "2024年1月"). Numeric form (2024-01-01) is matched by
	// the second pattern so a document that writes dates both ways still yields one
	// key.
	reportDateFactPattern = regexp.MustCompile(`(\d{4})\s*年\s*(\d{1,2})\s*月(?:\s*(\d{1,2})\s*日)?`)
	reportISOFactPattern  = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})`)
	// reportNumberFactPattern matches "N unit". The unit list is closed and drawn
	// from the compliance/HR corpus: a bare number with no unit ("第 3 条") is a
	// reference, not a fact, and comparing references across sections would report
	// every outline as self-contradictory.
	reportNumberFactPattern = regexp.MustCompile(
		`(\d+(?:\.\d+)?)\s*(个工作日|工作日|个月|天|日|小时|分钟|次|人|万元|元|%|周|月|年)`)
)

// reportFact is one comparable value with the words that key it.
type reportFact struct {
	Kind  string
	Label string
	Value string
}

// reportKeyedFact is a fact plus where it was found, which is what a disclosure
// needs: "冲突" without a source list is an accusation the reader cannot check.
type reportKeyedFact struct {
	reportFact
	Source string
	Ord    int
}

// reportKeyedFacts extracts the comparable facts of one text.
//
// The two passes are ordered, not independent: a date's "年"/"月" would otherwise
// be read as a duration ("2024 年" ⇒ number 2024 with unit 年), so date spans are
// consumed before durations are considered. Skipping that check produced a
// detector that reported every dated document as disagreeing about "年 2024".
func reportKeyedFacts(text string) []reportFact {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	facts := make([]reportFact, 0, 8)
	consumed := make([][2]int, 0, 8)
	overlaps := func(start, end int) bool {
		for _, span := range consumed {
			if start < span[1] && end > span[0] {
				return true
			}
		}
		return false
	}
	// A table row's cells are separated by pipes, which `reportFactLabel` treats as
	// a boundary — so a cell-authored value ("| 差旅标准 | 每晚 600 元 |") comes back
	// with no label at all and would be dropped from every comparison. Report bodies
	// are full of such tables, so the row's first cell is used as the fallback key,
	// suffixed with the value's position inside the row: without the suffix two
	// different numbers of one row would look like one key with two values and be
	// reported as the document disagreeing with itself.
	rowLabel := reportTableRowLabel(text)
	label := func(fact reportFact, ordinal int) string {
		if fact.Label != "" || rowLabel == "" {
			return fact.Label
		}
		return rowLabel + "·" + strconv.Itoa(ordinal)
	}
	for _, match := range reportDateFactPattern.FindAllStringSubmatchIndex(text, -1) {
		fact := reportFact{
			Kind:  reportFactKindDate,
			Label: reportFactLabel(text, match[0]),
			Value: reportDateValue(text, match),
		}
		fact.Label = label(fact, len(facts)+1)
		facts = append(facts, fact)
		consumed = append(consumed, [2]int{match[0], match[1]})
	}
	for _, match := range reportISOFactPattern.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(match[0], match[1]) {
			continue
		}
		fact := reportFact{
			Kind:  reportFactKindDate,
			Label: reportFactLabel(text, match[0]),
			Value: reportDateValue(text, match),
		}
		fact.Label = label(fact, len(facts)+1)
		facts = append(facts, fact)
		consumed = append(consumed, [2]int{match[0], match[1]})
	}
	for _, match := range reportNumberFactPattern.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(match[0], match[1]) {
			continue
		}
		fact := reportFact{
			Kind:  reportFactKindNumber,
			Label: reportFactLabel(text, match[0]),
			Value: reportNumberValue(text[match[2]:match[3]], text[match[4]:match[5]]),
		}
		fact.Label = label(fact, len(facts)+1)
		facts = append(facts, fact)
		if len(facts) >= reportKeyedFactLimit {
			break
		}
	}
	return facts
}

// reportTableRowLabel returns the first cell of a markdown table row, which is what
// names the values in that row. An empty answer means the text is not a table row
// (or its header cell is blank), in which case there is no fallback key to offer.
func reportTableRowLabel(text string) string {
	line := strings.TrimSpace(text)
	if !strings.HasPrefix(line, "|") {
		return ""
	}
	cells := strings.Split(line, "|")
	if len(cells) < 3 {
		return ""
	}
	cell := strings.TrimSpace(cells[1])
	if cell == "" || reportClaimIsTableSeparator(cell) {
		return ""
	}
	if runes := []rune(cell); len(runes) > reportFactLabelRunes {
		cell = string(runes[:reportFactLabelRunes])
	}
	return cell
}

// reportDateValue normalises a matched date to `YYYY-MM-DD` so "2024年1月" and
// "2024 年 01 月" compare equal.
func reportDateValue(text string, match []int) string {
	year, _ := strconv.Atoi(text[match[2]:match[3]])
	month, _ := strconv.Atoi(text[match[4]:match[5]])
	day := 0
	if len(match) > 7 && match[6] >= 0 && match[7] > match[6] {
		day, _ = strconv.Atoi(text[match[6]:match[7]])
	}
	if day > 0 {
		return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	}
	return fmt.Sprintf("%04d-%02d", year, month)
}

// reportNumberValue is "the number and its unit, with nothing between them" —
// `90 天` and `90天` are one value, and `90 天` and `90 小时` are two.
//
// 日 is folded into 天 because the corpus writes both for one rule; every other
// unit is kept verbatim. Folding more (个月→月) would make a duration compare equal
// to a calendar month, which is a different claim.
func reportNumberValue(number, unit string) string {
	normalized := strings.TrimSpace(number)
	if normalized == "" {
		return ""
	}
	if unit == "日" {
		unit = "天"
	}
	return normalized + unit
}

// reportFactLabel reads the words in front of a value.
//
// It walks back to the previous punctuation or space, strips the connective
// characters that carry no meaning ("为", "是", "的" …), and keeps the tail. The
// tail rather than the head on purpose: Chinese modifiers lead ("公司差旅住宿标准
// 500 元"), so the noun that identifies the fact is the *last* words before the
// value.
func reportFactLabel(text string, start int) string {
	if start <= 0 || start > len(text) {
		return ""
	}
	prefix := text[:start]
	runes := []rune(prefix)
	// The character immediately before a value is usually a space ("住宿标准为 600
	// 元"), so trailing whitespace is skipped before looking for a boundary. Walking
	// back from `len(runes)` reported an empty label for every spaced number.
	end := len(runes)
	for end > 0 && unicode.IsSpace(runes[end-1]) {
		end--
	}
	begin := end
	for begin > 0 && !reportLabelBoundary(runes[begin-1]) {
		begin--
	}
	label := strings.TrimSpace(string(runes[begin:end]))
	label = reportTrimFactLabel(label)
	// Quantifiers in front of the value are not part of what the fact *is*: "住宿
	// 标准为每晚 600 元" and "住宿标准为 800 元" state the same rule at different
	// numbers, so both must key to "住宿标准" or the comparison never happens.
	for {
		trimmed := label
		for _, quantifier := range reportFactLabelQuantifiers {
			trimmed = strings.TrimSuffix(trimmed, quantifier)
		}
		trimmed = reportTrimFactLabel(strings.TrimSpace(trimmed))
		if trimmed == label {
			break
		}
		label = trimmed
	}
	label = strings.TrimSpace(label)
	if runes := []rune(label); len(runes) > reportFactLabelRunes {
		label = string(runes[len(runes)-reportFactLabelRunes:])
	}
	return strings.TrimSpace(label)
}

// reportFactLabelConnectives are the trailing characters that join a label to its
// value ("住宿标准为 600 元", "本制度自 2024年1月1日"). They are stripped only while
// two runes remain: several of them are also the last character of a noun, and a
// blind TrimRight turned "响应" into "响" and "相应" into "相".
const reportFactLabelConnectives = "为是的有约至在已将与和及共计达不也都要需应可会能自"

// reportTrimFactLabel removes trailing connectives without emptying the label.
func reportTrimFactLabel(label string) string {
	for len([]rune(label)) > 2 {
		trimmed := strings.TrimRight(label, reportFactLabelConnectives)
		if trimmed == label || len([]rune(trimmed)) < 2 {
			break
		}
		label = trimmed
	}
	return label
}

// reportFactLabelQuantifiers are the trailing words that scope a value without
// naming the fact. They are stripped after the connectives so "每晚 600 元" and
// "600 元" compare under one key.
var reportFactLabelQuantifiers = []string{"每晚", "每月", "每年", "每日", "每人", "每次", "每"}

// reportLabelBoundaries lists the characters that end a label. Everything a
// sentence can be broken by is a boundary, including the markdown a body is full
// of: a label that starts with "**" or "|" names a table cell, not a fact.
var reportLabelBoundaries = "，,。.；;：:、|（）()“”‘’" + "\"" + "'！!？?—…《》【】*-<#>"

// reportLabelBoundary ends a label: whitespace, sentence punctuation, and every
// markdown character a body is full of — a label that starts with `**` or `|`
// names a table cell, not a fact.
func reportLabelBoundary(char rune) bool {
	if unicode.IsSpace(char) {
		return true
	}
	return strings.ContainsRune(reportLabelBoundaries, char)
}

// reportFactGroupKey is the identity of "the same fact said twice".
func reportFactGroupKey(fact reportFact) string {
	return fact.Kind + "\x00" + fact.Label
}

// reportEvidenceSignal is one disclosure attached to a section: which kind of
// trouble was found and which sources disagree.
type reportEvidenceSignal struct {
	Kind string `json:"kind"`
	// Sources are document labels, deduplicated and sorted, because the reader's
	// next action is "open these two files".
	Sources []string `json:"sources"`
	// Detail names the key and the competing values. It is machine-shaped
	// ("保留期: 90天 / 60天") rather than prose: the UI owns the sentence.
	Detail string `json:"detail,omitempty"`
}

// reportEvidenceSignals inspects one section's cited evidence for R12.
//
// Splitting conflict from staleness is the whole judgement here:
//
//   - the same key holds different values **from different documents** ⇒
//     `conflicting`: two sources in the knowledge base disagree and the writer had
//     to pick one;
//   - the same key holds different values **from one document** ⇒ `outdated`: one
//     document, two versions' worth of text. That is the "文档版本早于修订" case
//     §9.3 describes, and calling it a conflict between sources would blame the
//     wrong thing.
//
// Only cited evidence participates. Flagging chunks the writer never used would
// report a conflict about text that is not in the report.
func reportEvidenceSignals(items []reportEvidenceItem) []reportEvidenceSignal {
	if len(items) < 2 {
		return nil
	}
	type valueSources struct {
		documents map[string]struct{}
	}
	groups := make(map[string]map[string]*valueSources, 8)
	labels := make(map[string]string, 8)
	for _, item := range items {
		source := strings.TrimSpace(item.DocumentLabel)
		if source == "" {
			source = strings.TrimSpace(item.ChunkID)
		}
		text := item.SourceContent
		if strings.TrimSpace(text) == "" {
			text = item.Content
		}
		for _, fact := range reportKeyedFacts(text) {
			if fact.Label == "" {
				// An unkeyed fact cannot be compared: "90 天" with no words naming it
				// could be a retention period or an approval deadline.
				continue
			}
			key := reportFactGroupKey(fact)
			values, ok := groups[key]
			if !ok {
				values = make(map[string]*valueSources, 2)
				groups[key] = values
			}
			entry, ok := values[fact.Value]
			if !ok {
				entry = &valueSources{documents: make(map[string]struct{}, 2)}
				values[fact.Value] = entry
			}
			entry.documents[source] = struct{}{}
			labels[key] = fact.Label
		}
	}
	if len(groups) == 0 {
		return nil
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	signals := make([]reportEvidenceSignal, 0, 2)
	conflicting, outdated := []string{}, []string{}
	for _, key := range keys {
		values := groups[key]
		if len(values) < 2 {
			continue
		}
		documents := make(map[string]struct{}, 4)
		valueList := make([]string, 0, len(values))
		for value, entry := range values {
			valueList = append(valueList, value)
			for document := range entry.documents {
				documents[document] = struct{}{}
			}
		}
		sort.Strings(valueList)
		sources := make([]string, 0, len(documents))
		for document := range documents {
			sources = append(sources, document)
		}
		sort.Strings(sources)
		detail := labels[key] + ": " + strings.Join(valueList, " / ")
		if len(documents) < 2 {
			// One document holding both values: the section cited two versions of the
			// same file, so the older text is stale rather than contested.
			if !containsString(outdated, detail) {
				outdated = append(outdated, detail)
			}
			continue
		}
		if !containsString(conflicting, detail) {
			conflicting = append(conflicting, detail)
		}
	}
	if len(conflicting) > 0 {
		signals = append(signals, reportEvidenceSignal{
			Kind: reportSignalConflicting, Sources: reportSignalSources(items), Detail: strings.Join(conflicting, "; ")})
	}
	if len(outdated) > 0 {
		signals = append(signals, reportEvidenceSignal{
			Kind: reportSignalOutdated, Sources: reportSignalSources(items), Detail: strings.Join(outdated, "; ")})
	}
	return signals
}

// reportSignalSources lists the documents the section cited, sorted and deduped.
// A signal inherits the section's whole source list because the *pair* that
// disagrees is already named in `Detail`, and a per-signal source set small enough
// to be exact would need the detector to keep fact-level provenance it throws away.
func reportSignalSources(items []reportEvidenceItem) []string {
	seen := make(map[string]struct{}, len(items))
	sources := make([]string, 0, len(items))
	for _, item := range items {
		label := strings.TrimSpace(item.DocumentLabel)
		if label == "" {
			label = strings.TrimSpace(item.ChunkID)
		}
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		sources = append(sources, label)
	}
	sort.Strings(sources)
	return sources
}

// reportEvidenceStatusWithSignals folds R12's detection into the P0 verdict.
//
// Precedence is conflict → stale → the P0 answer. A conflict is the stronger
// accusation (two sources disagree), so a section that is both must not be filed
// under the milder word. Nothing here can *upgrade* a section: an `unsupported`
// section stays unsupported even if its (nonexistent) evidence were contested, and
// a `not_required` section only changes when it actually cited something.
func reportEvidenceStatusWithSignals(base string, signals []reportEvidenceSignal) string {
	if len(signals) == 0 {
		return base
	}
	switch base {
	case model.ReportEvidenceUnsupported, model.ReportEvidenceNotRequired:
		// `not_required` with citations was already reported as `supported` by the
		// P0 function, so reaching here with it means nothing was cited at all and
		// there is no evidence to contest.
		return base
	}
	for _, signal := range signals {
		if signal.Kind == reportSignalConflicting {
			return model.ReportEvidenceConflicting
		}
	}
	for _, signal := range signals {
		if signal.Kind == reportSignalOutdated {
			return model.ReportEvidenceOutdated
		}
	}
	return base
}

// reportConsistencySource is one section's contribution to the cross-section check.
type reportConsistencySource struct {
	Ord  int
	Text string
}

// reportConsistencyIssue is one self-contradiction of a report (R16): the same key
// carries different values in different sections.
type reportConsistencyIssue struct {
	Kind   string   `json:"kind"`
	Label  string   `json:"label"`
	Ords   []int    `json:"ords"`
	Values []string `json:"values"`
}

// reportCrossSectionConsistency compares the keyed facts of every section with
// each other.
//
// It returns the number of keys it actually looked at as well as the issues, for
// the same reason every other ratio in this package carries its denominator: "0
// inconsistencies" over zero keys and over forty keys are different claims, and a
// caller that cannot tell them apart will eventually render them the same way.
//
// Facts inside one section are not compared with themselves here — an internal
// contradiction is R12's job and already visible in that section's evidence
// signals. Comparing only across sections is what makes `ords` in the issue a
// meaningful "these two chapters say different things".
func reportCrossSectionConsistency(sources []reportConsistencySource) (int, []reportConsistencyIssue) {
	type valueOrds struct {
		ords map[int]struct{}
	}
	groups := make(map[string]map[string]*valueOrds, 16)
	labels := make(map[string]string, 16)
	for _, source := range sources {
		for _, fact := range reportKeyedFacts(source.Text) {
			if fact.Label == "" {
				continue
			}
			key := reportFactGroupKey(fact)
			values, ok := groups[key]
			if !ok {
				values = make(map[string]*valueOrds, 2)
				groups[key] = values
			}
			entry, ok := values[fact.Value]
			if !ok {
				entry = &valueOrds{ords: make(map[int]struct{}, 2)}
				values[fact.Value] = entry
			}
			entry.ords[source.Ord] = struct{}{}
			labels[key] = fact.Label
		}
	}
	if len(groups) == 0 {
		return 0, nil
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	issues := make([]reportConsistencyIssue, 0, 2)
	for _, key := range keys {
		values := groups[key]
		if len(values) < 2 {
			continue
		}
		// Which section said what: two values are only a cross-section
		// contradiction when they are in two different sections.
		valueOrds := make(map[string][]int, len(values))
		allOrds := make(map[int]struct{}, 4)
		for value, entry := range values {
			ords := make([]int, 0, len(entry.ords))
			for ord := range entry.ords {
				ords = append(ords, ord)
				allOrds[ord] = struct{}{}
			}
			sort.Ints(ords)
			valueOrds[value] = ords
		}
		if len(allOrds) < 2 {
			continue
		}
		distinctPerSection := make(map[int]map[string]struct{}, len(allOrds))
		for value, ords := range valueOrds {
			for _, ord := range ords {
				set, ok := distinctPerSection[ord]
				if !ok {
					set = make(map[string]struct{}, 2)
					distinctPerSection[ord] = set
				}
				set[value] = struct{}{}
			}
		}
		shared := false
		for _, set := range distinctPerSection {
			if len(set) > 1 {
				shared = true
				break
			}
		}
		// `shared` means one section alone holds both values — an internal
		// contradiction, already covered by the evidence signals. Only cross-section
		// disagreement belongs to R16.
		if shared {
			continue
		}
		ords := make([]int, 0, len(allOrds))
		for ord := range allOrds {
			ords = append(ords, ord)
		}
		sort.Ints(ords)
		sortedValues := make([]string, 0, len(values))
		for value := range values {
			sortedValues = append(sortedValues, value)
		}
		sort.Strings(sortedValues)
		issues = append(issues, reportConsistencyIssue{
			Kind:   strings.SplitN(key, "\x00", 2)[0],
			Label:  labels[key],
			Ords:   ords,
			Values: sortedValues,
		})
	}
	return len(groups), issues
}

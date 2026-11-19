package service

// Claim extraction and support scoring (doc/288 §9.2 R11, §9.1 L3, §9.7 R16).
//
// P0 discharged "the report must distinguish what it verified from what it
// inferred" with prose: the writer prompt demanded claim types and the appendix
// carried a fixed disclaimer. Both are true and both are unverifiable — nothing in
// the database said *which* sentence was an inference, so no UI could badge it, no
// metric could count it, and no reviewer could sample it. R11 is that gap: every
// section body is split into claims, each claim gets a type and a support status,
// and `rgx_report_claim` keeps a row per claim with `citation.claim_id` pointing
// back at it.
//
// The extractor is deliberately *not* a model call. Three reasons, in order of
// weight:
//
//  1. the claim set must be identical when the same body is re-read months later;
//     a second model call would make a published report's own appendix drift;
//  2. a claim row per sentence is the finest-grained thing in the report path, so
//     it is what a reviewer samples — a nondeterministic sampler cannot be audited;
//  3. the section body is already the product of a model call, and asking a model
//     to grade its own sentences has no independent signal.
//
// So the type comes from closed keyword sets and the support status comes from
// mechanical fact matching (numbers and dates the claim states, must appear in the
// evidence it cites). That is exactly the L3 "规则检查" of §9.1; the model-based
// scoring §9.1 marks as P1 is the *judgement*, and this package is honest about
// what it can and cannot say: `support_status = supported` means "every number and
// date in this sentence appears in a chunk it cites", never "this sentence is
// true".

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

const (
	// reportClaimMaxPerSection bounds the rows one section can write. A 400-word
	// body produces ~12 claims; the cap exists so a model that ignores its length
	// budget cannot turn one commit into thousands of rows.
	reportClaimMaxPerSection = 40
	// reportClaimMinRunes drops fragments that are not claims: a heading echoed
	// into the body, "如下：", a stray bullet. Below this length the type
	// classifier is reading a label, not an assertion.
	reportClaimMinRunes = 8
	// reportClaimTermRunes is the shortest CJK run counted as a subject term. Two
	// runes is the shortest meaningful Chinese word ("报销", "预算"); single runes
	// match everywhere and would make every claim look supported.
	reportClaimTermRunes = 2
	// reportClaimTermPrefixRunes is how much of a longer run still counts as naming
	// the same subject (see `reportClaimTermPresent`).
	reportClaimTermPrefixRunes = 4
)

// reportClaimTypeKeywords are the closed keyword sets behind `claim_type`.
//
// Order matters and is the judgement: a sentence that *proposes* while citing a
// number ("建议将保留期改为 60 天") is a recommendation, not a fact, even though it
// contains a checkable number. Recommendations and limitations are not scored for
// support at all, so mis-classifying in this direction is the safe one — it
// declines to certify rather than certifying something unverified.
var (
	reportClaimRecommendationKeywords = []string{
		"建议", "推荐", "应当", "宜", "可考虑", "有必要", "需要", "应尽快", "应力争",
	}
	reportClaimLimitationKeywords = []string{
		"不足", "缺失", "未覆盖", "缺少", "局限", "尚不明确", "无法", "待补充", "尚未",
		"未定义", "未明确", "风险敞口", "空白",
	}
	reportClaimInferenceKeywords = []string{
		"推断", "推测", "预计", "可能", "或许", "估计", "似乎", "有望", "大概率", "预计将",
	}
)

// reportClaimDraft is one claim as extracted from a body, before it gets an id.
type reportClaimDraft struct {
	Ord int
	// Text is the sentence as written, citation markers included: the appendix
	// prints it verbatim so a reviewer reads what the report actually said.
	Text string
	Type string
	// Markers are the citation numbers this sentence uses, ascending, deduped.
	Markers []int
	Support string
}

// reportClaimTypeOf classifies one sentence.
//
// Recommendation first, then limitation, then inference, then fact: the sets are
// ordered from "not an assertion about the world" to "an assertion about the
// world", so the first match is the most cautious reading of an ambiguous sentence.
func reportClaimTypeOf(text string) string {
	if reportClaimMatchesAny(text, reportClaimRecommendationKeywords) {
		return model.ReportClaimTypeRecommendation
	}
	if reportClaimMatchesAny(text, reportClaimLimitationKeywords) {
		return model.ReportClaimTypeLimitation
	}
	if reportClaimMatchesAny(text, reportClaimInferenceKeywords) {
		return model.ReportClaimTypeInference
	}
	return model.ReportClaimTypeFact
}

func reportClaimMatchesAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// reportClaimSegments splits a section body into claim-sized sentences.
//
// Skipped on purpose: markdown headings (they restate the outline, and counting
// them as claims would inflate every section's claim set by its own title), code
// fences, horizontal rules and table separator rows. Table *content* rows are
// kept — a compliance table's numbers are the most checkable claims in the
// document — with the pipes flattened to spaces so the sentence reads as prose.
func reportClaimSegments(content string) []string {
	segments := make([]string, 0, 16)
	inFence := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if reportClaimIsTableSeparator(line) {
			continue
		}
		line = strings.TrimSpace(strings.Trim(line, "|"))
		line = strings.TrimSpace(reportClaimStripListMarker(line))
		if line == "" {
			continue
		}
		line = strings.ReplaceAll(line, "|", " ")
		for _, sentence := range reportClaimSplitSentences(line) {
			sentence = strings.TrimSpace(strings.Join(strings.Fields(sentence), " "))
			if len([]rune(sentence)) < reportClaimMinRunes {
				continue
			}
			segments = append(segments, sentence)
		}
	}
	return segments
}

// reportClaimIsTableSeparator recognises the `|---|---|` row of a markdown table.
func reportClaimIsTableSeparator(line string) bool {
	if !strings.Contains(line, "-") {
		return false
	}
	for _, char := range line {
		switch char {
		case '|', '-', ':', ' ':
		default:
			return false
		}
	}
	return true
}

// reportClaimStripListMarker removes the bullet or ordinal in front of a list item
// so the claim text starts with the assertion.
func reportClaimStripListMarker(line string) string {
	for _, marker := range []string{"- [ ] ", "- [x] ", "- ", "* ", "+ "} {
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
	}
	index := 0
	for index < len(line) && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	if index > 0 && index+1 < len(line) && (line[index] == '.' || line[index] == ')') && line[index+1] == ' ' {
		return strings.TrimSpace(line[index+2:])
	}
	return line
}

// reportClaimSplitSentences splits on sentence terminators, keeping the terminator
// with the sentence it closes. Commas and semicolons do not split: they join two
// halves of one assertion, and splitting there would produce claim fragments whose
// fact labels live in the other half.
func reportClaimSplitSentences(line string) []string {
	var (
		sentences []string
		current   strings.Builder
	)
	for _, char := range line {
		current.WriteRune(char)
		switch char {
		case '。', '！', '？', '!', '?':
			sentences = append(sentences, current.String())
			current.Reset()
		}
	}
	if tail := strings.TrimSpace(current.String()); tail != "" {
		sentences = append(sentences, tail)
	}
	return sentences
}

// reportExtractClaims turns one section body plus the evidence it was shown into
// claims with types, marker links and support statuses.
//
// `evidence` is the bounded prompt view the writer saw, in citation order
// (`Index` 1-based). A marker with no matching evidence item is left out of
// `Markers` — it is a dangling marker, which A3c already reports as a section
// defect; inventing a claim link to a chunk that does not exist would hide it.
func reportExtractClaims(content string, evidence []reportEvidenceItem) []reportClaimDraft {
	fragments := make(map[int]string, len(evidence))
	for _, item := range evidence {
		if item.Index < 1 {
			continue
		}
		text := item.Content
		if strings.TrimSpace(text) == "" {
			text = item.SourceContent
		}
		fragments[item.Index] = text
	}
	claims := make([]reportClaimDraft, 0, 12)
	for _, segment := range reportClaimSegments(content) {
		markers := reportClaimMarkers(segment, fragments)
		claimType := reportClaimTypeOf(segment)
		fragmentTexts := make([]string, 0, len(markers))
		for _, marker := range markers {
			fragmentTexts = append(fragmentTexts, fragments[marker])
		}
		claims = append(claims, reportClaimDraft{
			Ord:     len(claims) + 1,
			Text:    segment,
			Type:    claimType,
			Markers: markers,
			Support: reportClaimSupport(claimType, segment, fragmentTexts),
		})
		if len(claims) >= reportClaimMaxPerSection {
			break
		}
	}
	return claims
}

// reportClaimMarkers lists the citation numbers one sentence uses, ascending and
// deduped, restricted to the evidence the section was actually shown.
func reportClaimMarkers(text string, fragments map[int]string) []int {
	seen := make(map[int]struct{}, 2)
	markers := make([]int, 0, 2)
	for _, match := range reportCitationMarkerPattern.FindAllStringSubmatch(text, -1) {
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if _, ok := fragments[index]; !ok {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		markers = append(markers, index)
	}
	sort.Ints(markers)
	return markers
}

// reportClaimSupport is the L3 rule check: does the evidence this claim cites
// contain the facts this claim states?
//
// The four answers and what each one certifies:
//
//   - `not_required` — the claim is a recommendation, an inference or a stated
//     limitation. It is not making a factual claim about the sources, and the
//     appendix's fixed declaration already says such sentences are not verified
//     facts (§9.2). Scoring them would manufacture a support rate out of sentences
//     that never claimed support.
//   - `unsupported` — a factual claim that cites nothing, or whose every number and
//     date is absent from what it cites. This is the "检索到证据 ≠ 结论有证据"
//     failure the whole evidence chain exists to surface.
//   - `partially_supported` — some checked facts are in the cited text, some are
//     not.
//   - `supported` — every checked fact is present, and at least one subject word of
//     the claim appears too. The subject-word condition is what stops a sentence
//     whose only number is a coincidence ("见第 3 条") from being certified.
//
// A factual claim with no numbers at all is decided by subject words alone: it is
// qualitative, so the only mechanical statement available is "the sentence's own
// vocabulary is present in the text it cites".
func reportClaimSupport(claimType, text string, fragments []string) string {
	if claimType != model.ReportClaimTypeFact {
		return model.ReportClaimSupportNotRequired
	}
	if len(fragments) == 0 {
		return model.ReportClaimSupportUnsupported
	}
	joined := reportClaimNormalizeFacts(strings.Join(fragments, "\n"))
	terms := reportClaimTerms(text)
	termHit := false
	for _, term := range terms {
		if reportClaimTermPresent(joined, term) {
			termHit = true
			break
		}
	}
	facts := reportKeyedFacts(text)
	if len(facts) == 0 {
		if termHit {
			return model.ReportClaimSupportSupported
		}
		return model.ReportClaimSupportUnsupported
	}
	matched := 0
	for _, fact := range facts {
		if reportFactPresent(joined, fact) {
			matched++
		}
	}
	switch {
	case matched == len(facts) && termHit:
		return model.ReportClaimSupportSupported
	case matched == 0:
		return model.ReportClaimSupportUnsupported
	default:
		return model.ReportClaimSupportPartiallySupported
	}
}

// reportClaimNormalizeFacts strips the whitespace a document may put inside a
// number ("90 天" vs "90天") so presence checks are about content, not typography.
func reportClaimNormalizeFacts(text string) string {
	return strings.NewReplacer(" ", "", "\u00a0", "", "\t", "").Replace(text)
}

// reportFactPresent reports whether one keyed fact's value appears in a normalised
// text. Dates accept both the written and the numeric form because a document that
// writes "2024年1月1日" in one clause and "2024-01-01" in another is stating one
// date.
func reportFactPresent(normalizedText string, fact reportFact) bool {
	value := reportClaimNormalizeFacts(fact.Value)
	if value == "" {
		return false
	}
	if strings.Contains(normalizedText, value) {
		return true
	}
	if fact.Kind != reportFactKindDate {
		return false
	}
	parts := strings.SplitN(value, "-", 3)
	if len(parts) < 2 {
		return false
	}
	year, month := parts[0], parts[1]
	written := year + "年" + trimLeadingZeros(month) + "月"
	if len(parts) == 3 {
		written += trimLeadingZeros(parts[2]) + "日"
	}
	return strings.Contains(normalizedText, written)
}

func trimLeadingZeros(value string) string {
	trimmed := strings.TrimLeft(value, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

// reportClaimTerms is the claim's own vocabulary: CJK runs long enough to be a
// word, minus the particles and connectives that appear in every sentence.
func reportClaimTerms(text string) []string {
	terms := make([]string, 0, 6)
	run := make([]rune, 0, 8)
	flush := func() {
		if len(run) >= reportClaimTermRunes {
			term := string(run)
			if !reportClaimStopTerm(term) && !containsString(terms, term) {
				terms = append(terms, term)
			}
		}
		run = run[:0]
	}
	for _, char := range text {
		if reportCJK(char) {
			run = append(run, char)
			continue
		}
		flush()
	}
	flush()
	return terms
}

// reportClaimTermPresent reports whether a claim's subject word appears in the text
// it cites.
//
// A whole-run match is too strict to be useful: "差旅报销须经部门审批" does not
// occur in "差旅报销须经部门负责人审批后提交财务", even though the claim is exactly
// what that chunk says. A four-rune prefix is the compromise — long enough to name a
// subject ("审计日志", "差旅报销") and short enough to survive the inflection
// Chinese documents apply by inserting words rather than changing endings.
func reportClaimTermPresent(text, term string) bool {
	if strings.Contains(text, term) {
		return true
	}
	runes := []rune(term)
	if len(runes) <= reportClaimTermPrefixRunes {
		return false
	}
	return strings.Contains(text, string(runes[:reportClaimTermPrefixRunes]))
}

// reportClaimStopTerm drops the words that carry no subject. Without this list
// "本制度适用于全体员工" certifies itself against any chunk containing "制度".
func reportClaimStopTerm(term string) bool {
	for _, stop := range reportClaimStopTerms {
		if term == stop {
			return true
		}
	}
	return false
}

var reportClaimStopTerms = []string{
	"本制度", "制度", "公司", "相关", "有关", "如下", "以下", "上述", "我们", "他们",
	"可以", "需要", "应当", "应该", "不得", "必须", "以及", "并且", "但是", "如果",
	"根据", "按照", "对于", "关于", "同时", "此外", "其中", "包括", "进行", "实行",
	"规定", "要求", "内容", "情况", "问题", "方面", "工作",
}

// reportCJK reports whether a rune is a Chinese character or a CJK-adjacent
// letter. Digits and punctuation break a term.
func reportCJK(char rune) bool {
	switch {
	case char >= 0x4e00 && char <= 0x9fff:
		return true
	case char >= 0x3400 && char <= 0x4dbf:
		return true
	case char >= 0xf900 && char <= 0xfaff:
		return true
	}
	return false
}

// reportClaimCitationLinks maps citation marker ordinals to the claim that owns
// them, which is what `rgx_report_citation.claim_id` stores.
//
// A marker can appear in two claims ("…[1]。如上[1]所述…"); the first claim wins.
// The alternative — one citation row per marker occurrence — would break the
// (section, ord) uniqueness the appendix numbering depends on, so the schema
// forces a choice and the honest one is the first mention.
func reportClaimCitationLinks(claims []reportClaimDraft) map[int]int {
	links := make(map[int]int, 8)
	for _, claim := range claims {
		for _, marker := range claim.Markers {
			if _, ok := links[marker]; ok {
				continue
			}
			links[marker] = claim.Ord
		}
	}
	return links
}

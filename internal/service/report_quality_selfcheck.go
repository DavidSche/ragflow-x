package service

// Q-1..Q-5 (doc/301 §9.4) — the review-quality self-check, as a function.
//
// §9.4 is five properties that must hold for every replay, and its own heading says
// where they run: 「跑在 CI 里，不靠人」. Until this file existed that heading was a
// promise: nothing counted `not_found`, nothing compared two replays of the same
// verdict input set, and the only 15% in the codebase was §4.2's
// `abstain_rate(model_uncertain)`, which has a *different* denominator (all claims,
// not all abstentions). A reviewer reading §9.4 would have had to take all five on
// faith, which is the one thing a self-check may not ask for.
//
// Four rules, each of which is a way this file could go wrong:
//
//  1. **A missing denominator is `unverified`, never `pass`.** "No evaluation
//     carried a quote status" is not "no `not_found`"; "no `supported` row exists"
//     is not "100% of them have evidence". Collapsing those two sentences is how a
//     self-check turns into a green badge over an empty table.
//  2. **A word outside the closed set is a fault, not a skip.** If a row says
//     `quote_status = unclear`, the check cannot count what it cannot name, and
//     quietly dropping the row would hide exactly the case the closed set exists
//     for.
//  3. **The five sentences are §9.4's, copied, not paraphrased.** Turning "`not_found`
//     为 0" into "few `not_found`" is a rule change made by a formatter. The gate
//     script greps these five literals in both this file and the document, so a
//     paraphrase on either side is red.
//  4. **Q-4 is a shape, not an overlap count.** The tempting implementation — count
//     citations whose chunk also appears in `rgx_report_conflict_candidate` — reports
//     a *legitimate* state as a violation: candidates are frozen **from** the
//     retrieval results of the same section, so on the live drill all 17 candidates
//     were also cited chunks (77 citation rows share a chunk with a candidate of the
//     same revision). What §9.4 forbids is a citation that *names a candidate*, and
//     the table has no column in which that could be written. So the check asserts
//     the shape (no candidate-shaped column) and the resolvability (every citation
//     resolves to an evidence snapshot), instead of a number that would be wrong for
//     a right system.
//
// What this file deliberately does not do: reach a database, read a clock, or
// decide which rows count as "this replay". The caller selects the window; this
// file only turns counts into five verdicts. That split is what lets the CI check
// run on fixtures while the live numbers in doc/305 §2.16 come from the same code.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/stats"
)

// qualitySelfCheckUncertainCeiling is §9.4's 「`model_uncertain` ≤15%」. It is the
// share **of abstentions**, not of all claims — §4.2's `abstain_rate` is the other
// denominator, and the two must not be added together or swapped. It is also a
// point-estimate rule, because that is how §9.4 words it: the CP-bound version of
// this ceiling is release gate row A1-09 (doc/301 §11.1), a different check over a
// different denominator. Q-5 prints the CP upper bound alongside the share so a small
// sample cannot look tighter than it is, but comparing it against 15% would be
// enforcing a rule §9.4 did not state — the gate already does that where it belongs.
const qualitySelfCheckUncertainCeiling = 0.15

// qualitySelfCheckExpectations are §9.4's five rows, verbatim (see rule 3 above).
// The gate script extracts them from this file and greps them in the document.
const (
	qualitySelfCheckQuoteStatusesCheck       = "金标 replay 的 `quote_status` 分布"
	qualitySelfCheckQuoteStatusesExpectation = "`not_found` 为 0"
	qualitySelfCheckQuoteStatusesMeans       = "提示模板或摘录策略有系统性问题"

	qualitySelfCheckDeterminismCheck       = "同一修订两次 replay 的判定 hash"
	qualitySelfCheckDeterminismExpectation = "完全一致"
	qualitySelfCheckDeterminismMeans       = "有未冻结的输入（时钟、随机、检索）泄漏进判定"

	qualitySelfCheckEvidenceCheck       = "`supported` 行的 `evidence_ids` 非空率"
	qualitySelfCheckEvidenceExpectation = "100%"
	qualitySelfCheckEvidenceMeans       = "有\"无来源的结论\"漏网"

	// 301 §9.4 row 4 was reworded on 2026-11-24 (第二十次修订摘要) to the criterion that
	// can actually be evaluated: the original 「`conflict_ref`（候选）出现在
	// `rgx_report_citation` 的次数 = 0」 named a column that table has never had, and
	// counted as a violation a state a correct system produces (candidates are frozen
	// *from* the same section's retrieval results). The reading that replaced it is the
	// one this file has implemented since Q-4 was written (rule 4 above), so the pair
	// doc <-> code now says the same thing; the history is kept in 304 E80 / §7 item 22
	// and 305 §2.16.
	qualitySelfCheckCitationCheck       = "`rgx_report_citation` 的列能否命名冲突候选（按列名形状），每条引用能否解析到证据快照"
	qualitySelfCheckCitationExpectation = "0 列可命名候选；悬空快照为 0"
	qualitySelfCheckCitationMeans       = "证据链被污染"

	qualitySelfCheckAbstainCheck       = "`abstained` 各原因的占比"
	qualitySelfCheckAbstainExpectation = "`model_uncertain` ≤15%"
	qualitySelfCheckAbstainMeans       = "模型在敷衍；或提示设计有问题"
)

// QualitySelfCheckReplay is one verdict input set — every evaluation row that shares
// a `cache_key` — and the distinct answers those rows gave. §9.4 asks for a hash of
// the judgment; a hash of `verdict|structure|relevance|quote_status` is the same
// claim made readable: two replays agreeing on all four agreed on the judgment, and
// when they do not, the tuple says where.
type QualitySelfCheckReplay struct {
	CacheKey string
	// Rows is how many evaluation rows carried this key. It is ≥2: a key seen once
	// is not a replay, and counting it would let a single run prove consistency.
	Rows int64
	// Variants are the distinct `verdict|structure|relevance|quote_status` tuples,
	// sorted. More than one is a nondeterminism finding.
	Variants []string
}

// QualitySelfCheckCitations is Q-4's shape: the citation table's columns, its row
// count, and how many rows fail to resolve to an evidence snapshot.
type QualitySelfCheckCitations struct {
	Columns          []string
	Rows             int64
	DanglingSnapshot int64
}

// QualitySelfCheckInput is what a replay window reports about itself. Every field is
// a count with an implicit denominator; there is no field for "rate", because a caller
// that computed a rate already chose the denominator and this file must not inherit
// that choice unexamined.
type QualitySelfCheckInput struct {
	// Q-1: evaluation rows that carry a quote status, counted by status.
	QuoteStatuses map[string]int64
	// Q-2: verdict input sets that were replayed (seen ≥2 times).
	Replays []QualitySelfCheckReplay
	// Q-3: `supported` rows, and how many of them carry no evidence id.
	Supported        int64
	SupportedUncited int64
	// Q-4: the citation table's shape and resolvability.
	Citations QualitySelfCheckCitations
	// Q-5: abstentions by reason.
	AbstainReasons map[string]int64
}

// QualitySelfCheckRow is one line of §9.4's table. `Observed` and `Sample` are two
// separate fields on purpose: the log is the only artifact that survives a run, and a
// reader must be able to see "0" and "of 548" without recomputing anything.
type QualitySelfCheckRow struct {
	ID           string
	Check        string
	Expectation  string
	FailureMeans string
	Observed     string
	Sample       string
	Verdict      string
	Detail       string
}

// QualitySelfCheckResult is the five rows.
type QualitySelfCheckResult struct {
	Rows []QualitySelfCheckRow
}

// Blocking returns every row a release may not ignore, which is every row that is not
// `pass` — `unverified` included, for the reason in rule 1.
func (r QualitySelfCheckResult) Blocking() []QualitySelfCheckRow {
	blocking := make([]QualitySelfCheckRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Verdict != releaseGatePass {
			blocking = append(blocking, row)
		}
	}
	return blocking
}

// Verdict is the one-word answer the CLI exits on: any `fail` outranks any
// `unverified`, because "measurably wrong" and "not measured" are different
// sentences and the stronger one should be the one reported.
func (r QualitySelfCheckResult) Verdict() string {
	verdict := releaseGatePass
	for _, row := range r.Rows {
		switch row.Verdict {
		case releaseGateFail:
			return releaseGateFail
		case releaseGateUnverified:
			verdict = releaseGateUnverified
		}
	}
	return verdict
}

// EvaluateQualitySelfCheck turns a window's counts into §9.4's five verdicts.
func EvaluateQualitySelfCheck(in QualitySelfCheckInput) QualitySelfCheckResult {
	result := QualitySelfCheckResult{Rows: []QualitySelfCheckRow{
		qualitySelfCheckQuoteStatuses(in),
		qualitySelfCheckDeterminism(in),
		qualitySelfCheckSupportedEvidence(in),
		qualitySelfCheckCitationChain(in),
		qualitySelfCheckAbstains(in),
	}}
	// A red row must repeat what it saw, in the row itself: the log is the only
	// artifact that survives a run, and "the check failed" without its number is a
	// ticket nobody can action. One place, so no helper can forget it.
	for i := range result.Rows {
		row := &result.Rows[i]
		if row.Verdict == releaseGatePass || strings.HasPrefix(row.Detail, row.Observed) {
			continue
		}
		row.Detail = row.Observed + " (" + row.Sample + ") — " + row.Detail
	}
	return result
}

// qualitySelfCheckQuoteStatuses is Q-1.
func qualitySelfCheckQuoteStatuses(in QualitySelfCheckInput) QualitySelfCheckRow {
	const id, naming = "Q-1", qualitySelfCheckQuoteStatusesCheck
	row := func(observed, sample, verdict, detail string) QualitySelfCheckRow {
		return QualitySelfCheckRow{
			ID: id, Check: naming,
			Expectation:  qualitySelfCheckQuoteStatusesExpectation,
			FailureMeans: qualitySelfCheckQuoteStatusesMeans,
			Observed:     observed, Sample: sample, Verdict: verdict, Detail: detail,
		}
	}

	var total int64
	for _, count := range in.QuoteStatuses {
		total += count
	}
	if unknown := unknownKeys(in.QuoteStatuses, model.ReportQuoteStatuses()); len(unknown) > 0 {
		return row("unknown quote status: "+strings.Join(unknown, ", "), sampleOf(total), releaseGateFail,
			"this check counts §5.1's four statuses; a fifth word means the column accepted a value the "+
				"closed set does not name, so the distribution it measures is not the one §9.4 is about")
	}
	if total == 0 {
		return row("no quote status to count", "n=0", releaseGateUnverified,
			"an empty column is not \"no not_found\": nothing was checked, so the check reports unverified "+
				"rather than pass (rule 1)")
	}
	notFound := in.QuoteStatuses[model.ReportQuoteStatusNotFound]
	observed := fmt.Sprintf("`not_found` = %d", notFound)
	detail := "distribution: " + formatCounts(in.QuoteStatuses)
	if notFound > 0 {
		return row(observed, sampleOf(total), releaseGateFail,
			detail+" — every presentation of a quote whose text is not in the snapshot points at the "+
				"template or the excerpt window, not at the claim")
	}
	return row(observed, sampleOf(total), releaseGatePass, detail)
}

// qualitySelfCheckDeterminism is Q-2.
func qualitySelfCheckDeterminism(in QualitySelfCheckInput) QualitySelfCheckRow {
	const id, naming = "Q-2", qualitySelfCheckDeterminismCheck
	row := func(observed, sample, verdict, detail string) QualitySelfCheckRow {
		return QualitySelfCheckRow{
			ID: id, Check: naming,
			Expectation:  qualitySelfCheckDeterminismExpectation,
			FailureMeans: qualitySelfCheckDeterminismMeans,
			Observed:     observed, Sample: sample, Verdict: verdict, Detail: detail,
		}
	}

	compared, differing := int64(0), make([]QualitySelfCheckReplay, 0, len(in.Replays))
	for _, replay := range in.Replays {
		if replay.Rows < 2 { // a key seen once is not a replay; see the type comment
			continue
		}
		compared++
		if len(replay.Variants) > 1 {
			differing = append(differing, replay)
		}
	}
	if compared == 0 {
		return row("no verdict input set was replayed", "n=0", releaseGateUnverified,
			"no `cache_key` was seen twice, so \"identical\" was never tested: a replay that never "+
				"happened cannot agree with itself")
	}
	sort.Slice(differing, func(i, j int) bool { return differing[i].CacheKey < differing[j].CacheKey })
	observed := fmt.Sprintf("%d of %d replay(s) differ", len(differing), compared)
	if len(differing) > 0 {
		named := make([]string, 0, 3)
		for _, replay := range differing {
			if len(named) == 3 {
				break
			}
			named = append(named, fmt.Sprintf("%s → %s", replay.CacheKey[:min(12, len(replay.CacheKey))],
				strings.Join(replay.Variants, " / ")))
		}
		return row(observed, sampleOf(compared), releaseGateFail,
			"named: "+strings.Join(named, "; ")+
				" — the cache key covers the full verdict input set (§6.1), so two answers under one key "+
				"mean an input outside it (clock, random, retrieval order) reached the evaluator")
	}
	return row(observed, sampleOf(compared), releaseGatePass,
		"every replayed input set produced one judgment tuple across all of its rows")
}

// qualitySelfCheckSupportedEvidence is Q-3.
func qualitySelfCheckSupportedEvidence(in QualitySelfCheckInput) QualitySelfCheckRow {
	const id, naming = "Q-3", qualitySelfCheckEvidenceCheck
	row := func(observed, sample, verdict, detail string) QualitySelfCheckRow {
		return QualitySelfCheckRow{
			ID: id, Check: naming,
			Expectation:  qualitySelfCheckEvidenceExpectation,
			FailureMeans: qualitySelfCheckEvidenceMeans,
			Observed:     observed, Sample: sample, Verdict: verdict, Detail: detail,
		}
	}

	if in.Supported <= 0 {
		return row(fmt.Sprintf("%d supported row(s)", in.Supported), "n=0", releaseGateUnverified,
			"a 100% rate over zero `supported` rows is undefined; the live drill's degraded runs produce "+
				"none, so this row says unverified instead of reporting a rate over an empty class")
	}
	if in.SupportedUncited < 0 || in.SupportedUncited > in.Supported {
		return row(fmt.Sprintf("%d of %d", in.SupportedUncited, in.Supported), sampleOf(in.Supported),
			releaseGateFail, "the two counts contradict each other: more uncited rows than rows")
	}
	rate := 1 - float64(in.SupportedUncited)/float64(in.Supported)
	observed := fmt.Sprintf("%.1f%% carry evidence", 100*rate)
	detail := fmt.Sprintf("%d of %d supported row(s) carry at least one evidence id",
		in.Supported-in.SupportedUncited, in.Supported)
	if in.SupportedUncited > 0 {
		return row(observed, sampleOf(in.Supported), releaseGateFail,
			fmt.Sprintf("%d supported row(s) carry no evidence id — a conclusion with no source is the "+
				"failure §9.4 exists for, and it is a row-level fact, not a rate to be averaged away",
				in.SupportedUncited))
	}
	return row(observed, sampleOf(in.Supported), releaseGatePass, detail)
}

// qualitySelfCheckCitationChain is Q-4. See rule 4 in the file header for why this is
// a shape check and not an overlap count.
func qualitySelfCheckCitationChain(in QualitySelfCheckInput) QualitySelfCheckRow {
	const id, naming = "Q-4", qualitySelfCheckCitationCheck
	row := func(observed, sample, verdict, detail string) QualitySelfCheckRow {
		return QualitySelfCheckRow{
			ID: id, Check: naming,
			Expectation:  qualitySelfCheckCitationExpectation,
			FailureMeans: qualitySelfCheckCitationMeans,
			Observed:     observed, Sample: sample, Verdict: verdict, Detail: detail,
		}
	}

	if len(in.Citations.Columns) == 0 {
		return row("the citation table's shape was not read", "n=0", releaseGateUnverified,
			"without the column list this check cannot say whether a candidate reference is even "+
				"expressible, which is the whole of Q-4")
	}
	forbidden := candidateShapedColumns(in.Citations.Columns)
	shape := fmt.Sprintf("0 of %d column(s) can name a candidate", len(in.Citations.Columns))
	if len(forbidden) > 0 {
		return row("column(s) "+strings.Join(forbidden, ", "), sampleOf(int64(len(in.Citations.Columns))),
			releaseGateFail,
			"a citation row can now name a conflict candidate. §9.4's zero was structural until this "+
				"column existed; from here on it has to be a measured count, and the evidence chain is "+
				"one write away from carrying a frozen candidate instead of a cited snapshot")
	}
	if in.Citations.Rows == 0 {
		return row(shape, "n=0", releaseGateUnverified,
			"the shape is right but there is no citation row to resolve, so the second half of Q-4 "+
				"(every citation reaches an evidence snapshot) was not tested")
	}
	if in.Citations.DanglingSnapshot > 0 {
		return row(fmt.Sprintf("%d dangling of %d citation(s)", in.Citations.DanglingSnapshot,
			in.Citations.Rows), sampleOf(in.Citations.Rows), releaseGateFail,
			"a citation whose snapshot does not exist is a reference a reader cannot follow; §9.4's "+
				"failure meaning (证据链被污染) is exactly this shape")
	}
	return row(shape, sampleOf(in.Citations.Rows), releaseGatePass,
		fmt.Sprintf("all %d citation(s) resolve to an evidence snapshot; chunk-level overlap with frozen "+
			"candidates is not counted here — candidates are frozen from the same section's retrieval "+
			"results, so overlap is what a correct system looks like (see rule 4)", in.Citations.Rows))
}

// qualitySelfCheckAbstains is Q-5.
func qualitySelfCheckAbstains(in QualitySelfCheckInput) QualitySelfCheckRow {
	const id, naming = "Q-5", qualitySelfCheckAbstainCheck
	row := func(observed, sample, verdict, detail string) QualitySelfCheckRow {
		return QualitySelfCheckRow{
			ID: id, Check: naming,
			Expectation:  qualitySelfCheckAbstainExpectation,
			FailureMeans: qualitySelfCheckAbstainMeans,
			Observed:     observed, Sample: sample, Verdict: verdict, Detail: detail,
		}
	}

	var total int64
	for _, count := range in.AbstainReasons {
		total += count
	}
	if unknown := unknownKeys(in.AbstainReasons, model.ReportAbstainReasons()); len(unknown) > 0 {
		return row("unknown abstain reason: "+strings.Join(unknown, ", "), sampleOf(total),
			releaseGateFail,
			"the share is over §5.2's closed set of reasons; an unnamed fifth reason would be counted in "+
				"the denominator and invisible in the numerator, which makes the 15% ceiling easier to meet")
	}
	if total == 0 {
		return row("no abstention to attribute", "n=0", releaseGateUnverified,
			"a share of zero abstentions is undefined. Reporting 0% here would say \"the model never "+
				"hedges\", when the truth is \"the model never ran\" — the live drill's rule-layer runs "+
				"abstain for other reasons, so the denominator is real only after the semantic layer runs")
	}
	uncertain := in.AbstainReasons[model.ReportAbstainModelUncertain]
	share := float64(uncertain) / float64(total)
	observed := fmt.Sprintf("`model_uncertain` = %.1f%%", 100*share)
	detail := fmt.Sprintf("%d of %d abstention(s); %s; CP 95%% upper bound %.1f%%",
		uncertain, total, formatCounts(in.AbstainReasons), 100*stats.Upper(uncertain, total, stats.Alpha))
	if share > qualitySelfCheckUncertainCeiling {
		return row(observed, sampleOf(total), releaseGateFail,
			detail+" — above §9.4's 15%: either the prompt is asking the model to judge cases it cannot "+
				"see, or the model is hedging to avoid being wrong")
	}
	return row(observed, sampleOf(total), releaseGatePass, detail)
}

// RenderQualitySelfCheckMarkdown is what a CI log shows. Every row prints its own
// sample next to the observation, because a verdict without its denominator is the
// reading §9.4's heading forbids.
func RenderQualitySelfCheckMarkdown(result QualitySelfCheckResult) string {
	var b strings.Builder
	b.WriteString("| # | 检查 | 期望 | 观测 | 样本 | 判定 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range result.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | **%s** |\n",
			row.ID, row.Check, row.Expectation, row.Observed, row.Sample, row.Verdict)
	}
	b.WriteString("\n")
	for _, row := range result.Rows {
		if row.Verdict == releaseGatePass {
			continue
		}
		fmt.Fprintf(&b, "- **%s %s**：%s（失败含义：%s）\n", row.ID, row.Verdict, row.Detail, row.FailureMeans)
	}
	return b.String()
}

// candidateShapedColumns returns the citation columns in which a conflict-candidate
// reference could be written. It matches by *shape of name*, not by a fixed list: the
// point is that the class is unrepresentable, and a future `conflict_candidate_uuid`
// must trip the same wire as `conflict_ref`.
func candidateShapedColumns(columns []string) []string {
	var out []string
	for _, column := range columns {
		lower := strings.ToLower(column)
		if strings.Contains(lower, "conflict") || strings.Contains(lower, "candidate") ||
			strings.HasPrefix(lower, "pool_") {
			out = append(out, column)
		}
	}
	sort.Strings(out)
	return out
}

// unknownKeys returns the keys outside a closed vocabulary, sorted.
func unknownKeys(counts map[string]int64, vocabulary []string) []string {
	known := make(map[string]bool, len(vocabulary))
	for _, word := range vocabulary {
		known[word] = true
	}
	var unknown []string
	for key := range counts {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// formatCounts renders a count map in a stable order, skipping zero buckets.
func formatCounts(counts map[string]int64) string {
	keys := make([]string, 0, len(counts))
	for key, count := range counts {
		if count == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "no non-zero bucket"
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func sampleOf(n int64) string { return fmt.Sprintf("n=%d", n) }

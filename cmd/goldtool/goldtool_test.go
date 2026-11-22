package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// sampleItem builds a *valid gold* item. Every field the validator checks is filled
// the way a real annotation would fill it, so a test that mutates one field is
// testing exactly one invariant instead of tripping over a missing prerequisite.
func sampleItem(n int, verdict string) Item {
	// 6 revisions, 3 templates, 2 datasets: the coverage floors of doc/301 §9.1, so a
	// fixture that satisfies the sample plan also satisfies provenance.
	rev := fmt.Sprintf("rev%02d", n%6)
	tpl := []string{"report-compliance", "report-postmortem", "report-tech-review"}[(n/2)%3]
	return Item{
		ItemID:          fmt.Sprintf("%s:claim%03d", rev, n),
		ClaimID:         fmt.Sprintf("claim%03d", n),
		ClaimText:       fmt.Sprintf("第 %d 条断言：某企业应当建立台账。", n),
		ClaimType:       "fact",
		SectionPrompt:   "责任分工\n风险点防控的责任分工是什么",
		SectionOrd:      n % 5,
		Evidence:        []Evidence{{SnapshotID: fmt.Sprintf("snap%03d", n), ChunkID: fmt.Sprintf("chunk%03d", n), DocumentID: "doc1", DatasetID: fmt.Sprintf("ds%d", n%2), ContentHash: "sha256:aa", ContentChars: 12, ContentExcerpt: "摘录十二字", Truncated: false}},
		CandidatePool:   nil,
		Source:          Source{TenantID: "t1", ReportID: rev, RevisionID: rev, Revision: 1, RevisionStatus: "published", TemplateKey: tpl, TemplateVersion: 1, DatasetIDs: []string{fmt.Sprintf("ds%d", n%2)}},
		AsOfDate:        "2026-11-21",
		GoldVerdict:     verdict,
		GoldQuote:       "",
		GoldQuoteStatus: "not_provided",
		GoldEvidenceIDs: []string{fmt.Sprintf("snap%03d", n)},
		// The default state of the product: nothing frozen, so nothing scanned. Tests
		// that do carry candidates must set the matching `cited+candidate:K`.
		GoldScanScope: "cited_only",
		Annotators:    []string{"qa-a", "qa-b"},
		Notes:         "",
	}
}

func goldWithDistribution() []Item {
	var out []Item
	n := 0
	for _, verdict := range GoldVerdicts {
		for i := 0; i < GoldDistribution[verdict]; i++ {
			n++
			out = append(out, sampleItem(n, verdict))
		}
	}
	return out
}

func codes(ps []Problem) map[string]int {
	out := map[string]int{}
	for _, p := range ps {
		out[p.Code]++
	}
	return out
}

func TestGoldSizeMatchesThePlan(t *testing.T) {
	// 36+40+24+12+8. If this ever changes, every pre-registered CP threshold in
	// doc/301 §4.2 changes with it — so the number is asserted, not computed.
	if got := GoldSize(); got != 120 {
		t.Fatalf("GoldSize = %d, want 120 (doc/301 §9.1)", got)
	}
}

func TestValidateAcceptsAPlanShapedFile(t *testing.T) {
	items := goldWithDistribution()
	problems := validateItems(items, true)
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %s\nfirst: %s", summariseProblems(problems), problems[0])
	}
}

func TestValidateNamesTheClassWhenTheDistributionDrifts(t *testing.T) {
	items := goldWithDistribution()
	// Move one `unsupported` to `supported`: the total stays 120, only the classes
	// move. A validator that only counted lines would pass this.
	for i := range items {
		if items[i].GoldVerdict == "unsupported" {
			items[i].GoldVerdict = "supported"
			break
		}
	}
	problems := validateItems(items, true)
	got := codes(problems)
	if got["distribution_mismatch"] != 2 {
		t.Fatalf("want 2 distribution mismatches (supported +1, unsupported -1), got %s", summariseProblems(problems))
	}
	var said []string
	for _, p := range problems {
		if p.Code == "distribution_mismatch" {
			said = append(said, p.Message)
		}
	}
	joined := strings.Join(said, " | ")
	if !strings.Contains(joined, "unsupported: got 39, want 40") || !strings.Contains(joined, "supported: got 37, want 36") {
		t.Fatalf("the message must name the class and both numbers, got %q", joined)
	}
}

func TestValidateRejectsAChunkThatIsBothCitedAndCandidate(t *testing.T) {
	items := goldWithDistribution()
	it := &items[0]
	it.CandidatePool = []Candidate{{ChunkID: it.Evidence[0].ChunkID, ContentHash: it.Evidence[0].ContentHash, ContentChars: 10, ContentExcerpt: "候选摘录"}}
	got := codes(validateItems(items, false))
	if got["item"] == 0 {
		t.Fatalf("a candidate that repeats a cited chunk must fail (doc/301 §9.1), got %s", summariseProblems(validateItems(items, false)))
	}
	found := false
	for _, p := range validateItems(items, false) {
		if strings.Contains(p.Message, "is by definition uncited") {
			found = true
		}
	}
	if !found {
		t.Fatal("the message must explain *why* the rule exists, not just that it fired")
	}
}

func TestValidateRejectsOversizedExcerpt(t *testing.T) {
	items := goldWithDistribution()
	long := strings.Repeat("字", maxExcerptRunes+1)
	items[0].Evidence[0].ContentExcerpt = long
	problems := validateItems(items, false)
	found := false
	for _, p := range problems {
		if strings.Contains(p.Message, "limit 200") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a 201-rune excerpt must fail: %s", summariseProblems(problems))
	}
}

func TestValidateRejectsEvidenceIDsThatWereNotCited(t *testing.T) {
	items := goldWithDistribution()
	items[0].GoldEvidenceIDs = []string{"not-a-cited-snapshot"}
	problems := validateItems(items, false)
	ok := false
	for _, p := range problems {
		if strings.Contains(p.Message, "not one of the item's cited snapshots") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("gold_evidence_ids must be drawn from the item's own snapshots: %s", summariseProblems(problems))
	}
}

func TestValidateRejectsOneAnnotator(t *testing.T) {
	items := goldWithDistribution()
	items[3].Annotators = []string{"qa-a", "qa-a"}
	problems := validateItems(items, false)
	ok := false
	for _, p := range problems {
		if strings.Contains(p.Message, "not two labels") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the same person twice is not two annotations: %s", summariseProblems(problems))
	}
}

func TestValidateRejectsQuoteStatusWithoutQuote(t *testing.T) {
	items := goldWithDistribution()
	items[5].GoldQuoteStatus = "verified" // but gold_quote stays empty
	problems := validateItems(items, false)
	ok := false
	for _, p := range problems {
		if strings.Contains(p.Message, "unfalsifiable") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("verified without a quote must fail: %s", summariseProblems(problems))
	}
}

func TestValidateRejectsEvaluatorStatesAsGoldLabels(t *testing.T) {
	items := goldWithDistribution()
	items[0].GoldVerdict = "abstained"
	problems := validateItems(items, false)
	ok := false
	for _, p := range problems {
		if strings.Contains(p.Message, "not a planned class") || strings.Contains(p.Message, "outside") ||
			strings.Contains(p.Message, "is not one of") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("abstained is an evaluator state, not a human label: %s", summariseProblems(problems))
	}
}

func TestValidateRejectsDuplicateClaimID(t *testing.T) {
	items := goldWithDistribution()
	items[1].ClaimID = items[0].ClaimID
	problems := validateItems(items, false)
	if codes(problems)["duplicate_claim_id"] != 1 {
		t.Fatalf("a claim counted twice doubles a denominator: %s", summariseProblems(problems))
	}
}

// TestValidateRejectsADuplicatedItemID pins the guard that keeps a copy-pasted line from
// being scored twice: two rows for one claim do not make the class twice as large, they
// make every ratio computed over the file a ratio over an unknown denominator.
func TestValidateRejectsADuplicatedItemID(t *testing.T) {
	items := goldWithDistribution()
	items[1].ItemID = items[0].ItemID
	problems := validateItems(items, false)
	if codes(problems)["duplicate_item_id"] != 1 {
		t.Fatalf("one item_id at two lines must be named once: %s", summariseProblems(problems))
	}
	for _, p := range problems {
		if p.Code == "duplicate_item_id" && !strings.Contains(p.Message, "lines 1 and 2") {
			t.Fatalf("the problem must name both lines, otherwise the annotator cannot find the copy: %q", p.Message)
		}
	}
}

// TestValidateNamesATamperedLineInTheFileItWasGiven: the §9.1 rules are also the
// mid-file-integrity check. A hand-edited verdict has to come back as a *line number* in
// the file that was passed, not as a message about the plan — otherwise "which line did I
// break" is a manual bisect.
func TestValidateNamesATamperedLineInTheFileItWasGiven(t *testing.T) {
	items := goldWithDistribution()
	dir := t.TempDir()
	path := filepath.Join(dir, "gold-v1.jsonl")
	writeItemsFile(t, path, items)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	// Line 57: the middle of the file, so an off-by-one in the reported index cannot hide.
	var tampered Item
	if err := json.Unmarshal([]byte(lines[56]), &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.GoldVerdict = model.ReportVerdictAbstained // an evaluator state, not a human label
	encoded, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	lines[56] = string(encoded)
	if err := writeText(path, strings.Join(lines, "\n")+"\n"); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runCapturingSplit(t, "validate", "--gold", path)
	if code != exitInvariant {
		t.Fatalf("a tampered line must exit 1, got %d: %s", code, firstLines(out, 6))
	}
	for _, needle := range []string{"line 57", tampered.ItemID, "distribution_mismatch"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("the report must contain %q, got: %s", needle, firstLines(out, 10))
		}
	}

	t.Run("a truncated line is a parse error, not a short file", func(t *testing.T) {
		broken := filepath.Join(dir, "gold-truncated.jsonl")
		if err := writeText(broken, strings.Join(lines[:len(lines)-1], "\n")+"\n"+
			lines[len(lines)-1][:len(lines[len(lines)-1])/2]); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runCapturingSplit(t, "validate", "--gold", broken)
		if code == exitOK {
			t.Fatalf("half a JSON line must not validate: %s", out)
		}
		// readItems reports `<path>:<line>: <parse error>`; the line number is the point —
		// "the file is bad" without a line is a bisect, not a diagnosis.
		if want := fmt.Sprintf(":%d:", len(lines)); !strings.Contains(errOut, want) {
			t.Fatalf("a truncated line must name the line it could not read (%s), got %q", want, errOut)
		}
	})
}

func TestRedactIsRuneSafeAndRecordsOriginalLength(t *testing.T) {
	text := strings.Repeat("安全生产", 100) // 400 runes, all multi-byte
	excerpt, chars := redact(text, "sha256:bb")
	if chars != 400 {
		t.Fatalf("content_chars = %d, want 400", chars)
	}
	if n := len([]rune(excerpt)); n != maxExcerptRunes {
		t.Fatalf("excerpt = %d runes, want %d", n, maxExcerptRunes)
	}
	if !strings.HasPrefix(text, excerpt) {
		t.Fatal("the excerpt must be a prefix of the original, otherwise it cannot be checked against the hash")
	}
}

func TestRedactKeepsShortTextWhole(t *testing.T) {
	excerpt, chars := redact("短文本", "sha256:cc")
	if excerpt != "短文本" || chars != 3 {
		t.Fatalf("got %q/%d", excerpt, chars)
	}
}

func TestCohenKappaKnownValues(t *testing.T) {
	// Hand-computed case: A = 6S/4U, B = 6S/4U, agreement on 6 of 10 =>
	// p0 = 0.60, pe = 0.6²+0.4² = 0.52, κ = 0.08/0.48 = 0.1667.
	// The numbers are deliberately *low*: a fixture where κ lands near 1 would not
	// prove that pe is actually subtracted.
	a := []string{"supported", "supported", "unsupported", "unsupported", "supported",
		"supported", "unsupported", "unsupported", "supported", "supported"}
	b := []string{"supported", "supported", "supported", "supported", "supported",
		"supported", "unsupported", "unsupported", "unsupported", "unsupported"}
	kappa, observed, expected, n := cohenKappa(a, b)
	if n != 10 {
		t.Fatalf("n = %d", n)
	}
	if diff := observed - 0.6; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("observed = %.4f, want 0.6", observed)
	}
	if diff := expected - 0.52; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected = %.4f, want 0.52", expected)
	}
	if diff := kappa - (0.08 / 0.48); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("kappa = %.4f, want %.4f", kappa, 0.08/0.48)
	}
}

func TestCohenKappaPerfectAgreementIsOne(t *testing.T) {
	v := []string{"supported", "unsupported", "conflicting"}
	kappa, _, _, _ := cohenKappa(v, v)
	if kappa != 1 {
		t.Fatalf("kappa = %v, want 1", kappa)
	}
}

func TestAgreementBelowTargetIsReported(t *testing.T) {
	items := []Item{{ItemID: "i1"}, {ItemID: "i2"}, {ItemID: "i3"}, {ItemID: "i4"}}
	labels := []Label{
		{ItemID: "i1", Annotator: "a", Verdict: "supported"},
		{ItemID: "i1", Annotator: "b", Verdict: "supported"},
		{ItemID: "i2", Annotator: "a", Verdict: "supported"},
		{ItemID: "i2", Annotator: "b", Verdict: "unsupported"},
		{ItemID: "i3", Annotator: "a", Verdict: "supported"},
		{ItemID: "i3", Annotator: "b", Verdict: "unsupported"},
		{ItemID: "i4", Annotator: "a", Verdict: "supported"},
		{ItemID: "i4", Annotator: "b", Verdict: "unsupported"},
	}
	ag, err := agreement(items, labels)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Pairs != 4 {
		t.Fatalf("pairs = %d, want 4", ag.Pairs)
	}
	if ag.MeetsTarget {
		t.Fatalf("κ = %.3f should not meet %.2f", ag.Kappa, minKappa)
	}
	if ag.PerClass["certification"] != 3 {
		t.Fatalf("disagreements should be classified by cost, got %v", ag.PerClass)
	}
}

func TestAgreementRejectsDuplicateLabel(t *testing.T) {
	labels := []Label{
		{ItemID: "i1", Annotator: "a", Verdict: "supported"},
		{ItemID: "i1", Annotator: "a", Verdict: "unsupported"},
		{ItemID: "i1", Annotator: "b", Verdict: "supported"},
	}
	if _, err := agreement([]Item{{ItemID: "i1"}}, labels); err == nil {
		t.Fatal("a second label from the same annotator must be refused")
	}
}

func TestAssembleDropsUnadjudicatedDisagreement(t *testing.T) {
	pool := []Item{sampleItem(1, "")}
	pool[0].GoldVerdict = ""
	pool[0].Evidence[0].Content = "某企业应当建立安全生产台账"
	pool[0].Evidence[0].ContentExcerpt = ""
	a := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-a", Verdict: "supported"}}
	b := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-b", Verdict: "unsupported"}}
	gold, problems := assembleItems(pool, a, b, nil, true)
	if len(gold) != 0 {
		t.Fatalf("an unadjudicated disagreement must be dropped, got %d items", len(gold))
	}
	if codes(problems)["unadjudicated_disagreement"] != 1 {
		t.Fatalf("want unadjudicated_disagreement, got %v", codes(problems))
	}
}

func TestAssembleUsesAdjudicationAndKeepsTheReason(t *testing.T) {
	pool := []Item{sampleItem(1, "")}
	pool[0].GoldVerdict = ""
	pool[0].Evidence[0].Content = "某企业应当建立安全生产台账"
	pool[0].Evidence[0].ContentExcerpt = ""
	a := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-a", Verdict: "supported", QuoteStatus: "not_provided"}}
	b := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-b", Verdict: "unsupported", QuoteStatus: "not_provided"}}
	adj := []Adjudication{{ItemID: pool[0].ItemID, Verdict: "unsupported", QuoteStatus: "not_provided",
		Adjudicator: "pm", Reason: "台账要求写入的是建议而非事实"}}
	gold, problems := assembleItems(pool, a, b, adj, true)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(gold) != 1 || gold[0].GoldVerdict != "unsupported" {
		t.Fatalf("adjudication must win: %+v", gold)
	}
	if !strings.Contains(gold[0].Notes, "台账要求写入的是建议而非事实") {
		t.Fatalf("the reason must survive into the gold row, got notes %q", gold[0].Notes)
	}
	if gold[0].Evidence[0].Content != "" || gold[0].Evidence[0].ContentExcerpt == "" {
		t.Fatal("assemble must redact full text and keep an excerpt")
	}
	if gold[0].Annotators[0] != "qa-a" || gold[0].Annotators[1] != "qa-b" {
		t.Fatalf("annotators = %v", gold[0].Annotators)
	}
}

func TestAssembleRefusesAnAdjudicationWithoutReason(t *testing.T) {
	pool := []Item{sampleItem(1, "")}
	pool[0].GoldVerdict = ""
	a := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-a", Verdict: "supported"}}
	b := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-b", Verdict: "unsupported"}}
	adj := []Adjudication{{ItemID: pool[0].ItemID, Verdict: "supported", QuoteStatus: "not_provided", Adjudicator: "pm"}}
	_, problems := assembleItems(pool, a, b, adj, true)
	if codes(problems)["adjudication_without_reason"] != 1 {
		t.Fatalf("a coin flip with a signature is not an adjudication: %v", codes(problems))
	}
}

func TestAssembleRequiresBothAnnotators(t *testing.T) {
	pool := []Item{sampleItem(1, "")}
	pool[0].GoldVerdict = ""
	a := []Label{{ItemID: pool[0].ItemID, Annotator: "qa-a", Verdict: "supported"}}
	gold, problems := assembleItems(pool, a, nil, nil, true)
	if len(gold) != 0 || codes(problems)["unlabelled_item"] != 1 {
		t.Fatalf("one annotator is a sample of one: gold=%d problems=%v", len(gold), codes(problems))
	}
}

func TestAssembleRejectsLabelForUnknownItem(t *testing.T) {
	pool := []Item{sampleItem(1, "")}
	a := []Label{{ItemID: "ghost", Annotator: "qa-a", Verdict: "supported"}}
	_, problems := assembleItems(pool, a, nil, nil, false)
	if codes(problems)["label_for_unknown_item"] != 1 {
		t.Fatalf("a label pointing nowhere is a bookkeeping error, not a skip: %v", codes(problems))
	}
}

func TestItemRoundTripsThroughJSONL(t *testing.T) {
	it := sampleItem(7, "supported")
	line, err := encodeItem(it)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeItem(line)
	if err != nil {
		t.Fatal(err)
	}
	if back.ItemID != it.ItemID || back.GoldVerdict != it.GoldVerdict || back.Evidence[0].SnapshotID != it.Evidence[0].SnapshotID {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if back.GoldQuoteStatus != it.GoldQuoteStatus || back.AsOfDate != it.AsOfDate {
		t.Fatalf("round trip lost labels: %+v", back)
	}
}

func TestDatasetIDsToleratesGarbage(t *testing.T) {
	if got := datasetIDs("not json"); got != nil {
		t.Fatalf("garbage must yield nil, got %v", got)
	}
	got := datasetIDs(`[{"dataset_id":"a"},{"dataset_id":""},{"dataset_id":"b"}]`)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

// The dedup unit is the section (2026-11-21 amendment to doc/301 §9.1, 304 D-08).
// All three outcomes are asserted in one table because the bug this replaced was a
// *scope* bug: the old filter looked revision-wide and deleted rows the writer had
// genuinely seen, which emptied E2's only source on the 151 corpus.
func TestFilterCandidatesDropsOnlySectionCited(t *testing.T) {
	sectionCited := map[string]bool{"own:hash-own": true}
	revisionCited := map[string]bool{
		"own:hash-own":     true, // cited by this section
		"other:hash-other": true, // cited by a different section of the same revision
	}
	in := []Candidate{
		{ChunkID: "own", ContentHash: "hash-own", PoolRank: 1},
		{ChunkID: "other", ContentHash: "hash-other", PoolRank: 2},
		{ChunkID: "fresh", ContentHash: "hash-fresh", PoolRank: 3},
	}
	out := filterCandidates(in, sectionCited, revisionCited)
	if len(out) != 2 {
		t.Fatalf("want the section-cited row gone and the other two kept, got %+v", out)
	}
	byChunk := map[string]Candidate{}
	for _, c := range out {
		byChunk[c.ChunkID] = c
	}
	if c, ok := byChunk["other"]; !ok || !c.CitedElsewhere {
		t.Fatalf("a chunk cited in another section must survive *and* be flagged: %+v", byChunk)
	}
	if c, ok := byChunk["fresh"]; !ok || c.CitedElsewhere {
		t.Fatalf("an uncited chunk must survive unflagged: %+v", byChunk)
	}
	if _, ok := byChunk["own"]; ok {
		t.Fatalf("a chunk this section cited must not survive: %+v", byChunk)
	}
}

// A candidate that repeats a chunk this item cites is a contradiction in the one
// scope the annotator judges, and the validator has to say so by name.
func TestValidateRefusesCandidateCitedBySameItem(t *testing.T) {
	it := sampleItem(1, "supported")
	it.CandidatePool = []Candidate{{ChunkID: it.Evidence[0].ChunkID,
		ContentHash: it.Evidence[0].ContentHash, ContentExcerpt: "同一段落", PoolRank: 1}}
	it.GoldScanScope = "cited+candidate:1" // otherwise the scope check fires first
	problems := checkItem(it, 0)
	if len(problems) == 0 {
		t.Fatalf("want a named problem, got none")
	}
	if !strings.Contains(fmt.Sprint(problems), "repeats a chunk that this item cites") {
		t.Fatalf("want the same-item citation named, got %v", problems)
	}
}

// Redaction is one-way but must be repeatable: an assembly retried after a κ failure
// re-reads the pool, and a redaction that blanked an already-excerpted item would
// produce a file that passes the length check carrying no text at all.
func TestRedactItemIsIdempotent(t *testing.T) {
	it := sampleItem(1, "supported")
	it.Evidence[0].Content = "完整原文，用于验证重复脱敏"
	it.Evidence[0].ContentExcerpt = ""
	once := redactItem(it)
	twice := redactItem(once)
	if once.Evidence[0].ContentExcerpt == "" {
		t.Fatalf("first redaction produced no excerpt")
	}
	if twice.Evidence[0].ContentExcerpt != once.Evidence[0].ContentExcerpt {
		t.Fatalf("second redaction changed the excerpt: %q -> %q",
			once.Evidence[0].ContentExcerpt, twice.Evidence[0].ContentExcerpt)
	}
	if twice.Evidence[0].ContentChars != once.Evidence[0].ContentChars {
		t.Fatalf("second redaction changed the char count: %d -> %d",
			once.Evidence[0].ContentChars, twice.Evidence[0].ContentChars)
	}
}

// …but the same chunk cited **elsewhere** is legal and keeps its flag through
// assembly, because the flag is what turns "the writer never saw it" into "the writer
// used it in another section" — a different judgement.
func TestCandidateCitedElsewhereSurvivesAssembly(t *testing.T) {
	it := sampleItem(1, "supported")
	it.CandidatePool = []Candidate{{ChunkID: "chk-elsewhere", ContentHash: "hash-elsewhere",
		Content: "别处引用过的一整段原文", PoolRank: 4, CitedElsewhere: true}}
	it.GoldScanScope = "cited+candidate:1"
	gold := redactItem(it)
	cand := gold.CandidatePool[0]
	if !cand.CitedElsewhere {
		t.Fatalf("cited_elsewhere must survive redaction: %+v", cand)
	}
	if cand.Content != "" || cand.ContentExcerpt == "" {
		t.Fatalf("a gold candidate must be redacted, not dropped: %+v", cand)
	}
	if problems := checkItem(gold, 0); len(problems) != 0 {
		t.Fatalf("a flagged-elsewhere candidate must pass checkItem: %v", problems)
	}
}

// `gold_scan_scope` is a disclosure, so a *wrong* one is worse than a missing one: it
// would let "we scanned 8 candidates and found nothing" be a property of a memory.
// Three ways it can be wrong, all of them named.
func TestValidateRefusesInconsistentScanScope(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		cands []Candidate
		want  string
	}{
		{"empty", "", nil, "gold_scan_scope is empty"},
		{"cited_only_with_rows", "cited_only",
			[]Candidate{{ChunkID: "c1", ContentHash: "h1", ContentExcerpt: "候选"}},
			"cited_only but candidate_pool has 1 row"},
		{"k_mismatch", "cited+candidate:8",
			[]Candidate{{ChunkID: "c1", ContentHash: "h1", ContentExcerpt: "候选"}},
			"says 8 candidate(s) but candidate_pool has 1"},
		{"not_the_vocabulary", "cited", nil, "neither cited_only nor cited+candidate:K"},
		{"k_not_a_number", "cited+candidate:many",
			[]Candidate{{ChunkID: "c1", ContentHash: "h1", ContentExcerpt: "候选"}},
			"no positive count"},
	}
	for _, c := range cases {
		it := sampleItem(1, "supported")
		it.GoldScanScope = c.scope
		it.CandidatePool = c.cands
		got := fmt.Sprint(checkItem(it, 0))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q, got %s", c.name, c.want, got)
		}
	}

	// …and the legal shapes must stay legal, including K = the real row count.
	ok := sampleItem(1, "supported")
	ok.GoldScanScope = "cited+candidate:2"
	ok.CandidatePool = []Candidate{
		{ChunkID: "c1", ContentHash: "h1", ContentExcerpt: "候选一"},
		{ChunkID: "c2", ContentHash: "h2", ContentExcerpt: "候选二", CitedElsewhere: true},
	}
	if problems := checkItem(ok, 0); len(problems) != 0 {
		t.Fatalf("a consistent scope must pass: %v", problems)
	}
}

func TestScanScopeOfMatchesCandidateRows(t *testing.T) {
	if got := scanScopeOf(nil); got != "cited_only" {
		t.Fatalf("no candidates must be cited_only, got %q", got)
	}
	if got := scanScopeOf([]Candidate{{ChunkID: "c1"}, {ChunkID: "c2"}, {ChunkID: "c3"}}); got != "cited+candidate:3" {
		t.Fatalf("K must be the row count, got %q", got)
	}
}

func TestSectionPromptKeepsBothHalves(t *testing.T) {
	if got := sectionPrompt("标题", "查询"); got != "标题\n查询" {
		t.Fatalf("got %q", got)
	}
	if got := sectionPrompt("标题", "  "); got != "标题" {
		t.Fatalf("got %q", got)
	}
}

// The CLI layer is thin but it owns the exit codes, and the exit codes are what a
// gate row reads. Two cases are enough: a clean file and a drifted one.
func TestCLIValidateExitCodes(t *testing.T) {
	dir := t.TempDir()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	good := filepath.Join(dir, "good.jsonl")
	lines, err := encodeItems(goldWithDistribution())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(good, lines); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"validate", "--gold", good}, devnull, devnull); code != exitOK {
		t.Fatalf("a plan-shaped file must exit 0, got %d", code)
	}

	drifted := goldWithDistribution()
	drifted = drifted[:len(drifted)-1] // 119 items
	lines, err = encodeItems(drifted)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.jsonl")
	if err := writeJSONL(bad, lines); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"validate", "--gold", bad}, devnull, devnull); code != exitInvariant {
		t.Fatalf("a short file must exit 1, got %d", code)
	}

	if code := run([]string{"validate"}, devnull, devnull); code != exitUsage {
		t.Fatalf("missing --gold must exit 2, got %d", code)
	}
	if code := run([]string{"nope"}, devnull, devnull); code != exitUsage {
		t.Fatalf("unknown subcommand must exit 2, got %d", code)
	}
}

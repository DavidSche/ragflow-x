package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// candRefFixture is a small pool where one item carries a frozen candidate, so the whole
// candidate-reference path (`label.candidate_chunk_id` → `gold_candidate_chunk_id`) can be
// exercised without the 120-item plan fixture.
func candRefFixture(t *testing.T) (dir, poolPath string, item Item) {
	t.Helper()
	dir = t.TempDir()
	it := sampleItem(1, "")
	it.GoldVerdict = ""
	it.GoldQuoteStatus = ""
	it.GoldEvidenceIDs = nil
	it.Annotators = nil
	it.GoldScanScope = "cited+candidate:1"
	it.CandidatePool = []Candidate{{
		ChunkID: "chunk-conflict", ContentHash: "hash-conflict",
		Content: "该企业第 12 家权属单位整改尚未完成。", CitedElsewhere: false, PoolRank: 1,
	}}
	poolPath = filepath.Join(dir, "pool.jsonl")
	lines, err := encodeItems([]Item{it})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(poolPath, lines); err != nil {
		t.Fatal(err)
	}
	return dir, poolPath, it
}

func assembleFixture(t *testing.T, dir, poolPath string, a, b []Label, adj []Adjudication) (int, string) {
	t.Helper()
	writeLabels(t, filepath.Join(dir, "a.jsonl"), a)
	writeLabels(t, filepath.Join(dir, "b.jsonl"), b)
	adjPath := filepath.Join(dir, "adj.jsonl")
	var lines []string
	for _, r := range adj {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := writeJSONL(adjPath, lines); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.Create(filepath.Join(dir, "stdout.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	code := run([]string{"assemble", "--pool", poolPath,
		"--labels-a", filepath.Join(dir, "a.jsonl"),
		"--labels-b", filepath.Join(dir, "b.jsonl"),
		"--adjudication", adjPath,
		"--version", "v9", "--out", filepath.Join(dir, "gold")}, stdout, devnull(t))
	raw, _ := os.ReadFile(filepath.Join(dir, "stdout.json"))
	return code, string(raw)
}

// A conflicting verdict that names the candidate row it rests on must carry that row into
// the gold file: E2's recall is reported per `cited_elsewhere` group, and the group cannot
// be derived from a gold file that forgot which candidate was judged.
func TestAssembleCarriesCandidateReference(t *testing.T) {
	dir, poolPath, it := candRefFixture(t)
	la := Label{ItemID: it.ItemID, Annotator: "qa", Verdict: "conflicting",
		QuoteStatus: "not_provided", CandidateChunkID: "chunk-conflict",
		Note: "候选里有一条相反的整改状态"}
	lb := la
	lb.Annotator = "pm"
	_, stdout := assembleFixture(t, dir, poolPath, []Label{la}, []Label{lb}, nil)
	if strings.Contains(stdout, "label_candidate_unknown") || strings.Contains(stdout, "candidate_ref") {
		t.Fatalf("a legal candidate reference must not be reported as a problem: %s", stdout)
	}
	gold, err := readItems(filepath.Join(dir, "gold", "report-assurance-gold.v9.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(gold) != 1 {
		t.Fatalf("gold has %d items, want 1", len(gold))
	}
	if gold[0].GoldCandidateChunkID != "chunk-conflict" {
		t.Fatalf("gold_candidate_chunk_id = %q, want chunk-conflict", gold[0].GoldCandidateChunkID)
	}
	// A one-item fixture cannot satisfy the plan's coverage floors, so only the
	// candidate-reference rules are asserted here.
	for _, p := range validateItems(gold, false) {
		if strings.Contains(p.Message, "candidate") {
			t.Fatalf("gold carries a candidate-reference problem: %s", summariseProblems([]Problem{p}))
		}
	}
}

// A reference to a chunk the item does not carry is refused, not dropped: an annotator who
// cites a candidate from another revision must be told, because the gold file would
// otherwise look like it had used the frozen row.
func TestAssembleRefusesUnknownCandidateReference(t *testing.T) {
	dir, poolPath, it := candRefFixture(t)
	la := Label{ItemID: it.ItemID, Annotator: "qa", Verdict: "conflicting",
		QuoteStatus: "not_provided", CandidateChunkID: "chunk-from-another-revision"}
	lb := la
	lb.Annotator = "pm"
	code, stdout := assembleFixture(t, dir, poolPath, []Label{la}, []Label{lb}, nil)
	if code == exitOK {
		t.Fatalf("a reference to a missing candidate must not assemble cleanly: %s", stdout)
	}
	if !strings.Contains(stdout, "label_candidate_unknown") {
		t.Fatalf("the refusal must be named: %s", stdout)
	}
	// The item is dropped rather than written with a dangling reference, so the gold file
	// (if written at all) must not contain it.
	gold, err := readItems(filepath.Join(dir, "gold", "report-assurance-gold.v9.jsonl"))
	if err == nil && len(gold) != 0 {
		t.Fatalf("the mis-referenced item must not appear in the gold file: %d items", len(gold))
	}
}

// Two annotators naming *different* candidates is a disagreement about what the claim
// conflicts with. Guessing one would make the E2 grouping depend on file order, so it must
// go to adjudication.
func TestAssembleRequiresAdjudicationForDifferentCandidates(t *testing.T) {
	dir, poolPath, it := candRefFixture(t)
	it.CandidatePool = append(it.CandidatePool, Candidate{
		ChunkID: "chunk-second", ContentHash: "hash-second",
		Content: "第二份相反的原文。", PoolRank: 2,
	})
	lines, err := encodeItems([]Item{it})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(poolPath, lines); err != nil {
		t.Fatal(err)
	}
	la := Label{ItemID: it.ItemID, Annotator: "qa", Verdict: "conflicting",
		QuoteStatus: "not_provided", CandidateChunkID: "chunk-conflict"}
	lb := Label{ItemID: it.ItemID, Annotator: "pm", Verdict: "conflicting",
		QuoteStatus: "not_provided", CandidateChunkID: "chunk-second"}
	code, stdout := assembleFixture(t, dir, poolPath, []Label{la}, []Label{lb}, nil)
	if code == exitOK {
		t.Fatalf("an unadjudicated candidate disagreement must not assemble cleanly: %s", stdout)
	}
	if !strings.Contains(stdout, "candidate_ref_disagreement") {
		t.Fatalf("the missing adjudication must be named: %s", stdout)
	}

	// With an adjudication that names the row, the same pair assembles and the
	// adjudicator's choice is the one recorded.
	adj := []Adjudication{{ItemID: it.ItemID, Verdict: "conflicting", QuoteStatus: "not_provided",
		Adjudicator: "be", Reason: "两句相反原文都在候选里，前者与本节的整改结论直接矛盾",
		CandidateChunkID: "chunk-second"}}
	_, stdout = assembleFixture(t, dir, poolPath, []Label{la}, []Label{lb}, adj)
	if strings.Contains(stdout, "candidate_ref_disagreement") {
		t.Fatalf("an adjudicated pair must not be reported as unresolved: %s", stdout)
	}
	gold, err := readItems(filepath.Join(dir, "gold", "report-assurance-gold.v9.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if gold[0].GoldCandidateChunkID != "chunk-second" {
		t.Fatalf("gold_candidate_chunk_id = %q, want the adjudicator's row", gold[0].GoldCandidateChunkID)
	}
}

// The validator refuses the two file-level lies: a reference to a row that is not there,
// and a candidate-backed verdict in a `cited_only` export (which never saw a candidate).
func TestValidateRefusesImpossibleCandidateReference(t *testing.T) {
	it := sampleItem(1, "conflicting")
	it.GoldScanScope = "cited_only"
	it.GoldCandidateChunkID = "chunk-conflict"
	problems := validateItems([]Item{it}, false)
	if !hasProblemText(problems, "gold_candidate_chunk_id") {
		t.Fatalf("cited_only + candidate reference must be refused: %s", summariseProblems(problems))
	}

	it = sampleItem(2, "conflicting")
	it.GoldScanScope = "cited+candidate:1"
	it.CandidatePool = []Candidate{{ChunkID: "chunk-real", ContentHash: "h", ContentExcerpt: "摘录"}}
	it.GoldCandidateChunkID = "chunk-absent"
	if problems := validateItems([]Item{it}, false); !hasProblemText(problems, "gold_candidate_chunk_id") {
		t.Fatalf("a reference outside candidate_pool must be refused: %s", summariseProblems(problems))
	}

	it.GoldCandidateChunkID = "chunk-real"
	if problems := validateItems([]Item{it}, false); hasProblemText(problems, "gold_candidate_chunk_id") {
		t.Fatalf("a reference to a present candidate must pass: %s", summariseProblems(problems))
	}
}

func hasProblemText(ps []Problem, needle string) bool {
	for _, p := range ps {
		if strings.Contains(p.Message, needle) || strings.Contains(p.Code, needle) {
			return true
		}
	}
	return false
}

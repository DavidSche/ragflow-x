package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPipelineEndToEnd runs the whole T02 machine path with no database: a pool
// file with unlabelled items, two annotators who agree, one adjudicated
// disagreement, then assemble + validate. It is the test that would have caught a
// mismatch between the *shape* assemble writes and the *shape* validate accepts —
// the two halves are in different files and used to share only a struct.
func TestPipelineEndToEnd(t *testing.T) {
	dir := t.TempDir()

	// 1. Pool: plan-shaped items, no labels, full text (that is what an annotator sees).
	pool := goldWithDistribution()
	for i := range pool {
		pool[i].GoldVerdict = ""
		pool[i].GoldQuote = ""
		pool[i].GoldQuoteStatus = ""
		pool[i].GoldEvidenceIDs = nil
		pool[i].Annotators = nil
		for j := range pool[i].Evidence {
			pool[i].Evidence[j].ContentExcerpt, pool[i].Evidence[j].ContentChars = "", 0
			pool[i].Evidence[j].Content = "某企业应当建立安全生产责任制台账，并按季度上报。"
		}
	}
	poolPath := filepath.Join(dir, "pool.jsonl")
	lines, err := encodeItems(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(poolPath, lines); err != nil {
		t.Fatal(err)
	}

	// 2. Two annotators agree on everything except item 10, which is adjudicated.
	var a, b []Label
	var adj []Adjudication
	for i, it := range pool {
		verdict := it.GoldVerdict
		if verdict == "" { // pool carries none; recover from the plan order
			verdict = plannedVerdict(i)
		}
		la := Label{ItemID: it.ItemID, Annotator: "qa", Verdict: verdict, QuoteStatus: "not_provided"}
		lb := la
		lb.Annotator = "pm"
		if i == 10 {
			// The pair must disagree *and* the adjudicated verdict must keep the planned
			// class, otherwise the file leaves the pre-registered distribution and
			// assemble (correctly) refuses it. So annotator A is the one who is wrong.
			la.Verdict = flip(verdict)
			adj = append(adj, Adjudication{ItemID: it.ItemID, Verdict: verdict,
				QuoteStatus: "not_provided", Adjudicator: "be", Reason: "复核后确认该句仍是事实陈述"})
		}
		a = append(a, la)
		b = append(b, lb)
	}
	writeLabels(t, filepath.Join(dir, "a.jsonl"), a)
	writeLabels(t, filepath.Join(dir, "b.jsonl"), b)
	// Adjudications are JSONL as well (a JSON array is not a label file, and the
	// reader must not have two shapes to handle).
	adjPath := filepath.Join(dir, "adj.jsonl")
	adjLines := make([]string, 0, len(adj))
	for _, r := range adj {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		adjLines = append(adjLines, string(raw))
	}
	if err := writeJSONL(adjPath, adjLines); err != nil {
		t.Fatal(err)
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	outDir := filepath.Join(dir, "gold")
	code := run([]string{"assemble", "--pool", poolPath, "--labels-a", filepath.Join(dir, "a.jsonl"),
		"--labels-b", filepath.Join(dir, "b.jsonl"), "--adjudication", adjPath,
		"--version", "v1", "--out", outDir}, devnull, devnull)
	if code != exitOK {
		t.Fatalf("assemble exited %d", code)
	}

	goldPath := filepath.Join(outDir, "report-assurance-gold.v1.jsonl")
	gold, err := readItems(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(gold) != GoldSize() {
		t.Fatalf("gold has %d items, want %d", len(gold), GoldSize())
	}

	// 3. The written file must pass its own validator, and the manifest hash must match.
	if problems := validateItems(gold, true); len(problems) != 0 {
		t.Fatalf("assembled file does not validate: %s", summariseProblems(problems))
	}
	body, err := os.ReadFile(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	var man manifest
	manRaw, err := os.ReadFile(filepath.Join(outDir, "report-assurance-gold.v1.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manRaw, &man); err != nil {
		t.Fatal(err)
	}
	if man.SHA256 != hashString(string(body)) {
		t.Fatalf("manifest sha256 %s does not match the file (%s)", man.SHA256, hashString(string(body)))
	}
	if man.Items != GoldSize() || man.Adjudicated != 1 {
		t.Fatalf("manifest counts: items=%d adjudicated=%d", man.Items, man.Adjudicated)
	}
	if len(man.Problems) != 0 {
		t.Fatalf("a clean assembly must record no problems, got %v", man.Problems)
	}
	if man.Redaction.MaxExcerptRunes != maxExcerptRunes {
		t.Fatalf("the manifest must state the redaction bound, got %d", man.Redaction.MaxExcerptRunes)
	}

	// 4. Customer text must be gone from the committed form, and the excerpt kept.
	for _, it := range gold {
		for _, ev := range it.Evidence {
			if ev.Content != "" {
				t.Fatalf("gold item %q still carries full text", it.ItemID)
			}
			if ev.ContentExcerpt == "" {
				t.Fatalf("gold item %q lost its excerpt", it.ItemID)
			}
			if len([]rune(ev.ContentExcerpt)) > maxExcerptRunes {
				t.Fatalf("gold item %q excerpt exceeds the bound", it.ItemID)
			}
		}
	}

	// 5. validate sees a clean file through the CLI too.
	if code := run([]string{"validate", "--gold", goldPath}, devnull, devnull); code != exitOK {
		t.Fatalf("validate on the assembled file exited %d", code)
	}
}

// TestValidateWithLabelsGatesOnKappa covers the `--labels` branch: a label file that
// disagrees with the gold verdicts everywhere drives κ to 0 and must fail the run.
func TestValidateWithLabelsGatesOnKappa(t *testing.T) {
	dir := t.TempDir()
	items := goldWithDistribution()
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	goldPath := filepath.Join(dir, "gold.jsonl")
	if err := writeJSONL(goldPath, lines); err != nil {
		t.Fatal(err)
	}
	// `--labels` takes the *whole* per-annotator file, so κ can be recomputed; a file
	// with one annotator is refused, which is why both appear here.
	var ls []Label
	for i, it := range items {
		ls = append(ls, Label{ItemID: it.ItemID, Annotator: "first", Verdict: it.GoldVerdict, QuoteStatus: "not_provided"})
		v := it.GoldVerdict
		if i < len(items)/2 {
			v = flip(v)
		}
		ls = append(ls, Label{ItemID: it.ItemID, Annotator: "second", Verdict: v, QuoteStatus: "not_provided"})
	}
	labelPath := filepath.Join(dir, "labels.jsonl")
	writeLabels(t, labelPath, ls)

	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()

	if code := run([]string{"validate", "--gold", goldPath, "--labels", labelPath}, devnull, devnull); code != exitInvariant {
		t.Fatalf("a half-disagreeing label file must fail the κ gate, got exit %d", code)
	}
	// A blanket agreement instead must pass: κ = 1 over the same 120 items, which
	// proves the failure above came from the gate and not from the file.
	var agree []Label
	for _, it := range items {
		agree = append(agree,
			Label{ItemID: it.ItemID, Annotator: "first", Verdict: it.GoldVerdict, QuoteStatus: "not_provided"},
			Label{ItemID: it.ItemID, Annotator: "second", Verdict: it.GoldVerdict, QuoteStatus: "not_provided"})
	}
	agreePath := filepath.Join(dir, "agree.jsonl")
	writeLabels(t, agreePath, agree)
	if code := run([]string{"validate", "--gold", goldPath, "--labels", agreePath}, devnull, devnull); code != exitOK {
		t.Fatalf("perfect agreement must pass, got exit %d", code)
	}
}

func writeLabels(t *testing.T, path string, ls []Label) {
	t.Helper()
	lines := make([]string, 0, len(ls))
	for _, l := range ls {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
}

// plannedVerdict recovers the class a plan-shaped pool item was generated for, from
// goldWithDistribution's fixed iteration order.
func plannedVerdict(i int) string {
	n := 0
	for _, v := range GoldVerdicts {
		n += GoldDistribution[v]
		if i < n {
			return v
		}
	}
	return ""
}

func flip(v string) string {
	if v == "supported" {
		return "unsupported"
	}
	return "supported"
}

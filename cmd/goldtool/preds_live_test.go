package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `preds` reads `rgx_report_claim_evaluation` and joins it to
// `rgx_report_assurance_run`. That SQL is the part of this command no unit test can
// check: a renamed column or a changed enum leaves every offline test green and turns
// the export into a runtime error the first time someone builds an E1 file.
//
// Opt-in like the pool tests: without a DSN these skip, because "no database here" is
// not a defect in the tool.

// TestPredsAgainstLiveDatabase exports from the real evaluation table and asserts the
// transcribing promises hold on data nobody wrote for the test.
func TestPredsAgainstLiveDatabase(t *testing.T) {
	dsn := liveDSN(t)
	dir := t.TempDir()

	// The population comes from a pool export, which is what a live database can supply
	// before any annotation exists. `items_has_labels: false` is then the *expected*
	// answer, and it is what makes `eval` refuse the file two commands later.
	poolPath := filepath.Join(dir, "pool.jsonl")
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	if code := run([]string{"pool", "--dsn", dsn, "--out", poolPath, "--max-revisions", "6"}, devnull, devnull); code != exitOK && code != exitIncomplete {
		t.Fatalf("pool exited %d; the live predicate below needs a real population", code)
	}
	items, err := readItems(poolPath)
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if len(items) == 0 {
		t.Skip("the live database has no pool to export predictions for")
	}

	out := filepath.Join(dir, "pred.jsonl")
	stdout, err := os.CreateTemp(dir, "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	code := run([]string{"preds", "--dsn", dsn, "--items", poolPath,
		"--method", "deterministic", "--out", out}, stdout, devnull)
	// 0 = every item has a row from a clean run, 3 = exported but not the whole story
	// (missing rows, or a degraded run). 1 and 2 would mean the tool or the SQL broke.
	if code != exitOK && code != exitIncomplete {
		t.Fatalf("preds exited %d; a live export must be 0 or 3, never a failure", code)
	}

	raw, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var receipt predsReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, raw)
	}
	if receipt.Method != "deterministic" {
		t.Fatalf("receipt method = %q, want the requested method", receipt.Method)
	}
	if receipt.Items != len(items) {
		t.Fatalf("receipt items = %d, pool has %d", receipt.Items, len(items))
	}
	if receipt.RowsConsidered != 0 && len(receipt.MethodsSeen) == 0 {
		t.Fatal("the receipt must say which methods the database holds: an empty file from a typo must look different from an empty revision")
	}

	preds, err := readPredictions(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if len(preds) != receipt.Written {
		t.Fatalf("receipt says %d rows, file has %d", receipt.Written, len(preds))
	}
	if receipt.Written == 0 {
		t.Skip("the live database has no claim evaluation for this method; nothing to transcribe")
	}

	pop := map[string]bool{}
	for _, it := range items {
		pop[it.ItemID] = true
	}
	for _, p := range preds {
		if !pop[p.ItemID] {
			t.Fatalf("prediction %q is not in the population: the export invented a claim", p.ItemID)
		}
		if !containsStr(model.ReportVerdicts(), p.Verdict) {
			t.Fatalf("%q: verdict %q is outside the runtime vocabulary", p.ItemID, p.Verdict)
		}
		if p.QuoteStatus == "" {
			t.Fatalf("%q: quote_status is empty; eval would refuse the file", p.ItemID)
		}
	}
	// The two files must describe the same claims in both directions.
	written := map[string]bool{}
	for _, p := range preds {
		written[p.ItemID] = true
	}
	if receipt.MissingCount != len(items)-len(preds) {
		t.Fatalf("missing = %d, want %d (items − rows)", receipt.MissingCount, len(items)-len(preds))
	}
	for _, id := range receipt.Missing {
		if written[id] {
			t.Fatalf("%q is reported missing and also written", id)
		}
	}
}

// TestPredsRefusesAWorksheetForEvaluation closes the loop between the two commands: a
// predictions file built from an unlabelled population cannot be scored, and the
// refusal must happen in `eval` (which owns the labelling rule), not silently here.
func TestPredsRefusesAWorksheetForEvaluation(t *testing.T) {
	dsn := liveDSN(t)
	dir := t.TempDir()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	poolPath := filepath.Join(dir, "pool.jsonl")
	if code := run([]string{"pool", "--dsn", dsn, "--out", poolPath, "--max-revisions", "6"}, devnull, devnull); code != exitOK && code != exitIncomplete {
		t.Fatalf("pool exited %d", code)
	}
	predPath := filepath.Join(dir, "pred.jsonl")
	if code := run([]string{"preds", "--dsn", dsn, "--items", poolPath,
		"--method", "deterministic", "--out", predPath, "--quiet"}, devnull, devnull); code != exitOK && code != exitIncomplete {
		t.Fatalf("preds exited %d", code)
	}
	stderr, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	code := run([]string{"eval", "--gold", poolPath, "--pred", predPath, "--exp", "E1"}, devnull, stderr)
	if code != exitInvariant {
		t.Fatalf("eval over a pool exited %d, want %d", code, exitInvariant)
	}
	raw, _ := os.ReadFile(stderr.Name())
	if !strings.Contains(string(raw), "not a gold file") {
		t.Fatalf("the refusal must say the input is not a gold file, got %q", raw)
	}
}

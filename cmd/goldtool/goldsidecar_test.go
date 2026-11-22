package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// writeSidecar writes the manifest `assemble` would leave next to a gold file. `problems`
// is the whole point of the type: an empty slice is a clean assembly, a non-empty one is a
// refusal that still left a JSONL behind.
func writeSidecar(t *testing.T, goldPath, sha string, problems []Problem) string {
	t.Helper()
	ms := map[string]any{
		"version":  "v1",
		"file":     goldPath,
		"sha256":   sha,
		"items":    GoldSize(),
		"problems": problems,
	}
	raw, err := json.Marshal(ms)
	if err != nil {
		t.Fatalf("encode sidecar: %v", err)
	}
	mpath := sidecarManifestPath(goldPath)
	if err := os.WriteFile(mpath, raw, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	return mpath
}

// TestEvalRefusesAGoldFileARefusedAssemblyWrote: `assemble` exits 1 but still writes the
// JSONL (that is how an operator sees what was wrong). The next command in the manual's
// chain must not read that draft as gold — otherwise the problems the assembly named turn
// into rows that look measured, which is the one reading error the whole gold set exists
// to prevent.
func TestEvalRefusesAGoldFileARefusedAssemblyWrote(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	mpath := writeSidecar(t, goldPath, sha, []Problem{
		{Code: "distribution_mismatch", Message: "conflicting: got 0, want 24 (doc/301 §9.1)"},
		{Code: "size_mismatch", Message: "the file has 96 items, the plan fixes 120"},
	})
	out := mpath[:len(mpath)-len(sidecarManifestSuffix)] + ".md"

	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath,
		"--exp", "E1", "--method", "control", "--out", out)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d (invariant): a refused assembly's gold file is not judgeable\n%s",
			code, exitInvariant, stderr)
	}
	for _, needle := range []string{
		"was written by an assembly that refused it",
		"records 2 problem(s)",
		"first: distribution_mismatch", // the sentence names the fault, not just the count
		"conflicting: got 0, want 24",
	} {
		if !strings.Contains(stderr, needle) {
			t.Fatalf("stderr does not carry %q:\n%s", needle, stderr)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused judgment still wrote %s: a report next to a refusal reads like a result", out)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("refused run wrote a report to stdout:\n%s", stdout)
	}
}

// TestEvalRefusesAGoldFileEditedAfterAssembly: the manifest's sha256 is what says its
// counts and κ still describe the file in front of you. A hand-edited verdict must be a
// named refusal, not a silently better score.
func TestEvalRefusesAGoldFileEditedAfterAssembly(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	stale := strings.Repeat("ab", 32) // a hash of some *other* file
	mpath := writeSidecar(t, goldPath, stale, nil)

	code, _, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: a gold file that changed after the assembly is not the assembled one\n%s",
			code, exitInvariant, stderr)
	}
	if !strings.Contains(stderr, "describes a different file") {
		t.Fatalf("the refusal does not say the manifest is stale:\n%s", stderr)
	}
	if strings.Contains(stderr, "assembly that refused it") {
		t.Fatalf("a clean-but-stale manifest was reported as a refusal: two different faults, two sentences\n%s", stderr)
	}
	if !strings.Contains(stderr, mpath) {
		t.Fatalf("the sentence does not name the manifest it read (%s):\n%s", mpath, stderr)
	}
}

// TestEvalJudgesAGoldFileWithNoSidecar: absent manifest = nothing claimed (a worksheet, an
// older artifact, a hand-built fixture). The check is additive; it must not make every
// pre-existing gold file unusable.
func TestEvalJudgesAGoldFileWithNoSidecar(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	if _, err := os.Stat(sidecarManifestPath(goldPath)); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a sidecar")
	}
	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0 for the control case:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "sidecar") || strings.Contains(stderr, "refused") {
		t.Fatalf("no sidecar, yet the run explains one away:\n%s", stderr)
	}
	if !strings.Contains(stdout, "unsupported_false_positive_rate") {
		t.Fatalf("report missing the primary metric:\n%s", stdout)
	}
}

// TestPredsRefusesAPopulationFromARefusedAssembly: `preds` binds run verdicts to the item
// ids of the file it is given. Transcribing against a draft gold file would make the
// predictions look like they belong to a gold set that does not exist yet. The refusal
// must happen *before* the database: the population is wrong, not the connection.
func TestPredsRefusesAPopulationFromARefusedAssembly(t *testing.T) {
	gold := evalGold(t)
	goldPath, _ := writeEvalFiles(t, gold, nil)
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	writeSidecar(t, goldPath, sha, []Problem{
		{Code: "pool_outside_sample", Message: "251 of 371 pool row(s) appear in no label file"},
	})
	out := strings.TrimSuffix(goldPath, ".jsonl") + "-pred.jsonl"

	code, _, stderr := runPredsCmd(t, "--dsn", "postgres://unused.invalid/rgx", "--items", goldPath,
		"--method", "deterministic", "--out", out)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d (invariant), not a connection error:\n%s", code, exitInvariant, stderr)
	}
	for _, needle := range []string{"--items:", "was written by an assembly that refused it", "pool_outside_sample"} {
		if !strings.Contains(stderr, needle) {
			t.Fatalf("stderr does not carry %q:\n%s", needle, stderr)
		}
	}
	if strings.Contains(stderr, "unused.invalid") || strings.Contains(stderr, "connect") {
		t.Fatalf("the refusal reached the database, so it depends on one being up:\n%s", stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("wrong population still wrote %s", out)
	}
}

// TestGoldRefusalsReadsAnUnparsableSidecar: a sidecar that cannot be parsed looks like
// evidence and is not. It must be named, not skipped.
func TestGoldRefusalsReadsAnUnparsableSidecar(t *testing.T) {
	gold := evalGold(t)
	goldPath, _ := writeEvalFiles(t, gold, nil)
	if err := os.WriteFile(sidecarManifestPath(goldPath), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	refusals := goldRefusals(goldPath)
	if len(refusals) != 1 {
		t.Fatalf("refusals = %v, want exactly one sentence", refusals)
	}
	if !strings.Contains(refusals[0], "is not a manifest") {
		t.Fatalf("sentence = %q, want it to say the sidecar is unparsable", refusals[0])
	}
}

// TestSidecarManifestPathMatchesAssemble: the consumer's path must be the producer's path,
// derived the same way — a mismatch would make every check silently vacuous.
func TestSidecarManifestPathMatchesAssemble(t *testing.T) {
	if got, want := sidecarManifestPath("tmp/gold/v1/report-assurance-gold.v1.jsonl"),
		"tmp/gold/v1/report-assurance-gold.v1.manifest.json"; got != want {
		t.Fatalf("sidecarManifestPath = %q, want %q", got, want)
	}
}

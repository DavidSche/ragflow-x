package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// TestValidateDescribesARefusedDraftWithoutQualifyingIt pins the one *exception* to E78's
// rule. `eval` and `preds` refuse a gold file that `assemble` refused, because they turn
// it into numbers. `validate` must not: its product is the list of what is wrong with the
// file, so refusing the draft would leave the operator with a diagnostic that declines to
// diagnose. The two answers are different sentences — "the file is coherent" vs "the file
// may be scored" — and a later symmetry pass that wired `goldRefusals` into `validate`
// would silently delete the diagnosis path this test exists to keep.
func TestValidateDescribesARefusedDraftWithoutQualifyingIt(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	writeSidecar(t, goldPath, sha, []Problem{
		{Code: "distribution_mismatch", Message: "conflicting: got 0, want 24 (doc/301 §9.1)"},
	})

	code, stdout, stderr := runCapturingSplit(t, "validate", "--gold", goldPath)
	if code != exitOK {
		t.Fatalf("validate must describe a draft, not refuse it: exit = %d\n%s", code, stderr)
	}
	for _, needle := range []string{"refused it", "output is a draft"} {
		if strings.Contains(stdout+stderr, needle) {
			t.Fatalf("validate borrowed the qualification refusal (%q): %s%s", needle, stdout, stderr)
		}
	}
	// The structural verdict is still owed: "ok" here means "no shape problem found",
	// which is exactly the claim that must not be read as "may be scored".
	if !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("validate dropped its structural verdict: %s", stdout)
	}

	// Same file, same sidecar, the reader that produces numbers: refused.
	report := strings.TrimSuffix(goldPath, ".jsonl") + ".e1.md"
	code, _, errOut := runEvalCmd(t, "--gold", goldPath, "--pred", predPath,
		"--exp", "E1", "--method", "control", "--out", report)
	if code != exitInvariant {
		t.Fatalf("eval on the same draft: exit = %d, want %d\n%s", code, exitInvariant, errOut)
	}
	if !strings.Contains(errOut, "was written by an assembly that refused it") {
		t.Fatalf("eval must name the refusal: %s", errOut)
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatalf("eval wrote %s for a refused draft", report)
	}
}

// writeManifestAs writes a sidecar whose `file` field is `as` (the manifest's own record of
// which file it describes). `assemble` always writes a basename; a test needs to be able to
// write a wrong one.
func writeManifestAs(t *testing.T, goldPath, as, sha string) string {
	t.Helper()
	ms := map[string]any{"version": "v1", "file": as, "sha256": sha, "items": GoldSize()}
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

// TestEvalRefusesAManifestThatNamesAnotherFile: the manifest records which file it
// describes. A copied manifest beside a *different* gold file would otherwise lend its
// counts, κ and problems to that file — the numbers would look measured, and they would be
// another file's numbers. The comparison is by identity (os.SameFile), so the manifest's
// basename resolved against its own directory is the correct spelling and must pass.
func TestEvalRefusesAManifestThatNamesAnotherFile(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	other := strings.TrimSuffix(goldPath, ".jsonl") + "-other.jsonl"
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
	}
	mpath := writeManifestAs(t, goldPath, filepath.Base(other), sha)

	code, _, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: a manifest describing another file is not this file's evidence\n%s",
			code, exitInvariant, stderr)
	}
	for _, needle := range []string{"names", filepath.Base(other), "a different file from", mpath} {
		if !strings.Contains(stderr, needle) {
			t.Fatalf("refusal does not carry %q:\n%s", needle, stderr)
		}
	}
	if strings.Contains(stderr, "describes a different file") {
		t.Fatalf("an identity mismatch was reported as a stale hash: two faults, two sentences\n%s", stderr)
	}
}

// TestEvalAcceptsAManifestThatNamesTheFileByBasename: `assemble` writes a basename, and a
// consumer must resolve it against the manifest's directory. This is the regression guard
// for the identity check above: a string compare against the path we were handed would
// refuse the one spelling the producer actually writes.
func TestEvalAcceptsAManifestThatNamesTheFileByBasename(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	writeManifestAs(t, goldPath, filepath.Base(goldPath), sha)

	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0: the producer's own spelling was refused\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "unsupported_false_positive_rate") {
		t.Fatalf("report missing the primary metric:\n%s", stdout)
	}
}

// TestEvalAcceptsAMovedPairWithNoManifest: a copy with no sidecar claims nothing. The
// identity check must not turn "this copy was never assembled" into "this copy is wrong".
func TestEvalAcceptsAMovedPairWithNoManifest(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	moved := strings.TrimSuffix(goldPath, ".jsonl") + "-moved.jsonl"
	if err := os.WriteFile(moved, []byte(readAll(t, goldPath)), 0o644); err != nil {
		t.Fatalf("write moved copy: %v", err)
	}
	if _, err := os.Stat(sidecarManifestPath(moved)); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a sidecar beside the copy")
	}
	code, _, stderr := runEvalCmd(t, "--gold", moved, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0: an unclaimed copy is not a contradiction (304 §7 item 20)\n%s", code, stderr)
	}
}

// TestEvalRecordsTheManifestItWasJudgedBy: the report must name the sidecar and its own
// hash, so a reviewer can pin them when the evidence is collected (304 §7 item 19). This is
// provenance, not authentication — the test asserts the recorded value is the manifest's
// actual hash, so a stale or invented string would fail.
func TestEvalRecordsTheManifestItWasJudgedBy(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	sha, err := hashFile(goldPath)
	if err != nil {
		t.Fatalf("hash gold: %v", err)
	}
	mpath := writeManifestAs(t, goldPath, filepath.Base(goldPath), sha)
	want, _, err := fileSum(mpath)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}

	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1", "--method", "control")
	if code != exitOK {
		t.Fatalf("exit = %d:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, mpath) || !strings.Contains(stdout, want) {
		t.Fatalf("report does not record the manifest it was judged by (%s sha256 %s):\n%s", mpath, want, stdout)
	}
	if !strings.Contains(stdout, "记录来源，不构成签名") {
		t.Fatalf("report does not say the manifest hash is provenance, not a signature:\n%s", stdout)
	}

	// No sidecar: nothing claimed, so nothing recorded. An empty field would look like a
	// manifest that hashed to nothing.
	if p, s := sidecarFingerprint(strings.TrimSuffix(goldPath, ".jsonl") + "-absent.jsonl"); p != "" || s != "" {
		t.Fatalf("sidecarFingerprint on a path with no manifest = (%q, %q), want empty", p, s)
	}
	if p, s := sidecarFingerprint(goldPath); p != mpath || s != want {
		t.Fatalf("sidecarFingerprint = (%q, %q), want (%q, %q)", p, s, mpath, want)
	}
}

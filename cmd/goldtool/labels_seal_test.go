package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A label file is produced after the freeze, so for a long time the honest sentence about it
// was `not_recorded_by_design` — "the binding rests on row keys and the census, not on bytes".
// That was true of every file written before E134 and would have stayed true of every file
// written after it, because nothing in the chain ever held both files at once and recorded the
// pairing. `labels --check` does hold both, and now leaves the pairing next to the file.
//
// These tests are about the pairing, not about the label rules: an unsealed file must keep
// passing identity (absence is the shape of the files already written), and every way the seal
// can fail to describe *these* bytes next to *this* sheet must be named.

// readSeal parses a companion the way a reader would, without going through the tool.
func readSeal(t *testing.T, path string) labelsSeal {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("seal must exist at %s: %v", path, err)
	}
	var s labelsSeal
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("seal is not JSON: %v", err)
	}
	return s
}

func sealExists(path string) bool {
	_, err := os.Stat(sealPathFor(path))
	return err == nil
}

// The whole point: after a clean check, the pairing is on disk, and it describes the bytes that
// were checked — not the bytes that existed when the annotator started.
func TestLabelsCheckSealsTheBytesItChecked(t *testing.T) {
	f := newKappaFixture(t)
	labels := f.file(t, "qa-a.jsonl", f.answered("qa-a", "supported"))

	code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", labels)
	if code != exitOK {
		t.Fatalf("a complete, valid file must pass: exit %d\n%s%s", code, out, errText)
	}
	sealPath := sealPathFor(labels)
	if sealPath != strings.TrimSuffix(labels, ".jsonl")+".seal.json" {
		t.Fatalf("the seal's name is decided in one place, got %s", sealPath)
	}
	s := readSeal(t, sealPath)
	if s.Schema != labelsSealSchema || s.LabelsSHA256 != fileSHA(t, labels) ||
		s.WorksheetSHA256 != fileSHA(t, f.worksheet) {
		t.Fatalf("the seal must describe these two files: %+v", s)
	}
	if s.Rows != len(f.items) || s.Items != len(f.items) || s.Side != "" && s.Side != "items" {
		t.Fatalf("the seal must carry the census it was written from: %+v", s)
	}
	if len(s.Annotators) != 1 || s.Annotators[0] != "qa-a" {
		t.Fatalf("the seal names who the file belongs to: %+v", s.Annotators)
	}
	// The receipt names the thing it just wrote: a reader should never have to guess where.
	var rec labelsReceipt
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &rec); err != nil {
		t.Fatalf("receipt: %v\n%s", err, out)
	}
	if rec.Seal != sealPath || rec.SealSHA256 != fileSHA(t, sealPath) {
		t.Fatalf("the receipt must carry the seal path and its digest: %+v", rec)
	}
}

// A seal next to a file that failed the check would say "these bytes were checked against this
// sheet" about rows that did not pass. That is worse than no companion.
func TestLabelsCheckSealsNothingWhenTheCheckFoundProblems(t *testing.T) {
	f := newKappaFixture(t)
	ls := f.answered("qa-a", "supported")
	ls[1].ItemID = "no-such-item"
	labels := f.file(t, "qa-a.jsonl", ls)

	code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("an unknown item is an invariant: exit %d\n%s%s", code, out, errText)
	}
	if sealExists(labels) {
		t.Fatalf("a failed check must not seal the file")
	}
	// Unfinished is not failed either: exit 3 means "I could not check", so there is nothing
	// to seal yet.
	partial := f.file(t, "pm-b.jsonl", f.answered("pm-b", "supported")[:1])
	if code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", partial); code != exitIncomplete {
		t.Fatalf("a half-judged file is unfinished: exit %d\n%s%s", code, out, errText)
	}
	if sealExists(partial) {
		t.Fatalf("an unfinished file must not be sealed: %d of %d rows judged", 1, len(f.items))
	}
}

// Read-only drills count the files in a directory; the flag exists so the counting stays
// possible without the seal being described as optional in the workflow.
func TestLabelsCheckCanBeAskedNotToSeal(t *testing.T) {
	f := newKappaFixture(t)
	labels := f.file(t, "qa-a.jsonl", f.answered("qa-a", "supported"))
	code, out, errText := labelsRun(t, "--check", "--no-seal", "--worksheet", f.worksheet, "--labels", labels)
	if code != exitOK {
		t.Fatalf("--no-seal is about the companion, not the check: exit %d\n%s%s", code, out, errText)
	}
	if sealExists(labels) {
		t.Fatalf("--no-seal must write nothing")
	}
	if strings.Contains(out, ".seal.json") {
		t.Fatalf("a receipt must not name a seal that was not written:\n%s", out)
	}
}

// Re-checking is the repair path the mismatch note names, so a second clean check must replace
// the stale seal rather than fail on an existing one.
func TestLabelsCheckRewritesTheSealAfterTheFileChanged(t *testing.T) {
	f := newKappaFixture(t)
	labels := f.file(t, "qa-a.jsonl", f.answered("qa-a", "supported"))
	if code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", labels); code != exitOK {
		t.Fatalf("first check: exit %d\n%s%s", code, out, errText)
	}
	first := readSeal(t, sealPathFor(labels))
	// Same judgements, different bytes: a trailing newline is enough to make the old seal
	// describe a file that no longer exists.
	raw, err := os.ReadFile(labels)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(labels, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", labels); code != exitOK {
		t.Fatalf("second check: exit %d\n%s%s", code, out, errText)
	}
	second := readSeal(t, sealPathFor(labels))
	if second.LabelsSHA256 == first.LabelsSHA256 || second.LabelsSHA256 != fileSHA(t, labels) {
		t.Fatalf("the seal must follow the file: first %s second %s file %s",
			first.LabelsSHA256, second.LabelsSHA256, fileSHA(t, labels))
	}
}

// sealedPair is the readiness fixture with both annotators' files sealed against this sheet.
func sealedPair(t *testing.T, f *kappaFixture) string {
	t.Helper()
	qa := f.file(t, "qa-a.jsonl", f.answered("qa-a", "supported"))
	pm := f.file(t, "pm-b.jsonl", f.answered("pm-b", "supported"))
	for _, p := range []string{qa, pm} {
		if code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", p); code != exitOK {
			t.Fatalf("sealing %s: exit %d\n%s%s", p, code, out, errText)
		}
	}
	return qa + "," + pm
}

// sealablePlan is the pool + frozen plan a readiness reading needs, with three items.
func sealablePlan(t *testing.T, f *kappaFixture) (string, string) {
	t.Helper()
	pool := writeReadinessPool(t, f.dir, []int{1, 1, 1})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)
	return pool, prereg
}

func labelInputs(rec Readiness) map[string]readinessInput {
	out := map[string]readinessInput{}
	for _, in := range rec.Identity.Inputs {
		if in.Role == "labels" {
			out[in.Path] = in
		}
	}
	return out
}

// The binding E132 could not make: two sealed files, and the receipt says the pairing was
// checked rather than asserting it.
func TestReadinessBindsASealedPairToTheWorksheet(t *testing.T) {
	f := newKappaFixture(t)
	pool, prereg := sealablePlan(t, f)
	labels := sealedPair(t, f)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitOK {
		t.Fatalf("a sealed pair on its own sheet is ready: exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.State != readinessPass {
		t.Fatalf("identity must pass: %s", rec.Identity.Reason)
	}
	ins := labelInputs(rec)
	if len(ins) != 2 {
		t.Fatalf("two label files, got %d", len(ins))
	}
	for path, in := range ins {
		if in.State != identityMatch || in.Seal == nil || !in.Seal.Present || in.Seal.State != identityMatch {
			t.Fatalf("%s must be sealed and match: %+v", path, in)
		}
		if in.Seal.LabelsSHA256 != in.SHA256 || in.Seal.WorksheetSHA256 != fileSHA(t, f.worksheet) {
			t.Fatalf("%s: the seal's digests must be the ones in the row: %+v", path, in.Seal)
		}
	}
	if !strings.Contains(rec.Identity.Reason, "2 份标注带 seal") {
		t.Fatalf("the reason must say what was proven, not just that it passed: %s", rec.Identity.Reason)
	}
}

// The file written before the rule keeps its old sentence: absence is not a failure, because
// calling it one would retroactively condemn the 120 rows already judged.
func TestReadinessKeepsAnUnsealedFileByDesign(t *testing.T) {
	f := newKappaFixture(t)
	pool, prereg := sealablePlan(t, f)
	labels := completePair(t, f)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitOK {
		t.Fatalf("an unsealed but valid pair must still be ready: exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.State != readinessPass {
		t.Fatalf("unsealed is not unverified: %s", rec.Identity.Reason)
	}
	for path, in := range labelInputs(rec) {
		if in.State != identityByDesign || in.Seal == nil || in.Seal.Present {
			t.Fatalf("%s must say a seal is absent by design: %+v", path, in)
		}
	}
	if !strings.Contains(rec.Identity.Reason, "按设计无冻结指纹") {
		t.Fatalf("the boundary sentence must survive: %s", rec.Identity.Reason)
	}
}

// Every row can be perfect and the file still cannot be read next to this sheet: it was checked
// against another one. No count in A or B changes.
func TestReadinessRefusesLabelsSealedAgainstAnotherWorksheet(t *testing.T) {
	f := newKappaFixture(t)
	pool, prereg := sealablePlan(t, f)
	// Same rows, one byte longer: a second sheet the same annotations were validated against.
	raw, err := os.ReadFile(f.worksheet)
	if err != nil {
		t.Fatal(err)
	}
	other := f.worksheet + ".other"
	if err := os.WriteFile(other, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var sealed []string
	for _, side := range []struct{ name, who string }{{"qa-a.jsonl", "qa-a"}, {"pm-b.jsonl", "pm-b"}} {
		p := f.file(t, side.name, f.answered(side.who, "supported"))
		if code, out, errText := labelsRun(t, "--check", "--worksheet", other, "--labels", p); code != exitOK {
			t.Fatalf("the labels are valid for the other sheet too: exit %d\n%s%s", code, out, errText)
		}
		sealed = append(sealed, p)
	}
	qa := sealed[0]

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", strings.Join(sealed, ","))
	if code != exitInvariant {
		t.Fatalf("a seal for another sheet is an invariant: exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.State != readinessFail || rec.Ready {
		t.Fatalf("identity must fail: %+v", rec.Identity)
	}
	in := labelInputs(rec)[qa]
	if in.Seal == nil || in.Seal.State != identityMismatch || !strings.Contains(in.Seal.Note, "另一份工作表") {
		t.Fatalf("the reason must name the sheet, not the file: %+v", in.Seal)
	}
	if rec.B.State != readinessPass {
		t.Fatalf("this test must fail on identity alone, B=%s", rec.B.State)
	}
	// The frozen plan has no labels row, so a seal disagreement is not a disagreement with the
	// plan. A sentence that said "another batch" here would send the reader to re-freeze when
	// the repair is to re-check the file against the sheet it was drawn from.
	if strings.Contains(rec.Identity.Reason, "冻结计划") || !strings.Contains(rec.Identity.Reason, "标注文件与本次工作表对不上") {
		t.Fatalf("the reason must name the labels' own reference: %s", rec.Identity.Reason)
	}
}

// The seal describes bytes. When the bytes move on, the seal describes a file that no longer
// exists, and the next step is printed rather than guessed.
func TestReadinessNoticesLabelsEditedAfterTheSeal(t *testing.T) {
	f := newKappaFixture(t)
	pool, prereg := sealablePlan(t, f)
	labels := sealedPair(t, f)
	qa := strings.Split(labels, ",")[0]
	raw, err := os.ReadFile(qa)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qa, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("a seal that no longer describes the file is an invariant: exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	in := labelInputs(rec)[qa]
	if in.Seal == nil || in.Seal.State != identityMismatch || !strings.Contains(in.Seal.Note, "seal 之后文件改过") {
		t.Fatalf("the note must say the file moved on: %+v", in.Seal)
	}
	if !strings.Contains(in.Seal.Note, "labels --check") {
		t.Fatalf("the note must name the repair: %+v", in.Seal)
	}
}

// A seal edited by hand to name someone else is not a description of this file. It cannot stop
// a deliberate forger — the same hand can edit the digests — but it catches the operator error
// of copying a seal from the other annotator's directory.
func TestReadinessNoticesASealThatNamesOtherAnnotators(t *testing.T) {
	f := newKappaFixture(t)
	pool, prereg := sealablePlan(t, f)
	labels := sealedPair(t, f)
	qa := strings.Split(labels, ",")[0]
	s := readSeal(t, sealPathFor(qa))
	s.Annotators = []string{"pm-b"}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealPathFor(qa), body, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("a seal naming another annotator is an invariant: exit %d\n%s%s", code, out, errText)
	}
	in := labelInputs(readinessReceipt(t, out))[qa]
	if in.Seal == nil || in.Seal.State != identityMismatch || !strings.Contains(in.Seal.Note, "标注者") {
		t.Fatalf("the note must name the disagreement: %+v", in.Seal)
	}
}

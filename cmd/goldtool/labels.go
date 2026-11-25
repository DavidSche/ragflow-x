package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The two-annotator loop has one hand-written file in it: each annotator's
// `labels-<name>.jsonl`. The manual's §6 step 1 says so out loud ("文件由人写，工具不生成"),
// and that is right about the *judgements* — `verdict` and `quote_status` must come from a
// person. But everything around them is transcription: 120 `item_id`s copied out of a
// worksheet, and a `quote_status` that nothing checks until `assemble` has already merged
// two files and the error message is about a gold item rather than about line 47 of
// somebody's file.
//
// So `labels` does the two halves that are not judgement:
//
//	--stub   emits one row per worksheet item with `item_id` and `annotator` and nothing
//	         else. No id is ever typed by hand, and no worksheet field is echoed (an
//	         echoed `prior_support_status` is the model's opinion sitting in the annotator's
//	         file — doc/301 §4 rule 3 says treat it as absent, so the stub never carries it).
//	--check  runs every per-label rule the manual states, against the worksheet, before κ:
//	         unknown ids (κ drops them silently), duplicates, closed vocabularies, and the
//	         two question-① rules (`not_provided` ⇔ empty quote, evidence ids are
//	         snapshot ids and never candidate chunk ids).
//
// `--stub` refuses a source that already carries `gold_verdict`, and refuses to overwrite a
// file: a stub that can replace a half-filled file can erase judgements that were never
// recorded anywhere else.

// labelsSchema identifies the receipt shape so a future reader can tell receipts apart.
const labelsSchema = "rgx.goldtool.labels/1"

// labelsSealSchema identifies the companion written next to a *filled* label file.
//
// The gap it closes (doc/304 E132's own boundary sentence): a label file is produced after the
// freeze, so the frozen plan cannot name its digest, and `readiness` had to say
// `not_recorded_by_design` — "the binding rests on row keys and the census, not on bytes".
// That was true of every file written before this seal existed, and it would still be true of
// every file written *after* it unless some tool that holds both files at once records the
// pairing. `labels --check` is that tool: it already reads the sheet and the labels together
// and knows whether every row is valid, so on a clean check it writes `<labels>.seal.json`
// saying "these exact bytes were checked against this exact sheet".
//
// What the seal is not: a signature. Anyone who can rewrite the labels file can rewrite the
// seal next to it. It moves "which files were checked together" from memory into evidence,
// which is the same thing `prereg --digest` does — no more, and the receipt says so.
const labelsSealSchema = "rgx.goldtool.labels.seal/1"

// labelsSeal is the companion's whole content. It records the digest of the *labels* bytes and
// of the *worksheet* they were checked against, because those are the two things a later
// reader cannot recompute once the annotator, the rows and the sheet have drifted apart.
type labelsSeal struct {
	Schema          string `json:"schema"`
	Labels          string `json:"labels"`
	LabelsSHA256    string `json:"labels_sha256"`
	Bytes           int    `json:"bytes"`
	Worksheet       string `json:"worksheet"`
	WorksheetSHA256 string `json:"worksheet_sha256"`
	Packet          string `json:"packet,omitempty"`
	PacketSHA256    string `json:"packet_sha256,omitempty"`
	// Annotators is every name the file carries, not the `--annotator` the check was scoped
	// to: a scoped check on a pooled file is legitimate, and recording only the scope would
	// make the seal disagree with its own file.
	Annotators []string `json:"annotators"`
	ScopedTo   string   `json:"scoped_to,omitempty"`
	Rows       int      `json:"rows"`
	Items      int      `json:"items"`
	Side       string   `json:"side"`
	Note       string   `json:"note"`
}

// sealPathFor is the one place the companion's name is decided, so `labels --check` and
// `readiness` cannot disagree about where to look.
func sealPathFor(labelsPath string) string {
	ext := filepath.Ext(labelsPath)
	if ext == "" {
		return labelsPath + ".seal.json"
	}
	return strings.TrimSuffix(labelsPath, ext) + ".seal.json"
}

// stubRow is the whole content of a stub line. Kept as its own type rather than a `Label`
// with empty fields: `{"verdict":""}` and "the field is absent" would mean different things
// to a reader ("judged empty" vs "not judged yet"), and only one of them is true.
//
// `CandidateIndex` is the second half of a candidates-side row's identity (doc/301 §9.1.3
// 一): the stub writes it so nobody types it, and `--check` refuses a row without it rather
// than guessing 0 (guessing re-keys every row of a file somebody sorted by anything else).
type stubRow struct {
	ItemID         string `json:"item_id"`
	CandidateIndex *int   `json:"candidate_index,omitempty"`
	Annotator      string `json:"annotator"`
}

// labelsReceipt is what both modes print. `Labelled` counts rows that carry a verdict;
// `Items` is the worksheet's size. They differ while the annotator is still working, and
// that difference is the receipt's most useful number.
type labelsReceipt struct {
	Schema          string `json:"schema"`
	Mode            string `json:"mode"`
	Worksheet       string `json:"worksheet"`
	WorksheetSHA256 string `json:"worksheet_sha256"`
	File            string `json:"file"`
	FileSHA256      string `json:"file_sha256,omitempty"`
	Annotator       string `json:"annotator,omitempty"`
	// Side says what `items` counts: one claim per row (`items`) or one frozen candidate
	// per row (`candidates`, doc/301 §9.1.3 一). Without it, "36 items" on a 30-claim
	// worksheet reads as a miscount rather than as the unit the plan asked for.
	Side          string         `json:"side"`
	Items         int            `json:"items"`
	ItemIDs       int            `json:"item_ids"`
	Rows          int            `json:"rows"`
	Labelled      int            `json:"labelled"`
	Unfilled      []string       `json:"unfilled,omitempty"`
	Missing       []string       `json:"missing,omitempty"`
	Annotators    []string       `json:"annotators,omitempty"`
	SkippedOther  int            `json:"skipped_other_annotator,omitempty"`
	ByVerdict     map[string]int `json:"by_verdict,omitempty"`
	ByQuoteStatus map[string]int `json:"by_quote_status,omitempty"`
	Problems      []Problem      `json:"problems,omitempty"`
	// Packet, when `--packet` was given, is the file the annotator reads: the chain from
	// sample to a filled label file is checked, not assumed.
	Packet       string `json:"packet,omitempty"`
	PacketSHA256 string `json:"packet_sha256,omitempty"`
	PacketRows   int    `json:"packet_rows,omitempty"`
	// Seal, on a clean `--check`, is the companion written next to the label file. It is in
	// the receipt for the reason every other path is: a reader must be able to find the thing
	// the receipt talks about without guessing where the tool put it.
	Seal       string `json:"seal,omitempty"`
	SealSHA256 string `json:"seal_sha256,omitempty"`
	OK         bool   `json:"ok"`
}

func runLabels(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("labels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stub := fs.Bool("stub", false, "emit a blank label skeleton (one row per worksheet item)")
	check := fs.Bool("check", false, "check a filled label file against the worksheet")
	worksheet := fs.String("worksheet", "", "worksheet JSONL produced by `goldtool sample`")
	labelsPath := fs.String("labels", "", "label JSONL to check (--check)")
	annotator := fs.String("annotator", "", "the annotator this file belongs to")
	out := fs.String("out", "", "stub output path (--stub)")
	packetPath := fs.String("packet", "", "annotation packet from `sample --blind-out`: the file the annotator actually reads (--stub)")
	noSeal := fs.Bool("no-seal", false, "do not write the `<labels>.seal.json` companion (--check): for read-only drills")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case *stub && *check:
		fmt.Fprintln(stderr, "goldtool labels: --stub and --check are different jobs; pick one")
		return exitUsage
	case !*stub && !*check:
		fmt.Fprintln(stderr, "goldtool labels: pass --stub (emit a skeleton) or --check (verify a filled file)")
		return exitUsage
	}
	if trimSpace(*worksheet) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --worksheet is required: a label file is only meaningful next to the sample it labels")
		return exitUsage
	}

	ws, err := readWorksheet(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: worksheet: %v\n", err)
		return exitUsage
	}
	if len(ws.Rows) == 0 {
		fmt.Fprintf(stderr, "goldtool labels: worksheet %s has no items\n", *worksheet)
		return exitInvariant
	}
	sum, err := hashFile(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: worksheet: %v\n", err)
		return exitUsage
	}

	if *stub {
		return labelsStub(ws, sum, *packetPath, *annotator, *out, stdout, stderr)
	}
	return labelsCheck(ws, sum, *labelsPath, *annotator, *packetPath, *noSeal, stdout, stderr)
}

func labelsStub(ws *worksheet, sum, packet, annotator, out string, stdout, stderr *os.File) int {
	worksheet := ws.Path
	if trimSpace(annotator) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --annotator is required with --stub; a stub without a name is a file "+
			"two people can both fill in and merge into one opinion")
		return exitUsage
	}
	if trimSpace(out) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --out is required with --stub")
		return exitUsage
	}
	if n := countLabelledRows(ws); n > 0 {
		fmt.Fprintf(stderr, "goldtool labels: the worksheet carries gold_verdict on %d of %d row(s): stubbing it "+
			"would hand the annotator the answer to the question they are supposed to answer (doc/301 §4 rule 3)\n",
			n, len(ws.Rows))
		return exitInvariant
	}
	// A worksheet with a repeated row key would produce a skeleton that `--check` then rejects
	// as the annotator's duplicate rows. Refuse it here, where the fault actually is. The key
	// is side-aware: on the candidate side `item_id` repeating is the plan (doc/301 §9.1.3 一)
	// and only `(item_id, candidate_index)` repeating is a fault.
	if dups := ws.duplicateKeys(); len(dups) > 0 {
		if ws.candidateSide() {
			fmt.Fprintf(stderr, "goldtool labels: the worksheet repeats %d row key(s) %v: a candidates-side "+
				"row is (item_id, candidate_index), and two rows with the same pair are one judgement "+
				"written twice\n", len(dups), cap10(dups))
		} else {
			fmt.Fprintf(stderr, "goldtool labels: the worksheet repeats %d item_id(s) %v: a stub would copy the "+
				"duplicate, and `--check` would report it as the annotator's mistake\n", len(dups), cap10(dups))
		}
		return exitInvariant
	}

	// The skeleton is the only file the annotator writes, but the packet is the only file
	// they *read*: checking it here is where "the answer is not in the packet" stops being a
	// promise made by the packet's writer and becomes a refusal by the stub.
	var pk packetSummary
	if trimSpace(packet) != "" {
		var err error
		pk, err = checkPacket(packet, ws.keys())
		if err != nil {
			fmt.Fprintf(stderr, "goldtool labels: %v\n", err)
			return exitInvariant
		}
	}

	lines := make([]string, 0, len(ws.Rows))
	for _, r := range ws.Rows {
		raw, err := json.Marshal(stubRow{ItemID: r.ItemID, CandidateIndex: r.CandidateIndex,
			Annotator: trimSpace(annotator)})
		if err != nil {
			fmt.Fprintf(stderr, "goldtool labels: encode %s: %v\n", r.Key, err)
			return exitInvariant
		}
		lines = append(lines, string(raw))
	}
	code := writeLinesExclusive(out, lines, stderr)
	if code != exitOK {
		return code
	}

	fmt.Fprintf(stdout, "goldtool labels: stub for %q over %d %s(s) -> %s\n", trimSpace(annotator),
		len(ws.Rows), unitNoun(ws), out)
	fmt.Fprintf(stdout, "  worksheet %s  sha256 %s\n", worksheet, sum)
	if pk.Rows > 0 {
		fmt.Fprintf(stdout, "  packet    %s  sha256 %s  (%d row(s), no prior_*/gold_* key)\n",
			pk.Path, pk.SHA256, pk.Rows)
	}
	fmt.Fprintf(stdout, "  fill in, per row: verdict, quote_status, quote (unless not_provided), "+
		"evidence_ids (snapshot ids only), candidate_chunk_id (only when a candidate row is the basis), note\n")
	if ws.candidateSide() {
		fmt.Fprintf(stdout, "  this is the candidates side: do not change candidate_index — it is half of the row's "+
			"identity, and the two of you are aligned on (item_id, candidate_index)\n")
	}
	fmt.Fprintf(stdout, "  then: goldtool labels --check --worksheet %s --labels %s\n", worksheet, out)
	printJSON(stdout, labelsReceipt{
		Schema: labelsSchema, Mode: "stub", Worksheet: worksheet, WorksheetSHA256: sum,
		File: out, Annotator: trimSpace(annotator), Side: ws.Side,
		Items: len(ws.Rows), ItemIDs: ws.distinctItemIDs(), Rows: len(lines),
		Packet: pk.Path, PacketSHA256: pk.SHA256, PacketRows: pk.Rows, OK: true,
	})
	return exitOK
}

// unitNoun is what one worksheet row is called in messages. "item" on the candidates side
// would be wrong in exactly the way doc/304 E118 is about: the count and the noun have to
// agree, or a reader concludes the worksheet lost rows.
func unitNoun(ws *worksheet) string { return nounForSide(ws.Side) }

// countLabelledRows is the blindness check: a worksheet is a question, a gold file is an
// answer.
func countLabelledRows(ws *worksheet) int {
	n := 0
	for _, r := range ws.Rows {
		if r.labelled() {
			n++
		}
	}
	return n
}

// sortedKeys is the deterministic rendering of a set of names; map order would make the
// same file produce a different message on every run.
func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeLinesExclusive(path string, lines []string, stderr *os.File) int {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "goldtool labels: %v\n", err)
			return exitUsage
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// The file may already hold judgements; regenerating it "because the stub is
			// only the skeleton" is how a half-filled file loses an hour of reading.
			fmt.Fprintf(stderr, "goldtool labels: %s already exists: refusing to overwrite it "+
				"(a second stub must go to a new path, so nothing that was already judged can be erased)\n", path)
			return exitUsage
		}
		fmt.Fprintf(stderr, "goldtool labels: %v\n", err)
		return exitUsage
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			fmt.Fprintf(stderr, "goldtool labels: write %s: %v\n", path, err)
			return exitUsage
		}
	}
	return exitOK
}

func labelsCheck(ws *worksheet, sum, labelsPath, wantAnnotator, packetPath string, noSeal bool, stdout, stderr *os.File) int {
	worksheet := ws.Path
	if trimSpace(labelsPath) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --labels is required with --check")
		return exitUsage
	}
	ls, err := readLabels(labelsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: labels: %v\n", err)
		return exitUsage
	}
	fileSum, err := hashFile(labelsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: labels: %v\n", err)
		return exitUsage
	}

	byKey, order := ws.byKey()

	var problems []Problem
	// A worksheet fault is reported as a worksheet fault: on the candidate side a repeated
	// (item_id, candidate_index) would otherwise read as the annotator's duplicate row.
	if dups := ws.duplicateKeys(); len(dups) > 0 {
		problems = append(problems, Problem{"worksheet_duplicate_row",
			fmt.Sprintf("%s repeats %d row key(s) %v", worksheet, len(dups), cap10(dups))})
	}
	seen := map[string]int{}
	annotators := map[string]int{}
	for _, l := range ls {
		if a := trimSpace(l.Annotator); a != "" {
			annotators[a]++
		}
	}
	want := trimSpace(wantAnnotator)
	if want != "" && annotators[want] == 0 {
		// The whole file would be skipped, and an empty check reads like a pass. A name
		// that matches nothing is a typo, and saying so beats reporting 0/120 labelled.
		problems = append(problems, Problem{"label_annotator_absent",
			fmt.Sprintf("no row in %s says annotator %q (the file names %v): check the spelling before reading "+
				"the coverage number", labelsPath, want, sortedCountKeys(annotators))})
	}
	receipt := labelsReceipt{
		Schema: labelsSchema, Mode: "check", Worksheet: worksheet, WorksheetSHA256: sum,
		File: labelsPath, FileSHA256: fileSum, Annotator: want, Side: ws.Side,
		Items: len(ws.Rows), ItemIDs: ws.distinctItemIDs(), Rows: len(ls),
		Annotators: sortedCountKeys(annotators),
		ByVerdict:  map[string]int{}, ByQuoteStatus: map[string]int{},
	}
	var unfilled []string

	for i, l := range ls {
		line := i + 1
		at := fmt.Sprintf("line %d", line)
		// `--annotator` scopes the check to one person's rows: κ eats a pooled file, but
		// "did A judge every item" is only answerable from one side at a time. A row with no
		// annotator at all is *not* someone else's row — it falls through to the named
		// problem below.
		if want != "" {
			if a := trimSpace(l.Annotator); a != "" && a != want {
				receipt.SkippedOther++
				continue
			}
		}
		switch {
		case trimSpace(l.ItemID) == "":
			problems = append(problems, Problem{"label_without_item_id", at})
			continue
		case trimSpace(l.Annotator) == "":
			problems = append(problems, Problem{"label_without_annotator",
				at + ": every row names who judged it, or two files merge into one opinion"})
			continue
		}
		key, err := labelKeyForSide(l, ws.Side)
		if err != nil {
			// A candidates-side label without `candidate_index` cannot be placed. Defaulting
			// it to 0 would make the file look complete and attach every judgement to the
			// first candidate of its claim — the one mistake a composite key exists to stop.
			problems = append(problems, Problem{"label_without_candidate_index",
				fmt.Sprintf("%s: %s; on this side write candidate_index (the stub already does)", at, err)})
			continue
		}
		r, known := byKey[key]
		if !known {
			// κ drops ids it does not recognise, so a single mistyped character here
			// silently shrinks the pair count — the number still looks like a κ.
			problems = append(problems, Problem{"label_for_unknown_item",
				fmt.Sprintf("%s: %s is not in %s (κ would skip it silently, so the row would never be counted)",
					at, key, worksheet)})
			continue
		}
		if prev, dup := seen[key]; dup {
			problems = append(problems, Problem{"duplicate_label",
				fmt.Sprintf("%s: %s was already labelled at line %d", at, key, prev)})
			continue
		}
		seen[key] = line

		if trimSpace(l.Verdict) == "" {
			// Not a violation: the annotator is not finished. Counted separately so
			// "3 rows still blank" never reads as "3 rows are wrong".
			unfilled = append(unfilled, key)
			continue
		}
		receipt.Labelled++
		receipt.ByVerdict[l.Verdict]++
		if qs := trimSpace(l.QuoteStatus); qs != "" {
			receipt.ByQuoteStatus[qs]++
		}
		for _, msg := range checkLabelRules(r, l) {
			problems = append(problems, Problem{"label_rule", at + " " + key + ": " + msg})
		}
	}

	if len(annotators) > 1 && want == "" {
		names := sortedCountKeys(annotators)
		problems = append(problems, Problem{"labels_mix_annotators",
			fmt.Sprintf("the file carries %d annotators %v: κ needs a pooled file, but coverage and question-① rules "+
				"are per person, so pass --annotator NAME to check one side", len(names), names)})
	}

	var missing []string
	for _, key := range order {
		if _, ok := seen[key]; ok {
			continue
		}
		missing = append(missing, key)
	}
	todo := len(unfilled) + len(missing)

	receipt.Unfilled = cap10(unfilled)
	receipt.Missing = cap10(missing)
	receipt.Problems = problems
	receipt.OK = len(problems) == 0 && todo == 0

	// The seal is written only for a check that found nothing: a companion that appears next
	// to a file with problems would say "these bytes were checked against this sheet" about a
	// file that failed the check, which is worse than no companion at all.
	if receipt.OK && !noSeal {
		// `--packet` is optional on `--check`, so the seal carries it only when the caller
		// named it — an empty digest in a seal reads as "no packet", which would be a claim.
		var pkPath, pkSum string
		if trimSpace(packetPath) != "" {
			pkPath = packetPath
			if s, err := hashFile(packetPath); err == nil {
				pkSum = s
			}
		}
		raw, err := os.ReadFile(labelsPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool labels: seal: %v\n", err)
			return exitInvariant
		}
		seal := labelsSeal{
			Schema: labelsSealSchema, Labels: labelsPath, LabelsSHA256: fileSum, Bytes: len(raw),
			Worksheet: worksheet, WorksheetSHA256: sum, Packet: pkPath, PacketSHA256: pkSum,
			Annotators: sortedCountKeys(annotators), ScopedTo: want,
			Rows: len(ls), Items: len(ws.Rows), Side: ws.Side,
			Note: "由 `goldtool labels --check` 在一次干净的检查之后写：它绑定的是这些字节与这份工作表，" +
				"不是谁的签名（能改标注文件的人也能改它）",
		}
		body, err := json.MarshalIndent(seal, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "goldtool labels: seal: %v\n", err)
			return exitInvariant
		}
		body = append(body, '\n')
		receipt.Seal = sealPathFor(labelsPath)
		if err := os.WriteFile(receipt.Seal, body, 0o644); err != nil {
			fmt.Fprintf(stderr, "goldtool labels: seal: %v\n", err)
			return exitInvariant
		}
		if s, err := hashFile(receipt.Seal); err == nil {
			receipt.SealSHA256 = s
		}
	}

	for _, pr := range problems {
		fmt.Fprintf(stderr, "goldtool labels: %s: %s\n", pr.Code, pr.Message)
	}
	fmt.Fprintf(stdout, "goldtool labels: %s  %d/%d %s(s) labelled, %d problem(s)\n",
		labelsPath, receipt.Labelled, receipt.Items, unitNoun(ws), len(problems))
	if len(unfilled) > 0 {
		fmt.Fprintf(stdout, "  unfilled (%d): %s\n", len(unfilled), strings.Join(cap10(unfilled), " "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(stdout, "  missing (%d): %s\n", len(missing), strings.Join(cap10(missing), " "))
	}
	if receipt.Seal != "" {
		fmt.Fprintf(stdout, "  seal      %s  sha256 %s\n", receipt.Seal, receipt.SealSHA256)
	}
	printJSON(stdout, receipt)
	switch {
	case len(problems) > 0:
		return exitInvariant
	case todo > 0:
		// Not finished is not a finding: exit 3 keeps "I could not check" apart from
		// "it holds", the same three-sentence discipline as `prereg --check`.
		fmt.Fprintf(stderr, "goldtool labels: %d %s row(s) not judged yet: this file is not wrong, it is unfinished "+
			"(exit 3, not pass)\n", todo, unitNoun(ws))
		return exitIncomplete
	}
	return exitOK
}

// checkLabelRules is the per-label half of the manual, applied to a real worksheet row.
// It exists because every one of these rules was previously enforced only *after* merging:
// an invalid `quote_status` surfaced as "gold item has no valid gold_quote_status", which
// names neither the annotator nor the line, and a `quote_status` disagreement between the
// two annotators surfaced as nothing at all.
//
// It takes a `wsRow`, not an `Item`, because on the candidates side the row is one frozen
// candidate: the evidence list is the claim's, and the candidate pool is exactly this row
// (doc/301 §9.1.3 一). The rules are the same sentences; what changes is what they are
// checked against.
func checkLabelRules(r wsRow, l Label) []string {
	var v []string
	verdict := trimSpace(l.Verdict)
	if !contains(GoldVerdicts, verdict) {
		v = append(v, fmt.Sprintf("verdict %q is not one of %v (doc/301 §9.1: the evaluator states "+
			"insufficient/abstained/not_required are not labels)", verdict, GoldVerdicts))
	}

	status := trimSpace(l.QuoteStatus)
	quote := trimSpace(l.Quote)
	switch {
	case status == "":
		v = append(v, "quote_status is empty: the manual asks two separate questions, and a verdict without a "+
			"quote judgement has only answered the second one")
	case !contains(model.ReportQuoteStatuses(), status):
		v = append(v, fmt.Sprintf("quote_status %q is not one of %v", status, model.ReportQuoteStatuses()))
	case status == model.ReportQuoteStatusNotProvided && quote != "":
		v = append(v, "quote_status is not_provided but quote is non-empty (the two must agree: "+
			"\"there was no quote\" and \"here is the quote\" cannot both be true)")
	case status != model.ReportQuoteStatusNotProvided && quote == "":
		v = append(v, fmt.Sprintf("quote_status is %q but quote is empty: a quote claim without the text is "+
			"unfalsifiable", status))
	}

	// Evidence ids are snapshot ids (doc/301 §9.1: candidate searching may not create
	// brand-new citations). Naming a candidate chunk here is the single most likely
	// confusion in the whole manual — §2 says candidates are never evidence — so it gets
	// its own sentence instead of "not a cited snapshot".
	snaps := map[string]bool{}
	chunks := map[string]bool{}
	for _, ev := range r.evidence() {
		snaps[ev.SnapshotID] = true
		chunks[ev.ChunkID] = true
	}
	candidates := map[string]bool{}
	for _, c := range r.candidatePool() {
		candidates[c.ChunkID] = true
	}
	for _, id := range l.EvidenceIDs {
		id = trimSpace(id)
		if id == "" {
			continue
		}
		if snaps[id] {
			continue
		}
		if candidates[id] {
			v = append(v, fmt.Sprintf("evidence_ids names %q, which is a candidate chunk_id: candidates are never "+
				"evidence (doc/301 §5.3.1) — use evidence[].snapshot_id", id))
			continue
		}
		if chunks[id] {
			v = append(v, fmt.Sprintf("evidence_ids names %q, which is an evidence chunk_id: the gold file records "+
				"snapshot ids, so a chunk id here cannot be replayed", id))
			continue
		}
		v = append(v, fmt.Sprintf("evidence_ids names %q, which is not one of this item's %d cited snapshot(s)",
			id, len(snaps)))
	}

	if bad := checkRowCandidateRef(r, l); bad != "" {
		v = append(v, bad)
	}
	return v
}

// checkRowCandidateRef states what a candidate reference means on each side, and it means
// two things:
//
//   - items side: `candidate_index`/`candidate_chunk_id` name *which* of the claim's frozen
//     candidates drove the verdict (doc/301 §9.1.3 二) — a reference, checked against the
//     item's own pool by `checkLabelCandidateRef`.
//   - candidates side: the row already *is* candidate #k, so `candidate_index` is half of
//     the identity and must say k, and `candidate_chunk_id` may only agree with the row's
//     own chunk. Naming another candidate here would be a judgement filed against the
//     wrong row — the one error a composite key makes visible instead of invisible.
func checkRowCandidateRef(r wsRow, l Label) string {
	if r.Cand == nil {
		return checkLabelCandidateRef(*r.Item, l)
	}
	want := *r.CandidateIndex
	if l.CandidateIndex != nil && *l.CandidateIndex != want {
		return fmt.Sprintf("candidate_index %d does not match the row's %d (the row is %s: on the "+
			"candidates side that index is half of the row's identity, not a reference)",
			*l.CandidateIndex, want, r.Key)
	}
	if ref := trimSpace(l.CandidateChunkID); ref != "" && ref != r.Cand.ChunkID {
		return fmt.Sprintf("candidate_chunk_id %q is not this row's chunk %q: a candidates-side label "+
			"judges %s, and naming a different chunk files the judgement against another row",
			ref, r.Cand.ChunkID, r.Key)
	}
	return ""
}

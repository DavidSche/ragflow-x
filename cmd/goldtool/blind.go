package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The annotation packet, and why it is a second file rather than a flag on the worksheet.
//
// doc/301 §9.1 rule 3 says the model's prior opinion (`prior_support_status`) is to be
// treated as absent while annotating. Until now that was a sentence in a manual, and the
// file an annotator actually opens — the worksheet — carried the field: it is written into
// the pool as a *stratification* key (doc/304 D-09 reports the prior mix of the draw), and
// the worksheet is the sampling record. So the same file was both the evidence that the
// class mix is what the plan says and the reading material handed to a person whose whole
// job is to form an independent judgement.
//
// Erasing the field from the worksheet is not the fix: the mix is how `sample` proves an
// equal-probability draw and how E1 later compares against the production layer, and a
// worksheet that cannot report it makes the sampling receipt unfalsifiable. The fix is two
// files with one direction between them:
//
//	worksheet      the sampling record — full rows, prior status, the drawn ids
//	blind packet   the same rows with no `prior_*` and no `gold_*` key, and nothing else
//	               removed, so an annotator can read claim text and evidence and cannot
//	               read an answer
//
// The rule is a *prefix*, not a list of fields, because a list ages: `Item` and
// `worksheetRow` both grew fields during v0.7, and the field that has to stay out is
// exactly the one added next (`applyGrep`-shaped allowlists fail open). Both prefixes are
// named in the receipt, so a packet that dropped nothing is visible as such rather than
// looking like a packet that was checked.
const (
	blindPrefixPrior = "prior_"
	blindPrefixGold  = "gold_"
)

// blindNote travels with the receipt: the packet's contract in one sentence, so a reader
// who finds the file a year from now does not have to reconstruct why keys are missing.
const blindNote = "rows carry no prior_* and no gold_* key: doc/301 §9.1 rule 3 says the " +
	"model's prior opinion is treated as absent while annotating, and the packet is where " +
	"that has to be true rather than remembered. Nothing else is removed — the packet is " +
	"the worksheet rows minus those keys, and the ids are unchanged, so labels join back. " +
	"`gold_scan_scope` goes with them: it is a sentence about the row's sampling frame, not " +
	"the row, and the frame is still visible as `candidate_pool` (a `cited_only` row carries " +
	"none, a `cited+candidate:K` row carries K), so the annotator reads the data instead of " +
	"a label about the data."

// blindBlock is the receipt's account of a packet. `Dropped` is the set of prefixes that
// were applied (both, always) and `Removed` counts the keys actually deleted: a run over a
// pool with no prior status must not look like a run that stripped one.
type blindBlock struct {
	Out           string   `json:"out"`
	SHA256        string   `json:"sha256"`
	Rows          int      `json:"rows"`
	ItemIDsSHA256 string   `json:"item_ids_sha256"`
	Dropped       []string `json:"dropped_prefixes"`
	RemovedKeys   []string `json:"removed_keys"`
	Removed       int      `json:"removed_per_row"`
	Note          string   `json:"note"`
}

// blindLine strips the two prefixes from one already-marshalled row. It works on the JSON
// object rather than on the structs on purpose: the item side and the candidate side have
// different structs (`Item`, `worksheetRow`), and two strips written against two structs is
// two places for the rule to be forgotten in.
func blindLine(v any) (line string, removed []string, err error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return "", nil, fmt.Errorf("row is not a JSON object: %w", err)
	}
	// A row that already carries an answer is not reading material. `sample` only ever
	// draws from the pool, where `gold_verdict` is empty by construction (doc/304 E100),
	// so this can only fire if the packet is later built from a labelled file — which is
	// precisely the mistake worth refusing instead of quietly stripping.
	if gv, ok := row["gold_verdict"]; ok && strings.TrimSpace(string(gv)) != `""` {
		return "", nil, fmt.Errorf("row already carries a gold_verdict: a packet made from a " +
			"labelled file would hand the answer to the annotator")
	}
	for k := range row {
		if strings.HasPrefix(k, blindPrefixPrior) || strings.HasPrefix(k, blindPrefixGold) {
			delete(row, k)
			removed = append(removed, k)
		}
	}
	// `item_id` (or, on the candidate side, `item_id` + `candidate_index`) is how a label
	// finds its row again. A packet missing it would be readable and unusable.
	if _, ok := row["item_id"]; !ok {
		return "", nil, fmt.Errorf("row has no item_id: the packet must join back to the labels file")
	}
	sort.Strings(removed)
	out, err := json.Marshal(row)
	if err != nil {
		return "", nil, err
	}
	return string(out), removed, nil
}

// writeBlindFile writes the packet and returns its receipt block. The file is derived, so
// it is rewritten rather than refused: the thing that must never be replaced in place is
// the worksheet (a half-annotated source of truth), not a packet that can be re-derived
// from it in one command.
func writeBlindFile(path string, rows []any) (blindBlock, error) {
	lines := make([]string, 0, len(rows))
	ids := make([]string, 0, len(rows))
	removedTotal := 0
	removedNames := map[string]struct{}{}
	for i, row := range rows {
		line, removed, err := blindLine(row)
		if err != nil {
			return blindBlock{}, fmt.Errorf("row %d: %w", i+1, err)
		}
		removedTotal += len(removed)
		for _, k := range removed {
			removedNames[k] = struct{}{}
		}
		lines = append(lines, line)
		if id, ok := rowItemID(row); ok {
			ids = append(ids, id)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	idSum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if err := writeJSONL(path, lines); err != nil {
		return blindBlock{}, err
	}
	// Names, sorted, because "6 keys per row" is a number an operator cannot check and
	// "gold_scan_scope, gold_verdict, …" is one they can.
	names := make([]string, 0, len(removedNames))
	for k := range removedNames {
		names = append(names, k)
	}
	sort.Strings(names)
	return blindBlock{
		Out: filepath.Base(path), SHA256: hex.EncodeToString(sum[:]), Rows: len(lines),
		ItemIDsSHA256: hex.EncodeToString(idSum[:]),
		Dropped:       []string{blindPrefixPrior, blindPrefixGold},
		RemovedKeys:   names,
		Removed:       perRow(removedTotal, len(lines)), Note: blindNote,
	}, nil
}

// rowItemID reads the id out of either row shape without caring which one it is: the two
// structs spell the field the same way in JSON, and that is the only property the packet
// depends on.
func rowItemID(v any) (string, bool) {
	switch r := v.(type) {
	case Item:
		return r.ItemID, r.ItemID != ""
	case worksheetRow:
		return r.ItemID, r.ItemID != ""
	default:
		return "", false
	}
}

// blindTargetOK refuses the one path combination that would destroy evidence: writing the
// packet over the worksheet. The worksheet is the sampling record (prior mix, drawn ids,
// the receipt's `out`), and a packet at that path leaves the receipt pointing at a file
// that no longer contains what the receipt describes.
func blindTargetOK(worksheet, packet string) error {
	if strings.TrimSpace(packet) == "" {
		return nil
	}
	if filepath.Clean(worksheet) == filepath.Clean(packet) {
		return fmt.Errorf("--blind-out must differ from --out: the worksheet is the sampling " +
			"record (prior mix, drawn ids) and the packet is the reading material; writing " +
			"one over the other loses the half the other one needs")
	}
	if ws, err := os.Stat(worksheet); err == nil {
		if pk, err := os.Stat(packet); err == nil && os.SameFile(ws, pk) {
			return fmt.Errorf("--blind-out and --out are the same file (two names, one inode)")
		}
	}
	return nil
}

// itemRows and candidateRowValues adapt the two row shapes to one list, so the two call
// sites read the same way. Written as functions rather than `[]any{...}` literals at each
// site because a call site that forgot to convert would still compile — `[]Item` is not
// assignable to `[]any` — while a call site that forgot to *call* the packet writer would
// not, which is the property the glue is here to keep.
func itemRows(items []Item) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	return out
}

func candidateRowValues(rows []worksheetRow) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	return out
}

// writeBlindIfAsked is the call sites' whole branch: no flag, no packet, and no receipt
// block (an empty `blind` object would read as "a packet was written and it was empty").
// The bool is "keep going"; when it is false the caller returns the code unchanged, so a
// refusal here is reported with the exit code of a usage error rather than invented.
func writeBlindIfAsked(receipt *sampleReceipt, path string, rows []any, stderr *os.File) (int, bool) {
	if strings.TrimSpace(path) == "" {
		return exitOK, true
	}
	block, err := writeBlindFile(path, rows)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage, false
	}
	// The receipt is printed by the caller after this returns, so this takes a pointer:
	// with a value receiver the packet would be written and the receipt would not know.
	receipt.Blind = &block
	return exitOK, true
}

// packetSummary is what `labels --stub --packet` reports: the file the annotator reads, its
// hash, and how many rows it holds. Reporting the hash is what lets a label file say which
// packet it was filled in against — the same reason the stub already reports the worksheet
// hash, one file further down the chain.
type packetSummary struct {
	Path   string `json:"packet"`
	SHA256 string `json:"packet_sha256"`
	Rows   int    `json:"packet_rows"`
}

// checkPacket is the reader-side half of the rule. `sample --blind-out` makes the packet
// blind; this refuses to build a skeleton from a file that is not. The check is not
// redundant with the writer: the packet is a file on a disk, and the file handed to an
// annotator may be any file. One of the two has to look at what is actually there.
func checkPacket(path string, items []Item) (packetSummary, error) {
	sum := packetSummary{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return sum, err
	}
	h := sha256.Sum256(raw)
	sum.SHA256 = hex.EncodeToString(h[:])

	want := make(map[string]int, len(items))
	for _, it := range items {
		want[it.ItemID]++
	}
	got := map[string]int{}
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum.Rows++
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return sum, fmt.Errorf("packet line %d is not a JSON object: %w", i+1, err)
		}
		for k := range row {
			if strings.HasPrefix(k, blindPrefixPrior) || strings.HasPrefix(k, blindPrefixGold) {
				return sum, fmt.Errorf("packet line %d carries %q: the file an annotator reads "+
					"still holds the model's opinion, so the blindness is a habit again "+
					"(doc/301 §9.1 rule 3)", i+1, k)
			}
		}
		rawID, ok := row["item_id"]
		if !ok {
			return sum, fmt.Errorf("packet line %d has no item_id: a label row could not be "+
				"joined back to this packet", i+1)
		}
		var id string
		if err := json.Unmarshal(rawID, &id); err != nil {
			return sum, fmt.Errorf("packet line %d: item_id is not a string: %w", i+1, err)
		}
		got[id]++
	}

	var missing, extra []string
	for id, c := range want {
		if got[id] < c {
			missing = append(missing, id)
		}
	}
	for id, c := range got {
		if want[id] < c {
			extra = append(extra, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		// Named, and counted on both sides: "the packet does not match" is the sentence a
		// person cannot act on, and a count without names cannot be checked by hand.
		return sum, fmt.Errorf("packet %s and worksheet are not the same rows: %d id(s) only in "+
			"the worksheet %v, %d id(s) only in the packet %v", path, len(missing), cap10(missing),
			len(extra), cap10(extra))
	}
	return sum, nil
}

// perRow keeps the receipt's "keys removed per row" honest on an empty packet: a draw that
// selected nothing must report 0, not a division result nobody can read.
func perRow(total, rows int) int {
	if rows == 0 {
		return 0
	}
	return total / rows
}

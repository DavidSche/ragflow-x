package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The worksheet has two sides, and until this file existed the whole label chain only
// knew one of them.
//
// doc/301 §9.1.3 draws the distinction: the items side asks one question per claim, so a
// row's identity is `item_id`; the candidates side asks one question per frozen candidate
// row, so a row's identity is `(item_id, candidate_index)` and `item_id` legitimately
// repeats (one claim with two candidates is two rows to judge). That sentence was in the
// plan and in the manual v1.2 ("两人标注时按 `(item_id, candidate_index)` 对齐，不要按
// `item_id`"), and every command in the loop still keyed on `item_id` alone. The visible
// effect was absurd in the useful way: `labels --stub` refused its own 36-row worksheet as
// "repeats item_id", i.e. the tool called the sampling unit the plan requires a mistake
// (doc/304 E118 / §7 item 40).
//
// The fix is not a second copy of the loop for the candidate side. It is one row view that
// knows which side it came from, and one key function every map and every message uses:
//
//	items side      key = item_id
//	candidates side key = item_id "#" candidate_index
//
// The key is display text as well as map key, because every refusal in this chain names
// the row — "line 47: 4e4d02a2#1 is not in the worksheet" is actionable and
// "line 47: 4e4d02a2" is ambiguous on the candidate side.
//
// What this file deliberately does *not* do is make `assemble` accept the candidate side.
// The gold file is one row per claim (doc/301 §9.1: `gold_verdict` + `gold_candidate_chunk_id`),
// so a per-candidate verdict has nowhere to go; assembly keeps refusing the candidate side
// and now says why (see `labelRowsFromCandidateSide` in assemble.go).

const (
	// sideItems is one row per claim: `goldtool sample`'s default, and what `assemble`
	// writes.
	sideItems = "items"
	// sideCandidates is one row per frozen candidate (`sample --side candidates`,
	// doc/301 §9.1.3 一).
	sideCandidates = "candidates"
)

// wsRow is one worksheet row as the label chain sees it. Exactly one of `Item`/`Cand` is
// set; the pair `(Key, side)` is what every map in labels/kappa/adjudicate stores.
type wsRow struct {
	Key            string
	ItemID         string
	CandidateIndex *int
	Item           *Item
	Cand           *worksheetRow
}

// worksheet is a whole read side, with the path kept so every message can send the reader
// to the file rather than to "the worksheet".
type worksheet struct {
	Path string
	Side string
	Rows []wsRow
}

func (w *worksheet) candidateSide() bool { return w.Side == sideCandidates }

// rowKey is the one place the two sides' identities are spelled.
func rowKey(itemID string, idx *int) string {
	if idx == nil {
		return itemID
	}
	return itemID + "#" + strconv.Itoa(*idx)
}

func (w *worksheet) keys() []string {
	out := make([]string, 0, len(w.Rows))
	for _, r := range w.Rows {
		out = append(out, r.Key)
	}
	return out
}

// byKey returns the rows in worksheet order plus the index. Rows keep their order because
// every "missing"/"unfilled" list is printed in it: a list in map order would make the same
// file produce a different report on every run.
func (w *worksheet) byKey() (map[string]wsRow, []string) {
	by := make(map[string]wsRow, len(w.Rows))
	order := make([]string, 0, len(w.Rows))
	for _, r := range w.Rows {
		if _, dup := by[r.Key]; !dup {
			order = append(order, r.Key)
		}
		by[r.Key] = r
	}
	return by, order
}

// duplicateKeys is a worksheet fault, not a label fault: the same row written twice gives
// the annotator two places for one judgement and κ one observation for two rows. Matching
// on (item_id, candidate_index) is what makes this checkable *on the candidate side* — the
// item id repeating there is the plan, not a duplicate.
func (w *worksheet) duplicateKeys() []string {
	seen := map[string]int{}
	var dups []string
	for _, r := range w.Rows {
		seen[r.Key]++
		if seen[r.Key] == 2 {
			dups = append(dups, r.Key)
		}
	}
	return dups
}

func (w *worksheet) distinctItemIDs() int {
	seen := map[string]bool{}
	for _, r := range w.Rows {
		seen[r.ItemID] = true
	}
	return len(seen)
}

func (r wsRow) evidence() []Evidence {
	if r.Item != nil {
		return r.Item.Evidence
	}
	if r.Cand != nil {
		return r.Cand.Evidence
	}
	return nil
}

// candidatePool is what a `candidate_index`/`candidate_chunk_id` may point at. On the
// candidate side the row *is* the candidate, so the pool has one entry: the row's own
// frozen chunk. That is what lets the same reference rules be stated for both sides
// without a second implementation.
func (r wsRow) candidatePool() []Candidate {
	if r.Item != nil {
		return r.Item.CandidatePool
	}
	if r.Cand != nil {
		return []Candidate{{
			ChunkID: r.Cand.ChunkID, ContentHash: r.Cand.ContentHash,
			ContentChars: r.Cand.ContentChars, PoolRank: r.Cand.PoolRank,
			Truncated: r.Cand.Truncated,
		}}
	}
	return nil
}

// labelled is `countLabelledRows` for one row, on both shapes: a worksheet is a question and a
// gold file is an answer, whichever side the answer would be written on.
func (r wsRow) labelled() bool {
	if r.Item != nil {
		return trimSpace(r.Item.GoldVerdict) != ""
	}
	return false
}

// keyOfLabel says which worksheet row a label is about. On the items side that is
// `item_id` — `candidate_index` there is a *reference* ("this candidate drove my verdict"),
// validated against the item's pool, not an identity. On the candidate side it is the
// second half of the identity, so a label without it cannot be placed, and that is a
// problem to report rather than a default to guess (guessing 0 would silently re-key every
// row of a file somebody sorted).
func (r wsRow) keyOfLabel(l Label) (string, error) {
	if r.Cand == nil {
		return l.ItemID, nil
	}
	return labelKeyForSide(l, sideCandidates)
}

// labelKeyForSide is the same rule without a row in hand: κ and coverage key a whole file,
// and they need the identity before they know which row it will land on.
func labelKeyForSide(l Label, side string) (string, error) {
	if side != sideCandidates {
		return l.ItemID, nil
	}
	if l.CandidateIndex == nil {
		return "", fmt.Errorf("this is a candidates-side label file: a label must carry " +
			"candidate_index, because one claim has one row per frozen candidate (doc/301 §9.1.3)")
	}
	return rowKey(l.ItemID, l.CandidateIndex), nil
}

// nounForSide is the one place the unit is put into a sentence. It exists because every
// receipt says "3 item(s)" and on the candidates side that noun is wrong in the direction
// that matters: a reader who sees "item" adds rows up as claims.
func nounForSide(side string) string {
	if side == sideCandidates {
		return "candidate row"
	}
	return "item"
}

// readWorksheet reads either side and says which one it read. The side is decided per line
// from the row itself (`unit: candidate_row`, or a `candidate_index` key) rather than from a
// flag: the file is the authority, and a flag would let an operator point the candidate
// chain at an items worksheet and get 0 pairs with no message.
func readWorksheet(path string) (*worksheet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ws := &worksheet{Path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		side, err := worksheetLineSide(text)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if ws.Side == "" {
			ws.Side = side
		} else if ws.Side != side {
			// A file with both shapes is not one worksheet with a mixed row; it is two
			// files concatenated, and every count after this point would be about neither.
			return nil, fmt.Errorf("%s:%d: this worksheet holds %s rows after %s rows: one file, "+
				"one side (the two sides have different units and different keys)", path, line,
				side, ws.Side)
		}
		if side == sideCandidates {
			var row worksheetRow
			if err := json.Unmarshal([]byte(text), &row); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, line, err)
			}
			if row.ItemID == "" {
				return nil, fmt.Errorf("%s:%d: candidate row has no item_id: a label could not be "+
					"joined back to this row", path, line)
			}
			idx := row.CandidateIndex
			ws.Rows = append(ws.Rows, wsRow{
				Key: rowKey(row.ItemID, &idx), ItemID: row.ItemID,
				CandidateIndex: &idx, Cand: &row,
			})
			continue
		}
		it, err := decodeItem(text)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		ws.Rows = append(ws.Rows, wsRow{Key: it.ItemID, ItemID: it.ItemID, Item: &it})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if ws.Side == "" {
		ws.Side = sideItems
	}
	return ws, nil
}

// worksheetLineSide is the whole detection rule, in one function so "which side is this
// file" cannot drift between the reader and the tests.
func worksheetLineSide(text string) (string, error) {
	var probe struct {
		Unit           string `json:"unit"`
		CandidateIndex *int   `json:"candidate_index"`
	}
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return "", fmt.Errorf("not a JSON object: %w", err)
	}
	// `item_id` + `candidate_index` is the candidate side's identity (doc/301 §9.1.3 一).
	// An items-side row has no such key: `candidate_pool` is a list of candidates and
	// `gold_candidate_chunk_id` is a reference, and neither is spelled `candidate_index`.
	if probe.Unit == "candidate_row" || probe.CandidateIndex != nil {
		if probe.CandidateIndex == nil {
			return "", fmt.Errorf("row says unit=%q but has no candidate_index: the candidate "+
				"side's key is (item_id, candidate_index), and a row missing the second half "+
				"cannot be told from the first candidate twice", probe.Unit)
		}
		return sideCandidates, nil
	}
	return sideItems, nil
}

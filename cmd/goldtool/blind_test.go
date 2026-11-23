package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The packet a person reads must not contain the answer or the model's old opinion of it
// (doc/301 §9.1 rule 3). These tests hold the three properties that make that a fact about
// the file rather than a sentence in a manual: nothing with a `prior_`/`gold_` prefix
// survives, nothing else is removed, and the ids still line up with the worksheet.

func readLinesOf(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var rows []map[string]json.RawMessage
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %d of %s is not a JSON object: %v", i+1, path, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func decodeString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("not a string: %v", err)
	}
	return s
}

func TestThePacketCarriesNoPriorAndNoGold(t *testing.T) {
	pool := poolOf(t, 40, map[string]int{"unsupported": 12})
	dir := t.TempDir()
	worksheet := filepath.Join(dir, "worksheet.jsonl")
	packet := filepath.Join(dir, "packet.jsonl")
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", worksheet, "--n", "10",
		"--seed", "20261121", "--blind-out", packet)
	if code != exitOK {
		t.Fatalf("sample exited %d, want 0; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, `"blind"`) {
		t.Fatalf("receipt has no blind block:\n%s", stdout)
	}

	// The worksheet really does carry the field: without this the test would pass on a
	// pool that never had one, which is the shape of a check that proves nothing.
	var sheetHasPrior bool
	for _, row := range readLinesOf(t, worksheet) {
		if _, ok := row["prior_support_status"]; ok {
			sheetHasPrior = true
		}
	}
	if !sheetHasPrior {
		t.Fatal("the worksheet has no prior_support_status: this fixture checks nothing")
	}

	for i, row := range readLinesOf(t, packet) {
		for key := range row {
			if strings.HasPrefix(key, blindPrefixPrior) || strings.HasPrefix(key, blindPrefixGold) {
				t.Fatalf("row %d of the packet carries %q: a reader of this file can see the "+
					"answer or the model's prior opinion", i+1, key)
			}
		}
	}
}

// Stripping is not the whole job: a packet that also dropped the claim text would contain
// no answer either. The packet has to be the worksheet minus those keys and nothing else.
func TestThePacketRemovesOnlyThoseKeys(t *testing.T) {
	pool := poolOf(t, 40, map[string]int{"unsupported": 12})
	dir := t.TempDir()
	worksheet := filepath.Join(dir, "worksheet.jsonl")
	packet := filepath.Join(dir, "packet.jsonl")
	if code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", worksheet, "--n", "10",
		"--seed", "20261121", "--blind-out", packet); code != exitOK {
		t.Fatalf("sample exited %d; stderr:\n%s", code, stderr)
	}

	sheet, blind := readLinesOf(t, worksheet), readLinesOf(t, packet)
	if len(sheet) != len(blind) {
		t.Fatalf("worksheet has %d rows, packet has %d", len(sheet), len(blind))
	}
	for i := range sheet {
		for key, want := range sheet[i] {
			if strings.HasPrefix(key, blindPrefixPrior) || strings.HasPrefix(key, blindPrefixGold) {
				if _, ok := blind[i][key]; ok {
					t.Fatalf("row %d: %q should have been removed", i+1, key)
				}
				continue
			}
			got, ok := blind[i][key]
			if !ok {
				t.Fatalf("row %d: %q was removed but is neither a prior nor an answer", i+1, key)
			}
			if string(got) != string(want) {
				t.Fatalf("row %d: %q changed: %s -> %s", i+1, key, want, got)
			}
		}
	}
}

// The ids are the join key back to `labels-<name>.jsonl`. A packet whose order or ids
// drifted would produce labels that assemble against the wrong rows — silently, because
// `labels --check` compares ids against the worksheet, not against the packet.
func TestThePacketKeepsTheWorksheetIdsInOrder(t *testing.T) {
	pool := poolOf(t, 40, map[string]int{"unsupported": 12})
	dir := t.TempDir()
	worksheet := filepath.Join(dir, "worksheet.jsonl")
	packet := filepath.Join(dir, "packet.jsonl")
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", worksheet, "--n", "10",
		"--seed", "20261121", "--blind-out", packet)
	if code != exitOK {
		t.Fatalf("sample exited %d; stderr:\n%s", code, stderr)
	}

	var sheetIDs, blindIDs []string
	for _, row := range readLinesOf(t, worksheet) {
		sheetIDs = append(sheetIDs, decodeString(t, row["item_id"]))
	}
	for _, row := range readLinesOf(t, packet) {
		blindIDs = append(blindIDs, decodeString(t, row["item_id"]))
	}
	if strings.Join(sheetIDs, ",") != strings.Join(blindIDs, ",") {
		t.Fatalf("ids differ:\n worksheet %v\n packet    %v", sheetIDs, blindIDs)
	}

	var receipt struct {
		Blind *blindBlock `json:"blind"`
	}
	if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, stdout)
	}
	if receipt.Blind == nil {
		t.Fatal("receipt has no blind block: the packet would be an unaccounted file")
	}
	if receipt.Blind.Rows != len(sheetIDs) {
		t.Fatalf("receipt says %d rows, packet has %d", receipt.Blind.Rows, len(sheetIDs))
	}
	if receipt.Blind.Removed == 0 {
		t.Fatal("receipt says nothing was removed: the packet would be a copy of the worksheet")
	}
	if len(receipt.Blind.Dropped) != 2 {
		t.Fatalf("receipt names %v, want both prefixes (prior_, gold_)", receipt.Blind.Dropped)
	}
	if receipt.Blind.SHA256 == "" || receipt.Blind.ItemIDsSHA256 == "" {
		t.Fatal("receipt has no packet hash: a packet nobody can name is a file, not evidence")
	}
}

// Two files, one direction. Writing the packet over the worksheet would leave the receipt's
// `out` pointing at a file that no longer holds the prior mix the receipt reports.
func TestThePacketMayNotReplaceTheWorksheet(t *testing.T) {
	pool := poolOf(t, 20, nil)
	dir := t.TempDir()
	same := filepath.Join(dir, "both.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", same, "--n", "5", "--seed", "7",
		"--blind-out", filepath.Join(dir, ".", "both.jsonl"))
	if code != exitUsage {
		t.Fatalf("sample exited %d with --blind-out pointing at --out, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--blind-out must differ from --out") {
		t.Fatalf("refusal does not say what is wrong:\n%s", stderr)
	}
	if _, err := os.Stat(same); err == nil {
		t.Fatal("a worksheet was written even though the run was refused")
	}
}

// A labelled row is not reading material. `sample` only draws from a pool, where the field
// is empty by construction, so this can only fire when a packet is built from a file that
// already went through `assemble` — which is exactly the mistake to refuse.
func TestALabelledRowIsRefusedNotStripped(t *testing.T) {
	item := sampleItem(1, "")
	item.GoldVerdict = "supported"
	if _, _, err := blindLine(item); err == nil {
		t.Fatal("a row carrying gold_verdict was accepted: stripping it would hide that an " +
			"answer had reached the reading material")
	}
	item.GoldVerdict = ""
	line, removed, err := blindLine(item)
	if err != nil {
		t.Fatalf("an unlabelled row was refused: %v", err)
	}
	if !strings.Contains(line, `"claim_text"`) {
		t.Fatal("the packet lost the claim text")
	}
	for _, key := range removed {
		if !strings.HasPrefix(key, blindPrefixPrior) && !strings.HasPrefix(key, blindPrefixGold) {
			t.Fatalf("removed %q, which is neither a prior nor an answer", key)
		}
	}
}

// The candidate side is a different struct with the same rule. Without this, a change that
// stripped only `Item` would leave E2's packet carrying `prior_support_status`.
func TestTheCandidateSideStripsTheSameKeys(t *testing.T) {
	// A local pool rather than `poolWithCandidates`: the shared fixture leaves the prior
	// status empty (it is there for the group split, not for this rule), and a packet test
	// over a pool with no prior status would pass without stripping anything.
	items := []Item{
		candItem("cand-a", 0, "cited+candidate:1", []Candidate{
			{ChunkID: "chunk-a", ContentHash: "h1", Content: "第一条候选原文。", CitedElsewhere: true, PoolRank: 1},
		}),
		candItem("cand-b", 1, "cited+candidate:1", []Candidate{
			{ChunkID: "chunk-b", ContentHash: "h2", Content: "第二条候选原文。", PoolRank: 2},
		}),
		candItem("cand-c", 2, "cited_only", nil),
	}
	for i := range items {
		items[i].PriorSupportStatus = "supported"
	}
	pool := writePoolFixture(t, items)
	dir := t.TempDir()
	worksheet := filepath.Join(dir, "cand.jsonl")
	packet := filepath.Join(dir, "cand-packet.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", worksheet, "--side", "candidates",
		"--n", "2", "--seed", "20261121", "--blind-out", packet)
	if code != exitOK {
		t.Fatalf("candidate sample exited %d, want 0; stderr:\n%s", code, stderr)
	}
	sheet := readLinesOf(t, worksheet)
	if _, ok := sheet[0]["prior_support_status"]; !ok {
		t.Fatal("the candidate worksheet has no prior_support_status: this fixture checks nothing")
	}
	packetRows := readLinesOf(t, packet)
	if len(packetRows) != len(sheet) {
		t.Fatalf("candidate packet has %d rows, worksheet %d", len(packetRows), len(sheet))
	}
	for i, row := range packetRows {
		for key := range row {
			if strings.HasPrefix(key, blindPrefixPrior) || strings.HasPrefix(key, blindPrefixGold) {
				t.Fatalf("candidate row %d carries %q", i+1, key)
			}
		}
		if _, ok := row["candidate_index"]; !ok {
			t.Fatalf("candidate row %d lost candidate_index: the row can no longer be identified", i+1)
		}
	}
}

// A run without `--blind-out` must not grow a `blind` block: an empty block would read as
// "a packet was written and it was empty", which is the opposite of "no packet was asked
// for" (the same distinction `sample_id` and `material.not_measured` keep elsewhere).
func TestNoBlindFlagMeansNoBlindBlock(t *testing.T) {
	pool := poolOf(t, 20, nil)
	dir := t.TempDir()
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", filepath.Join(dir, "w.jsonl"),
		"--n", "5", "--seed", "20261121")
	if code != exitOK {
		t.Fatalf("sample exited %d; stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, `"blind"`) {
		t.Fatalf("receipt mentions a packet that was not asked for:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "packet.jsonl")); err == nil {
		t.Fatal(fmt.Sprintf("a packet appeared at %s", filepath.Join(dir, "packet.jsonl")))
	}
}

// ── the reader side: `labels --stub --packet` ──────────────────────────────

// priorWorksheet writes a worksheet that *does* carry the model's prior opinion, which is
// what every real `sample` export does (it is the stratification key). Without a worksheet
// like this, a packet test would pass on a file that had nothing to leak.
func priorWorksheet(t *testing.T, n int) (string, []Item) {
	t.Helper()
	items := make([]Item, 0, n)
	for i := 1; i <= n; i++ {
		it := sampleItem(i, "")
		it.GoldVerdict, it.GoldQuoteStatus, it.GoldQuote = "", "", ""
		it.GoldEvidenceIDs, it.Annotators = nil, nil
		it.PriorSupportStatus = "supported"
		items = append(items, it)
	}
	path := filepath.Join(t.TempDir(), "worksheet.jsonl")
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	return path, items
}

// The chain the manual asks for is `sample --blind-out` then `labels --stub --packet`: the
// skeleton is built against the file the person reads. If the stub accepted a packet that
// still carried the prior, the two files would differ in exactly the field that matters and
// nothing downstream would notice.
func TestStubFromAPacketChecksThePacketIsBlind(t *testing.T) {
	worksheet, items := priorWorksheet(t, 4)
	packet := filepath.Join(t.TempDir(), "packet.jsonl")
	if _, err := writeBlindFile(packet, itemRows(items)); err != nil {
		t.Fatalf("write packet: %v", err)
	}
	out := filepath.Join(t.TempDir(), "labels.jsonl")
	code, stdout, stderr := labelsRun(t, "--stub", "--worksheet", worksheet, "--packet", packet,
		"--annotator", "a1", "--out", out)
	if code != exitOK {
		t.Fatalf("stub exited %d, want 0; stderr:\n%s", code, stderr)
	}
	var receipt struct {
		Packet       string `json:"packet"`
		PacketSHA256 string `json:"packet_sha256"`
		PacketRows   int    `json:"packet_rows"`
	}
	if err := json.Unmarshal(lastJSONLine([]byte(stdout)), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, stdout)
	}
	if receipt.PacketRows != len(items) {
		t.Fatalf("receipt says %d packet rows, want %d", receipt.PacketRows, len(items))
	}
	if receipt.PacketSHA256 == "" {
		t.Fatal("receipt does not name the packet it checked: a skeleton that cannot say which " +
			"file it was built against is not a checked skeleton")
	}
	// The skeleton still carries only the two identity fields. That was true before the
	// packet existed and must stay true: the packet changed what is *read*, not what is written.
	for i, row := range readLinesOf(t, out) {
		for key := range row {
			if key != "item_id" && key != "annotator" {
				t.Fatalf("skeleton row %d carries %q", i+1, key)
			}
		}
	}
}

// A packet on disk may be any file: the writer's promise is not a property of this one.
func TestStubRefusesAPacketThatLeakedThePrior(t *testing.T) {
	worksheet, items := priorWorksheet(t, 3)
	packet := filepath.Join(t.TempDir(), "packet.jsonl")
	// Written by hand rather than by `writeBlindFile`: the point is a packet that looks like
	// one and is not, which is what a hand-edited or re-exported file gives you.
	var lines []string
	for _, it := range items {
		lines = append(lines, `{"item_id":"`+it.ItemID+`","claim_text":"`+it.ClaimText+`","prior_support_status":"unsupported"}`)
	}
	if err := writeJSONL(packet, lines); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "labels.jsonl")
	code, _, stderr := labelsRun(t, "--stub", "--worksheet", worksheet, "--packet", packet,
		"--annotator", "a1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("stub exited %d, want %d (an invariant break, not a usage error)", code, exitInvariant)
	}
	if !strings.Contains(stderr, "prior_support_status") || !strings.Contains(stderr, "line 1") {
		t.Fatalf("refusal does not name the key and the line:\n%s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a skeleton was written from a packet that was not blind")
	}
}

// Same rows, one direction. A packet missing a row would give the annotator no text for an
// item the skeleton still asks about — the row would be labelled from memory.
func TestStubRefusesAPacketThatIsNotTheSameRows(t *testing.T) {
	worksheet, items := priorWorksheet(t, 3)
	packet := filepath.Join(t.TempDir(), "packet.jsonl")
	if _, err := writeBlindFile(packet, itemRows(items[:2])); err != nil {
		t.Fatalf("write packet: %v", err)
	}
	out := filepath.Join(t.TempDir(), "labels.jsonl")
	code, _, stderr := labelsRun(t, "--stub", "--worksheet", worksheet, "--packet", packet,
		"--annotator", "a1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("stub exited %d, want %d", code, exitInvariant)
	}
	if !strings.Contains(stderr, "only in the worksheet") || !strings.Contains(stderr, items[2].ItemID) {
		t.Fatalf("refusal must say which id is missing, and where it is missing from:\n%s", stderr)
	}

	// And the other direction, because a packet with an extra row means the annotator is
	// reading an item the labels file will never account for.
	var lines []string
	for _, it := range items {
		lines = append(lines, `{"item_id":"`+it.ItemID+`","claim_text":"x"}`)
	}
	lines = append(lines, `{"item_id":"not-in-the-worksheet","claim_text":"x"}`)
	if err := writeJSONL(packet, lines); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = labelsRun(t, "--stub", "--worksheet", worksheet, "--packet", packet,
		"--annotator", "a1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("stub exited %d with an extra packet row, want %d", code, exitInvariant)
	}
	if !strings.Contains(stderr, "only in the packet") {
		t.Fatalf("refusal does not say the packet has a row the worksheet does not:\n%s", stderr)
	}
}

// `gold_scan_scope` is a sentence about the row's sampling frame, and the prefix rule takes
// it with the rest. That is acceptable only because the frame itself stays visible as
// `candidate_pool`: a `cited_only` row carries none, a `cited+candidate:K` row carries K.
// If a future change ever drops the candidates too, the annotator would lose the ability to
// tell "I found no conflict" from "I was never shown the candidates" — which is E65's hole,
// reopened from the other side.
func TestThePacketKeepsTheFrameWhenItDropsTheScopeLabel(t *testing.T) {
	worksheet, items := priorWorksheet(t, 2)
	items[1].GoldScanScope = "cited+candidate:2"
	items[1].CandidatePool = []Candidate{
		{ChunkID: "c1", ContentHash: "h1", Content: "候选一。", PoolRank: 1},
		{ChunkID: "c2", ContentHash: "h2", Content: "候选二。", PoolRank: 2},
	}
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(worksheet, lines); err != nil {
		t.Fatal(err)
	}
	packet := filepath.Join(t.TempDir(), "packet.jsonl")
	block, err := writeBlindFile(packet, itemRows(items))
	if err != nil {
		t.Fatalf("write packet: %v", err)
	}

	sheet, blind := readLinesOf(t, worksheet), readLinesOf(t, packet)
	if _, ok := sheet[1]["gold_scan_scope"]; !ok {
		t.Fatal("the worksheet has no gold_scan_scope: this fixture checks nothing")
	}
	if _, ok := blind[1]["gold_scan_scope"]; ok {
		t.Fatal("the packet kept gold_scan_scope")
	}
	var pooled []Candidate
	if err := json.Unmarshal(blind[1]["candidate_pool"], &pooled); err != nil {
		t.Fatalf("the packet's candidate_pool cannot be read: %v", err)
	}
	if len(pooled) != 2 {
		t.Fatalf("the packet carries %d candidate row(s), want 2: the scan width is no longer "+
			"visible anywhere", len(pooled))
	}
	named := strings.Join(block.RemovedKeys, ",")
	if !strings.Contains(named, "gold_scan_scope") || !strings.Contains(named, "prior_support_status") {
		t.Fatalf("receipt names %q; it must say which keys went, not only how many", named)
	}
}

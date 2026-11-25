package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `goldtool packet` is the second writer of the same file shape, so it is held to the same
// three properties as `sample --blind-out` plus one of its own: it must not lose fields that
// today's structs do not know, because the whole reason it exists is a worksheet drawn by an
// older version of this tool (doc/304 §7 item 37).

func runToolCmd(t *testing.T, sub string, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath, stderrPath := filepath.Join(dir, "stdout.json"), filepath.Join(dir, "stderr.txt")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{sub}, args...), stdout, stderr)
	o, _ := os.ReadFile(stdoutPath)
	e, _ := os.ReadFile(stderrPath)
	return code, string(o), string(e)
}

// oldWorksheet writes the shape v1.12 was drawn from: the prior opinion, the sampling-frame
// sentence, a field this build's structs have never heard of, and a claim body.
func oldWorksheet(t *testing.T, dir string) string {
	t.Helper()
	rows := []string{
		`{"item_id":"it-1","claim_text":"退款要 7 个工作日","prior_support_status":"unsupported",` +
			`"gold_scan_scope":"cited_only","gold_verdict":"","template_key":"refund-clock",` +
			`"future_field":"added after this draw"}`,
		`{"item_id":"it-2","claim_text":"工单 24 小时内响应","prior_support_status":"supported",` +
			`"gold_scan_scope":"cited+candidate:2","gold_verdict":"","template_key":"sla",` +
			`"future_field":"added after this draw"}`,
	}
	path := filepath.Join(dir, "worksheet-primary-v1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write worksheet: %v", err)
	}
	return path
}

func TestPacketIsDerivedFromAnOlderWorksheet(t *testing.T) {
	dir := t.TempDir()
	worksheet := oldWorksheet(t, dir)
	packet := filepath.Join(dir, "packet-primary-v1.jsonl")

	// The worksheet really carries the keys: without this the test would pass on a file that
	// never had them, which is the shape of a check that cannot fail.
	src := readLinesOf(t, worksheet)
	if _, ok := src[0]["prior_support_status"]; !ok {
		t.Fatal("the fixture has no prior_support_status; the strip would prove nothing")
	}
	before, err := os.ReadFile(worksheet)
	if err != nil {
		t.Fatalf("read worksheet: %v", err)
	}

	code, stdout, stderr := runToolCmd(t, "packet", "--worksheet", worksheet, "--out", packet)
	if code != exitOK {
		t.Fatalf("packet exited %d, want 0; stderr:\n%s", code, stderr)
	}

	rows := readLinesOf(t, packet)
	if len(rows) != 2 {
		t.Fatalf("packet has %d rows, want 2", len(rows))
	}
	for i, row := range rows {
		for k := range row {
			if strings.HasPrefix(k, blindPrefixPrior) || strings.HasPrefix(k, blindPrefixGold) {
				t.Errorf("packet row %d still carries %q", i+1, k)
			}
		}
		if got := decodeString(t, row["item_id"]); got != []string{"it-1", "it-2"}[i] {
			t.Errorf("packet row %d has item_id %q: the ids must not move", i+1, got)
		}
		if _, ok := row["claim_text"]; !ok {
			t.Errorf("packet row %d lost claim_text: the packet is reading material", i+1)
		}
		// This is the reason the command reads JSON objects instead of `worksheetRow`: a
		// struct round-trip would drop this key silently, and the annotator would be reading
		// a row that is not what was drawn.
		if got := decodeString(t, row["future_field"]); got != "added after this draw" {
			t.Errorf("packet row %d lost a field the structs do not know (%q): a derived "+
				"packet must not be filtered through today's structs", i+1, got)
		}
	}

	// The worksheet is untouched: it is the sampling record the receipt describes.
	after, err := os.ReadFile(worksheet)
	if err != nil {
		t.Fatalf("re-read worksheet: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the worksheet changed; this command derives a packet and must not edit the sample")
	}

	var receipt packetReceipt
	if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, stdout)
	}
	// Three, not two: `gold_verdict` is one of the removed keys even when it is empty —
	// the prefix is the rule, and a list of "fields that matter today" would let the next
	// answer field through (the reason blind.go strips by prefix).
	if receipt.Blind.Rows != 2 || receipt.Blind.Removed != 3 {
		t.Errorf("receipt says rows=%d removed_per_row=%d, want 2 and 3",
			receipt.Blind.Rows, receipt.Blind.Removed)
	}
	want := []string{"gold_scan_scope", "gold_verdict", "prior_support_status"}
	if strings.Join(receipt.Blind.RemovedKeys, ",") != strings.Join(want, ",") {
		t.Errorf("receipt names removed keys %v, want %v: a count alone cannot be checked by hand",
			receipt.Blind.RemovedKeys, want)
	}
	if receipt.Worksheet != "worksheet-primary-v1.jsonl" {
		t.Errorf("receipt names worksheet %q; the reader has to be able to find it", receipt.Worksheet)
	}
	if receipt.WorksheetSHA256 == "" || receipt.Blind.SHA256 == "" {
		t.Error("receipt carries no hash: a label file could not say which packet it was filled in against")
	}
	// The ids hash is how the receipt says "the same rows, in the same order" — the first
	// version of this command left it empty (it read rows as objects, and rowItemID only knew
	// the two structs), so the field was present, plausible and sha256(""). Pinned against the
	// value the caller can compute from the file it just wrote.
	joined := strings.Join([]string{"it-1", "it-2"}, string(rune(10)))
	h := sha256.Sum256([]byte(joined))
	if receipt.Blind.ItemIDsSHA256 != hex.EncodeToString(h[:]) {
		t.Errorf("receipt ids hash = %q, want sha256 of the two ids: `sample --blind-out` and "+
			"`packet` have to agree on what the packet's rows are", receipt.Blind.ItemIDsSHA256)
	}
}

// The reader side already exists (`labels --stub --packet`). A packet the reader rejects is
// not a packet, so the two are checked against each other rather than each on its own.
func TestThePacketCommandFeedsTheReaderThatChecksIt(t *testing.T) {
	dir := t.TempDir()
	worksheet := oldWorksheet(t, dir)
	packet := filepath.Join(dir, "packet.jsonl")
	if code, _, stderr := runToolCmd(t, "packet", "--worksheet", worksheet, "--out", packet); code != exitOK {
		t.Fatalf("packet exited %d; stderr:\n%s", code, stderr)
	}
	labels := filepath.Join(dir, "labels-a.jsonl")
	code, stdout, stderr := runToolCmd(t, "labels", "--stub", "--worksheet", worksheet,
		"--annotator", "a", "--out", labels, "--packet", packet)
	if code != exitOK {
		t.Fatalf("labels --stub --packet exited %d; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, `"packet_rows": 2`) {
		t.Errorf("label file does not report the packet it was filled in against:\n%s", stdout)
	}
}

func TestThePacketCommandRefusesToOverwriteItsWorksheet(t *testing.T) {
	dir := t.TempDir()
	worksheet := oldWorksheet(t, dir)
	code, _, stderr := runToolCmd(t, "packet", "--worksheet", worksheet, "--out", worksheet)
	if code != exitUsage {
		t.Fatalf("packet exited %d, want %d (usage)", code, exitUsage)
	}
	if !strings.Contains(stderr, "--out must differ from --worksheet") {
		t.Errorf("refusal does not name the two flags the operator typed:\n%s", stderr)
	}
	if !strings.Contains(stderr, "sampling record") {
		t.Errorf("refusal does not say why the worksheet must survive:\n%s", stderr)
	}
}

func TestALabelledFileCannotBecomeAPacket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels-a.jsonl")
	row := `{"item_id":"it-1","claim_text":"x","prior_support_status":"supported","gold_verdict":"supported"}`
	if err := os.WriteFile(path, []byte(row+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(dir, "packet.jsonl")
	code, _, stderr := runToolCmd(t, "packet", "--worksheet", path, "--out", out)
	if code != exitInvariant {
		t.Fatalf("packet exited %d, want %d (invariant)", code, exitInvariant)
	}
	if !strings.Contains(stderr, "would hand the answer") {
		t.Errorf("refusal does not name the mistake (a labelled file is not reading material):\n%s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a packet was written even though the strip was refused")
	}
}

// "Removed 0 keys" is what a broken strip reports too, so the command says which one it is
// looking at instead of leaving a receipt that reads like a checked packet.
func TestThePacketCommandSaysWhenItStrippedNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "already-a-packet.jsonl")
	row := `{"item_id":"it-1","claim_text":"x"}`
	if err := os.WriteFile(path, []byte(row+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(dir, "packet.jsonl")
	code, stdout, stderr := runToolCmd(t, "packet", "--worksheet", path, "--out", out)
	if code != exitOK {
		t.Fatalf("packet exited %d, want 0 (an unstriped row is reported, not refused)", code)
	}
	if !strings.Contains(stderr, "0 prior_*/gold_* keys removed") {
		t.Errorf("stderr does not say that nothing was stripped:\n%s", stderr)
	}
	var receipt packetReceipt
	if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if receipt.Blind.Removed != 0 || receipt.Blind.Rows != 1 {
		t.Errorf("receipt says removed=%d rows=%d, want 0 and 1", receipt.Blind.Removed, receipt.Blind.Rows)
	}
}

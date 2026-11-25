package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// `goldtool packet` exists because the rule arrived after the file it applies to.
//
// doc/301 §9.1 rule 3 has said since v1.12 (doc/304 E112) that an annotator reads a packet
// with no `prior_*`/`gold_*` key and never the worksheet. `sample --blind-out` writes that
// packet at draw time, which covers every draw made after v1.12 — and nothing drawn before
// it. `tmp/gold/worksheet-primary-v1.jsonl` and `worksheet-cand-all-v1.jsonl` are older than
// the flag: they carry the model's prior opinion in every row, and they are the two files
// T02's 120 annotations are supposed to come from (doc/304 §7 item 37).
//
// The three routes were: redraw them, hand-edit them, or derive the packet from the
// worksheet that exists. Redrawing changes the sample — a different seed is a different 120
// rows and a receipt that no longer matches `prereg` — and hand-editing the worksheet
// destroys the sampling record the receipt describes. Neither is needed: the packet is a
// *function* of the worksheet, so a second command can apply the same strip and write the
// same receipt block. Nothing in the worksheet changes; the drawn ids, the prior mix and
// the receipt that describes them stay exactly where they are.
//
// One difference from `sample`'s call site is deliberate: the rows are read as JSON objects
// rather than as `worksheetRow`. A struct round-trip silently drops every field the struct
// does not know, so a packet built from last month's worksheet would hand an annotator a row
// missing whatever last month had that today's struct does not — the failure `blindLine` was
// written against, one layer up. The cost is that keys come out sorted, because the object is
// re-marshalled; `item_id` does not move, so a label still joins back.
const packetNote = "the packet is derived from the worksheet and the worksheet is not " +
	"touched: this command exists for draws made before `sample --blind-out` (doc/304 §7 " +
	"item 37), so the sample, its seed and its receipt stay valid. Keys come out sorted " +
	"because the row is re-marshalled from a JSON object; item_id does not move, and the " +
	"packet's sha256 in this receipt is what a label file cites."

type packetReceipt struct {
	Worksheet       string     `json:"worksheet"`
	WorksheetSHA256 string     `json:"worksheet_sha256"`
	Blind           blindBlock `json:"blind"`
	Note            string     `json:"note"`
}

func runPacket(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("packet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	worksheet := fs.String("worksheet", "", "worksheet JSONL drawn by `goldtool sample` (any vintage)")
	out := fs.String("out", "", "annotation packet JSONL path (must differ from --worksheet)")
	quiet := fs.Bool("quiet", false, "do not print the JSON receipt")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*worksheet) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool packet: --worksheet and --out are required")
		return exitUsage
	}
	if err := blindTargetOK(*worksheet, "--worksheet", *out, "--out"); err != nil {
		fmt.Fprintf(stderr, "goldtool packet: %v\n", err)
		return exitUsage
	}

	sum, err := hashFile(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool packet: worksheet: %v\n", err)
		return exitUsage
	}
	rows, err := readRowsRaw(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool packet: worksheet: %v\n", err)
		return exitUsage
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "goldtool packet: worksheet %s has no rows: an empty packet would look "+
			"like a draw that selected nothing\n", *worksheet)
		return exitInvariant
	}

	block, err := writeBlindFile(*out, rows)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool packet: %v\n", err)
		return exitInvariant
	}
	// A packet built from a file that is already a packet removes nothing, and "removed 0
	// keys per row" is exactly what a *broken* strip also reports. The receipt carries the
	// number, and this line says out loud which of the two the operator is looking at.
	if block.Removed == 0 {
		fmt.Fprintf(stderr, "goldtool packet: 0 prior_*/gold_* keys removed — either this "+
			"worksheet carries no prior status, or %s is already a packet. Nothing was "+
			"stripped, so nothing here proves blindness.\n", *worksheet)
	}

	receipt := packetReceipt{
		Worksheet: filepath.Base(*worksheet), WorksheetSHA256: sum,
		Blind: block, Note: packetNote,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if !*quiet {
		if err := enc.Encode(receipt); err != nil {
			fmt.Fprintf(stderr, "goldtool packet: %v\n", err)
			return exitInvariant
		}
	}
	return exitOK
}

// readRowsRaw keeps each row as the JSON object it was, so the packet is the worksheet minus
// two prefixes and not the worksheet minus everything the current structs do not mention.
func readRowsRaw(path string) ([]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body := strings.TrimRight(string(raw), "\n")
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	lines := strings.Split(body, "\n")
	rows := make([]any, 0, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("line %d is not a JSON object: %w", i+1, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// poolOf writes a pool file with `n` unlabelled items and a controllable prior-status
// mix, which is the only thing the sampler is allowed to look at.
func poolOf(t *testing.T, n int, statuses map[string]int) string {
	t.Helper()
	items := make([]Item, 0, n)
	for i := 1; i <= n; i++ {
		it := sampleItem(i, "")
		it.GoldVerdict = ""
		it.GoldQuoteStatus = ""
		it.GoldEvidenceIDs = nil
		it.Annotators = nil
		it.PriorSupportStatus = "supported"
		items = append(items, it)
	}
	// Spread the requested statuses over the tail of the pool, deterministically.
	i := 0
	for status, count := range statuses {
		for j := 0; j < count && i < n; j++ {
			items[i].PriorSupportStatus = status
			i++
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.jsonl")
	if err := writeSample(path, items); err != nil {
		t.Fatalf("write pool: %v", err)
	}
	return path
}

func devnull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The same seed must draw the same items out of the same pool: a sample that cannot be
// re-drawn is an anecdote (doc/301 §4.4 D5).
func TestSampleIsReproducible(t *testing.T) {
	pool := poolOf(t, 60, map[string]int{"unsupported": 10})
	out := devnull(t)
	defer out.Close()
	dir := t.TempDir()

	run1 := filepath.Join(dir, "a.jsonl")
	run2 := filepath.Join(dir, "b.jsonl")
	run3 := filepath.Join(dir, "c.jsonl")
	for path, seed := range map[string]string{run1: "7", run2: "7", run3: "8"} {
		errFile, readErr := captureStderr(t)
		code := run([]string{"sample", "--pool", pool, "--out", path,
			"--n", "20", "--seed", seed, "--quiet"}, out, errFile)
		if msg := readErr(); code != exitOK {
			t.Fatalf("sample --seed %s -> exit %d: %s", seed, code, msg)
		}
	}
	a, _ := os.ReadFile(run1)
	b, _ := os.ReadFile(run2)
	c, _ := os.ReadFile(run3)
	if string(a) != string(b) {
		t.Fatal("the same seed drew two different samples")
	}
	if string(a) == string(c) {
		t.Fatal("two different seeds drew the same sample out of 60 items — the seed is not wired")
	}
	if got := strings.Count(string(a), "\n"); got != 20 {
		t.Fatalf("want 20 worksheet lines, got %d", got)
	}
}

// Stratification is the option that over-samples a class, so it must honour the
// targets exactly and must not borrow from another stratum to fill a gap.
func TestSampleStratifiedHonoursTargets(t *testing.T) {
	pool := poolOf(t, 100, map[string]int{"unsupported": 12})
	out := devnull(t)
	defer out.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	code := run([]string{"sample", "--pool", pool, "--out", path, "--strategy", "stratified",
		"--targets", "supported=20,unsupported=10", "--seed", "3", "--quiet"}, out, out)
	if code != exitOK {
		t.Fatalf("stratified sample -> exit %d", code)
	}
	items, err := readItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 30 {
		t.Fatalf("want 30 items (20+10), got %d", len(items))
	}
	counts := countBy(items, func(it Item) string { return it.PriorSupportStatus })
	if counts["unsupported"] != 10 || counts["supported"] != 20 {
		t.Fatalf("targets were not honoured: %v", counts)
	}
}

// A stratum the pool cannot fill is exit 3, and without --allow-short no worksheet is
// written at all: an under-filled class must not become an invisible property of the
// gold file.
func TestSampleStratifiedShortStratumIsThirdState(t *testing.T) {
	pool := poolOf(t, 50, map[string]int{"unsupported": 3})
	out := devnull(t)
	defer out.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "short.jsonl")
	code := run([]string{"sample", "--pool", pool, "--out", path, "--strategy", "stratified",
		"--targets", "supported=20,unsupported=40", "--seed", "3", "--quiet"}, out, out)
	if code != exitIncomplete {
		t.Fatalf("a short stratum must be exit %d, got %d", exitIncomplete, code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("no worksheet may be written when a stratum is short and --allow-short is absent")
	}
	if code := run([]string{"sample", "--pool", pool, "--out", path, "--strategy", "stratified",
		"--targets", "supported=20,unsupported=40", "--seed", "3", "--allow-short", "--quiet"},
		out, out); code != exitIncomplete {
		t.Fatalf("--allow-short writes the file but must still exit %d", exitIncomplete)
	}
	items, err := readItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 23 {
		t.Fatalf("want 20 supported + 3 unsupported = 23, got %d", len(items))
	}
}

// Stratified sampling without targets is a refusal, not a default: the targets are
// the pre-registration decision (doc/304 §7 item 10).
// The pre-registration has two tracks and the tool has to keep them apart, because
// the difference is not cosmetic: a stratified draw over-samples classes by
// construction, so its class mix stops estimating the corpus and a plain CP bound
// computed on it is systematically optimistic (doc/301 §9.1, doc/304 §7 item 10).
//
// Refusing `--strategy stratified --role primary` is therefore the same kind of rule
// as refusing a default `--targets`: it makes "which sample is the acceptance sample"
// a property of the file rather than a memory.
func TestSampleStratifiedCannotBePrimary(t *testing.T) {
	poolPath := poolOf(t, 12, map[string]int{"unsupported": 4})
	out := filepath.Join(t.TempDir(), "w.jsonl")
	stderr, read := captureStderr(t)
	code := run([]string{"sample", "--pool", poolPath, "--out", out, "--strategy", "stratified",
		"--role", "primary", "--targets", "supported=6,unsupported=6"}, devnull(t), stderr)
	if code != exitUsage {
		t.Fatalf("a stratified primary sample must be refused with exit 2, got %d", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a refused draw must not write a worksheet")
	}
	msg := read()
	for _, want := range []string{"cannot be --role primary", "doc/301 §9.1", "doc/304 §7 item 10"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must name %q, got: %s", want, msg)
		}
	}
}

// …and the two tracks must be independently numbered in the receipt, so that a
// supplementary worksheet produced today cannot be read as the primary sample of the
// same seed tomorrow. Random is primary by default; an extra random draw may be
// supplementary, and then it says so.
func TestSampleRecordsRoleAndSampleID(t *testing.T) {
	poolPath := poolOf(t, 12, map[string]int{"unsupported": 4})

	cases := []struct {
		name   string
		args   []string
		role   string
		wantID string
	}{
		{"random defaults to primary", []string{"--n", "6", "--seed", "7"}, "primary", "primary-random-7"},
		{"random can be supplementary", []string{"--n", "6", "--seed", "7", "--role", "supplementary"}, "supplementary", "supplementary-random-7"},
		{"stratified is supplementary", []string{"--n", "6", "--seed", "7", "--strategy", "stratified",
			"--targets", "supported=3,unsupported=3"}, "supplementary", "supplementary-stratified-7"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "w.jsonl")
		args := append([]string{"sample", "--pool", poolPath, "--out", outPath}, c.args...)
		stdout, err := os.CreateTemp(dir, "receipt")
		if err != nil {
			t.Fatal(err)
		}
		code := run(args, stdout, devnull(t))
		if code != exitOK {
			t.Fatalf("%s: exit %d", c.name, code)
		}
		if _, err := stdout.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		var got sampleReceipt
		if err := json.NewDecoder(stdout).Decode(&got); err != nil {
			t.Fatalf("%s: receipt is not valid JSON: %v", c.name, err)
		}
		stdout.Close()
		if got.Role != c.role {
			t.Errorf("%s: role = %q, want %q", c.name, got.Role, c.role)
		}
		if got.SampleID != c.wantID {
			t.Errorf("%s: sample_id = %q, want %q", c.name, got.SampleID, c.wantID)
		}
	}
}

func TestSampleStratifiedRequiresTargets(t *testing.T) {
	pool := poolOf(t, 10, nil)
	out := devnull(t)
	defer out.Close()
	errFile, readErr := captureStderr(t)
	code := run([]string{"sample", "--pool", pool, "--out", filepath.Join(t.TempDir(), "x.jsonl"),
		"--strategy", "stratified"}, out, errFile)
	msg := readErr()
	if code != exitUsage {
		t.Fatalf("want exit %d, got %d (%s)", exitUsage, code, msg)
	}
	if !strings.Contains(msg, "doc/304 §7 item 10") {
		t.Fatalf("the refusal must name the decision it refuses to make, got %q", msg)
	}
}

// captureStderr hands back a file to pass as stderr plus a reader that closes it and
// returns what was written. A refusal is only a refusal if the reason reaches the
// person who ran the command.
func captureStderr(t *testing.T) (*os.File, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	return f, func() string {
		f.Close()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

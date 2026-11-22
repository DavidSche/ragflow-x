package main

// Tests for the parts of `assurance-quality` that are this command's own logic rather
// than §9.4's: the window, the receipt, and the arguments.
//
// The five checks themselves are tested where they live
// (internal/service/report_quality_selfcheck_test.go); the SQL that feeds them is
// cross-checked against independent counting in doc/304 §5.3, because a query can only
// be shown to be wrong by a second query.
//
// Two behaviours here are load-bearing for the evidence chain and are asserted, not
// assumed:
//
//   - a receipt is **not** rewritten (`O_EXCL`), and the refusal happens *before* any
//     query runs, so a refused run leaves no window half-reported;
//   - `unverified` writes `ok: false`, because the JSON outlives the console and is
//     what a later reader will trust.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestParseSinceAcceptsBothSpellings(t *testing.T) {
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	at, err := parseSince("168h", now)
	if err != nil || at == nil || !at.Equal(now.Add(-168*time.Hour)) {
		t.Fatalf("a duration is a window back from now: %v %v", at, err)
	}
	at, err = parseSince("2026-11-01T00:00:00Z", now)
	if err != nil || at == nil || !at.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("an instant is the window start: %v %v", at, err)
	}
	if at, err := parseSince("  ", now); at != nil || err != nil {
		t.Fatalf("an empty --since means every row: %v %v", at, err)
	}
	for _, bad := range []string{"-1h", "yesterday", "168"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Fatalf("%q must not parse: a window nobody can place is a window nobody can reproduce", bad)
		}
	}
}

func TestWindowFilterNamesTheTableItReads(t *testing.T) {
	at := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	where, args := window{}.filter("created_at", "tenant_id")
	if where != "" || args != nil {
		t.Fatalf("an empty window adds no clause, got %q %v", where, args)
	}
	where, args = window{Since: &at, Tenant: "t1"}.filter("created_at", "tenant_id")
	if where != " where created_at >= $1 and tenant_id = $2" {
		t.Fatalf("unexpected clause %q", where)
	}
	if len(args) != 2 || args[1] != "t1" {
		t.Fatalf("arguments must line up with the placeholders, got %v", args)
	}
	// The joined citation query must name both sides: an unqualified tenant_id there
	// is a SQL error, and a clause over the *other* table would widen the window
	// silently instead of failing.
	where, _ = window{Tenant: "t1"}.filter("c.referenced_at", "c.tenant_id")
	if !strings.Contains(where, "c.tenant_id = $1") {
		t.Fatalf("a joined query must qualify its columns, got %q", where)
	}
}

func TestAndWhereKeepsTheWindowFirst(t *testing.T) {
	if got := andWhere("", "x = 1"); got != " where x = 1" {
		t.Fatalf("no window yet: %q", got)
	}
	if got := andWhere(" where a = $1", "x = 1"); got != " where a = $1 and x = 1" {
		t.Fatalf("a window exists: %q", got)
	}
}

func unverifiedResult() service.QualitySelfCheckResult {
	return service.EvaluateQualitySelfCheck(service.QualitySelfCheckInput{})
}

func TestWriteReceiptRecordsTheVerdictItActuallyHad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	at := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	if err := writeReceipt(path, window{Since: &at, Tenant: "t1"}, unverifiedResult()); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var rec receipt
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("the receipt must be JSON: %v", err)
	}
	if rec.Version != receiptVersion {
		t.Fatalf("version %q", rec.Version)
	}
	if rec.Window.Since != "2026-11-22T00:00:00Z" || rec.Window.Tenant != "t1" {
		t.Fatalf("the window must be stored with the result: %+v", rec.Window)
	}
	// The fixture has no denominators at all, so every row is unverified: `ok` must be
	// false even though nothing *failed*, which is the distinction exit code 3 carries.
	if rec.Verdict != "unverified" || rec.OK || rec.Failed != 0 || rec.Unverified != 5 {
		t.Fatalf("an unverified run is not ok: %+v", rec)
	}
	if len(rec.Problems) != 5 {
		t.Fatalf("every non-passing row must be named, got %v", rec.Problems)
	}
	for _, problem := range rec.Problems {
		if !strings.Contains(problem, " unverified: ") {
			t.Fatalf("a problem must say which sentence it is: %q", problem)
		}
	}
}

func TestWriteReceiptRefusesToOverwriteEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	if err := writeReceipt(path, window{}, unverifiedResult()); err != nil {
		t.Fatalf("first write: %v", err)
	}
	err := writeReceipt(path, window{}, unverifiedResult())
	if err == nil {
		t.Fatal("a second run must not overwrite the first one's receipt")
	}
	if !strings.Contains(err.Error(), "evidence of one run") {
		t.Fatalf("the refusal must say why: %v", err)
	}
}

func TestParseOptionsFailsBeforeTheDatabase(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "r.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no dsn", []string{"--receipt", filepath.Join(dir, "a.json")}, "--dsn is required"},
		{"receipt already exists", []string{"--dsn", "x", "--receipt", existing}, "refusing to overwrite"},
		{"stray argument", []string{"--dsn", "x", "extra"}, "unexpected argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseOptions(tc.args, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	opts, err := parseOptions([]string{"--dsn", "x", "--receipt", filepath.Join(dir, "new.json")},
		&bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("a fresh receipt path must be accepted: %v", err)
	}
	if opts.dsn != "x" {
		t.Fatalf("dsn %q", opts.dsn)
	}
}

func TestHelpIsNotAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("--help must exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Exit codes: 0") {
		t.Fatalf("help must name the four sentences:\n%s", stdout.String())
	}
	code = run(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("a missing dsn is usage, got %d", code)
	}
	if !strings.Contains(stderr.String(), "RGX_ASSURANCE_DSN") {
		t.Fatalf("the usage error must name the flag and the env var: %s", stderr.String())
	}
}

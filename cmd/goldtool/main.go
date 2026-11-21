// Command goldtool is the machine half of task T02 (doc/301 §9.1): it exports
// annotation candidates out of real, published report revisions, validates an
// annotated gold file against the rules that make it usable as an evaluation
// input, computes the inter-annotator agreement the plan gates on, and assembles
// a frozen `report-assurance-gold.vN.jsonl` out of two independent annotations
// plus adjudication.
//
// It exists because the gold set is the *only* input the quality claims in
// doc/301 §4.2 are allowed to rest on, and a hand-made file would silently break
// three promises at once: same shape as runtime input, no customer text in the
// tree, and a label someone can point at. So the tool, not the annotator, decides
// what an item looks like; the annotator only decides what it means.
//
// Subcommands:
//
//	pool       export candidates from published revisions of a live database (read-only)
//	validate   check a gold file against doc/301 §9.1 (offline, no database)
//	kappa      pairwise Cohen's κ over per-annotator label files (offline)
//	assemble   labels + adjudication -> redacted, validated, frozen gold file
//
// Exit codes:
//
//	0  ok
//	1  an invariant failed (the message names the invariant, not just "invalid")
//	2  usage / environment error (missing flag, unreadable file, no DSN)
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
)

const usage = `goldtool - gold-set tooling for report assurance (doc/301 §9.1)

usage:
  goldtool pool     --dsn DSN --out pool.jsonl [--max-revisions 40] [--tenant ID]
  goldtool validate --gold FILE.jsonl [--labels FILE.jsonl] [--strict-coverage]
  goldtool kappa    --labels FILE.jsonl [--min 0.70]
  goldtool assemble --pool pool.jsonl --labels-a a.jsonl --labels-b b.jsonl
                    [--adjudication adj.jsonl] --version v1 --out DIR

The DSN is only ever used inside a read-only transaction: this tool annotates
history, it never writes it.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "pool":
		return runPool(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "kappa":
		return runKappa(args[1:], stdout, stderr)
	case "assemble":
		return runAssemble(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "goldtool: unknown subcommand %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

const (
	exitOK        = 0
	exitInvariant = 1
	exitUsage     = 2
)

// envDSN is read when --dsn is not given, so the command matches the rest of the
// tooling in this repo (RGX_TEST_POSTGRES_DSN and friends).
const envDSN = "RGX_GOLDTOOL_DSN"

// openReadOnly hands back a connection pool plus a read-only transaction. The
// transaction is what enforces the promise: a bug in a query cannot write to a
// customer database, and a future edit cannot either without editing this
// function.
func openReadOnly(ctx context.Context, dsn string) (*sql.DB, *sql.Tx, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping database: %w", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	return db, tx, nil
}

// writeJSONL writes items one per line, creating the parent directory. It is
// deliberately the only writer in this package: pool, assemble and the tests all
// go through it, so "what a gold line looks like" has exactly one answer.
func writeJSONL(path string, lines []string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, line := range lines {
		w.WriteString(line)
		w.WriteString("\n")
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// stamp is the as-of date written onto exported items: the calendar day the
// pool was taken. It is not "today" at validation time, because a gold file must
// keep meaning the same thing when it is re-validated next month.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02") }

func trimSpace(s string) string { return strings.TrimSpace(s) }

func marshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// Command gormguard reports GORM query builders that serve both a Count and a read
// after carrying a clause only the count wanted (Group/Select/Distinct/Having/Unscoped,
// and — as a warning — Joins).
//
// The knowledge-ops dashboard's 500 was this shape: a bare `q.Group("id")` mutated the
// paging builder, so the count produced `count(*) ... GROUP BY id` (correct) and the
// read produced `SELECT * ... GROUP BY id` (PostgreSQL 42803 / wrong rows). SQLite
// accepts the read, so nothing but a PostgreSQL contract test or this check catches it.
//
// Usage:
//
//	go run ./cmd/gormguard ./internal
//
// A shared clause exits 1. A shared join alone exits 0 and prints a warning: it is
// usually intentional (count and page the same joined rows) and becomes fatal only when
// a Group/Distinct joins it on the same builder, which this check then reports as an
// error. Exit 2 is a usage or parse failure.
package main

import (
	"fmt"
	"os"

	"github.com/ragflow-x/ragflow-x/internal/gormguard"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"./internal"}
	}
	findings, err := gormguard.ScanDirs(roots...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gormguard: %v\n", err)
		os.Exit(2)
	}
	var problems, warnings int
	for _, f := range findings {
		if f.Severity == gormguard.SeverityError {
			problems++
			fmt.Fprintln(os.Stderr, f.String())
			continue
		}
		warnings++
		fmt.Fprintf(os.Stderr, "warning: %s\n", f.String())
	}
	if problems > 0 {
		fmt.Fprintf(os.Stderr, "gormguard: %d shared builder(s) across %v; a count and a read on one builder must not carry Group/Select/Distinct/Having/Unscoped\n", problems, roots)
		os.Exit(1)
	}
	fmt.Printf("gormguard: clean across %v (%d shared-join warning(s))\n", roots, warnings)
}

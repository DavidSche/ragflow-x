package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `goldtool pool-material` reads a *pool export that already exists* and prints the
// doc/304 §1.3 W-2 reading: how many items carry a frozen candidate row, how many carry
// none, what `as_of_date` values the export spans, and whether the ceiling on
// `conflicting` still reaches the frozen plan's 24.
//
// It exists because W-2's decision was "expand the corpus, re-scan `pool`, then check the
// per-stratum candidate counts", and the middle step needs a database, a DSN and a live
// stack — while the *reading* needs none of them. Splitting the two means a re-scan can be
// re-derived from an archived file months later, by anyone, without credentials. The
// numbers come from the same `poolMaterialOf` that `pool` now prints in its own receipt,
// so the two can never disagree.
//
// It is deliberately not a verdict on the gold set: reaching 24 does not license drawing
// 24 (the sampler and the two annotators still have to produce labels), and falling short
// does not delete the plan. Exit 3 says only "this export cannot support the frozen plan",
// which is the same sentence `pool` uses for its coverage floors. Nor is `reachable` a
// statement about the *whole* plan: `material.not_measured` names the strata this file
// cannot count at all, so `conflicting` being reachable says nothing about `outdated`.

type poolMaterialExport struct {
	In       string       `json:"in"`
	Items    int          `json:"items"`
	Material poolMaterial `json:"material"`
	// StampNote travels with the export, not just with the struct comment: the reader of
	// a receipt file months later has this file and nothing else.
	StampNote string `json:"export_stamp_note"`
	// Plan is the frozen plan *as frozen*: `conflicting 24` comes from
	// GoldDistribution, not from a flag, because a command that let the caller pass a
	// target could be pointed at 10 and report `reachable`.
	Plan poolMaterialPlan `json:"frozen_plan"`
}

type poolMaterialPlan struct {
	Conflicting int    `json:"conflicting"`
	Reachable   bool   `json:"reachable"`
	Note        string `json:"note"`
}

const poolMaterialPlanNote = "the frozen plan is read from the tree, never from a flag; " +
	"`reachable` only says the export has enough items with frozen candidate rows"

func runPoolMaterial(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("pool-material", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "pool JSONL written by `goldtool pool --out` (read offline)")
	quiet := fs.Bool("quiet", false, "do not print the JSON receipt")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*in) == "" {
		fmt.Fprintln(stderr, "goldtool pool-material: --in is required")
		return exitUsage
	}
	items, err := readItems(*in)
	if err != nil {
		// The path and the line are in the error: "unreadable" without "which line" is
		// the failure mode that sends the reader back to the database instead of the file.
		fmt.Fprintf(stderr, "goldtool pool-material: %v\n", err)
		return exitUsage
	}
	export := poolMaterialExport{
		In:        *in,
		Items:     len(items),
		Material:  poolMaterialOf(items),
		StampNote: poolMaterialStampNote,
	}
	export.Plan = poolMaterialPlan{
		Conflicting: GoldDistribution[model.ReportVerdictConflicting],
		Note:        poolMaterialPlanNote,
	}
	export.Plan.Reachable = export.Material.ConflictingUpperBound >= export.Plan.Conflicting
	if !*quiet {
		printJSON(stdout, export)
	}
	if !export.Plan.Reachable {
		return exitIncomplete
	}
	return exitOK
}

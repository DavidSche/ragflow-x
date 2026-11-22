package main

// `goldtool kc2` is the executable half of task T11 (doc/301 §5.7): the KC-2 experiment
// "Compiled Locator 辅助定位 + Raw Retrieval" vs today's raw retrieval.
//
// T11 is a two-week, allowed-to-fail PoC, and the plan's own discipline (§9.2) says an
// experiment's decision rule is written down *before* the numbers exist. So this file has
// three jobs, in the order they have to happen:
//
//  1. `prereg` freezes §5.7's three numbers and the two arm definitions into a file that
//     `--check` compares against the compiled constants in `internal/service`. Editing a
//     threshold in Go without re-freezing is red; editing the file without editing Go is
//     red. Neither direction can make the experiment easier.
//  2. `collect` reads one revision per arm out of a live database inside a read-only
//     transaction and writes the section-level observations `eval` judges. It refuses the
//     one thing that would make the comparison a lie — two revisions of different reports,
//     which cannot be paired by section — and it exits 3 (not 0) when an arm comes back
//     empty, because "collected nothing" is not a successful collection.
//  3. `eval` refuses to judge anything without a frozen pre-registration (`--prereg` is
//     mandatory) and prints the four judging rows with their denominators.
//
// What it does not do: it does not *run* the retrieval. Switching `report.locator_hints_enabled`
// on, generating two revisions over the same outline, and paying for the tokens is the
// PoC itself. The tool measures it; it cannot produce it.

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/service"
)

const kc2PreregSchema = "rgx.goldtool.kc2prereg/1"

// kc2Threshold is one pre-registered number plus the sentence it was copied from. The
// table below is built from the constants `internal/service` judges with, so the frozen
// file and the code can only agree or be red — there is no third state.
type kc2Threshold struct {
	Key   string  `json:"key"`
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
	Quote string  `json:"quote"`
	Ref   string  `json:"ref"`
}

type kc2ArmDef struct {
	Name           string `json:"name"`
	Behavior       string `json:"behavior"`
	EvidenceSource string `json:"evidence_source"`
}

// kc2Plan is the sample size frozen in advance. It is deliberately a separate block from
// `thresholds`: a plan quantity is met by adding sections, a threshold is never moved.
type kc2Plan struct {
	SectionsPerArm int    `json:"sections_per_arm"`
	Note           string `json:"note"`
}

type kc2PreregFile struct {
	Schema     string         `json:"schema"`
	Experiment string         `json:"experiment"`
	Ref        string         `json:"ref"`
	FrozenAt   string         `json:"frozen_at"`
	Author     string         `json:"author"`
	Primary    string         `json:"primary_metric"`
	Decision   string         `json:"decision"`
	Arms       []kc2ArmDef    `json:"arms"`
	Thresholds []kc2Threshold `json:"thresholds"`
	Plan       kc2Plan        `json:"plan"`
	Invariants []string       `json:"invariants"`
	NotClaimed []string       `json:"not_claimed"`
}

// kc2ThresholdTable is doc/301 §5.7's 判定 line, one number per row, quotes verbatim.
func kc2ThresholdTable() []kc2Threshold {
	return []kc2Threshold{
		{Key: "kc2LiftFloorPP", Unit: "pp", Value: service.KC2LiftFloorPP,
			Quote: "lift ≥10pp", Ref: "301 §5.7"},
		{Key: "kc2TraceabilityFloor", Unit: "rate", Value: service.KC2TraceabilityFloor,
			Quote: "原文可回溯率 **100%**", Ref: "301 §5.7"},
		{Key: "kc2CostDeltaCeiling", Unit: "rate", Value: service.KC2CostDeltaCeiling,
			Quote: "成本增幅 ≤30%", Ref: "301 §5.7"},
	}
}

// kc2ArmDefinitions is §5.7's experiment-group table. The treatment's one documented
// capability boundary is part of the definition, not a footnote: page-level narrowing is
// not a capability the code has (`reportLocationHint` has no page field), and an
// experiment that pretends it does would hand the treatment an input it cannot receive.
func kc2ArmDefinitions() []kc2ArmDef {
	return []kc2ArmDef{
		{Name: service.KC2ArmControl,
			Behavior:       "传统 raw retrieval（现状）",
			EvidenceSource: "原始 chunk"},
		{Name: service.KC2ArmTreatment,
			Behavior:       "Compiled Locator 辅助定位 + Raw Retrieval：先用 locator hint 只缩小文档范围（source_document_ids），再对原文检索与取证；不做页级缩小（该能力不存在）",
			EvidenceSource: "仍然是原始 chunk（编译件只参与「去哪里找」）"},
	}
}

// kc2DecisionSentence is §5.7's 判定 line without its bullet marker, copied not
// paraphrased: the gate greps it inside doc/develop/301-*.md.
func kc2DecisionSentence() string {
	return "lift ≥10pp 且回溯 100% 且成本增幅 ≤30% → 允许在实验开关下用于报告取证；否则保持关闭并写负面实测报告。"
}

func kc2Invariants() []string {
	return []string{
		"原文可回溯率 100%（每条引用都能回到 raw chunk）",
		"编译件不得进入 EvidenceSnapshot、不得出现在证据引用里",
		"compiled 组不得把编译产物直接当检索结果：Treatment 的证据来源仍然是原始 chunk",
		"PoC 期间不得打开默认检索、自动增强、默认路由或 Release Gate",
	}
}

func kc2NotClaimed() []string {
	return []string{
		"lift 的判定量是点估计：两个 CP 区间相减不是配对差分的区间，本文件不为 lift 预注册任何区间。",
		"sections_per_arm=30 是预注册的规划量（沿用 §9.2 E2 的 30 节口径），不是 §5.7 的门槛；样本不足时结论是 unverified，不是 refused。",
		"frozen_at 是自述时间：机器可检的只有阈值与编译常量一致、两臂定义与 §5.7 一致（git 历史是唯一的旁证）。",
		"页级定位没有预注册，因为该能力不存在（§5.7 能力边界）；它若要做，验收第一条是同一 artifact 三次投影的页锚点一致。",
		"本文件不预注册成本模型之外的东西：token 按 §9.3 计，耗时只作次指标，不计入 30%。",
	}
}

func buildKC2Prereg(author string, now time.Time) kc2PreregFile {
	return kc2PreregFile{
		Schema:     kc2PreregSchema,
		Experiment: "KC-2",
		Ref:        "doc/301 §5.7",
		FrozenAt:   now.UTC().Format(time.RFC3339),
		Author:     author,
		Primary:    "evidence_coverage",
		Decision:   kc2DecisionSentence(),
		Arms:       kc2ArmDefinitions(),
		Thresholds: kc2ThresholdTable(),
		Plan: kc2Plan{SectionsPerArm: service.KC2PlanSectionsPerArm,
			Note: "每臂 30 节，沿用 301 §9.2 的 E2 口径；不足 30 节时判定为 unverified，不改门槛。"},
		Invariants: kc2Invariants(),
		NotClaimed: kc2NotClaimed(),
	}
}

// checkKC2Prereg compares a file against the compiled rule in both directions. Every
// message names the field, because "prereg invalid" would leave a reader guessing which
// of five things to edit.
func checkKC2Prereg(p kc2PreregFile) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if p.Schema != kc2PreregSchema {
		add("schema = %q, want %q", p.Schema, kc2PreregSchema)
	}
	if p.Experiment != "KC-2" {
		add("experiment = %q, want \"KC-2\"", p.Experiment)
	}
	if !strings.Contains(p.Ref, "§5.7") {
		add("ref = %q, want the section the rule lives in (301 §5.7)", p.Ref)
	}
	if strings.TrimSpace(p.FrozenAt) == "" {
		add("frozen_at is empty: a pre-registration without a freeze time cannot be told from a note")
	}
	if strings.TrimSpace(p.Author) == "" {
		add("author is empty: an unsigned pre-registration has nobody to ask")
	}
	if strings.TrimSpace(p.Decision) != kc2DecisionSentence() {
		add("decision is not doc/301 §5.7's sentence verbatim:\n    have: %s\n    want: %s", p.Decision, kc2DecisionSentence())
	}

	want := kc2ThresholdTable()
	if len(p.Thresholds) != len(want) {
		add("declares %d threshold(s), doc/301 §5.7 has %d", len(p.Thresholds), len(want))
	} else {
		for i, w := range want {
			got := p.Thresholds[i]
			switch {
			case got.Key != w.Key:
				add("threshold[%d].key = %q, want %q", i, got.Key, w.Key)
			case got.Value != w.Value:
				add("threshold %s = %v, but internal/service judges with %v (moving either side is a change to the pre-registered rule)", got.Key, got.Value, w.Value)
			case got.Unit != w.Unit:
				add("threshold %s unit = %q, want %q", got.Key, got.Unit, w.Unit)
			case got.Quote != w.Quote:
				add("threshold %s quote = %q, want %q (the sentence the number was copied from)", got.Key, got.Quote, w.Quote)
			case got.Ref != w.Ref:
				add("threshold %s ref = %q, want %q", got.Key, got.Ref, w.Ref)
			}
		}
	}

	wantArms := kc2ArmDefinitions()
	if len(p.Arms) != len(wantArms) {
		add("declares %d arm(s), §5.7 defines %d", len(p.Arms), len(wantArms))
	} else {
		for i, w := range wantArms {
			if p.Arms[i].Name != w.Name {
				add("arm[%d].name = %q, want %q", i, p.Arms[i].Name, w.Name)
			}
			if strings.TrimSpace(p.Arms[i].Behavior) == "" || strings.TrimSpace(p.Arms[i].EvidenceSource) == "" {
				add("arm %s has no behavior/evidence source: §5.7 fixes both", p.Arms[i].Name)
			}
		}
	}

	if p.Plan.SectionsPerArm != service.KC2PlanSectionsPerArm {
		add("plan.sections_per_arm = %d, the compiled plan is %d (a plan quantity is met by adding sections, never by editing either side)",
			p.Plan.SectionsPerArm, service.KC2PlanSectionsPerArm)
	}
	if len(p.Invariants) == 0 {
		add("invariants is empty: the 100%% traceability clause is the reason this experiment is admissible at all")
	}
	if len(p.NotClaimed) == 0 {
		add("not_claimed is empty: a pre-registration that states only what it guarantees overstates it")
	}
	return problems
}

// --- commands ------------------------------------------------------------------------

func runKC2(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "goldtool kc2: missing subcommand (prereg | collect | eval)\n\n"+kc2Usage)
		return exitUsage
	}
	switch args[0] {
	case "prereg":
		return runKC2Prereg(args[1:], stdout, stderr)
	case "collect":
		return runKC2Collect(args[1:], stdout, stderr)
	case "eval":
		return runKC2Eval(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, kc2Usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "goldtool kc2: unknown subcommand %q\n\n%s", args[0], kc2Usage)
		return exitUsage
	}
}

const kc2Usage = `usage:
  goldtool kc2 prereg  --freeze --author NAME --out prereg-kc2-v1.json
  goldtool kc2 prereg  --check prereg-kc2-v1.json
  goldtool kc2 collect --dsn DSN --control-revision ID --treatment-revision ID --out items.jsonl
  goldtool kc2 eval    --items items.jsonl --prereg prereg-kc2-v1.json [--out report.md]

The pre-registration is mandatory for eval: doc/301 §5.7's rule is frozen before the run,
and a verdict computed against a drifted file is not the experiment that was registered.
`

func runKC2Prereg(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("kc2 prereg", flag.ContinueOnError)
	fs.SetOutput(stderr)
	freeze := fs.Bool("freeze", false, "write a new pre-registration (refuses to overwrite one)")
	checkPath := fs.String("check", "", "validate an existing pre-registration file")
	author := fs.String("author", "", "who froze this file")
	out := fs.String("out", "", "write the pre-registration here (required with --freeze)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *freeze == (*checkPath != "") {
		fmt.Fprintln(stderr, "goldtool kc2 prereg: pass exactly one of --freeze or --check")
		return exitUsage
	}

	if *checkPath != "" {
		p, err := readKC2Prereg(*checkPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool kc2 prereg: %v\n", err)
			return exitUsage
		}
		problems := checkKC2Prereg(p)
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool kc2 prereg: %s\n", pr)
		}
		if len(problems) > 0 {
			return exitInvariant
		}
		sum, err := hashFile(*checkPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool kc2 prereg: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "goldtool kc2 prereg: %s ok\n  experiment %s  frozen %s  arms %d  thresholds %d  sha256 %s\n",
			*checkPath, p.Experiment, p.FrozenAt, len(p.Arms), len(p.Thresholds), sum)
		// Machine-readable lines for scripts/release-gate/kc2-prereg.sh: each quote is
		// grepped inside doc/301, so a paraphrase in either copy is red.
		for _, th := range p.Thresholds {
			fmt.Fprintf(stdout, "kc2-threshold: key=%s value=%v unit=%s\n", th.Key, th.Value, th.Unit)
			fmt.Fprintf(stdout, "kc2-quote: %s\n", th.Quote)
		}
		fmt.Fprintf(stdout, "kc2-decision: %s\n", p.Decision)
		fmt.Fprintf(stdout, "kc2-plan: sections_per_arm=%d\n", p.Plan.SectionsPerArm)
		return exitOK
	}

	if strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool kc2 prereg: --out is required with --freeze")
		return exitUsage
	}
	if strings.TrimSpace(*author) == "" {
		fmt.Fprintln(stderr, "goldtool kc2 prereg: --author is required; an unsigned pre-registration has nobody to ask")
		return exitUsage
	}
	p := buildKC2Prereg(*author, time.Now())
	if problems := checkKC2Prereg(p); len(problems) > 0 {
		// A file the checker would reject must never reach disk.
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool kc2 prereg: %s\n", pr)
		}
		return exitInvariant
	}
	if err := writeKC2PreregExclusive(*out, p); err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 prereg: %v\n", err)
		if errors.Is(err, os.ErrExist) {
			return exitInvariant
		}
		return exitUsage
	}
	sum, _ := hashFile(*out)
	fmt.Fprintf(stdout, "goldtool kc2 prereg: froze %s\n  sha256 %s\n  experiment %s  frozen %s\n  arms %s\n  thresholds %d\n  plan %d sections/arm\n",
		*out, sum, p.Experiment, p.FrozenAt, kc2ArmNames(p.Arms), len(p.Thresholds), p.Plan.SectionsPerArm)
	return exitOK
}

func runKC2Eval(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("kc2 eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	itemsPath := fs.String("items", "", "the section-level items file (goldtool kc2 collect / a hand-written file)")
	preregPath := fs.String("prereg", "", "the frozen KC-2 pre-registration (required)")
	out := fs.String("out", "", "write the markdown report here")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*itemsPath) == "" {
		fmt.Fprintln(stderr, "goldtool kc2 eval: --items is required")
		return exitUsage
	}
	if strings.TrimSpace(*preregPath) == "" {
		fmt.Fprintln(stderr, "goldtool kc2 eval: --prereg is required: doc/301 §5.7's rule is frozen before the run, "+
			"and a verdict computed against a drifted file is not the experiment that was registered")
		return exitUsage
	}
	p, err := readKC2Prereg(*preregPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 eval: %v\n", err)
		return exitUsage
	}
	if problems := checkKC2Prereg(p); len(problems) > 0 {
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool kc2 eval: prereg %s\n", pr)
		}
		return exitInvariant
	}
	sections, err := readKC2Items(*itemsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 eval: %v\n", err)
		return exitUsage
	}
	result := service.EvaluateKC2(sections)
	control, treatment := 0, 0
	for _, s := range sections {
		switch s.Arm {
		case service.KC2ArmControl:
			control++
		case service.KC2ArmTreatment:
			treatment++
		}
	}
	fmt.Fprintf(stdout, "goldtool kc2 eval: %d section record(s) — control %d / treatment %d\n", len(sections), control, treatment)
	report := service.RenderKC2Markdown(result)
	fmt.Fprint(stdout, report)
	if strings.TrimSpace(*out) != "" {
		if err := writeTextFile(*out, report); err != nil {
			fmt.Fprintf(stderr, "goldtool kc2 eval: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "\nwritten: %s\n", *out)
	}
	switch result.Verdict {
	case service.KC2VerdictAllowed:
		return exitOK
	case service.KC2VerdictRefused:
		// A result, not a crash — but the plan's answer to "refused" is a decision, so the
		// shell must see a non-zero code exactly as it does for a broken instrument. The
		// report separates the two blocks (problems vs rows), which is where a reader looks.
		return exitInvariant
	default:
		return exitIncomplete
	}
}

// --- collect -------------------------------------------------------------------------

// kc2Revision is the identity `collect` insists on before it pairs anything: two revisions
// of the *same* report are the only pair whose sections can be aligned by key.
type kc2Revision struct {
	ID       string
	ReportID string
	Number   int
	Status   string
}

type kc2SectionRow struct {
	Ord       int
	Key       string
	Tokens    int
	LatencyMS int64
	HintDocs  []string
	Citations int
	Traceable int
	CitedDocs []string
}

type kc2Hint struct {
	Kind              string   `json:"kind"`
	Title             string   `json:"title"`
	SourceDocumentIDs []string `json:"source_document_ids"`
}

func runKC2Collect(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("kc2 collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", "", "database DSN (default: $"+envDSN+")")
	controlRev := fs.String("control-revision", "", "the control arm's revision id (locator hints off)")
	treatmentRev := fs.String("treatment-revision", "", "the treatment arm's revision id (locator hints on)")
	out := fs.String("out", "", "write the items JSONL here")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*dsn) == "" {
		*dsn = os.Getenv(envDSN)
	}
	if strings.TrimSpace(*dsn) == "" {
		fmt.Fprintf(stderr, "goldtool kc2 collect: no DSN (--dsn or $%s)\n", envDSN)
		return exitUsage
	}
	if strings.TrimSpace(*controlRev) == "" || strings.TrimSpace(*treatmentRev) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool kc2 collect: --control-revision, --treatment-revision and --out are required")
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, tx, err := openReadOnly(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: %v\n", err)
		return exitUsage
	}
	defer db.Close()
	defer tx.Rollback()

	control, err := readKC2Revision(tx, *controlRev)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: %v\n", err)
		return exitUsage
	}
	treatment, err := readKC2Revision(tx, *treatmentRev)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: %v\n", err)
		return exitUsage
	}
	if control.ReportID != treatment.ReportID {
		// Two revisions of different reports have different outlines: pairing them by
		// section key would compare the outlines, not the retrieval.
		fmt.Fprintf(stderr, "goldtool kc2 collect: the two revisions belong to different reports (%s vs %s); "+
			"§5.7's arms must be the same report generated twice (hints off / hints on), or the \"lift\" "+
			"is a difference in outline\n", control.ReportID, treatment.ReportID)
		return exitInvariant
	}

	controlRows, err := readKC2Arm(tx, control.ID)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: control: %v\n", err)
		return exitUsage
	}
	treatmentRows, err := readKC2Arm(tx, treatment.ID)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: treatment: %v\n", err)
		return exitUsage
	}

	sections := kc2Records(controlRows, treatmentRows)
	if err := writeKC2Items(*out, sections); err != nil {
		fmt.Fprintf(stderr, "goldtool kc2 collect: %v\n", err)
		return exitUsage
	}
	paired := 0
	controlKeys := map[string]struct{}{}
	for _, r := range controlRows {
		controlKeys[r.Key] = struct{}{}
	}
	for _, r := range treatmentRows {
		if _, ok := controlKeys[r.Key]; ok {
			paired++
		}
	}
	fmt.Fprintf(stdout, "goldtool kc2 collect: wrote %s\n  report %s  control r%d (%s) %d section(s) / treatment r%d (%s) %d section(s)\n  paired %d\n",
		*out, control.ReportID,
		control.Number, control.Status, len(controlRows),
		treatment.Number, treatment.Status, len(treatmentRows), paired)
	hinted := 0
	for _, r := range treatmentRows {
		if len(r.HintDocs) > 0 {
			hinted++
		}
	}
	fmt.Fprintf(stdout, "  treatment sections with locator hints: %d/%d\n", hinted, len(treatmentRows))

	switch {
	case len(controlRows) == 0 || len(treatmentRows) == 0:
		fmt.Fprintf(stderr, "goldtool kc2 collect: an arm is empty — nothing to compare (exit 3, not 0)\n")
		return exitIncomplete
	case paired < service.KC2PlanSectionsPerArm:
		fmt.Fprintf(stderr, "goldtool kc2 collect: paired %d < %d sections: the experiment can be collected but not judged (exit 3)\n",
			paired, service.KC2PlanSectionsPerArm)
		return exitIncomplete
	}
	return exitOK
}

func readKC2Revision(tx *sql.Tx, id string) (kc2Revision, error) {
	var rev kc2Revision
	err := tx.QueryRow(`select id, report_id, revision, status from rgx_report_revision where id = $1`, id).
		Scan(&rev.ID, &rev.ReportID, &rev.Number, &rev.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return rev, fmt.Errorf("revision %s not found", id)
	}
	if err != nil {
		return rev, fmt.Errorf("read revision %s: %w", id, err)
	}
	return rev, nil
}

// readKC2Arm loads one revision's section-level observations. `covered` is not read from
// `evidence_status`: the numerator of §5.7's main metric is "at least one *usable* evidence
// item", and usable means the citation resolves to a raw chunk — the same fact the
// traceability invariant is about.
func readKC2Arm(tx *sql.Tx, revisionID string) ([]kc2SectionRow, error) {
	rows, err := tx.Query(`
		select s.ord,
		       coalesce(s.section_key, ''),
		       coalesce(s.tokens_in, 0) + coalesce(s.tokens_out, 0) as tokens,
		       case when s.started_at is not null and s.finished_at is not null
		            then (extract(epoch from (s.finished_at - s.started_at)) * 1000)::bigint
		            else 0 end as latency_ms,
		       coalesce(s.location_hints_json, '')
		  from rgx_report_section s
		 where s.revision_id = $1
		 order by s.ord`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("read sections: %w", err)
	}
	defer rows.Close()

	// Index into `out`, not a pointer: the citation loop below appends, and a slice
	// append may move the backing array, which would leave every stored pointer pointing
	// at the old copy and silently drop every citation count.
	byOrd := map[int]int{}
	var out []kc2SectionRow
	for rows.Next() {
		var r kc2SectionRow
		var raw string
		if err := rows.Scan(&r.Ord, &r.Key, &r.Tokens, &r.LatencyMS, &raw); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		for _, hint := range kc2ParseHints(raw) {
			r.HintDocs = append(r.HintDocs, hint.SourceDocumentIDs...)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sections: %w", err)
	}
	for i := range out {
		byOrd[out[i].Ord] = i
	}

	citations, err := tx.Query(`
		select c.section_id, c.section_ord,
		       count(*) as citations,
		       count(*) filter (where coalesce(c.source_chunk_id, '') <> '') as traceable,
		       coalesce(string_agg(distinct coalesce(c.source_document_id, ''), ','), '') as docs
		  from rgx_report_citation c
		 where c.revision_id = $1
		 group by c.section_id, c.section_ord`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("read citations: %w", err)
	}
	defer citations.Close()
	for citations.Next() {
		var sectionID, docList string
		var ord, count, traceable int
		if err := citations.Scan(&sectionID, &ord, &count, &traceable, &docList); err != nil {
			return nil, fmt.Errorf("scan citation counts: %w", err)
		}
		// string_agg, not array_agg: the driver has no array decoder, and document ids are
		// hex, so no id can contain the separator.
		var docs []string
		for _, doc := range strings.Split(docList, ",") {
			if doc = strings.TrimSpace(doc); doc != "" {
				docs = append(docs, doc)
			}
		}
		if idx, ok := byOrd[ord]; ok {
			out[idx].Citations = count
			out[idx].Traceable = traceable
			out[idx].CitedDocs = docs
			continue
		}
		// A citation whose section is not in this revision is a data defect the engine
		// reports as a section without a key (ord is its only handle).
		out = append(out, kc2SectionRow{Ord: ord, Citations: count, Traceable: traceable, CitedDocs: docs})
	}
	if err := citations.Err(); err != nil {
		return nil, fmt.Errorf("read citations: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ord < out[j].Ord })
	return out, nil
}

// kc2ParseHints decodes a section's frozen locators. A malformed value reads as none for
// the same reason the reader treats it that way: the column is advisory and must not make
// a section unusable.
func kc2ParseHints(raw string) []kc2Hint {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var hints []kc2Hint
	if err := json.Unmarshal([]byte(raw), &hints); err != nil {
		return nil
	}
	return hints
}

// kc2Records is the pure half of `collect`: it turns the two arms' rows into the engine's
// records, so "what counts as hint_used" is testable without a database.
func kc2Records(control, treatment []kc2SectionRow) []service.KC2Section {
	out := make([]service.KC2Section, 0, len(control)+len(treatment))
	for _, r := range control {
		out = append(out, kc2Record(service.KC2ArmControl, r))
	}
	for _, r := range treatment {
		out = append(out, kc2Record(service.KC2ArmTreatment, r))
	}
	return out
}

func kc2Record(arm string, r kc2SectionRow) service.KC2Section {
	hintDocs := map[string]struct{}{}
	for _, doc := range r.HintDocs {
		if strings.TrimSpace(doc) != "" {
			hintDocs[doc] = struct{}{}
		}
	}
	used := false
	for _, doc := range r.CitedDocs {
		if _, ok := hintDocs[doc]; ok {
			used = true
			break
		}
	}
	return service.KC2Section{
		SectionKey: r.Key,
		Ord:        r.Ord,
		Arm:        arm,
		Covered:    r.Traceable > 0,
		Citations:  r.Citations,
		Traceable:  r.Traceable,
		Hints:      len(r.HintDocs),
		HintUsed:   used,
		Tokens:     r.Tokens,
		LatencyMS:  r.LatencyMS,
	}
}

// --- files ---------------------------------------------------------------------------

func readKC2Prereg(path string) (kc2PreregFile, error) {
	var p kc2PreregFile
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, nil
}

func writeKC2PreregExclusive(path string, p kc2PreregFile) error {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readKC2Items(path string) ([]service.KC2Section, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []service.KC2Section
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var s service.KC2Section
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, s)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func writeKC2Items(path string, sections []service.KC2Section) error {
	lines := make([]string, 0, len(sections))
	for _, s := range sections {
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		lines = append(lines, string(raw))
	}
	return writeJSONL(path, lines)
}

func writeTextFile(path, body string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func kc2ArmNames(arms []kc2ArmDef) string {
	names := make([]string, 0, len(arms))
	for _, a := range arms {
		names = append(names, a.Name)
	}
	return strings.Join(names, " / ")
}

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `goldtool readiness` answers doc/304 §7 item 30's question — "may T02 start?" — as *three*
// conditions with separate receipts, because in this repository they have never failed for
// the same reason and merging them is how one gets read as the other:
//
//	Identity 这三个文件是同一批吗？（prereg / pool / worksheet 的指纹与键关系）
//	A 语料条件: can the frozen plan's strata still be drawn out of the pool?
//	B 人员与流程条件: are there two people, are they finished, does their κ clear the floor?
//
// Item 30's reading was taken by hand with three commands (`labels --check`,
// `adjudicate --stub`, `pool-material`) and then explained in prose. That prose is the fragile
// part: a hand-derived reading cannot be re-run when the files change, and its numbers cannot
// be checked against the frozen plan without opening the plan. This command is the instrument
// — it reads the files that already exist, prints all three blocks, and says `ready` only when
// all three say `pass`.
//
// The distinctions it refuses to blur:
//
//   - **A is `unverified`, not `pass`, while a planned stratum has no count in the export.**
//     Only `conflicting` is countable here (frozen candidate rows). `outdated` is a *content*
//     property — same document, same key, different value — and a pool export carries neither
//     per-document key grouping nor per-evidence dates, so it is not "zero", it is "this file
//     cannot say". Folding that into `pass` is how "A 成立" gets quoted about a stratum nobody
//     measured. The consequence is deliberate: this command cannot turn green by relaxing
//     itself — closing the `outdated` gap is either content-level scanning or the plan's
//     documented downgrade (301 §9.1.2, PM+QA, archived), and either one changes an input.
//   - **A ceiling is a row count, not a forecast.** 36 frozen candidate rows over 30 claims is
//     a ceiling of 36; reporting 30 is the denominator mistake 304 §1.3 W-1 warns about. A
//     ceiling ≥ target proves the *draw* is possible; it does not prove 24 true conflicts exist
//     in those 36 rows — that is the annotator's judgement, and B is where it shows up.
//   - **B is `fail`, not `unverified`, when the number is known and zero.** One annotator who
//     has judged 0 of 120 rows is not an unmeasurable question; it is an unanswered one.
//   - **Identity is not a formality.** A digest proves a file has not changed since *it* was
//     hashed; it cannot prove the pool and the worksheet belong to the same freeze. That is
//     why the chain also compares the worksheet's item ids against the pool's, and why the
//     prerolled `prereg.inputs[]` (not a second registry) is the reference.
//   - **The plan and the κ recipe are frozen, not flags.** The plan is read from the prereg
//     file; there is no `--conflicting`, no `--plan-total`, and no `--min`. A command that
//     accepted a floor could be pointed at a number the current labels reach, which is the
//     one thing a readiness receipt must not be able to do.
//
// It is offline on purpose: no DSN, no tenant, no model. `ready` also does not license starting
// — it says the measured conditions hold, and the frozen rows still have to be annotated by two
// people who do not see the model's verdict (manual §4 rule 3).

const readinessSchema = "rgx.goldtool.readiness/2"

// defaultPrereg is doc/301 §5.7's frozen E1 preregistration. It is tracked, so a reading taken
// from a clean checkout is reproducible; if it moves, every receipt names the path and the
// digest it actually read rather than "the plan".
const defaultPrereg = "internal/service/testdata/gold/prereg-e1-v1.json"

// The three words a condition may report. `unverified` is not a soft `fail`: it means the file
// cannot answer the question, and it blocks exactly like `fail` does.
const (
	readinessPass       = "pass"
	readinessFail       = "fail"
	readinessUnverified = "unverified"
)

// readinessPrereg is the denominator: plan_total, the per-stratum targets, and the identity of
// the file they were read from (bytes and inputs included, so the identity block has one
// source for "what was frozen").
type readinessPrereg struct {
	Path      string                 `json:"path"`
	SHA256    string                 `json:"sha256"`
	Bytes     int                    `json:"bytes"`
	SampleID  string                 `json:"sample_id,omitempty"`
	PlanTotal int                    `json:"plan_total"`
	Plan      map[string]int         `json:"plan"`
	Inputs    []readinessFrozenInput `json:"inputs"`
}

type readinessTarget struct {
	Verdict string `json:"verdict"`
	Target  int    `json:"target"`
	// Ceiling is nil when this export cannot count the stratum — which is different from a
	// ceiling of zero, and that difference is the whole reason `not_counted` exists.
	Ceiling *int   `json:"ceiling"`
	State   string `json:"state"`
	Note    string `json:"note,omitempty"`
}

type readinessA struct {
	Pool             string            `json:"pool"`
	PoolRows         int               `json:"pool_rows"`
	CandidateRows    int               `json:"candidate_rows"`
	ItemsWithCands   int               `json:"items_with_candidate_rows"`
	ItemsWithoutCand int               `json:"items_without_candidate_rows"`
	Targets          []readinessTarget `json:"targets"`
	NotCounted       []string          `json:"not_counted"`
	State            string            `json:"state"`
	Reason           string            `json:"reason"`
}

type readinessAnnotator struct {
	Name   string `json:"name"`
	Judged int    `json:"judged_rows"`
	File   string `json:"file"`
}

// readinessKappaRecipe is the arithmetic, written down. It travels with every receipt —
// including a failing one — because "κ 0.68" means nothing without it: which statistic, over
// which unit, over which classes, and what happened to missing cells. It is a description of
// frozen choices, not a set of knobs (there is no flag that changes any field here).
type readinessKappaRecipe struct {
	Statistic   string   `json:"statistic"`
	Unit        string   `json:"unit"`
	Classes     []string `json:"classes"`
	Missing     string   `json:"missing"`
	Floor       float64  `json:"floor"`
	FloorSource string   `json:"floor_source"`
}

type readinessB struct {
	Worksheet      string               `json:"worksheet"`
	WorksheetRows  int                  `json:"worksheet_rows"`
	PlanTotal      int                  `json:"plan_total"`
	Labels         []string             `json:"labels"`
	Annotators     []readinessAnnotator `json:"annotators"`
	DistinctPeople int                  `json:"distinct_annotators"`
	JudgedRows     int                  `json:"rows_judged_by_everyone"`
	MissingCells   int                  `json:"annotator_row_cells_missing"`
	// Duplicate/Extra are the two label shapes that make a census lie: a second judgement of
	// the same cell, and a row for something the sheet never asked about. Both are refused by
	// `validateLabelsAgainstWorksheet`; they are *counted* here so the receipt says how much.
	DuplicateRows int                  `json:"label_rows_duplicate"`
	ExtraRows     int                  `json:"label_rows_outside_worksheet"`
	Kappa         *float64             `json:"cohen_kappa,omitempty"`
	Method        readinessKappaRecipe `json:"kappa_method"`
	Problems      []Problem            `json:"problems,omitempty"`
	State         string               `json:"state"`
	Reason        string               `json:"reason"`
}

// Readiness is the receipt. `Ready` is derived from the three states and nothing else, so
// "all conditions hold" cannot drift from what was measured.
type Readiness struct {
	Schema   string            `json:"schema"`
	Prereg   readinessPrereg   `json:"prereg"`
	Identity readinessIdentity `json:"identity"`
	A        readinessA        `json:"a_corpus"`
	B        readinessB        `json:"b_people"`
	Ready    bool              `json:"ready"`
	// Boundary is printed with every receipt, including a ready one: a green reading here means
	// "the measurable conditions of item 30 hold", never "the gold set is good".
	Boundary string `json:"boundary"`
}

const readinessBoundary = "ready 只表示 §7 第 30 条的三条**可测**条件成立（identity / A / B）；" +
	"它不表示语料充分（A 只数得出 conflicting 的候选行上限）、不表示判得准（κ 是两人一致性，不是与真值的一致性），" +
	"也不表示可以跳过手册 §4 第 3 条（标注人只读包，不读工作表与模型 verdict）"

func runReadiness(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("readiness", flag.ContinueOnError)
	fs.SetOutput(stderr)
	poolPath := fs.String("pool", "", "pool JSONL written by `goldtool pool --out` (offline)")
	worksheetPath := fs.String("worksheet", "", "worksheet JSONL produced by `goldtool sample`")
	labelsArg := fs.String("labels", "", "comma-separated per-annotator label JSONL files (0..n)")
	preregPath := fs.String("prereg", defaultPrereg, "frozen preregistration that holds the plan and the input digests")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if trimSpace(*poolPath) == "" || trimSpace(*worksheetPath) == "" {
		fmt.Fprintln(stderr, "goldtool readiness: --pool and --worksheet are both required: A is a question about "+
			"the pool, B is a question about the worksheet, identity is a question about the two together, "+
			"and a receipt that answered only one would be the merged condition this command exists to keep apart")
		return exitUsage
	}

	prereg, err := readReadinessPrereg(*preregPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool readiness: prereg: %v\n", err)
		return exitUsage
	}
	items, err := readItems(*poolPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool readiness: %v\n", err)
		return exitUsage
	}
	ws, err := readWorksheet(*worksheetPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool readiness: worksheet: %v\n", err)
		return exitUsage
	}

	files := make([]readinessLabelFile, 0, 4)
	lines := make([]labelledLine, 0, 128)
	for _, p := range splitList(*labelsArg) {
		part, err := readLabelsAt(p)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool readiness: %v\n", err)
			return exitUsage
		}
		lines = append(lines, part...)
		// One file, one person: the identity rules below depend on knowing which file each
		// name came from, and on noticing when a file carries two of them.
		files = append(files, readinessLabelFile{Path: p, Lines: part, Names: annotatorNames(part), Rows: len(part)})
	}

	rec := Readiness{
		Schema: readinessSchema,
		Prereg: prereg,
		A:      corpusReadiness(*poolPath, items, prereg),
		B:      peopleReadiness(*worksheetPath, ws, files, lines, prereg),
	}
	// Identity needs B's row census (it reports the same "rows outside the sheet" count B
	// refuses), so it is built after B and never re-counts it.
	rec.Identity = identityReadiness(*preregPath, prereg, *poolPath, items, ws, files, rec.B.ExtraRows)
	rec.Ready = rec.Identity.State == readinessPass && rec.A.State == readinessPass && rec.B.State == readinessPass
	rec.Boundary = readinessBoundary

	reportReadiness(stdout, rec)
	switch {
	case rec.Ready:
		return exitOK
	case rec.Identity.State == readinessFail || rec.A.State == readinessFail || rec.B.State == readinessFail:
		// A gap the file can describe is an invariant: the receipt already named which condition
		// and why. Exit 1 keeps it distinct from "cannot answer at all".
		fmt.Fprintf(stderr, "goldtool readiness: not ready — identity=%s A=%s B=%s; the named condition has to "+
			"change, not the target (exit 1, blocking)\n", rec.Identity.State, rec.A.State, rec.B.State)
		return exitInvariant
	}
	fmt.Fprintf(stderr, "goldtool readiness: not ready — identity=%s A=%s B=%s: %s (exit 3, blocking)\n",
		rec.Identity.State, rec.A.State, rec.B.State, unverifiedReason(rec))
	return exitIncomplete
}

func unverifiedReason(rec Readiness) string {
	switch {
	case rec.Identity.State == readinessUnverified:
		return "identity is unverified: " + rec.Identity.Reason
	case rec.A.State == readinessUnverified:
		return "the pool cannot count " + strings.Join(rec.A.NotCounted, "/") +
			", so A is unverified rather than passed; closing it is a content scan or the plan's " +
			"documented downgrade (301 §9.1.2), not a smaller target"
	}
	return "B is unverified"
}

// readReadinessPrereg reads the frozen plan and fingerprints the file it came from. A prereg
// whose strata do not add up to plan_total is not a denominator: it is a document whose parts
// disagree, and drawing rows against it would be drawing against two different plans.
func readReadinessPrereg(path string) (readinessPrereg, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return readinessPrereg{}, err
	}
	var parsed struct {
		SampleID  string                 `json:"sample_id"`
		PlanTotal int                    `json:"plan_total"`
		Plan      map[string]int         `json:"plan"`
		Inputs    []readinessFrozenInput `json:"inputs"`
	}
	// An unreadable plan must not silently become an empty one: `plan_total 0` would then be
	// reported as "no plan", which reads like a clean refusal of a file that was fine.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return readinessPrereg{}, fmt.Errorf("%s: %v", path, err)
	}
	sum := sha256.Sum256(body)
	return readinessPrereg{
		Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: len(body),
		SampleID: parsed.SampleID, PlanTotal: parsed.PlanTotal, Plan: parsed.Plan, Inputs: parsed.Inputs,
	}, nil
}

// corpusReadiness turns the pool export into the A block. `poolMaterialOf` is reused rather
// than re-counted: the ceiling has to be the same number `pool` prints in its own receipt, or
// the two commands disagree about the material and neither is believable.
func corpusReadiness(poolPath string, items []Item, prereg readinessPrereg) readinessA {
	mat := poolMaterialOf(items)
	a := readinessA{
		Pool: poolPath, PoolRows: len(items),
		CandidateRows: mat.CandidateRows, ItemsWithCands: mat.ItemsWithCandidate,
		ItemsWithoutCand: mat.ItemsWithoutCandidate,
	}

	if prereg.PlanTotal <= 0 || len(prereg.Plan) == 0 {
		a.State, a.Reason = readinessFail, "prereg 里没有 plan：目标为空不是分母"
		return a
	}
	total := 0
	for _, target := range prereg.Plan {
		total += target
	}
	if total != prereg.PlanTotal {
		a.State = readinessFail
		a.Reason = fmt.Sprintf("prereg 的各层加起来是 %d，plan_total 是 %d：两处不一致，无法判断哪条才是分母",
			total, prereg.PlanTotal)
		return a
	}

	verdicts := make([]string, 0, len(prereg.Plan))
	for v := range prereg.Plan {
		verdicts = append(verdicts, v)
	}
	sort.Strings(verdicts)

	short := 0
	for _, v := range verdicts {
		t := readinessTarget{Verdict: v, Target: prereg.Plan[v]}
		if v == model.ReportVerdictConflicting {
			// The only stratum a pool export can count: every frozen candidate row is a row an
			// annotator *could* call conflicting. It is a ceiling, not a forecast.
			ceiling := mat.CandidateRows
			t.Ceiling = &ceiling
			if ceiling >= prereg.Plan[v] {
				t.State = readinessPass
				t.Note = "候选行数是 conflicting 的计数上限（不是预测）：够画；够不够判要标注回答"
			} else {
				t.State = readinessFail
				t.Note = "候选行数不足以画满冻结的计划"
				short++
			}
		} else {
			t.State = readinessUnverified
			t.Note = "池里没有按 verdict 分的材料：只数得出候选行与运行时意见（4 值闭集），这是内容属性"
			a.NotCounted = append(a.NotCounted, v)
		}
		a.Targets = append(a.Targets, t)
	}

	switch {
	case short > 0:
		a.State = readinessFail
		a.Reason = fmt.Sprintf("%d 个可数的层达不到冻结计划", short)
	case len(a.NotCounted) > 0:
		// Deliberately not `pass`: `outdated 12` is not "reached", it is "not countable in this
		// file", and folding it into pass is exactly how a gap becomes a number nobody measured.
		// The way out is a content-level scan, or the plan's own downgrade (301 §9.1.2) recorded
		// as a decision — both change an input, neither is this tool being lenient.
		a.State = readinessUnverified
		a.Reason = fmt.Sprintf("可数的层（conflicting）够画，但 %s 这些层这份导出数不出来，不能报 pass",
			strings.Join(a.NotCounted, "/"))
	default:
		a.State = readinessPass
		a.Reason = "冻结计划里每一层都有可数读数且达标"
	}
	return a
}

// kappaRecipe is the frozen arithmetic. `minKappa` comes from the manual (doc/operations/
// gold-labeling-manual.md §5) and 301 §9.1 — it is a constant in this package precisely so
// that no invocation can move it.
func kappaRecipe() readinessKappaRecipe {
	classes := append([]string{}, GoldVerdicts...)
	sort.Strings(classes)
	return readinessKappaRecipe{
		Statistic: "cohen_kappa_unweighted",
		Unit:      "worksheet row (item_id; candidate side would be item_id#candidate_index)",
		Classes:   classes,
		Missing: "any missing (annotator, row) cell blocks κ — no imputation, no pairwise-deletion: " +
			"a κ over the overlap is a different number from a κ over the sample",
		Floor:       minKappa,
		FloorSource: "手册 §5 与 301 §9.1 冻结的 0.70（常量，不是命令行参数）",
	}
}

// peopleReadiness turns the label files into the B block. It reports four separate facts (who,
// how much, does the worksheet match the frozen plan, what κ) because the three failure modes
// look identical from a single boolean: only one person; two people who have not finished; two
// people who finished and disagree so much that the manual is the fault.
func peopleReadiness(worksheetPath string, ws *worksheet, files []readinessLabelFile,
	lines []labelledLine, prereg readinessPrereg) readinessB {
	b := readinessB{
		Worksheet: worksheetPath, WorksheetRows: len(ws.Rows),
		PlanTotal: prereg.PlanTotal, Method: kappaRecipe(),
	}
	for _, f := range files {
		b.Labels = append(b.Labels, f.Path)
	}
	names := annotatorNames(lines)
	b.DistinctPeople = len(names)

	judgedBy := map[string]int{}
	for _, l := range lines {
		if trimSpace(l.Verdict) != "" && trimSpace(l.Annotator) != "" {
			judgedBy[l.Annotator]++
		}
	}
	fileOf := map[string]string{}
	for _, f := range files {
		for _, n := range f.Names {
			if _, seen := fileOf[n]; !seen {
				fileOf[n] = f.Path
			}
		}
	}
	for _, n := range names {
		b.Annotators = append(b.Annotators, readinessAnnotator{Name: n, Judged: judgedBy[n], File: fileOf[n]})
	}

	// Identity rules first: "one name in two files" would otherwise be counted as one person
	// and reported as a staffing problem, which is a different action than "the same name was
	// used twice".
	identityProblems := annotatorIdentityProblems(files)
	problems := append(validateLabelsAgainstWorksheet(ws, lines, worksheetPath), identityProblems...)
	b.DuplicateRows = countProblemCode(problems, "duplicate_label")
	b.ExtraRows = countProblemCode(problems, "label_for_unknown_item")
	if len(problems) > 0 {
		b.Problems = problems
		b.State = readinessFail
		if len(identityProblems) > 0 {
			b.Reason = fmt.Sprintf("标注人身份不成立（%d 处）：同一个名字出现在两份文件里，或一份文件里出现两个名字。"+
				"按名字去重时前者读成「一个人」、后者读成「两个人」，两种都不是事实", len(identityProblems))
		} else {
			b.Reason = fmt.Sprintf("标签文件有 %d 处问题（逐条在 problems 里）：行对不上工作表时，"+
				"κ 是一个不存在的样本上的数", len(problems))
		}
		return b
	}

	_, labelledByAll, missing := coverage(ws, lines, names)
	b.JudgedRows, b.MissingCells = labelledByAll, missing

	// The worksheet has to be the one the prereg froze. A candidate-side worksheet or a
	// differently sampled one makes every count below a count about another sample — and the
	// numbers would still look healthy.
	if prereg.PlanTotal > 0 && len(ws.Rows) != prereg.PlanTotal {
		b.State = readinessFail
		b.Reason = fmt.Sprintf("工作表 %d 行，冻结计划 plan_total %d：不是预注册冻结的那份样本",
			len(ws.Rows), prereg.PlanTotal)
		return b
	}

	judged := make([]Label, 0, len(lines))
	for _, l := range lines {
		if trimSpace(l.Verdict) == "" {
			continue
		}
		judged = append(judged, l.Label)
	}

	switch {
	case len(lines) == 0:
		b.State = readinessFail
		b.Reason = "还没有任何一份标注文件：B 不是「未测」，是「没人开始」"
	case len(names) < 2:
		started := 0
		for _, n := range judgedBy {
			started += n
		}
		b.State = readinessFail
		// Both facts are true at once today and they mean different things to a planner: "there is
		// no second person" is a staffing action, "nobody has judged a row" is a calendar one.
		if started == 0 {
			b.Reason = fmt.Sprintf("只有一个标注人（%s），且 %d 行一行都还没判：κ 需要两个独立的人，"+
				"`adjudicate --stub` 也会给 same_annotator_twice", strings.Join(names, ","), len(ws.Rows))
		} else {
			b.Reason = fmt.Sprintf("只有一个标注人（%s，已判 %d 行）：κ 需要两个独立的人，"+
				"`adjudicate --stub` 也会给 same_annotator_twice", strings.Join(names, ","), started)
		}
	case missing > 0:
		b.State = readinessFail
		b.Reason = fmt.Sprintf("未完成：%d/%d 行两人都判过，%d 个 (标注人,行) 格仍空——这不是低 κ，是没做完",
			b.JudgedRows, len(ws.Rows), missing)
	default:
		pair, err := agreementRows(ws, judged)
		if err != nil {
			b.State = readinessFail
			b.Reason = fmt.Sprintf("κ 算不出来：%v", err)
			return b
		}
		kappa := pair.Kappa
		b.Kappa = &kappa
		if pair.Pairs != len(ws.Rows) {
			b.State = readinessFail
			b.Reason = fmt.Sprintf("κ 只配上了 %d/%d 行：样本被截断过", pair.Pairs, len(ws.Rows))
			return b
		}
		if kappa < minKappa {
			b.State = readinessFail
			// Two outcomes, two instructions: below 0.60 the plan says fix the manual, not the
			// model (301 §9.1) — a low κ is a definition problem, not an annotator problem.
			if kappa < 0.60 {
				b.Reason = fmt.Sprintf("κ %.3f < 0.60：先改手册，不要动模型（301 §9.1）", kappa)
			} else {
				b.Reason = fmt.Sprintf("κ %.3f 低于 %.2f 的地板（%d 对）", kappa, minKappa, pair.Pairs)
			}
			return b
		}
		b.State = readinessPass
		b.Reason = fmt.Sprintf("%d 人、%d/%d 行两人都判过、κ %.3f ≥ %.2f",
			len(names), b.JudgedRows, len(ws.Rows), kappa, minKappa)
	}
	return b
}

func countProblemCode(problems []Problem, code string) int {
	n := 0
	for _, p := range problems {
		if p.Code == code {
			n++
		}
	}
	return n
}

func reportReadiness(stdout *os.File, rec Readiness) {
	fmt.Fprintf(stdout, "goldtool readiness: identity=%s A=%s B=%s ready=%v\n",
		rec.Identity.State, rec.A.State, rec.B.State, rec.Ready)
	fmt.Fprintf(stdout, "  identity: %s\n", rec.Identity.Reason)
	for _, in := range rec.Identity.Inputs {
		fmt.Fprintf(stdout, "    %-7s %-42s %s  rows %d  %s\n",
			in.Role, in.Path, shortDigest(in.SHA256), in.Rows, in.State)
	}
	fmt.Fprintf(stdout, "  A 语料: pool %d 行, 候选 %d 行 / %d 条 item, 无候选 %d 条; %s\n",
		rec.A.PoolRows, rec.A.CandidateRows, rec.A.ItemsWithCands, rec.A.ItemsWithoutCand, rec.A.Reason)
	for _, t := range rec.A.Targets {
		ceiling := "null"
		if t.Ceiling != nil {
			ceiling = fmt.Sprintf("%d", *t.Ceiling)
		}
		fmt.Fprintf(stdout, "    %-20s target %d ceiling %s  %s\n", t.Verdict, t.Target, ceiling, t.State)
	}
	fmt.Fprintf(stdout, "  B 人员: %s\n", rec.B.Reason)
	for _, a := range rec.B.Annotators {
		fmt.Fprintf(stdout, "    %-12s %d 行  (%s)\n", a.Name, a.Judged, a.File)
	}
	fmt.Fprintf(stdout, "  κ 口径: %s, %d 类, 地板 %.2f（%s）\n",
		rec.B.Method.Statistic, len(rec.B.Method.Classes), rec.B.Method.Floor, rec.B.Method.FloorSource)
	fmt.Fprintf(stdout, "  计划: %s plan_total %d sha256 %s\n",
		rec.Prereg.Path, rec.Prereg.PlanTotal, shortDigest(rec.Prereg.SHA256))
	printJSON(stdout, rec)
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

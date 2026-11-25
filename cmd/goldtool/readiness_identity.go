package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The identity block answers a question no single digest can: **are these files the same
// batch?** `readiness`'s first version fingerprinted the prereg and stopped there. That
// proves the *plan* has not been edited since it was frozen; it does not prove the pool the
// command read is the pool the plan was frozen against, and it says nothing at all about the
// worksheet. Every file can be individually well-formed and individually "the right kind of
// file" while the combination is a category error — a 120-row worksheet drawn from last
// week's pool, a pool re-exported after a re-freeze, an annotation file produced against a
// sheet that has since been redrawn. None of those would change any count in the A or B
// blocks; all of them invalidate the reading.
//
// So the receipt carries a chain:
//
//	H(prereg) + H(pool) + H(worksheet) + H(labels…)  and the relations between them
//
// The hashes come from the frozen plan itself (`prereg.inputs[]`, written by `goldtool
// prereg --freeze`), not from a second registry — one source, and the same source the
// evaluator will later diff against. Two relations are checked by *keys* rather than by
// digest, because a digest cannot express them:
//
//	worksheet item_ids ⊆ pool item_ids     (the sheet was drawn from this pool)
//	labels keys ⊆ worksheet keys           (counted in B, reported here)
//
// What this block deliberately does not claim: that the labels belong to the worksheet by
// content. They cannot — labels carry no digest of the sheet they were produced against, and
// adding one now would not help the 120 rows already written. The binding is by key plus the
// row census in B, and the boundary sentence says so.
const (
	// identityMatch: the digest in the frozen plan equals the file on disk.
	identityMatch = "match"
	// identityMismatch: they differ. This is not a warning; the reading is about another batch.
	identityMismatch = "mismatch"
	// identityNotRecorded: the plan does not name this input at all, so "same batch" is
	// unprovable rather than false.
	identityNotRecorded = "not_recorded"
	// identityByDesign: an annotation file, produced *after* the freeze. There is nothing to
	// compare it to, and inventing a comparison would be the kind of check that always passes.
	identityByDesign = "not_recorded_by_design"
)

// readinessFrozenInput is one `inputs[]` row of the frozen prereg: what was hashed, at freeze
// time, under which role.
type readinessFrozenInput struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Items  int    `json:"items"`
}

// readinessInput is one file as this run saw it, next to what the plan froze.
type readinessInput struct {
	// Role is the plan's vocabulary: `pool`, `sample`, `labels`.
	Role string `json:"role"`
	Path string `json:"path"`
	// SHA256/Bytes are what this run read; FrozenSHA256/FrozenItems are what the plan says.
	// Keeping both in one row is the point: a reader should never have to hold two receipts
	// side by side to see that they disagree.
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
	Rows         int    `json:"rows"`
	FrozenSHA256 string `json:"frozen_sha256,omitempty"`
	FrozenItems  int    `json:"frozen_rows,omitempty"`
	State        string `json:"state"`
	Note         string `json:"note,omitempty"`
	// Seal is present only on `labels` rows: the companion `labels --check` writes next to a
	// file it found clean (doc/304 E134). Nested rather than flattened so a seal disagreement
	// cannot be mistaken for the label file's own digest disagreeing with anything.
	Seal *readinessLabelSeal `json:"seal,omitempty"`
}

// readinessLabelSeal is a label file's self-description read back: which bytes, against which
// worksheet, by which names. `State` is the verdict on the *pairing*, not on the file.
type readinessLabelSeal struct {
	Path            string   `json:"path"`
	Present         bool     `json:"present"`
	LabelsSHA256    string   `json:"labels_sha256,omitempty"`
	WorksheetSHA256 string   `json:"worksheet_sha256,omitempty"`
	Annotators      []string `json:"annotators,omitempty"`
	State           string   `json:"state"`
	Note            string   `json:"note,omitempty"`
}

// readinessDigestFile is the companion `*.digest.json` that `goldtool prereg --digest`
// writes. Its presence is what makes "the plan was not edited after freeze" checkable; its
// absence is not a pass.
type readinessDigestFile struct {
	Path         string `json:"path"`
	Present      bool   `json:"present"`
	PreregSHA256 string `json:"recorded_prereg_sha256,omitempty"`
	Bytes        int    `json:"recorded_bytes,omitempty"`
	State        string `json:"state"`
	Note         string `json:"note,omitempty"`
}

// readinessIdentity is the chain block. `State` aggregates only the things that *can* be
// decided here; per-input states stay in the rows so a mismatch names the file.
type readinessIdentity struct {
	PreregDigest readinessDigestFile `json:"prereg_digest"`
	Inputs       []readinessInput    `json:"inputs"`
	// Keys, not digests: a sheet can be a byte-perfect copy of nothing in the plan and still
	// carry item ids the pool never had.
	WorksheetItemsOutsidePool  int    `json:"worksheet_items_outside_pool"`
	LabelsRowsOutsideWorksheet int    `json:"labels_rows_outside_worksheet"`
	State                      string `json:"state"`
	Reason                     string `json:"reason"`
}

// identityReadiness builds the chain. `rowsOutside` is counted by B (it is the same
// `label_for_unknown_item` the label check reports); passing the number in keeps one
// implementation of "is this row in the sheet".
func identityReadiness(preregPath string, prereg readinessPrereg, poolPath string, items []Item,
	ws *worksheet, files []readinessLabelFile, rowsOutside int) readinessIdentity {
	id := readinessIdentity{LabelsRowsOutsideWorksheet: rowsOutside}

	// 1. The plan's own companion digest. `prereg --digest` writes it next to the file; when
	// it is missing, the only thing the prereg's sha256 proves is that the bytes on disk hash
	// to something — not that they are the bytes that were frozen.
	id.PreregDigest = readinessPreregDigest(preregPath, prereg)
	if id.PreregDigest.State == identityMatch {
		id.Inputs = append(id.Inputs, readinessInput{
			Role: "prereg", Path: preregPath, SHA256: prereg.SHA256, Bytes: prereg.Bytes, Rows: 1,
			FrozenSHA256: id.PreregDigest.PreregSHA256, State: identityMatch,
			Note: "计划自身：digest 伴随件与文件一致",
		})
	}

	// 2. pool and worksheet against the plan's recorded inputs. Role, not path: the plan was
	// frozen with `tmp/gold/...` paths, and a clean-checkout reader may hold the same bytes
	// under another path.
	frozen := map[string]readinessFrozenInput{}
	for _, in := range prereg.Inputs {
		frozen[in.Role] = in
	}
	id.Inputs = append(id.Inputs, compareInput("pool", poolPath, len(items), frozen["pool"]))
	sampleIn := compareInput("sample", ws.Path, len(ws.Rows), frozen["sample"])
	id.Inputs = append(id.Inputs, sampleIn)

	// 3. Annotation files: produced after the freeze, so the plan cannot have their digest.
	// Since E134 a file may carry its own `<labels>.seal.json` — the pairing `labels --check`
	// recorded when it found the file clean — and then the binding is decidable instead of
	// asserted. No seal is still `not_recorded_by_design`: absence is the shape of every file
	// written before this rule, and calling it a failure would retroactively condemn them.
	for _, f := range files {
		id.Inputs = append(id.Inputs, compareLabelInput(f.Path, f.Rows, f.Names, sampleIn.SHA256))
	}

	// 4. The one relation no digest can carry: was this sheet drawn from this pool?
	poolIDs := map[string]bool{}
	for _, it := range items {
		poolIDs[it.ItemID] = true
	}
	for _, r := range ws.Rows {
		if !poolIDs[r.ItemID] {
			id.WorksheetItemsOutsidePool++

		}
	}

	// 5. Aggregate. A mismatch is an invariant (the reading is about another batch); an
	// unrecorded or absent fingerprint is `unverified` — the same word, and the same exit
	// code, the rest of this command uses for "this file cannot answer".
	var mismatch, unproven, labelMismatch []string
	sealed, unsealed := 0, 0
	for _, in := range id.Inputs {
		if in.Role == "labels" {
			switch in.State {
			case identityMatch:
				sealed++
			case identityByDesign:
				unsealed++
			}
		}
		switch {
		case in.State == identityMismatch && in.Role == "labels":
			// A seal disagreement is not a disagreement with the *plan*: the plan has no labels
			// row to disagree with. Keeping the two lists apart is what lets the sentence name
			// the right reference — "another worksheet" is a different repair from "another batch".
			labelMismatch = append(labelMismatch, in.Path)
		case in.State == identityMismatch:
			mismatch = append(mismatch, fmt.Sprintf("%s(%s)", in.Role, in.Path))
		case in.State == identityNotRecorded:
			unproven = append(unproven, in.Role)
		}
	}
	// The plan's own digest is decided separately, because its row is only appended when it
	// matched: a tampered prereg must fail the chain even though nothing in `Inputs` differs.
	switch id.PreregDigest.State {
	case identityMismatch:
		mismatch = append(mismatch, "prereg("+preregPath+")")
	case identityNotRecorded:
		unproven = append(unproven, "prereg.digest")
	}

	switch {
	case len(mismatch) > 0 || len(labelMismatch) > 0 || id.WorksheetItemsOutsidePool > 0:
		id.State = readinessFail
		var parts []string
		if len(mismatch) > 0 || id.WorksheetItemsOutsidePool > 0 {
			parts = append(parts, fmt.Sprintf("%s：这些文件与冻结计划不是同一批（%d 条工作表 item 不在 pool 里）",
				strings.Join(mismatch, "、"), id.WorksheetItemsOutsidePool))
		}
		if len(labelMismatch) > 0 {
			parts = append(parts, fmt.Sprintf("标注文件与本次工作表对不上：%s（行可能是对的，但它们不是对这张表做的；"+
				"处理方式见对应行的 seal.note）", strings.Join(labelMismatch, "、")))
		}
		id.Reason = strings.Join(parts, "；")
	case len(unproven) > 0:
		id.State = readinessUnverified
		id.Reason = fmt.Sprintf("%s 没有可比的指纹：能证明文件现在长什么样，不能证明它与冻结时是同一批",
			strings.Join(unproven, "/"))
	default:
		id.State = readinessPass
		id.Reason = "prereg、pool、工作表三者的指纹与冻结记录一致，工作表 item 全部出自该 pool"
		switch {
		case unsealed == 0:
			id.Reason += fmt.Sprintf("；%d 份标注带 seal，与本次工作表指纹一致", sealed)
		case sealed == 0:
			id.Reason += "；标注文件按设计无冻结指纹，绑定靠键与行普查"
		default:
			id.Reason += fmt.Sprintf("；%d 份标注带 seal 与本次工作表指纹一致，%d 份是 seal 规则之前写的"+
				"（按设计无冻结指纹，绑定靠键与行普查）", sealed, unsealed)
		}
	}
	return id
}

// compareLabelInput decides what can be decided about one annotation file. Two questions are
// separate and both matter:
//
//	do these bytes equal the bytes the seal describes?   (the file did not change after checking)
//	is the sheet in the seal the sheet this run read?    (the labels were checked against *this* sheet)
//
// A file whose rows were checked against another worksheet is not a malformed file: every row
// may be perfect, and it still cannot be read next to this sheet. That is the same category
// error E132 was written about, one level down.
func compareLabelInput(path string, rows int, names []string, worksheetSHA256 string) readinessInput {
	in := readinessInput{Role: "labels", Path: path, Rows: rows}
	raw, err := os.ReadFile(path)
	if err != nil {
		in.State = identityMismatch
		in.Note = fmt.Sprintf("读不到：%v", err)
		return in
	}
	sum := sha256.Sum256(raw)
	in.SHA256, in.Bytes = hex.EncodeToString(sum[:]), len(raw)

	seal := &readinessLabelSeal{Path: sealPathFor(path)}
	in.Seal = seal
	sraw, serr := os.ReadFile(seal.Path)
	if serr != nil {
		seal.State = identityByDesign
		seal.Note = "旁边没有 seal：这份标注是在 seal 规则之前写的，或由另一个工具产出；绑定仍只靠行键与 B 的行普查"
		in.State, in.Note = identityByDesign, seal.Note
		return in
	}
	seal.Present = true
	var parsed labelsSeal
	if err := json.Unmarshal(sraw, &parsed); err != nil {
		seal.State = identityNotRecorded
		seal.Note = fmt.Sprintf("%s 读不成 JSON（%v）：它不能当参照", seal.Path, err)
		in.State, in.Note = identityNotRecorded, seal.Note
		return in
	}
	seal.LabelsSHA256, seal.WorksheetSHA256 = parsed.LabelsSHA256, parsed.WorksheetSHA256
	seal.Annotators = parsed.Annotators

	switch {
	case parsed.LabelsSHA256 == "" || parsed.WorksheetSHA256 == "":
		seal.State = identityNotRecorded
		seal.Note = fmt.Sprintf("%s 缺 labels_sha256 或 worksheet_sha256：一份说不全的 seal 不能当参照", seal.Path)
	case parsed.LabelsSHA256 != in.SHA256:
		seal.State = identityMismatch
		seal.Note = fmt.Sprintf("seal 之后文件改过（seal 记 %s，现在是 %s）：重新跑 `goldtool labels --check` 再读，"+
			"否则这份 seal 描述的不是这些行", short12(parsed.LabelsSHA256), short12(in.SHA256))
	case parsed.WorksheetSHA256 != worksheetSHA256:
		seal.State = identityMismatch
		seal.Note = fmt.Sprintf("这些标注是对另一份工作表做的（seal 记 %s，本次读的是 %s）：行键可能都对，"+
			"但它回答的是另一张表的问题", short12(parsed.WorksheetSHA256), short12(worksheetSHA256))
	case !sameNames(parsed.Annotators, names):
		seal.State = identityMismatch
		seal.Note = fmt.Sprintf("seal 记的标注者是 %v，文件里是 %v：一张 seal 描述的是它写时的那些行",
			parsed.Annotators, names)
	default:
		seal.State = identityMatch
		seal.Note = "seal 与文件、工作表、标注者三者一致"
	}
	in.State, in.Note = seal.State, seal.Note
	return in
}

// sameNames compares two name lists as sets. Order is an artifact of the file's line order and
// must not decide whether two descriptions agree.
func sameNames(a, b []string) bool {
	x, y := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// short12 is the digest width a human compares by eye in a message; the full 64 characters
// stay in the receipt's fields.
func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// compareInput hashes one file and compares it to the plan's row for that role. A missing
// file is a mismatch, not an unverified: the command read it a moment ago to produce the
// counts above, so "gone now" means the tree changed under the reading.
func compareInput(role, path string, rows int, want readinessFrozenInput) readinessInput {
	in := readinessInput{Role: role, Path: path, Rows: rows, FrozenSHA256: want.SHA256, FrozenItems: want.Items}
	raw, err := os.ReadFile(path)
	if err != nil {
		in.State = identityMismatch
		in.Note = fmt.Sprintf("读不到：%v", err)
		return in
	}
	sum := sha256.Sum256(raw)
	in.SHA256, in.Bytes = hex.EncodeToString(sum[:]), len(raw)

	switch {
	case role == "labels":
		// No frozen row by design (see the const's comment): a comparison here would have to
		// invent a reference, and a check with an invented reference is a check that passes.
		in.State = identityByDesign
		in.Note = "标注产出在冻结之后，计划里没有它的指纹；它与工作表的绑定靠行键与 B 的行普查"
	case want.SHA256 == "":
		in.State = identityNotRecorded
		in.Note = "冻结计划里没有这个 role 的 inputs 行：这一项无法核对，不能报 pass"
	case want.SHA256 != in.SHA256:
		in.State = identityMismatch
		in.Note = "与冻结计划记录的指纹不同：这是另一批文件"
	default:
		in.State = identityMatch
		if want.Items > 0 && want.Items != rows {
			in.State = identityMismatch
			in.Note = fmt.Sprintf("指纹相同但行数 %d ≠ 计划记的 %d", rows, want.Items)
		}
	}
	return in
}

// checkPreregDigest reads `<prereg>.digest.json` and compares its recorded `prereg_sha256`
// with the bytes on disk. Absence is `not_recorded` — the tool says so instead of assuming
// the file is the frozen one because it is called "prereg-e1-v1.json".
func readinessPreregDigest(preregPath string, prereg readinessPrereg) readinessDigestFile {
	d := readinessDigestFile{Path: readinessDigestPath(preregPath)}
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		d.State = identityNotRecorded
		d.Note = fmt.Sprintf("没有伴随 digest 件（%v）：只能证明文件当前内容的指纹，不能证明它与冻结时一致", err)
		return d
	}
	d.Present = true
	var parsed struct {
		SHA256 string `json:"prereg_sha256"`
		Bytes  int    `json:"bytes"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		d.State = identityMismatch
		d.Note = fmt.Sprintf("%s 读不成 JSON：%v", d.Path, err)
		return d
	}
	d.PreregSHA256, d.Bytes = parsed.SHA256, parsed.Bytes
	switch {
	case parsed.SHA256 == "":
		d.State = identityNotRecorded
		d.Note = "digest 件里没有 prereg_sha256：这份伴随件不能当参照"
	case parsed.SHA256 != prereg.SHA256:
		d.State = identityMismatch
		d.Note = "prereg 在冻结后被改过，digest 件仍指着旧内容"
	default:
		d.State = identityMatch
		d.Note = "冻结时的指纹"
	}
	return d
}

func readinessDigestPath(preregPath string) string {
	return strings.TrimSuffix(preregPath, ".json") + ".digest.json"
}

// labelFileCensus reports the per-file facts the identity block and the annotator rules need:
// how many rows, which names, and the digest. Rows are counted from what was parsed, so an
// empty file is 0 rows rather than "file exists".
type readinessLabelFile struct {
	Path  string
	Lines []labelledLine
	Names []string
	Rows  int
}

// annotatorIdentityProblems refuses the two shapes in which "two annotators" is only two file
// names. Both are invisible to a count: the first reads as one person, the second as two
// people who happen to agree with themselves.
func annotatorIdentityProblems(files []readinessLabelFile) []Problem {
	var problems []Problem
	nameFiles := map[string][]string{}
	for _, f := range files {
		if len(f.Names) > 1 {
			problems = append(problems, Problem{"label_file_mixes_annotators",
				fmt.Sprintf("%s carries %d names %v: one file is one person's judgement, or the merge "+
					"has already happened and nobody can say which row is whose", f.Path, len(f.Names), f.Names)})
		}
		for _, n := range f.Names {
			nameFiles[n] = append(nameFiles[n], f.Path)
		}
	}
	names := make([]string, 0, len(nameFiles))
	for n := range nameFiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if ps := nameFiles[n]; len(ps) > 1 {
			problems = append(problems, Problem{"annotator_name_in_two_files",
				fmt.Sprintf("%q appears in %d files %v: identity comes from the row's annotator field, "+
					"not from the file name, so this is one person — or two people who signed the same "+
					"name, which κ cannot tell apart", n, len(ps), ps)})
		}
	}
	return problems
}

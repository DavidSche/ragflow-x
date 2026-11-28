package model

// ScenarioID: SC-DOC-003
//
// doc/specification/16 §4 freezes the timestamp vocabulary (doc/319 §5 S-3). A table in a
// specification is worth exactly as much as its distance from the code: written once by
// hand, it drifts the first time a field is renamed, and nobody notices because prose
// cannot fail.
//
// So the table is checked in both directions:
//   * every row names a field that exists, with the nullability the Go type actually has
//     (`*time.Time` ⇔ 可空) — the row cannot describe a field that is gone or has moved;
//   * every timestamp column on the covered structs is *either* in the table or on the
//     generic-bookkeeping list (`created_at`/`updated_at`/`deleted_at`) — an unlisted
//     timestamp is an un-frozen fact, and the completeness direction is the one people
//     forget.
//
// What this test deliberately does NOT do: judge the prose. "语义 written correctly" is a
// review question; "row and struct agree" is a machine question. Keeping them apart is why
// this gate can stay green for years instead of being disabled for noise.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// timestampVocabularySpec is the single source of the table. The path is fixed on purpose:
// a test that accepts "any file that looks like the table" would pass while the frozen
// table sits in an unreferenced file.
const timestampVocabularySpec = "doc/specification/16_判定、审批与发布的责任边界规范.md"

// timestampVocabularyTimeKinds is the closed set from §4.1. A new kind is a vocabulary
// decision, not a local convenience, so it has to be added here and in the spec together.
var timestampVocabularyTimeKinds = map[string]bool{
	"事件": true,
	"系统": true,
	"外部": true,
	"期限": true,
	"保留": true,
}

// timestampVocabularyGeneric lists the bookkeeping columns that §4.2 declares out of scope.
var timestampVocabularyGeneric = map[string]bool{
	"created_at": true,
	"updated_at": true,
	"deleted_at": true,
}

// timestampVocabularyStructs are the struct types the freeze covers: the report surface
// (evidence → assurance → revision) and the approval surface, because S-1's three objects
// are carried by exactly these rows.
func timestampVocabularyStructs() map[string]reflect.Type {
	return map[string]reflect.Type{
		"Report":                 reflect.TypeOf(Report{}),
		"ReportRevision":         reflect.TypeOf(ReportRevision{}),
		"ReportJob":              reflect.TypeOf(ReportJob{}),
		"ReportEvidenceSnapshot": reflect.TypeOf(ReportEvidenceSnapshot{}),
		"ReportAssuranceRun":     reflect.TypeOf(ReportAssuranceRun{}),
		"ReportClaimEvaluation":  reflect.TypeOf(ReportClaimEvaluation{}),
		"ReportReviewFinding":    reflect.TypeOf(ReportReviewFinding{}),
		"Approval":               reflect.TypeOf(Approval{}),
		"ApprovalStep":           reflect.TypeOf(ApprovalStep{}),
		"ApprovalStepActor":      reflect.TypeOf(ApprovalStepActor{}),
	}
}

var (
	timestampRowPattern = regexp.MustCompile("^\\|\\s*`([A-Za-z]+)\\.([A-Za-z]+)`\\s*\\|")
	gormColumnPattern   = regexp.MustCompile(`column:([a-z_]+)`)
)

// timestampVocabularyRow is one parsed table line. `raw` is kept so a failure can quote the
// sentence a reviewer wrote rather than an index into an array.
type timestampVocabularyRow struct {
	Struct     string
	Field      string
	Column     string
	Fact       string
	TimeKind   string
	Writer     string
	Nullable   bool
	NotThat    string
	raw        string
	lineNumber int
}

func TestTimestampVocabularyMatchesTheModels(t *testing.T) {
	root, err := repoRootFromModel(t)
	if err != nil {
		t.Skipf("module root not found: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(timestampVocabularySpec)))
	if err != nil {
		t.Fatalf("read %s: %v", timestampVocabularySpec, err)
	}
	rows := parseTimestampVocabulary(t, string(raw))
	if len(rows) == 0 {
		t.Fatalf("%s has no timestamp table rows; the freeze is gone or the table changed shape",
			timestampVocabularySpec)
	}

	structs := timestampVocabularyStructs()

	// Direction 1: every row points at a real field with the declared nullability.
	seen := map[string]timestampVocabularyRow{}
	for _, row := range rows {
		typ, ok := structs[row.Struct]
		if !ok {
			t.Fatalf("line %d names struct %q, which the freeze does not cover (covered: %v)",
				row.lineNumber, row.Struct, sortedStructNames(structs))
		}
		field, ok := typ.FieldByName(row.Field)
		if !ok {
			t.Fatalf("line %d names %s.%s, but that field does not exist",
				row.lineNumber, row.Struct, row.Field)
		}
		pointer, isTime := timestampFieldShape(field.Type)
		if !isTime {
			t.Fatalf("line %d names %s.%s, whose type is %s — not a timestamp",
				row.lineNumber, row.Struct, row.Field, field.Type)
		}
		if pointer != row.Nullable {
			t.Fatalf("line %d says %s.%s is %s, but the Go type is %s (%s)\n  row: %s",
				row.lineNumber, row.Struct, row.Field, nullableWord(row.Nullable),
				field.Type, nullableWord(pointer), row.raw)
		}
		if !timestampVocabularyTimeKinds[row.TimeKind] {
			t.Fatalf("line %d gives %s.%s the time kind %q, which is not in the closed set",
				row.lineNumber, row.Struct, row.Field, row.TimeKind)
		}
		if row.Fact == "" || row.Writer == "" || row.NotThat == "" {
			t.Fatalf("line %d leaves a column empty for %s.%s: the point of the table is the "+
				"distinction, and an empty cell is a distinction nobody wrote down\n  row: %s",
				row.lineNumber, row.Struct, row.Field, row.raw)
		}
		key := row.Struct + "." + row.Field
		if previous, dup := seen[key]; dup {
			t.Fatalf("%s appears twice (lines %d and %d): one fact, one row",
				key, previous.lineNumber, row.lineNumber)
		}
		seen[key] = row
	}

	// Direction 2: no timestamp column is left un-frozen. This is the half that catches
	// "someone added a field and moved on".
	listed := map[string]bool{}
	for _, row := range rows {
		listed[row.Struct+"."+row.Field] = true
	}
	var missing []string
	for _, name := range sortedStructNames(structs) {
		typ := structs[name]
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if _, isTime := timestampFieldShape(field.Type); !isTime {
				continue
			}
			column := ""
			if match := gormColumnPattern.FindStringSubmatch(field.Tag.Get("gorm")); len(match) == 2 {
				column = match[1]
			}
			if timestampVocabularyGeneric[column] {
				continue
			}
			if !listed[name+"."+field.Name] {
				missing = append(missing, name+"."+field.Name+" ("+column+")")
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("these timestamp columns are not in %s §4.2:\n  %s\n"+
			"An unlisted timestamp is a fact whose 时间种类 and 不是 have never been written "+
			"down — add the row (and decide which of the two facts it is).",
			timestampVocabularySpec, strings.Join(missing, "\n  "))
	}
}

// TestTimestampVocabularyDistinguishesTheSameColumnNameAcrossStructs pins the one pair the
// table exists for: `acted_at` on a step is nullable (the step may not have been reached)
// while `acted_at` on an actor row is not (the row only exists after acting). If a future
// change makes them agree, the "同名不同可空性" sentence in §4.2 becomes wrong, and a reader
// who trusts it will mis-read a pending step as un-acted.
func TestTimestampVocabularyDistinguishesTheSameColumnNameAcrossStructs(t *testing.T) {
	stepField, ok := reflect.TypeOf(ApprovalStep{}).FieldByName("ActedAt")
	if !ok {
		t.Fatal("ApprovalStep.ActedAt is gone; §4.2's 同名不同可空性 row needs rewriting, not deleting")
	}
	actorField, ok := reflect.TypeOf(ApprovalStepActor{}).FieldByName("ActedAt")
	if !ok {
		t.Fatal("ApprovalStepActor.ActedAt is gone; §4.2's 同名不同可空性 row needs rewriting")
	}
	stepPointer, _ := timestampFieldShape(stepField.Type)
	actorPointer, _ := timestampFieldShape(actorField.Type)
	if !stepPointer {
		t.Fatalf("ApprovalStep.ActedAt became %s: §4.2 says a step may not have been reached yet, "+
			"and a non-nullable timestamp would have to encode \"not yet\" as a zero value",
			stepField.Type)
	}
	if actorPointer {
		t.Fatalf("ApprovalStepActor.ActedAt became %s: the row is created by the act, so a nullable "+
			"clock would let an approval record exist without a time", actorField.Type)
	}
}

func parseTimestampVocabulary(t *testing.T, doc string) []timestampVocabularyRow {
	t.Helper()
	// Only §4.2 is the vocabulary table. §4.3 (zero-value semantics) also starts rows with
	// `Struct.Field`, and reading the two as one table would make the shape check fire on the
	// wrong section — the same "two tables, one parser" mistake this freeze is about.
	body, startLine := sectionBody(doc, "### 4.2")
	if body == "" {
		t.Fatalf("%s no longer has a \"### 4.2\" section; the vocabulary table moved and this "+
			"gate would silently check nothing", timestampVocabularySpec)
	}
	var rows []timestampVocabularyRow
	for offset, line := range strings.Split(body, "\n") {
		lineNumber := startLine + offset
		match := timestampRowPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) != 6 {
			t.Fatalf("line %d: a timestamp row must have 6 cells (字段/事实/时间种类/写入者/可空/不是), got %d\n  %s",
				lineNumber, len(cells), line)
		}
		nullable := false
		switch cells[4] {
		case "可空":
			nullable = true
		case "必填":
			nullable = false
		default:
			t.Fatalf("line %d: the 可空 cell must read 可空 or 必填, got %q", lineNumber, cells[4])
		}
		rows = append(rows, timestampVocabularyRow{
			Struct: match[1], Field: match[2],
			Fact: cells[1], TimeKind: cells[2], Writer: cells[3],
			Nullable: nullable, NotThat: cells[5],
			raw: line, lineNumber: lineNumber,
		})
	}
	return rows
}

// splitTableRow splits on unescaped pipes and drops the leading/trailing empties a markdown
// row carries. Cells are trimmed so the table stays readable in source form.
// sectionBody returns the lines between `heading` and the next heading of the same or a
// higher level, plus the 1-based line number the block starts on.
func sectionBody(doc, heading string) (string, int) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, heading) {
			continue
		}
		depth := strings.Count(heading, "#")
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if !strings.HasPrefix(lines[j], "### ") && !strings.HasPrefix(lines[j], "## ") {
				continue
			}
			if strings.Count(strings.Fields(lines[j])[0], "#") <= depth {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), i + 2
	}
	return "", 0
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func timestampFieldShape(typ reflect.Type) (pointer bool, isTimestamp bool) {
	if typ.Kind() == reflect.Ptr {
		if typ.Elem() == reflect.TypeOf(time.Time{}) {
			return true, true
		}
		return false, false
	}
	return false, typ == reflect.TypeOf(time.Time{})
}

func nullableWord(nullable bool) string {
	if nullable {
		return "可空"
	}
	return "必填"
}

func sortedStructNames(structs map[string]reflect.Type) []string {
	names := make([]string, 0, len(structs))
	for name := range structs {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// repoRootFromModel walks up from the test's working directory (internal/model) to the
// module root. Doing it here instead of importing internal/engdocs keeps the model package
// free of a dependency that only a test needs.
func repoRootFromModel(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

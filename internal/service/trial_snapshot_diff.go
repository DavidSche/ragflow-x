package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// TrialSnapshotChange is one field that differs between the snapshot a run
// executed under and the configuration the assistant exposes now.
type TrialSnapshotChange struct {
	Field string `json:"field"`
	Run   string `json:"run"`
	Now   string `json:"now"`
}

// TrialSnapshotDiff is the answer to the v0.4.0 exit question "does the run
// snapshot equal the assistant configuration?" (doc 227 §7 G6, doc 228 附录 C).
// An empty change list is the only acceptable outcome for a run whose quality
// card is shown to a user; anything else means the numbers describe a
// configuration the assistant no longer has.
type TrialSnapshotDiff struct {
	Identical bool                  `json:"identical"`
	Changes   []TrialSnapshotChange `json:"changes"`
}

// trialSnapshotDiff compares two snapshots field by field.
//
// The comparison is reflection-based on purpose: a hand-written field list would
// silently stop covering a field added later, which is exactly how the
// "scenario_template_id came back empty" class of bug survives a spot-check.
func trialSnapshotDiff(run, now TrialAuthoringSnapshot) TrialSnapshotDiff {
	runValues := trialSnapshotFieldValues(run)
	nowValues := trialSnapshotFieldValues(now)

	fields := make([]string, 0, len(runValues))
	for name := range runValues {
		fields = append(fields, name)
	}
	sort.Strings(fields)

	diff := TrialSnapshotDiff{Changes: []TrialSnapshotChange{}}
	for _, name := range fields {
		if runValues[name] == nowValues[name] {
			continue
		}
		diff.Changes = append(diff.Changes, TrialSnapshotChange{
			Field: name, Run: runValues[name], Now: nowValues[name],
		})
	}
	diff.Identical = len(diff.Changes) == 0
	return diff
}

// trialSnapshotFieldValues flattens a snapshot into json-tag -> canonical value.
//
// Canonicalisation rules that carry meaning:
//   - slices are sorted, because dataset id order is not semantic (the assistant
//     may reorder its datasets without changing what the run retrieved);
//   - maps/structs go through encoding/json, which sorts map keys, so prompt
//     config comparison cannot flap on key order.
func trialSnapshotFieldValues(snapshot TrialAuthoringSnapshot) map[string]string {
	values := make(map[string]string, 20)
	value := reflect.ValueOf(snapshot)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fieldValue := value.Field(i)
		switch fieldValue.Kind() {
		case reflect.Slice:
			items := make([]string, 0, fieldValue.Len())
			for j := 0; j < fieldValue.Len(); j++ {
				items = append(items, fmt.Sprint(fieldValue.Index(j).Interface()))
			}
			sort.Strings(items)
			encoded, err := json.Marshal(items)
			if err != nil {
				values[name] = fmt.Sprintf("<unserialisable:%v>", err)
				continue
			}
			values[name] = string(encoded)
		case reflect.Map, reflect.Struct, reflect.Ptr:
			encoded, err := json.Marshal(fieldValue.Interface())
			if err != nil {
				values[name] = fmt.Sprintf("<unserialisable:%v>", err)
				continue
			}
			values[name] = string(encoded)
		default:
			values[name] = fmt.Sprint(fieldValue.Interface())
		}
	}
	return values
}

// trialPolicyHash binds the retrieval policy a run executed under. It is a pure
// function of the snapshot so the stored hash can be re-derived and checked:
// a hash that is computed from constants alone would not detect a run whose
// threshold/weight were edited after the fact.
func trialPolicyHash(snapshot TrialAuthoringSnapshot) (string, error) {
	return canonicalHash(json.RawMessage(`{"mode":"retrieval_only","top_n":` +
		fmt.Sprint(trialCaseTopN) + `,"threshold":` + trimFloat(snapshot.SimilarityThreshold) +
		`,"weight":` + trimFloat(snapshot.VectorSimilarityWeight) + `,"policy":"` + trialPolicyID + `"}`))
}

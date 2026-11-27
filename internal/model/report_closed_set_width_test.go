package model

// A closed set whose words do not fit their column is a defect that only one of the
// two supported drivers can see (doc/319 §9.3).
//
// Every report enum is a `varchar(n)` on PostgreSQL and a `text` on SQLite, so a
// value one byte too long inserts happily on the driver the unit suite uses and
// fails on the driver production uses — at the moment a run tries to write it, not
// at review time. R-01 added two values (`evidence_expired`, `evidence_missing`)
// that land exactly on the 16-byte limit of `quote_status`, which makes the width a
// load-bearing detail rather than a style choice.
//
// The assertion reads the limit out of the struct tag instead of hard-coding 16:
// narrowing the column later must fail here rather than in a drill.

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// columnSizes maps `column:"name"` to the `size:n` of the same field. Columns
// declared without a size are absent, which is itself a failure for an enum: a
// `varchar(255)` default would silently accept a typo'd value.
func columnSizes(t reflect.Type) map[string]int {
	sizes := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("gorm")
		if tag == "" {
			continue
		}
		column, size := "", 0
		for _, part := range strings.Split(tag, ";") {
			switch {
			case strings.HasPrefix(part, "column:"):
				column = strings.TrimPrefix(part, "column:")
			case strings.HasPrefix(part, "size:"):
				if parsed, err := strconv.Atoi(strings.TrimPrefix(part, "size:")); err == nil {
					size = parsed
				}
			}
		}
		if column != "" {
			sizes[column] = size
		}
	}
	return sizes
}

func TestReportClosedSetsFitTheirColumns(t *testing.T) {
	sizes := columnSizes(reflect.TypeOf(ReportClaimEvaluation{}))
	cases := []struct {
		column string
		values []string
	}{
		{"quote_status", ReportQuoteStatuses()},
		{"insufficient_reason", ReportInsufficientReasons()},
		{"abstain_reason", ReportAbstainReasons()},
		{"not_evaluated_reason", ReportNotEvaluatedReasons()},
	}
	for _, testCase := range cases {
		limit, ok := sizes[testCase.column]
		if !ok || limit <= 0 {
			t.Fatalf("%s has no declared size: an enum column without one accepts any typo", testCase.column)
		}
		if len(testCase.values) == 0 {
			t.Fatalf("%s has an empty closed set", testCase.column)
		}
		for _, value := range testCase.values {
			if len(value) > limit {
				t.Fatalf("%s value %q is %d bytes, column is %d: PostgreSQL rejects it, "+
					"SQLite accepts it, so only production would fail",
					testCase.column, value, len(value), limit)
			}
			if strings.TrimSpace(value) != value || value == "" {
				t.Fatalf("%s contains an unusable value %q", testCase.column, value)
			}
		}
	}
}

// TestReportQuoteStatusesCoverEveryLifecycleAnswer pins the two directions R-01
// needs to be honest about: the closed set must keep the three "nothing was
// checkable" answers apart, and the unreadable predicate must agree with it.
func TestReportQuoteStatusesCoverEveryLifecycleAnswer(t *testing.T) {
	for _, want := range []string{"not_provided", "evidence_expired", "evidence_missing"} {
		if !containsString(ReportQuoteStatuses(), want) {
			t.Fatalf("quote_status closed set is missing %q: the three unverifiable "+
				"answers must not collapse into one", want)
		}
	}
	unreadable := map[string]bool{
		ReportQuoteStatusVerified:        false,
		ReportQuoteStatusAmbiguous:       false,
		ReportQuoteStatusNotProvided:     false,
		ReportQuoteStatusNotFound:        true,
		ReportQuoteStatusEvidenceExpired: true,
		ReportQuoteStatusEvidenceMissing: true,
	}
	for status, want := range unreadable {
		if got := ReportQuoteStatusUnreadable(status); got != want {
			t.Fatalf("ReportQuoteStatusUnreadable(%q) = %v, want %v: a status that is "+
				"not unreadable would be spending a model call on an empty prompt", status, got, want)
		}
	}
	if len(unreadable) != len(ReportQuoteStatuses()) {
		t.Fatalf("the predicate covers %d statuses, the closed set has %d: the new one "+
			"was added without deciding whether it is readable", len(unreadable), len(ReportQuoteStatuses()))
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

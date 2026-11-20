package service

// Q&A proxy metrics (doc/301 §5.9 QA-3, task T20).
//
// This is **not** an answer-arm evaluation, and the file is shaped so it cannot be
// mistaken for one. Every number here is an *online proxy* computed from tables the
// product already writes (`rgx_knowledge_ops_event`, `rgx_message_feedback`), which
// buys comparability across versions at the price of selection bias — people rate what
// annoyed them. The two consequences, both structural:
//
//   - **A proxy never renders without its denominator.** `false_answer_proxy` divides by
//     answered turns *that received feedback*, not by all answered turns: dividing by
//     all of them would silently mix "no complaints" with "nobody looked".
//   - **The bias travels with the payload.** `Bias` carries feedback coverage, the
//     unlabelled turn count and the week basis, so the board physically cannot print
//     "误答率 3%" without also having the coverage number next to it.
//
// The calendar lives here rather than in SQL for a portability reason (see
// `repository/knowledge_ops_quality.go`): `strftime('%Y-%W')` and `date_trunc('week')`
// disagree about what a week is.

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

const (
	// answerQualityDefaultWeeks is what the board asks for when the caller says nothing:
	// two months is long enough to see a trend and short enough to still act on it.
	answerQualityDefaultWeeks = 8
	// answerQualityMaxWeeks caps the query count. Each week costs a fixed handful of
	// aggregates, so an unbounded `weeks` parameter is an unbounded cost on a read
	// endpoint that any `read` grantee can call.
	answerQualityMaxWeeks = 26
	// answerQualityWeekBasis is disclosed, not assumed: an operator in UTC+8 reading a
	// Monday-based UTC bucket will see "last week" differently from their own week, and
	// the number must say which one it is.
	answerQualityWeekBasis = "utc_monday"
	// answerQualityMethodology names the measurement so the UI can print the caveat from
	// its own dictionary rather than inventing words per screen.
	answerQualityMethodology = "online_proxy"
)

// AnswerQualityBucket is one week (or the whole window, for `Totals`). Dates are empty
// on totals: a total is not "the week of nothing".
type AnswerQualityBucket struct {
	WeekStart string `json:"week_start,omitempty"`
	WeekEnd   string `json:"week_end,omitempty"`

	Turns     int64 `json:"turns"`
	Answered  int64 `json:"answered"`
	Abstained int64 `json:"abstained"`

	// Rates. nil means "no denominator": 0/0 is unknown, never 0%.
	AbstainRate         *float64 `json:"abstain_rate"`
	CitationMissingRate *float64 `json:"citation_missing_rate"`
	FalseAnswerProxy    *float64 `json:"false_answer_proxy"`
	FeedbackCoverage    *float64 `json:"feedback_coverage"`

	// Raw counts behind every rate, so the reader can print the denominator next to it
	// instead of trusting a percentage. `CitationMissing` has no rate of its own beyond
	// `CitationMissingRate` but travels anyway: the board renders "n/m".
	CitationsMissing int64 `json:"citations_missing"`
	FeedbackAnswered int64 `json:"feedback_answered"`
	NegativeAnswered int64 `json:"negative_answered"`

	// Retrieval similarity. `SimilaritySamples` counts turns with a measurable value
	// (> 0); when it is 0 both quantiles are nil rather than 0.
	SimilaritySamples int64    `json:"similarity_samples"`
	SimilarityP50     *float64 `json:"similarity_p50"`
	SimilarityP90     *float64 `json:"similarity_p90"`
}

// AnswerQualityBias is the disclosure block. It is not decoration: doc/301 §5.9 says the
// page must state that these are proxies with selection bias, and a page cannot state
// what the server does not send.
type AnswerQualityBias struct {
	Methodology      string   `json:"methodology"`
	WeekBasis        string   `json:"week_basis"`
	FeedbackCoverage *float64 `json:"feedback_coverage"`
	// UnlabelledAnswered is the population the feedback-derived rates could not see.
	UnlabelledAnswered int64 `json:"unlabelled_answered"`
	WindowWeeks        int   `json:"window_weeks"`
	WeeksClamped       bool  `json:"weeks_clamped"`
}

// AnswerQualityProxyView is the whole board payload.
type AnswerQualityProxyView struct {
	Window Start                 `json:"window"`
	Weeks  []AnswerQualityBucket `json:"weeks"`
	Totals AnswerQualityBucket   `json:"totals"`
	Bias   AnswerQualityBias     `json:"bias"`
}

// Start is the half-open window the numbers cover.
type Start struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// answerQualityWindow is one bucket's half-open bounds.
type answerQualityWindow struct {
	Start time.Time
	End   time.Time
}

// AnswerQualityProxy computes the proxy board for a window.
//
// `dateFrom`/`dateTo` are the same optional `YYYY-MM-DD` strings every other
// knowledge-ops read accepts, so the board's date picker is not a special case. When
// they are absent the window is the last `weeks` whole UTC weeks ending with the week
// that contains `now` — "whole weeks" on purpose: a partial trailing week makes the last
// bucket dip and the trend line lie.
func (s *Service) AnswerQualityProxy(
	ctx context.Context, tenantID string, scopeAll bool,
	weeks int, dateFrom, dateTo string, now time.Time,
) (*AnswerQualityProxyView, error) {
	windows, clamped, err := answerQualityWindows(weeks, dateFrom, dateTo, now)
	if err != nil {
		return nil, err
	}
	view := &AnswerQualityProxyView{
		Weeks: make([]AnswerQualityBucket, 0, len(windows)),
		Bias: AnswerQualityBias{
			Methodology:  answerQualityMethodology,
			WeekBasis:    answerQualityWeekBasis,
			WindowWeeks:  len(windows),
			WeeksClamped: clamped,
		},
	}
	if len(windows) > 0 {
		view.Window.From = windows[0].Start.Format(time.RFC3339)
		view.Window.To = windows[len(windows)-1].End.Format(time.RFC3339)
	}

	for _, window := range windows {
		bucket, err := s.answerQualityBucket(ctx, tenantID, scopeAll, window)
		if err != nil {
			return nil, err
		}
		bucket.WeekStart = window.Start.Format(time.RFC3339)
		bucket.WeekEnd = window.End.Format(time.RFC3339)
		view.Weeks = append(view.Weeks, bucket)
	}

	// Totals are summed from raw counts rather than averaged over weeks: an average of
	// weekly rates weights a quiet week the same as a busy one, which is exactly the
	// mistake the report trend card was fixed for (T10 ②).
	if len(windows) > 0 {
		whole := answerQualityWindow{Start: windows[0].Start, End: windows[len(windows)-1].End}
		counts, err := s.Store.AnswerQualityCounts(ctx, tenantID, scopeAll, whole.Start, whole.End)
		if err != nil {
			return nil, err
		}
		samples, quantiles, err := s.Store.AnswerQualitySimilarityQuantiles(
			ctx, tenantID, scopeAll, whole.Start, whole.End, []float64{0.5, 0.9})
		if err != nil {
			return nil, err
		}
		view.Totals = answerQualityBucketOf(counts, samples, quantiles)
		view.Bias.FeedbackCoverage = view.Totals.FeedbackCoverage
		view.Bias.UnlabelledAnswered = view.Totals.Answered - view.Totals.FeedbackAnswered
		if view.Bias.UnlabelledAnswered < 0 {
			view.Bias.UnlabelledAnswered = 0
		}
	}
	return view, nil
}

// answerQualityBucket reads one window.
func (s *Service) answerQualityBucket(
	ctx context.Context, tenantID string, scopeAll bool, window answerQualityWindow,
) (AnswerQualityBucket, error) {
	counts, err := s.Store.AnswerQualityCounts(ctx, tenantID, scopeAll, window.Start, window.End)
	if err != nil {
		return AnswerQualityBucket{}, err
	}
	samples, quantiles, err := s.Store.AnswerQualitySimilarityQuantiles(
		ctx, tenantID, scopeAll, window.Start, window.End, []float64{0.5, 0.9})
	if err != nil {
		return AnswerQualityBucket{}, err
	}
	return answerQualityBucketOf(counts, samples, quantiles), nil
}

// answerQualityBucketOf turns counts into a bucket, deriving each rate with its own
// denominator. The four denominators are deliberately different:
//
//	abstain_rate          ÷ all turns          (how often the product declined)
//	citation_missing_rate ÷ answered turns     (how often it answered unsourced)
//	false_answer_proxy    ÷ feedback-answered  (how often a rated answer was wrong)
//	feedback_coverage     ÷ answered turns     (how much of the above we can see)
func answerQualityBucketOf(counts *model.AnswerQualityCounts, samples int64, quantiles []float64) AnswerQualityBucket {
	bucket := AnswerQualityBucket{
		Turns:             counts.Turns,
		Answered:          counts.Answered,
		Abstained:         counts.Abstained,
		CitationsMissing:  counts.CitationsMissing,
		FeedbackAnswered:  counts.FeedbackAnswered,
		NegativeAnswered:  counts.NegativeAnswered,
		SimilaritySamples: samples,
	}
	bucket.AbstainRate = answerQualityRatio(counts.Abstained, counts.Turns)
	bucket.CitationMissingRate = answerQualityRatio(counts.CitationsMissing, counts.Answered)
	bucket.FalseAnswerProxy = answerQualityRatio(counts.NegativeAnswered, counts.FeedbackAnswered)
	bucket.FeedbackCoverage = answerQualityRatio(counts.FeedbackAnswered, counts.Answered)
	if len(quantiles) > 0 {
		p50 := quantiles[0]
		bucket.SimilarityP50 = &p50
	}
	if len(quantiles) > 1 {
		p90 := quantiles[1]
		bucket.SimilarityP90 = &p90
	}
	return bucket
}

// answerQualityRatio is `ratioPtr` for int64 counts. Kept separate rather than widening
// the report one: the report's helper is on the assurance path and touching it would
// couple two unrelated denominators.
func answerQualityRatio(numerator, denominator int64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

// answerQualityWindows builds the bucket bounds.
//
// With `dateFrom`/`dateTo` the window is exactly what was asked for, cut into whole weeks
// from `from` and ending at `to` (the final bucket may be partial: an explicit range is a
// question about *that range*, not about the calendar). Without them, the last `weeks`
// complete UTC weeks ending with the week containing `now`.
func answerQualityWindows(weeks int, dateFrom, dateTo string, now time.Time) ([]answerQualityWindow, bool, error) {
	clamped := false
	if weeks <= 0 {
		weeks = answerQualityDefaultWeeks
	}
	if weeks > answerQualityMaxWeeks {
		weeks = answerQualityMaxWeeks
		clamped = true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	if strings.TrimSpace(dateFrom) != "" {
		from, ok := answerQualityParseDate(dateFrom)
		if !ok {
			return nil, false, &answerQualityDateError{value: dateFrom, field: "date_from"}
		}
		to := answerQualityMonday(now).AddDate(0, 0, 7)
		if strings.TrimSpace(dateTo) != "" {
			parsed, ok := answerQualityParseDate(dateTo)
			if !ok {
				return nil, false, &answerQualityDateError{value: dateTo, field: "date_to"}
			}
			// `date_to` is inclusive in the picker, so the half-open end is the next day.
			to = parsed.AddDate(0, 0, 1)
		}
		if !to.After(from) {
			return nil, false, &answerQualityDateError{value: dateFrom + ".." + dateTo, field: "date_to"}
		}
		windows := make([]answerQualityWindow, 0, answerQualityMaxWeeks)
		for cursor := from; cursor.Before(to); {
			end := cursor.AddDate(0, 0, 7)
			if end.After(to) {
				end = to
			}
			windows = append(windows, answerQualityWindow{Start: cursor, End: end})
			cursor = end
			if len(windows) == answerQualityMaxWeeks {
				clamped = true
				break
			}
		}
		return windows, clamped, nil
	}

	// `end` is the current week's Monday, i.e. the default window stops *before* the week
	// that is still running. A partial bucket always looks better or worse than it is
	// depending on which day the reader opens the page, and a trend line built from that
	// is a trend line about the weekday. An operator who wants today asks for an explicit
	// `date_from`..`date_to` range instead.
	end := answerQualityMonday(now)
	windows := make([]answerQualityWindow, 0, weeks)
	for i := 0; i < weeks; i++ {
		start := end.AddDate(0, 0, -7)
		windows = append([]answerQualityWindow{{Start: start, End: end}}, windows...)
		end = start
	}
	return windows, clamped, nil
}

// answerQualityMonday truncates to the Monday 00:00 UTC of `t`'s week.
func answerQualityMonday(t time.Time) time.Time {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(day.Weekday()) + 6) % 7 // Monday=0 … Sunday=6
	return day.AddDate(0, 0, -offset)
}

// answerQualityParseDate accepts the same `YYYY-MM-DD` and RFC3339 forms the other
// knowledge-ops reads accept (`repository.parseFilterDate`), so the board's picker does
// not need a second format.
func answerQualityParseDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// answerQualityDateError is a 400, not a 500: a malformed date is the caller's input.
type answerQualityDateError struct {
	value string
	field string
}

func (e *answerQualityDateError) Error() string {
	return "invalid " + e.field + " value " + e.value + ": expected YYYY-MM-DD or RFC3339"
}

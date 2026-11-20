package repository

// The Q&A proxy-metric aggregates behind doc/301 §5.9 QA-3 (T20).
//
// Three decisions are baked in here and they are all about *portability and honesty*,
// not about speed:
//
//   - **No week-truncating SQL.** `strftime('%Y-%W')` is sqlite, `date_trunc('week')` is
//     postgres, and a `GROUP BY` on either silently produces a different calendar on
//     the other engine. The service therefore hands this file one window at a time and
//     owns the calendar.
//   - **Counts, never rates.** A rate computed in SQL with `SUM(...)/COUNT(*)` yields 0
//     when the denominator is 0, and 0% abstention on a week with no traffic reads as a
//     healthy week. The struct carries counts; the service decides what "unjudged"
//     looks like.
//   - **Quantiles by nearest rank through `OFFSET`.** Both engines support it, and it
//     is exact. An approximation over a `LIMIT`ed sample would be a number nobody could
//     defend on a board operators use to decide staffing.

import (
	"context"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// knowledgeOpsAnsweredFilter is the status test for "the user got an answer". It is a
// named constant because three aggregates below must agree on it: if one of them
// counted `failed` as answered, `citation_missing_rate` would divide by a denominator
// the abstain rate does not use.
const knowledgeOpsAnsweredStatus = model.KnowledgeOpsCompleted

// errAnswerQualityQuantileOutOfRange is a programming error, not a user error: it can
// only be raised by a caller in this package passing a quantile outside (0,1].
var errAnswerQualityQuantileOutOfRange = errors.New("answer quality quantile must be in (0,1]")

// AnswerQualityCounts aggregates one time window.
//
// `abstained` counts a turn once even when both signals fire (`status = no_answer` and a
// recorded `abstain_reason`), because a turn is a turn: adding them would produce an
// abstain rate above 100% in a week where the honesty layer and the engine agreed.
func (s *store) AnswerQualityCounts(ctx context.Context, tenantID string, scopeAll bool, from, to time.Time) (*model.AnswerQualityCounts, error) {
	var events struct {
		Turns     int64 `gorm:"column:turns"`
		Answered  int64 `gorm:"column:answered"`
		Abstained int64 `gorm:"column:abstained"`
		Missing   int64 `gorm:"column:citations_missing"`
	}
	q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).Select(`
		COUNT(*) AS turns,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS answered,
		SUM(CASE WHEN status = ? OR abstain_reason <> '' THEN 1 ELSE 0 END) AS abstained,
		SUM(CASE WHEN status = ? AND citations_count = 0 THEN 1 ELSE 0 END) AS citations_missing
	`, knowledgeOpsAnsweredStatus, model.KnowledgeOpsNoAnswer, knowledgeOpsAnsweredStatus)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	q = q.Where("created_at >= ? AND created_at < ?", from, to)
	if err := q.Scan(&events).Error; err != nil {
		return nil, err
	}

	counts := &model.AnswerQualityCounts{
		Turns:            events.Turns,
		Answered:         events.Answered,
		Abstained:        events.Abstained,
		CitationsMissing: events.Missing,
	}
	if events.Answered == 0 {
		// Nothing was answered, so there is nothing to join: skipping the second query
		// keeps an empty week cheap without changing any number.
		return counts, nil
	}

	var feedback struct {
		Answered int64 `gorm:"column:feedback_answered"`
		Negative int64 `gorm:"column:negative_answered"`
	}
	// `COUNT(DISTINCT e.id)` because feedback is per user per message: one turn rated by
	// three colleagues is one turn, and counting rows would let a single unhappy session
	// move the board.
	fq := s.WithContext(ctx).
		Table("rgx_knowledge_ops_event AS e").
		Select(`
			COUNT(DISTINCT e.id) AS feedback_answered,
			COUNT(DISTINCT CASE WHEN f.rating = ? THEN e.id END) AS negative_answered
		`, model.FeedbackNegative).
		Joins("JOIN rgx_message_feedback AS f ON f.request_id = e.request_id").
		Where("e.status = ? AND e.request_id <> ''", knowledgeOpsAnsweredStatus).
		Where("e.created_at >= ? AND e.created_at < ?", from, to)
	if !scopeAll {
		fq = fq.Where("e.tenant_id = ?", tenantID)
	}
	if err := fq.Scan(&feedback).Error; err != nil {
		return nil, err
	}
	counts.FeedbackAnswered = feedback.Answered
	counts.NegativeAnswered = feedback.Negative
	return counts, nil
}

// AnswerQualitySimilarityQuantiles returns the exact nearest-rank quantiles of
// `top_similarity` over the window, restricted to turns that actually had a measured
// similarity (`> 0`).
//
// `samples == 0` means "no measurable retrieval", which is why the values slice is
// empty rather than full of zeros: the caller renders that as 暂无判定.
func (s *store) AnswerQualitySimilarityQuantiles(ctx context.Context, tenantID string, scopeAll bool, from, to time.Time, quantiles []float64) (int64, []float64, error) {
	// A fresh query per statement: gorm sessions are stateful, and reusing one for the
	// count and then for an `OFFSET` scan would carry the first statement's clauses.
	base := func() *gorm.DB {
		q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).
			Where("top_similarity > 0").
			Where("created_at >= ? AND created_at < ?", from, to)
		if !scopeAll {
			q = q.Where("tenant_id = ?", tenantID)
		}
		return q
	}
	var samples int64
	if err := base().Count(&samples).Error; err != nil {
		return 0, nil, err
	}
	values := make([]float64, 0, len(quantiles))
	if samples == 0 {
		return 0, values, nil
	}
	for _, q := range quantiles {
		if q <= 0 || q > 1 {
			// A quantile outside (0,1] would make the rank arithmetic meaningless; the
			// caller passed a programming error, and dropping it silently would return
			// a shorter slice than it asked for.
			return 0, nil, errAnswerQualityQuantileOutOfRange
		}
		rank := int64(math.Ceil(q*float64(samples))) - 1
		if rank < 0 {
			rank = 0
		}
		if rank > samples-1 {
			rank = samples - 1
		}
		var value float64
		row := base().
			Select("top_similarity").
			Order("top_similarity ASC").
			Offset(int(rank)).
			Limit(1).
			Scan(&value)
		if row.Error != nil {
			return 0, nil, row.Error
		}
		values = append(values, value)
	}
	return samples, values, nil
}

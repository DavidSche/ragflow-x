package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// seedAnswerQualityEvent writes one ledger row; feedback is attached separately so a
// test can control the join.
func seedAnswerQualityEvent(t *testing.T, store Store, ctx context.Context, in answerQualitySeed) *model.KnowledgeOpsEvent {
	t.Helper()
	event := &model.KnowledgeOpsEvent{
		RequestID:      in.requestID,
		TenantID:       in.tenantID,
		UserID:         "u1",
		AppType:        "chat",
		AppID:          "chat-1",
		SessionID:      "session-1",
		Question:       "问题",
		QuestionHash:   "hash-" + in.requestID,
		AnswerExcerpt:  "答案",
		Status:         in.status,
		CitationsCount: in.citations,
		AbstainReason:  in.abstainReason,
		TopSimilarity:  in.topSimilarity,
		CreatedAt:      in.createdAt,
	}
	if err := store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatalf("upsert event %s: %v", in.requestID, err)
	}
	for i, feedback := range in.feedback {
		if err := store.UpsertMessageFeedback(ctx, &model.MessageFeedback{
			TenantID: in.tenantID, ChatID: "chat-1", SessionID: "session-1",
			// `RequestID` is what the proxy board joins on: feedback recorded without it
			// is invisible to the join, which is why the fixture must carry it.
			MessageID: "message-" + in.requestID + "-" + string(rune('a'+i)),
			UserID:    "u1", RequestID: in.requestID, Rating: feedback, Comment: "",
		}); err != nil {
			t.Fatalf("upsert feedback %s: %v", in.requestID, err)
		}
	}
	return event
}

type answerQualitySeed struct {
	requestID     string
	tenantID      string
	status        string
	citations     int
	abstainReason string
	topSimilarity float64
	createdAt     time.Time
	feedback      []string
}

func TestAnswerQualityCountsUseFourDifferentDenominators(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	windowStart := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // a Monday
	windowEnd := windowStart.AddDate(0, 0, 7)

	// Five turns in-window: 3 answered (one unsourced), 1 no-answer, 1 failed.
	// The failed turn carries a negative rating — proving the proxy's denominator is
	// answered-with-feedback, not "anything rated".
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-answered-sourced", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 2, topSimilarity: 0.81, createdAt: windowStart.Add(time.Hour),
		feedback: []string{model.FeedbackPositive},
	})
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-answered-unsourced", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 0, topSimilarity: 0.42, createdAt: windowStart.Add(2 * time.Hour),
		// Two colleagues rated the same turn: one turn, and it must count once.
		feedback: []string{model.FeedbackNegative, model.FeedbackNegative},
	})
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-answered-unrated", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 1, topSimilarity: 0.33, createdAt: windowStart.Add(3 * time.Hour),
	})
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-no-answer", tenantID: "t1", status: model.KnowledgeOpsNoAnswer,
		abstainReason: "no_evidence", createdAt: windowStart.Add(4 * time.Hour),
		feedback: []string{model.FeedbackNegative},
	})
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-failed", tenantID: "t1", status: model.KnowledgeOpsFailed,
		createdAt: windowStart.Add(5 * time.Hour), feedback: []string{model.FeedbackNegative},
	})
	// Out of window, and another tenant: neither may move a count.
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-out-of-window", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 1, topSimilarity: 0.99, createdAt: windowEnd.Add(time.Hour),
	})
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-other-tenant", tenantID: "t2", status: model.KnowledgeOpsCompleted,
		citations: 1, topSimilarity: 0.99, createdAt: windowStart.Add(time.Hour),
	})

	counts, err := store.AnswerQualityCounts(ctx, "t1", false, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("answer quality counts: %v", err)
	}
	if counts.Turns != 5 {
		t.Fatalf("turns must be scoped to the window and tenant, got %d", counts.Turns)
	}
	if counts.Answered != 3 {
		t.Fatalf("answered must count only completed, got %d", counts.Answered)
	}
	// `req-no-answer` (status) and nothing else: the failed turn recorded no reason, and
	// a failure is not an abstention.
	if counts.Abstained != 1 {
		t.Fatalf("abstained must count no-answer + recorded reason once each, got %d", counts.Abstained)
	}
	if counts.CitationsMissing != 1 {
		t.Fatalf("citations missing must divide answered turns only, got %d", counts.CitationsMissing)
	}
	if counts.FeedbackAnswered != 2 {
		t.Fatalf("feedback answered must be distinct turns, got %d", counts.FeedbackAnswered)
	}
	if counts.NegativeAnswered != 1 {
		t.Fatalf("negative answered must exclude the no-answer and failed turns, got %d", counts.NegativeAnswered)
	}

	// A second turn whose status is completed *and* whose reason is recorded must count
	// as one abstention, not two: the two signals routinely agree.
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-answered-but-abstain-marker", tenantID: "t1",
		status: model.KnowledgeOpsCompleted, abstainReason: "low_similarity",
		createdAt: windowStart.Add(6 * time.Hour),
	})
	counts, err = store.AnswerQualityCounts(ctx, "t1", false, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("answer quality counts after abstain marker: %v", err)
	}
	if counts.Abstained != 2 || counts.Turns != 6 {
		t.Fatalf("an agreeing signal must not double count: abstained=%d turns=%d", counts.Abstained, counts.Turns)
	}

	// `scopeAll` is the enterprise view: it must see both tenants and nothing else.
	all, err := store.AnswerQualityCounts(ctx, "t1", true, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("answer quality counts scopeAll: %v", err)
	}
	if all.Turns != 7 {
		t.Fatalf("scopeAll must include the other tenant, got %d", all.Turns)
	}
}

func TestAnswerQualitySimilarityQuantilesAreExactNearestRank(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	windowStart := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	windowEnd := windowStart.AddDate(0, 0, 7)

	for i, value := range []float64{0.10, 0.20, 0.30, 0.40, 0.50, 0.60, 0.70, 0.80, 0.90, 1.00} {
		seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
			requestID: "req-sim-" + string(rune('a'+i)), tenantID: "t1",
			status: model.KnowledgeOpsCompleted, citations: 1, topSimilarity: value,
			createdAt: windowStart.Add(time.Duration(i) * time.Minute),
		})
	}
	// Zero similarity means "no measurement", not "irrelevant": it must not enter the
	// sample, otherwise p50 collapses on a week of failures.
	seedAnswerQualityEvent(t, store, ctx, answerQualitySeed{
		requestID: "req-sim-none", tenantID: "t1", status: model.KnowledgeOpsFailed,
		topSimilarity: 0, createdAt: windowStart.Add(11 * time.Minute),
	})

	samples, quantiles, err := store.AnswerQualitySimilarityQuantiles(
		ctx, "t1", false, windowStart, windowEnd, []float64{0.5, 0.9})
	if err != nil {
		t.Fatalf("quantiles: %v", err)
	}
	if samples != 10 {
		t.Fatalf("zero similarity must be excluded from the sample, got %d", samples)
	}
	if len(quantiles) != 2 {
		t.Fatalf("expected two quantiles, got %v", quantiles)
	}
	// Nearest rank over 10 sorted values: ceil(0.5*10)-1 = index 4 → 0.50;
	// ceil(0.9*10)-1 = index 8 → 0.90.
	if quantiles[0] != 0.50 || quantiles[1] != 0.90 {
		t.Fatalf("unexpected nearest-rank quantiles: %v", quantiles)
	}

	empty := windowStart.AddDate(0, 0, 7)
	samples, quantiles, err = store.AnswerQualitySimilarityQuantiles(
		ctx, "t1", false, empty, empty.AddDate(0, 0, 7), []float64{0.5, 0.9})
	if err != nil {
		t.Fatalf("quantiles on empty window: %v", err)
	}
	if samples != 0 || len(quantiles) != 0 {
		t.Fatalf("an empty window must return no samples and no quantiles: samples=%d values=%v", samples, quantiles)
	}

	if _, _, err := store.AnswerQualitySimilarityQuantiles(
		ctx, "t1", false, windowStart, windowEnd, []float64{1.5}); err == nil {
		t.Fatal("a quantile outside (0,1] must be refused, not silently dropped")
	}
}

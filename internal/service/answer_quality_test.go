package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// answerQualityFixture is the smallest thing that can run the proxy board: a real store
// (the board's honesty is a SQL property, not a mock property) and the service.
func answerQualityFixture(t *testing.T) (*Service, repository.Store, context.Context) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: testSQLiteDSN(t, "answer-quality.db")})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	return &Service{Store: store}, store, context.Background()
}

// proxySeed is the event shape the board reads. Feedback carries `RequestID` on
// purpose: the join is `request_id`, so a fixture without it would exercise nothing.
type proxySeed struct {
	requestID     string
	tenantID      string
	status        string
	citations     int
	abstainReason string
	topSimilarity float64
	createdAt     time.Time
	feedback      []string
}

func seedProxyEvent(t *testing.T, store repository.Store, ctx context.Context, seed proxySeed) {
	t.Helper()
	event := &model.KnowledgeOpsEvent{
		RequestID: seed.requestID, TenantID: seed.tenantID, UserID: "u1", AppType: "chat",
		AppID: "chat-1", SessionID: "session-1", Question: "问题",
		QuestionHash: "hash-" + seed.requestID, AnswerExcerpt: "答案",
		Status: seed.status, CitationsCount: seed.citations,
		AbstainReason: seed.abstainReason, TopSimilarity: seed.topSimilarity,
		CreatedAt: seed.createdAt,
	}
	if err := store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatalf("upsert event: %v", err)
	}
	for i, rating := range seed.feedback {
		if err := store.UpsertMessageFeedback(ctx, &model.MessageFeedback{
			TenantID: seed.tenantID, ChatID: "chat-1", SessionID: "session-1",
			MessageID: "message-" + seed.requestID + string(rune('a'+i)),
			UserID:    "u1", RequestID: seed.requestID, Rating: rating,
		}); err != nil {
			t.Fatalf("upsert feedback: %v", err)
		}
	}
}

// TestAnswerQualityProxyRatesAreNeverVacuous is the gate the whole task exists for: a
// week with no traffic must render as "unjudged", never as 0%.
func TestAnswerQualityProxyRatesAreNeverVacuous(t *testing.T) {
	svc, _, ctx := answerQualityFixture(t)
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC) // Wednesday

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 2, "", "", now)
	if err != nil {
		t.Fatalf("proxy on an empty tenant: %v", err)
	}
	if len(view.Weeks) != 2 {
		t.Fatalf("expected two weekly buckets, got %d", len(view.Weeks))
	}
	for _, week := range view.Weeks {
		if week.Turns != 0 {
			t.Fatalf("an empty tenant must produce empty weeks: %+v", week)
		}
		if week.AbstainRate != nil || week.CitationMissingRate != nil ||
			week.FalseAnswerProxy != nil || week.FeedbackCoverage != nil {
			t.Fatalf("0/0 must be nil, never 0: %+v", week)
		}
		if week.SimilarityP50 != nil || week.SimilarityP90 != nil || week.SimilaritySamples != 0 {
			t.Fatalf("no measurable similarity must leave the quantiles nil: %+v", week)
		}
	}
	if view.Totals.AbstainRate != nil {
		t.Fatalf("totals of nothing must also be nil: %+v", view.Totals)
	}
	if view.Bias.FeedbackCoverage != nil {
		t.Fatalf("feedback coverage of nothing must be nil, not 0%%: %+v", view.Bias)
	}
}

// TestAnswerQualityProxyWeekCalendar pins the calendar: Monday 00:00 UTC buckets, the
// trailing bucket is a *whole* week, and the disclosure says which week basis it is.
func TestAnswerQualityProxyWeekCalendar(t *testing.T) {
	svc, _, ctx := answerQualityFixture(t)
	// Wednesday 2026-03-04 → the containing week starts Monday 2026-03-02, so the two
	// complete weeks are 02-16..02-23 and 02-23..03-02.
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 2, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-02-16T00:00:00Z", "2026-02-23T00:00:00Z"}
	for i, week := range view.Weeks {
		if week.WeekStart != want[i] {
			t.Fatalf("week %d must start at %s, got %s", i, want[i], week.WeekStart)
		}
	}
	// The window ends where the current week begins: a partial trailing week would make
	// the newest bucket dip simply because it is unfinished.
	if view.Window.To != "2026-03-02T00:00:00Z" {
		t.Fatalf("the window must end at the current week's Monday, got %s", view.Window.To)
	}
	if view.Bias.WeekBasis != answerQualityWeekBasis || view.Bias.Methodology != answerQualityMethodology {
		t.Fatalf("the bias block must name the measurement and the calendar: %+v", view.Bias)
	}
	if view.Bias.WindowWeeks != 2 || view.Bias.WeeksClamped {
		t.Fatalf("unexpected window metadata: %+v", view.Bias)
	}
}

// TestAnswerQualityProxyExplicitRangeIsHonoured — an explicit range is a question about
// that range, so the first bucket starts at `date_from` even when it is mid-week.
func TestAnswerQualityProxyExplicitRangeIsHonoured(t *testing.T) {
	svc, store, ctx := answerQualityFixture(t)
	seedProxyEvent(t, store, ctx, proxySeed{
		requestID: "req-in", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 1, createdAt: time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC),
	})
	seedProxyEvent(t, store, ctx, proxySeed{
		requestID: "req-before", tenantID: "t1", status: model.KnowledgeOpsCompleted,
		citations: 0, createdAt: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
	})

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 0, "2026-03-04", "2026-03-04", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Weeks) != 1 {
		t.Fatalf("a one-day range is one bucket, got %d", len(view.Weeks))
	}
	if view.Weeks[0].WeekStart != "2026-03-04T00:00:00Z" || view.Weeks[0].WeekEnd != "2026-03-05T00:00:00Z" {
		t.Fatalf("date_to must be inclusive: %s..%s", view.Weeks[0].WeekStart, view.Weeks[0].WeekEnd)
	}
	if view.Weeks[0].Turns != 1 || view.Totals.Turns != 1 {
		t.Fatalf("only the in-range turn may be counted: week=%+v totals=%+v", view.Weeks[0], view.Totals)
	}
	if _, err := svc.AnswerQualityProxy(ctx, "t1", false, 0, "not-a-date", "", time.Now()); err == nil {
		t.Fatal("a malformed date must be refused with an error, not silently ignored")
	}
	if _, err := svc.AnswerQualityProxy(ctx, "t1", false, 0, "2026-03-05", "2026-03-04", time.Now()); err == nil {
		t.Fatal("an inverted range must be refused, not answered with an empty board")
	}
}

// TestAnswerQualityProxyTotalsPoolCounts — totals must be pooled counts, not an average
// of weekly rates: a quiet week with one bad turn cannot outweigh a busy clean one.
func TestAnswerQualityProxyTotalsPoolCounts(t *testing.T) {
	svc, store, ctx := answerQualityFixture(t)
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	// Week A (2026-02-16): 1 turn, 1 abstention → weekly rate 100%.
	seedProxyEvent(t, store, ctx, proxySeed{
		requestID: "req-a", tenantID: "t1", status: model.KnowledgeOpsNoAnswer,
		createdAt: time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
	})
	// Week B (2026-02-23): 9 answered turns → weekly rate 0%.
	for i := 0; i < 9; i++ {
		seedProxyEvent(t, store, ctx, proxySeed{
			requestID: "req-b-" + string(rune('a'+i)), tenantID: "t1",
			status: model.KnowledgeOpsCompleted, citations: 1,
			createdAt: time.Date(2026, 2, 24, 9, i, 0, 0, time.UTC),
		})
	}

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 2, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Weeks) != 2 {
		t.Fatalf("expected two weeks, got %d", len(view.Weeks))
	}
	if view.Weeks[0].AbstainRate == nil || *view.Weeks[0].AbstainRate != 1 {
		t.Fatalf("week A must be 1/1: %+v", view.Weeks[0])
	}
	if view.Weeks[1].AbstainRate == nil || *view.Weeks[1].AbstainRate != 0 {
		t.Fatalf("week B must be 0/9 (a measured zero): %+v", view.Weeks[1])
	}
	if view.Totals.AbstainRate == nil || *view.Totals.AbstainRate != 0.1 {
		t.Fatalf("totals must pool counts to 1/10, not average (100%%+0%%)/2: %+v", view.Totals)
	}
	// The measured-zero / unknown distinction: 0/9 is not nil, 0/0 is.
	if view.Totals.AbstainRate == nil {
		t.Fatal("a measured zero must be 0, not unknown")
	}
}

// TestAnswerQualityProxyFalseAnswerProxyCarriesItsDenominator — the feedback-derived rate
// divides by rated answers, and the coverage that makes it readable travels with it.
func TestAnswerQualityProxyFalseAnswerProxyCarriesItsDenominator(t *testing.T) {
	svc, store, ctx := answerQualityFixture(t)
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	// 4 answered turns in the week of 2026-02-23; only 1 rated, and it is negative.
	for i, rating := range []string{model.FeedbackNegative, "", "", ""} {
		var feedback []string
		if rating != "" {
			feedback = []string{rating}
		}
		seedProxyEvent(t, store, ctx, proxySeed{
			requestID: "req-" + string(rune('a'+i)), tenantID: "t1",
			status: model.KnowledgeOpsCompleted, citations: 1, topSimilarity: 0.5,
			createdAt: time.Date(2026, 2, 25, 9, i, 0, 0, time.UTC), feedback: feedback,
		})
	}

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 2, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	week := view.Weeks[1]
	if week.Answered != 4 || week.FeedbackAnswered != 1 || week.NegativeAnswered != 1 {
		t.Fatalf("unexpected counts: %+v", week)
	}
	if week.FalseAnswerProxy == nil || *week.FalseAnswerProxy != 1 {
		t.Fatalf("false answer proxy must divide by rated answers (1/1): %+v", week)
	}
	if week.FeedbackCoverage == nil || *week.FeedbackCoverage != 0.25 {
		t.Fatalf("coverage must divide by answered turns (1/4): %+v", week)
	}
	if view.Bias.UnlabelledAnswered != 3 {
		t.Fatalf("the disclosure must name the population the rate cannot see: %+v", view.Bias)
	}
	if view.Bias.FeedbackCoverage == nil || *view.Bias.FeedbackCoverage != 0.25 {
		t.Fatalf("the bias block must carry the same coverage: %+v", view.Bias)
	}
}

// TestAnswerQualityProxyClampsTheWindow — an unbounded `weeks` is an unbounded query
// count on a read endpoint, and the clamp must be disclosed rather than silent.
func TestAnswerQualityProxyClampsTheWindow(t *testing.T) {
	svc, _, ctx := answerQualityFixture(t)
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	view, err := svc.AnswerQualityProxy(ctx, "t1", false, 500, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Weeks) != answerQualityMaxWeeks {
		t.Fatalf("expected %d weeks, got %d", answerQualityMaxWeeks, len(view.Weeks))
	}
	if !view.Bias.WeeksClamped {
		t.Fatal("a clamped window must say so: a shorter trend than requested is not the same trend")
	}
}

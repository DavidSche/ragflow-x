package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestKnowledgeOpsSummaryTopQueriesAndReview(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)

	mk := func(requestID, question, status string, citations int, rating, comment string) {
		t.Helper()
		event := &model.KnowledgeOpsEvent{
			RequestID: requestID, TenantID: "t1", UserID: "u1", AppType: "chat",
			AppID: "chat-1", SessionID: "session-1", Question: question,
			AnswerExcerpt: "answer", Status: status, CitationsCount: citations,
			DurationMs: int64(len(requestID)*100 + 100),
			TokensIn:   10, TokensOut: 5,
		}
		if err := store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
			t.Fatalf("upsert event %s: %v", requestID, err)
		}
		if rating == "" {
			return
		}
		if err := store.UpsertMessageFeedback(ctx, &model.MessageFeedback{
			TenantID: "t1", ChatID: "chat-1", SessionID: "session-1",
			MessageID: "message-" + requestID, UserID: "u1", Rating: rating, Comment: comment,
		}); err != nil {
			t.Fatalf("upsert feedback %s: %v", requestID, err)
		}
	}

	mk("req-1", "报销流程是什么", model.KnowledgeOpsCompleted, 3, model.FeedbackPositive, "")
	mk("req-2", "报销流程是什么", model.KnowledgeOpsNoAnswer, 0, "", "")
	mk("req-3", "数据库回退步骤", model.KnowledgeOpsFailed, 0, model.FeedbackNegative, "答案错误")

	summary, err := store.KnowledgeOpsSummary(ctx, "t1", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalTurns != 3 || summary.ActiveUsers != 1 || summary.ActiveSessions != 1 {
		t.Fatalf("unexpected core summary: %+v", summary)
	}
	if summary.NoAnswer != 1 || summary.Failed != 1 || summary.WithCitations != 1 {
		t.Fatalf("unexpected quality summary: %+v", summary)
	}
	if summary.Positive != 1 || summary.Negative != 1 || summary.SatisfactionRate != 0.5 {
		t.Fatalf("unexpected feedback summary: %+v", summary)
	}
	if summary.AttributionSummary["unclassified"] != 1 {
		t.Fatalf("legacy negative feedback must be counted as unclassified: %+v", summary.AttributionSummary)
	}
	if summary.AvgLatencyMs <= 0 {
		t.Fatalf("expected average latency, got %f", summary.AvgLatencyMs)
	}

	queries, err := store.TopKnowledgeQueries(ctx, "t1", false, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0].Question != "报销流程是什么" || queries[0].Requests != 2 {
		t.Fatalf("unexpected top queries: %+v", queries)
	}
	if queries[0].NoAnswerCount != 1 {
		t.Fatalf("expected grouped no-answer count: %+v", queries[0])
	}
	if queries[0].AvgLatencyMs <= 0 {
		t.Fatalf("expected grouped latency, got %+v", queries[0])
	}

	events, total, err := store.ListKnowledgeOpsEvents(ctx, "t1", false, 1, 20, KnowledgeOpsFilter{ReviewStatus: model.KnowledgeOpsReviewOpen})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("unexpected open events: total=%d len=%d", total, len(events))
	}

	target := events[0].ID
	updated, err := store.ReviewKnowledgeOpsEvent(ctx, "t1", target, false, KnowledgeOpsReview{
		Status: model.KnowledgeOpsReviewResolved, Note: "知识已补充", Actor: "admin",
	})
	if err != nil || !updated {
		t.Fatalf("review failed: updated=%v err=%v", updated, err)
	}
	events, total, err = store.ListKnowledgeOpsEvents(ctx, "t1", false, 1, 20, KnowledgeOpsFilter{ReviewStatus: model.KnowledgeOpsReviewOpen})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(events) != 2 {
		t.Fatalf("reviewed event was not closed: total=%d len=%d", total, len(events))
	}
}

func TestKnowledgeOpsResolutionDuration(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	createdAt := time.Now().UTC().Add(-2 * time.Hour)
	event := &model.KnowledgeOpsEvent{
		RequestID: "resolution-1", TenantID: "t1", UserID: "u1", AppType: "chat",
		AppID: "chat-1", Question: "坏了", QuestionHash: "badcase", Status: model.KnowledgeOpsFailed,
		CreatedAt: createdAt,
	}
	if err := store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatalf("upsert resolution event: %v", err)
	}
	events, _, err := store.ListKnowledgeOpsEvents(ctx, "t1", false, 1, 10, KnowledgeOpsFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("list resolution event: events=%d err=%v", len(events), err)
	}
	updated, err := store.ReviewKnowledgeOpsEvent(ctx, "t1", events[0].ID, false, KnowledgeOpsReview{
		Status: model.KnowledgeOpsReviewResolved, Actor: "admin",
	})
	if err != nil || !updated {
		t.Fatalf("review resolution event: updated=%v err=%v", updated, err)
	}
	summary, err := store.KnowledgeOpsSummary(ctx, "t1", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.AvgResolutionHours <= 0 || summary.AvgResolutionHours > 3 {
		t.Fatalf("unexpected resolution duration: %f", summary.AvgResolutionHours)
	}
}

func TestKnowledgeOpsAttributionSummaryAndFilter(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	event := &model.KnowledgeOpsEvent{
		RequestID: "request-attribution", TenantID: "t1", UserID: "u1", AppType: "chat",
		AppID: "chat-1", SessionID: "session-1", Question: "为什么错误", QuestionHash: "hash",
		AnswerExcerpt: "bad answer", Status: model.KnowledgeOpsCompleted,
	}
	if err := store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	feedback := &model.MessageFeedback{
		TenantID: "t1", ChatID: "chat-1", SessionID: "session-1", MessageID: "message-1",
		UserID: "u1", RequestID: "request-attribution", Rating: model.FeedbackNegative,
		Attribution: model.FeedbackAttributionRetrieval, Comment: "没有检索到制度",
	}
	if err := store.UpsertMessageFeedback(ctx, feedback); err != nil {
		t.Fatal(err)
	}
	attached, err := store.AttachMessageFeedbackToKnowledgeOpsEvent(ctx, "t1", feedback.RequestID, feedback)
	if err != nil || !attached {
		t.Fatalf("attach attribution: attached=%v err=%v", attached, err)
	}
	events, _, err := store.ListKnowledgeOpsEvents(ctx, "t1", false, 1, 10, KnowledgeOpsFilter{
		Attribution: model.FeedbackAttributionRetrieval,
	})
	if err != nil || len(events) != 1 || events[0].FeedbackAttribution != model.FeedbackAttributionRetrieval {
		t.Fatalf("attribution filter: events=%d err=%v", len(events), err)
	}
	unclassified, _, err := store.ListKnowledgeOpsEvents(ctx, "t1", false, 1, 10, KnowledgeOpsFilter{Attribution: "unclassified"})
	if err != nil || len(unclassified) != 0 {
		t.Fatalf("unclassified filter: events=%d err=%v", len(unclassified), err)
	}
	summary, err := store.KnowledgeOpsSummary(ctx, "t1", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Negative != 1 || summary.AttributionSummary[model.FeedbackAttributionRetrieval] != 1 || summary.AttributionSummary["unclassified"] != 0 {
		t.Fatalf("unexpected attribution summary: %+v", summary)
	}
}

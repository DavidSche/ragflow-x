package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestChatFeedbackOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, ta.ID, "kb", []string{"d1"})
	if err != nil {
		t.Fatal(err)
	}
	req := FeedbackRequest{ChatID: chat.ID, SessionID: "s1", MessageID: "m1", Rating: "positive"}

	// Owner can record feedback; tenant B cannot.
	if _, err := svc.RecordMessageFeedback(ctx, ta.ID, "u1", req); err != nil {
		t.Fatalf("owner feedback: %v", err)
	}
	if _, err := svc.RecordMessageFeedback(ctx, tb.ID, "u2", req); err == nil {
		t.Fatal("tenant B must not feedback on tenant A's chat")
	}

	// Invalid rating is rejected.
	if _, err := svc.RecordMessageFeedback(ctx, ta.ID, "u1", FeedbackRequest{ChatID: chat.ID, SessionID: "s1", MessageID: "m1", Rating: "meh"}); err == nil {
		t.Fatal("invalid rating must be rejected")
	}
	if _, err := svc.RecordMessageFeedback(ctx, ta.ID, "u1", FeedbackRequest{
		ChatID: chat.ID, SessionID: "s1", MessageID: "m1", Rating: model.FeedbackNegative,
		Attribution: "unknown",
	}); err == nil {
		t.Fatal("invalid negative attribution must be rejected")
	}
	pos, err := svc.RecordMessageFeedback(ctx, ta.ID, "u1", FeedbackRequest{
		ChatID: chat.ID, SessionID: "s1", MessageID: "m1", Rating: model.FeedbackPositive,
		Attribution: model.FeedbackAttributionTool,
	})
	if err != nil || pos.Attribution != "" {
		t.Fatalf("positive feedback must clear attribution: feedback=%+v err=%v", pos, err)
	}

	rows, total, err := svc.Store.ListMessageFeedback(ctx, ta.ID, chat.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Rating != "positive" {
		t.Fatalf("unexpected feedback rows: total=%d rows=%+v", total, rows)
	}
}

func TestNegativeFeedbackBecomesKnowledgeOpsBadcase(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Feedback Tenant")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "kb", []string{"d1"})
	if err != nil {
		t.Fatal(err)
	}
	event := &model.KnowledgeOpsEvent{
		RequestID: "request-feedback", TenantID: tenant.ID, UserID: "user-1",
		AppType: "chat", AppID: chat.ID, SessionID: "session-1",
		Question: "如何报销？", QuestionHash: "hash", AnswerExcerpt: "不完整答案",
		Status: model.KnowledgeOpsCompleted, ReviewStatus: model.KnowledgeOpsReviewOpen,
	}
	if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatal(err)
	}

	longComment := strings.Repeat("错", 1200)
	feedback, err := svc.RecordMessageFeedback(ctx, tenant.ID, "user-1", FeedbackRequest{
		ChatID: chat.ID, SessionID: "session-1", MessageID: "message-1",
		RequestID: "request-feedback", Rating: model.FeedbackNegative,
		Attribution: model.FeedbackAttributionKnowledge, Comment: longComment,
	})
	if err != nil {
		t.Fatalf("negative feedback: %v", err)
	}
	updated, err := svc.Store.GetKnowledgeOpsEvent(ctx, tenant.ID, event.ID, false)
	if err != nil || updated == nil {
		t.Fatalf("get event: %+v err=%v", updated, err)
	}
	if len([]rune(feedback.Comment)) != 1024 || len([]rune(updated.FeedbackComment)) != 1024 {
		t.Fatalf("feedback payload budget violated: feedback=%d event=%d",
			len([]rune(feedback.Comment)), len([]rune(updated.FeedbackComment)))
	}
	if updated.FeedbackID != feedback.ID || updated.FeedbackRating != model.FeedbackNegative {
		t.Fatalf("event feedback was not attached: %+v", updated)
	}
	if updated.ReviewStatus != model.KnowledgeOpsReviewOpen || updated.ReviewedBy != "" {
		t.Fatalf("negative feedback must create an open badcase: %+v", updated)
	}

	err = svc.ReviewKnowledgeOpsEvent(ctx, tenant.ID, event.ID, false, repository.KnowledgeOpsReview{
		Status: model.KnowledgeOpsReviewIgnored, Note: longComment, Actor: "reviewer",
	})
	if err != nil {
		t.Fatalf("review event: %v", err)
	}
	afterReview, err := svc.Store.GetKnowledgeOpsEvent(ctx, tenant.ID, event.ID, false)
	if err != nil || afterReview == nil {
		t.Fatalf("get reviewed event: %+v err=%v", afterReview, err)
	}
	if len([]rune(afterReview.ReviewNote)) != 1024 {
		t.Fatalf("review payload budget violated: %d", len([]rune(afterReview.ReviewNote)))
	}
	ignoredFeedback, err := svc.RecordMessageFeedback(ctx, tenant.ID, "user-1", FeedbackRequest{
		ChatID: chat.ID, SessionID: "session-1", MessageID: "message-1",
		RequestID: "request-feedback", Rating: model.FeedbackNegative,
		Attribution: model.FeedbackAttributionTemplate, Comment: "更新",
	})
	if err != nil {
		t.Fatalf("update feedback: %v", err)
	}
	final, err := svc.Store.GetKnowledgeOpsEvent(ctx, tenant.ID, event.ID, false)
	if err != nil || final == nil {
		t.Fatalf("get final event: %+v err=%v", final, err)
	}
	if final.FeedbackID == ignoredFeedback.ID && final.FeedbackComment != "更新" {
		t.Fatalf("feedback fields were not refreshed: %+v", final)
	}
	if final.FeedbackAttribution != model.FeedbackAttributionTemplate {
		t.Fatalf("attribution was not refreshed: %+v", final)
	}
	if final.ReviewStatus != model.KnowledgeOpsReviewIgnored || final.ReviewedBy != "reviewer" {
		t.Fatalf("manual review was reset by feedback: %+v", final)
	}
}

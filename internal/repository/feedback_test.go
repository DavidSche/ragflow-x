package repository

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestUpsertMessageFeedbackIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)

	f := func(rating, comment string) *model.MessageFeedback {
		return &model.MessageFeedback{
			TenantID: "t1", ChatID: "c1", SessionID: "s1", MessageID: "m1", UserID: "u1",
			Rating: rating, Comment: comment, RequestID: "request-1",
		}
	}
	if err := store.UpsertMessageFeedback(ctx, f("positive", "ok")); err != nil {
		t.Fatal(err)
	}
	// Re-submit the same turn with a different rating updates, never duplicates.
	if err := store.UpsertMessageFeedback(ctx, f("negative", "wrong")); err != nil {
		t.Fatal(err)
	}
	list, total, err := store.ListMessageFeedback(ctx, "t1", "c1", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("expected 1 feedback row, got total=%d len=%d", total, len(list))
	}
	if list[0].Rating != "negative" || list[0].Comment != "wrong" || list[0].RequestID != "request-1" {
		t.Fatalf("feedback not updated: %+v", list[0])
	}

	// Tenant scoping: a different tenant must not see it.
	if _, other, err := store.ListMessageFeedback(ctx, "t2", "c1", 1, 20); err != nil {
		t.Fatal(err)
	} else if other != 0 {
		t.Fatalf("cross-tenant feedback leak: total=%d", other)
	}
}

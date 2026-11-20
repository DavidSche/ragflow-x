package service

// doc/301 T12 (QA-1) — the answerability exit.
//
// Three ways this surface can lie, and one test each:
//   1. a missing measurement rendered as a passing one (`nil` is not `answerable`);
//   2. a filter that answers an empty page for an unknown reason, so the reader
//      cannot tell "the filter is right" from "the filter is nonsense";
//   3. a projection column that drifts from the JSON it was copied out of.
//
// The fourth concern — "which turn was it" — is `request_id`: the ledger keys a
// turn by the request that produced it, and the conversation read path already
// resolves that id from the answer snapshot. A claim is therefore only attached when
// the id agrees, never by position.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestAnswerabilityReasonsIsAClosedSet(t *testing.T) {
	reasons := AnswerabilityReasons()
	if len(reasons) != 5 {
		t.Fatalf("expected 5 reasons, got %d: %v", len(reasons), reasons)
	}
	for _, reason := range reasons {
		if !IsAnswerabilityReason(reason) {
			t.Fatalf("%q must be accepted", reason)
		}
	}
	// The empty string is not a reason: it is "no claim recorded", which the UI shows
	// as such. Accepting it as a filter would silently mean "unfiltered".
	for _, bad := range []string{"", "answerable", "no_evidenc", "NONE"} {
		if IsAnswerabilityReason(bad) {
			t.Fatalf("%q must not be accepted as a reason", bad)
		}
	}
}

// TestAttachAnswerabilityClaimProjectsTheColumn pins that one function writes both
// the JSON and the column. Two writers would be two answers waiting to differ, and
// the filter would then disagree with the badge.
func TestAttachAnswerabilityClaimProjectsTheColumn(t *testing.T) {
	fx := newReportFixture(t, "t12-claim")
	svc := fx.svc
	svc.SetKnowledgeOpsConfig(config.KnowledgeOps{
		AnswerabilityEnabled:    true,
		NoEvidenceSimilarity:    0.30,
		MaxCitationsPerDocument: 3,
	})
	event := &model.KnowledgeOpsEvent{}
	svc.attachAnswerabilityClaim(event, "无法从知识库确认该流程。", nil)
	if event.AnswerabilityJSON == "" {
		t.Fatal("claim JSON must be written when evaluation is enabled")
	}
	var claim AnswerabilityClaim
	if err := json.Unmarshal([]byte(event.AnswerabilityJSON), &claim); err != nil {
		t.Fatalf("claim JSON must be readable: %v", err)
	}
	if claim.AbstainReason == "" {
		t.Fatalf("expected a non-empty reason for a refusal answer, got %+v", claim)
	}
	if event.AbstainReason != claim.AbstainReason {
		t.Fatalf("column %q must equal JSON %q", event.AbstainReason, claim.AbstainReason)
	}
}

// TestAttachAnswerabilityClaimLeavesBothEmptyWhenDisabled: a disabled deployment has
// measured nothing, so it must not stamp a reason — an empty column is the honest
// "not recorded" that the UI renders.
func TestAttachAnswerabilityClaimLeavesBothEmptyWhenDisabled(t *testing.T) {
	fx := newReportFixture(t, "t12-claim")
	svc := fx.svc
	svc.SetKnowledgeOpsConfig(config.KnowledgeOps{AnswerabilityEnabled: false})
	event := &model.KnowledgeOpsEvent{}
	svc.attachAnswerabilityClaim(event, "无法从知识库确认该流程。", nil)
	if event.AnswerabilityJSON != "" || event.AbstainReason != "" {
		t.Fatalf("disabled evaluation must write nothing, got json=%q reason=%q",
			event.AnswerabilityJSON, event.AbstainReason)
	}
}

// TestAnswerabilityAttachedByRequestID: the claim lands on the turn whose request id
// matches, and a turn with no ledger row stays nil rather than borrowing a neighbour.
func TestAnswerabilityAttachedByRequestID(t *testing.T) {
	fx := newReportFixture(t, "t12-turn")
	svc, ctx := fx.svc, context.Background()
	tenantID := fx.tenantID
	for _, event := range []*model.KnowledgeOpsEvent{
		{
			RequestID: "req-recorded", TenantID: tenantID, UserID: "u1", AppType: "chat",
			AppID: "chat-1", SessionID: "s1", Question: "q", QuestionHash: "h1",
			Status:            model.KnowledgeOpsCompleted,
			AnswerabilityJSON: `{"answerable":false,"confidence":0,"citation_count":0,"distinct_docs":0,"abstain_reason":"no_evidence"}`,
			AbstainReason:     AbstainReasonNoEvidence,
		},
	} {
		if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}

	messages := []SessionMessage{
		{Message: ragflow.Message{Role: "assistant", Content: "a1"}, RequestID: "req-recorded"},
		{Message: ragflow.Message{Role: "assistant", Content: "a2"}, RequestID: "req-unknown"},
		{Message: ragflow.Message{Role: "assistant", Content: "a3"}},
	}
	svc.attachAnswerability(ctx, tenantID, messages)
	if messages[0].Answerability == nil {
		t.Fatal("the recorded turn must carry its claim")
	}
	if reasons := messages[0].Answerability.AbstainReason; reasons != AbstainReasonNoEvidence {
		t.Fatalf("unexpected reason: %q", reasons)
	}
	// A turn the ledger never saw, and a turn with no request id at all: both are
	// "not recorded". Filling either in would be inventing a measurement.
	if messages[1].Answerability != nil || messages[2].Answerability != nil {
		t.Fatalf("unrecorded turns must stay nil: %+v / %+v", messages[1].Answerability, messages[2].Answerability)
	}
}

// TestAnswerabilityIsTenantScoped: a request id is not a capability. The same id in
// another tenant must not leak a claim across the boundary.
func TestAnswerabilityIsTenantScoped(t *testing.T) {
	fx := newReportFixture(t, "t12-scope")
	svc, ctx := fx.svc, context.Background()
	ownerID := fx.tenantID
	stranger, err := svc.CreateTenant(ctx, "t12-stranger")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	event := &model.KnowledgeOpsEvent{
		RequestID: "req-shared", TenantID: ownerID, UserID: "u1", AppType: "chat",
		AppID: "chat-1", SessionID: "s1", Question: "q", QuestionHash: "h1",
		Status:            model.KnowledgeOpsCompleted,
		AnswerabilityJSON: `{"answerable":true,"abstain_reason":"none"}`,
		AbstainReason:     AbstainReasonNone,
	}
	if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	claims, err := svc.Store.AnswerabilityByRequestIDs(ctx, stranger.ID, []string{"req-shared"})
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("cross-tenant read must be empty, got %v", claims)
	}
	messages := []SessionMessage{{Message: ragflow.Message{Role: "assistant"}, RequestID: "req-shared"}}
	svc.attachAnswerability(ctx, stranger.ID, messages)
	if messages[0].Answerability != nil {
		t.Fatal("a foreign tenant's claim must not be attached")
	}
}

// TestKnowledgeOpsEventFilterByAbstainReason: the filter is applied in SQL (so the
// total is right), and an unknown reason is a 400 rather than an empty page.
func TestKnowledgeOpsEventFilterByAbstainReason(t *testing.T) {
	fx := newReportFixture(t, "t12-filter")
	svc, ctx := fx.svc, context.Background()
	tenantID := fx.tenantID
	seed := []*model.KnowledgeOpsEvent{
		{RequestID: "r1", TenantID: tenantID, UserID: "u1", AppType: "chat", AppID: "c1",
			Question: "q1", QuestionHash: "h1", Status: model.KnowledgeOpsCompleted, AbstainReason: AbstainReasonNoEvidence},
		{RequestID: "r2", TenantID: tenantID, UserID: "u1", AppType: "chat", AppID: "c1",
			Question: "q2", QuestionHash: "h2", Status: model.KnowledgeOpsCompleted, AbstainReason: AbstainReasonLowSimilarity},
		{RequestID: "r3", TenantID: tenantID, UserID: "u1", AppType: "chat", AppID: "c1",
			Question: "q3", QuestionHash: "h3", Status: model.KnowledgeOpsCompleted},
	}
	for _, event := range seed {
		if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}

	events, total, err := svc.ListKnowledgeOpsEvents(ctx, tenantID, false, 1, 20, repository.KnowledgeOpsFilter{
		ReviewStatus:  "open",
		AbstainReason: AbstainReasonNoEvidence,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// `total` counts the filtered set: if the filter ran in Go after the query, the
	// page would hold one row but the total would still say three.
	if total != 1 || len(events) != 1 || events[0].RequestID != "r1" {
		t.Fatalf("expected exactly r1, got total=%d rows=%+v", total, events)
	}

	_, _, err = svc.ListKnowledgeOpsEvents(ctx, tenantID, false, 1, 20, repository.KnowledgeOpsFilter{
		AbstainReason: "no_evidenc",
	})
	business, ok := err.(*httperr.Error)
	if !ok || business.Code != 40096 {
		t.Fatalf("expected 40096 for an unknown reason, got %v", err)
	}
}

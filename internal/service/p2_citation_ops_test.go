package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// TestCitationPositionsJSONExtraction covers P2 citation positioning: locator
// keys the engine actually emits are preserved verbatim, and absent locators
// yield an empty string instead of an invented one.
func TestCitationPositionsJSONExtraction(t *testing.T) {
	cases := []struct {
		name   string
		item   map[string]interface{}
		expect string
	}{
		{name: "positions and bbox preserved", item: map[string]interface{}{
			"document_id": "doc-1",
			"content":     "x",
			"positions":   []interface{}{[]interface{}{1.0, 2.0}},
			"bbox":        []interface{}{0.1, 0.2, 0.3, 0.4},
		}, expect: `{"bbox":[0.1,0.2,0.3,0.4],"positions":[[1,2]]}`},
		{name: "page and line keys", item: map[string]interface{}{
			"page": 3.0, "line_start": 42.0, "line_end": 44.0,
		}, expect: `{"line_end":44,"line_start":42,"page":3}`},
		{name: "no locator keys yields empty", item: map[string]interface{}{
			"document_id": "doc-1", "content": "x", "similarity": 0.9,
		}, expect: ""},
		{name: "nil locator values ignored", item: map[string]interface{}{
			"positions": nil, "page": 1.0,
		}, expect: `{"page":1}`},
	}
	for _, tc := range cases {
		got := citationPositionsJSON(tc.item)
		if got != tc.expect {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.expect)
		}
	}
}

// TestMaxReferenceSimilarityAndApply covers the P2 operations signal: the top
// retrieval similarity is picked across references (with a score alias for
// version tolerance) and annotates the event additively.
func TestMaxReferenceSimilarityAndApply(t *testing.T) {
	if got := maxReferenceSimilarity(nil); got != 0 {
		t.Fatalf("nil references must yield 0, got %v", got)
	}
	refs := []map[string]interface{}{
		{"document_id": "a", "similarity": 0.42},
		{"document_id": "b", "score": 0.78},
		{"document_id": "c", "similarity": 0.55},
	}
	if got := maxReferenceSimilarity(refs); got != 0.78 {
		t.Fatalf("expected top similarity 0.78, got %v", got)
	}
	// score is only an alias when similarity is absent.
	refsScoreOnly := []map[string]interface{}{{"score": 0.9}}
	if got := maxReferenceSimilarity(refsScoreOnly); got != 0.9 {
		t.Fatalf("expected score alias 0.9, got %v", got)
	}

	event := &model.KnowledgeOpsEvent{RequestID: "r1"}
	applyReferenceMetrics(event, refs)
	if event.TopSimilarity != 0.78 {
		t.Fatalf("expected annotated top similarity 0.78, got %v", event.TopSimilarity)
	}
	// No measurable similarity must never zero-out or overwrite existing data.
	applyReferenceMetrics(event, nil)
	if event.TopSimilarity != 0.78 {
		t.Fatalf("nil reference must not change an annotated event, got %v", event.TopSimilarity)
	}
	applyReferenceMetrics(nil, refs) // nil event safe
}

// TestListChatReferencesOwnershipAndOrder covers the P2 citation drawer data
// plane: the chat must be owned by the tenant, and citations come back in
// reference-index order with locator positions preserved.
func TestListChatReferencesOwnershipAndOrder(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "RefTenant")
	if err != nil {
		t.Fatal(err)
	}
	ua, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "refadmin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}

	other, err := svc.CreateTenant(ctx, "OtherTenant")
	if err != nil {
		t.Fatal(err)
	}
	_ = other

	chat := &model.ChatShadow{
		ID: "chat-ref-1", TenantID: ta.ID, Name: "refs", Status: "active", OwnerID: ua.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.UpsertChatShadow(ctx, chat); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	seed := []model.CitationReference{
		{ID: "ref-1", TenantID: ta.ID, ChatID: chat.ID, SessionID: "s1", RequestID: "req-1", DatasetID: "ds-1", DocumentID: "doc-a", ChunkID: "c-1", Index: 0, Positions: `{"page":1}`, RetrievedAt: now},
		{ID: "ref-2", TenantID: ta.ID, ChatID: chat.ID, SessionID: "s1", RequestID: "req-1", DatasetID: "ds-1", DocumentID: "doc-b", ChunkID: "c-2", Index: 1, Positions: `{"page":2}`, RetrievedAt: now.Add(time.Second)},
	}
	for i := range seed {
		if err := svc.Store.CreateCitationReference(ctx, &seed[i]); err != nil {
			t.Fatalf("seed citation %d: %v", i, err)
		}
	}

	rows, err := svc.ListChatReferences(ctx, ta.ID, chat.ID, "s1")
	if err != nil {
		t.Fatalf("list chat references: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 references, got %d", len(rows))
	}
	for i, row := range rows {
		if row.Index != i {
			t.Fatalf("reference %d must carry index %d, got %d", i, i, row.Index)
		}
		if row.Positions == "" {
			t.Fatalf("reference %d must preserve locator positions", i)
		}
	}

	// Cross-tenant chat must be rejected before any read.
	if _, err := svc.ListChatReferences(ctx, other.ID, chat.ID, "s1"); err == nil {
		t.Fatal("cross-tenant chat reference read must fail")
	}

	// Unknown chat fails with a not-found contract error.
	_, err = svc.ListChatReferences(ctx, ta.ID, "chat-missing", "")
	if err == nil {
		t.Fatal("missing chat must fail")
	}
	if herr, ok := err.(*httperr.Error); !ok || herr.Status != 404 {
		t.Fatalf("expected httperr 404, got %v", err)
	}
}

// TestKnowledgeOpsSummaryThresholdAndTopSimilarity covers the P2 operations
// board: avg top-similarity is aggregated from annotated events and the
// operator-tunable thresholds (config.knowledge_ops) flag crossing windows.
func TestKnowledgeOpsSummaryThresholdAndTopSimilarity(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetKnowledgeOpsConfig(config.KnowledgeOps{AbstainRateWarn: 0.10, CitationRateBaseline: 0.80, TopSimilarityFloor: 0.40})

	ta, err := svc.CreateTenant(ctx, "OpsTenant")
	if err != nil {
		t.Fatal(err)
	}

	seed := func(i int, answer, status string, cited int64, topSim float64) {
		event := knowledgeEvent(ta.ID, "u1", "chat", "chat-ops", "s1", "req-0"+string(rune('0'+i)), "题", answer, cited, 10, 20, 300)
		event.Status = status
		event.TopSimilarity = topSim
		if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	// 4 turns: 3 completed with citations (top sim 0.5, 0.45, 0.38), 1 no_answer.
	seed(1, "有据答案", model.KnowledgeOpsCompleted, 2, 0.5)
	seed(2, "有据答案2", model.KnowledgeOpsCompleted, 1, 0.45)
	seed(3, "略低相似", model.KnowledgeOpsCompleted, 1, 0.38)
	seed(4, "", model.KnowledgeOpsNoAnswer, 0, 0)

	summary, err := svc.KnowledgeOpsSummary(ctx, ta.ID, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalTurns != 4 {
		t.Fatalf("expected 4 turns, got %d", summary.TotalTurns)
	}
	// avg of {0.5, 0.45, 0.38} = 0.44333 ; no_answer 0 excluded.
	if want := 0.44333; summary.AvgTopSimilarity < want-0.001 || summary.AvgTopSimilarity > want+0.001 {
		t.Fatalf("expected avg top similarity ~0.443, got %v", summary.AvgTopSimilarity)
	}
	if got := summary.NoAnswerRate; got != 0.25 {
		t.Fatalf("expected no-answer rate 0.25, got %v", got)
	}
	if got := summary.CitationRate; got != 0.75 {
		t.Fatalf("expected citation rate 0.75, got %v", got)
	}
	// 0.25 > abstain 0.10 -> flag; 0.75 < citation baseline 0.80 -> flag;
	// 0.443 > floor 0.40 -> no similarity flag.
	if !summary.AbstainAboveWarn {
		t.Fatal("abstain 0.25 must flag above warn 0.10")
	}
	if !summary.CitationBelowBaseline {
		t.Fatal("citation 0.75 must flag below baseline 0.80")
	}
	if summary.AvgTopSimBelowFloor {
		t.Fatal("avg top similarity 0.443 must NOT flag below floor 0.40")
	}
	if summary.AbstainRateWarn != 0.10 || summary.CitationRateBaseline != 0.80 || summary.TopSimilarityFloor != 0.40 {
		t.Fatalf("thresholds must be echoed verbatim, got %+v", summary)
	}

	// Raise the floor above the observed average to prove the flag flips.
	svc.SetKnowledgeOpsConfig(config.KnowledgeOps{AbstainRateWarn: 0.10, CitationRateBaseline: 0.80, TopSimilarityFloor: 0.50})
	summary2, err := svc.KnowledgeOpsSummary(ctx, ta.ID, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !summary2.AvgTopSimBelowFloor {
		t.Fatal("avg top similarity 0.443 must flag below floor 0.50")
	}
}

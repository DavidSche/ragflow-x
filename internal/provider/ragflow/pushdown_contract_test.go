package ragflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestMetadataConditionSerialization pins the wire shape of the retrieval
// pushdown condition (doc/123 §2.1/§6.1): RAGFlow expects
// {logic, conditions:[{name, comparison_operator, value}]}.
func TestMetadataConditionSerialization(t *testing.T) {
	condition := &MetadataCondition{
		Logic: "and",
		Conditions: []MetadataConditionOp{
			{Name: "rgx_sensitivity", ComparisonOperator: "!=", Value: "restricted"},
			{Name: "rgx_sensitivity", ComparisonOperator: "!=", Value: "confidential"},
		},
	}
	data, err := json.Marshal(map[string]interface{}{"metadata_condition": condition})
	if err != nil {
		t.Fatal(err)
	}
	payload := string(data)
	for _, want := range []string{`"logic":"and"`, `"name":"rgx_sensitivity"`, `"comparison_operator":"!="`, `"value":"restricted"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("serialized condition missing %s: %s", want, payload)
		}
	}
}

// TestCompletionRequestCarriesExtraBody pins that ExtraBody wins for the
// OpenAI-compatible channel and the top-level field stays usable for the
// unified endpoint (doc/123 §6.1).
func TestCompletionRequestCarriesExtraBody(t *testing.T) {
	condition := &MetadataCondition{Logic: "or", Conditions: []MetadataConditionOp{
		{Name: "rgx_sensitivity", ComparisonOperator: "=", Value: "internal"},
	}}

	// Top-level form.
	top := CompletionRequest{MetadataCondition: condition}
	data, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"metadata_condition":{"logic":"or"`) {
		t.Fatalf("top-level condition not serialized: %s", data)
	}

	// ExtraBody form: top-level nil, so only extra_body carries it.
	extra := CompletionRequest{ExtraBody: &CompletionExtraBody{MetadataCondition: condition}}
	data, err = json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(data)
	if !strings.Contains(payload, `"extra_body":{"metadata_condition":`) {
		t.Fatalf("extra_body condition not serialized: %s", payload)
	}
	if strings.Count(payload, "metadata_condition") != 1 {
		t.Fatalf("condition must not leak to top level when ExtraBody set: %s", payload)
	}
}

// TestEffectiveMetadataConditionPrecedence locks the precedence rule used by
// the mock capture.
func TestEffectiveMetadataConditionPrecedence(t *testing.T) {
	top := &MetadataCondition{Conditions: []MetadataConditionOp{{Name: "a", ComparisonOperator: "="}}}
	extra := &MetadataCondition{Conditions: []MetadataConditionOp{{Name: "b", ComparisonOperator: "="}}}

	req := CompletionRequest{MetadataCondition: top}
	if got := effectiveMetadataCondition(req); got != top {
		t.Fatalf("top-level condition lost: %+v", got)
	}
	req.ExtraBody = &CompletionExtraBody{MetadataCondition: extra}
	if got := effectiveMetadataCondition(req); got != extra {
		t.Fatalf("extra_body must win: %+v", got)
	}
}

// TestMockCapturesPushdownCondition proves the mock surfaces the last
// condition a chat/search completion carried, which the service-layer tests
// rely on.
func TestMockCapturesPushdownCondition(t *testing.T) {
	ctx := context.Background()
	mock := NewMock()
	if _, err := mock.CreateDataset(ctx, CreateDatasetRequest{Name: "ds"}); err != nil {
		t.Fatal(err)
	}
	chat, err := mock.CreateChat(ctx, CreateChatRequest{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	condition := &MetadataCondition{Logic: "and", Conditions: []MetadataConditionOp{
		{Name: "rgx_sensitivity", ComparisonOperator: "!=", Value: "restricted"},
	}}
	if _, err := mock.ChatCompletion(ctx, chat.ID, CompletionRequest{MetadataCondition: condition}); err != nil {
		t.Fatal(err)
	}
	got := mock.LastChatMetadataCondition()
	if got == nil || len(got.Conditions) != 1 || got.Conditions[0].Value != "restricted" {
		t.Fatalf("mock lost chat condition: %+v", got)
	}
	searchApp, err := mock.CreateSearchApp(ctx, CreateSearchAppRequest{Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mock.SearchAppCompletion(ctx, searchApp, SearchAppCompletionRequest{Question: "q", MetadataCondition: condition}); err != nil {
		t.Fatal(err)
	}
	got = mock.LastSearchMetadataCondition()
	if got == nil || len(got.Conditions) != 1 {
		t.Fatalf("mock lost search condition: %+v", got)
	}
}

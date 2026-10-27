package service

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestDecodePlannerSubqueriesExtractsJSONFromProse(t *testing.T) {
	response := &ragflow.CompletionResponse{Choices: []ragflow.CompletionChoice{{
		Message: ragflow.Message{
			Role:    "assistant",
			Content: "The plan is {\"sub_queries\":[{\"intent\":\"metric\",\"entity\":\"rate\",\"time_range\":\"Q1\",\"confidence\":1,\"tool_input\":{\"key\":\"value\"}}]}",
		},
	}}}
	subqueries, err := decodePlannerSubqueries(response)
	if err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(subqueries) != 1 || subqueries[0].Intent != "metric" {
		t.Fatalf("unexpected plan: %+v", subqueries)
	}
}

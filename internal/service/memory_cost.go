package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const memoryDispatchEstimationPolicy = "memory-dispatch-character-estimate-v1"

func estimateMemoryDispatchTokens(value string) int64 {
	if value == "" {
		return 0
	}
	return int64((len(value) + 3) / 4)
}

func (s *Service) recordMemoryDispatchCostMetric(
	ctx context.Context, operation *model.MemoryOperation, payload ragflow.AddMessageRequest,
) {
	if operation == nil {
		return
	}
	_, err := s.Store.RecordCostMetric(ctx, &model.CostMetric{
		RequestID: "memory:" + operation.ID, TenantID: operation.TenantID,
		UserID: operation.UserID, Date: time.Now().UTC().Format("2006-01-02"),
		Model: "ragflow-memory", Scenario: "memory",
		ProjectID: operation.ProjectID, XAssistantID: operation.AssistantID,
		TokensIn:  estimateMemoryDispatchTokens(payload.UserInput),
		TokensOut: estimateMemoryDispatchTokens(payload.AgentResponse),
		Estimated: true, EstimationPolicyVersion: memoryDispatchEstimationPolicy,
	})
	if err != nil {
		logger.Warn("memory dispatch cost metric failed",
			"operation_id", operation.ID, "error", err,
		)
	}
}

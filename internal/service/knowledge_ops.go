package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// KnowledgeOpsScopeAll permits explicitly authorized governance readers to
// inspect tenant metrics.
func (s *Service) KnowledgeOpsScopeAll(ctx context.Context, actorID string) bool {
	return s.Authorize(ctx, actorID, "governance.read", "tenant") == nil
}

// KnowledgeOpsSummary returns the aggregate knowledge quality KPIs.
func (s *Service) KnowledgeOpsSummary(
	ctx context.Context, tenantID string, scopeAll bool,
	dateFrom, dateTo, datasetID string,
) (*model.KnowledgeOpsSummary, error) {
	summary, err := s.Store.KnowledgeOpsSummary(ctx, tenantID, scopeAll, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.SummarizeCostAttribution(ctx, tenantID, scopeAll, dateFrom, dateTo, datasetID)
	if err != nil {
		return nil, err
	}
	for index := range rows {
		used := rows[index].TokensIn + rows[index].TokensOut
		rows[index].UsedTokens = used
		switch {
		case rows[index].DatasetID != "":
			dataset, datasetErr := s.Store.GetDatasetLink(ctx, tenantID, rows[index].DatasetID)
			if datasetErr != nil {
				return nil, datasetErr
			}
			if dataset != nil {
				rows[index].BudgetTokenLimit = dataset.CompilationTokenBudget
			}
		case rows[index].ProfileID == "" && rows[index].RunID == "":
			tenant, tenantErr := s.Store.GetTenant(ctx, rows[index].TenantID)
			if tenantErr != nil {
				return nil, tenantErr
			}
			if tenant != nil {
				rows[index].BudgetTokenLimit = tenant.CompilationTokenBudget
			}
		}
		switch {
		case rows[index].BudgetTokenLimit <= 0:
			rows[index].Status = "unlimited"
		case used > rows[index].BudgetTokenLimit:
			rows[index].Status = "exceeded"
		case used*5 >= rows[index].BudgetTokenLimit*4:
			rows[index].Status = "warning"
		default:
			rows[index].Status = "ok"
		}
	}
	summary.CostAttribution = rows
	return summary, nil
}

// TopKnowledgeQueries returns the most frequently asked normalized questions.
func (s *Service) TopKnowledgeQueries(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string, limit int) ([]model.KnowledgeOpsQueryRow, error) {
	return s.Store.TopKnowledgeQueries(ctx, tenantID, scopeAll, dateFrom, dateTo, limit)
}

// ListKnowledgeOpsEvents returns a page of badcase candidates and outcomes.
func (s *Service) ListKnowledgeOpsEvents(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.KnowledgeOpsFilter) ([]model.KnowledgeOpsEvent, int64, error) {
	return s.Store.ListKnowledgeOpsEvents(ctx, tenantID, scopeAll, page, pageSize, filter)
}

// ReviewKnowledgeOpsEvent records a badcase disposition. This is the closure
// step that lets an operator distinguish fixed, accepted and still-open issues.
func (s *Service) ReviewKnowledgeOpsEvent(ctx context.Context, tenantID, eventID string, scopeAll bool, review repository.KnowledgeOpsReview) error {
	switch review.Status {
	case model.KnowledgeOpsReviewOpen, model.KnowledgeOpsReviewResolved, model.KnowledgeOpsReviewIgnored:
	default:
		return httperr.BadRequest(40095, "review status must be open, resolved or ignored")
	}
	review.Note = truncateKnowledgeText(review.Note, 1024)
	updated, err := s.Store.ReviewKnowledgeOpsEvent(ctx, tenantID, eventID, scopeAll, review)
	if err != nil {
		return err
	}
	if !updated {
		return httperr.NotFound("knowledge ops event not found")
	}
	return nil
}

func normalizeKnowledgeQuestion(question string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(question)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func truncateKnowledgeText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	if limit <= len("...") {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-len("...")])) + "..."
}

func lastUserQuestion(messages []ragflow.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(messages[i].Role, "user") && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func knowledgeStatus(answer string) string {
	if strings.TrimSpace(answer) == "" {
		return model.KnowledgeOpsNoAnswer
	}
	return model.KnowledgeOpsCompleted
}

func knowledgeEvent(
	tenantID, userID, appType, appID, sessionID, requestID, question, answer string,
	citations, tokensIn, tokensOut int64,
	durationMs ...int64,
) *model.KnowledgeOpsEvent {
	answer = visibleRAGFlowAnswer(answer)
	if requestID == "" {
		requestID = id.New()
	}
	event := &model.KnowledgeOpsEvent{
		RequestID: requestID, TenantID: tenantID, UserID: userID,
		AppType: appType, AppID: appID, SessionID: sessionID,
		Question:       truncateKnowledgeText(question, 2048),
		QuestionHash:   normalizeKnowledgeQuestion(question),
		AnswerExcerpt:  truncateKnowledgeText(answer, 1024),
		Status:         knowledgeStatus(answer),
		CitationsCount: int(citations), TokensIn: tokensIn, TokensOut: tokensOut,
	}
	if len(durationMs) > 0 && durationMs[0] > 0 {
		event.DurationMs = durationMs[0]
	}
	return event
}

func (s *Service) recordKnowledgeEvent(ctx context.Context, event *model.KnowledgeOpsEvent) {
	if event == nil {
		return
	}
	if err := s.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		// Observation must never break a completion. Failures are visible in logs.
		logger.Warn("knowledge ops event write failed", "request_id", event.RequestID, "error", err)
	} else if err := s.Store.UpsertTraceRun(ctx, knowledgeOpsTraceRun(ctx, event)); err != nil {
		logger.Warn("trace run projection failed", "request_id", event.RequestID, "error", err)
	}
}

func citationCountFromMaps(values []map[string]interface{}) int64 {
	if len(values) == 0 {
		return 0
	}
	return int64(len(values))
}

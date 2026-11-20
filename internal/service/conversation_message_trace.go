package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type SessionMessage struct {
	ragflow.Message
	RequestID string `json:"request_id,omitempty"`
	// Answerability is the per-turn honesty claim from the knowledge-ops ledger
	// (doc/301 T12). `nil` means the ledger has no row for this turn — a turn that
	// predates the claim, or one that never reached the ledger at all. The UI shows
	// that as "not recorded"; it must never be rendered as "answerable", because a
	// missing measurement is not a passing measurement.
	Answerability *AnswerabilityClaim `json:"answerability,omitempty"`
}

func answerTraceKey(question, answer string) string {
	return sha256Hex(question + "\x1f" + answer)
}

func (s *Service) traceSessionMessages(
	ctx context.Context, tenantID, assistantID, sessionID string, items []ragflow.Message,
) []SessionMessage {
	traced := make([]SessionMessage, len(items))
	for index, item := range items {
		traced[index] = SessionMessage{Message: item}
	}
	if sessionID == "" {
		return traced
	}

	runs, err := s.Store.ListAnswerRunSnapshots(ctx, tenantID, assistantID, sessionID)
	if err != nil {
		logger.Warn("answer snapshot trace lookup failed",
			"tenant_id", tenantID, "assistant_id", assistantID,
			"session_id", sessionID, "error", err)
		return traced
	}
	pending := make(map[string][]repository.AnswerRunSnapshot)
	for _, run := range runs {
		key := answerTraceKey(run.Snapshot.UserMessage, run.Snapshot.Content)
		pending[key] = append(pending[key], run)
	}

	previousQuestion := ""
	for index := range traced {
		switch traced[index].Role {
		case "user":
			previousQuestion = traced[index].Content
		case "assistant":
			if previousQuestion == "" {
				continue
			}
			key := answerTraceKey(previousQuestion, traced[index].Content)
			queue := pending[key]
			if len(queue) == 0 {
				continue
			}
			// T6 (S14): rc3 sessions no longer echo references on GET; rehydrate
			// the citations captured at completion time from the matching answer
			// snapshot, keyed by the same question+answer hash as RequestID.
			run := queue[0]
			traced[index].RequestID = run.Run.RequestID
			if citations := answerCitationsFromSnapshot(run.Snapshot.CitationsJSON); len(citations) > 0 {
				traced[index].Citations = citations
			}
			pending[key] = queue[1:]
		}
	}
	s.attachAnswerability(ctx, tenantID, traced)
	return traced
}

// attachAnswerability joins the ledger's per-turn claim onto the assistant turns of
// one page (doc/301 T12). It is best-effort in the same sense as the citation trace:
// a ledger read failure leaves `Answerability` nil, which renders as "not recorded"
// rather than breaking the conversation.
func (s *Service) attachAnswerability(ctx context.Context, tenantID string, messages []SessionMessage) {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.RequestID != "" {
			ids = append(ids, message.RequestID)
		}
	}
	if len(ids) == 0 {
		return
	}
	claims, err := s.Store.AnswerabilityByRequestIDs(ctx, tenantID, ids)
	if err != nil {
		logger.Warn("answerability trace lookup failed",
			"tenant_id", tenantID, "turns", len(ids), "error", err)
		return
	}
	for index := range messages {
		raw, ok := claims[messages[index].RequestID]
		if !ok {
			continue
		}
		var claim AnswerabilityClaim
		// A claim we cannot read is not a claim we may invent.
		if err := json.Unmarshal([]byte(raw), &claim); err != nil {
			continue
		}
		messages[index].Answerability = &claim
	}
}

// answerCitationsFromSnapshot converts the completion-time AnswerCitation
// records (persisted in the answer snapshot) back into the raw chunk maps that
// Message.Citations carries and the conversation center renders.
func answerCitationsFromSnapshot(citJSON string) []map[string]interface{} {
	if strings.TrimSpace(citJSON) == "" {
		return nil
	}
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(citJSON), &citations); err != nil || len(citations) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(citations))
	for _, citation := range citations {
		name := citation.Title
		if name == "" {
			name = "引用"
		}
		out = append(out, map[string]interface{}{
			"id":            citation.ID,
			"chunk_id":      citation.ChunkID,
			"document_id":   citation.DocumentID,
			"doc_id":        citation.DocumentID,
			"document_name": name,
			"docnm_kwd":     name,
			"content":       citation.CitedContentExcerpt,
			"dataset_id":    citation.DatasetID,
			"location":      citation.CitationLocator,
		})
	}
	return out
}

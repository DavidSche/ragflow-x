package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	memoryCompensationVisibilityGrace = 2 * time.Minute
	memoryCompensationLease           = 5 * time.Minute
	memoryCompensationInterval        = time.Minute
	memoryCompensationPageSize        = 100
	memoryCompensationMaxPages        = 10
)

type MemoryCompensationSummary struct {
	Scanned     int
	Compensated int
	Absent      int
	Pending     int
	Skipped     int
}

func (s *Service) ProcessMemoryCompensations(ctx context.Context, limit int) (MemoryCompensationSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	now := time.Now().UTC()
	owner := "memory-compensation:" + id.New()
	operations, err := s.Store.ClaimDueMemoryCompensationOperations(
		ctx, now.Add(-memoryCompensationVisibilityGrace), now,
		now.Add(memoryCompensationLease), owner, limit,
	)
	if err != nil {
		return MemoryCompensationSummary{}, err
	}

	summary := MemoryCompensationSummary{Scanned: len(operations)}
	var failures []error
	for index := range operations {
		operation := &operations[index]
		compensated, err := s.compensateMemoryOperation(ctx, operation, owner, now)
		if err != nil {
			failures = append(failures, err)
			obs.Get().AddMemoryTombstoneVerification("failed")
			s.emitMemoryCompensationFailed(operation, err)
			continue
		}
		switch compensated {
		case "compensated":
			summary.Compensated++
			// External content removed after the tombstone: enforcement verified.
			obs.Get().AddMemoryTombstoneVerification("verified")
		case "absent":
			summary.Absent++
			// External scan proved the content is already invisible.
			obs.Get().AddMemoryTombstoneVerification("absent")
		case "pending":
			summary.Pending++
			obs.Get().AddMemoryTombstoneVerification("pending")
		default:
			summary.Skipped++
			obs.Get().AddMemoryTombstoneVerification("skipped")
		}
	}
	obs.Get().AddMemoryCompensation(
		int64(summary.Scanned), int64(summary.Compensated), int64(summary.Absent),
		int64(summary.Pending), int64(summary.Skipped), int64(len(failures)),
	)
	return summary, errors.Join(failures...)
}

func (s *Service) emitMemoryCompensationFailed(operation *model.MemoryOperation, err error) {
	notify.Emit(context.Background(), notify.Event{
		ID:       "memory-compensation-failed:" + operation.ID,
		TenantID: operation.TenantID, Resource: "memory-operation", ResourceID: operation.ID,
		Type: "memory.compensation.failed", Severity: "error",
		Title:  "Memory tombstone compensation failed",
		Detail: "Provider or reconciliation failure; no content is included",
		Fields: map[string]string{
			"operation_id": operation.ID, "request_id": operation.RequestID,
			"policy_id": operation.PolicyID, "state": operation.State,
		},
	})
}

func (s *Service) compensateMemoryOperation(
	ctx context.Context, operation *model.MemoryOperation, owner string, now time.Time,
) (string, error) {
	matches, complete, err := s.findMemoryExternalMessages(ctx, operation)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 && complete {
		if operation.RagflowMessageID != "" {
			providerErr := s.deleteRememberedExternalMessage(ctx, operation, operation.RagflowMessageID)
			if providerErr != nil {
				return "", providerErr
			}
			return s.completeMemoryCompensation(
				ctx, operation, owner, operation.RagflowMessageID,
				"memory_tombstone_compensated", now,
			)
		}
		return s.completeMemoryCompensation(
			ctx, operation, owner, "",
			"memory_tombstone_external_absent", now,
		)
	}
	if len(matches) == 0 {
		return "pending", nil
	}

	target := matches[0]
	if operation.RagflowMessageID != "" {
		rememberedID, parseErr := strconv.ParseInt(operation.RagflowMessageID, 10, 64)
		if parseErr != nil || rememberedID <= 0 {
			return "", errors.New("invalid remembered memory message id")
		}
		for _, message := range matches {
			if message.MessageID == rememberedID {
				target = message
				break
			}
		}
	}
	if err := s.RAGFlow.DeleteMemoryMessage(ctx, operation.MemoryID, target.MessageID); err != nil {
		return "", err
	}
	return s.completeMemoryCompensation(
		ctx, operation, owner, strconv.FormatInt(target.MessageID, 10),
		"memory_tombstone_compensated", now,
	)
}

func (s *Service) findMemoryExternalMessages(
	ctx context.Context, operation *model.MemoryOperation,
) ([]ragflow.MemoryMessage, bool, error) {
	matches := make([]ragflow.MemoryMessage, 0, 1)
	for page := 1; page <= memoryCompensationMaxPages; page++ {
		messages, err := s.RAGFlow.ListMemoryMessagesFiltered(ctx, operation.MemoryID, ragflow.MemoryMessageListFilter{
			AgentID: operation.AssistantID, SessionID: operation.SessionID,
			Page: page, PageSize: memoryCompensationPageSize,
		})
		if err != nil {
			return nil, false, err
		}
		for _, message := range messages {
			if message.MemoryID != operation.MemoryID || message.UserID != operation.RagflowAttributionUserID {
				continue
			}
			if !memoryMessageActive(message) {
				continue
			}
			hash, err := s.memoryMessageExternalHash(ctx, operation.MemoryID, message)
			if err != nil {
				return nil, false, err
			}
			if hash == operation.ExternalMessageHash {
				matches = append(matches, message)
			}
		}
		if len(messages) < memoryCompensationPageSize {
			return matches, true, nil
		}
	}
	return matches, false, nil
}

func (s *Service) deleteRememberedExternalMessage(
	ctx context.Context, operation *model.MemoryOperation, messageID string,
) error {
	externalID, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil || externalID <= 0 {
		return errors.New("invalid remembered memory message id")
	}
	return s.RAGFlow.DeleteMemoryMessage(ctx, operation.MemoryID, externalID)
}

func (s *Service) completeMemoryCompensation(
	ctx context.Context, operation *model.MemoryOperation, owner, messageID, reason string, now time.Time,
) (string, error) {
	completed, err := s.Store.CompleteMemoryCompensationOperation(
		ctx, operation.TenantID, operation.ID, owner, messageID, reason, now,
	)
	if err != nil {
		return "", err
	}
	if !completed {
		return "skipped", nil
	}
	if reason == "memory_tombstone_compensated" {
		return "compensated", nil
	}
	return "absent", nil
}

func (s *Service) memoryMessageExternalHash(
	ctx context.Context, memoryID string, message ragflow.MemoryMessage,
) (string, error) {
	if message.UserInput != "" || message.AgentResponse != "" {
		return memoryExternalContentHash(s.HMACKey, memoryID, message.UserInput, message.AgentResponse), nil
	}
	raw, err := s.RAGFlow.GetMemoryMessageContent(ctx, memoryID, message.MessageID)
	if err != nil {
		return "", err
	}
	var payload struct {
		UserInput     string `json:"user_input"`
		AgentResponse string `json:"agent_response"`
		Content       string `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	userInput, agentResponse := payload.UserInput, payload.AgentResponse
	if userInput == "" && agentResponse == "" {
		userInput, agentResponse, err = parseRawMemoryContent(payload.Content)
		if err != nil {
			return "", err
		}
	}
	return memoryExternalContentHash(s.HMACKey, memoryID, userInput, agentResponse), nil
}

func parseRawMemoryContent(content string) (string, string, error) {
	const prefix = "User Input: "
	if !strings.HasPrefix(content, prefix) {
		return "", "", fmt.Errorf("unexpected memory content format")
	}
	remainder := strings.TrimPrefix(content, prefix)
	const separator = "\nAgent Response: "
	userInput, response, found := strings.Cut(remainder, separator)
	if !found {
		return "", "", fmt.Errorf("unexpected memory content format")
	}
	return userInput, response, nil
}

func memoryMessageActive(message ragflow.MemoryMessage) bool {
	return message.Status && strings.TrimSpace(message.ForgetAt) == ""
}

func memoryExternalContentHash(key []byte, memoryID, userInput, agentResponse string) string {
	return memoryTombstoneHash(
		key, "external-message-content",
		memoryID, userInput, agentResponse,
	)
}

func (s *Service) ScheduleMemoryCompensation(ctx context.Context, excludeID string) (bool, error) {
	return s.scheduleMemoryCompensation(ctx, time.Now().UTC().Add(memoryCompensationInterval), excludeID)
}

func (s *Service) scheduleMemoryCompensation(
	ctx context.Context, runAfter time.Time, excludeID string,
) (bool, error) {
	if s.Runner == nil {
		return false, errors.New("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindMemoryCompensation, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	return s.Runner.Enqueue(ctx, model.JobKindMemoryCompensation,
		"memory_compensation:"+runAfter.UTC().Format("20060102T150405.000000000Z"),
		SystemTenantID, "", runAfter, 0,
	)
}

type memoryCompensationWorker struct {
	svc *Service
}

func (w *memoryCompensationWorker) Kind() string { return model.JobKindMemoryCompensation }

func (w *memoryCompensationWorker) Run(ctx context.Context, job *model.Job) error {
	_, processErr := w.svc.ProcessMemoryCompensations(ctx, 20)
	if _, scheduleErr := w.svc.ScheduleMemoryCompensation(ctx, job.ID); scheduleErr != nil {
		return errors.Join(processErr, scheduleErr)
	}
	return processErr
}

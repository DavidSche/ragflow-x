package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const outboxDispatchInterval = time.Minute
const maxOutboxRetryBackoff = 5 * time.Minute

func nextOutboxRetryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 20 {
		attempts = 20
	}
	delay := 2 * time.Second << uint(attempts)
	if delay <= 0 || delay > maxOutboxRetryBackoff {
		return maxOutboxRetryBackoff
	}
	return delay
}

func (s *Service) ProcessOutboxEvents(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	now := time.Now().UTC()
	events, err := s.Store.ClaimOutboxEvents(ctx, limit, now, time.Minute)
	if err != nil {
		return err
	}
	var failures []error
	for index := range events {
		event := &events[index]
		if event.EventType != model.EventTypeDocumentVersionPublished ||
			event.AggregateType != model.AggregateTypeDocumentVersion {
			retryErr := s.Store.RetryOutboxEvent(
				ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)),
				fmt.Sprintf("unsupported outbox event type %s/%s", event.EventType, event.AggregateType),
			)
			if retryErr != nil {
				failures = append(failures, retryErr)
			}
			failures = append(failures, fmt.Errorf("unsupported outbox event type %s/%s", event.EventType, event.AggregateType))
			continue
		}
		var payload DocumentVersionPublishedPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil || payload.LogicalDocumentID == "" ||
			payload.NewVersionID == "" || payload.PublishAttemptID == "" {
			message := "invalid document version published payload"
			if err != nil {
				message = err.Error()
			}
			if retryErr := s.Store.RetryOutboxEvent(
				ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)), message,
			); retryErr != nil {
				failures = append(failures, retryErr)
			}
			failures = append(failures, errors.New(message))
			continue
		}
		_, published, impactErr := s.Store.MarkDocumentVersionPublishedImpact(
			ctx, event.TenantID, event.ID, payload.LogicalDocumentID, now,
			func(affected int64) (string, error) {
				result, err := json.Marshal(map[string]interface{}{
					"status":                          "published",
					"affected_eval_case_dependencies": affected,
				})
				return string(result), err
			},
		)
		if impactErr != nil {
			if retryErr := s.Store.RetryOutboxEvent(
				ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)), impactErr.Error(),
			); retryErr != nil {
				failures = append(failures, retryErr)
			}
			failures = append(failures, impactErr)
			continue
		}
		if !published {
			continue
		}
		logger.Info("document version published event dispatched",
			"event_id", event.ID, "logical_document_id", payload.LogicalDocumentID,
			"new_version_id", payload.NewVersionID,
		)
	}
	return errors.Join(failures...)
}

func (s *Service) ScheduleOutboxDispatch(ctx context.Context, excludeID string) (bool, error) {
	return s.scheduleOutboxDispatch(ctx, time.Now().UTC().Add(outboxDispatchInterval), excludeID)
}

func (s *Service) scheduleOutboxDispatch(
	ctx context.Context, runAfter time.Time, excludeID string,
) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindOutboxDispatch, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	return s.Runner.Enqueue(ctx, model.JobKindOutboxDispatch,
		"outbox_dispatch:"+runAfter.UTC().Format("20060102T150405.000000000Z"),
		SystemTenantID, "", runAfter, 0,
	)
}

func (s *Service) ListOutboxEvents(
	ctx context.Context, tenantID string, filter repository.OutboxEventFilter, page, pageSize int,
) ([]repository.OutboxEventView, int64, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, 0, httperr.BadRequest(40094, "tenant is required")
	}
	if page < 1 || pageSize < 1 || pageSize > 200 {
		return nil, 0, httperr.BadRequest(40094, "page must be at least 1 and page_size must be between 1 and 200")
	}
	filter.EventType = strings.TrimSpace(filter.EventType)
	filter.AggregateType = strings.TrimSpace(filter.AggregateType)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.EventType != "" && len(filter.EventType) > 64 {
		return nil, 0, httperr.BadRequest(40094, "event_type is invalid")
	}
	if filter.AggregateType != "" && len(filter.AggregateType) > 64 {
		return nil, 0, httperr.BadRequest(40094, "aggregate_type is invalid")
	}
	switch filter.Status {
	case "", repository.OutboxEventStatusPending,
		repository.OutboxEventStatusPublished, repository.OutboxEventStatusRetrying:
	default:
		return nil, 0, httperr.BadRequest(40094, "status must be pending, retrying or published")
	}
	return s.Store.ListOutboxEvents(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetOutboxEvent(ctx context.Context, tenantID, eventID string) (*repository.OutboxEventView, error) {
	tenantID = strings.TrimSpace(tenantID)
	eventID = strings.TrimSpace(eventID)
	if tenantID == "" || eventID == "" {
		return nil, httperr.BadRequest(40094, "tenant and event id are required")
	}
	view, err := s.Store.GetOutboxEvent(ctx, tenantID, eventID)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, httperr.NotFound("outbox event not found")
	}
	return view, nil
}

func (s *Service) RequestOutboxEventRetry(
	ctx context.Context, tenantID, userID, eventID string,
) (*repository.OutboxEventView, error) {
	tenantID = strings.TrimSpace(tenantID)
	eventID = strings.TrimSpace(eventID)
	if tenantID == "" || eventID == "" {
		return nil, httperr.BadRequest(40094, "tenant and event id are required")
	}
	view, err := s.GetOutboxEvent(ctx, tenantID, eventID)
	if err != nil {
		return nil, err
	}
	if view.Status == repository.OutboxEventStatusPublished {
		return nil, httperr.New(409, 40998, "published outbox event cannot be retried")
	}
	now := time.Now().UTC()
	_, retried, err := s.Store.RetryOutboxEventForTenant(ctx, tenantID, eventID, now)
	if err != nil {
		return nil, err
	}
	if !retried {
		return nil, httperr.New(409, 40998, "outbox event state changed")
	}
	view, err = s.Store.GetOutboxEvent(ctx, tenantID, eventID)
	if err != nil || view == nil {
		return nil, err
	}
	if _, err := s.scheduleOutboxDispatch(ctx, now, ""); err != nil {
		return nil, err
	}
	audit := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: userID, Action: "outbox_event.retry_requested",
		Resource: "outbox-event", ResourceID: eventID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"event_type": view.EventType, "aggregate_type": view.AggregateType,
			"status": view.Status, "attempts": view.Attempts,
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
	if err := s.Store.CreateAudit(ctx, audit); err != nil {
		return nil, err
	}
	return view, nil
}

type outboxDispatchWorker struct {
	svc *Service
}

func (w *outboxDispatchWorker) Kind() string { return model.JobKindOutboxDispatch }

func (w *outboxDispatchWorker) Run(ctx context.Context, job *model.Job) error {
	if err := w.svc.ProcessOutboxEvents(ctx, 20); err != nil {
		logger.Warn("outbox dispatch failed", "job_id", job.ID, "error", err)
		return err
	}
	if _, err := w.svc.ScheduleOutboxDispatch(ctx, job.ID); err != nil {
		logger.Warn("outbox dispatch reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}

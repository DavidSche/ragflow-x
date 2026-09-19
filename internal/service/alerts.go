package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// RecordAlert implements notify.Sink. Persistence failures are logged by the
// hub and do not block approval facts or outbound webhook delivery.
func (s *Service) RecordAlert(ctx context.Context, event notify.Event) error {
	tenantID := alertTenantID(event)
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	fieldsJSON := "{}"
	if len(event.Fields) > 0 {
		raw, err := json.Marshal(event.Fields)
		if err != nil {
			return err
		}
		fieldsJSON = string(raw)
	}
	fingerprint := event.Type + "|" + event.Resource + "|" + event.ResourceID + "|" + tenantID
	sum := sha256.Sum256([]byte(fingerprint))
	return s.Store.CreateAlertEvent(ctx, &model.AlertEvent{
		ID:          event.ID,
		TenantID:    tenantID,
		Title:       event.Title,
		Severity:    event.Severity,
		Type:        event.Type,
		Resource:    event.Resource,
		ResourceID:  event.ResourceID,
		Detail:      event.Detail,
		FieldsJSON:  fieldsJSON,
		Fingerprint: hex.EncodeToString(sum[:]),
		OccurredAt:  occurredAt.UTC(),
		Status:      model.AlertStatusOpen,
	})
}

func alertTenantID(event notify.Event) string {
	if event.TenantID == "" {
		return SystemTenantID
	}
	return event.TenantID
}

// RecordAlertDeliveryPending implements notify.DeliverySink. It creates the
// restartable lifecycle row before the outbound channel is called.
func (s *Service) RecordAlertDeliveryPending(ctx context.Context, event notify.Event, channel string) error {
	now := time.Now().UTC()
	return s.Store.UpsertAlertDelivery(ctx, &model.AlertDelivery{
		AlertEventID: event.ID, Channel: channel, TenantID: alertTenantID(event),
		Status: model.AlertDeliveryStatusPending, Attempts: 0, LastAttemptAt: now,
		NextRetryAt: &now, CreatedAt: now, UpdatedAt: now,
	})
}

// RecordAlertDeliveryResult implements notify.DeliverySink. It transitions the
// lifecycle row to succeeded or failed without losing the terminal error.
func (s *Service) RecordAlertDeliveryResult(ctx context.Context, event notify.Event, channel string, attempts int, deliveryErr error) error {
	now := time.Now().UTC()
	if attempts < 1 {
		attempts = 1
	}
	status := model.AlertDeliveryStatusSucceeded
	nextRetryAt := (*time.Time)(nil)
	lastError := ""
	if deliveryErr != nil {
		status = model.AlertDeliveryStatusFailed
		lastError = deliveryErr.Error()
		var deliveryFailure *notify.DeliveryError
		if errors.As(deliveryErr, &deliveryFailure) && !deliveryFailure.Retryable {
			nextRetryAt = nil
		} else {
			cfg := s.CurrentAlertingConfig()
			delay := alertDeliveryRetryDelay(cfg, attempts)
			var deliveryFailure *notify.DeliveryError
			if errors.As(deliveryErr, &deliveryFailure) && deliveryFailure.RetryAfter > delay {
				delay = alertDeliveryApplyJitter(deliveryFailure.RetryAfter, cfg)
			}
			retryAt := now.Add(delay)
			nextRetryAt = &retryAt
		}
	}
	return s.Store.UpsertAlertDelivery(ctx, &model.AlertDelivery{
		AlertEventID: event.ID, Channel: channel, TenantID: alertTenantID(event),
		Status: status, Attempts: attempts, LastError: lastError, LastAttemptAt: now,
		NextRetryAt: nextRetryAt, DeliveredAt: pointerToTimeOrNil(deliveryErr == nil, now),
		CreatedAt: now, UpdatedAt: now,
	})
}

func pointerToTimeOrNil(condition bool, value time.Time) *time.Time {
	if !condition {
		return nil
	}
	copied := value
	return &copied
}

func (s *Service) alertDeliveryLeaseResult(
	event notify.Event, channel string, attempts int, deliveryErr error,
) (*model.AlertDelivery, int64) {
	now := time.Now().UTC()
	if attempts < 1 {
		attempts = 1
	}
	status := model.AlertDeliveryStatusSucceeded
	nextRetryAt := (*time.Time)(nil)
	lastError := ""
	if deliveryErr != nil {
		status = model.AlertDeliveryStatusFailed
		lastError = deliveryErr.Error()
		var deliveryFailure *notify.DeliveryError
		if errors.As(deliveryErr, &deliveryFailure) && !deliveryFailure.Retryable {
			nextRetryAt = nil
		} else {
			cfg := s.CurrentAlertingConfig()
			delay := alertDeliveryRetryDelay(cfg, attempts)
			if errors.As(deliveryErr, &deliveryFailure) && deliveryFailure.RetryAfter > delay {
				delay = alertDeliveryApplyJitter(deliveryFailure.RetryAfter, cfg)
			}
			retryAt := now.Add(delay)
			nextRetryAt = &retryAt
		}
	}
	return &model.AlertDelivery{
		AlertEventID: event.ID, Channel: channel, TenantID: alertTenantID(event),
		Status: status, LastError: lastError, LastAttemptAt: now,
		NextRetryAt: nextRetryAt, DeliveredAt: pointerToTimeOrNil(deliveryErr == nil, now),
		UpdatedAt: now,
	}, int64(attempts)
}

// RecordAlertDeliveryLeaseResult implements notify.LeasedDeliverySink. The
// conditional update is the only path used after compensation delivery, so a
// worker whose lease expired cannot overwrite the current owner's state.
func (s *Service) RecordAlertDeliveryLeaseResult(
	ctx context.Context, event notify.Event, channel string, attempts int,
	lease notify.DeliveryLease, deliveryErr error,
) (bool, error) {
	delivery, attemptsDelta := s.alertDeliveryLeaseResult(event, channel, attempts, deliveryErr)
	return s.Store.UpdateAlertDeliveryLease(
		ctx, event.ID, channel, lease.Owner, lease.Generation, attemptsDelta, delivery,
	)
}

// ListAlertDeliveries returns tenant-scoped lifecycle rows; platform admins may
// optionally inspect all tenants.
func (s *Service) ListAlertDeliveries(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.AlertDeliveryFilter) ([]model.AlertDelivery, int64, error) {
	return s.Store.ListAlertDeliveries(ctx, tenantID, scopeAll, page, pageSize, filter)
}

// AlertDeliveryRetrySummary reports one bounded compensation pass.
type AlertDeliveryRetrySummary struct {
	Scanned     int `json:"scanned"`
	Compensated int `json:"compensated"`
	Skipped     int `json:"skipped"`
	Abandoned   int `json:"abandoned"`
	Fenced      int `json:"fenced"`
}

// RetryDueAlertDeliveries redelivers a bounded batch of pending/failed rows.
// Rows that exceed the configured cumulative attempt budget are abandoned so a
// permanently broken endpoint cannot consume worker capacity forever.
func (s *Service) RetryDueAlertDeliveries(ctx context.Context, limit int) (AlertDeliveryRetrySummary, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	cfg := s.CurrentAlertingConfig()
	if !cfg.Enabled {
		return AlertDeliveryRetrySummary{}, nil
	}
	now := time.Now().UTC()
	deliveries, err := s.Store.ListCompensableAlertDeliveries(ctx, now, limit)
	if err != nil {
		return AlertDeliveryRetrySummary{}, err
	}
	summary := AlertDeliveryRetrySummary{Scanned: len(deliveries)}
	for _, delivery := range deliveries {
		claimed, err := s.Store.ClaimAlertDeliveryLease(
			ctx, delivery.AlertEventID, delivery.Channel, alertDeliveryLeaseOwner(),
			now.Add(time.Duration(cfg.CompensationLeaseSec)*time.Second), now,
		)
		if err != nil {
			return summary, err
		}
		if claimed == nil {
			summary.Skipped++
			continue
		}
		delivery = *claimed
		if delivery.Attempts >= cfg.CompensationMaxAttempts {
			if valid, err := s.abandonAlertDelivery(ctx, delivery, now); err != nil {
				return summary, err
			} else if !valid {
				summary.Skipped++
				continue
			}
			summary.Abandoned++
			continue
		}
		alert, err := s.Store.GetAlertEvent(ctx, delivery.TenantID, delivery.AlertEventID, false)
		if err != nil {
			return summary, err
		}
		if alert == nil {
			if valid, err := s.abandonAlertDelivery(ctx, delivery, now); err != nil {
				return summary, err
			} else if !valid {
				summary.Skipped++
				continue
			}
			summary.Abandoned++
			continue
		}
		known, leaseValid := notify.Get().RetryLeased(
			ctx, alertDeliveryEvent(alert), delivery.Channel,
			notify.DeliveryLease{Owner: delivery.LeaseOwner, Generation: delivery.LeaseGeneration},
		)
		if !known {
			if valid, err := s.abandonAlertDelivery(ctx, delivery, now); err != nil {
				return summary, err
			} else if !valid {
				summary.Skipped++
				continue
			}
			summary.Abandoned++
			continue
		}
		if !leaseValid {
			summary.Skipped++
			summary.Fenced++
			continue
		}
		summary.Compensated++
	}
	return summary, nil
}

func (s *Service) abandonAlertDelivery(ctx context.Context, delivery model.AlertDelivery, now time.Time) (bool, error) {
	abandoned := delivery
	abandoned.Status = model.AlertDeliveryStatusAbandoned
	abandoned.Attempts = 0
	abandoned.NextRetryAt = nil
	abandoned.LeaseOwner = ""
	abandoned.LeaseExpiresAt = nil
	abandoned.LastAttemptAt = now
	abandoned.UpdatedAt = now
	return s.Store.UpdateAlertDeliveryLease(
		ctx, delivery.AlertEventID, delivery.Channel, delivery.LeaseOwner,
		delivery.LeaseGeneration, -int64(delivery.Attempts), &abandoned,
	)
}

// RetryAlertDelivery performs a tenant-scoped, lease-fenced manual retry.
// It intentionally ignores scheduled backoff but never retries succeeded rows.
func (s *Service) RetryAlertDelivery(
	ctx context.Context, tenantID string, scopeAll bool, alertEventID, channel, actor string,
) (AlertDeliveryRetrySummary, error) {
	if channel == "" {
		return AlertDeliveryRetrySummary{}, httperr.BadRequest(42971, "alert delivery channel is required")
	}
	if actor == "" {
		return AlertDeliveryRetrySummary{}, httperr.Forbidden("delivery retry actor is required")
	}
	cfg := s.CurrentAlertingConfig()
	if !cfg.Enabled {
		return AlertDeliveryRetrySummary{}, httperr.BadRequest(42972, "alert delivery is disabled")
	}

	delivery, err := s.Store.GetAlertDelivery(ctx, tenantID, scopeAll, alertEventID, channel)
	if err != nil {
		return AlertDeliveryRetrySummary{}, err
	}
	if delivery == nil {
		return AlertDeliveryRetrySummary{}, httperr.NotFound("alert delivery not found")
	}
	if delivery.Status == model.AlertDeliveryStatusSucceeded {
		return AlertDeliveryRetrySummary{}, httperr.BadRequest(42973, "succeeded delivery cannot be retried")
	}

	now := time.Now().UTC()
	leaseExpiresAt := now.Add(time.Duration(cfg.CompensationLeaseSec) * time.Second)
	claimed, err := s.Store.ClaimAlertDeliveryForManualRetry(
		ctx, tenantID, scopeAll, alertEventID, channel,
		manualAlertDeliveryLeaseOwner(actor), leaseExpiresAt,
	)
	if err != nil {
		return AlertDeliveryRetrySummary{}, err
	}
	if claimed == nil {
		return AlertDeliveryRetrySummary{}, httperr.New(409, 42974, "alert delivery lease is active")
	}
	delivery = claimed

	summary := AlertDeliveryRetrySummary{Scanned: 1}
	alert, err := s.Store.GetAlertEvent(ctx, delivery.TenantID, delivery.AlertEventID, false)
	if err != nil {
		return summary, err
	}
	if alert == nil {
		if valid, err := s.abandonAlertDelivery(ctx, *delivery, now); err != nil {
			return summary, err
		} else if !valid {
			summary.Skipped++
			summary.Fenced++
			return summary, nil
		}
		summary.Abandoned++
		return summary, nil
	}

	known, leaseValid := notify.Get().RetryLeased(
		ctx, alertDeliveryEvent(alert), delivery.Channel,
		notify.DeliveryLease{Owner: delivery.LeaseOwner, Generation: delivery.LeaseGeneration},
	)
	if !known {
		if valid, err := s.abandonAlertDelivery(ctx, *delivery, now); err != nil {
			return summary, err
		} else if !valid {
			summary.Skipped++
			summary.Fenced++
			return summary, nil
		}
		summary.Abandoned++
		return summary, nil
	}
	if !leaseValid {
		summary.Skipped++
		summary.Fenced++
		return summary, nil
	}

	summary.Compensated++
	_ = s.RecordAudit(ctx, &model.AuditLog{
		TenantID:   delivery.TenantID,
		UserID:     actor,
		Action:     "alert.delivery.retry",
		Resource:   "alert",
		ResourceID: delivery.AlertEventID + "|" + delivery.Channel,
	})
	return summary, nil
}

func alertDeliveryEvent(alert *model.AlertEvent) notify.Event {
	fields := map[string]string{}
	if alert.FieldsJSON != "" {
		_ = json.Unmarshal([]byte(alert.FieldsJSON), &fields)
	}
	return notify.Event{
		ID: alert.ID, Title: alert.Title, Severity: alert.Severity, Type: alert.Type,
		TenantID: alert.TenantID, Resource: alert.Resource, ResourceID: alert.ResourceID,
		Detail: alert.Detail, OccurredAt: alert.OccurredAt, Fields: fields,
	}
}

func alertDeliveryRetryDelay(cfg config.Alerting, attempts ...int) time.Duration {
	base := time.Duration(cfg.CompensationInitialDelaySec) * time.Second
	if len(attempts) > 0 {
		retryAttempt := attempts[0]
		if retryAttempt < 1 {
			retryAttempt = 1
		}
		shift := retryAttempt - 1
		if shift > 5 {
			shift = 5
		}
		base <<= shift
	}
	if base > time.Duration(cfg.CompensationMaxDelaySec)*time.Second {
		base = time.Duration(cfg.CompensationMaxDelaySec) * time.Second
	}
	return alertDeliveryApplyJitter(base, cfg)
}

func alertDeliveryApplyJitter(delay time.Duration, cfg config.Alerting) time.Duration {
	if cfg.CompensationJitterPercent <= 0 || delay <= 0 {
		return delay
	}
	increment := delay * time.Duration(cfg.CompensationJitterPercent) / 100
	return delay + time.Duration(rand.Int64N(int64(increment)+1))
}

func alertDeliveryLeaseOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "ragflow-x"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}

func manualAlertDeliveryLeaseOwner(actor string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "ragflow-x"
	}
	return fmt.Sprintf("manual:%s:%s:%d", actor, host, os.Getpid())
}

// ListAlerts returns a tenant-scoped page; platform admins may optionally
// filter across tenants.
func (s *Service) ListAlerts(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.AlertFilter) ([]model.AlertEvent, int64, error) {
	return s.Store.ListAlertEvents(ctx, tenantID, scopeAll, page, pageSize, filter)
}

// UpdateAlertStatus marks an alert read or claims it for operations.
func (s *Service) UpdateAlertStatus(ctx context.Context, tenantID, alertID string, scopeAll bool, review repository.AlertReview) error {
	switch review.Status {
	case model.AlertStatusRead, model.AlertStatusClaimed:
	default:
		return httperr.BadRequest(42970, "alert status must be read or claimed")
	}
	alert, err := s.Store.GetAlertEvent(ctx, tenantID, alertID, scopeAll)
	if err != nil {
		return err
	}
	if alert == nil {
		return httperr.NotFound("alert not found")
	}
	updated, err := s.Store.UpdateAlertEventStatus(ctx, tenantID, alertID, scopeAll, review)
	if err != nil {
		return err
	}
	if !updated {
		return httperr.NotFound("alert not found")
	}
	_ = s.RecordAudit(ctx, &model.AuditLog{
		TenantID: alert.TenantID, UserID: review.Actor, Action: "alert." + review.Status,
		Resource: "alert", ResourceID: alertID,
	})
	return nil
}

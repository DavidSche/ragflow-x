package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type capturedNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (n *capturedNotifier) Name() string { return "test" }

func (n *capturedNotifier) Send(_ context.Context, event notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	return nil
}

func (n *capturedNotifier) snapshot() []notify.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.Event(nil), n.events...)
}

func createMaintenanceApproval(t *testing.T, svc *Service, tenantID, policyID, requesterID, dueIn string) {
	t.Helper()
	now := time.Now().UTC()
	dueAt := now
	switch dueIn {
	case "soon":
		dueAt = now.Add(10 * time.Minute)
	case "overdue":
		dueAt = now.Add(-10 * time.Minute)
	}
	approval := &model.Approval{
		ID:             id.New(),
		TenantID:       tenantID,
		RequestNo:      "APR-MAINT-" + id.New()[:8],
		ObjectType:     model.ApprovalObjectDataset,
		ObjectID:       "ds-" + id.New()[:6],
		Action:         model.ApprovalActionDelete,
		Title:          "maintenance approval",
		Status:         model.ApprovalStatusPendingApproval,
		PolicyID:       policyID,
		PolicyVersion:  1,
		CurrentStep:    1,
		RequesterID:    requesterID,
		IdempotencyKey: "maint-" + id.New(),
		PayloadJSON:    "{}",
		SnapshotJSON:   "{}",
		ExpiresAt:      now.Add(48 * time.Hour),
		SubmittedAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	steps := []model.ApprovalStep{{
		ID:            id.New(),
		TenantID:      tenantID,
		ApprovalID:    approval.ID,
		StepNo:        1,
		Name:          "approval",
		ApproverType:  model.ApprovalApproverUser,
		ApproverValue: requesterID,
		Status:        model.ApprovalStepCurrent,
		DueAt:         &dueAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}}
	if err := svc.Store.CreateApprovalWithAudit(context.Background(), approval, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalMaintenanceEmitsDueAndOverdueNotifications(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true, ReminderBeforeHours: 24})
	requester, _, tenantID, policyID := setupApprovalEnv(t, svc)
	createMaintenanceApproval(t, svc, tenantID, policyID, requester, "soon")
	createMaintenanceApproval(t, svc, tenantID, policyID, requester, "overdue")

	captured := &capturedNotifier{}
	hub := notify.NewHubWithNotifiers([]notify.Notifier{captured}, 0)
	notify.Set(hub)
	defer func() {
		hub.Shutdown()
		notify.Set(nil)
	}()

	if err := svc.RunApprovalMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	hub.Shutdown()

	var dueSoon, overdue bool
	for _, event := range captured.snapshot() {
		switch event.Type {
		case "approval.due_soon":
			dueSoon = true
			if event.Fields["recipients"] != requester {
				t.Fatalf("due soon recipients = %q, want %q", event.Fields["recipients"], requester)
			}
		case "approval.overdue":
			overdue = true
			if event.Fields["recipients"] != "platform_admin" || event.Fields["escalation"] != "true" {
				t.Fatalf("overdue notification fields = %v", event.Fields)
			}
		}
	}
	if !dueSoon || !overdue {
		t.Fatalf("dueSoon=%t overdue=%t events=%v", dueSoon, overdue, captured.snapshot())
	}
}

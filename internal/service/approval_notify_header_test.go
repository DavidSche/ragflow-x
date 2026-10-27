package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

// captureNotifier is a notifier that hands delivered events back to the test
// on a buffered channel so approval events can be asserted after Emit.
type captureNotifier struct{ ch chan notify.Event }

func (c *captureNotifier) Name() string { return "capture" }
func (c *captureNotifier) Send(_ context.Context, ev notify.Event) error {
	c.ch <- ev
	return nil
}

// installCaptureHub swaps in a capture hub for the duration of the test and
// restores a no-op hub afterwards.
func installCaptureHub(t *testing.T) *captureNotifier {
	t.Helper()
	capture := &captureNotifier{ch: make(chan notify.Event, 8)}
	notify.Set(notify.NewHubWithNotifiers([]notify.Notifier{capture}, 0))
	t.Cleanup(func() { notify.Set(notify.NewHubWithNotifiers(nil, 0)) })
	return capture
}

// ScenarioID: SC-NOTIFY-004 (doc/223 §3 T8) — the approval event stream emits
// every event with the unified header: object_type/action/approval_id/deadline
// mirroring the audit vocabulary, plus the originating trace id injected from
// the request context.
func TestEmitApprovalEventCarriesUnifiedHeader(t *testing.T) {
	svc := newAuthzSvc(t)
	capture := installCaptureHub(t)
	ctx := requestid.NewContext(context.Background(), "trace-abc")
	approval := &model.Approval{
		ID: "apx", TenantID: "t1", RequestNo: "R-1",
		ObjectType: "dataset", ObjectID: "d1", Action: "delete",
		Status: model.ApprovalStatusPendingApproval, CurrentStep: 1,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	svc.emitApprovalEvent(ctx, "approval.pending", "pending approval", approval, "info")

	select {
	case ev := <-capture.ch:
		if ev.ObjectType != "dataset" || ev.Action != "delete" || ev.ApprovalID != "apx" {
			t.Fatalf("unified header mismatch: object=%q action=%q approval=%q", ev.ObjectType, ev.Action, ev.ApprovalID)
		}
		if ev.TraceID != "trace-abc" {
			t.Fatalf("trace id must flow from request context, got %q", ev.TraceID)
		}
		if ev.Deadline.IsZero() || !ev.Deadline.Equal(approval.ExpiresAt) {
			t.Fatalf("deadline must mirror approval expires_at: %v", ev.Deadline)
		}
		h := ev.Header()
		if h.ObjectType != "dataset" || h.Action != "delete" || h.ApprovalID != "apx" {
			t.Fatalf("header() must expose the unified columns: %+v", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval event was not delivered to the capture notifier")
	}
}

// ScenarioID: SC-NOTIFY-005 — the overdue/due-soon emitter (approval_action)
// carries the same header so escalation routing never parses detail text.
func TestEmitApprovalEventWithFieldsCarriesUnifiedHeader(t *testing.T) {
	svc := newAuthzSvc(t)
	capture := installCaptureHub(t)
	ctx := context.Background()
	approval := &model.Approval{
		ID: "apy", TenantID: "t1", RequestNo: "R-2",
		ObjectType: "memory_policy", ObjectID: "mp1", Action: "update",
		Status: model.ApprovalStatusPendingApproval, CurrentStep: 1,
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	svc.emitApprovalEventWithFields(ctx, "approval.overdue", "approval overdue", approval, "warn", map[string]string{"escalation": "level-1"})

	select {
	case ev := <-capture.ch:
		if ev.Type != "approval.overdue" {
			t.Fatalf("unexpected event type %q", ev.Type)
		}
		if ev.ObjectType != "memory_policy" || ev.ApprovalID != "apy" {
			t.Fatalf("unified header mismatch: %+v", ev.Header())
		}
		if ev.Fields["escalation"] != "level-1" {
			t.Fatalf("overrides must survive alongside the header: %+v", ev.Fields)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("overdue event was not delivered")
	}
}

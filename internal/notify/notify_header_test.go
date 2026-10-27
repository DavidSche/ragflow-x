package notify

import (
	"encoding/json"
	"testing"
	"time"
)

// auditAlignedHeaderKeys is the vocabulary the unified event-stream header must
// share with AuditLog columns (doc/223 §3 T8): consumers diff the webhook body
// against the audit trail byte-for-byte on these keys.
var auditAlignedHeaderKeys = []string{
	"tenant_id", "object_type", "action", "resource", "resource_id",
	"approval_id", "trace_id",
}

// ScenarioID: SC-NOTIFY-001 (doc/223 §3 T8 — unified event-stream header)
func TestEventHeaderExposesAuditAlignedColumns(t *testing.T) {
	dl := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ev := Event{
		TenantID: "t1", ObjectType: "dataset", Action: "create",
		Resource: "dataset", ResourceID: "d1", ApprovalID: "a1",
		TraceID: "trace-1", Deadline: dl, Type: "approval.pending",
	}
	h := ev.Header()
	if h.TenantID != "t1" || h.ObjectType != "dataset" || h.Action != "create" {
		t.Fatalf("header object/tenant mismatch: %+v", h)
	}
	if h.ApprovalID != "a1" || h.TraceID != "trace-1" {
		t.Fatalf("header approval/trace mismatch: %+v", h)
	}
	if h.Deadline == nil || !h.Deadline.Equal(dl) {
		t.Fatalf("header deadline mismatch: %+v", h.Deadline)
	}
}

// ScenarioID: SC-NOTIFY-002 — legacy emitters that fill none of the new header
// fields must remain byte-compatible (no zero-value noise in the JSON body).
func TestEventHeaderElidesZeroValues(t *testing.T) {
	ev := Event{TenantID: "t1"}
	h := ev.Header()
	if h.ObjectType != "" || h.Action != "" || h.ApprovalID != "" || h.TraceID != "" {
		t.Fatalf("zero header fields must be elided: %+v", h)
	}
	if h.Deadline != nil {
		t.Fatalf("zero deadline must be elided: %+v", h.Deadline)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("header must remain a valid JSON object")
	}
}

// ScenarioID: SC-NOTIFY-003 — payload carries the unified header and the
// header's keys cover the audit-aligned vocabulary.
func TestEventPayloadCarriesUnifiedHeader(t *testing.T) {
	ev := Event{
		TenantID: "t1", ObjectType: "approval", Action: "timeout",
		Resource: "approval", ResourceID: "a1", ApprovalID: "a1",
		TraceID: "trace-9", Deadline: time.Now(), Type: "approval.overdue",
	}
	raw, err := json.Marshal(ev.payload())
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	header, ok := body["header"].(map[string]any)
	if !ok {
		t.Fatalf("payload must carry a header object, got %#v", body["header"])
	}
	for _, key := range auditAlignedHeaderKeys {
		if _, present := header[key]; !present {
			t.Fatalf("unified header missing audit-aligned key %q: %#v", key, header)
		}
	}
	if header["approval_id"] != "a1" || header["action"] != "timeout" {
		t.Fatalf("header values mismatch: %#v", header)
	}
}

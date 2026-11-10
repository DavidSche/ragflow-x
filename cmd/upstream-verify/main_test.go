package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// report decisions depend only on in-memory registry data (no network), so
// the whole preflight semantics of the upgrade front command is unit-testable
// here. Version strings are chosen relative to the live verified band so the
// tests survive future pin raises without a rewrite.

func TestExitCodeForMapsDecisions(t *testing.T) {
	cases := []struct {
		decision ragflow.VersionGateDecision
		want     int
	}{
		{ragflow.VersionGatePass, exitPass},
		{ragflow.VersionGateDegrade, exitDegrade},
		{ragflow.VersionGateVerificationRequired, exitVerificationRequired},
		{ragflow.VersionGateBlock, exitBlock},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.decision); got != c.want {
			t.Errorf("exitCodeFor(%s) = %d, want %d", c.decision, got, c.want)
		}
	}
}

// TestNewReportFailClosed asserts a probe error or an empty observation can
// never pass: it must collapse to block no matter what the observed text is.
func TestNewReportFailClosed(t *testing.T) {
	probeErr := errors.New("version probe request failed: boom")
	r := newReport(time.Now(), "https://engine:8001", "v1.0.0", probeErr)
	if r.Decision != ragflow.VersionGateBlock {
		t.Fatalf("with probe error decision = %s, want block", r.Decision)
	}
	if !strings.Contains(r.Reason, "boom") {
		t.Errorf("reason does not surface the probe error: %q", r.Reason)
	}
	if r.ObservedVersion != "v1.0.0" {
		t.Errorf("observed must be preserved for the report, got %q", r.ObservedVersion)
	}

	empty := newReport(time.Now(), "https://engine:8001", "  ", nil)
	if empty.Decision != ragflow.VersionGateBlock {
		t.Fatalf("empty observation decision = %s, want block", empty.Decision)
	}
}

// TestNewReportWithinVerifiedBandPass asserts an engine inside the verified
// band is a clean pass with no forced re-drill.
func TestNewReportWithinVerifiedBandPass(t *testing.T) {
	verified := ragflow.VerifiedMaxVersion()
	if verified == "" {
		t.Fatal("registry has no verified band; this test needs T6.3 data")
	}
	r := newReport(time.Now(), "http://engine:8001", verified, nil)
	if r.Decision != ragflow.VersionGatePass {
		t.Fatalf("observed %s decision = %s, want pass (%s)", verified, r.Decision, r.Reason)
	}
	if r.ContractDrillCommand != "" {
		t.Errorf("pass must not force a drill command, got %q", r.ContractDrillCommand)
	}
	if r.PinnedBaseline != ragflow.PinnedBaselineVersion() {
		t.Errorf("pinned baseline mismatch: %q vs %q", r.PinnedBaseline, ragflow.PinnedBaselineVersion())
	}
}

// TestNewReportNewerThanBandRequiresDrill asserts a newer engine flips to
// verification_required and ships a ready-to-run drill command.
func TestNewReportNewerThanBandRequiresDrill(t *testing.T) {
	r := newReport(time.Now(), "http://engine:8001", "v100.0.0", nil)
	if r.Decision != ragflow.VersionGateVerificationRequired {
		t.Fatalf("newer engine decision = %s, want verification_required (%s)", r.Decision, r.Reason)
	}
	if !strings.Contains(r.ContractDrillCommand, "scripts/upstream-verify.sh") {
		t.Errorf("drill command missing orchestrating script: %q", r.ContractDrillCommand)
	}
	hasNext := false
	for _, s := range r.NextSteps {
		if strings.Contains(s, "baselineVersion") {
			hasNext = true
		}
	}
	if !hasNext {
		t.Error("verification_required must point the operator at the raise checklist")
	}
}

// TestNewReportOlderThanBandDegrades asserts an older engine conservatively
// degrades instead of trusting an unproven floor.
func TestNewReportOlderThanBandDegrades(t *testing.T) {
	r := newReport(time.Now(), "http://engine:8001", "v0.0.1", nil)
	if r.Decision != ragflow.VersionGateDegrade {
		t.Fatalf("older engine decision = %s, want degrade (%s)", r.Decision, r.Reason)
	}
}

// TestRenderTextCoversVerdict asserts the human report carries the four
// decisive fields an operator needs before running the contract pipeline.
func TestRenderTextCoversVerdict(t *testing.T) {
	r := newReport(time.Now(), "https://engine:8001", "v1.3.0", errors.New("network down"))
	text := renderText(r)
	for _, want := range []string{"observed version", "pinned baseline", "decision", "block", "network down", "capability gates"} {
		if !strings.Contains(text, want) {
			t.Errorf("renderText missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(renderText(r), "engine:8001") {
		t.Errorf("URL host must be listed (scheme-stripped) for operator identification")
	}
}

// TestJSONReportRoundTrips asserts the machine report is stable and lossless
// so the orchestrating script can archive it.
func TestJSONReportRoundTrips(t *testing.T) {
	r := newReport(time.Now(), "https://engine:8001", "v1.0.0", nil)
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"decision": "pass"`, `"observed_version"`, `"contract_drill_command"`, `"next_steps"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json report missing %q:\n%s", want, raw)
		}
	}
}

func TestMaskBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://example.com:8001", "example.com:8001"},
		{"https://engine:8001/", "engine:8001"},
		{"  engine:8001 ", "engine:8001"},
		{"", ""},
	}
	for _, c := range cases {
		if got := maskBaseURL(c.in); got != c.want {
			t.Errorf("maskBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

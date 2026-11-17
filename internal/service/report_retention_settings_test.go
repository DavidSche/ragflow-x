package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// The report-class TTLs are operator-editable (doc/288 D3 v2.6). These tests
// pin the two properties the janitor relies on: the keys are registered as
// hot-reloadable database-overridable settings, and an applied revision lands
// in the live retention policy the report janitor reads.

func TestReportRetentionTTLsAreEditableSettings(t *testing.T) {
	keys := []string{
		"retention.report_days",
		"retention.report_evidence_days",
		"retention.report_artifact_days",
		"retention.report_event_days",
		"retention.report_cleanup_hold_days",
	}
	for _, key := range keys {
		policy, ok := settingKeyPolicies[key]
		if !ok {
			t.Fatalf("%s is not a registered setting key", key)
		}
		if !policy.Editable || !policy.DatabaseOverride {
			t.Fatalf("%s must be editable with database override: %+v", key, policy)
		}
		if policy.RestartRequired {
			t.Fatalf("%s must apply without a restart", key)
		}
		if _, ok := settingKeyValidators[key]; !ok {
			t.Fatalf("%s has no validation metadata", key)
		}
	}
}

func TestReportRetentionTTLsFlowIntoLivePolicy(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	cfg := config.Config{Retention: config.Retention{
		ReportDays: 90, ReportEvidenceDays: 90, ReportArtifactDays: 30,
		ReportEventDays: 7, ReportCleanupHoldDays: 7,
	}}
	// Boot order: the process stores the effective policy (SetupRetention) and
	// then seeds the immutable baseline revision from it.
	svc.SetRetentionPolicy(cfg.Retention)
	if err := svc.InitializeSystemSettingsFromConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := svc.currentRetentionPolicy(); got.ReportDays != 90 || got.ReportArtifactDays != 30 {
		t.Fatalf("initial report TTLs not seeded from config: %+v", got)
	}

	initial, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil || initial == nil {
		t.Fatalf("initial revision: %+v err=%v", initial, err)
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: initial.Revision,
		Changes: map[string]interface{}{
			"retention.report_days":              120,
			"retention.report_evidence_days":     60,
			"retention.report_artifact_days":     14,
			"retention.report_event_days":        3,
			"retention.report_cleanup_hold_days": 5,
		},
		Note:      "report retention TTLs per doc 288 D3",
		Confirmed: true,
	}); err != nil {
		t.Fatalf("update report TTLs: %v", err)
	}

	got := svc.currentRetentionPolicy()
	if got.ReportDays != 120 || got.ReportEvidenceDays != 60 || got.ReportArtifactDays != 14 ||
		got.ReportEventDays != 3 || got.ReportCleanupHoldDays != 5 {
		t.Fatalf("live policy did not follow the revision: %+v", got)
	}
	// Evidence may never expire before the report that cites it, so the janitor
	// sees max(120, 60) even though the operator typed 60.
	if effective := got.EffectiveReportEvidenceTTL(); effective != 120 {
		t.Fatalf("effective evidence TTL = %d, want max(report, evidence) = 120", effective)
	}
}

func TestEffectiveReportEvidenceTTL(t *testing.T) {
	cases := []struct {
		report, evidence, want int
	}{
		{90, 90, 90},
		{90, 180, 180}, // evidence kept longer than the report
		{120, 60, 120}, // evidence request widened, never shortened below the report
		{0, 30, 30},    // evidence-only configuration still self-consistent
	}
	for _, c := range cases {
		got := config.Retention{ReportDays: c.report, ReportEvidenceDays: c.evidence}.EffectiveReportEvidenceTTL()
		if got != c.want {
			t.Fatalf("report=%d evidence=%d -> %d, want %d", c.report, c.evidence, got, c.want)
		}
	}
}

func TestReportRetentionTTLRejectsOutOfRangeValues(t *testing.T) {
	if err := validateSettingChange("retention.report_days", 0); err == nil {
		t.Fatal("report_days=0 must be rejected (0 would purge everything at the next sweep)")
	}
	if err := validateSettingChange("retention.report_cleanup_hold_days", 0); err != nil {
		t.Fatalf("cleanup hold 0 is a legal value (no recovery window): %v", err)
	}
	if err := validateSettingChange("retention.report_event_days", 3651); err == nil {
		t.Fatal("report_event_days above the documented maximum must be rejected")
	}
}

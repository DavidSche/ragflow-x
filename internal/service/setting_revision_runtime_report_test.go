package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRuntimeSettingStateReportIsPerInstanceAndRevisionBound(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.ReportRuntimeSettingState(ctx, RuntimeSettingStateReport{
		RuntimeRevisionID: current.ID,
		RuntimeInstanceID: "runtime-a",
		InstanceIdentity:  "node-a",
		ApplyStatus:       model.RuntimeApplyApplied,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ReportRuntimeSettingState(ctx, RuntimeSettingStateReport{
		RuntimeRevisionID: current.ID,
		RuntimeInstanceID: "runtime-b",
		InstanceIdentity:  "node-b",
		ApplyStatus:       model.RuntimeApplyFailed,
		ApplyError:        "gateway reload failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	states, err := svc.Store.ListRuntimeSettingInstanceStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]model.RuntimeSettingInstanceState{}
	for _, state := range states {
		if state.RuntimeInstanceID == first.RuntimeInstanceID || state.RuntimeInstanceID == second.RuntimeInstanceID {
			reported[state.RuntimeInstanceID] = state
		}
	}
	if len(reported) != 2 {
		t.Fatalf("runtime states: %+v", states)
	}
	if reported[first.RuntimeInstanceID].ApplyStatus != model.RuntimeApplyApplied ||
		reported[second.RuntimeInstanceID].ApplyStatus != model.RuntimeApplyFailed {
		t.Fatalf("unexpected statuses: %+v", reported)
	}
	retry, err := svc.ReportRuntimeSettingState(ctx, RuntimeSettingStateReport{
		RuntimeRevisionID: current.ID,
		RuntimeInstanceID: "runtime-b",
		InstanceIdentity:  "node-b",
		ApplyStatus:       model.RuntimeApplyApplied,
	})
	if err != nil {
		t.Fatal(err)
	}
	states, err = svc.Store.ListRuntimeSettingInstanceStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reported = map[string]model.RuntimeSettingInstanceState{}
	for _, state := range states {
		if state.RuntimeInstanceID == first.RuntimeInstanceID || state.RuntimeInstanceID == second.RuntimeInstanceID {
			reported[state.RuntimeInstanceID] = state
		}
	}
	if len(reported) != 2 {
		t.Fatalf("runtime report must upsert by instance: %+v", states)
	}
	for _, state := range states {
		if state.RuntimeInstanceID == retry.RuntimeInstanceID && state.ApplyStatus != model.RuntimeApplyApplied {
			t.Fatalf("latest report was not applied: %+v", state)
		}
	}
}

func TestRuntimeReportTokenImportAndHotReload(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{
		Runtime: config.Runtime{ReportToken: "bootstrap-token", HeartbeatTimeoutSec: 30},
	}); err != nil {
		t.Fatal(err)
	}
	if got := svc.CurrentRuntimeReportToken(); got != "bootstrap-token" {
		t.Fatalf("bootstrap report token: %q", got)
	}
	svc.SetRuntimeConfig(config.Runtime{})
	if _, err := svc.EffectiveSystemSettingsConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if got := svc.CurrentRuntimeReportToken(); got != "bootstrap-token" {
		t.Fatalf("startup must load report token from Secret Store: %q", got)
	}
	if got := svc.CurrentRuntimeConfig().HeartbeatTimeoutSec; got != 30 {
		t.Fatalf("heartbeat timeout: %d", got)
	}
	if _, err := svc.UpdateSystemSettingSecret(ctx, "admin", "runtime.report_token", "rotated-token", "rotate runtime identity", true); err != nil {
		t.Fatal(err)
	}
	if got := svc.CurrentRuntimeReportToken(); got != "rotated-token" {
		t.Fatalf("rotated report token: %q", got)
	}
}

func TestRuntimeHeartbeatStaleStateMarksDeploymentDegraded(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{
		Runtime: config.Runtime{ReportToken: "report-token", HeartbeatTimeoutSec: 10},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertRuntimeSettingInstanceState(ctx, &model.RuntimeSettingInstanceState{
		RuntimeInstanceID: "runtime-stale", InstanceIdentity: "node-stale",
		RuntimeRevisionID: current.ID, ApplyStatus: model.RuntimeApplyApplied,
		LastSeenAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale := false
	for _, state := range view.RuntimeInstances {
		if state.RuntimeInstanceID == "runtime-stale" && state.HeartbeatState == "stale" {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("stale heartbeat not exposed: %+v", view.RuntimeInstances)
	}
	if view.DeploymentEffectiveState != "degraded" {
		t.Fatalf("stale heartbeat state: %s", view.DeploymentEffectiveState)
	}
}

func TestRuntimeSettingStateReportRejectsStaleRevisionAndInvalidStatus(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: current.Revision,
		Changes:          map[string]interface{}{"gateway.max_conns": 35},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReportRuntimeSettingState(ctx, RuntimeSettingStateReport{
		RuntimeRevisionID: current.ID,
		RuntimeInstanceID: "runtime-a",
		InstanceIdentity:  "node-a",
		ApplyStatus:       model.RuntimeApplyApplied,
	}); err == nil {
		t.Fatal("stale revision must be rejected")
	}
	if _, err := svc.ReportRuntimeSettingState(ctx, RuntimeSettingStateReport{
		RuntimeRevisionID: latest.ID,
		RuntimeInstanceID: "runtime-a",
		InstanceIdentity:  "node-a",
		ApplyStatus:       model.RuntimeApplyPending,
	}); err == nil {
		t.Fatal("local-only pending state must not be reported")
	}
}

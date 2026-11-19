package service

// The two governance switches of the report surface, resolved in one place
// (doc/296 §2.2 / §2.3 / A68–A70).
//
// Both switches answer "how much rope does this workspace get", and both are read
// exactly twice: once when a tenant row is written (to validate the value) and once
// when a run is created (to freeze the answer on the row). Nothing reads them at
// call time. That is the property that keeps a mid-run switch flip from changing what
// an already-running report is allowed to do — the failure mode an operator would
// never think to check for.

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// reportBudgetPolicyResolved is `observe` only when the tenant said so, and
// `enforce` for every other state including "no overlay at all".
//
// The direction of the default is the point: a tenant that never opened this switch
// keeps the P0-A behaviour, and a corrupt value fails *closed* (enforce) rather than
// open — a typo must not turn every report into an unmetered one.
func reportBudgetPolicyResolved(overlay *model.TenantReportPolicy) string {
	if overlay == nil {
		return model.ReportBudgetEnforce
	}
	if strings.TrimSpace(overlay.BudgetPolicy) == model.ReportBudgetObserve {
		return model.ReportBudgetObserve
	}
	return model.ReportBudgetEnforce
}

// reportAutoConfirmPolicyResolved is `approval` only when the tenant said so.
//
// `self` is the default because §8.4 already requires the owner's *own* explicit
// choice to skip the outline review; making governance approval the default would
// turn every `auto` report into a pending approval for workspaces that never asked
// for it.
func reportAutoConfirmPolicyResolved(overlay *model.TenantReportPolicy) string {
	if overlay == nil {
		return model.ReportAutoConfirmSelf
	}
	if strings.TrimSpace(overlay.AutoConfirmPolicy) == model.ReportAutoConfirmApproval {
		return model.ReportAutoConfirmApproval
	}
	return model.ReportAutoConfirmSelf
}

// reportBudgetPolicyFor is the creation-time read: what policy should a *new* report
// job freeze?
//
// A broken overlay must not make reports un-creatable, and it must not silently
// unmeter them either, so the error path falls back to `enforce` and logs. This
// mirrors `ReportRetentionFor`'s rule for the clocks, for the same reason.
func (s *Service) reportBudgetPolicyFor(ctx context.Context, tenantID string) string {
	return reportBudgetPolicyResolved(s.reportPolicyOverlay(ctx, tenantID))
}

// reportAutoConfirmPolicyFor is the same read for the confirmation switch.
func (s *Service) reportAutoConfirmPolicyFor(ctx context.Context, tenantID string) string {
	return reportAutoConfirmPolicyResolved(s.reportPolicyOverlay(ctx, tenantID))
}

// reportPolicyOverlay loads the tenant row once for both switches. `nil` means "no
// overlay", which every resolver reads as "the documented default".
func (s *Service) reportPolicyOverlay(ctx context.Context, tenantID string) *model.TenantReportPolicy {
	if tenantID == "" {
		return nil
	}
	overlay, err := s.Store.GetTenantReportPolicy(ctx, tenantID)
	if err != nil {
		logger.Warn("tenant report policy lookup failed, using the documented defaults",
			"tenant_id", tenantID, "error", err)
		return nil
	}
	return overlay
}

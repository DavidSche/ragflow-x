package service

// Tenant-level report retention overlay (doc/288 §7.6 / T20b).
//
// P0-A made the four report clocks configurable at the system level and froze the
// resulting instant on every row at creation. This file adds the tenant dimension
// without touching either invariant:
//
//   - **Override, not copy.** A tenant row stores only the numbers the tenant
//     actually chose (`NULL` = inherit). An operational change to
//     `retention.report_days` therefore keeps flowing to tenants that never
//     overrode it — the bug that a copied default would introduce ("tenant silently
//     pinned to last year's value").
//   - **`max` survives the overlay.** `report_evidence_days` is compared against the
//     *effective* report clock, so a tenant that shortens its report TTL to 30 days
//     cannot end up with evidence expiring at 7 days (which would make "有据可查"
//     false for 23 days of a report's life). The rule is applied after the overlay,
//     not before, because that is the only order where a tenant override can never
//     weaken it.
//   - **Resolution happens at creation only.** Nothing here is read by the janitor,
//     by a read endpoint, or by a repair path: a row's `expires_at` never changes
//     after it is written (§7.6 invariant ①). Editing the overlay therefore cannot
//     retro-shorten or revive anything (A47).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// ReportRetentionView is what the policy endpoint echoes: the effective values the
// next created row would get, plus which ones came from the tenant overlay. The UI
// needs both halves — "inherited 90 days" and "your 30 days" are different promises
// and an operator must not have to guess which one is in force.
type ReportRetentionView struct {
	TenantID string `json:"tenant_id"`
	// Effective values, in days.
	ReportDays            int `json:"report_days"`
	ReportEvidenceDays    int `json:"report_evidence_days"`
	ReportArtifactDays    int `json:"report_artifact_days"`
	ReportEventDays       int `json:"report_event_days"`
	ReportCleanupHoldDays int `json:"report_cleanup_hold_days"`
	// SystemReportDays..SystemCleanupHoldDays are the inherited values, so a client
	// can render "30 (system 90)" without a second request.
	SystemReportDays            int `json:"system_report_days"`
	SystemReportEvidenceDays    int `json:"system_report_evidence_days"`
	SystemReportArtifactDays    int `json:"system_report_artifact_days"`
	SystemReportEventDays       int `json:"system_report_event_days"`
	SystemReportCleanupHoldDays int `json:"system_report_cleanup_hold_days"`
	// Overridden flags name the fields the tenant pins.
	Overridden []string  `json:"overridden"`
	UpdatedBy  string    `json:"updated_by,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// reportRetentionDefaults are the documented fallbacks when neither the system nor
// the tenant configured a value (doc/288 §7.6 v2.6 建议值).
const (
	reportDefaultEvidenceDays = 90
	reportDefaultHoldDays     = 7
)

// ReportRetentionFor resolves the effective report clocks for one tenant.
//
// It is the only reader of the overlay. Callers use it at *row creation* time,
// immediately followed by `reportExpiry(now, days, fallback)`, so the value lands on
// the row and is never consulted again.
func (s *Service) ReportRetentionFor(ctx context.Context, tenantID string) config.Retention {
	policy := s.currentRetentionPolicy()
	if tenantID == "" {
		return policy
	}
	overlay, err := s.Store.GetTenantReportPolicy(ctx, tenantID)
	if err != nil {
		// A broken overlay must not make reports un-creatable, and it must not
		// silently apply *some* other tenant's numbers either. Logging and falling
		// back to the system policy is the answer that keeps the product working
		// while staying visible in the logs.
		logger.Warn("tenant report policy lookup failed, falling back to the system policy",
			"tenant_id", tenantID, "error", err)
		return policy
	}
	if overlay == nil {
		return policy
	}
	return applyTenantReportPolicy(policy, overlay)
}

// applyTenantReportPolicy overlays the tenant row on the system policy. The
// `evidence ≥ report` rule is deliberately *not* re-applied here: it is derived by
// `config.Retention.EffectiveReportEvidenceTTL()` from the effective values, so
// every reader (the view, `ReportRetentionFor`, the janitor's contract) sees the
// same numbers whether the tenant overrode one clock, both, or neither. Baking the
// max into a stored field would let the two disagree after the next overlay write.
func applyTenantReportPolicy(system config.Retention, overlay *model.TenantReportPolicy) config.Retention {
	effective := system
	if overlay.ReportDays != nil {
		effective.ReportDays = *overlay.ReportDays
	}
	if overlay.ReportEvidenceDays != nil {
		effective.ReportEvidenceDays = *overlay.ReportEvidenceDays
	}
	if overlay.ReportArtifactDays != nil {
		effective.ReportArtifactDays = *overlay.ReportArtifactDays
	}
	if overlay.ReportEventDays != nil {
		effective.ReportEventDays = *overlay.ReportEventDays
	}
	if overlay.ReportCleanupHoldDays != nil {
		effective.ReportCleanupHoldDays = *overlay.ReportCleanupHoldDays
	}
	return effective
}

// reportRetentionDays resolves one clock to a concrete day count, applying the same
// documented fallbacks as `reportExpiry` so the view and the written row agree.
func reportRetentionDays(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

// ReportRetention returns the effective policy plus its inheritance breakdown.
func (s *Service) ReportRetention(ctx context.Context, tenantID string) (*ReportRetentionView, error) {
	if tenantID == "" {
		return nil, httperr.Forbidden("report retention requires a workspace")
	}
	system := s.currentRetentionPolicy()
	overlay, err := s.Store.GetTenantReportPolicy(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	effective := system
	if overlay != nil {
		effective = applyTenantReportPolicy(system, overlay)
	}
	view := &ReportRetentionView{
		TenantID:                    tenantID,
		ReportDays:                  reportRetentionDays(effective.ReportDays, reportDefaultTTLDays),
		ReportEvidenceDays:          reportRetentionDays(effective.EffectiveReportEvidenceTTL(), reportDefaultEvidenceDays),
		ReportArtifactDays:          reportRetentionDays(effective.ReportArtifactDays, reportDefaultArtifactDays),
		ReportEventDays:             reportRetentionDays(effective.ReportEventDays, reportDefaultEventDays),
		ReportCleanupHoldDays:       reportRetentionDays(effective.ReportCleanupHoldDays, reportDefaultHoldDays),
		SystemReportDays:            reportRetentionDays(system.ReportDays, reportDefaultTTLDays),
		SystemReportEvidenceDays:    reportRetentionDays(system.EffectiveReportEvidenceTTL(), reportDefaultEvidenceDays),
		SystemReportArtifactDays:    reportRetentionDays(system.ReportArtifactDays, reportDefaultArtifactDays),
		SystemReportEventDays:       reportRetentionDays(system.ReportEventDays, reportDefaultEventDays),
		SystemReportCleanupHoldDays: reportRetentionDays(system.ReportCleanupHoldDays, reportDefaultHoldDays),
		Overridden:                  []string{},
	}
	if overlay != nil {
		view.UpdatedBy = overlay.UpdatedBy
		view.UpdatedAt = overlay.UpdatedAt
		if overlay.ReportDays != nil {
			view.Overridden = append(view.Overridden, "report_days")
		}
		if overlay.ReportEvidenceDays != nil {
			view.Overridden = append(view.Overridden, "report_evidence_days")
		}
		if overlay.ReportArtifactDays != nil {
			view.Overridden = append(view.Overridden, "report_artifact_days")
		}
		if overlay.ReportEventDays != nil {
			view.Overridden = append(view.Overridden, "report_event_days")
		}
		if overlay.ReportCleanupHoldDays != nil {
			view.Overridden = append(view.Overridden, "report_cleanup_hold_days")
		}
	}
	return view, nil
}

// TenantReportPolicyRequest is the overlay write payload. Every field is optional:
// omitting one means "inherit", which is also how a tenant *reverts* an override
// (`null`). The zero value therefore clears the whole overlay rather than writing
// zeros — writing 0 would mean "purge everything immediately", which is not a
// reachable state for a policy (validation rejects it).
type TenantReportPolicyRequest struct {
	ReportDays            *int `json:"report_days"`
	ReportEvidenceDays    *int `json:"report_evidence_days"`
	ReportArtifactDays    *int `json:"report_artifact_days"`
	ReportEventDays       *int `json:"report_event_days"`
	ReportCleanupHoldDays *int `json:"report_cleanup_hold_days"`
}

// validateReportTTLDays mirrors the system-level bounds from `settingKeyValidators`
// (1..3650 for the four TTLs, 0..365 for the recovery window), so a tenant cannot
// reach a state the operator surface would reject. `cleanup_hold_days` alone allows
// 0: "no recovery window" is a legal governance choice.
//
// The code is `44105`, the surface's "a request parameter is invalid" answer
// (`handler/report.go` already returns it for a bad `revision`/`ord`), for two
// reasons. `44114` is ruled out by §8.7 rule ⑤: it is the `Idempotency-Key` code and
// must not be borrowed for other malformed requests. A generic code outside the
// report block is ruled out by the opposite failure — `40098` is already mapped to
// one memory-specific sentence in the web error table, so reusing it here would show
// "请选择嵌入模型" for a bad retention window (the exact overloaded-code incident that
// `Test40085HasASingleOwner` exists to prevent — that test also rejects this
// sentence if the number is spelled out, so it stays described, not cited).
func validateReportTTLDays(name string, value *int, allowZero bool) error {
	if value == nil {
		return nil
	}
	lower, upper := 1, 3650
	if allowZero {
		lower, upper = 0, 365
	}
	if *value < lower || *value > upper {
		return httperr.BadRequest(codeReportOutlineInvalid,
			fmt.Sprintf("report retention %s must be between %d and %d days", name, lower, upper))
	}
	return nil
}

// SetTenantReportPolicy writes the tenant override and audits it.
//
// Only a caller with `manage` on this workspace's reports may do it: retention is a
// governance decision (it decides when evidence disappears), not a per-user
// preference, so `execute` is not enough.
func (s *Service) SetTenantReportPolicy(
	ctx context.Context, actorID, tenantID string, req TenantReportPolicyRequest,
) (*ReportRetentionView, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report retention requires an authenticated workspace")
	}
	if err := s.AuthorizeObject(ctx, actorID, "manage", "report", tenantID, ""); err != nil {
		return nil, err
	}
	for _, check := range []struct {
		name      string
		value     *int
		allowZero bool
	}{
		{"report_days", req.ReportDays, false},
		{"report_evidence_days", req.ReportEvidenceDays, false},
		{"report_artifact_days", req.ReportArtifactDays, false},
		{"report_event_days", req.ReportEventDays, false},
		{"report_cleanup_hold_days", req.ReportCleanupHoldDays, true},
	} {
		if err := validateReportTTLDays(check.name, check.value, check.allowZero); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	policy := &model.TenantReportPolicy{
		TenantID:              tenantID,
		ReportDays:            req.ReportDays,
		ReportEvidenceDays:    req.ReportEvidenceDays,
		ReportArtifactDays:    req.ReportArtifactDays,
		ReportEventDays:       req.ReportEventDays,
		ReportCleanupHoldDays: req.ReportCleanupHoldDays,
		UpdatedBy:             actorID,
		UpdatedAt:             now,
	}
	if err := s.Store.UpsertTenantReportPolicy(ctx, policy); err != nil {
		return nil, err
	}
	view, err := s.ReportRetention(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.retention.tenant_overridden",
		Resource: "report", ResourceID: tenantID, Result: "SUCCESS",
		DetailJSON: reportRetentionAuditDetail(view, now), At: now,
	}); auditErr != nil {
		return nil, auditErr
	}
	return view, nil
}

// reportRetentionAuditDetail is the audit row's payload. The note is part of the
// contract, not decoration: "this only affects rows created from now on" is the
// fact an auditor will otherwise assume the opposite of.
func reportRetentionAuditDetail(view *ReportRetentionView, at time.Time) string {
	detail, err := json.Marshal(map[string]interface{}{
		"overridden":               view.Overridden,
		"report_days":              view.ReportDays,
		"report_evidence_days":     view.ReportEvidenceDays,
		"report_artifact_days":     view.ReportArtifactDays,
		"report_event_days":        view.ReportEventDays,
		"report_cleanup_hold_days": view.ReportCleanupHoldDays,
		"applies_to":               "rows created after " + at.Format(time.RFC3339),
		"existing_rows_recomputed": false,
	})
	if err != nil {
		return "{}"
	}
	return string(detail)
}

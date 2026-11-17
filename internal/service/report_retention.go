package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Report retention (doc/288 §7.6 / T20).
//
// Design invariants this file exists to protect:
//
//  1. Four clocks, four passes. Report rows, evidence snapshots, artifacts and
//     events each expire on their own `expires_at`. Nothing here reads
//     `config.Retention` — the value was frozen into the row when it was
//     created, so editing `retention.report_*` never retro-shortens or revives
//     an existing report.
//  2. Evidence never expires before its report. The snapshot clock is
//     `max(report_days, report_evidence_days)` at creation, and the report janitor
//     does NOT cascade into snapshots.
//  3. A soft delete buys a recovery window, not extra life. `cleanup_hold_until`
//     delays the purge and never rewrites `expires_at`.
//
// Artifacts are shared with answer exports, so they are purged by the existing
// `CleanupExpiredExportArtifacts` pass: it is already driven by the row clock
// and therefore covers report artifacts without a second code path.

// reportRetentionBatch bounds one purge pass. The janitor runs on the 24h
// retention cadence, so a bounded pass that catches up over several runs is
// preferable to one long transaction competing with report writes.
const reportRetentionBatch = 200

// cleanupExpiredReports deletes report aggregates whose report clock ran out and
// whose recovery window (if any) is closed. Returns tenant -> deleted reports.
func (s *Service) cleanupExpiredReports(ctx context.Context, now time.Time) (map[string]int64, error) {
	counts := map[string]int64{}
	reports, err := s.Store.ListExpiredReportIDs(ctx, now, reportRetentionBatch)
	if err != nil {
		return nil, err
	}
	for _, report := range reports {
		if err := s.Store.PurgeReport(ctx, report.TenantID, report.ID); err != nil {
			// A concurrent pass (or a manual "run now") may have purged it first;
			// that is not a failure and must not inflate the audit counts.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			// One stuck row must not block the whole class; log and continue so
			// the next pass retries it.
			logger.Warn("report retention purge failed",
				"tenant_id", report.TenantID, "report_id", report.ID, "error", err)
			continue
		}
		counts[report.TenantID]++
	}
	return counts, nil
}

// cleanupExpiredReportEvidence clears evidence snapshots on their own clock.
func (s *Service) cleanupExpiredReportEvidence(ctx context.Context, now time.Time) (map[string]int64, error) {
	return s.Store.PurgeExpiredReportEvidence(ctx, now, reportRetentionBatch)
}

// cleanupExpiredReportEvents clears replay events on their own clock. A stream
// cursor pointing into the gap must degrade to `reset_required` (44115), never
// to a silent jump — that contract lives in the stream handler.
func (s *Service) cleanupExpiredReportEvents(ctx context.Context, now time.Time) (map[string]int64, error) {
	return s.Store.PurgeExpiredReportEvents(ctx, now, reportRetentionBatch)
}

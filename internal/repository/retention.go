// Package repository: data retention TTL purges (A5, doc/33 Sprint P1-2).
package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// RetentionRepo executes bounded TTL purges of operational ledgers. Each purge
// returns per-tenant deleted counts so the service can (a) write a delete-amount
// audit record ("有删除量审计") and (b) re-anchor the audit hash chain after an
// audit purge so the retained chain stays tamper-verifiable.
type RetentionRepo interface {
	// PurgeAuditBefore deletes audit rows older than before, returning the
	// per-tenant deleted counts (the tenants needing a chain re-anchor).
	PurgeAuditBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// ReanchorAuditChains re-anchors the audit hash chain of the given tenants:
	// surviving rows are renumbered 1..N and hashed from a fresh anchor so
	// VerifyAuditChain continues to hold after a legitimate retention purge.
	ReanchorAuditChains(ctx context.Context, tenantIDs []string) error
	// PurgeCostMetricBefore deletes gateway usage-detail rows older than before.
	PurgeCostMetricBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeQuotaUsageBefore deletes daily usage aggregates older than before.
	PurgeQuotaUsageBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeMeterRequestBefore deletes gateway idempotency-ledger rows older than before.
	PurgeMeterRequestBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeGatewayIdempotencyBefore deletes expired caller retry records.
	PurgeGatewayIdempotencyBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeMessageFeedbackBefore deletes message feedback rows older than before.
	PurgeMessageFeedbackBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeTerminalJobsBefore deletes succeeded/failed job records (rgx_job)
	// older than before; active jobs are never touched by retention.
	PurgeTerminalJobsBefore(ctx context.Context, before time.Time) (map[string]int64, error)
	// PurgeTerminalTasksBefore deletes done/failed/stopped task projections
	// (rgx_task) older than before; active tasks are never touched.
	PurgeTerminalTasksBefore(ctx context.Context, before time.Time) (map[string]int64, error)
}

// tenantPurgeRow is a GROUP BY tenant_id count row.
type tenantPurgeRow struct {
	TenantID string `gorm:"column:tenant_id"`
	Cnt      int64  `gorm:"column:cnt"`
}

// purgeByModel deletes rows of dst older than before (optionally narrowed by
// extra) inside one transaction, returning per-tenant deleted counts. The
// count and delete share the same snapshot so the reported amounts match what
// was actually removed.
func (s *store) purgeByModel(ctx context.Context, dst interface{}, column string, before time.Time, extra func(*gorm.DB) *gorm.DB) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows := []tenantPurgeRow{}
		q := tx.Model(dst).Where(column+" < ?", before)
		if extra != nil {
			q = extra(q)
		}
		if err := q.Select("tenant_id, COUNT(*) AS cnt").Group("tenant_id").Scan(&rows).Error; err != nil {
			return err
		}
		var total int64
		for _, r := range rows {
			out[r.TenantID] = r.Cnt
			total += r.Cnt
		}
		if total == 0 {
			return nil
		}
		dq := tx.Where(column+" < ?", before)
		if extra != nil {
			dq = extra(dq)
		}
		return dq.Delete(dst).Error
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PurgeAuditBefore deletes audit rows older than before.
func (s *store) PurgeAuditBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.AuditLog{}, "at", before, nil)
}

// PurgeCostMetricBefore deletes usage-detail rows older than before.
func (s *store) PurgeCostMetricBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.CostMetric{}, "created_at", before, nil)
}

// PurgeQuotaUsageBefore deletes daily usage aggregate rows older than before.
func (s *store) PurgeQuotaUsageBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.QuotaUsage{}, "created_at", before, nil)
}

// PurgeMeterRequestBefore deletes gateway idempotency-ledger rows older than before.
func (s *store) PurgeMeterRequestBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.MeterRequest{}, "created_at", before, nil)
}

func (s *store) PurgeGatewayIdempotencyBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.GatewayIdempotency{}, "expires_at", before, nil)
}

// PurgeMessageFeedbackBefore deletes message feedback rows older than before.
func (s *store) PurgeMessageFeedbackBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	return s.purgeByModel(ctx, &model.MessageFeedback{}, "created_at", before, nil)
}

// PurgeTerminalJobsBefore deletes only terminal job records
// (succeeded/failed/canceled).
func (s *store) PurgeTerminalJobsBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	terminal := func(q *gorm.DB) *gorm.DB {
		return q.Where("status IN ?", []string{
			model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusCanceled,
		})
	}
	return s.purgeByModel(ctx, &model.Job{}, "updated_at", before, terminal)
}

// PurgeTerminalTasksBefore deletes only terminal task projections
// (done/failed/stopped).
func (s *store) PurgeTerminalTasksBefore(ctx context.Context, before time.Time) (map[string]int64, error) {
	terminal := func(q *gorm.DB) *gorm.DB {
		return q.Where("status IN ?", []string{model.TaskStatusDone, model.TaskStatusFailed, model.TaskStatusStopped})
	}
	return s.purgeByModel(ctx, &model.Task{}, "updated_at", before, terminal)
}

// ReanchorAuditChains re-anchors each tenant's audit chain after an audit
// purge. It serializes against CreateAudit for the same tenant so a re-anchor
// can never interleave with a chain append.
func (s *store) ReanchorAuditChains(ctx context.Context, tenantIDs []string) error {
	for _, tenantID := range tenantIDs {
		if tenantID == "" {
			continue
		}
		unlock := s.lockTenant(tenantID)
		err := s.reanchorAuditChain(ctx, tenantID)
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// reanchorAuditChain renumbers the tenant's surviving rows to 1..N and
// recomputes the hash chain from a fresh anchor, restoring the gapless-seq
// property that VerifyAuditChain relies on.
func (s *store) reanchorAuditChain(ctx context.Context, tenantID string) error {
	var rows []model.AuditLog
	if err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("seq ASC").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	// Phase 1: free the (now gapped) positive seq space by negating it. The
	// (tenant_id, seq) unique index is unaffected because the negated values
	// remain distinct within the tenant.
	if err := s.WithContext(ctx).Model(&model.AuditLog{}).
		Where("tenant_id = ?", tenantID).
		Update("seq", gorm.Expr("seq * -1")).Error; err != nil {
		return err
	}
	// Phase 2: renumber 1..N in the original order and recompute the chain.
	prev := ""
	for i := range rows {
		var cur model.AuditLog
		if err := s.WithContext(ctx).Where("id = ?", rows[i].ID).First(&cur).Error; err != nil {
			return err
		}
		cur.Seq = int64(i + 1)
		cur.PrevHash = prev
		cur.Hash = model.AuditHash(prev, &cur)
		prev = cur.Hash
		if err := s.WithContext(ctx).Model(&model.AuditLog{}).
			Where("id = ?", rows[i].ID).
			Updates(map[string]interface{}{
				"seq":       cur.Seq,
				"prev_hash": cur.PrevHash,
				"hash":      cur.Hash,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

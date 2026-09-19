// Audit-chain external anchoring provides tamper evidence independent of the
// operational database. This implementation freezes the canonical audit tail;
// WORM/object-storage export can safely consume these immutable rows later.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const auditAnchorKeyPrefix = "audit_anchor:main:"

// defaultAuditAnchorInterval is used when the configured interval is invalid.
const defaultAuditAnchorInterval = time.Hour

// AuditAnchorSummary reports one platform-wide anchoring cycle.
type AuditAnchorSummary struct {
	Enabled       bool   `json:"enabled"`
	Tenants       int64  `json:"tenants"`
	Anchored      int64  `json:"anchored"`
	Unchanged     int64  `json:"unchanged"`
	Skipped       int64  `json:"skipped"`
	Exported      int64  `json:"exported"`
	ExportSkipped int64  `json:"export_skipped"`
	ExportDir     string `json:"export_dir,omitempty"`
}

// SetAuditAnchorPolicy stores the anchoring policy and cadence.
func (s *Service) SetAuditAnchorPolicy(cfg config.AuditAnchor) {
	s.auditAnchorMu.Lock()
	defer s.auditAnchorMu.Unlock()
	s.auditAnchorPolicy = cfg
}

func (s *Service) currentAuditAnchorPolicy() config.AuditAnchor {
	s.auditAnchorMu.Lock()
	defer s.auditAnchorMu.Unlock()
	return s.auditAnchorPolicy
}

// auditAnchorInterval resolves the cadence, falling back to one hour.
func (s *Service) auditAnchorInterval(cfg config.AuditAnchor) time.Duration {
	if cfg.IntervalSec > 0 {
		return time.Duration(cfg.IntervalSec) * time.Second
	}
	return defaultAuditAnchorInterval
}

// SetupAuditAnchor stores the policy and registers the worker. If enabled, the
// caller should schedule the first run after the runner is ready.
func (s *Service) SetupAuditAnchor(cfg config.AuditAnchor) {
	s.SetAuditAnchorPolicy(cfg)
	if s.Runner != nil {
		s.Runner.Register(&auditAnchorWorker{svc: s})
	}
}

// ScheduleAuditAnchor arms the recurring anchor job. Like retention, it is
// platform-wide, crash-recoverable, and never keeps two active jobs.
func (s *Service) ScheduleAuditAnchor(ctx context.Context, excludeID string) (bool, error) {
	cfg := s.currentAuditAnchorPolicy()
	if !cfg.Enabled {
		return false, nil
	}
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindAuditAnchor, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	key := auditAnchorKeyPrefix + time.Now().UTC().Format("20060102T150405Z")
	runAfter := time.Now().UTC().Add(s.auditAnchorInterval(cfg))
	return s.Runner.Enqueue(ctx, model.JobKindAuditAnchor, key, SystemTenantID, "", runAfter, 0)
}

type auditAnchorWorker struct {
	svc *Service
}

func (w *auditAnchorWorker) Kind() string { return model.JobKindAuditAnchor }

func (w *auditAnchorWorker) Run(ctx context.Context, job *model.Job) error {
	summary, err := w.svc.RunAuditAnchoring(ctx)
	if err != nil {
		logger.Warn("audit anchoring failed", "job_id", job.ID, "error", err)
		return err
	}
	if summary.Enabled {
		if _, err := w.svc.ScheduleAuditAnchor(ctx, job.ID); err != nil {
			logger.Warn("audit anchoring reschedule failed", "job_id", job.ID, "error", err)
			return err
		}
	}
	return nil
}

// RunAuditAnchoring snapshots every tenant's current audit tail. Empty chains
// have nothing to prove yet and are skipped; unchanged tails are idempotent.
func (s *Service) RunAuditAnchoring(ctx context.Context) (*AuditAnchorSummary, error) {
	cfg := s.currentAuditAnchorPolicy()
	if !cfg.Enabled {
		return &AuditAnchorSummary{Enabled: false}, nil
	}
	tenants, err := s.Store.ListAllTenants(ctx)
	if err != nil {
		return nil, err
	}
	summary := &AuditAnchorSummary{Enabled: true, Tenants: int64(len(tenants))}
	for _, tenant := range tenants {
		tail, err := s.Store.LastAudit(ctx, tenant.ID)
		if err != nil {
			return summary, err
		}
		if tail == nil {
			summary.Skipped++
			continue
		}
		anchor := &model.AuditAnchor{
			TenantID:  tenant.ID,
			LastSeq:   tail.Seq,
			LastHash:  tail.Hash,
			Algorithm: model.AuditAnchorAlgorithm,
			AnchorAt:  time.Now().UTC(),
		}
		created, err := s.Store.CreateAuditAnchor(ctx, anchor)
		if err != nil {
			return summary, err
		}
		if created {
			summary.Anchored++
		} else {
			summary.Unchanged++
		}
	}
	summary.ExportDir = cfg.ExportDir
	if cfg.ExportDir != "" {
		exported, skipped, err := s.ExportAuditAnchors(ctx, cfg.ExportDir)
		if err != nil {
			return summary, err
		}
		summary.Exported = exported
		summary.ExportSkipped = skipped
	}
	return summary, nil
}

// auditAnchorExport is the immutable external representation. It intentionally
// contains only the fields needed to prove that a tenant's chain tail existed.
type auditAnchorExport struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	LastSeq       int64     `json:"last_seq"`
	LastHash      string    `json:"last_hash"`
	Algorithm     string    `json:"algorithm"`
	AnchorAt      time.Time `json:"anchor_at"`
}

const auditAnchorExportSchemaVersion = 1

// ExportAuditAnchors copies permitted anchors to a WORM-backed directory. The
// target must already exist so an unmounted volume cannot silently fall back to
// a database-local path. Existing files are verified byte-for-byte and never
// rewritten, preserving WORM semantics.
func (s *Service) ExportAuditAnchors(ctx context.Context, dir string) (int64, int64, error) {
	if dir == "" {
		return 0, 0, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return 0, 0, fmt.Errorf("audit anchor export directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("audit anchor export path is not a directory: %s", dir)
	}
	anchors, err := s.Store.ListAuditAnchorsAll(ctx, "", true)
	if err != nil {
		return 0, 0, err
	}
	var exported, skipped int64
	for _, anchor := range anchors {
		if err := ctx.Err(); err != nil {
			return exported, skipped, err
		}
		created, err := writeAuditAnchorExport(ctx, dir, anchor)
		if err != nil {
			return exported, skipped, err
		}
		if created {
			exported++
		} else {
			skipped++
		}
	}
	return exported, skipped, nil
}

func writeAuditAnchorExport(ctx context.Context, dir string, anchor model.AuditAnchor) (bool, error) {
	if !validAuditAnchorID(anchor.ID) {
		return false, fmt.Errorf("invalid audit anchor id: %s", anchor.ID)
	}
	document := auditAnchorExport{
		SchemaVersion: auditAnchorExportSchemaVersion,
		ID:            anchor.ID,
		TenantID:      anchor.TenantID,
		LastSeq:       anchor.LastSeq,
		LastHash:      anchor.LastHash,
		Algorithm:     anchor.Algorithm,
		AnchorAt:      anchor.AnchorAt.UTC(),
	}
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return false, err
	}
	payload = append(payload, '\n')
	target := filepath.Join(dir, anchor.ID+".json")
	temp, err := os.CreateTemp(dir, ".audit-anchor-*.tmp")
	if err != nil {
		return false, err
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if _, err := temp.Write(payload); err != nil {
		cleanup()
		return false, err
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return false, err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return false, err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return false, err
	}
	if created, err := verifyExistingAuditAnchorExport(target, payload); err == nil {
		_ = os.Remove(tempName)
		return created, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(tempName)
		return false, err
	}
	if created, err := linkAuditAnchorExport(ctx, dir, target, tempName, payload); err == nil {
		return created, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(tempName)
		return false, err
	}
	_ = os.Remove(tempName)
	return false, nil
}

func verifyExistingAuditAnchorExport(target string, payload []byte) (bool, error) {
	if existing, err := os.ReadFile(target); err == nil {
		if string(existing) != string(payload) {
			return false, fmt.Errorf("audit anchor export content mismatch: %s", target)
		}
		return false, nil
	}
	return false, fs.ErrNotExist
}

func linkAuditAnchorExport(ctx context.Context, dir, target, tempName string, payload []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err := os.Link(tempName, target)
	_ = os.Remove(tempName)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrExist) {
		if _, verifyErr := verifyExistingAuditAnchorExport(target, payload); verifyErr != nil {
			return false, verifyErr
		}
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	file, fileErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if fileErr != nil {
		if errors.Is(fileErr, fs.ErrExist) {
			if _, verifyErr := verifyExistingAuditAnchorExport(target, payload); verifyErr != nil {
				return false, verifyErr
			}
			return false, nil
		}
		return false, fileErr
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	return true, nil
}

func validAuditAnchorID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		isDigit := char >= '0' && char <= '9'
		isLowerHex := char >= 'a' && char <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}

// ListAuditAnchors returns the tenant's anchors, or every tenant for platform
// administrators. Scope is enforced by RBAC and the repository filter.
func (s *Service) ListAuditAnchors(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int) ([]model.AuditAnchor, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListAuditAnchors(ctx, tenantID, scopeAll, page, pageSize)
}

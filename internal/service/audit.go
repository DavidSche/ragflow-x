package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// RecordAudit appends an audit record, stamping id and timestamp.
func (s *Service) RecordAudit(ctx context.Context, entry *model.AuditLog) error {
	if entry.ID == "" {
		entry.ID = id.New()
	}
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if entry.ActorTenantID == "" {
		entry.ActorTenantID = entry.TenantID
	}
	if entry.TargetTenantID == "" {
		entry.TargetTenantID = entry.TenantID
	}
	if entry.Result == "" {
		entry.Result = "SUCCESS"
	}
	if entry.AuthorizationDecision == "" {
		entry.AuthorizationDecision = "ALLOW"
	}
	if entry.AuthorizationPolicyVersion == "" {
		entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
	}
	entry.DetailJSON = redactAuditSecrets(entry.DetailJSON)
	if err := s.Store.CreateAudit(ctx, entry); err != nil {
		logger.Warn("audit write failed",
			"tenant_id", entry.TenantID,
			"user_id", entry.UserID,
			"action", entry.Action,
			"resource", entry.Resource,
			"resource_id", entry.ResourceID,
			"error", err,
		)
		return err
	}
	s.emitAuditAlert(ctx, entry)
	return nil
}

func (s *Service) emitAuditAlert(ctx context.Context, entry *model.AuditLog) {
	if entry.Action != AuditActionProviderCompensationFailed {
		return
	}
	detail := map[string]any{}
	if entry.DetailJSON != "" {
		_ = json.Unmarshal([]byte(entry.DetailJSON), &detail)
	}
	operation, _ := detail["operation"].(string)
	stage, _ := detail["stage"].(string)
	cause, _ := detail["error"].(string)
	notify.Emit(ctx, notify.Event{
		ID:         entry.ID,
		Title:      "Provider compensation failed",
		Severity:   "error",
		Type:       entry.Action,
		TenantID:   entry.TenantID,
		Resource:   entry.Resource,
		ResourceID: entry.ResourceID,
		Detail:     "Provider state compensation could not be completed; manual reconciliation is required.",
		OccurredAt: entry.At,
		Fields: map[string]string{
			"operation": operation,
			"stage":     stage,
			"error":     cause,
			"trace_id":  entry.TraceID,
		},
	})
}

func redactAuditSecrets(raw string) string {
	if !json.Valid([]byte(raw)) {
		return raw
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	redacted := redactSecretValue(value)
	next, err := json.Marshal(redacted)
	if err != nil {
		return raw
	}
	return string(next)
}

func redactSecretValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveAuditKey(key) {
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = redactSecretValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactSecretValue(item)
		}
		return result
	default:
		return value
	}
}

func sensitiveAuditKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "secret") ||
		strings.Contains(key, "token") || strings.Contains(key, "api_key") ||
		strings.Contains(key, "credential")
}

// ListAudits returns a page of audit records scoped to a tenant.
func (s *Service) ListAudits(ctx context.Context, tenantID string, page, pageSize int, filter repository.AuditFilter) ([]model.AuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListAudits(ctx, tenantID, page, pageSize, filter)
}

func (s *Service) ListAuditsForScope(ctx context.Context, scope TenantScope, page, pageSize int, filter repository.AuditFilter) ([]model.AuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListAuditsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), page, pageSize, filter)
}

// ExportAudits renders audit rows as CSV for compliance export.
func (s *Service) ExportAudits(ctx context.Context, tenantID string) ([]byte, error) {
	rows, err := s.Store.ListAuditsAll(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return auditCSV(rows)
}

const auditExportLimit = 10000

func (s *Service) ExportAuditsForScope(ctx context.Context, scope TenantScope) (int64, []byte, error) {
	rows, total, err := s.Store.ListAuditsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), 1, auditExportLimit, repository.AuditFilter{})
	if err != nil {
		return 0, nil, err
	}
	if total > auditExportLimit {
		return 0, nil, httperr.BadRequest(40096, "导出结果超过 10,000 行，请缩小筛选范围")
	}
	data, err := auditCSV(rows)
	if err != nil {
		return 0, nil, err
	}
	return int64(len(rows)), data, nil
}

func auditCSV(rows []model.AuditLog) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"at", "user_id", "action", "resource", "resource_id", "detail", "ip", "trace_id", "hash", "prev_hash"})
	for _, r := range rows {
		_ = w.Write([]string{
			r.At.UTC().Format(time.RFC3339), sanitizeCSVCell(r.UserID), sanitizeCSVCell(r.Action),
			sanitizeCSVCell(r.Resource), sanitizeCSVCell(r.ResourceID), sanitizeCSVCell(r.DetailJSON),
			sanitizeCSVCell(r.IP), sanitizeCSVCell(r.TraceID), sanitizeCSVCell(r.Hash),
			sanitizeCSVCell(r.PrevHash),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// VerifyAuditChain recomputes the audit hashes and reports whether the chain
// is intact (tamper detection). Rows are walked in seq order and must be
// gapless: a missing sequence number proves a record was deleted, which hash
// recomputation alone could miss when only the endpoints survive.
func (s *Service) VerifyAuditChain(ctx context.Context, tenantID string) (bool, error) {
	rows, err := s.Store.ListAuditsAll(ctx, tenantID)
	if err != nil {
		return false, err
	}
	prev := ""
	for i, r := range rows {
		if r.Seq != int64(i+1) || r.PrevHash != prev || r.Hash != model.AuditHash(prev, &r) {
			return false, nil
		}
		prev = r.Hash
	}
	return true, nil
}

func (s *Service) RepairAuditChain(ctx context.Context, tenantID string) (bool, error) {
	if strings.TrimSpace(tenantID) == "" {
		return false, httperr.BadRequest(40000, "tenant_id is required")
	}
	if err := s.Store.ReanchorAuditChains(ctx, []string{tenantID}); err != nil {
		return false, err
	}
	return s.VerifyAuditChain(ctx, tenantID)
}

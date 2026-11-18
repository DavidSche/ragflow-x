package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// CloneReportTemplateResult is what the clone route returns: the fork as a
// normal template view plus the frozen provenance line the detail page renders
// as "克隆自 source@version，平台当前已到 vN".
type CloneReportTemplateResult struct {
	// Template is the workspace's new fork row.
	Template              ReportTemplateView `json:"template"`
	SourceTemplateID      string             `json:"source_template_id"`
	SourceTemplateVersion int64              `json:"source_template_version"`
	SourceVersionPinned   bool               `json:"source_version_pinned"`
	// Created is false when the request was a replay of an identical fork and the
	// already-committed clone was returned instead (double-click safe).
	Created bool `json:"created"`
}

// CloneReportTemplate forks the platform baseline for `templateKey` into the
// workspace's private version sequence (doc/288 §11.4 / D4).
//
// The fork is a new row, never an overwrite: it keeps its own 1-based version
// sequence per `(template_key, tenant_id)` — a tenant's version 1 is not the
// platform's version 1 — and carries `source_template_id` /
// `source_template_version` so the provenance is auditable even after the
// platform ships newer versions. A later `POST :key/clone` forks the newer
// baseline as the tenant's next version (2, 3, …); the older clone is left
// untouched, which is what makes "平台升级不覆盖克隆" hold by construction.
func (s *Service) CloneReportTemplate(
	ctx context.Context, actorID, tenantID, templateKey string,
) (*CloneReportTemplateResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "manage", "report-template", tenantID, ""); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(templateKey)
	if key == "" {
		return nil, errReportTemplateNotFound()
	}
	// The source is the platform baseline, not whatever the workspace currently
	// resolves: a clone means "fork upstream", and fork-on-top-of-your-own-fork
	// would silently freeze stale upstream content forever.
	source, err := s.Store.GetReportTemplate(ctx, model.PlatformTenantID, key, 0)
	if err != nil {
		return nil, err
	}
	if source == nil || source.RetiredAt != nil || source.Status == model.ReportTemplateStatusRetired {
		return nil, errReportTemplateNotFound()
	}
	payload, err := reportTemplatePayloadOf(source)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	clone, err := reportTemplateRow(tenantID, key, 1, payload, now)
	if err != nil {
		return nil, err
	}
	clone.CreatedBy = actorID
	clone.SourceTemplateID = source.ID
	clone.SourceTemplateVersion = source.Version
	clone.SourceVersionPinned = true
	// The workspace fork starts its own sequence at the next integer after its
	// own newest version; the platform's version numbers are irrelevant to it.
	// A retired fork still occupies its slot in the sequence, so the next clone
	// advances past it rather than colliding.
	if current, err := s.Store.GetReportTemplate(ctx, tenantID, key, 0); err != nil {
		return nil, err
	} else if current != nil {
		clone.Version = current.Version + 1
	}

	created := true
	if err := s.Store.CreateReportTemplate(ctx, clone); err != nil {
		replay, replayErr := s.cloneReplay(ctx, tenantID, key, source.ID, source.Version, payload)
		if replayErr != nil {
			return nil, fmt.Errorf("clone %q: %w", key, err)
		}
		clone = replay
		created = false
	}

	view, err := s.reportTemplateViewOf(clone)
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.template.cloned",
		Resource: "report-template", ResourceID: clone.TemplateKey, Result: "SUCCESS",
		DetailJSON: fmt.Sprintf(`{"template_key":%q,"version":%d,"source_template_id":%q,"source_template_version":%d,"created":%t}`,
			key, clone.Version, source.ID, source.Version, created),
		At: now,
	}); auditErr != nil {
		logger.Warn("report template clone audit failed", "template_key", key, "error", auditErr)
		return nil, auditErr
	}
	return &CloneReportTemplateResult{
		Template: *view, SourceTemplateID: source.ID,
		SourceTemplateVersion: source.Version, SourceVersionPinned: true, Created: created,
	}, nil
}

// cloneReplay decides whether a version collision is a replay of this exact fork.
// Without it, two concurrent clones of the same key would allocate the same next
// version and one would surface a raw unique-constraint error instead of the
// other's result.
func (s *Service) cloneReplay(
	ctx context.Context, tenantID, templateKey, sourceID string, sourceVersion int64,
	want *ReportTemplatePayload,
) (*model.ReportTemplate, error) {
	current, err := s.Store.GetReportTemplate(ctx, tenantID, templateKey, 0)
	if err != nil {
		return nil, err
	}
	if current == nil || current.SourceTemplateID != sourceID || current.SourceTemplateVersion != sourceVersion {
		return nil, fmt.Errorf("clone raced but no identical fork is committed")
	}
	stored, err := reportTemplatePayloadOf(current)
	if err != nil {
		return nil, err
	}
	if hashReportTemplatePayload(stored) != hashReportTemplatePayload(want) {
		return nil, fmt.Errorf("clone raced and the committed fork differs")
	}
	return current, nil
}

func (s *Service) reportTemplateViewOf(row *model.ReportTemplate) (*ReportTemplateView, error) {
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		return nil, err
	}
	view := &ReportTemplateView{
		TemplateID: row.ID, Key: row.TemplateKey, Version: row.Version,
		TenantID: row.TenantID, Payload: *payload, Status: row.Status,
		Retired: row.RetiredAt != nil || row.Status == model.ReportTemplateStatusRetired,
	}
	return view, nil
}

package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Report template authoring (doc/295 §2.3 / R19).
//
// P0-B gave a workspace a way to *fork* the platform baseline (`POST :key/clone`).
// That is enough to run with a customisation, not enough to own one: every change
// had to be re-cloned from upstream, and there was no way to take a template out
// of service without deleting a report's provenance. P2 adds the two missing
// verbs — **publish a new version** and **retire** — under the same rules the
// platform baseline is held to.
//
// Three invariants keep this from becoming a second, weaker template system:
//
//   - a version row is immutable: publishing always inserts version N+1, so a
//     report that pinned N keeps rendering N (doc/288 A38);
//   - a workspace payload goes through exactly the same
//     `validateReportTemplatePayload` the seed uses, so a hand-authored template
//     cannot be shape-invalid in a way the platform's are not;
//   - retirement is a state on the newest version, and resolution already refuses
//     to offer a retired newest row for a new run (44101). Historical revisions
//     resolve by explicit version and keep working.

// reportTemplateKeyPattern is what a key may look like. Keys travel into export
// metadata, audit details and error messages, so the shape is enforced rather than
// sanitised: a key with a space would make `report template %q not found` read
// like two different sentences depending on where it landed.
var reportTemplateKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,62}$`)

// ReportTemplatePublishRequest is the authoring body. `Key` is carried in the body
// (not only in the path) so create and update can share one validator and one
// handler; the two must agree or a client that PUTs `/report-templates/x` with
// `{"key":"y"}` would silently publish `y`.
type ReportTemplatePublishRequest struct {
	Key     string                `json:"key"`
	Payload ReportTemplatePayload `json:"payload"`
}

// ReportTemplateMutationResult is a publish/retire answer. `Created` is false for
// an idempotent replay: re-publishing byte-identical content returns the existing
// newest version instead of minting an empty version number. Version numbers are
// audit coordinates — inflating them with no-ops makes "which version changed the
// outline" unanswerable.
type ReportTemplateMutationResult struct {
	Template ReportTemplateView `json:"template"`
	Created  bool               `json:"created"`
}

// validateReportTemplateKey rejects anything that is not a lower-case slug.
func validateReportTemplateKey(key string) error {
	if !reportTemplateKeyPattern.MatchString(key) {
		return fmt.Errorf("template key must match %s", reportTemplateKeyPattern.String())
	}
	return nil
}

// PublishReportTemplate creates the workspace's next version of `key` (doc/295 A58).
//
// The platform baseline is never touched: publishing under a key the platform also
// ships simply makes the workspace row win at resolution time (that is how a
// customisation takes effect). A payload that fails validation is a 400/44105 at
// the boundary, not a row that fails later inside a run.
func (s *Service) PublishReportTemplate(
	ctx context.Context, actorID, tenantID string, req ReportTemplatePublishRequest,
) (*ReportTemplateMutationResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "manage", "report-template", tenantID, ""); err != nil {
		return nil, err
	}
	if tenantID == "" || tenantID == model.PlatformTenantID {
		// The platform catalogue is seeded at release time. Editing it through the
		// tenant route would mean one workspace can change every other workspace's
		// baseline — a governance action that must go through a deploy.
		return nil, errReportOutlineInvalid("the platform template catalogue is not editable through this route")
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return nil, errReportOutlineInvalid("template key is required")
	}
	if err := validateReportTemplateKey(key); err != nil {
		return nil, errReportOutlineInvalid(err.Error())
	}
	payload := req.Payload
	if err := validateReportTemplatePayload(&payload); err != nil {
		return nil, errReportOutlineInvalid(err.Error())
	}

	now := time.Now().UTC()
	nextVersion := int64(1)
	current, err := s.Store.GetReportTemplate(ctx, tenantID, key, 0)
	if err != nil {
		return nil, err
	}
	if current != nil {
		nextVersion = current.Version + 1
		stored, err := reportTemplatePayloadOf(current)
		if err != nil {
			return nil, err
		}
		if hashReportTemplatePayload(stored) == hashReportTemplatePayload(&payload) {
			// Identical content: the honest answer is the version that already says
			// exactly this, and `Created=false` tells the client nothing was added.
			view, err := s.reportTemplateViewOf(current)
			if err != nil {
				return nil, err
			}
			return &ReportTemplateMutationResult{Template: *view, Created: false}, nil
		}
	}

	row, err := reportTemplateRow(tenantID, key, nextVersion, &payload, now)
	if err != nil {
		return nil, err
	}
	row.CreatedBy = actorID
	// Provenance: a version authored in the workspace has no upstream id, but
	// keeping the platform's id/version here would be a lie. Empty is the truthful
	// value and the view reports it as `source=workspace`.
	if err := s.Store.CreateReportTemplate(ctx, row); err != nil {
		return nil, err
	}
	view, err := s.reportTemplateViewOf(row)
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID,
		Action: "report.template.version_published", Resource: "report-template",
		ResourceID: key, Result: "SUCCESS",
		DetailJSON: fmt.Sprintf(`{"template_key":%q,"version":%d}`, key, row.Version),
		At:         now,
	}); auditErr != nil {
		logger.Warn("report template publish audit failed", "template_key", key, "error", auditErr)
		return nil, auditErr
	}
	return &ReportTemplateMutationResult{Template: *view, Created: true}, nil
}

// RetireReportTemplate takes the workspace's newest version out of service.
//
// Retiring an older version would be a no-op (resolution always looks at the
// newest row) and would misrepresent the state, so the call is refused instead of
// quietly doing nothing. A retired newest version stays listed — historical
// reports must remain explainable — and `resolveReportTemplate` answers 44101 for
// any *new* run that names it.
func (s *Service) RetireReportTemplate(
	ctx context.Context, actorID, tenantID, templateKey string,
) (*ReportTemplateMutationResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "manage", "report-template", tenantID, ""); err != nil {
		return nil, err
	}
	if tenantID == "" || tenantID == model.PlatformTenantID {
		return nil, errReportTemplateNotFound()
	}
	key := strings.TrimSpace(templateKey)
	if key == "" {
		return nil, errReportTemplateNotFound()
	}
	current, err := s.Store.GetReportTemplate(ctx, tenantID, key, 0)
	if err != nil {
		return nil, err
	}
	if current == nil {
		// Not a workspace template. The platform baseline of the same key is not
		// retired by a tenant — that would take it away from every other workspace.
		return nil, errReportTemplateNotFound()
	}
	now := time.Now().UTC()
	if current.RetiredAt == nil && current.Status != model.ReportTemplateStatusRetired {
		current.RetiredAt = &now
		current.Status = model.ReportTemplateStatusRetired
		if err := s.Store.UpdateReportTemplate(ctx, current); err != nil {
			return nil, err
		}
	}
	view, err := s.reportTemplateViewOf(current)
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID,
		Action: "report.template.retired", Resource: "report-template",
		ResourceID: key, Result: "SUCCESS",
		DetailJSON: fmt.Sprintf(`{"template_key":%q,"version":%d}`, key, current.Version),
		At:         now,
	}); auditErr != nil {
		logger.Warn("report template retire audit failed", "template_key", key, "error", auditErr)
		return nil, auditErr
	}
	return &ReportTemplateMutationResult{Template: *view, Created: false}, nil
}

// reportTemplateSourceOf labels where a row came from. It is derived, never
// stored: `tenant_id == platform` is exactly what "platform seed" means, and the
// clone columns already tell a workspace row's upstream.
func reportTemplateSourceOf(row *model.ReportTemplate) string {
	if row.TenantID == model.PlatformTenantID || row.TenantID == "" {
		return "platform"
	}
	return "workspace"
}

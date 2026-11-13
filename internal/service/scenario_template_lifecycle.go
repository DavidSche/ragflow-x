package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// Baseline lifecycle (doc/276 §7.1):
//
//	platform baseline (source=builtin, tenant=PlatformTenantID, read-only to workspaces)
//	  └─ fork ──▶ tenant copy (source=copy, baseline_key/version, lineage=confirmed_baseline)
//	        └─ update ──▶ new immutable version in the tenant (baseline never moves)
//
// The platform baseline is never written by a workspace, and a fork never
// tracks later baseline versions automatically: the catalog only reports
// `baselineUpdateAvailable` so the tenant decides whether to re-fork.

// Lifecycle validation codes (doc/276 §11.2). 40105 is the knowledge contract
// code; the rest are the fork/publish input checks that the design leaves to
// implementation, kept here so they are registered in one place.
const (
	codeTemplateKnowledgeInvalid = 40105
	codeForkRequiresBaseline     = 40107
	codeForkPlatformSelf         = 40108
	codeBaselineKeyRequired      = 40109
	codePublishRequiresBuiltin   = 40114
	codeBaselineVersionStale     = 40115
)

// ResolveScenarioTemplateAsset returns the caller's row, falling back to the
// platform baseline row. Baseline assets are shared read-only content, so a
// workspace may read/fork/instantiate them by their platform id; any row owned
// by a *different* workspace still resolves to nil (404, never 403).
func (s *Service) ResolveScenarioTemplateAsset(ctx context.Context, tenantID, templateID string) (*model.ScenarioTemplateAsset, error) {
	asset, err := s.Store.GetScenarioTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if asset != nil {
		return asset, nil
	}
	if tenantID == model.PlatformTenantID {
		return nil, nil
	}
	return s.Store.GetPlatformScenarioTemplate(ctx, templateID)
}

// isPlatformBaseline reports whether the asset is a platform baseline row.
func isPlatformBaseline(asset *model.ScenarioTemplateAsset) bool {
	return asset != nil && asset.TenantID == model.PlatformTenantID
}

// ForkScenarioTemplate creates a tenant-owned copy of a platform baseline
// (doc/276 §7.1 step 4). It is an explicit action: instantiation never forks.
// A tenant key collision returns the existing 40901 code so the wizard can tell
// the user to rename instead of receiving a generic conflict.
func (s *Service) ForkScenarioTemplate(ctx context.Context, tenantID, userID, templateID string) (*model.ScenarioTemplateAsset, error) {
	if err := s.AuthorizeABAC(ctx, userID, "manage", "scenario-template", tenantID, "", ""); err != nil {
		return nil, err
	}
	if tenantID == "" || tenantID == model.PlatformTenantID {
		return nil, httperr.BadRequest(codeForkPlatformSelf, "fork is a workspace action; the platform baseline is not forked into itself")
	}
	baseline, err := s.Store.GetPlatformScenarioTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if baseline == nil {
		// A tenant-owned row id is not a fork source: forking a row you already
		// own would silently duplicate it under a colliding key.
		return nil, httperr.NotFound("platform baseline not found")
	}
	if baseline.Source != model.TemplateSourceBuiltin {
		return nil, httperr.BadRequest(codeForkRequiresBaseline, "only a platform baseline can be forked")
	}
	existing, err := s.Store.GetScenarioTemplateByKey(ctx, tenantID, baseline.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, httperr.New(409, 40901, "template key already exists in this workspace; rename the existing template or update it instead of forking")
	}
	version, err := s.Store.GetScenarioTemplateVersion(ctx, model.PlatformTenantID, baseline.ID, baseline.LatestVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("platform baseline version not found")
	}
	payload, err := parseTemplatePayload(version.PayloadJSON)
	if err != nil {
		return nil, err
	}
	if err := payload.Knowledge.Validate(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	asset := &model.ScenarioTemplateAsset{
		ID: id.New(), TenantID: tenantID, Key: baseline.Key, Name: baseline.Name,
		AppTypes: baseline.AppTypes, Description: baseline.Description,
		LatestVersion: 1, Source: model.TemplateSourceCopy, Status: model.AssetStatusPublished,
		PayloadJSON: version.PayloadJSON, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		BaselineKey: baseline.Key, BaselineVersion: baseline.LatestVersion,
		LineageState: model.LineageStateConfirmedBaseline,
	}
	forked := &model.ScenarioTemplateVersion{
		ID: id.New(), TenantID: tenantID, TemplateID: asset.ID, Version: 1,
		PayloadJSON:        version.PayloadJSON,
		ChangeNote:         "forked from baseline v" + strconv.FormatInt(baseline.LatestVersion, 10),
		PayloadSchema:      model.TemplatePayloadSchemaV2,
		SnapshotProvenance: model.SnapshotAuthored,
		CreatedBy:          userID, CreatedAt: now,
	}
	if err := s.Store.CreateScenarioTemplate(ctx, asset, forked); err != nil {
		return nil, err
	}
	s.recordTemplateAudit(ctx, tenantID, userID, "scenario_template.fork", asset.ID,
		`{"baseline_key":"`+baseline.Key+`","baseline_version":`+strconv.FormatInt(baseline.LatestVersion, 10)+`}`)
	return asset, nil
}

// BaselinePublishInput is the minimal publish request (doc/276 §7.1 step 8).
// The body selects a version + note; the content is the latest draft of the
// key, so a publish cannot silently invent content.
type BaselinePublishInput struct {
	Version    int64  `json:"version"`
	ChangeNote string `json:"change_note"`
}

// ListScenarioTemplateBaselines returns every platform baseline row. It is the
// read side of the platform-only `scenario-template-baseline` resource.
func (s *Service) ListScenarioTemplateBaselines(ctx context.Context) ([]model.ScenarioTemplateAsset, error) {
	baselines, err := s.Store.ListPlatformScenarioTemplates(ctx, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(baselines, func(i, j int) bool { return baselines[i].Key < baselines[j].Key })
	return baselines, nil
}

// PublishScenarioTemplateBaseline appends a new version of a platform baseline
// key and bumps `latest_version` (never rewrites history). Publishing the same
// payload twice is idempotent and answered with replayed=true.
func (s *Service) PublishScenarioTemplateBaseline(ctx context.Context, actorID, key string, input BaselinePublishInput) (*model.ScenarioTemplateAsset, bool, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil, false, httperr.BadRequest(codeBaselineKeyRequired, "baseline key is required")
	}
	// Platform scope + permission: only a platform administrator publishes the
	// shared baseline; workspaces fork instead.
	if err := s.AuthorizeABAC(ctx, actorID, "manage", "scenario-template-baseline", model.PlatformTenantID, "", ""); err != nil {
		return nil, false, err
	}
	asset, err := s.Store.GetPlatformScenarioTemplateByKey(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if asset == nil {
		return nil, false, httperr.NotFound("platform baseline not found")
	}
	if asset.Source != model.TemplateSourceBuiltin {
		return nil, false, httperr.BadRequest(codePublishRequiresBuiltin, "only builtin platform rows can be published as baseline")
	}
	current, err := s.Store.GetScenarioTemplateVersion(ctx, model.PlatformTenantID, asset.ID, asset.LatestVersion)
	if err != nil {
		return nil, false, err
	}
	if current == nil {
		return nil, false, httperr.NotFound("platform baseline version not found")
	}
	payload, err := parseTemplatePayload(current.PayloadJSON)
	if err != nil {
		return nil, false, err
	}
	if err := payload.Knowledge.Validate(); err != nil {
		return nil, false, err
	}
	if err := s.enforceScenarioEvalGate(ctx, model.PlatformTenantID, asset.ID, current.PayloadJSON); err != nil {
		return nil, false, err
	}
	// Publishing is a state transition, never a rewrite: it releases the latest
	// immutable version and records it as the published baseline version. New
	// content arrives through UpdateScenarioTemplate (append-only), so a publish
	// can neither invent content nor invalidate an older version.
	if input.Version > asset.LatestVersion {
		return nil, false, httperr.BadRequest(codeBaselineVersionStale, "version must not exceed the latest stored version")
	}
	if asset.Status == model.AssetStatusPublished && asset.BaselineVersion == asset.LatestVersion && input.Version <= asset.LatestVersion {
		return asset, true, nil
	}
	if err := s.Store.SetScenarioTemplateLineage(ctx, model.PlatformTenantID, asset.ID, key, asset.LatestVersion, model.LineageStateConfirmedBaseline); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, httperr.NotFound("platform baseline not found")
		}
		return nil, false, err
	}
	if err := s.Store.UpdateScenarioTemplateStatus(ctx, model.PlatformTenantID, asset.ID, model.AssetStatusPublished); err != nil {
		return nil, false, err
	}
	asset.Status = model.AssetStatusPublished
	asset.BaselineKey = key
	asset.BaselineVersion = asset.LatestVersion
	asset.LineageState = model.LineageStateConfirmedBaseline
	asset.UpdatedAt = time.Now().UTC()
	nextVersion := asset.LatestVersion
	s.recordTemplateAudit(ctx, model.PlatformTenantID, actorID, "scenario_template.baseline_publish", asset.ID,
		`{"key":"`+key+`","version":`+strconv.FormatInt(nextVersion, 10)+`,"payload_hash":"`+hashTemplatePayload(asset.PayloadJSON)+`"}`)
	return asset, false, nil
}

// BootstrapScenarioTemplateLineage upgrades tenant rows from legacy_unknown to
// confirmed_baseline when their payload is byte-identical (after canonical JSON)
// to the platform baseline of the same key (doc/276 §7.1 migration 165 rule ②).
// It is idempotent and never overwrites a row that already carries a baseline
// key, so a re-fork or a tenant edit keeps its own lineage.
func (s *Service) BootstrapScenarioTemplateLineage(ctx context.Context) {
	baselines, err := s.Store.ListPlatformScenarioTemplates(ctx, repository.GovernanceFilter{})
	if err != nil {
		logger.Warn("scenario template lineage backfill: list baselines failed", "error", err)
		return
	}
	byKey := make(map[string]model.ScenarioTemplateAsset, len(baselines))
	for _, baseline := range baselines {
		byKey[baseline.Key] = baseline
		// Platform rows seeded before 2.0 carry no key/version. Normalize them
		// here rather than in SQL: `key` is a reserved word on MySQL, so the
		// migration deliberately leaves this to the idempotent backfill.
		if baseline.BaselineKey == "" || baseline.LineageState != model.LineageStateConfirmedBaseline {
			if err := s.Store.SetScenarioTemplateLineage(ctx, model.PlatformTenantID, baseline.ID, baseline.Key, baseline.LatestVersion, model.LineageStateConfirmedBaseline); err != nil {
				logger.Warn("scenario template lineage backfill: normalize platform row failed", "template_id", baseline.ID, "error", err)
			}
		}
	}
	if len(byKey) == 0 {
		return
	}
	const pageSize = 200
	for page := 1; ; page++ {
		tenants, total, err := s.Store.ListTenants(ctx, page, pageSize, repository.TenantFilter{})
		if err != nil {
			logger.Warn("scenario template lineage backfill: list tenants failed", "error", err)
			return
		}
		for _, tenant := range tenants {
			if tenant.ID == model.PlatformTenantID {
				continue
			}
			assets, _, err := s.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 500, repository.GovernanceFilter{})
			if err != nil {
				logger.Warn("scenario template lineage backfill: list assets failed", "tenant_id", tenant.ID, "error", err)
				continue
			}
			for i := range assets {
				asset := assets[i]
				if asset.BaselineKey != "" || asset.Source != model.TemplateSourceCopy {
					continue
				}
				baseline, ok := byKey[asset.Key]
				if !ok {
					continue
				}
				if hashTemplatePayload(asset.PayloadJSON) != hashTemplatePayload(baseline.PayloadJSON) {
					continue
				}
				if err := s.Store.SetScenarioTemplateLineage(ctx, tenant.ID, asset.ID, baseline.Key, baseline.LatestVersion, model.LineageStateConfirmedBaseline); err != nil {
					logger.Warn("scenario template lineage backfill: set lineage failed", "tenant_id", tenant.ID, "template_id", asset.ID, "error", err)
				}
			}
		}
		if int64(page*pageSize) >= total || len(tenants) == 0 {
			return
		}
	}
}

// recordTemplateAudit writes one append-only audit entry for a lifecycle event.
func (s *Service) recordTemplateAudit(ctx context.Context, tenantID, userID, action, objectID, detail string) {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: userID,
		Action: action, Resource: "scenario-template",
		Result: "SUCCESS", DetailJSON: detail, At: time.Now().UTC(),
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		logger.Warn("scenario template audit failed", "action", action, "error", err)
	}
}

// sortCatalogByKey keeps the catalog deterministic regardless of the two
// sources (platform baselines then tenant overrides) it is merged from.
func sortCatalogByKey(items []ScenarioTemplateCatalogItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Key < items[j].Key })
}

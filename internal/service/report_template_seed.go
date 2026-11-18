package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Report content-template seeding (doc/288 §11.1 / §11.2).
//
// P0-A shipped exactly one template, `report-compliance`, because its acceptance
// list only claimed "1 个模板（制度符合性）". P0-B adds the two templates its scope
// promised (`report-tech-review`, `report-postmortem`), which is why this file
// is a catalogue rather than a single literal.
//
// Every row is seeded into the *platform* tenant and read as a baseline by every
// workspace, the same posture as the scenario-template catalogue: one authored
// copy, no per-tenant drift, and a workspace that customises one forks into its own
// version sequence (292 D4).
//
// The three templates differ in **section graph**, not just in prose. That is the
// point of shipping more than one: `depends_on` chains, the number of sections that
// require evidence, and the order a reader expects are what a template actually is
// (§11.1). A second template that reused the compliance section keys would prove
// nothing about the outline/ordering path.

// builtinReportBootstrapActor is the audit actor of system-seeded templates.
// Tenant users never authenticate as this principal.
const builtinReportBootstrapActor = "system"

// Template keys. They are referenced by the creation path, by the export layout id
// and by tests, so each lives in exactly one place. Renaming a key is a breaking
// change for every report pinned to it: keys are contract, versions are content.
const (
	reportComplianceTemplateKey = "report-compliance"
	// Kept compact on purpose: `rgx_export_template_version.template_id` is 32 bytes
	// and the export layout id is `<key>-v<version>-<format>` (docx|markdown|pdf), so
	// `report-tech-review-v1-markdown` (30) fits while a spelled-out key would not.
	reportTechnicalReviewKey    = "report-tech-review"
	reportPostmortemTemplateKey = "report-postmortem"
)

// BuiltinReportTemplate is one shipped content template.
type BuiltinReportTemplate struct {
	Key     string
	Payload ReportTemplatePayload
}

// builtinReportTemplates is the shipped catalogue. Section keys are a contract:
// `structure_completeness` validates them, so renaming one is a template version
// bump, never an in-place edit (doc/288 §11.1).
var builtinReportTemplates = []BuiltinReportTemplate{
	{
		Key: reportComplianceTemplateKey,
		Payload: ReportTemplatePayload{
			Name: ReportTemplateName{
				Zh: "制度符合性分析报告",
				En: "Compliance analysis report",
			},
			OutputFormats: []string{"docx", "md", "pdf"},
			Sections: []ReportTemplateSection{
				{
					Key: "background", Title: "背景与范围",
					Prompt:           "说明本次合规评估的业务范围、评估对象与资料来源边界；不引用结论性判断。",
					RequiresEvidence: false, MaxWords: 300,
				},
				{
					Key: "applicable_rules", Title: "适用制度清单",
					Prompt:           "列出与被评估对象相关的制度条款，逐条给出制度名称、条款位置与适用范围。",
					RequiresEvidence: true, MaxWords: 500,
				},
				{
					Key: "compliance", Title: "逐条符合性判定",
					Prompt:           "对每一条适用制度判定「符合 / 部分符合 / 不符合 / 资料不足」，判定必须引用制度条款与执行证据。",
					RequiresEvidence: true, MaxWords: 1200,
					DependsOn: []string{"applicable_rules"},
				},
				{
					Key: "gaps", Title: "差距与风险",
					Prompt:           "汇总不符合与部分符合项，说明差距事实、可能后果与影响的业务环节；不得把「资料不足」写成「不符合」。",
					RequiresEvidence: true, MaxWords: 800,
					DependsOn: []string{"compliance"},
				},
				{
					Key: "remediation", Title: "整改建议",
					Prompt:           "针对每条差距给出可执行的整改动作、责任角色与验证方式，并与差距逐条对应。",
					RequiresEvidence: true, MaxWords: 600,
					DependsOn: []string{"gaps"},
				},
			},
			Limits: ReportTemplateLimits{
				MaxSections: 10, SliceMaxSeconds: 480, TotalMaxSeconds: 3600,
				MaxSlices: 8, MaxTokensPerSection: 1600,
			},
			Retrieval: ReportTemplateRetrieval{
				TopK: 8, ThresholdPolicy: "dataset_calibrated", MinEvidence: 1,
			},
			EvidencePolicy: ReportTemplateEvidencePolicy{
				ConflictHandling:  reportConflictHandlingP0,
				OutdatedEvidence:  reportOutdatedEvidenceP0,
				UnsupportedClaims: reportUnsupportedClaimsP0,
			},
			// P0-A only implements `required`; `auto` stays frozen in the closed
			// set with an empty role list so it cannot be turned on by accident
			// (292 D2).
			OutlineConfirmation:     model.ReportOutlineRequired,
			AutoConfirmAllowedRoles: []string{},
		},
	},
	{
		// Technical review: the reader wants "what was proposed, does it hold, what
		// is still unproven". `assessment` is the only section allowed to be long,
		// and it depends on both the criteria and the proposal so it can judge one
		// against the other instead of restating the proposal.
		Key: reportTechnicalReviewKey,
		Payload: ReportTemplatePayload{
			Name: ReportTemplateName{
				Zh: "技术方案评审报告",
				En: "Technical design review",
			},
			OutputFormats: []string{"docx", "md", "pdf"},
			Sections: []ReportTemplateSection{
				{
					Key: "context", Title: "背景与评审目标",
					Prompt:           "说明被评审方案的业务背景、评审触发原因与本报告的评审目标；不评价方案本身。",
					RequiresEvidence: false, MaxWords: 300,
				},
				{
					Key: "criteria", Title: "评审依据与标准",
					Prompt:           "列出本次评审采用的依据（规范、既有架构约定、性能与安全基线），逐条写明来源与适用范围。",
					RequiresEvidence: true, MaxWords: 400,
				},
				{
					Key: "proposal", Title: "方案要点归纳",
					Prompt:           "客观归纳方案的关键设计选择（数据流、接口、存储、部署），不加入评审意见。",
					RequiresEvidence: true, MaxWords: 800,
					DependsOn: []string{"context"},
				},
				{
					Key: "assessment", Title: "逐项评估结论",
					Prompt:           "按评审依据逐条评估方案，判定「满足 / 部分满足 / 不满足 / 资料不足」，每条必须引用方案依据与评审标准。",
					RequiresEvidence: true, MaxWords: 1200,
					DependsOn: []string{"criteria", "proposal"},
				},
				{
					Key: "risks", Title: "风险与待确认项",
					Prompt:           "列出评估中发现的风险与无法从资料判定的待确认项，写明影响面与需要谁来确认；不得把「资料不足」写成「不满足」。",
					RequiresEvidence: true, MaxWords: 800,
					DependsOn: []string{"assessment"},
				},
				{
					Key: "recommendation", Title: "结论与建议",
					Prompt:           "给出评审结论（通过 / 有条件通过 / 不通过 / 信息不足）并说明条件或补充材料要求，与前文评估逐条对应。",
					RequiresEvidence: true, MaxWords: 500,
					DependsOn: []string{"risks"},
				},
			},
			Limits: ReportTemplateLimits{
				MaxSections: 10, SliceMaxSeconds: 480, TotalMaxSeconds: 3600,
				MaxSlices: 8, MaxTokensPerSection: 1600,
			},
			Retrieval: ReportTemplateRetrieval{
				TopK: 8, ThresholdPolicy: "dataset_calibrated", MinEvidence: 1,
			},
			EvidencePolicy: ReportTemplateEvidencePolicy{
				ConflictHandling:  reportConflictHandlingP0,
				OutdatedEvidence:  reportOutdatedEvidenceP0,
				UnsupportedClaims: reportUnsupportedClaimsP0,
			},
			OutlineConfirmation:     model.ReportOutlineRequired,
			AutoConfirmAllowedRoles: []string{},
		},
	},
	{
		// Postmortem: `timeline` is the shared spine — impact, root cause and the
		// response all hang off it, so the four sections cannot contradict each
		// other's sequence of events. `actions` depends on both the causes and the
		// response, which is what stops an improvement list from being written
		// before anyone established why it happened.
		Key: reportPostmortemTemplateKey,
		Payload: ReportTemplatePayload{
			Name: ReportTemplateName{
				Zh: "事故复盘报告",
				En: "Incident postmortem",
			},
			OutputFormats: []string{"docx", "md", "pdf"},
			Sections: []ReportTemplateSection{
				{
					Key: "summary", Title: "事件概要",
					Prompt:           "用不超过五句话说明发生了什么、影响谁、持续多久、当前状态；细节留给后续章节。",
					RequiresEvidence: false, MaxWords: 200,
				},
				{
					Key: "timeline", Title: "事件时间线",
					Prompt:           "按时间顺序列出关键节点（发现、升级、定位、处置、恢复），每行给出时间与事实来源，不做归因。",
					RequiresEvidence: true, MaxWords: 800,
					DependsOn: []string{"summary"},
				},
				{
					Key: "impact", Title: "影响范围与损失",
					Prompt:           "量化影响：受影响的对象、范围、时长与可核对的损失指标；无法量化的影响写明估算依据。",
					RequiresEvidence: true, MaxWords: 600,
					DependsOn: []string{"timeline"},
				},
				{
					Key: "root_cause", Title: "根因分析",
					Prompt:           "区分直接原因、根本原因与促成因素，每条给出证据；证据不足以定论时明确写「待确认」，不猜测。",
					RequiresEvidence: true, MaxWords: 900,
					DependsOn: []string{"impact"},
				},
				{
					Key: "response", Title: "处置与恢复过程",
					Prompt:           "说明处置动作、有效性判断与恢复验证方式，与时间线节点一一对应。",
					RequiresEvidence: true, MaxWords: 600,
					DependsOn: []string{"timeline"},
				},
				{
					Key: "actions", Title: "改进项与责任人",
					Prompt:           "针对每条根因与促成因素给出可验证的改进项、责任角色与完成判据，并与根因逐条对应。",
					RequiresEvidence: true, MaxWords: 600,
					DependsOn: []string{"root_cause", "response"},
				},
			},
			Limits: ReportTemplateLimits{
				MaxSections: 10, SliceMaxSeconds: 480, TotalMaxSeconds: 3600,
				MaxSlices: 8, MaxTokensPerSection: 1600,
			},
			Retrieval: ReportTemplateRetrieval{
				TopK: 8, ThresholdPolicy: "dataset_calibrated", MinEvidence: 1,
			},
			EvidencePolicy: ReportTemplateEvidencePolicy{
				ConflictHandling:  reportConflictHandlingP0,
				OutdatedEvidence:  reportOutdatedEvidenceP0,
				UnsupportedClaims: reportUnsupportedClaimsP0,
			},
			OutlineConfirmation:     model.ReportOutlineRequired,
			AutoConfirmAllowedRoles: []string{},
		},
	},
}

// BuiltinReportTemplates returns the shipped catalogue. The payload is deep
// copied so a caller cannot mutate the package-level baseline.
func BuiltinReportTemplates() []BuiltinReportTemplate {
	out := make([]BuiltinReportTemplate, 0, len(builtinReportTemplates))
	for _, item := range builtinReportTemplates {
		copied := item
		copied.Payload.OutputFormats = append([]string(nil), item.Payload.OutputFormats...)
		copied.Payload.AutoConfirmAllowedRoles = append([]string(nil), item.Payload.AutoConfirmAllowedRoles...)
		sections := make([]ReportTemplateSection, 0, len(item.Payload.Sections))
		for _, section := range item.Payload.Sections {
			cloned := section
			cloned.DependsOn = append([]string(nil), section.DependsOn...)
			sections = append(sections, cloned)
		}
		copied.Payload.Sections = sections
		out = append(out, copied)
	}
	return out
}

// EnsureBuiltinReportTemplates seeds the shipped report templates for one tenant.
//
// Idempotency is by payload hash: an unchanged template creates nothing, a
// changed one appends a new immutable version, and an existing version is never
// rewritten (a report pinned to version 3 keeps reading version 3).
//
// The version bump is for the *platform baseline* only. A workspace row is a
// fork: re-running the seed there must not keep chasing a customised row with new
// versions on every boot, so a workspace only gets a row when it has none.
func (s *Service) EnsureBuiltinReportTemplates(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	existing, err := s.Store.ListReportTemplates(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	latest := make(map[string]model.ReportTemplate, len(existing))
	for _, row := range existing {
		current, ok := latest[row.TemplateKey]
		if !ok || row.Version > current.Version {
			latest[row.TemplateKey] = row
		}
	}
	created := 0
	now := time.Now().UTC()
	for _, item := range builtinReportTemplates {
		if err := validateReportTemplatePayload(&item.Payload); err != nil {
			// A shipped template that fails its own contract is a build-time bug;
			// refusing here keeps it from silently seeding a broken baseline.
			return created, fmt.Errorf("builtin report template %q is invalid: %w", item.Key, err)
		}
		if current, ok := latest[item.Key]; ok {
			if tenantID != model.PlatformTenantID {
				// A workspace fork is authoritative for itself: leaving it alone is
				// what makes customisation survive a restart.
				continue
			}
			stored, err := reportTemplatePayloadOf(&current)
			if err != nil {
				return created, fmt.Errorf("stored report template %q is unreadable: %w", item.Key, err)
			}
			if hashReportTemplatePayload(stored) == hashReportTemplatePayload(&item.Payload) {
				continue
			}
		}
		row, err := reportTemplateRow(tenantID, item.Key, 1, &item.Payload, now)
		if err != nil {
			return created, err
		}
		if current, ok := latest[item.Key]; ok {
			row.Version = current.Version + 1
		}
		if err := s.Store.CreateReportTemplate(ctx, row); err != nil {
			// A concurrent bootstrap already inserted this version: treat it as a
			// no-op instead of failing startup.
			race, getErr := s.Store.GetReportTemplate(ctx, tenantID, item.Key, row.Version)
			if getErr == nil && race != nil {
				continue
			}
			return created, err
		}
		created++
	}
	return created, nil
}

// reportTemplateRow projects one typed payload onto the stored columns. The
// columns are denormalised views of the payload so list queries never have to
// re-parse four JSON blobs; `SectionsJSON` holds the sections array only, which
// keeps every column's meaning equal to its name.
func reportTemplateRow(tenantID, templateKey string, version int64, payload *ReportTemplatePayload, now time.Time) (*model.ReportTemplate, error) {
	nameJSON, err := json.Marshal(payload.Name)
	if err != nil {
		return nil, err
	}
	formatsJSON, err := json.Marshal(payload.OutputFormats)
	if err != nil {
		return nil, err
	}
	sectionsJSON, err := json.Marshal(payload.Sections)
	if err != nil {
		return nil, err
	}
	limitsJSON, err := json.Marshal(payload.Limits)
	if err != nil {
		return nil, err
	}
	retrievalJSON, err := json.Marshal(payload.Retrieval)
	if err != nil {
		return nil, err
	}
	evidenceJSON, err := json.Marshal(payload.EvidencePolicy)
	if err != nil {
		return nil, err
	}
	rolesJSON, err := json.Marshal(payload.AutoConfirmAllowedRoles)
	if err != nil {
		return nil, err
	}
	return &model.ReportTemplate{
		ID:                      id.New(),
		TenantID:                tenantID,
		TemplateKey:             templateKey,
		Version:                 version,
		NameJSON:                string(nameJSON),
		OutputFormatsJSON:       string(formatsJSON),
		SectionsJSON:            string(sectionsJSON),
		LimitsJSON:              string(limitsJSON),
		RetrievalJSON:           string(retrievalJSON),
		EvidencePolicyJSON:      string(evidenceJSON),
		OutlineConfirmation:     payload.OutlineConfirmation,
		AutoConfirmAllowedRoles: string(rolesJSON),
		Status:                  model.ReportTemplateStatusActive,
		CreatedBy:               builtinReportBootstrapActor,
		CreatedAt:               now,
	}, nil
}

// BootstrapReportTemplates seeds the platform baseline at startup. Failures are
// logged and audited but never block startup — a broken template catalogue must
// not keep the API from serving chat/search.
func (s *Service) BootstrapReportTemplates(ctx context.Context) {
	created, err := s.EnsureBuiltinReportTemplates(ctx, model.PlatformTenantID)
	entry := &model.AuditLog{
		ID: id.New(), TenantID: model.PlatformTenantID, UserID: builtinReportBootstrapActor,
		Action: "report_template.bootstrap", Resource: "report-template",
		Result: "SUCCESS", DetailJSON: fmt.Sprintf(`{"created":%d}`, created), At: time.Now().UTC(),
	}
	if err != nil {
		entry.Result = "FAILED"
		entry.DetailJSON = fmt.Sprintf(`{"created":%d,"error":"bootstrap failed"}`, created)
		logger.Warn("report template bootstrap failed", "error", err)
	}
	if auditErr := s.RecordAudit(ctx, entry); auditErr != nil {
		logger.Warn("report template bootstrap audit failed", "error", auditErr)
	}
}

// ReportTemplateView is the decoded, display-ready form of one template version.
// Handlers render this instead of re-parsing the four JSON columns themselves,
// so "the UI reads the same contract the worker reads" stays true.
type ReportTemplateView struct {
	TemplateID string `json:"template_id"`
	Key        string `json:"template_key"`
	Version    int64  `json:"version"`
	// TenantID is the owner of this row: the platform tenant for the baseline, or
	// the workspace that forked it.
	TenantID string                `json:"tenant_id"`
	Payload  ReportTemplatePayload `json:"payload"`
	Status   string                `json:"status"`
	Retired  bool                  `json:"retired"`
}

// resolveReportTemplate picks the template version a new report must pin.
//
// Resolution order is workspace row first, then the platform baseline: a
// workspace clone must win over the baseline, or a customisation could never take
// effect. A retired row is not offered for new reports, which is what 44101
// reports to the caller.
func (s *Service) resolveReportTemplate(ctx context.Context, tenantID, templateKey string, version int64) (*model.ReportTemplate, error) {
	key := strings.TrimSpace(templateKey)
	if key == "" {
		return nil, errReportTemplateNotFound()
	}
	if tenantID != "" {
		row, err := s.Store.GetReportTemplate(ctx, tenantID, key, version)
		if err != nil {
			return nil, err
		}
		if row != nil {
			if version <= 0 && row.RetiredAt != nil {
				return nil, errReportTemplateNotFound()
			}
			return row, nil
		}
	}
	row, err := s.Store.GetReportTemplate(ctx, model.PlatformTenantID, key, version)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errReportTemplateNotFound()
	}
	if version <= 0 && (row.RetiredAt != nil || row.Status == model.ReportTemplateStatusRetired) {
		return nil, errReportTemplateNotFound()
	}
	return row, nil
}

// reportTemplatePayloadOf reassembles the stored columns into the typed payload.
// The columns are denormalised views written by the same projection, so reading
// them back is not a second source of truth — it is the same fact in its stored
// shape. A row whose sections do not parse is an error, never an empty outline.
func reportTemplatePayloadOf(row *model.ReportTemplate) (*ReportTemplatePayload, error) {
	if row == nil {
		return nil, fmt.Errorf("report template is nil")
	}
	payload := &ReportTemplatePayload{
		OutlineConfirmation:     row.OutlineConfirmation,
		AutoConfirmAllowedRoles: []string{},
	}
	if raw := strings.TrimSpace(row.NameJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.Name); err != nil {
			return nil, fmt.Errorf("name_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.OutputFormatsJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.OutputFormats); err != nil {
			return nil, fmt.Errorf("output_formats_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.SectionsJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.Sections); err != nil {
			return nil, fmt.Errorf("sections_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.LimitsJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.Limits); err != nil {
			return nil, fmt.Errorf("limits_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.RetrievalJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.Retrieval); err != nil {
			return nil, fmt.Errorf("retrieval_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.EvidencePolicyJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.EvidencePolicy); err != nil {
			return nil, fmt.Errorf("evidence_policy_json: %w", err)
		}
	}
	if raw := strings.TrimSpace(row.AutoConfirmAllowedRoles); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload.AutoConfirmAllowedRoles); err != nil {
			return nil, fmt.Errorf("auto_confirm_allowed_roles: %w", err)
		}
	}
	return payload, nil
}

// ListReportTemplates returns one row per template key: the workspace's newest
// version when it has forked the key, otherwise the platform baseline. A retired
// baseline is still listed (it explains historical reports) but flagged, so the
// UI never offers it for a new run.
func (s *Service) ListReportTemplates(ctx context.Context, tenantID string) ([]ReportTemplateView, error) {
	tenantRows, err := s.Store.ListReportTemplates(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	platformRows, err := s.Store.ListReportTemplates(ctx, model.PlatformTenantID)
	if err != nil {
		return nil, err
	}
	newest := make(map[string]model.ReportTemplate, len(platformRows)+len(tenantRows))
	newestOf := func(rows []model.ReportTemplate) {
		for _, row := range rows {
			current, ok := newest[row.TemplateKey]
			if !ok || row.Version > current.Version {
				newest[row.TemplateKey] = row
			}
		}
	}
	newestOf(platformRows)
	// The workspace overlay is applied key-by-key, not version-by-version: a
	// tenant fork starts its own version sequence at 1, so comparing versions
	// across tenants would always let the platform row win and the fork could
	// never take effect.
	for _, row := range tenantRows {
		newest[row.TemplateKey] = row
	}
	keys := make([]string, 0, len(newest))
	for key := range newest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	views := make([]ReportTemplateView, 0, len(keys))
	for _, key := range keys {
		row := newest[key]
		payload, err := reportTemplatePayloadOf(&row)
		if err != nil {
			return nil, err
		}
		views = append(views, ReportTemplateView{
			TemplateID: row.ID, Key: row.TemplateKey, Version: row.Version,
			TenantID: row.TenantID, Payload: *payload, Status: row.Status,
			Retired: row.RetiredAt != nil || row.Status == model.ReportTemplateStatusRetired,
		})
	}
	return views, nil
}

package service

// The assurance request path (doc/301 §5.5, §6, §7).
//
// This file owns the four decisions a *request* may take: is this revision
// assurable, which policy is frozen for this run, does the tenant's allowance
// cover one call, and what does the caller get to read. Everything that decides a
// verdict lives in report_assurance_exec.go — the split matters because the
// request path must never be able to write `release_eligibility`. A handler that
// could compute a tier could also be asked to.

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	// reportAssuranceMethodVersion is written on every evaluation row. It is the
	// version of the *method*, not of the code: a prompt change that alters what
	// `supported` means must bump it, or two incomparable runs would share a name
	// (doc/301 §5.2 / D5).
	reportAssuranceMethodVersion = "claim-eval.v2"
	// reportAssuranceMinCallTokens is the smallest useful call: one section, one
	// batch. Both the per-run budget and the monthly allowance are compared against
	// it before a run is allowed to exist, so a tenant can be told "not enough left
	// for even one call" instead of watching a run fail on its first claim
	// (doc/301 §6.5, second review P1-2 ①).
	reportAssuranceMinCallTokens = 9000
	// reportAssuranceDefaultBudgetTokens mirrors the settings default. It is a
	// fallback for a snapshot that predates the `report` group, not a second
	// source of truth (§7.6).
	reportAssuranceDefaultBudgetTokens = 80000
	// Review projection caps (doc/301 §7.1). They are page sizes, never refusals:
	// exceeding them returns a `next_cursor`, because a workbench that cannot page
	// is a workbench that silently shows the first 300 claims of an 800-claim
	// revision.
	reportAssuranceReviewSectionLimit = 10
	reportAssuranceReviewClaimLimit   = 300
	reportAssuranceReviewFindingLimit = 200
)

// ReportAssuranceSettings is the `report.*` slice of the system settings snapshot.
//
// It is a struct rather than six reads at the call site so the "was it ever set"
// question is answered once: `AssuranceMonthlyTokens == 0` means unlimited *and*
// is distinguishable from "the key is missing", because a missing key is filled
// from the default snapshot before this struct exists.
type ReportAssuranceSettings struct {
	Enabled             bool
	Gate                string
	Model               string
	ReviewModel         string
	BudgetTokens        int64
	MonthlyTokens       int64
	CandidateScanTopK   int
	LocatorHintsEnabled bool
	// Source records which layer each field came from, for `policy_json`
	// (doc/301 §7.6 rule 1: one resolver, and it says where it won).
	Source map[string]string
}

// reportAssuranceSettingsOf reads the live settings snapshot. A tenant with no
// stored revision gets the compiled defaults, which is exactly what the settings
// endpoint would show it — the two must not disagree, or a tenant would be reading
// one value and being charged by another.
func (s *Service) reportAssuranceSettingsOf(ctx context.Context) (ReportAssuranceSettings, error) {
	revision, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return ReportAssuranceSettings{}, err
	}
	var snapshot map[string]interface{}
	if revision == nil {
		snapshot = defaultSystemSettingsSnapshot()
	} else if err := json.Unmarshal([]byte(revision.SnapshotJSON), &snapshot); err != nil {
		return ReportAssuranceSettings{}, httperr.Internal("invalid system settings snapshot")
	}
	snapshot = withDefaultSettingGroups(snapshot)
	return reportAssuranceSettingsFromSnapshot(snapshot), nil
}

// reportAssuranceSettingsFromSnapshot is the pure half, so a test can state the
// mapping without a database.
func reportAssuranceSettingsFromSnapshot(snapshot map[string]interface{}) ReportAssuranceSettings {
	group := settingGroup(snapshot, "report")
	settings := ReportAssuranceSettings{
		Gate:                model.ReportAssurancePolicyAdvisory,
		BudgetTokens:        reportAssuranceDefaultBudgetTokens,
		LocatorHintsEnabled: true,
		Source: map[string]string{
			"enabled": "platform", "gate": "platform", "budget_tokens": "platform",
			"monthly_tokens": "platform", "scan_top_k": "platform",
		},
	}
	if group == nil {
		return settings
	}
	if value, ok := group["assurance_enabled"].(bool); ok {
		settings.Enabled = value
		settings.Source["enabled"] = "tenant"
	}
	if gate := strings.TrimSpace(settingStringOf(group, "assurance_gate")); gate != "" {
		settings.Gate = gate
		settings.Source["gate"] = "tenant"
	}
	if value, ok := settingNumberOf(group, "assurance_budget_tokens"); ok && value > 0 {
		settings.BudgetTokens = int64(value)
		settings.Source["budget_tokens"] = "tenant"
	}
	if value, ok := settingNumberOf(group, "assurance_monthly_tokens"); ok && value >= 0 {
		// 0 is a real answer here ("unlimited, still metered"), not "unset": the
		// generic `settingNumber` treats <= 0 as absent, which would silently turn
		// an explicit 0 into the platform fallback (doc/301 §6.5).
		settings.MonthlyTokens = int64(value)
		settings.Source["monthly_tokens"] = "tenant"
	}
	if value, ok := settingNumberOf(group, "candidate_scan_top_k"); ok && value >= 0 {
		settings.CandidateScanTopK = int(value)
		settings.Source["scan_top_k"] = "tenant"
	}
	if value, ok := group["locator_hints_enabled"].(bool); ok {
		settings.LocatorHintsEnabled = value
	}
	settings.Model = strings.TrimSpace(settingStringOf(group, "assurance_model"))
	settings.ReviewModel = strings.TrimSpace(settingStringOf(group, "assurance_review_model"))
	return settings
}

// settingStringOf / settingNumberOf are the tolerant readers for a group the
// caller already resolved. They exist because `settingString`/`settingNumber`
// take (snapshot, group, field) and would re-resolve the group for every field.
func settingStringOf(group map[string]interface{}, field string) string {
	if text, ok := group[field].(string); ok {
		return text
	}
	return ""
}

func settingNumberOf(group map[string]interface{}, field string) (float64, bool) {
	if group == nil {
		return 0, false
	}
	number, err := toFloat64(group[field])
	if err != nil {
		return 0, false
	}
	return number, true
}

// AssurancePolicyResolution is the frozen policy of one run. `Source` is per
// field: "the candidate range came from the template, the gate from the tenant"
// is a sentence an auditor can check, while a single `source: "merged"` is not.
type AssurancePolicyResolution struct {
	Gate         string            `json:"gate"`
	ScanTopK     int               `json:"scan_top_k"`
	BudgetTokens int64             `json:"budget_tokens"`
	Source       map[string]string `json:"policy_source"`
	Model        string            `json:"assurance_model,omitempty"`
	ReviewModel  string            `json:"assurance_review_model,omitempty"`
	// ReviewTransport is `provider_direct` or `engine_chat`: it records *how* the
	// review question will be asked, because on this engine the answer does not
	// say which model produced it (doc/304 E125/E127). A run whose transport is
	// `engine_chat` reports `independence=unknown` by construction.
	ReviewTransport string `json:"assurance_review_transport,omitempty"`
	// ReviewTransportReason says *why* that route was chosen, in the operator's
	// words: "the reference names no instance", "model X is not registered on
	// instance Y", … Without it a run that silently fell back to the engine looks
	// exactly like a run that was never meant to go direct, and the operator's only
	// recourse is to guess which of the four gates refused (doc/304 E129).
	ReviewTransportReason string `json:"assurance_review_transport_reason,omitempty"`
}

// resolveAssurancePolicy is the ONLY policy resolver (doc/301 §7.6 rule 1).
//
// Precedence, strongest first, is a fact about *when* each layer may change:
//
//  1. template `scan_top_k` (non-zero) — it describes the run conditions of this
//     revision and is part of the template version the revision pinned;
//  2. tenant settings — the only layer an administrator can change at runtime;
//  3. platform defaults — a compiled fallback, never a runtime knob.
//
// The template's `assurance_policy.gate` is deliberately NOT read: it is a v0.7.1
// field (T18) and honouring it now would let a template announce `required` in a
// version where the validator refuses that value (doc/301 §7.6 rule 3).
func resolveAssurancePolicy(settings ReportAssuranceSettings, templateScanTopK int) AssurancePolicyResolution {
	resolved := AssurancePolicyResolution{
		Gate:         settings.Gate,
		ScanTopK:     settings.CandidateScanTopK,
		BudgetTokens: settings.BudgetTokens,
		Model:        settings.Model,
		ReviewModel:  settings.ReviewModel,
		Source: map[string]string{
			"gate":          settings.Source["gate"],
			"scan_top_k":    settings.Source["scan_top_k"],
			"budget_tokens": settings.Source["budget_tokens"],
		},
	}
	if resolved.Gate == "" {
		resolved.Gate = model.ReportAssurancePolicyAdvisory
		resolved.Source["gate"] = "platform"
	}
	if templateScanTopK > 0 {
		resolved.ScanTopK = templateScanTopK
		resolved.Source["scan_top_k"] = "template"
	}
	if resolved.Source["scan_top_k"] == "" {
		resolved.Source["scan_top_k"] = "platform"
	}
	if resolved.Source["budget_tokens"] == "" {
		resolved.Source["budget_tokens"] = "platform"
	}
	return resolved
}

// reportCandidateScanTopK resolves the candidate width for a *generation* run and
// says where it came from (doc/301 §5.3.1):
//
//	platform default 0 ──▶ report.candidate_scan_top_k ──▶ template retrieval.scan_top_k
//
// The template wins when it is explicitly positive: "does this report want
// cross-document contrast" is the template's business declaration, and a tenant
// setting is a default the template may overrule. The result is frozen into the
// revision's `execution_snapshot_json`, so a review never re-resolves it.
func (s *Service) reportCandidateScanTopK(ctx context.Context, templateScanTopK int) (int, string) {
	settings, _ := s.reportAssuranceSettingsOf(ctx)
	resolved := resolveAssurancePolicy(settings, templateScanTopK)
	width := resolved.ScanTopK
	if width < 0 {
		width = 0
	}
	if width > reportAssuranceMaxScanTopK {
		// Clamped, not rejected: the generation path has already frozen an outline, and
		// failing a published path over an over-wide setting would be a worse answer
		// than the documented ceiling.
		width = reportAssuranceMaxScanTopK
	}
	return width, resolved.Source["scan_top_k"]
}

// reportAssuranceMaxScanTopK is the §5.3.1 range ceiling (0–20).
const reportAssuranceMaxScanTopK = 20

// ReportAssuranceTriggerInput is the trigger body. Only `mode` is a caller
// decision: policy, budget and candidate range are resolved, never supplied.
type ReportAssuranceTriggerInput struct {
	Mode string `json:"mode"`
}

// ReportAssuranceRunView is the run as the API returns it.
//
// `RequestedPolicy` and `ReleaseEligibility` are both present, and that is the
// point: a `queued` run answers "what was asked" (`advisory`) and
// `release_eligibility=pending`, never a tier (doc/301 §5.5 / second review P0-5).
type ReportAssuranceRunView struct {
	ID              string `json:"id"`
	ReportID        string `json:"report_id"`
	RevisionID      string `json:"revision_id"`
	Revision        int64  `json:"revision"`
	Status          string `json:"status"`
	Mode            string `json:"mode"`
	RequestedPolicy string `json:"requested_policy"`
	// ReleaseEligibility is `pending` until the run ends; after that it is the
	// single verdict of `reportAssuranceEligibility`.
	ReleaseEligibility string `json:"release_eligibility"`
	MethodVersion      string `json:"method_version"`
	Independence       string `json:"independence"`
	ReviewModel        string `json:"review_model,omitempty"`
	GenerationModel    string `json:"generation_model,omitempty"`

	TotalClaims   int `json:"total_claims"`
	Evaluated     int `json:"evaluated"`
	NotRequired   int `json:"not_required"`
	NotEvaluated  int `json:"not_evaluated"`
	Abstained     int `json:"abstained"`
	StructurePass int `json:"structure_pass"`
	StructureFail int `json:"structure_fail"`

	BudgetTokens       int64  `json:"budget_tokens"`
	MinCallTokens      int64  `json:"min_call_tokens"`
	UsedTokens         int64  `json:"used_tokens"`
	BudgetExhausted    bool   `json:"budget_exhausted"`
	QuotaPeriod        string `json:"quota_period,omitempty"`
	QuotaSettledTokens int64  `json:"quota_settled_tokens"`

	Policy        AssurancePolicyResolution `json:"policy"`
	Metrics       map[string]interface{}    `json:"metrics,omitempty"`
	FailureCode   string                    `json:"failure_code,omitempty"`
	FailureReason string                    `json:"failure_reason,omitempty"`

	TriggeredBy string     `json:"triggered_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	// Replayed marks a response answered from the idempotency key rather than a
	// fresh run. A client that retries must be able to tell "your click created
	// this" from "someone's click created this earlier".
	Replayed bool `json:"replayed"`
}

// assuranceRunViewOf renders a run row. JSON columns are decoded best-effort: a
// malformed `metrics_json` must not turn a readable run into a 500, because the
// counters are stored separately and are still worth showing.
func assuranceRunViewOf(run *model.ReportAssuranceRun) *ReportAssuranceRunView {
	view := &ReportAssuranceRunView{
		ID: run.ID, ReportID: run.ReportID, RevisionID: run.RevisionID, Revision: run.Revision,
		Status: run.Status, Mode: run.Mode, RequestedPolicy: run.RequestedPolicy,
		ReleaseEligibility: run.ReleaseEligibility, MethodVersion: run.MethodVersion,
		Independence: run.Independence, ReviewModel: run.ReviewModelID,
		GenerationModel: run.GenerationModelID,
		Evaluated:       run.CountEvaluated, NotRequired: run.CountNotRequired,
		NotEvaluated: run.CountNotEvaluated, Abstained: run.CountAbstained,
		StructurePass: run.StructurePass, StructureFail: run.StructureFail,
		BudgetTokens: run.BudgetTokens, MinCallTokens: reportAssuranceMinCallTokens,
		UsedTokens: run.UsedTokens, BudgetExhausted: run.BudgetExhausted,
		QuotaPeriod: run.QuotaPeriod, QuotaSettledTokens: run.QuotaSettledTokens,
		FailureCode: run.FailureCode, FailureReason: run.FailureReason,
		TriggeredBy: run.TriggeredBy, CreatedAt: run.CreatedAt,
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
	}
	view.TotalClaims = run.CountEvaluated + run.CountAbstained +
		run.CountNotEvaluated + run.CountNotRequired
	_ = json.Unmarshal([]byte(run.PolicyJSON), &view.Policy)
	if view.Policy.Source == nil {
		view.Policy.Source = map[string]string{}
	}
	if run.MetricsJSON != "" {
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(run.MetricsJSON), &metrics); err == nil {
			view.Metrics = metrics
		}
	}
	return view
}

// TriggerReportAssurance starts one run (doc/301 §7.1 POST).
//
// The order of the checks is the contract:
//
//	authorize → assurable → settings enabled → idempotent replay → active run →
//	budget → allowance → insert → enqueue
//
// `assurable` before `settings` because "this revision is not reviewable" is true
// regardless of configuration; replay before the active-run mutex because a retry
// of the *same* click must get its own run back, not a 44118; budget before
// allowance because a per-run budget of 1 token is a configuration error the caller
// can fix themselves, while an exhausted monthly allowance is not.
func (s *Service) TriggerReportAssurance(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
	clientKey string, input ReportAssuranceTriggerInput,
) (*ReportAssuranceRunView, error) {
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = model.ReportAssuranceModeFull
	}
	if !reportAssuranceModeKnown(mode) {
		return nil, httperr.New(400, codeReportOutlineInvalid,
			"mode must be one of "+strings.Join([]string{
				model.ReportAssuranceModeSemantic, model.ReportAssuranceModeCitationAudit,
				model.ReportAssuranceModeFull,
			}, ", "))
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	// Triggering is the owner's action (doc/301 §7.1): a grantee reads, the owner
	// spends the tenant's allowance. 44109 rather than 403, so the endpoint is not
	// an oracle for "does this report id exist".
	if report.OwnerUserID != actorID {
		return nil, errReportAssuranceOwnerOnly()
	}
	if revisionNo <= 0 {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, report.ID, revisionNo)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	// Only a published revision can be audited. Auditing a draft would measure a
	// text nobody may read, and `superseded`/`cancelled` rows are history.
	if revision.Status != model.ReportRevisionPublished {
		return nil, errReportRevisionNotAssurable(
			"only a published revision can be reviewed; this one is " + revision.Status)
	}

	settings, err := s.reportAssuranceSettingsOf(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, errReportRevisionNotAssurable(
			"assurance is disabled for this tenant; an administrator must enable report.assurance_enabled first")
	}
	if settings.Gate == model.ReportAssurancePolicyOff {
		return nil, errReportRevisionNotAssurable(
			"report.assurance_gate is off for this tenant; set it to advisory to allow review runs")
	}
	policy := resolveAssurancePolicy(settings, reportFrozenScanTopK(ctx, s, revision))

	if strings.TrimSpace(clientKey) == "" {
		return nil, errReportIdempotencyKeyRequired("Idempotency-Key header is required to start a review run")
	}
	key, err := reportAssuranceIdempotencyKey(report.ID, revision.Revision, actorID, clientKey)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.FindAssuranceRunByIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		view := assuranceRunViewOf(existing)
		view.Replayed = true
		return view, nil
	}
	active, err := s.Store.FindActiveAssuranceRun(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return nil, errReportAssuranceRunConflict()
	}
	if policy.BudgetTokens < reportAssuranceMinCallTokens {
		// A budget below one call can never produce a verdict. Refusing here beats
		// accepting a run that is guaranteed to end `degraded` with zero coverage.
		return nil, errReportAssuranceBudgetExhausted(policy.BudgetTokens)
	}
	quota, err := s.reportAssuranceQuotaState(ctx, tenantID, settings.MonthlyTokens)
	if err != nil {
		return nil, err
	}
	if quota.Limited && quota.Remaining() < reportAssuranceMinCallTokens {
		return nil, errReportAssuranceQuotaExhausted(
			quota.Period, quota.Limit, quota.Used, quota.Pending, reportAssuranceMinCallTokens)
	}

	generation := reportGenerationModelOf(revision)
	review, independence := reportReviewModelOf(policy, generation)
	// Freeze the route with the policy (doc/304 E127). Resolving here rather than
	// only in the worker means the *audit row and the API view* carry the route
	// before any money is spent, and a run that cannot address its reviewer
	// directly says so at creation instead of after the fact.
	if review.ModelID == "" {
		// No reviewer configured: no model is asked, so there is no route to freeze —
		// but "no route" must be *stated*, or the audit row is silent about the one
		// question it exists to answer (doc/304 E129).
		policy.ReviewTransport = assuranceTransportNone
		policy.ReviewTransportReason = "no review model configured: the mechanical layers decide alone"
	} else {
		route := s.resolveAssuranceReviewRoute(ctx, tenantID, review)
		// `reportReviewModelOf` compared two strings; the engine route cannot show
		// that they describe the answerer (E125), so the claim weakens to `unknown`
		// here — the one place allowed to do it.
		independence = assuranceIndependenceFor(independence, route)
		policy.ReviewTransport = route.Transport
		// The reason travels with the route: a receipt that says `engine_chat` and
		// nothing else asks the operator to re-derive which gate refused.
		policy.ReviewTransportReason = route.Reason
	}
	now := time.Now().UTC()
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	run := &model.ReportAssuranceRun{
		ID: id.New(), TenantID: tenantID, ReportID: report.ID, RevisionID: revision.ID,
		Revision: revision.Revision, Status: model.ReportAssuranceStatusQueued, Mode: mode,
		RequestedPolicy:    policy.Gate,
		ReleaseEligibility: model.ReportAssuranceEligibilityPending,
		PolicyJSON:         string(policyJSON), MethodVersion: reportAssuranceMethodVersion,
		GenerationModelID: generation.ModelID, ReviewModelID: review.ModelID,
		ReviewProvider: review.Provider, ReviewDeployment: review.Instance,
		Independence: independence, BudgetTokens: policy.BudgetTokens,
		IdempotencyKey: key, TriggeredBy: actorID, CreatedAt: now,
	}
	if err := s.Store.CreateAssuranceRun(ctx, run); err != nil {
		// The unique `(tenant_id, idempotency_key)` index is the real race winner.
		// The loser resolves the row the winner wrote instead of surfacing a DB
		// error, so two identical clicks both answer with one run id.
		if replay, findErr := s.Store.FindAssuranceRunByIdempotencyKey(ctx, tenantID, key); findErr == nil && replay != nil {
			view := assuranceRunViewOf(replay)
			view.Replayed = true
			return view, nil
		}
		return nil, err
	}
	if _, err := s.Store.EnqueueJob(ctx, reportAssuranceJob(run)); err != nil {
		// The run row exists and is `queued`. The reconcile sweep of
		// `report_assurance` jobs re-arms an unarmed run (see
		// `reconcileReportAssuranceRuns`), so a losing insert here is a delay, not
		// a lost run — which is why this is logged rather than rolled back.
		logger.Warn("report assurance enqueue failed", "run_id", run.ID, "tenant_id", tenantID, "error", err)
	}
	// §7.4 `report.assurance_started`. `release_eligibility` is written as `pending`
	// because it is `pending` at this moment — an audit row that recorded the tier the
	// caller *hoped* for would be indistinguishable from one recording a measurement.
	// Argument order is `(actorID, tenantID, …)`: swapped, the row lands on a hash
	// chain keyed by a user id, where neither the tenant's audit view nor its
	// verification can ever see that the run was authorised.
	s.recordReportAudit(ctx, actorID, tenantID, "report.assurance_started", report.ID, map[string]any{
		"run_id": run.ID, "revision": revision.Revision, "mode": mode,
		"requested_policy":    policy.Gate,
		"release_eligibility": model.ReportAssuranceEligibilityPending,
		"method_version":      run.MethodVersion, "model_id": run.ReviewModelID,
		"review_transport": policy.ReviewTransport, "review_transport_reason": policy.ReviewTransportReason,
		"independence":  run.Independence,
		"budget_tokens": policy.BudgetTokens,
	})
	return assuranceRunViewOf(run), nil
}

// reportAssuranceModeKnown closes the mode set. A free-form string would land in
// the `mode` column and be read back by the workbench as an unknown layout.
func reportAssuranceModeKnown(mode string) bool {
	switch mode {
	case model.ReportAssuranceModeSemantic, model.ReportAssuranceModeCitationAudit,
		model.ReportAssuranceModeFull:
		return true
	}
	return false
}

// reportAssuranceIdempotencyKey builds `assurance:<report>:<revision>:<user>:<client>`
// (doc/301 §7.1). The target is *inside* the key because the unique index is
// tenant-wide: without `report`+`revision`, the same client key aimed at another
// report would resolve to this run and silently return the wrong one.
func reportAssuranceIdempotencyKey(reportID string, revision int64, actorID, clientKey string) (string, error) {
	key := "assurance:" + reportID + ":" + strconv.FormatInt(revision, 10) + ":" + actorID + ":" + clientKey
	if len(key) > 128 {
		return "", errReportIdempotencyKeyRequired("Idempotency-Key is too long; keep it under 32 characters")
	}
	return key, nil
}

// reportFrozenScanTopK reads the revision's frozen candidate width.
//
// The snapshot is the only source (§5.3.1): re-reading the template here would let a
// template republished after the report was written change what an already-published
// revision is reviewed against, and raise the tenant setting would retro-apply
// candidates that were never frozen — both are the "published is mutable" defect in
// disguise. A revision with no frozen value (any report generated before this
// release) is `cited_only`, and the UI says so rather than implying full contrast.
func reportFrozenScanTopK(
	ctx context.Context, s *Service, revision *model.ReportRevision,
) int {
	if revision == nil || revision.ExecutionSnapshotJSON == "" {
		return 0
	}
	var snapshot struct {
		Retrieval struct {
			ScanTopK int `json:"scan_top_k"`
		} `json:"retrieval"`
	}
	if err := json.Unmarshal([]byte(revision.ExecutionSnapshotJSON), &snapshot); err != nil {
		return 0
	}
	if snapshot.Retrieval.ScanTopK < 0 {
		return 0
	}
	return snapshot.Retrieval.ScanTopK
}

// reportTemplateScanTopK resolves the template layer of the scan range. The
// payload is additive (doc/301 §7.6 rule 2): an older template has no
// `scan_top_k`, reads as 0 and inherits the tenant value. Any failure to load the
// template degrades to "inherit", because the candidate range is a scope decision,
// not an authorization one — a run must not be refused for it.
//
// Only the generation path uses this now: a *review* reads the value the generation
// path froze (`reportFrozenScanTopK`), never the live template.
func reportTemplateScanTopK(
	ctx context.Context, s *Service, tenantID string, revision *model.ReportRevision,
) int {
	if revision.TemplateKey == "" {
		return 0
	}
	row, err := s.Store.GetReportTemplate(ctx, tenantID, revision.TemplateKey, revision.TemplateVersion)
	if err != nil || row == nil {
		row, err = s.Store.GetReportTemplate(ctx, tenantID, revision.TemplateKey, 0)
		if err != nil || row == nil {
			return 0
		}
	}
	payload, err := reportTemplatePayloadOf(row)
	if err != nil || payload == nil {
		return 0
	}
	return payload.Retrieval.ScanTopK
}

// ReportModelRef is the provider/instance/model triple a review call needs.
type ReportModelRef struct {
	ModelID  string `json:"model_id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Instance string `json:"instance"`
}

// reportGenerationModelOf reads who wrote the revision. The value comes from the
// frozen execution snapshot, never from today's default: the reviewer must be
// compared against the model that actually produced the text (doc/301 §5.4).
func reportGenerationModelOf(revision *model.ReportRevision) ReportModelRef {
	if revision == nil || revision.ExecutionSnapshotJSON == "" {
		return ReportModelRef{}
	}
	var snapshot struct {
		Model struct {
			ModelID  string `json:"model_id"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
			Instance string `json:"instance"`
		} `json:"model"`
	}
	if err := json.Unmarshal([]byte(revision.ExecutionSnapshotJSON), &snapshot); err != nil {
		return ReportModelRef{}
	}
	return ReportModelRef{
		ModelID: snapshot.Model.ModelID, Name: snapshot.Model.Name,
		Provider: snapshot.Model.Provider, Instance: snapshot.Model.Instance,
	}
}

// reportReviewModelOf resolves the reviewer and the independence level it can
// actually prove.
//
// The scale is deliberately conservative: an unset review model is `unknown`, not
// `independent_provider`. "We did not configure a second model" must never read as
// "a second model agreed" (doc/301 §5.4).
func reportReviewModelOf(policy AssurancePolicyResolution, generation ReportModelRef) (ReportModelRef, string) {
	reference := strings.TrimSpace(policy.ReviewModel)
	if reference == "" {
		reference = strings.TrimSpace(policy.Model)
	}
	if reference == "" {
		return ReportModelRef{}, model.ReportIndependenceUnknown
	}
	if reference == generation.ModelID && reference != "" {
		return ReportModelRef{ModelID: reference}, model.ReportIndependenceSameModel
	}
	parts := strings.Split(reference, "@")
	ref := ReportModelRef{ModelID: reference}
	if len(parts) > 0 {
		ref.Name = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		ref.Instance = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		ref.Provider = strings.TrimSpace(parts[2])
	}
	switch {
	case ref.Provider != "" && generation.Provider != "" && ref.Provider != generation.Provider:
		return ref, model.ReportIndependenceSeparateProvider
	case ref.Provider != "" && ref.Provider == generation.Provider:
		return ref, model.ReportIndependenceSameFamily
	}
	return ref, model.ReportIndependenceUnknown
}

// ReportAssuranceQuotaState is the read-only allowance precheck (doc/301 §6.5).
//
// It never reserves: a reservation at queue time would pin the money for a run
// that may sit in the queue, and the existing `ReserveQuotaBucketTokens` has no
// partial-reservation semantics, so "reserve min(budget, remaining)" is not
// expressible on this ledger. Reservation therefore happens per model call.
type ReportAssuranceQuotaState struct {
	Limited bool
	Period  string
	Limit   int64
	Used    int64
	Pending int64
}

// Remaining is the spendable balance now. Unused when `Limited` is false.
func (q ReportAssuranceQuotaState) Remaining() int64 {
	remaining := q.Limit - q.Used - q.Pending
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *Service) reportAssuranceQuotaState(
	ctx context.Context, tenantID string, monthlyLimit int64,
) (ReportAssuranceQuotaState, error) {
	state := ReportAssuranceQuotaState{Period: quotaPeriodStart()}
	if monthlyLimit <= 0 {
		// 0 = unlimited *and* unmetered-by-bucket. The run still records
		// `quota_settled_tokens`, so cost stays visible (doc/301 §6.5).
		return state, nil
	}
	state.Limited = true
	state.Limit = monthlyLimit
	bucket, err := s.Store.GetQuotaBucket(
		ctx, tenantID, model.QuotaBucketDimensionAssurance, tenantID, state.Period)
	if err != nil {
		return state, err
	}
	if bucket != nil {
		state.Used = bucket.TokensUsed
		state.Pending = bucket.Pending
	}
	return state, nil
}

// ensureReportAssuranceQuotaBucket materialises the monthly bucket row when a
// limit is configured, and keeps `token_limit` equal to the current setting.
// Raising the limit must not require an operator to touch the ledger by hand; it
// must also never *shrink* below what is already spent, so the write is a
// `token_limit` set only, never a reset of `tokens_used`.
func (s *Service) ensureReportAssuranceQuotaBucket(
	ctx context.Context, tenantID string, monthlyLimit int64,
) (ReportAssuranceQuotaState, error) {
	state := ReportAssuranceQuotaState{Period: quotaPeriodStart()}
	if monthlyLimit <= 0 {
		return state, nil
	}
	state.Limited, state.Limit = true, monthlyLimit
	if err := s.Store.EnsureQuotaBucketTokenLimit(
		ctx, tenantID, model.QuotaBucketDimensionAssurance, tenantID, state.Period, monthlyLimit); err != nil {
		return state, err
	}
	bucket, err := s.Store.GetQuotaBucket(
		ctx, tenantID, model.QuotaBucketDimensionAssurance, tenantID, state.Period)
	if err != nil {
		return state, err
	}
	if bucket == nil {
		return state, nil
	}
	state.Used, state.Pending = bucket.TokensUsed, bucket.Pending
	return state, nil
}

// ListReportAssuranceRuns is the history read (doc/301 §7.1 GET).
func (s *Service) ListReportAssuranceRuns(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64, limit int,
) ([]*ReportAssuranceRunView, error) {
	revision, err := s.assuranceRevisionForRead(ctx, actorID, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.ListAssuranceRuns(ctx, tenantID, reportID, revision.ID, limit)
	if err != nil {
		return nil, err
	}
	views := make([]*ReportAssuranceRunView, 0, len(rows))
	for index := range rows {
		views = append(views, assuranceRunViewOf(&rows[index]))
	}
	return views, nil
}

// GetReportAssuranceRun is the single-run read (doc/301 §7.1 GET :runId). A run
// of another revision is a 404 even when its id is valid: the path names the
// revision, and answering from the id alone would let a caller probe runs across
// revisions of a report they can only partially see.
func (s *Service) GetReportAssuranceRun(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64, runID string,
) (*ReportAssuranceRunView, error) {
	revision, err := s.assuranceRevisionForRead(ctx, actorID, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	run, err := s.Store.GetAssuranceRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.RevisionID != revision.ID || run.ReportID != reportID {
		// A missing run and a run of another revision are the same 404: the path
		// already names report and revision, so the only honest answer for "this
		// run is not here" is the report layer's not-found, which never
		// distinguishes "gone" from "never yours" (44109).
		return nil, errReportNotFound()
	}
	return assuranceRunViewOf(run), nil
}

// assuranceRevisionForRead is the shared read gate: permission, then report
// visibility, then the revision. Copying this into three handlers is how the three
// drift into different answers about the same report.
func (s *Service) assuranceRevisionForRead(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
) (*model.ReportRevision, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	if revisionNo <= 0 {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, report.ID, revisionNo)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	return revision, nil
}

// reportAssuranceJob builds the runtime job. The payload carries only the run id:
// everything else is read from the row, so a stale payload cannot describe a run
// that has since been cancelled.
func reportAssuranceJob(run *model.ReportAssuranceRun) *model.Job {
	payload, _ := json.Marshal(map[string]string{"run_id": run.ID})
	return &model.Job{
		TenantID: run.TenantID, Kind: model.JobKindReportAssurance,
		Key:     "assurance:" + run.ID,
		Payload: string(payload),
	}
}

// ReportFindingDecision is the PATCH body of one finding.
//
// Three fields, and `resolved_revision_id` is required exactly when `status` is
// `resolved`: the service asserts that pairing rather than the binder, because the
// rule is about meaning ("fixed" needs a revision to point at), not about shape.
type ReportFindingDecision struct {
	Status             string `json:"status"`
	Note               string `json:"note"`
	ResolvedRevisionID string `json:"resolved_revision_id"`
}

// UpdateReviewFinding applies one status transition (doc/301 §7.1 PATCH).
//
// Owner-only, and the transition set is a closed table rather than "whatever the
// caller sends": `open → acknowledged|dismissed`, `acknowledged → resolved|dismissed`.
// `resolved` requires a revision id, because "fixed" without a target revision is
// an opinion, and `resolved_revision_id` is what lets the next reader check it.
func (s *Service) UpdateReviewFinding(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
	findingID string, status, note string, resolvedRevisionID string,
) (*ReportReviewFindingView, error) {
	revision, err := s.assuranceRevisionForRead(ctx, actorID, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report == nil || report.OwnerUserID != actorID {
		return nil, errReportAssuranceOwnerOnly()
	}
	if !reportFindingStatusKnown(status) {
		return nil, httperr.New(400, codeReportOutlineInvalid,
			"status must be one of "+strings.Join(model.ReportFindingStatuses(), ", "))
	}
	finding, err := s.Store.GetReviewFinding(ctx, tenantID, findingID)
	if err != nil {
		return nil, err
	}
	if finding == nil || finding.RevisionID != revision.ID || finding.ReportID != reportID {
		return nil, errReportFindingNotFound()
	}
	if !reportFindingTransitionAllowed(finding.Status, status) {
		return nil, httperr.New(409, codeReportInvalidTransition,
			"finding cannot move from "+finding.Status+" to "+status)
	}
	var resolved *string
	if status == model.ReportFindingStatusResolved {
		if strings.TrimSpace(resolvedRevisionID) == "" {
			return nil, httperr.New(400, codeReportOutlineInvalid,
				"resolved requires resolved_revision_id: \"fixed\" needs a revision to point at")
		}
		target, err := s.Store.GetReportRevisionByID(ctx, tenantID, resolvedRevisionID)
		if err != nil {
			return nil, err
		}
		if target == nil || target.ReportID != reportID || target.Revision <= revision.Revision {
			// Resolving against the *same* revision is refused: a published
			// revision's body cannot change, so nothing in it can be fixed there.
			return nil, httperr.New(400, codeReportOutlineInvalid,
				"resolved_revision_id must name a later revision of the same report")
		}
		resolved = &target.ID
	}
	applied, err := s.Store.UpdateReviewFindingStatus(ctx, repository.ReviewFindingUpdate{
		TenantID: tenantID, FindingID: finding.ID, FromStatus: finding.Status, ToStatus: status,
		ReviewedBy: actorID, ReviewNote: strings.TrimSpace(note),
		ResolvedRevisionID: resolved, Now: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		// Someone else moved it between our read and our write. Re-read for the
		// honest answer instead of pretending the transition succeeded.
		current, err := s.Store.GetReviewFinding(ctx, tenantID, findingID)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, errReportFindingNotFound()
		}
		return nil, httperr.New(409, codeReportInvalidTransition,
			"finding already moved to "+current.Status)
	}
	s.recordReportAudit(ctx, actorID, tenantID, "report.finding_reviewed", reportID, map[string]any{
		"finding_id": finding.ID, "from": finding.Status, "to": status,
	})
	updated, err := s.Store.GetReviewFinding(ctx, tenantID, findingID)
	if err != nil {
		return nil, err
	}
	return reviewFindingViewOf(updated), nil
}

func reportFindingStatusKnown(status string) bool {
	for _, candidate := range model.ReportFindingStatuses() {
		if candidate == status {
			return true
		}
	}
	return false
}

// reportFindingTransitionAllowed is the closed transition table of §7.3.
//
// `dismissed` and `resolved` are terminal. Re-opening them is intentionally absent:
// a false positive that turns out to be real is a *new* finding from a new run, and
// allowing a reopen would let a reviewer erase the history of having dismissed it.
func reportFindingTransitionAllowed(from, to string) bool {
	switch from {
	case model.ReportFindingStatusOpen:
		return to == model.ReportFindingStatusAcknowledged || to == model.ReportFindingStatusDismissed
	case model.ReportFindingStatusAcknowledged:
		return to == model.ReportFindingStatusResolved || to == model.ReportFindingStatusDismissed
	}
	return false
}

// ReportReviewFindingView is one finding as the workbench renders it.
type ReportReviewFindingView struct {
	ID                 string     `json:"id"`
	SectionOrd         int        `json:"section_ord"`
	ClaimID            string     `json:"claim_id,omitempty"`
	Source             string     `json:"source"`
	Category           string     `json:"category"`
	Severity           string     `json:"severity"`
	Status             string     `json:"status"`
	Title              string     `json:"title,omitempty"`
	Detail             string     `json:"detail,omitempty"`
	SuggestedAction    string     `json:"suggested_action,omitempty"`
	ResolvedRevisionID string     `json:"resolved_revision_id,omitempty"`
	ReviewedBy         string     `json:"reviewed_by,omitempty"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote         string     `json:"review_note,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

func reviewFindingViewOf(finding *model.ReportReviewFinding) *ReportReviewFindingView {
	if finding == nil {
		return nil
	}
	view := &ReportReviewFindingView{
		ID: finding.ID, SectionOrd: finding.SectionOrd, Source: finding.Source,
		Category: finding.Category, Severity: finding.Severity, Status: finding.Status,
		Title: finding.Title, Detail: finding.Detail, SuggestedAction: finding.SuggestedAction,
		ReviewedBy: finding.ReviewedBy, ReviewedAt: finding.ReviewedAt,
		ReviewNote: finding.ReviewNote, CreatedAt: finding.CreatedAt,
	}
	if finding.ClaimID != nil {
		view.ClaimID = *finding.ClaimID
	}
	if finding.ResolvedRevisionID != nil {
		view.ResolvedRevisionID = *finding.ResolvedRevisionID
	}
	return view
}

// reportAssuranceCacheKey is the input hash of one verdict (doc/301 §6.1). Every
// component is something that can change the verdict; a component that cannot change
// it is dead weight, and a missing one lets two different questions share a cached
// answer.
//
// The list is not "whatever was easy to plumb through": it must cover every field
// `buildReportAssurancePrompt` renders, because the prompt is the question. That is
// how `Section.Title` came to be missing while the builder's own comment claimed it
// was "part of the cache key" (doc/319 R-04): the section key was hashed, the title
// that goes into 【章节】 was not, so renaming a section after a run reused verdicts
// reached under the old title.
//
// The components are concatenated with a length prefix so `("ab","c")` and
// `("a","bc")` cannot collide — the classic way a naive join makes two distinct
// inputs hash the same.
func reportAssuranceCacheKey(parts ...string) string {
	hasher := fnv.New128a()
	for _, part := range parts {
		_, _ = hasher.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hasher.Write([]byte(":"))
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte("|"))
	}
	sum := hasher.Sum(nil)
	return fmtHex(sum)
}

func fmtHex(sum []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(sum)*2)
	for _, b := range sum {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

// sortedCopy is used wherever a set feeds a hash: iteration order of a map is
// randomised in Go, and an order-dependent hash would produce a new cache key on
// every run for the same inputs.
func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}

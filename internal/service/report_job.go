package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// Report creation (doc/288 §11.4 POST /reports, §7.6 clocks, §8.4 first state).
//
// Creation is where three separate contracts get frozen at once, and mixing them
// up is the classic failure mode of this feature:
//
//  1. **Identity**: `Idempotency-Key` is request identity, stored as
//     `<user_id>:<client key>`. The same key returns the same report; a bare key
//     would let two users in one tenant collide on each other's reports.
//  2. **Structure**: the template version is pinned into the revision, so a later
//     platform upgrade never re-sections a report that is already running.
//  3. **Retention**: the four row clocks are written once, from the live policy,
//     and never recomputed (the janitor reads the row, not the config).
//
// What creation deliberately does NOT do: it does not enqueue the first runtime
// slice. A runtime job is only useful once a consumer for its kind is
// registered, and the async runner fails unclaimed kinds fast
// (`pollOnce` → "no worker registered"). Writing `rgx_job` before the consumer
// exists would therefore turn every creation into a failed job. The dispatch step
// (Slice F) owns "queued business job → first runtime slice" and is the only
// place that may set `current_runtime_job_id`.

// reportDefaultTTLDays is the fallback used when a class TTL is not configured.
// A zero value in config means "unset", not "keep forever": `expires_at` is NOT
// NULL, and a clock of `now` would delete the report on the next janitor pass.
const (
	reportDefaultTTLDays   = 90
	reportDefaultEventDays = 7
)

// reportIdempotencyKeyPattern bounds the client key. Slashes, spaces and control
// characters are refused instead of escaped: the value is compared literally, and
// a normalising comparison would merge two different requests.
var reportIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

// CreateReportRequest is the creation body. `Title` and `Question` are both
// required: a report without a question has nothing to answer, and one without a
// title cannot be found again in a list.
type CreateReportRequest struct {
	Title       string `json:"title"`
	Question    string `json:"question"`
	TemplateKey string `json:"template_key"`
	// TemplateVersion 0 means "latest visible version", which is what a new
	// report should pin; an explicit version is for reproducibility.
	TemplateVersion int64 `json:"template_version"`
	// Datasets binds the knowledge the report may cite. The capability recheck
	// runs against exactly this list (A10).
	Datasets []CapabilityCheckDataset `json:"datasets"`
	// OutlineConfirmation lets the owner skip the outline review. P0-A only
	// accepts `required`; `auto` stays frozen in the contract until P0-B opens
	// the permission (292 D2).
	OutlineConfirmation string `json:"outline_confirmation"`
	OriginChatID        string `json:"origin_chat_id"`
	OriginSessionID     string `json:"origin_session_id"`
}

// CreateReportResult is what the handler answers. `Created=false` means an
// idempotent replay returned the existing aggregate, which is a success — the
// caller must not be told its second click failed.
type CreateReportResult struct {
	Report   *model.Report         `json:"report"`
	Revision *model.ReportRevision `json:"revision"`
	Job      *model.ReportJob      `json:"job"`
	Created  bool                  `json:"created"`
}

// CreateReport validates, rechecks and freezes one report aggregate.
func (s *Service) CreateReport(
	ctx context.Context, actorID, tenantID, idempotencyKey string, req CreateReportRequest,
) (*CreateReportResult, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report creation requires an authenticated workspace")
	}
	clientKey, err := normalizeReportIdempotencyKey(idempotencyKey)
	if err != nil {
		return nil, err
	}
	// Creation runs a token-billed job, so it is gated on `execute:report` — its
	// own resource, not `execute:chat`: a report is not a conversation turn and
	// must stay revocable without touching chat (doc/288 §7.6).
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	question := strings.TrimSpace(req.Question)
	if title == "" {
		return nil, httperr.BadRequest(40105, "title is required")
	}
	if question == "" {
		return nil, httperr.BadRequest(40105, "question is required")
	}
	if len([]rune(title)) > 255 {
		return nil, httperr.BadRequest(40105, "title must be at most 255 characters")
	}
	datasets, err := normalizeReportDatasets(req.Datasets)
	if err != nil {
		return nil, err
	}
	templateRow, err := s.resolveReportTemplate(ctx, tenantID, req.TemplateKey, req.TemplateVersion)
	if err != nil {
		return nil, err
	}
	payload, err := reportTemplatePayloadOf(templateRow)
	if err != nil {
		return nil, err
	}
	if err := validateReportTemplatePayload(payload); err != nil {
		// A stored template that breaks its own contract is a server-side fault:
		// answering 400 would blame the caller for our row.
		logger.Warn("report template payload invalid", "template_id", templateRow.ID, "error", err)
		return nil, httperr.Internal("report template is invalid")
	}
	if err := s.requireReportOutlineConfirmation(actorID, req.OutlineConfirmation, payload, templateRow); err != nil {
		return nil, err
	}

	// Idempotent replay is checked before the (network-bound) capability probe:
	// a retried POST must be cheap and must not fail because the engine is briefly
	// unreachable.
	storedKey := actorID + ":" + clientKey
	if existing, err := s.Store.GetReportJobByIdempotencyKey(ctx, tenantID, storedKey); err != nil {
		return nil, err
	} else if existing != nil {
		return s.replayReportCreation(ctx, tenantID, existing)
	}
	if err := s.recheckReportCapability(ctx, tenantID, datasets); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// The clocks are resolved per tenant (T20b): a tenant override wins over the
	// system value, and the evidence `max` rule is re-applied after the overlay.
	policy := s.ReportRetentionFor(ctx, tenantID)
	reportClock := reportExpiry(now, policy.ReportDays, reportDefaultTTLDays)
	eventClock := reportExpiry(now, policy.ReportEventDays, reportDefaultEventDays)

	reportID := id.New()
	revisionID := id.New()
	jobID := id.New()
	// The knowledge binding is frozen now, not at the first retrieval: the dataset
	// link's engine id is what the run must cite, and resolving it later would let a
	// re-created dataset change what this report retrieves from (§13). The outline
	// step refuses to run without this column.
	datasetsJSON, err := s.freezeReportDatasets(ctx, tenantID, datasets)
	if err != nil {
		return nil, err
	}
	report := &model.Report{
		ID: reportID, TenantID: tenantID, Title: title, Question: question,
		OwnerUserID: actorID, OriginChatID: strings.TrimSpace(req.OriginChatID),
		OriginSessionID: strings.TrimSpace(req.OriginSessionID),
		TemplateKey:     templateRow.TemplateKey, CurrentRevisionID: revisionID,
		AccessPolicy: model.ReportAccessOwnerOnly,
		Status:       model.ReportStatusActive,
		ExpiresAt:    reportClock,
		CreatedAt:    now, UpdatedAt: now,
	}
	revision := &model.ReportRevision{
		ID: revisionID, ReportID: reportID, TenantID: tenantID, Revision: 1,
		OutlineConfirmation: model.ReportOutlineRequired,
		TemplateKey:         templateRow.TemplateKey, TemplateVersion: templateRow.Version,
		DatasetsJSON: datasetsJSON,
		// The execution snapshot is written by the outline step (Slice E), not here,
		// and exactly once: at creation the workspace default model and the
		// calibration-derived threshold are not resolved yet, and freezing a null
		// model would make A8 ("can this be reproduced?") unanswerable. From the
		// moment the outline starts, every later step reads this snapshot instead of
		// re-reading config, so the generation conditions cannot drift.
		Status:    model.ReportRevisionDraft,
		CreatedBy: actorID, CreatedAt: now,
	}
	budgetJSON, err := json.Marshal(payload.Limits)
	if err != nil {
		return nil, err
	}
	job := &model.ReportJob{
		ID: jobID, TenantID: tenantID, ReportID: reportID, RevisionID: revisionID,
		JobKind: model.ReportJobKindGenerate, Status: model.ReportJobQueued,
		Stage: model.ReportStageOutlining,
		// The first slice is described but not enqueued: see the file comment.
		AttemptSeq: 1, SliceSeq: 1,
		// Progress counts sections, so the denominator is the number of sections
		// this revision will write — not the template's declared ceiling.
		ProgressTotal: len(payload.Sections),
		BudgetJSON:    string(budgetJSON),
		// The envelope is frozen here, not at the first call: it is derived from
		// the pinned template and must not move when an admin edits the template
		// while this report runs (doc/288 §8.6).
		ReservedTokens: reportBudgetReservation(payload.Limits, len(payload.Sections)),
		IdempotencyKey: storedKey,
		UpdatedAt:      now,
	}
	eventPayload, err := json.Marshal(map[string]interface{}{
		"attempt_seq": job.AttemptSeq, "slice_seq": job.SliceSeq,
		"template_key": templateRow.TemplateKey, "template_version": templateRow.Version,
	})
	if err != nil {
		return nil, err
	}
	event := &model.ReportJobEvent{
		TenantID: tenantID, JobID: jobID, Type: reportEventJobQueued,
		PayloadJSON: string(eventPayload), CreatedAt: now, ExpiresAt: eventClock,
	}

	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateReport(ctx, report); err != nil {
			return err
		}
		if err := tx.CreateReportRevision(ctx, revision); err != nil {
			return err
		}
		if err := tx.CreateReportJob(ctx, job); err != nil {
			return err
		}
		_, err := tx.AppendReportEvent(ctx, event)
		return err
	})
	if err != nil {
		// A concurrent replay may have won the `uk_rgx_report_job_idempotency`
		// race. Answering the existing aggregate keeps "same key = same report"
		// true; anything else is a real failure.
		if existing, getErr := s.Store.GetReportJobByIdempotencyKey(ctx, tenantID, storedKey); getErr == nil && existing != nil {
			return s.replayReportCreation(ctx, tenantID, existing)
		}
		return nil, err
	}

	// Arm the first execution slice. A created report that nobody dispatches is a
	// job that stays `queued` forever: the aggregate would be visible, the UI would
	// poll a run that never starts, and no error would ever say so. The dispatch is
	// idempotent (`applied=false` when someone else armed it first), so the retry
	// path below can safely repeat it after a crash between commit and dispatch.
	job, err = s.armReportJob(ctx, tenantID, job)
	if err != nil {
		return nil, err
	}

	detail, err := json.Marshal(map[string]interface{}{
		"report_id": reportID, "job_id": jobID, "revision_id": revisionID,
		"template_key": templateRow.TemplateKey, "template_version": templateRow.Version,
		"datasets": len(datasets), "budget_tokens": job.ReservedTokens, "idempotent": false,
	})
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.job.created",
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); auditErr != nil {
		// The audit trail is a compliance requirement for this feature, not a log
		// line: a report that cannot be audited must not exist silently.
		logger.Warn("report creation audit failed", "report_id", reportID, "error", auditErr)
		return nil, auditErr
	}
	return &CreateReportResult{Report: report, Revision: revision, Job: job, Created: true}, nil
}

// replayReportCreation returns the aggregate of an already-created job.
func (s *Service) replayReportCreation(ctx context.Context, tenantID string, job *model.ReportJob) (*CreateReportResult, error) {
	report, err := s.Store.GetReport(ctx, tenantID, job.ReportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		// The job outlived its report: the retention purge removes both in one
		// transaction, so this is "purged between the two reads". Fail loudly
		// rather than inventing an aggregate.
		// 44109 is the "you may not see this" answer and is also the honest one for
		// "it no longer exists": §8.7 forbids distinguishing the two.
		return nil, httperr.New(404, codeReportAccessDenied, "report is no longer available")
	}
	// The replay answers with *this job's* revision, which is what the original 202
	// described. Reading the newest row would hand back a later regenerate's
	// revision, and the caller would take it for the run it just replayed.
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, job.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		// No revision id on the row (an older job): the report's current revision is
		// the closest honest answer.
		revision, err = s.reportCurrentRevision(ctx, tenantID, report)
		if err != nil {
			return nil, err
		}
	}
	if revision == nil {
		return nil, httperr.New(404, codeReportRevisionNotFound, "report revision is no longer available")
	}
	// Same-key replay is also the recovery path for a process that died between
	// committing the aggregate and arming the first slice: arming here is a no-op
	// for a job that already started, and heals one that did not.
	job, err = s.armReportJob(ctx, tenantID, job)
	if err != nil {
		return nil, err
	}
	return &CreateReportResult{Report: report, Revision: revision, Job: job, Created: false}, nil
}

// armReportJob dispatches the job's first slice and returns the job row as it now
// stands, so the 202 body describes the armed run instead of a pre-dispatch copy.
//
// A dispatch error is returned rather than logged: the caller would otherwise
// answer "202 accepted" for a report that will never run. The aggregate is already
// committed, and the same idempotency key replays into the dispatch above, so the
// retry semantics stay intact.
func (s *Service) armReportJob(ctx context.Context, tenantID string, job *model.ReportJob) (*model.ReportJob, error) {
	if job == nil {
		return nil, nil
	}
	if _, _, err := s.DispatchReportJob(ctx, tenantID, job.ID); err != nil {
		return nil, err
	}
	fresh, err := s.Store.GetReportJob(ctx, tenantID, job.ID)
	if err != nil {
		return nil, err
	}
	if fresh == nil {
		return job, nil
	}
	return fresh, nil
}

// normalizeReportIdempotencyKey validates the header value. The error is 44114 in
// both the missing and malformed case: the frontend must show "a key is required"
// and let the click be an explicit retry, never mint one silently (doc/288 §8.7).
func normalizeReportIdempotencyKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errReportIdempotencyKeyRequired("Idempotency-Key header is required")
	}
	if !reportIdempotencyKeyPattern.MatchString(key) {
		return "", errReportIdempotencyKeyRequired("Idempotency-Key must be 8-128 characters of [A-Za-z0-9._:-]")
	}
	return key, nil
}

// normalizeReportDatasets validates the dataset bindings. Roles default to the
// primary knowledge role, and a dataset may only appear once: two roles on one
// dataset would probe it twice and cite it under two identities.
// freezeReportDatasets resolves each bound dataset to the engine id it will be
// retrieved from, and stores the result on the revision.
//
// The engine id is resolved here because it is the *only* id the retrieval call can
// use, and the workspace id is the only id a reader can open: a report must record
// both, or "which knowledge produced this" becomes unanswerable after a dataset is
// re-created. A missing link is a real failure — silently dropping it would publish
// a report whose citations point at a dataset nobody selected.
func (s *Service) freezeReportDatasets(
	ctx context.Context, tenantID string, datasets []CapabilityCheckDataset,
) (string, error) {
	bindings := make([]reportDatasetBinding, 0, len(datasets))
	for _, binding := range datasets {
		link, err := s.Store.GetDatasetLink(ctx, tenantID, binding.DatasetID)
		if err != nil {
			return "", err
		}
		if link == nil {
			return "", httperr.NotFound("dataset " + binding.DatasetID + " not found")
		}
		engineID := strings.TrimSpace(link.RAGFlowDatasetID)
		if engineID == "" {
			// A link without an engine id cannot be retrieved from; the capability
			// recheck already refuses this shape, so reaching here means the row was
			// mutated in between.
			return "", errReportCapabilityBlocked(binding.DatasetID, "embedding_model:not_configured")
		}
		bindings = append(bindings, reportDatasetBinding{
			DatasetID: binding.DatasetID, EngineDatasetID: engineID, Role: binding.Role,
		})
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func normalizeReportDatasets(input []CapabilityCheckDataset) ([]CapabilityCheckDataset, error) {
	if len(input) == 0 {
		return nil, httperr.BadRequest(40105, "datasets[] with dataset_id is required")
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]CapabilityCheckDataset, 0, len(input))
	for i, item := range input {
		datasetID := strings.TrimSpace(item.DatasetID)
		if datasetID == "" {
			return nil, httperr.BadRequest(40105, fmt.Sprintf("datasets[%d].dataset_id is required", i))
		}
		if _, dup := seen[datasetID]; dup {
			return nil, httperr.BadRequest(40105, "datasets[] contains duplicate dataset_id "+datasetID)
		}
		seen[datasetID] = struct{}{}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = scenarioDatasetRolePrimary
		}
		switch role {
		case scenarioDatasetRolePrimary, scenarioDatasetRoleReference:
		default:
			// An unknown role would silently match no capability requirement, so the
			// dataset would be cited without ever being probed.
			return nil, httperr.BadRequest(40105,
				fmt.Sprintf("datasets[%d].role must be primary or reference", i))
		}
		out = append(out, CapabilityCheckDataset{Role: role, DatasetID: datasetID})
	}
	return out, nil
}

// requireReportOutlineConfirmation enforces the P0-A boundary. §8.4 allows a
// template to declare `auto`, but the permission to skip the structure review is
// the *owner's own* choice and belongs to P0-B; until then the API refuses to turn
// it on instead of silently honouring it (292 D2, §7.2 permission table).
func (s *Service) requireReportOutlineConfirmation(
	actorID, requested string, payload *ReportTemplatePayload, templateRow *model.ReportTemplate,
) error {
	mode := strings.TrimSpace(requested)
	if mode == "" {
		if payload.OutlineConfirmation == model.ReportOutlineAuto {
			return httperr.New(409, codeReportInvalidTransition,
				"template "+templateRow.TemplateKey+" allows auto confirmation, which this release does not implement")
		}
		return nil
	}
	switch mode {
	case model.ReportOutlineRequired:
		return nil
	case model.ReportOutlineAuto:
		if payload.OutlineConfirmation != model.ReportOutlineAuto {
			return httperr.New(409, codeReportInvalidTransition,
				"template "+templateRow.TemplateKey+" requires outline confirmation")
		}
		if actorID == "" {
			return httperr.Forbidden("outline auto confirmation requires an owner")
		}
		return httperr.New(409, codeReportInvalidTransition,
			"auto outline confirmation is not implemented in this release")
	default:
		return httperr.New(400, codeReportOutlineInvalid, "outline_confirmation must be required or auto")
	}
}

// recheckReportCapability re-probes the bound datasets at creation time (A10).
//
// It is stricter than the scenario wizard on one point. `datasetVerdict` calls a
// dataset that is configured but not yet operational `repairable`, because a
// wizard can hand the operator a repair button and let the page continue. A
// report run cannot: it is an asynchronous, token-billed job whose every section
// needs retrieval, so starting it on a dataset without a bound embedding model or
// without parsed chunks can only produce empty sections and a `partial` report —
// the silent failure §9.4/§13 forbid. Refusing here is also the only moment the
// operator can act on it.
//
// What still passes: `probe_limited` (we could not look) allows creation, because
// "unknown" is not "known bad", and a disclosure is the honest answer.
func (s *Service) recheckReportCapability(
	ctx context.Context, tenantID string, datasets []CapabilityCheckDataset,
) error {
	knowledge := reportKnowledgeContract(datasets)
	for _, binding := range datasets {
		profile, probeStatus, err := s.DescribeDataset(ctx, tenantID, binding.DatasetID)
		if err != nil {
			return err
		}
		verdict := EvaluateRequirements(knowledge, binding.Role, *profile)
		verdict.ProbeStatus = probeStatus
		reason, blocked := reportCapabilityGate(verdict)
		if !blocked {
			continue
		}
		// The refusal carries the dataset id and the failed requirement, but never
		// the repair endpoint: repair hints belong to the capability-check endpoint,
		// whose answer the UI already renders as buttons.
		return errReportCapabilityBlocked(binding.DatasetID, reason)
	}
	return nil
}

// reportCapabilityGate decides whether one dataset refusal blocks a report run.
// A failed probe is not a reason to refuse: it caps the report's disclosure, not
// its existence.
func reportCapabilityGate(verdict DatasetCapabilityVerdict) (string, bool) {
	if verdict.Verdict == CapabilityVerdictBlocked {
		return firstUnsatisfiedReason(verdict), true
	}
	if verdict.ProbeStatus == CapabilityProbeFailed {
		return "", false
	}
	// Evidence-critical requirements: a section that cannot retrieve cannot be
	// written, and the run is billed per slice.
	for _, item := range verdict.Requirements {
		if item.Satisfied || item.Capability != CapabilityVector {
			continue
		}
		return item.Capability + ":" + item.Reason, true
	}
	return "", false
}

func firstUnsatisfiedReason(verdict DatasetCapabilityVerdict) string {
	for _, item := range verdict.Requirements {
		if item.Satisfied {
			continue
		}
		return item.Capability + ":" + item.Reason
	}
	return verdict.Verdict
}

// reportKnowledgeContract is the capability contract of a report run: every bound
// dataset must serve retrieval at the `operational` level, which is what
// "已解析且绑定了嵌入模型" means in evidence terms (doc/288 §13).
func reportKnowledgeContract(datasets []CapabilityCheckDataset) *ScenarioTemplateKnowledge {
	// One requirement covers every binding: `scope=all` is evaluated once per
	// dataset, so repeating it per binding would only multiply the same verdict.
	requirements := []ScenarioCapabilityRequirement{{
		Capability: CapabilityVector,
		// All roles, not primary-only: a reference dataset is cited exactly like a
		// primary one, so the evidence requirement cannot skip it.
		Scope:    scenarioRequirementScopeAll,
		MinLevel: scenarioRequirementLevelOperation,
	}}
	roles := make([]ScenarioKnowledgeDataset, 0, len(datasets))
	for _, binding := range datasets {
		roles = append(roles, ScenarioKnowledgeDataset{Role: binding.Role})
	}
	return &ScenarioTemplateKnowledge{
		Aggregate:            scenarioAggregateAllRequired,
		RequiredCapabilities: requirements,
		Datasets:             roles,
	}
}

func reportExpiry(now time.Time, configuredDays, fallbackDays int) time.Time {
	days := configuredDays
	if days <= 0 {
		days = fallbackDays
	}
	return now.AddDate(0, 0, days)
}

// RestoreReport re-opens a soft-deleted report inside its recovery window (A47).
//
// The permission is `delete:own`, the same action that deleted it: whoever may close
// a report may re-open it, and re-opening is not a read permission someone could hold
// separately. A `force=true` delete has a zero-length window, so it refuses here with
// 44104 rather than resurrecting a row the caller explicitly asked to lose.
//
// The answer is the report detail view because that is what the client needs next: a
// `{"restored":true}` would force every caller to re-read the report it just got
// back, and the window between the two reads is exactly when the janitor could take
// it away again.
func (s *Service) RestoreReport(ctx context.Context, actorID, tenantID, reportID string) (*ReportDetail, error) {
	if err := s.AuthorizeObject(ctx, actorID, "delete:own", "report", tenantID, ""); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	// The row is read before the compare-and-swap only to tell the three refusals
	// apart; whether the restore is allowed is decided by the UPDATE in the store.
	deleted, err := s.Store.GetReportIncludingDeleted(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if deleted == nil {
		return nil, errReportNotFound()
	}
	if deleted.OwnerUserID != actorID {
		ac, err := s.authz(ctx, actorID)
		if err != nil {
			return nil, err
		}
		if !s.canAccessAcrossTenant(ac, "manage") {
			return nil, errReportNotFound()
		}
	}
	if deleted.DeletedAt == nil {
		return nil, errReportInvalidTransition("report is not deleted")
	}
	if !deleted.ExpiresAt.IsZero() && !deleted.ExpiresAt.After(now) {
		// Retention already ended. Recycling it would produce a report the next
		// janitor pass deletes again, which reads to the user as "my restore was
		// silently undone".
		return nil, errReportInvalidTransition("report retention has ended")
	}
	restored, err := s.Store.RestoreReport(ctx, tenantID, reportID, now)
	if err != nil {
		return nil, err
	}
	if !restored {
		// Lost the race with the janitor (or with a later delete): the window closed
		// between the read and the write.
		return nil, errReportInvalidTransition("report recovery window has closed")
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.restored",
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: `{"cleanup_hold_until":null}`, At: now,
	}); err != nil {
		// The restore already happened. Reporting it as a failure would make the user
		// retry and be told "report is not deleted", so the audit error is logged and
		// the restored row — the thing the caller asked for — is still returned.
		logger.Warn("report restore audit failed", "report_id", reportID, "error", err)
	}
	return s.GetReport(ctx, actorID, tenantID, reportID)
}

// SoftDeleteReport closes a report and opens its recovery window. The recovery
// window is a grace period on a deleted row, never a TTL extension: `expires_at`
// stays exactly as it was frozen at creation (doc/288 §7.6).
//
// `force` collapses the recovery window to zero (A47): the report disappears now and
// can no longer be restored, so the only thing left is the purge at its original
// `expires_at`. It is a stronger action than "delete my own report" and therefore
// needs `manage:report` — a caller who may only delete their own rows must not be
// able to shorten somebody else's recovery window either.
func (s *Service) SoftDeleteReport(ctx context.Context, actorID, tenantID, reportID string, reason string, force bool) error {
	// `delete:own` is the route-level action too: the RBAC answer is "you may
	// delete rows of your own", and the row check below decides whether this row
	// is one of them.
	if err := s.AuthorizeObject(ctx, actorID, "delete:own", "report", tenantID, ""); err != nil {
		return err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return err
	}
	if report == nil {
		return errReportNotFound()
	}
	if report.OwnerUserID != actorID {
		// `owner_only` answers "not found" to every other principal. A platform
		// administrator may delete across tenants (governance), but a same-tenant
		// colleague may not: deleting someone else's report is never an audit read.
		ac, err := s.authz(ctx, actorID)
		if err != nil {
			return err
		}
		if !s.canAccessAcrossTenant(ac, "manage") {
			return errReportNotFound()
		}
	}
	if force {
		if err := s.AuthorizeObject(ctx, actorID, "manage", "report", tenantID, ""); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	// The recovery window follows the tenant's overlay too (T20b). It is a policy on
	// the *delete*, not on the row: `expires_at` is untouched either way (§7.6).
	holdDays := s.ReportRetentionFor(ctx, tenantID).ReportCleanupHoldDays
	if holdDays < 0 {
		holdDays = 0
	}
	holdUntil := now.AddDate(0, 0, holdDays)
	if force {
		// Zero-length window: `RestoreReport` requires `cleanup_hold_until >= now`,
		// so this is the one write that closes the door while keeping the row for
		// the purge audit trail.
		holdUntil = now
	}
	if err := s.Store.SoftDeleteReport(ctx, tenantID, reportID, now, holdUntil); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Someone deleted it between the read and the write. Same answer as "no
			// such report": the second delete must not open a fresh window.
			return errReportNotFound()
		}
		return err
	}
	detail, err := json.Marshal(map[string]interface{}{
		"reason": strings.TrimSpace(reason), "cleanup_hold_until": holdUntil,
		"force": force,
	})
	if err != nil {
		return err
	}
	return s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.soft_deleted",
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	})
}

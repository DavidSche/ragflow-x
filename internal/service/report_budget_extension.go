package service

// Budget extension: the one governed way a frozen envelope may grow (doc/296 §2.2 /
// A71).
//
// The problem this file solves is real and narrow. A report's envelope is frozen at
// creation from the pinned template (§8.6), and the gate refuses any call that would
// cross it. When that happens the report is *not* broken: the written sections stay
// published, the rest are disclosed as `budget_exhausted` gaps, and the run ends
// `partial`. That is the correct default. But an operator who needs the remaining
// sections has only one honest option today: create another report. That is expensive
// and produces a second, inconsistent document.
//
// So: an approval may widen the envelope, and nothing else.
//
//   - **It cannot rewrite the ledger.** `rgx_report_charge` rows are what was spent;
//     an approval changes what may be spent next. Both numbers stay true, and the
//     overage that already exists is still audited as overage.
//   - **It cannot lower the envelope.** An approver who could shrink one mid-run would
//     be killing calls that are already paid for, which §8.6 forbids more strongly
//     than overspending.
//   - **It is bounded.** One extension may add at most the original envelope, so the
//     ceiling is at most 2× what the template asked for. Without a bound, "approve a
//     bit more" is a form of unbounded spend that a policy engine will happily wave
//     through.
//   - **It never touches `report.status`.** The run resumes because the gate now says
//     yes; nothing here writes a terminal state (that stays finalize's job, §8.4).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// ReportBudgetExtensionRequest is the extension payload. `tokens` is an *increment*,
// not a new ceiling: "add 4000" survives a concurrent extension applied between the
// read and the write, "set the ceiling to 8000" would silently undo it.
type ReportBudgetExtensionRequest struct {
	Tokens int64  `json:"tokens"`
	Reason string `json:"reason"`
}

// ReportBudgetExtensionView is what the caller gets back.
//
// `PendingApproval` is not an error and not a failure: the workspace has configured a
// governance step, and the answer is "nothing has changed yet, here is the approval".
// The status word is part of the contract so the UI can render "等待审批" instead of
// inventing one.
type ReportBudgetExtensionView struct {
	Status      string `json:"status"`
	JobID       string `json:"job_id,omitempty"`
	AddedTokens int64  `json:"added_tokens"`
	BudgetAfter int64  `json:"budget_tokens"`
	ApprovalID  string `json:"approval_id,omitempty"`
}

// ReportBudgetExtensionPending and ReportBudgetExtensionApplied are the two status
// words of the view. They are a closed set on the wire, not free text.
const (
	ReportBudgetExtensionPending = "pending_approval"
	ReportBudgetExtensionApplied = "applied"
)

// reportBudgetExtensionMaxFactor bounds one extension to the original envelope.
const reportBudgetExtensionMaxFactor = 1

// RequestReportBudgetExtension asks for more budget on the report's live job.
//
// The flow is the knowledge-task flow (`SubmitApproval` → registry executor) for one
// reason: it is the code path the workspace already audited, and a second, bespoke
// approval mechanism for reports would be a second thing to get wrong. When the
// workspace has no matching approval policy, `SubmitApproval` answers `nil` and the
// extension is applied immediately — a workspace that configured no governor must not
// be blocked by one.
func (s *Service) RequestReportBudgetExtension(
	ctx context.Context, actorID, tenantID, reportID string, req ReportBudgetExtensionRequest,
) (*ReportBudgetExtensionView, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report budget extension requires an authenticated workspace")
	}
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}
	if err := s.assertReportOwner(ctx, actorID, report); err != nil {
		return nil, err
	}
	// An archived report produces nothing new (doc/297 §2): unarchive first.
	if err := s.assertReportArchivedBlocksWork(report); err != nil {
		return nil, err
	}
	job, err := s.Store.GetLatestReportJob(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.FinalizedAt != nil {
		// A finished run's envelope is history; extending it would change what the
		// delivered report was allowed to spend after the fact.
		return nil, errReportInvalidTransition("only a running report can be granted more budget")
	}
	if job.ReportBudgetPolicy == model.ReportBudgetObserve {
		// `observe` never refuses a call, so there is nothing to extend. Answering
		// "applied" here would book an approval for an action with no effect.
		return nil, errReportInvalidTransition(
			"this workspace measures report spend without enforcing a limit, so no extension is needed")
	}
	if req.Tokens <= 0 {
		return nil, errReportAccessInvalid("tokens must be a positive increment")
	}
	budget := job.ReservedTokens
	if budget <= 0 {
		return nil, errReportLedgerInconsistent("report job " + job.ID + " has no budget envelope")
	}
	if req.Tokens > budget*reportBudgetExtensionMaxFactor {
		return nil, errReportAccessInvalid(
			"one extension may add at most the report's original budget")
	}
	reason := strings.TrimSpace(req.Reason)
	if len([]rune(reason)) > 200 {
		reason = string([]rune(reason)[:200])
	}
	payload := map[string]any{
		"report_id": reportID, "job_id": job.ID,
		"tokens": req.Tokens, "budget_tokens": budget, "reason": reason,
	}
	applyDirect := func() (*ReportBudgetExtensionView, error) {
		updated, applyErr := s.applyReportBudgetExtension(ctx, tenantID, job.ID, req.Tokens, "direct",
			map[string]any{"report_id": reportID, "requested_by": actorID, "reason": reason})
		if applyErr != nil {
			return nil, applyErr
		}
		if updated == nil {
			return nil, errReportInvalidTransition("the report finished before the extension applied")
		}
		return &ReportBudgetExtensionView{
			Status: ReportBudgetExtensionApplied, JobID: updated.ID,
			AddedTokens: req.Tokens, BudgetAfter: updated.ReservedTokens,
		}, nil
	}
	// A workspace with the workflow switched off (or with no matching policy) applies
	// the increment directly: `SubmitApproval` answers an error for "disabled" and
	// `nil` for "no policy", and a workspace that governs nothing must not be blocked
	// by the governor it does not have.
	if !s.currentApprovalConfig().Enabled {
		return applyDirect()
	}
	approval, err := s.SubmitApproval(ctx, actorID, ApprovalSubmitRequest{
		TenantID: tenantID, ObjectType: model.ApprovalObjectReport,
		Action: model.ApprovalActionExtendBudget, ObjectID: reportID,
		Title: "Extend report budget: " + report.Title,
		// The reason is the approver's only evidence: an approval for "+4000 tokens"
		// without a "why" cannot be decided, only rubber-stamped.
		Reason: reason, Payload: payload,
		// The key includes the current envelope so a retry of the *same* request with
		// the same numbers is idempotent, while a second, separate extension after the
		// first landed is a new key.
		IdempotencyKey: "report-budget-" + job.ID + "-" + strconv.FormatInt(budget, 10) + "-" + strconv.FormatInt(req.Tokens, 10),
	})
	if err != nil {
		return nil, err
	}
	if approval != nil {
		if auditErr := s.RecordAudit(ctx, &model.AuditLog{
			TenantID: tenantID, UserID: actorID, Action: "report.budget.extension_requested",
			Resource: "report", ResourceID: reportID, Result: "SUCCESS",
			DetailJSON: reportBudgetAuditDetail(job, req.Tokens, "approval", approval.ID),
			At:         time.Now().UTC(),
		}); auditErr != nil {
			logger.Warn("report budget extension audit failed", "report_id", reportID, "error", auditErr)
		}
		return &ReportBudgetExtensionView{
			Status: ReportBudgetExtensionPending, JobID: job.ID,
			AddedTokens: req.Tokens, BudgetAfter: budget, ApprovalID: approval.ID,
		}, nil
	}
	return applyDirect()
}

// applyReportBudgetExtension is the single writer of `reserved_tokens` after creation.
//
// Both callers go through it — the direct path and the approval executor — so the
// audit action, the "never on a finalized job" rule and the returned number cannot
// differ between "nobody governs this" and "an approver said yes".
func (s *Service) applyReportBudgetExtension(
	ctx context.Context, tenantID, jobID string, tokens int64, via string, extra map[string]any,
) (*model.ReportJob, error) {
	if tokens <= 0 {
		return nil, errReportAccessInvalid("tokens must be a positive increment")
	}
	now := time.Now().UTC()
	before, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	updated, err := s.Store.ExtendReportBudget(ctx, tenantID, jobID, tokens, now)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, nil
	}
	// The amounts are spelled `*_budget`, not `*_tokens`: the audit writer redacts every
	// key containing "token" (a secret filter, and the right default), so a well-named
	// field here would be stored as `"[REDACTED]"` and the trail would lose the only
	// number it exists to carry. Same rule as the charge audit (doc/288 §8.6).
	detail := map[string]any{
		"job_id": jobID, "added_budget": tokens, "via": via,
		"budget_before": budgetBeforeOf(before, updated, tokens),
		"budget_after":  updated.ReservedTokens,
	}
	for key, value := range extra {
		detail[key] = value
	}
	encoded, marshalErr := json.Marshal(detail)
	if marshalErr != nil {
		encoded = []byte("{}")
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: approvalActorOf(extra), Action: "report.budget.extended",
		Resource: "report", ResourceID: reportIDOf(detail), Result: "SUCCESS",
		DetailJSON: string(encoded), At: now,
	}); auditErr != nil {
		// The envelope already grew; failing now would misreport it. Log and continue,
		// the same way every other report audit treats a late failure.
		logger.Warn("report budget extension audit failed", "job_id", jobID, "error", auditErr)
	}
	logger.Info("report budget extended", "job_id", jobID, "added", tokens, "via", via)
	return updated, nil
}

// budgetBeforeOf prefers the pre-read row and falls back to arithmetic: the pre-read
// can be older than the transaction that just landed (a concurrent second extension),
// and a wrong "before" would make the audit trail unreadable.
func budgetBeforeOf(before, after *model.ReportJob, added int64) int64 {
	if after != nil {
		return after.ReservedTokens - added
	}
	if before != nil {
		return before.ReservedTokens
	}
	return 0
}

func approvalActorOf(extra map[string]any) string {
	if extra == nil {
		return ""
	}
	actor, _ := extra["approver_id"].(string)
	if actor == "" {
		actor, _ = extra["requested_by"].(string)
	}
	return actor
}

func reportIDOf(detail map[string]any) string {
	reportID, _ := detail["report_id"].(string)
	return reportID
}

// reportBudgetAuditDetail is the payload of "an extension was requested". It names the
// approval so the three rows (requested → approved → extended) can be read as a
// sequence instead of guessed at.
func reportBudgetAuditDetail(job *model.ReportJob, tokens int64, via, approvalID string) string {
	if job == nil {
		return "{}"
	}
	encoded, err := json.Marshal(map[string]any{
		"job_id": job.ID, "added_budget": tokens, "budget_before": job.ReservedTokens,
		"via": via, "approval_id": approvalID,
	})
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// validateReportBudgetExtension is the executor's pre-flight: the approval may only be
// executed while the job it was written for is still live, and the payload may only
// name an increment inside the documented bound.
//
// Re-validating here is not redundant with the submit-time check: an approval can sit
// for hours, and in that time the run may finish, the workspace may switch to
// `observe`, or a second approval may already have doubled the envelope.
func validateReportBudgetExtension(ctx context.Context, svc *Service, approval *model.Approval) error {
	if approval.Action != model.ApprovalActionExtendBudget {
		return httperr.BadRequest(codeReportOutlineInvalid, "unsupported report approval action")
	}
	input, job, err := svc.reportBudgetExtensionTarget(ctx, approval)
	if err != nil {
		return err
	}
	if job.ReportBudgetPolicy == model.ReportBudgetObserve {
		return httperr.New(409, codeReportInvalidTransition,
			"this workspace no longer enforces a budget, so the extension has no effect")
	}
	// The bound is applied against the job's *current* envelope, so a second approval
	// cannot walk the ceiling up one envelope at a time without limit.
	if input.Tokens > job.ReservedTokens*reportBudgetExtensionMaxFactor {
		return httperr.New(409, codeReportInvalidTransition,
			"the requested increment exceeds the report's current budget")
	}
	return nil
}

// executeReportBudgetExtension widens the envelope and reports what changed.
func executeReportBudgetExtension(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	input, job, err := svc.reportBudgetExtensionTarget(ctx, approval)
	if err != nil {
		return nil, err
	}
	updated, err := svc.applyReportBudgetExtension(ctx, approval.TenantID, job.ID, input.Tokens,
		"approval", map[string]any{
			"report_id": job.ReportID, "approval_id": approval.ID,
			"approver_id": approvalExecutionActor(approval), "reason": input.Reason,
		})
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, httperr.New(409, codeReportInvalidTransition,
			"the report finished before the extension applied")
	}
	return map[string]any{
		"report_id": job.ReportID, "job_id": job.ID,
		"added_tokens": input.Tokens, "budget_tokens": updated.ReservedTokens,
	}, nil
}

// approvalExecutionActor names who the executed change is attributed to. The approval
// row carries no "last approver" column here, so the requester is used and the detail
// says `via=approval` — the sequence itself is the evidence, and inventing an actor
// from the step table would be a guess.
func approvalExecutionActor(approval *model.Approval) string {
	if approval == nil {
		return ""
	}
	return approval.RequesterID
}

// reportBudgetExtensionTarget decodes the approval payload and re-reads the job.
func (s *Service) reportBudgetExtensionTarget(
	ctx context.Context, approval *model.Approval,
) (ReportBudgetExtensionRequest, *model.ReportJob, error) {
	input := ReportBudgetExtensionRequest{}
	if approval == nil {
		return input, nil, httperr.BadRequest(codeReportOutlineInvalid, "approval is required")
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return input, nil, err
	}
	if err := decodeApprovalPayload(payload, &input); err != nil {
		return input, nil, httperr.BadRequest(codeReportOutlineInvalid, "invalid report budget approval payload")
	}
	if input.Tokens <= 0 {
		return input, nil, httperr.BadRequest(codeReportOutlineInvalid, "tokens must be positive")
	}
	jobID, _ := payload["job_id"].(string)
	if jobID == "" {
		return input, nil, httperr.BadRequest(codeReportOutlineInvalid, "the approval does not name a report job")
	}
	job, err := s.Store.GetReportJob(ctx, approval.TenantID, jobID)
	if err != nil {
		return input, nil, err
	}
	if job == nil {
		return input, nil, errReportNotFound()
	}
	if job.FinalizedAt != nil {
		return input, nil, httperr.New(409, codeReportInvalidTransition,
			"the report already finished, so its budget cannot be extended")
	}
	return input, job, nil
}

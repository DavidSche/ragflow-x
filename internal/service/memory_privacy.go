package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// MemoryPrivacyRuleVersion pins the deterministic PII rule set. It is stamped
// onto every evaluated memory operation and every exemption row so audits can
// reproduce the decision even after rules evolve.
const MemoryPrivacyRuleVersion = "memory-pii-rules-v1"

// Memory privacy actions. BLOCK rejects the write; REDACT replaces matched
// spans before the payload is sealed; ALLOW only means the deterministic gate
// did not intercept — scope, consent and retention checks still apply.
const (
	MemoryPrivacyActionBlock  = "BLOCK"
	MemoryPrivacyActionRedact = "REDACT"
	MemoryPrivacyActionAllow  = "ALLOW"
)

type memoryPrivacyRule struct {
	Label   string
	Pattern *regexp.Regexp
	// HighRisk labels are credentials, keys, connection strings and similar
	// secrets. They always BLOCK and can never be exempted or redacted.
	HighRisk bool
}

// memoryPrivacyRulesV1 is the deterministic first version of the PII rule set
// (137 plan section 6.4): no NER, label + count output, fixed patterns.
var memoryPrivacyRulesV1 = []memoryPrivacyRule{
	{Label: "phone_cn", Pattern: regexp.MustCompile(`(?:^|\D)(1[3-9]\d{9})(?:\D|$)`)},
	{Label: "id_card_cn", Pattern: regexp.MustCompile(`(?:^|\D)([1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx])(?:\D|$)`)},
	{Label: "bank_card", Pattern: regexp.MustCompile(`(?:^|\D)((?:62|60|48|45|43|49|51|52|53|55)\d{11,18})(?:\D|$)`)},
	{Label: "email", Pattern: regexp.MustCompile(`([A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,})`)},
	{Label: "ipv4", Pattern: regexp.MustCompile(`\b((?:\d{1,3}\.){3}\d{1,3})\b`)},
	{Label: "credential", Pattern: regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret)["'\s:=]{1,4}(\S{6,})`), HighRisk: true},
	{Label: "api_key", Pattern: regexp.MustCompile(`\b((?:sk|rk|pk|rc|ak|ghp|gho|xoxb|xoxp|AIza)[-_A-Za-z0-9]{15,})\b`), HighRisk: true},
	{Label: "db_connection_string", Pattern: regexp.MustCompile(`\b((?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|jdbc:[a-z0-9]+)://[^\s'"]{6,})`), HighRisk: true},
	{Label: "jwt", Pattern: regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b`), HighRisk: true},
	{Label: "private_key_pem", Pattern: regexp.MustCompile(`(-----BEGIN [A-Z ]*PRIVATE KEY-----)`), HighRisk: true},
	{Label: "business_license_cn", Pattern: regexp.MustCompile(`(?:^|[^0-9A-HJ-NPQRTUWXY])([0-9A-HJ-NPQRTUWXY]{18})(?:[^0-9A-HJ-NPQRTUWXY]|$)`)},
	{Label: "certificate_no", Pattern: regexp.MustCompile(`(?:证书(?:编号|号)|证号)[:：]?\s*([A-Za-z0-9-]{6,})`)},
	{Label: "upload_link", Pattern: regexp.MustCompile(`(?i)\b(https?://[^\s'"]*/(?:uploads?|files?|attachments?)/[^\s'"]+)`)},
}

// MemoryPrivacyLabelHit reports one label's occurrence count for an evaluated
// text. Matched raw content never leaves the gate.
type MemoryPrivacyLabelHit struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// MemoryPrivacyEvaluation is the outcome of running the deterministic gate
// over a write payload.
type MemoryPrivacyEvaluation struct {
	Action           string
	RuleVersion      string
	Labels           []MemoryPrivacyLabelHit
	RedactedInput    string
	RedactedResponse string
	ExemptionsHit    []string
}

type memoryPrivacyExemption struct {
	Label   string
	Pattern *regexp.Regexp
}

// EvaluateMemoryPrivacy runs the deterministic PII gate over a write payload.
//
// Resolution order per 137 section 6.4: the configured policy action decides
// BLOCK/REDACT/ALLOW for ordinary labels; high-risk labels always BLOCK;
// tenant exemptions may only suppress ordinary labels and every exemption hit
// is auditable metadata (label + rule version, never raw content).
func (s *Service) EvaluateMemoryPrivacy(
	ctx context.Context, tenantID string, policy *model.MemoryPolicy,
	userInput, agentResponse string,
) (*MemoryPrivacyEvaluation, error) {
	action, err := memoryPrivacyActionForPolicy(policy)
	if err != nil {
		return nil, err
	}
	exemptions, err := s.loadMemoryPrivacyExemptions(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	evaluation := &MemoryPrivacyEvaluation{
		Action:      action,
		RuleVersion: MemoryPrivacyRuleVersion,
	}
	counts := map[string]int{}
	exemptionHits := map[string]bool{}
	blocked := false
	anyHit := false

	redact := func(text string) string {
		for _, rule := range memoryPrivacyRulesV1 {
			updated, labelCount, exemptionLabels, hit := memoryPrivacyApplyRule(text, rule, exemptions)
			for label := range exemptionLabels {
				exemptionHits[label] = true
			}
			counts[rule.Label] += labelCount
			if labelCount > 0 {
				anyHit = true
			}
			if hit && (rule.HighRisk || action == MemoryPrivacyActionBlock) {
				blocked = true
			}
			if hit && !rule.HighRisk && action == MemoryPrivacyActionRedact {
				text = updated
			}
		}
		return text
	}

	evaluation.RedactedInput = redact(userInput)
	evaluation.RedactedResponse = redact(agentResponse)

	for _, rule := range memoryPrivacyRulesV1 {
		if counts[rule.Label] > 0 {
			evaluation.Labels = append(evaluation.Labels, MemoryPrivacyLabelHit{Label: rule.Label, Count: counts[rule.Label]})
		}
	}
	for label := range exemptionHits {
		evaluation.ExemptionsHit = append(evaluation.ExemptionsHit, label)
	}

	if blocked {
		evaluation.Action = MemoryPrivacyActionBlock
		// A blocked payload is never persisted or dispatched: do not let the
		// evaluation carry raw content back into logs or previews.
		evaluation.RedactedInput = ""
		evaluation.RedactedResponse = ""
	} else if !anyHit {
		// No rule intercepted the payload: report honestly that the gate did
		// not act, regardless of the configured posture.
		evaluation.Action = MemoryPrivacyActionAllow
	}
	return evaluation, nil
}

// memoryPrivacyApplyRule scans one rule over the text. It returns the text
// with non-exempted matches replaced by placeholders (only meaningful when the
// caller redacts), the number of counted matches, the exemption labels hit,
// and whether any non-exempted match was found.
func memoryPrivacyApplyRule(
	text string, rule memoryPrivacyRule, exemptions []memoryPrivacyExemption,
) (string, int, map[string]bool, bool) {
	if text == "" {
		return text, 0, nil, false
	}
	matches := rule.Pattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, 0, nil, false
	}
	// Group 1 is the sensitive span; boundaries around it must survive.
	groupOf := func(match []int) (int, int) {
		if len(match) >= 4 && match[2] >= 0 {
			return match[2], match[3]
		}
		return match[0], match[1]
	}
	exemptedSpans := map[string]bool{}
	exemptionLabels := map[string]bool{}
	if !rule.HighRisk {
		for _, exemption := range exemptions {
			if exemption.Label != rule.Label {
				continue
			}
			for _, match := range exemption.Pattern.FindAllString(text, -1) {
				exemptedSpans[match] = true
				exemptionLabels[exemption.Label] = true
			}
		}
	}
	var builder strings.Builder
	last := 0
	counted := 0
	sequence := 0
	for _, match := range matches {
		groupStart, groupEnd := groupOf(match)
		captured := text[groupStart:groupEnd]
		if exemptedSpans[captured] {
			continue
		}
		counted++
		sequence++
		builder.WriteString(text[last:groupStart])
		fmt.Fprintf(&builder, "[REDACTED:%s#%d]", rule.Label, sequence)
		last = groupEnd
	}
	if counted == 0 {
		return text, 0, exemptionLabels, false
	}
	if actionRedacts := !rule.HighRisk; actionRedacts {
		builder.WriteString(text[last:])
		return builder.String(), counted, exemptionLabels, true
	}
	return text, counted, exemptionLabels, true
}

// memoryPrivacyActionForPolicy resolves the gate action from the policy
// config's `pii.action`; the fail-safe default is REDACT. Invalid values are
// rejected instead of silently downgraded.
func memoryPrivacyActionForPolicy(policy *model.MemoryPolicy) (string, error) {
	action := MemoryPrivacyActionRedact
	if policy != nil && strings.TrimSpace(policy.Config) != "" {
		var config struct {
			PII *struct {
				Action string `json:"action"`
			} `json:"pii"`
		}
		if err := json.Unmarshal([]byte(policy.Config), &config); err != nil {
			return "", httperr.BadRequest(40094, "memory policy config must be valid JSON for privacy evaluation")
		}
		if config.PII != nil && strings.TrimSpace(config.PII.Action) != "" {
			action = strings.ToUpper(strings.TrimSpace(config.PII.Action))
		}
	}
	switch action {
	case MemoryPrivacyActionBlock, MemoryPrivacyActionRedact, MemoryPrivacyActionAllow:
		return action, nil
	default:
		return "", httperr.BadRequest(40095, "memory policy pii.action must be BLOCK, REDACT or ALLOW")
	}
}

func (s *Service) loadMemoryPrivacyExemptions(ctx context.Context, tenantID string) ([]memoryPrivacyExemption, error) {
	rows, err := s.Store.ListMemoryPrivacyExemptions(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	exemptions := make([]memoryPrivacyExemption, 0, len(rows))
	for _, row := range rows {
		if memoryPrivacyRuleHighRisk(row.Label) {
			// High-risk labels are not exemptable; skip defensively even if a
			// row somehow exists.
			continue
		}
		pattern, err := regexp.Compile(row.Pattern)
		if err != nil {
			return nil, httperr.New(500, 50082, "memory privacy exemption pattern is invalid")
		}
		exemptions = append(exemptions, memoryPrivacyExemption{Label: row.Label, Pattern: pattern})
	}
	return exemptions, nil
}

func memoryPrivacyRuleHighRisk(label string) bool {
	for _, rule := range memoryPrivacyRulesV1 {
		if rule.Label == label {
			return rule.HighRisk
		}
	}
	return false
}

// MemoryPrivacyExemptionInput is the API payload for tenant-level PII
// exemptions (manage @ memory-policy).
type MemoryPrivacyExemptionInput struct {
	Label   string `json:"label" binding:"required"`
	Pattern string `json:"pattern" binding:"required"`
}

// requirePrivacyExemptionGovernance enforces plan-137 §10.1 rule 15: PII
// exemptions are a tenant-level control. Only tenant admins (or platform
// admins acting in cross-tenant governance context) may configure them;
// team_admin own-project manage, operator, business_user and viewer are
// rejected at the service boundary, independent of UI visibility.
func (s *Service) requirePrivacyExemptionGovernance(ctx context.Context, tenantID, actorID string) error {
	actor, err := s.CurrentUser(ctx, actorID)
	if err != nil {
		return err
	}
	if actor == nil {
		return httperr.Forbidden("actor is required")
	}
	if actor.TenantID != tenantID && actor.Role != model.RolePlatformAdmin {
		return httperr.Forbidden("cross-tenant privacy governance requires platform context")
	}
	if actor.Role != model.RoleTenantAdmin && actor.Role != model.RolePlatformAdmin {
		return httperr.Forbidden("only tenant or platform admins may configure PII exemptions")
	}
	return nil
}

// ListMemoryPrivacyExemptions returns the tenant's exemption rows.
func (s *Service) ListMemoryPrivacyExemptions(ctx context.Context, tenantID string) ([]model.MemoryPrivacyExemption, error) {
	return s.Store.ListMemoryPrivacyExemptions(ctx, tenantID, false)
}

// CreateMemoryPrivacyExemption registers a versioned tenant exemption for a
// non-high-risk label. High-risk categories are rejected unconditionally.
func (s *Service) CreateMemoryPrivacyExemption(
	ctx context.Context, tenantID, actorID string, input MemoryPrivacyExemptionInput,
) (*model.MemoryPrivacyExemption, error) {
	if err := s.requirePrivacyExemptionGovernance(ctx, tenantID, actorID); err != nil {
		return nil, err
	}
	input.Label = strings.TrimSpace(input.Label)
	input.Pattern = strings.TrimSpace(input.Pattern)
	if input.Label == "" || input.Pattern == "" {
		return nil, httperr.BadRequest(40096, "label and pattern are required")
	}
	if len(input.Label) > 64 || len(input.Pattern) > 256 {
		return nil, httperr.BadRequest(40096, "label and pattern exceed their size limits")
	}
	if memoryPrivacyRuleHighRisk(input.Label) {
		return nil, httperr.Forbidden("high-risk privacy labels cannot be exempted")
	}
	if _, err := regexp.Compile(input.Pattern); err != nil {
		return nil, httperr.BadRequest(40096, "pattern must be a valid regular expression")
	}
	now := time.Now().UTC()
	exemption := &model.MemoryPrivacyExemption{
		ID: id.New(), TenantID: tenantID, Label: input.Label, Pattern: input.Pattern,
		RuleVersion: MemoryPrivacyRuleVersion, Version: 1, Active: true,
		CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateMemoryPrivacyExemption(ctx, exemption); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: actorID,
		Action: "memory_privacy.exemption_created", Resource: "memory-policy",
		ResourceID: exemption.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"label": exemption.Label, "rule_version": exemption.RuleVersion,
			"version": exemption.Version,
		}),
		At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "manage:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}); err != nil {
		return nil, err
	}
	return exemption, nil
}

// PreviewMemoryPrivacy is the governance diagnostic behind
// POST /memory-privacy/pii-preview: run sample text through the deterministic
// gate with the tenant's current exemptions. Nothing is persisted; the
// response carries label+count and the resolved action, never stored corpus.
func (s *Service) PreviewMemoryPrivacy(
	ctx context.Context, tenantID, actorID, userInput, agentResponse string,
) (*MemoryPrivacyEvaluation, error) {
	if err := s.requirePrivacyExemptionGovernance(ctx, tenantID, actorID); err != nil {
		return nil, err
	}
	if len(userInput) > 64*1024 || len(agentResponse) > 64*1024 {
		return nil, httperr.BadRequest(40098, "preview payload exceeds the 64KB limit")
	}
	return s.EvaluateMemoryPrivacy(ctx, tenantID, nil, userInput, agentResponse)
}

// DeleteMemoryPrivacyExemption removes a tenant exemption and audits it.
func (s *Service) DeleteMemoryPrivacyExemption(
	ctx context.Context, tenantID, actorID, exemptionID string,
) error {
	if err := s.requirePrivacyExemptionGovernance(ctx, tenantID, actorID); err != nil {
		return err
	}
	deleted, err := s.Store.DeleteMemoryPrivacyExemption(ctx, tenantID, exemptionID)
	if err != nil {
		return err
	}
	if !deleted {
		return httperr.NotFound("memory privacy exemption not found")
	}
	now := time.Now().UTC()
	return s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: actorID,
		Action: "memory_privacy.exemption_deleted", Resource: "memory-policy",
		ResourceID: exemptionID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{}),
		At:         now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "manage:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	})
}

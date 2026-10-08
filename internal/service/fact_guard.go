package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	factGuardModeStrict = "strict"
	factGuardModeRegen  = "regen"
	factGuardModeWarn   = "warn"
	factGuardModeOff    = "off"
	maxEvidenceFacts    = 100
)

type EvidenceFactInput struct {
	LogicalDocumentID string  `json:"logical_document_id"`
	DocumentVersionID string  `json:"document_version_id"`
	FactKey           string  `json:"fact_key" binding:"required"`
	Value             string  `json:"value" binding:"required"`
	Unit              string  `json:"unit"`
	TimeRange         string  `json:"time_range"`
	SourceChunkID     string  `json:"source_chunk_id"`
	SourceDocID       string  `json:"source_doc_id" binding:"required"`
	SourceSpan        string  `json:"source_span"`
	ClaimType         string  `json:"claim_type" binding:"required"`
	Confidence        float64 `json:"confidence"`
}

type RegisterEvidenceFactsInput struct {
	AnswerRunID  string              `json:"answer_run_id" binding:"required"`
	ResolverMode string              `json:"resolver_mode"`
	AsOf         *time.Time          `json:"as_of"`
	Facts        []EvidenceFactInput `json:"facts" binding:"required,min=1,max=100"`
}

type RoutedEvidenceFactsInput struct {
	ResolverMode string              `json:"resolver_mode"`
	AsOf         *time.Time          `json:"as_of"`
	Facts        []EvidenceFactInput `json:"facts" binding:"required,min=1,max=100"`
}

type EvidenceFactRegistrationResult struct {
	Facts     []model.FactRegistry     `json:"facts"`
	Conflicts []model.EvidenceConflict `json:"conflicts"`
}

type ClaimValidationSummary struct {
	ID               string    `json:"id"`
	AnswerRunID      string    `json:"answer_run_id"`
	PrimaryFactID    *string   `json:"primary_fact_id"`
	FactCount        int       `json:"fact_count"`
	ValidationStatus string    `json:"validation_status"`
	Action           string    `json:"action"`
	CreatedAt        time.Time `json:"created_at"`
}

type FactGuardOverviewSummary struct {
	FactCount        int            `json:"fact_count"`
	ConflictCount    int            `json:"conflict_count"`
	ClaimCount       int            `json:"claim_count"`
	ValidationStatus map[string]int `json:"validation_status"`
	Action           map[string]int `json:"action"`
}

type FactGuardOverview struct {
	AnswerRunID string                   `json:"answer_run_id"`
	Facts       []model.FactRegistry     `json:"facts"`
	Conflicts   []model.EvidenceConflict `json:"conflicts"`
	Claims      []ClaimValidationSummary `json:"claims"`
	Summary     FactGuardOverviewSummary `json:"summary"`
}

type EvidenceClaimInput struct {
	AnswerRunID string   `json:"answer_run_id" binding:"required"`
	LLMClaim    string   `json:"llm_claim" binding:"required"`
	FactIDs     []string `json:"fact_ids"`
	FactKey     string   `json:"fact_key"`
	Value       string   `json:"value"`
	Unit        string   `json:"unit"`
	TimeRange   string   `json:"time_range"`
}

func (s *Service) SetFactGuardConfig(cfg config.FactGuard) {
	s.factGuardMu.Lock()
	defer s.factGuardMu.Unlock()
	s.factGuardConfig = config.FactGuard{Mode: strings.ToLower(strings.TrimSpace(cfg.Mode))}
}

func (s *Service) CurrentFactGuardConfig() config.FactGuard {
	s.factGuardMu.RLock()
	defer s.factGuardMu.RUnlock()
	if s.factGuardConfig.Mode == "" {
		return config.FactGuard{Mode: factGuardModeWarn}
	}
	return s.factGuardConfig
}

func ValidateFactGuardConfig(environment, mode string) error {
	environment = strings.ToLower(strings.TrimSpace(environment))
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case factGuardModeStrict, factGuardModeRegen, factGuardModeWarn:
		return nil
	case factGuardModeOff:
		if environment == "production" {
			return errors.New("fact_guard.mode=off is not allowed when app.environment=production")
		}
		return nil
	default:
		return fmt.Errorf("fact_guard.mode must be one of strict, regen, warn, off")
	}
}

func (s *Service) RegisterEvidenceFacts(
	ctx context.Context, tenantID, userID string, input RegisterEvidenceFactsInput,
) (*EvidenceFactRegistrationResult, error) {
	answerRunID := strings.TrimSpace(input.AnswerRunID)
	if _, err := s.Store.GetAnswerRun(ctx, tenantID, answerRunID); err != nil {
		return nil, err
	}
	if len(input.Facts) == 0 {
		return nil, httperr.BadRequest(40089, "at least one evidence fact is required")
	}
	if len(input.Facts) > maxEvidenceFacts {
		return nil, httperr.BadRequest(40091, "at most 100 evidence facts can be registered per answer run")
	}
	resolverMode, err := normalizeFactResolverMode(input.ResolverMode)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	facts := make([]model.FactRegistry, 0, len(input.Facts))
	seen := map[string]struct{}{}
	for index := range input.Facts {
		factInput := &input.Facts[index]
		fact, buildErr := buildEvidenceFact(tenantID, answerRunID, factInput, now)
		if buildErr != nil {
			return nil, buildErr
		}
		key := evidenceFactSignature(fact)
		if _, exists := seen[key]; exists {
			return nil, httperr.New(409, 40998, "duplicate evidence fact signature")
		}
		seen[key] = struct{}{}
		facts = append(facts, *fact)
	}

	conflicts := make([]model.EvidenceConflict, 0)
	for left := 0; left < len(facts); left++ {
		for right := left + 1; right < len(facts); right++ {
			if !evidenceFactConflict(&facts[left], &facts[right]) {
				continue
			}
			conflictType := evidenceConflictType(&facts[left], &facts[right])
			resolution := model.EvidenceConflictUnresolved
			selectedFact := (*model.FactRegistry)(nil)
			if conflictType == model.FactConflictTemporal {
				resolved, selected, resolveErr := s.resolveTemporalFactConflict(
					ctx, tenantID, &facts[left], &facts[right], resolverMode, input.AsOf,
				)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if resolved {
					resolution = model.EvidenceConflictResolvedByVersion
					selectedFact = selected
				}
			}
			conflict, conflictErr := buildEvidenceConflict(
				tenantID, input.AnswerRunID, &facts[left], &facts[right], conflictType, resolution, resolverMode,
			)
			if conflictErr != nil {
				return nil, conflictErr
			}
			conflicts = append(conflicts, *conflict)
			applyEvidenceConflictStatus(facts, &conflicts[len(conflicts)-1], selectedFact)
		}
	}

	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		for index := range facts {
			if err := tx.CreateFactRegistry(ctx, &facts[index]); err != nil {
				return err
			}
		}
		for index := range conflicts {
			if err := tx.CreateEvidenceConflict(ctx, &conflicts[index]); err != nil {
				return err
			}
		}
		for index := range facts {
			if facts[index].ConflictStatus == model.FactConflictNone {
				continue
			}
			if err := tx.UpdateFactRegistry(ctx, &facts[index]); err != nil {
				return err
			}
		}
		detail := map[string]interface{}{
			"answer_run_id":  answerRunID,
			"fact_count":     len(facts),
			"conflict_count": len(conflicts),
			"resolver_mode":  resolverMode,
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
			UserID: userID, Action: "fact_guard.evidence_registered", Resource: "answer-run",
			ResourceID: answerRunID, DetailJSON: encodeAuditDetail(detail), At: now,
			Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err != nil {
		return nil, err
	}
	return &EvidenceFactRegistrationResult{Facts: facts, Conflicts: conflicts}, nil
}

func (s *Service) RegisterRoutedEvidenceFacts(
	ctx context.Context, tenantID, userID, answerRunID string, input RoutedEvidenceFactsInput,
) (*EvidenceFactRegistrationResult, error) {
	return s.RegisterEvidenceFacts(ctx, tenantID, userID, RegisterEvidenceFactsInput{
		AnswerRunID: answerRunID, ResolverMode: input.ResolverMode,
		AsOf: input.AsOf, Facts: input.Facts,
	})
}

func (s *Service) ValidateEvidenceClaim(
	ctx context.Context, tenantID, userID string, input EvidenceClaimInput,
) (*model.ClaimValidation, error) {
	if _, err := s.Store.GetAnswerRun(ctx, tenantID, strings.TrimSpace(input.AnswerRunID)); err != nil {
		return nil, err
	}
	claimValue := strings.TrimSpace(input.Value)
	if claimValue == "" {
		return nil, httperr.BadRequest(40090, "claim value is required")
	}
	now := time.Now().UTC()
	validation := &model.ClaimValidation{
		ID: id.New(), TenantID: tenantID, AnswerRunID: input.AnswerRunID,
		LLMClaim: strings.TrimSpace(input.LLMClaim), FactKey: strings.TrimSpace(input.FactKey),
		Value: claimValue, Unit: strings.TrimSpace(input.Unit), TimeRange: strings.TrimSpace(input.TimeRange),
		CreatedAt: now,
	}
	if len(input.FactIDs) > 0 {
		encoded, err := json.Marshal(input.FactIDs)
		if err != nil {
			return nil, err
		}
		validation.FactIDs = string(encoded)
	}
	facts, err := s.Store.ListFactRegistryByIDs(ctx, tenantID, input.AnswerRunID, input.FactIDs)
	if err != nil {
		return nil, err
	}
	if len(facts) != len(input.FactIDs) {
		return nil, httperr.New(409, 40999, "one or more evidence facts do not belong to the answer run")
	}
	if len(facts) == 0 {
		validation.ValidationStatus = model.ClaimValidationUnsupported
	} else if unresolved, err := s.hasUnresolvedEvidenceConflict(ctx, tenantID, input.AnswerRunID, facts); err != nil {
		return nil, err
	} else if unresolved {
		validation.ValidationStatus = model.ClaimValidationUnsupported
	} else {
		primary, status := validateClaimAgainstFacts(&input, facts)
		if primary != nil {
			primaryID := primary.ID
			validation.PrimaryFactID = &primaryID
		}
		validation.ValidationStatus = status
	}
	validation.Action = factGuardAction(s.CurrentFactGuardConfig().Mode, validation.ValidationStatus)
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateClaimValidation(ctx, validation); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
			UserID: userID, Action: "fact_guard.claim_validated", Resource: "answer-run",
			ResourceID: input.AnswerRunID,
			DetailJSON: encodeAuditDetail(map[string]interface{}{
				"claim_validation_id": validation.ID,
				"validation_status":   validation.ValidationStatus,
				"action":              validation.Action,
				"fact_count":          len(facts),
			}), At: now,
			Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err != nil {
		return nil, err
	}
	return validation, nil
}

func (s *Service) ListEvidenceFacts(ctx context.Context, tenantID, answerRunID string) ([]model.FactRegistry, []model.EvidenceConflict, error) {
	if _, err := s.Store.GetAnswerRun(ctx, tenantID, answerRunID); err != nil {
		return nil, nil, err
	}
	conflicts, err := s.Store.ListEvidenceConflicts(ctx, tenantID, answerRunID)
	if err != nil {
		return nil, nil, err
	}
	facts, err := s.Store.ListFactRegistry(ctx, tenantID, answerRunID)
	return facts, conflicts, err
}

func (s *Service) GetRoutedFactGuardOverview(
	ctx context.Context, tenantID, answerRunID string,
) (*FactGuardOverview, error) {
	facts, conflicts, err := s.ListEvidenceFacts(ctx, tenantID, answerRunID)
	if err != nil {
		return nil, err
	}
	validations, err := s.Store.ListClaimValidations(ctx, tenantID, answerRunID)
	if err != nil {
		return nil, err
	}
	claims := make([]ClaimValidationSummary, 0, len(validations))
	statusCounts := map[string]int{}
	actionCounts := map[string]int{}
	for index := range validations {
		validation := &validations[index]
		var factIDs []string
		if validation.FactIDs != "" {
			if err := json.Unmarshal([]byte(validation.FactIDs), &factIDs); err != nil {
				return nil, httperr.Internal("claim validation evidence reference is invalid")
			}
		}
		claims = append(claims, ClaimValidationSummary{
			ID: validation.ID, AnswerRunID: validation.AnswerRunID,
			PrimaryFactID: validation.PrimaryFactID, FactCount: len(factIDs),
			ValidationStatus: validation.ValidationStatus, Action: validation.Action,
			CreatedAt: validation.CreatedAt,
		})
		statusCounts[validation.ValidationStatus]++
		actionCounts[validation.Action]++
	}
	return &FactGuardOverview{
		AnswerRunID: answerRunID, Facts: facts, Conflicts: conflicts, Claims: claims,
		Summary: FactGuardOverviewSummary{
			FactCount: len(facts), ConflictCount: len(conflicts), ClaimCount: len(claims),
			ValidationStatus: statusCounts, Action: actionCounts,
		},
	}, nil
}

func buildEvidenceFact(tenantID, answerRunID string, input *EvidenceFactInput, now time.Time) (*model.FactRegistry, error) {
	factKey := strings.TrimSpace(input.FactKey)
	if factKey == "" {
		return nil, httperr.BadRequest(40092, "fact_key is required")
	}
	value := strings.TrimSpace(input.Value)
	if value == "" {
		return nil, httperr.BadRequest(40093, "evidence fact value is required")
	}
	sourceDocID := strings.TrimSpace(input.SourceDocID)
	if sourceDocID == "" {
		return nil, httperr.BadRequest(40094, "source_doc_id is required")
	}
	claimType := strings.TrimSpace(input.ClaimType)
	if claimType != model.FactClaimTypeExtracted && claimType != model.FactClaimTypeComputed {
		return nil, httperr.BadRequest(40095, "claim_type must be extracted or computed")
	}
	if input.Confidence < 0 || input.Confidence > 1 {
		return nil, httperr.BadRequest(40096, "confidence must be between 0 and 1")
	}
	if len(value) > 512 || len(factKey) > 128 || len(strings.TrimSpace(input.Unit)) > 32 ||
		len(strings.TrimSpace(input.TimeRange)) > 128 || len(sourceDocID) > 64 ||
		len(strings.TrimSpace(input.SourceChunkID)) > 64 || len(strings.TrimSpace(input.SourceSpan)) > 256 ||
		len(strings.TrimSpace(input.LogicalDocumentID)) > 32 || len(strings.TrimSpace(input.DocumentVersionID)) > 32 {
		return nil, httperr.BadRequest(40097, "evidence fact field exceeds its maximum length")
	}
	return &model.FactRegistry{
		ID: id.New(), TenantID: tenantID, AnswerRunID: answerRunID,
		LogicalDocumentID: strings.TrimSpace(input.LogicalDocumentID),
		DocumentVersionID: strings.TrimSpace(input.DocumentVersionID),
		FactKey:           factKey, Value: value, Unit: strings.TrimSpace(input.Unit),
		TimeRange: strings.TrimSpace(input.TimeRange), SourceChunkID: strings.TrimSpace(input.SourceChunkID),
		SourceDocID: sourceDocID, SourceSpan: strings.TrimSpace(input.SourceSpan),
		ClaimType: claimType, ConflictStatus: model.FactConflictNone,
		Confidence: input.Confidence, CreatedAt: now,
	}, nil
}

func normalizeFactResolverMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "":
		return "current", nil
	case "current", "as_of", "exact":
		return mode, nil
	default:
		return "", httperr.BadRequest(40098, "resolver_mode must be current, as_of, or exact")
	}
}

func evidenceFactSignature(fact *model.FactRegistry) string {
	signature := map[string]string{
		"fact_key":   strings.ToLower(fact.FactKey),
		"unit":       strings.ToLower(strings.TrimSpace(fact.Unit)),
		"time_range": strings.ToLower(strings.TrimSpace(fact.TimeRange)),
		"value":      normalizeFactValue(fact.Value),
	}
	encoded, _ := json.Marshal(signature)
	return string(encoded)
}

func normalizeFactValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	normalizedNumber := strings.ReplaceAll(value, ",", "")
	if number, err := strconv.ParseFloat(normalizedNumber, 64); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return strings.Join(strings.Fields(value), " ")
}

func evidenceFactConflict(left, right *model.FactRegistry) bool {
	return strings.EqualFold(left.FactKey, right.FactKey) &&
		strings.EqualFold(left.Unit, right.Unit) &&
		strings.EqualFold(left.TimeRange, right.TimeRange) &&
		normalizeFactValue(left.Value) != normalizeFactValue(right.Value)
}

func evidenceConflictType(left, right *model.FactRegistry) string {
	leftLogical, rightLogical := strings.TrimSpace(left.LogicalDocumentID), strings.TrimSpace(right.LogicalDocumentID)
	leftVersion, rightVersion := strings.TrimSpace(left.DocumentVersionID), strings.TrimSpace(right.DocumentVersionID)
	if leftLogical != "" && leftLogical == rightLogical && leftVersion != "" && rightVersion != "" && leftVersion != rightVersion {
		return model.FactConflictTemporal
	}
	if strings.TrimSpace(left.SourceDocID) != strings.TrimSpace(right.SourceDocID) {
		return model.FactConflictSource
	}
	return model.FactConflictValue
}

func (s *Service) resolveTemporalFactConflict(
	ctx context.Context, tenantID string, left, right *model.FactRegistry,
	resolverMode string, asOf *time.Time,
) (bool, *model.FactRegistry, error) {
	if resolverMode != "current" && resolverMode != "as_of" {
		return false, nil, nil
	}
	leftVersion, err := s.Store.GetDocumentVersion(ctx, tenantID, left.DocumentVersionID)
	if err != nil {
		return false, nil, err
	}
	rightVersion, err := s.Store.GetDocumentVersion(ctx, tenantID, right.DocumentVersionID)
	if err != nil {
		return false, nil, err
	}
	if leftVersion == nil || rightVersion == nil ||
		leftVersion.TenantID != tenantID || rightVersion.TenantID != tenantID ||
		leftVersion.LogicalDocumentID != left.LogicalDocumentID ||
		rightVersion.LogicalDocumentID != right.LogicalDocumentID {
		return false, nil, nil
	}
	if resolverMode == "current" {
		leftActive := leftVersion.Status == model.DocumentVersionActive
		rightActive := rightVersion.Status == model.DocumentVersionActive
		if leftActive == rightActive {
			return false, nil, nil
		}
		if leftActive {
			return true, left, nil
		}
		return true, right, nil
	}
	if asOf == nil {
		return false, nil, nil
	}
	leftMatches := effectiveRangeContains(leftVersion, *asOf)
	rightMatches := effectiveRangeContains(rightVersion, *asOf)
	if leftMatches == rightMatches {
		return false, nil, nil
	}
	if leftMatches {
		return true, left, nil
	}
	return true, right, nil
}

func effectiveRangeContains(version *model.DocumentVersion, at time.Time) bool {
	if at.Before(version.EffectiveFrom) {
		return false
	}
	return version.EffectiveTo == nil || at.Before(*version.EffectiveTo)
}

func buildEvidenceConflict(
	tenantID, answerRunID string, left, right *model.FactRegistry,
	conflictType, resolution, resolverMode string,
) (*model.EvidenceConflict, error) {
	evidenceRefs, err := json.Marshal([]string{left.ID, right.ID})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &model.EvidenceConflict{
		ID: id.New(), TenantID: tenantID, AnswerRunID: answerRunID,
		LeftFactID: left.ID, RightFactID: right.ID, ConflictType: conflictType,
		Resolution: resolution, ResolverMode: resolverMode,
		EvidenceRefs: string(evidenceRefs), CreatedAt: now,
	}, nil
}

func applyEvidenceConflictStatus(
	facts []model.FactRegistry, conflict *model.EvidenceConflict, selectedFact *model.FactRegistry,
) {
	for index := range facts {
		if facts[index].ID != conflict.LeftFactID && facts[index].ID != conflict.RightFactID {
			continue
		}
		if conflict.Resolution == model.EvidenceConflictResolvedByVersion &&
			selectedFact != nil && facts[index].ID == selectedFact.ID {
			facts[index].ConflictStatus = model.FactConflictNone
			continue
		}
		facts[index].ConflictStatus = conflict.ConflictType
	}
}

func (s *Service) hasUnresolvedEvidenceConflict(
	ctx context.Context, tenantID, answerRunID string, facts []model.FactRegistry,
) (bool, error) {
	conflicts, err := s.Store.ListEvidenceConflicts(ctx, tenantID, answerRunID)
	if err != nil {
		return false, err
	}
	factIDs := make(map[string]struct{}, len(facts))
	for index := range facts {
		factIDs[facts[index].ID] = struct{}{}
	}
	for index := range conflicts {
		if conflicts[index].Resolution != model.EvidenceConflictUnresolved {
			continue
		}
		_, left := factIDs[conflicts[index].LeftFactID]
		_, right := factIDs[conflicts[index].RightFactID]
		if left || right {
			return true, nil
		}
	}
	return false, nil
}

func validateClaimAgainstFacts(input *EvidenceClaimInput, facts []model.FactRegistry) (*model.FactRegistry, string) {
	factKey := strings.TrimSpace(input.FactKey)
	unit := strings.TrimSpace(input.Unit)
	timeRange := strings.TrimSpace(input.TimeRange)
	claimValue := strings.TrimSpace(input.Value)
	var primary *model.FactRegistry
	for index := range facts {
		fact := &facts[index]
		if factKey != "" && !strings.EqualFold(fact.FactKey, factKey) {
			continue
		}
		if unit != "" && !strings.EqualFold(fact.Unit, unit) {
			continue
		}
		if timeRange != "" && !strings.EqualFold(fact.TimeRange, timeRange) {
			continue
		}
		if normalizeFactValue(fact.Value) != normalizeFactValue(claimValue) {
			continue
		}
		primary = fact
		break
	}
	if primary != nil {
		return primary, model.ClaimValidationSupported
	}
	if len(facts) == 0 {
		return nil, model.ClaimValidationUnsupported
	}
	if factKey == "" {
		return nil, model.ClaimValidationContradicted
	}
	for index := range facts {
		if strings.EqualFold(facts[index].FactKey, factKey) {
			return &facts[index], model.ClaimValidationContradicted
		}
	}
	return nil, model.ClaimValidationUnsupported
}

func factGuardAction(mode, validationStatus string) string {
	switch mode {
	case factGuardModeStrict:
		if validationStatus == model.ClaimValidationSupported {
			return model.ClaimActionKeep
		}
		return model.ClaimActionBlock
	case factGuardModeRegen:
		if validationStatus == model.ClaimValidationContradicted {
			return model.ClaimActionRegenerate
		}
		if validationStatus == model.ClaimValidationUnsupported {
			return model.ClaimActionFlag
		}
		return model.ClaimActionKeep
	case factGuardModeOff:
		return model.ClaimActionKeep
	default:
		if validationStatus == model.ClaimValidationSupported {
			return model.ClaimActionKeep
		}
		return model.ClaimActionFlag
	}
}

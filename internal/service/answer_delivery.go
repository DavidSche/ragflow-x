package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"gorm.io/gorm"
)

type AnswerDeliveryInput struct {
	TenantID           string
	ProjectID          string
	SessionID          string
	AssistantID        string
	AssistantReleaseID string
	PrincipalID        string
	Question           string
	Model              string
	RequestID          string
	TraceID            string
	Channel            string
	AnswerStatus       string
	CompletionReason   string
	LifecycleState     string
	ReasonCode         string
	UserMessage        string
	AdminReason        string
	Summary            string
	Content            string
	Citations          []model.AnswerCitation
	Artifacts          []model.AnswerArtifact
	Execution          []map[string]interface{}
	Limitations        []string
	Actions            []map[string]interface{}
	// Pushdown carries the retrieval-layer metadata_condition evidence
	// (doc/123 §7); nil means the scenario has no pushdown channel.
	Pushdown *pushdownEvidence
}

type AnswerDeliveryResult struct {
	Run              *model.AnswerRun
	Snapshot         *model.AnswerSnapshot
	Projection       *model.AuthorizationProjection
	AnswerRunID      string
	AnswerSnapshotID string
}

const (
	ExportScopeAnswer       = "answer"
	ExportScopeConversation = "conversation"
)

type ExportRequest struct {
	AnswerSnapshotID string
	Format           string
	Scope            string
	IdempotencyKey   string
	TraceID          string
}

type ExportDownload struct {
	Artifact *model.ExportArtifact
	Path     string
}

func validAnswerStatus(status string) bool {
	switch status {
	case model.AnswerStatusAnswered, model.AnswerStatusPartial, model.AnswerStatusNoAnswer,
		model.AnswerStatusInsufficientEvidence, model.AnswerStatusPermissionLimited,
		model.AnswerStatusToolFailed, model.AnswerStatusSystemFailed:
		return true
	default:
		return false
	}
}

func validCompletionReason(reason string) bool {
	switch reason {
	case model.CompletionReasonNormal, model.CompletionReasonUserCancelled,
		model.CompletionReasonTimeout, model.CompletionReasonToolFailed,
		model.CompletionReasonSystemFailed:
		return true
	default:
		return false
	}
}

func validAnswerLifecycle(lifecycle string) bool {
	switch lifecycle {
	case model.AnswerLifecycleCompleted, model.AnswerLifecycleCancelled, model.AnswerLifecycleFailed:
		return true
	default:
		return false
	}
}

func normalizedAnswerCitations(input []model.AnswerCitation) []model.AnswerCitation {
	result := make([]model.AnswerCitation, 0, len(input))
	for index, citation := range input {
		normalized := model.AnswerCitation{
			ID:                  strings.TrimSpace(citation.ID),
			Title:               strings.TrimSpace(citation.Title),
			DatasetID:           strings.TrimSpace(citation.DatasetID),
			DocumentID:          strings.TrimSpace(citation.DocumentID),
			DocumentVersion:     strings.TrimSpace(citation.DocumentVersion),
			ChunkID:             strings.TrimSpace(citation.ChunkID),
			CitedContentExcerpt: truncateKnowledgeText(citation.CitedContentExcerpt, 1024),
			CitationLocator:     strings.TrimSpace(citation.CitationLocator),
			CitationContentHash: strings.TrimSpace(citation.CitationContentHash),
		}
		if normalized.ID == "" {
			normalized.ID = fmt.Sprintf("citation-%d", index+1)
		}
		if normalized.CitationContentHash == "" && normalized.CitedContentExcerpt != "" {
			normalized.CitationContentHash = sha256Hex(normalized.CitedContentExcerpt)
		}
		result = append(result, normalized)
	}
	return result
}

func citationsFromProviderReferences(values []map[string]interface{}) []model.AnswerCitation {
	citations := make([]model.AnswerCitation, 0, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		content, _ := value["content"].(string)
		if content == "" {
			content, _ = value["text"].(string)
		}
		title := stringFromAny(value["document_name"])
		if title == "" {
			title = stringFromAny(value["doc_name"])
		}
		if title == "" {
			title = stringFromAny(value["title"])
		}
		citation := model.AnswerCitation{
			ID:                  stringFromAny(value["id"]),
			Title:               title,
			DatasetID:           stringFromAny(value["dataset_id"]),
			DocumentID:          stringFromAny(value["document_id"]),
			DocumentVersion:     stringFromAny(value["document_version"]),
			ChunkID:             stringFromAny(value["chunk_id"]),
			CitedContentExcerpt: content,
			CitationLocator:     stringFromAny(value["location"]),
		}
		if citation.ID == "" {
			citation.ID = fmt.Sprintf("citation-%d", index+1)
		}
		if citation.DatasetID == "" {
			citation.DatasetID = stringFromAny(value["datasetId"])
		}
		citations = append(citations, citation)
	}
	return citations
}

func mustJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func answerLifecycleFromStatus(status string) string {
	switch status {
	case model.AnswerStatusSystemFailed:
		return model.AnswerLifecycleFailed
	case model.AnswerStatusToolFailed:
		return model.AnswerLifecycleFailed
	default:
		return model.AnswerLifecycleCompleted
	}
}

func (s *Service) FinalizeAnswerDelivery(ctx context.Context, input AnswerDeliveryInput) (*AnswerDeliveryResult, error) {
	if input.TenantID == "" || input.SessionID == "" || input.AssistantID == "" || input.RequestID == "" {
		return nil, httperr.BadRequest(40120, "tenant, session, assistant and request are required")
	}
	if !validAnswerStatus(input.AnswerStatus) {
		return nil, httperr.BadRequest(40121, "invalid answer status")
	}
	if !validCompletionReason(input.CompletionReason) {
		return nil, httperr.BadRequest(40122, "invalid completion reason")
	}
	if input.LifecycleState == "" {
		input.LifecycleState = answerLifecycleFromStatus(input.AnswerStatus)
	}
	if !validAnswerLifecycle(input.LifecycleState) {
		return nil, httperr.BadRequest(40123, "invalid answer lifecycle")
	}
	if input.ReasonCode == "" {
		input.ReasonCode = input.CompletionReason
	}
	citations := normalizedAnswerCitations(input.Citations)
	now := time.Now().UTC()
	run := &model.AnswerRun{
		TenantID: input.TenantID, ProjectID: input.ProjectID, SessionID: input.SessionID,
		AssistantID: input.AssistantID, AssistantReleaseID: input.AssistantReleaseID,
		QuestionRef: sha256Hex(strings.Join(strings.Fields(strings.ToLower(input.Question)), " ")),
		Model:       input.Model, TraceID: input.TraceID, RequestID: input.RequestID,
		LifecycleState: input.LifecycleState, AnswerStatus: input.AnswerStatus,
		CompletionReason: input.CompletionReason, CreatedAt: now, CompletedAt: &now,
	}
	if err := s.Store.CreateAnswerRun(ctx, run); err != nil {
		return nil, err
	}
	citationsJSON, artifactsJSON, executionJSON, limitationsJSON, actionsJSON, err := answerJSONFields(input, citations)
	if err != nil {
		return nil, err
	}
	canonicalPayload := map[string]interface{}{
		"answer_schema_version": model.AnswerSchemaVersion,
		"answer_status":         input.AnswerStatus,
		"completion_reason":     input.CompletionReason,
		"reason_code":           input.ReasonCode,
		"summary":               input.Summary,
		"content":               input.Content,
		"citations":             citations,
		"artifacts":             input.Artifacts,
		"execution":             input.Execution,
		"limitations":           input.Limitations,
		"actions":               input.Actions,
	}
	canonicalData, err := json.Marshal(canonicalPayload)
	if err != nil {
		return nil, err
	}
	snapshot := &model.AnswerSnapshot{
		TenantID: input.TenantID, ProjectID: input.ProjectID, AnswerRunID: run.ID,
		AnswerSchemaVersion: model.AnswerSchemaVersion, LifecycleState: input.LifecycleState,
		AnswerStatus: input.AnswerStatus, CompletionReason: input.CompletionReason,
		ReasonCode: input.ReasonCode, UserMessage: input.UserMessage,
		AdminReason: input.AdminReason, Summary: input.Summary, Content: input.Content,
		CitationsJSON: citationsJSON, ArtifactsJSON: artifactsJSON,
		ExecutionJSON: executionJSON, LimitationsJSON: limitationsJSON,
		ActionsJSON: actionsJSON, CanonicalHash: sha256Hex(string(canonicalData)),
		HashAlgorithm: model.AnswerHashAlgorithm, CreatedAt: now, CompletedAt: &now,
	}
	if err := s.Store.CreateAnswerSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	eventPayload, err := mustJSON(map[string]interface{}{
		"answer_status":      input.AnswerStatus,
		"completion_reason":  input.CompletionReason,
		"reason_code":        input.ReasonCode,
		"answer_snapshot_id": snapshot.ID,
		"canonical_hash":     snapshot.CanonicalHash,
	})
	if err != nil {
		return nil, err
	}
	event := &model.AnswerEvent{
		TenantID: input.TenantID, ProjectID: input.ProjectID, AnswerRunID: run.ID,
		EventID: "status-" + input.RequestID, EventSeq: 1, ResourceType: "answer",
		ResourceSeq: snapshot.ID, EventType: model.AnswerEventTypeStatus, Status: input.AnswerStatus,
		PayloadJSON: eventPayload, OccurredAt: now,
	}
	if err := s.Store.CreateAnswerEvent(ctx, event); err != nil {
		return nil, err
	}
	projection, err := s.createDefaultAuthorizationProjection(
		ctx, input.TenantID, input.ProjectID, snapshot.ID, input.PrincipalID, input.Channel, snapshot.CitationsJSON, input.Pushdown,
	)
	if err != nil {
		return nil, err
	}
	return &AnswerDeliveryResult{
		Run: run, Snapshot: snapshot, Projection: projection,
		AnswerRunID: run.ID, AnswerSnapshotID: snapshot.ID,
	}, nil
}

func answerJSONFields(input AnswerDeliveryInput, citations []model.AnswerCitation) (citationsJSON, artifactsJSON, executionJSON, limitationsJSON, actionsJSON string, err error) {
	if citationsJSON, err = mustJSON(citations); err != nil {
		return
	}
	if artifactsJSON, err = mustJSON(input.Artifacts); err != nil {
		return
	}
	if executionJSON, err = mustJSON(input.Execution); err != nil {
		return
	}
	if limitationsJSON, err = mustJSON(input.Limitations); err != nil {
		return
	}
	if actionsJSON, err = mustJSON(input.Actions); err != nil {
		return
	}
	return
}

func normalizeAnswerChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "api", "wecom", "web", "agent", "search", "chat":
		return strings.ToLower(strings.TrimSpace(channel))
	default:
		return "web"
	}
}

// citationRedactionReasons derives the minimal policy decision (doc/118 F-06):
// when any cited dataset carries a restricted/confidential sensitivity level,
// citations are redacted and the offending sensitivity levels are recorded as
// decision reasons. Unknown sensitivity levels fail closed (REDACTED). It
// returns the citation visibility and the redaction reasons.
func citationRedactionReasons(citationsJSON string, datasetSensitivity func(datasetID string) (string, error)) (string, []string) {
	var citations []model.AnswerCitation
	if err := json.Unmarshal([]byte(citationsJSON), &citations); err != nil || len(citations) == 0 {
		// Unparseable citation payloads fail closed as well.
		if err != nil {
			return model.AuthorizationVisibilityRedacted, []string{"sensitivity:citations_unparseable"}
		}
		return model.AuthorizationVisibilityVisible, nil
	}
	reasons := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, citation := range citations {
		if citation.DatasetID == "" {
			continue
		}
		level, err := datasetSensitivity(citation.DatasetID)
		if err != nil {
			if !seen["sensitivity:lookup_failed"] {
				reasons = append(reasons, "sensitivity:lookup_failed")
				seen["sensitivity:lookup_failed"] = true
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(level)) {
		case "", "public", "internal":
			// Non-sensitive or unset: stay visible.
		case "restricted", "confidential":
			reason := "sensitivity:" + strings.ToLower(strings.TrimSpace(level))
			if !seen[reason] {
				reasons = append(reasons, reason)
				seen[reason] = true
			}
		default:
			// Unknown sensitivity level: fail closed.
			reason := "sensitivity:unknown_level"
			if !seen[reason] {
				reasons = append(reasons, reason)
				seen[reason] = true
			}
		}
	}
	if len(reasons) > 0 {
		return model.AuthorizationVisibilityRedacted, reasons
	}
	return model.AuthorizationVisibilityVisible, nil
}

func (s *Service) createDefaultAuthorizationProjection(
	ctx context.Context, tenantID, projectID, snapshotID, principalID, channel, citationsJSON string, pushdown *pushdownEvidence,
) (*model.AuthorizationProjection, error) {
	return s.buildAuthorizationProjection(ctx, tenantID, projectID, snapshotID, principalID, channel, citationsJSON, pushdown)
}

func (s *Service) buildAuthorizationProjection(
	ctx context.Context, tenantID, projectID, snapshotID, principalID, channel, citationsJSON string, pushdown *pushdownEvidence,
) (*model.AuthorizationProjection, error) {
	channel = normalizeAnswerChannel(channel)
	now := time.Now().UTC()
	citationVisibility := model.AuthorizationVisibilityVisible
	redactionReasons := []string{}
	if citationsJSON != "" {
		visibility, reasons := citationRedactionReasons(citationsJSON, func(datasetID string) (string, error) {
			dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return "internal", nil
				}
				return "", err
			}
			if dataset == nil {
				return "internal", nil
			}
			return dataset.Sensitivity, nil
		})
		citationVisibility = visibility
		redactionReasons = reasons
	}
	policyInput := map[string]string{
		"tenant_id": tenantID, "project_id": projectID,
		"principal_id": principalID, "channel": channel, "policy_version": model.AnswerPolicyVersion,
	}
	// Retrieval pushdown evidence enters the policy input hash so audit
	// recomputation covers the retrieval layer (doc/123 §7).
	pushdownPolicyInputHashInputs(policyInput, pushdown)
	policyData, err := json.Marshal(policyInput)
	if err != nil {
		return nil, err
	}
	decision := map[string]interface{}{
		"answer": model.AuthorizationVisibilityVisible, "content": model.AuthorizationVisibilityVisible,
		"citation": citationVisibility, "artifact": model.AuthorizationVisibilityVisible,
		"execution": model.AuthorizationVisibilityVisible, "metadata": model.AuthorizationVisibilityVisible,
		"actions": model.AuthorizationVisibilityVisible,
	}
	decisionData, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	redactionData, err := json.Marshal(redactionReasons)
	if err != nil {
		return nil, err
	}
	projection := &model.AuthorizationProjection{
		TenantID: tenantID, ProjectID: projectID, AnswerSnapshotID: snapshotID,
		PrincipalID: principalID, Channel: channel, PolicyVersion: model.AnswerPolicyVersion,
		PolicyEvaluatedAt: now, PolicyInputHash: sha256Hex(string(policyData)),
		DecisionHash:     sha256Hex(string(decisionData)),
		AnswerVisibility: model.AuthorizationVisibilityVisible, ContentVisibility: model.AuthorizationVisibilityVisible,
		CitationVisibility: citationVisibility, ArtifactVisibility: model.AuthorizationVisibilityVisible,
		ExecutionVisibility: model.AuthorizationVisibilityVisible, MetadataVisibility: model.AuthorizationVisibilityVisible,
		ActionsVisibility:     model.AuthorizationVisibilityVisible,
		RedactionReasonsJSON:  string(redactionData),
		RetrievalPushdownJSON: pushdownEvidenceJSON(pushdown),
		CreatedAt:             now,
	}
	if err := s.Store.CreateAuthorizationProjection(ctx, projection); err != nil {
		return nil, err
	}
	return projection, nil
}

func (s *Service) GetAnswerSnapshot(ctx context.Context, actorID, tenantID, snapshotID, channel string) (*model.AnswerSnapshot, *model.AuthorizationProjection, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, nil, err
	}
	snapshot, err := s.Store.GetAnswerSnapshot(ctx, tenantID, snapshotID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, nil, err
	}
	projection, err := s.ensureAuthorizationProjection(ctx, actorID, tenantID, snapshot.ID, actorID, channel)
	if err != nil {
		return nil, nil, err
	}
	return snapshot, projection, nil
}

func (s *Service) GetAnswerSnapshotByRequest(ctx context.Context, actorID, tenantID, requestID, channel string) (*model.AnswerSnapshot, *model.AuthorizationProjection, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "chat", tenantID, ""); err != nil {
		return nil, nil, err
	}
	snapshot, err := s.Store.GetAnswerSnapshotByRequest(ctx, tenantID, requestID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureAnswerSchemaSupported(snapshot.AnswerSchemaVersion); err != nil {
		return nil, nil, err
	}
	projection, err := s.ensureAuthorizationProjection(ctx, actorID, tenantID, snapshot.ID, actorID, channel)
	if err != nil {
		return nil, nil, err
	}
	return snapshot, projection, nil
}

func ensureAnswerSchemaSupported(version string) error {
	if version == model.AnswerSchemaVersion {
		return nil
	}
	return httperr.New(400, 40134, "unsupported answer schema version")
}

func (s *Service) ensureAuthorizationProjection(
	ctx context.Context, actorID, tenantID, snapshotID, principalID, channel string,
) (*model.AuthorizationProjection, error) {
	projection, err := s.Store.GetAuthorizationProjection(ctx, tenantID, snapshotID, principalID, normalizeAnswerChannel(channel))
	if err == nil {
		return projection, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	snapshot, err := s.Store.GetAnswerSnapshot(ctx, tenantID, snapshotID)
	if err != nil {
		return nil, err
	}
	return s.createDefaultAuthorizationProjection(
		ctx, tenantID, snapshot.ProjectID, snapshotID, principalID, channel, snapshot.CitationsJSON, nil,
	)
}

func (s *Service) recordAnswerDelivery(ctx context.Context, tenantID, principalID, appType, assistantID, sessionID, requestID, question, answer string, citations []model.AnswerCitation) {
	s.recordAnswerDeliveryWithPushdown(ctx, tenantID, principalID, appType, assistantID, sessionID, requestID, question, answer, citations, nil)
}

// recordAnswerDeliveryWithPushdown records answer delivery with the
// retrieval-layer pushdown evidence for the projection (doc/123 §8.4).
func (s *Service) recordAnswerDeliveryWithPushdown(ctx context.Context, tenantID, principalID, appType, assistantID, sessionID, requestID, question, answer string, citations []model.AnswerCitation, pushdown *pushdownEvidence) {
	answer = visibleRAGFlowAnswer(answer)
	if answer == "" {
		return
	}
	if sessionID == "" {
		sessionID = "request-" + requestID
	}
	status := model.AnswerStatusAnswered
	if len(citations) == 0 {
		status = model.AnswerStatusInsufficientEvidence
	}
	input := AnswerDeliveryInput{
		TenantID: tenantID, ProjectID: "", SessionID: sessionID,
		AssistantID: assistantID, PrincipalID: principalID, Question: question,
		Model: appType + "-" + assistantID, RequestID: requestID,
		AnswerStatus: status, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "ANSWER_DELIVERED", UserMessage: question, Summary: truncateKnowledgeText(answer, 256),
		Content: answer, Citations: citations,
		Limitations: []string{}, Actions: []map[string]interface{}{},
	}
	if status == model.AnswerStatusInsufficientEvidence {
		input.ReasonCode = "NO_CITATION_EVIDENCE"
		input.AdminReason = "RAGFlow returned content without citation references"
	}
	input.Pushdown = pushdown
	if _, err := s.FinalizeAnswerDelivery(ctx, input); err != nil {
		s.answerDeliveryFailed(requestID, err)
	}
}

func (s *Service) recordFailedAnswerDelivery(ctx context.Context, tenantID, principalID, appType, assistantID, sessionID, requestID, question, answer, reason string) {
	answer = visibleRAGFlowAnswer(answer)
	if sessionID == "" {
		sessionID = "request-" + requestID
	}
	status := model.AnswerStatusSystemFailed
	completionReason := model.CompletionReasonSystemFailed
	reasonCode := "PROVIDER_FAILED"
	if ctx.Err() != nil {
		status = model.AnswerStatusPartial
		completionReason = model.CompletionReasonUserCancelled
		reasonCode = "USER_REQUEST_STOPPED"
	}
	_, err := s.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenantID, SessionID: sessionID, AssistantID: assistantID,
		PrincipalID: principalID, Question: question, Model: appType + "-" + assistantID,
		RequestID: requestID, AnswerStatus: status,
		CompletionReason: completionReason,
		ReasonCode:       reasonCode, AdminReason: reason, UserMessage: question, Content: answer,
		Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		s.answerDeliveryFailed(requestID, err)
	}
}

func (s *Service) answerDeliveryFailed(requestID string, err error) {
	logger.Warn("answer delivery snapshot write failed", "request_id", requestID, "error", err)
}

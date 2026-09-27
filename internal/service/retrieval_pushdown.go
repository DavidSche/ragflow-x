package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Metadata keys X syncs into RAGFlow document meta_fields (doc/123 §3).
// The rgx_ prefix avoids collisions with engine-generated metadata.
const (
	metadataKeySensitivity   = "rgx_sensitivity"
	metadataKeyBusinessDoain = "rgx_business_domain"

	// Metadata comparison operators. Both are documented across the
	// retrieval/metadata_condition families; exact engine spelling of the
	// not-equal operator is confirmed by live drill (doc/123 §10.3).
	operatorEqual    = "="
	operatorNotEqual = "!="

	// exclusionConditionLimit is the maximum number of distinct sensitive
	// levels handled with per-level exclusion conditions before switching to
	// the whitelist form (doc/123 §3.3). Three or more exclusions would make
	// every request carry a long AND chain; the OR whitelist stays compact.
	exclusionConditionLimit = 2

	// metadataBatchSize bounds each POST /metadata/update payload.
	metadataBatchSize = 100
)

// normalizeSensitivity maps a governance sensitivity value onto the
// fail-closed scale shared with citationRedactionReasons (doc/118 F-06):
// unknown levels are treated as sensitive.
func normalizeSensitivity(level string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "public", "internal":
		return "internal", false
	case "restricted", "confidential":
		return strings.ToLower(strings.TrimSpace(level)), true
	default:
		return strings.ToLower(strings.TrimSpace(level)), true
	}
}

// buildPushdownCondition derives the retrieval-layer metadata_condition for
// the given X-side dataset ids (doc/123 §3.3). It returns nil (no condition)
// when every dataset is visible. Unknown or missing datasets fail closed:
// they are treated as sensitive.
func (s *Service) buildPushdownCondition(ctx context.Context, tenantID string, datasetIDs []string) (*ragflow.MetadataCondition, error) {
	sensitive := map[string]bool{}
	levels := make([]string, 0, len(datasetIDs))
	visibleOnly := true
	for _, id := range datasetIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		link, err := s.Store.GetDatasetLink(ctx, tenantID, id)
		if err != nil {
			return nil, fmt.Errorf("pushdown dataset lookup: %w", err)
		}
		if link == nil {
			// Missing governance record: fail closed.
			if _, ok := sensitive["unknown"]; !ok {
				sensitive["unknown"] = true
				levels = append(levels, "unknown")
			}
			visibleOnly = false
			continue
		}
		if !link.PushdownEnabled {
			continue
		}
		_, isSensitive := normalizeSensitivity(link.Sensitivity)
		if isSensitive {
			level, _ := normalizeSensitivity(link.Sensitivity)
			if !sensitive[level] {
				sensitive[level] = true
				levels = append(levels, level)
			}
			visibleOnly = false
		}
	}
	if visibleOnly {
		return nil, nil
	}
	sort.Strings(levels)
	if len(levels) == 0 {
		return nil, nil
	}
	if len(levels) <= exclusionConditionLimit {
		// Exclusion form: and(not level1, not level2, ...).
		conditions := make([]ragflow.MetadataConditionOp, 0, len(levels))
		for _, level := range levels {
			conditions = append(conditions, ragflow.MetadataConditionOp{
				Name: metadataKeySensitivity, ComparisonOperator: operatorNotEqual, Value: level,
			})
		}
		return &ragflow.MetadataCondition{Logic: "and", Conditions: conditions}, nil
	}
	// Whitelist form: or(visible levels).
	conditions := make([]ragflow.MetadataConditionOp, 0, 2)
	for _, level := range []string{"internal", "public"} {
		conditions = append(conditions, ragflow.MetadataConditionOp{
			Name: metadataKeySensitivity, ComparisonOperator: operatorEqual, Value: level,
		})
	}
	return &ragflow.MetadataCondition{Logic: "or", Conditions: conditions}, nil
}

// pushdownEvidence summarizes the outcome for the AuthorizationProjection.
type pushdownEvidence struct {
	Status    string                  `json:"status"`
	Policy    string                  `json:"policy_version"`
	Condition *map[string]interface{} `json:"condition,omitempty"`
	Reason    string                  `json:"reason,omitempty"`
}

// conditionJSON canonicalizes a condition for hashing and persistence.
func conditionJSON(c *ragflow.MetadataCondition) (string, *map[string]interface{}) {
	if c.Empty() {
		return "", nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err != nil {
		return string(data), nil
	}
	return string(data), &generic
}

// applyChatPushdownForChat resolves the chat assistant's datasets and applies
// the metadata condition to req (doc/123 §8.1).
func (s *Service) applyChatPushdownForChat(ctx context.Context, tenantID, chatID string, req *ragflow.CompletionRequest) *pushdownEvidence {
	shadow, err := s.Store.GetChatShadow(ctx, tenantID, chatID, false)
	if err != nil || shadow == nil {
		return &pushdownEvidence{Status: model.RetrievalPushdownBypassed, Policy: model.RetrievalPushdownPolicyVersion, Reason: "chat_shadow_unavailable"}
	}
	var datasetIDs []string
	if strings.TrimSpace(shadow.DatasetIDs) != "" {
		datasetIDs = strings.Split(shadow.DatasetIDs, ",")
	}
	return s.applyChatPushdown(ctx, tenantID, datasetIDs, req)
}

// applyChatPushdown fills req's metadata condition for a chat completion and
// returns the projection evidence. Failures degrade to bypassed evidence so
// the request proceeds with projection-layer redaction only (doc/123 §6.4).
func (s *Service) applyChatPushdown(ctx context.Context, tenantID string, datasetIDs []string, req *ragflow.CompletionRequest) *pushdownEvidence {
	if len(datasetIDs) == 0 {
		return &pushdownEvidence{Status: model.RetrievalPushdownAbsent, Policy: model.RetrievalPushdownPolicyVersion, Reason: "no_datasets"}
	}
	condition, err := s.buildPushdownCondition(ctx, tenantID, datasetIDs)
	if err != nil {
		return &pushdownEvidence{Status: model.RetrievalPushdownBypassed, Policy: model.RetrievalPushdownPolicyVersion, Reason: "build_failed"}
	}
	if condition.Empty() {
		return &pushdownEvidence{Status: model.RetrievalPushdownAbsent, Policy: model.RetrievalPushdownPolicyVersion, Reason: "all_visible"}
	}
	req.ExtraBody = &ragflow.CompletionExtraBody{MetadataCondition: condition}
	_, generic := conditionJSON(condition)
	return &pushdownEvidence{Status: model.RetrievalPushdownApplied, Policy: model.RetrievalPushdownPolicyVersion, Condition: generic}
}

// applySearchPushdown is a v1 no-op: docs v0.27.2 do not document
// metadata_condition on the search completion endpoint (doc/123 §9).
func (s *Service) applySearchPushdown(ctx context.Context, tenantID string, datasetIDs []string, req *ragflow.SearchAppCompletionRequest) *pushdownEvidence {
	return &pushdownEvidence{Status: model.RetrievalPushdownAbsent, Policy: model.RetrievalPushdownPolicyVersion, Reason: "endpoint_unsupported"}
}

// agentPushdownEvidence is the constant absent evidence for agent scenarios.
func agentPushdownEvidence() *pushdownEvidence {
	return &pushdownEvidence{Status: model.RetrievalPushdownAbsent, Policy: model.RetrievalPushdownPolicyVersion, Reason: "endpoint_unsupported"}
}

// notePushdownFailure emits the degradation signal for operators.
func (s *Service) notePushdownFailure(ctx context.Context, tenantID, requestID string, evidence *pushdownEvidence) {
	if evidence == nil || evidence.Status != model.RetrievalPushdownBypassed {
		return
	}
	notify.Emit(ctx, notify.Event{
		Title: "retrieval pushdown bypassed", Severity: "warn", TenantID: tenantID,
		Type: "knowledge.pushdown_bypassed", Resource: "chat", ResourceID: requestID,
		Fields: map[string]string{"reason": evidence.Reason, "policy": evidence.Policy},
	})
}

// SyncDatasetPushdownMetadata backfills rgx_sensitivity (and optionally the
// business domain) onto every document of the dataset via the documented
// POST /datasets/{id}/metadata/update endpoint (doc/123 §5.2).
func (s *Service) SyncDatasetPushdownMetadata(ctx context.Context, tenantID, datasetID string) error {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if link == nil {
		return nil
	}
	level, _ := normalizeSensitivity(link.Sensitivity)
	updates := []ragflow.MetadataUpdate{{Key: metadataKeySensitivity, Value: level}}
	if strings.TrimSpace(link.BusinessDomain) != "" {
		updates = append(updates, ragflow.MetadataUpdate{Key: metadataKeyBusinessDoain, Value: strings.ToLower(strings.TrimSpace(link.BusinessDomain))})
	}
	docs, err := s.RAGFlow.ListDocuments(ctx, link.RAGFlowDatasetID)
	if err != nil {
		return fmt.Errorf("pushdown list documents: %w", err)
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	for start := 0; start < len(ids); start += metadataBatchSize {
		end := start + metadataBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := s.RAGFlow.BatchUpdateDatasetMetadata(ctx, link.RAGFlowDatasetID, ids[start:end], updates); err != nil {
			return fmt.Errorf("pushdown batch update: %w", err)
		}
	}
	return nil
}

// syncDatasetPushdownMetadataAsync best-effort wrapper for governance flows:
// failures never block the caller (doc/123 §5.3).
func (s *Service) syncDatasetPushdownMetadataAsync(ctx context.Context, tenantID, datasetID string) {
	go func() {
		if err := s.SyncDatasetPushdownMetadata(ctx, tenantID, datasetID); err != nil {
			notify.Emit(context.Background(), notify.Event{
				Title: "retrieval pushdown metadata sync failed", Severity: "warn", TenantID: tenantID,
				Type: "knowledge.pushdown_sync_failed", Resource: "dataset", ResourceID: datasetID,
				Fields: map[string]string{"error": err.Error()},
			})
		}
	}()
}

// pushdownEvidenceJSON serializes evidence for the projection column.
func pushdownEvidenceJSON(evidence *pushdownEvidence) string {
	if evidence == nil {
		evidence = agentPushdownEvidence()
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return `{"status":"absent"}`
	}
	return string(data)
}

// pushdownPolicyInputHashInputs appends the pushdown keys to the projection
// policy input so audit recomputation covers the retrieval layer (doc/123 §7).
func pushdownPolicyInputHashInputs(policyInput map[string]string, evidence *pushdownEvidence) {
	if evidence == nil {
		evidence = agentPushdownEvidence()
	}
	policyInput["pushdown_policy_version"] = evidence.Policy
	conditionJSON, _ := conditionJSONFromEvidence(evidence)
	sum := sha256.Sum256([]byte(conditionJSON))
	policyInput["pushdown_condition_hash"] = hex.EncodeToString(sum[:])
}

func conditionJSONFromEvidence(evidence *pushdownEvidence) (string, bool) {
	if evidence == nil || evidence.Condition == nil {
		return "", false
	}
	data, err := json.Marshal(evidence.Condition)
	if err != nil {
		return "", false
	}
	return string(data), true
}

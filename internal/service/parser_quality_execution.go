package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const parseQualityChunkSampleLimit = 200
const parserPolicyMetadataKey = "rgx_parser_policy_id"

// extractHeuristicParseMetrics derives the Profile-defined raw sources from
// parsed chunk text. Structural metrics remain conservative neutral values
// until RAGFlow exposes page/table layout metadata on its chunk contract.
func extractHeuristicParseMetrics(contents []string) map[string]float64 {
	metrics := map[string]float64{
		"text_density":                0,
		"empty_page_ratio":            1,
		"garbled_char_ratio":          0,
		"column_count_consistency":    1,
		"table_structure_consistency": 1,
		"reading_order_anomaly":       0,
		"repeated_header_ratio":       0,
	}
	if len(contents) == 0 {
		return metrics
	}

	var totalRunes, textRunes, garbledRunes, emptyChunks, nonEmptyChunks int
	headerCounts := map[string]int{}
	for _, content := range contents {
		if strings.TrimSpace(content) == "" {
			emptyChunks++
			continue
		}
		nonEmptyChunks++
		for _, char := range content {
			totalRunes++
			switch {
			case unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsPunct(char):
				textRunes++
			case char == unicode.ReplacementChar:
				garbledRunes++
			case unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t':
				garbledRunes++
			}
		}
		firstLine := strings.ToLower(strings.Join(strings.Fields(strings.SplitN(content, "\n", 2)[0]), " "))
		if firstLine != "" {
			headerCounts[firstLine]++
		}
	}

	if totalRunes > 0 {
		metrics["text_density"] = float64(textRunes) / float64(totalRunes)
		metrics["garbled_char_ratio"] = float64(garbledRunes) / float64(totalRunes)
	}
	metrics["empty_page_ratio"] = float64(emptyChunks) / float64(len(contents))
	repeatedHeaders := 0
	for _, count := range headerCounts {
		if count > 1 {
			repeatedHeaders += count - 1
		}
	}
	if nonEmptyChunks > 0 {
		metrics["repeated_header_ratio"] = float64(repeatedHeaders) / float64(nonEmptyChunks)
	}
	return metrics
}

func documentTypeFromName(name string) string {
	extension := strings.ToLower(strings.TrimSpace(name))
	if index := strings.LastIndex(extension, "."); index >= 0 {
		extension = extension[index+1:]
	}
	return extension
}

func parseRAGFlowTimestamp(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return time.Time{}
	}
	switch {
	case number > 1e12:
		return time.UnixMilli(int64(number))
	case number > 1e9:
		return time.Unix(int64(number), 0)
	default:
		return time.Time{}
	}
}

func documentParseInterval(document ragflow.Document, fallback time.Time) (time.Time, time.Time) {
	started := parseRAGFlowTimestamp(string(document.ProcessBeginAt))
	finished := parseRAGFlowTimestamp(string(document.ProcessDuration))
	if started.IsZero() {
		started = time.UnixMilli(document.CreateTime)
	}
	if finished.IsZero() {
		finished = time.UnixMilli(document.UpdateTime)
	}
	if started.IsZero() || finished.IsZero() {
		started = fallback.UTC()
		finished = started
	}
	if finished.Before(started) {
		finished = started
	}
	return started.UTC(), finished.UTC()
}

func hasCurrentParseQualityReport(report *model.ParseQualityReport, document ragflow.Document) bool {
	if report == nil {
		return false
	}
	updateTime := time.UnixMilli(document.UpdateTime)
	if updateTime.IsZero() {
		return true
	}
	return !report.CreatedAt.Before(updateTime)
}

func (s *Service) recordParseQualityForDocument(
	ctx context.Context, tenantID string, dataset *model.DatasetLink, document ragflow.Document, userID, traceID string,
) (*model.ParseQualityReport, error) {
	documentType := documentTypeFromName(document.Name)
	resolvedPolicy, err := s.ResolveParserPolicy(ctx, tenantID, "", dataset.ID, documentType)
	if err != nil {
		return nil, err
	}
	if resolvedPolicy == nil {
		return nil, nil
	}
	latest, err := s.LatestParseQualityReport(ctx, tenantID, document.ID)
	if err != nil {
		return nil, err
	}
	if hasCurrentParseQualityReport(latest, document) {
		return latest, nil
	}
	policy := resolvedPolicy
	if markerPolicyID := documentMetadataParserPolicy(document); markerPolicyID != "" {
		markerPolicy, err := s.getParserPolicy(ctx, tenantID, markerPolicyID)
		if err != nil {
			return nil, err
		}
		if markerPolicy != nil && markerPolicy.Active {
			policy = markerPolicy
		}
	} else if latest != nil && latest.GateAction == model.GateActionRetry {
		nextPolicy, err := s.parserFallbackSuccessor(ctx, tenantID, latest.ParserPolicyID)
		if err != nil {
			return nil, err
		}
		policy = nextPolicy
	}

	chunks, _, err := s.RAGFlow.ListDocumentChunks(ctx, dataset.RAGFlowDatasetID, document.ID, 1, parseQualityChunkSampleLimit)
	if err != nil {
		return nil, httperr.New(502, 50210, "ragflow list document chunks failed")
	}
	contents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
	}
	startedAt, finishedAt := documentParseInterval(document, time.Now().UTC())
	_, report, _, err := s.RecordParseQuality(ctx, tenantID, ParseQualityRequest{
		DatasetID:        dataset.ID,
		DocumentType:     documentType,
		ParserPolicyID:   policy.ID,
		DocumentID:       document.ID,
		UserID:           userID,
		TraceID:          traceID,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		HeuristicMetrics: extractHeuristicParseMetrics(contents),
	})
	if err != nil {
		return nil, err
	}
	if report.GateAction == model.GateActionRetry || report.GateAction == model.GateActionQuarantine {
		if report.GateAction == model.GateActionRetry {
			if err := s.executeParserFallbackForDocument(ctx, tenantID, dataset, document, report, userID, traceID); err != nil {
				if disableErr := s.RAGFlow.SetDocumentsStatus(ctx, dataset.RAGFlowDatasetID, []string{document.ID}, false); disableErr != nil {
					logger.Warn("failed to disable document after parser fallback failure",
						"dataset_id", dataset.ID, "document_id", document.ID, "error", disableErr)
				}
				document.Status = "failed"
				document.ProgressMsg = "quality gate retry: parser fallback failed"
				return report, err
			}
			document.Status = "pending"
			document.Progress = 0
			document.ProgressMsg = ""
			return report, nil
		}
		if err := s.RAGFlow.SetDocumentsStatus(ctx, dataset.RAGFlowDatasetID, []string{document.ID}, false); err != nil {
			logger.Warn("failed to disable document rejected by parse quality gate",
				"dataset_id", dataset.ID, "document_id", document.ID, "gate_action", report.GateAction, "error", err)
			return report, err
		}
		document.Status = "failed"
		document.ProgressMsg = "quality gate quarantine"
	}
	return report, nil
}

func (s *Service) parserFallbackSuccessor(ctx context.Context, tenantID, policyID string) (*model.ParserPolicy, error) {
	policy, err := s.getParserPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, notFoundParserPolicy()
	}
	var fallback ParserFallbackPolicy
	if err := json.Unmarshal([]byte(policy.FallbackPolicy), &fallback); err != nil || strings.TrimSpace(fallback.FallbackPolicyID) == "" {
		return nil, httperr.New(409, 40983, "parser policy fallback chain is unavailable")
	}
	nextPolicy, err := s.getParserPolicy(ctx, tenantID, fallback.FallbackPolicyID)
	if err != nil {
		return nil, err
	}
	if nextPolicy == nil || !nextPolicy.Active {
		return nil, httperr.New(409, 40983, "parser policy fallback must reference an active policy")
	}
	return nextPolicy, nil
}

func documentParseConfig(policy *model.ParserPolicy, document ragflow.Document) (*ragflow.DocumentParseConfigUpdate, error) {
	config := &ragflow.DocumentParseConfigUpdate{
		Pipeline:        policy.ParseMode == model.ParseModePipeline,
		BuiltinParserID: policy.ChunkMethod,
		PipelineID:      policy.PipelineID,
	}
	if strings.TrimSpace(policy.ParserConfig) != "" && policy.ParserConfig != "{}" {
		var parserConfig map[string]interface{}
		if err := json.Unmarshal([]byte(policy.ParserConfig), &parserConfig); err != nil {
			return nil, httperr.New(400, 40081, "parser_config must be a JSON object")
		}
		config.ParserConfig = parserConfig
	}
	metaFields := map[string]interface{}{}
	for key, value := range document.Metadata {
		if value == nil {
			continue
		}
		metaFields[key] = value
	}
	metaFields[parserPolicyMetadataKey] = policy.ID
	config.MetaFields = metaFields
	return config, nil
}

func documentMetadataParserPolicy(document ragflow.Document) string {
	if document.Metadata == nil {
		return ""
	}
	policyID, _ := document.Metadata[parserPolicyMetadataKey].(string)
	return strings.TrimSpace(policyID)
}

func (s *Service) executeParserFallbackForDocument(
	ctx context.Context, tenantID string, dataset *model.DatasetLink, document ragflow.Document,
	report *model.ParseQualityReport, userID, traceID string,
) error {
	policy, err := s.getParserPolicy(ctx, tenantID, report.ParserPolicyID)
	if err != nil {
		return err
	}
	if policy == nil {
		return notFoundParserPolicy()
	}
	nextPolicy, err := s.parserFallbackSuccessor(ctx, tenantID, policy.ID)
	if err != nil {
		return err
	}
	config, err := documentParseConfig(nextPolicy, document)
	if err != nil {
		return err
	}
	if err := s.RAGFlow.UpdateDocumentParseConfig(ctx, dataset.RAGFlowDatasetID, document.ID, *config); err != nil {
		return fmt.Errorf("ragflow update document parse config failed: %w", err)
	}
	if err := s.RAGFlow.ParseDocuments(ctx, dataset.RAGFlowDatasetID, []string{document.ID}); err != nil {
		return fmt.Errorf("ragflow parser fallback parse failed: %w", err)
	}
	if err := s.CreateTask(ctx, &model.Task{
		TenantID: tenantID, DatasetID: dataset.ID, DocID: document.ID, DocName: document.Name,
		TaskType: model.TaskTypeParse, Status: model.TaskStatusRunning,
		Detail: "parser fallback to policy " + nextPolicy.ID,
	}); err != nil {
		return fmt.Errorf("parser fallback task projection failed: %w", err)
	}
	return nil
}

func (s *Service) assertDatasetDocument(ctx context.Context, tenantID, datasetID, documentID string) (*model.DatasetLink, error) {
	dataset, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	for _, document := range documents {
		if document.ID == documentID {
			return dataset, nil
		}
	}
	return nil, httperr.NotFound("document not found")
}

func (s *Service) ListDocumentParseAttempts(ctx context.Context, tenantID, datasetID, documentID string, page, pageSize int) ([]model.ParseAttempt, int64, error) {
	if _, err := s.assertDatasetDocument(ctx, tenantID, datasetID, documentID); err != nil {
		return nil, 0, err
	}
	return s.Store.ListParseAttempts(ctx, tenantID, documentID, page, pageSize)
}

func (s *Service) GetLatestDocumentParseQualityReport(ctx context.Context, tenantID, datasetID, documentID string) (*model.ParseQualityReport, error) {
	if _, err := s.assertDatasetDocument(ctx, tenantID, datasetID, documentID); err != nil {
		return nil, err
	}
	return s.LatestParseQualityReport(ctx, tenantID, documentID)
}

func (s *Service) ListDocumentParseQualityReports(ctx context.Context, tenantID, datasetID, documentID string, page, pageSize int) ([]model.ParseQualityReport, int64, error) {
	if _, err := s.assertDatasetDocument(ctx, tenantID, datasetID, documentID); err != nil {
		return nil, 0, err
	}
	return s.Store.ListParseQualityReports(ctx, tenantID, documentID, page, pageSize)
}

func (s *Service) ListDatasetLatestParseQualityReports(ctx context.Context, tenantID, datasetID string, page, pageSize int) ([]model.ParseQualityReport, int64, error) {
	dataset, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, 0, err
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, 0, httperr.New(502, 50202, "ragflow list documents failed")
	}
	documentIDs := make([]string, 0, len(documents))
	for _, document := range documents {
		documentIDs = append(documentIDs, document.ID)
	}
	reports, total, err := s.Store.ListLatestParseQualityReports(ctx, tenantID, documentIDs)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(reports, func(left, right int) bool {
		if !reports[left].CreatedAt.Equal(reports[right].CreatedAt) {
			return reports[left].CreatedAt.After(reports[right].CreatedAt)
		}
		return reports[left].ID > reports[right].ID
	})
	start := (page - 1) * pageSize
	if start >= len(reports) {
		return []model.ParseQualityReport{}, total, nil
	}
	end := min(start+pageSize, len(reports))
	return reports[start:end], total, nil
}

package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type KnowledgeAssetEvidence struct {
	AnswerSnapshotID string    `json:"answer_snapshot_id"`
	RequestID        string    `json:"request_id"`
	CitationID       string    `json:"citation_id"`
	ChunkID          string    `json:"chunk_id"`
	DocumentID       string    `json:"document_id"`
	DocumentVersion  string    `json:"document_version"`
	CitationLocator  string    `json:"citation_locator"`
	CitationHash     string    `json:"citation_hash"`
	CreatedAt        time.Time `json:"created_at"`
}

type KnowledgeAssetTrace struct {
	TraceID     string `json:"trace_id"`
	RequestID   string `json:"request_id"`
	AssistantID string `json:"assistant_id"`
	AppType     string `json:"app_type"`
	Channel     string `json:"channel"`
	Status      string `json:"status"`
}

type KnowledgeAssetTask struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Category           string    `json:"category"`
	Status             string    `json:"status"`
	Priority           string    `json:"priority"`
	OwnerID            string    `json:"owner_id"`
	SourceAnswerRunID  string    `json:"source_answer_run_id"`
	SourceAnswerSnapID string    `json:"source_answer_snapshot_id"`
	SourceCitationID   string    `json:"source_citation_id"`
	SourceChunkID      string    `json:"source_chunk_id"`
	SourceCitationHash string    `json:"source_citation_hash"`
	SourceDocVersion   string    `json:"source_document_version"`
	RegressionStatus   string    `json:"regression_status"`
	CreatedAt          time.Time `json:"created_at"`
}

type KnowledgeAssetView struct {
	model.DatasetLink
	LifecycleStatus string                   `json:"lifecycle_status"`
	AssistantIDs    []string                 `json:"assistant_ids"`
	AssistantReleas []string                 `json:"assistant_release_ids"`
	TraceRuns       []KnowledgeAssetTrace    `json:"trace_runs"`
	ChunkEvidence   []KnowledgeAssetEvidence `json:"chunk_evidence"`
	KnowledgeTasks  []KnowledgeAssetTask     `json:"knowledge_tasks"`
}

type KnowledgeAssetRepo interface {
	ListKnowledgeAssetViews(ctx context.Context, tenantID string, datasets []model.DatasetLink, limit int) ([]KnowledgeAssetView, error)
}

func (s *store) ListKnowledgeAssetViews(ctx context.Context, tenantID string, datasets []model.DatasetLink, limit int) ([]KnowledgeAssetView, error) {
	if limit <= 0 {
		limit = 20
	}
	out := make([]KnowledgeAssetView, 0, len(datasets))
	datasetIDs := make([]string, 0, len(datasets))
	for _, dataset := range datasets {
		datasetIDs = append(datasetIDs, dataset.ID)
	}

	type assistantLink struct {
		DatasetID   string `gorm:"column:dataset_id"`
		AssistantID string `gorm:"column:assistant_id"`
		ReleaseID   string `gorm:"column:release_id"`
	}
	var links []assistantLink
	if len(datasetIDs) > 0 {
		if err := s.WithContext(ctx).Raw(`
			SELECT b.dataset_id, r.assistant_id, b.assistant_release_id AS release_id
			FROM rgx_dataset_binding_version b
			JOIN rgx_assistant_release r ON r.id = b.assistant_release_id
			WHERE b.tenant_id = ? AND b.status = 'ACTIVE' AND b.dataset_id IN ?
		`, tenantID, datasetIDs).Scan(&links).Error; err != nil {
			return nil, err
		}
	}
	assistantsByDataset := map[string][]string{}
	releasesByDataset := map[string][]string{}
	for _, link := range links {
		assistantsByDataset[link.DatasetID] = appendUniqueString(assistantsByDataset[link.DatasetID], link.AssistantID)
		releasesByDataset[link.DatasetID] = appendUniqueString(releasesByDataset[link.DatasetID], link.ReleaseID)
	}

	for _, dataset := range datasets {
		view := KnowledgeAssetView{
			DatasetLink: dataset, AssistantIDs: assistantsByDataset[dataset.ID],
			AssistantReleas: releasesByDataset[dataset.ID],
			TraceRuns:       []KnowledgeAssetTrace{}, ChunkEvidence: []KnowledgeAssetEvidence{},
			KnowledgeTasks: []KnowledgeAssetTask{},
		}
		if err := s.loadKnowledgeAssetEvidence(ctx, tenantID, dataset.ID, limit, &view); err != nil {
			return nil, err
		}
		if err := s.loadKnowledgeAssetTraces(ctx, tenantID, &view, limit); err != nil {
			return nil, err
		}
		if err := s.loadKnowledgeAssetTasks(ctx, tenantID, dataset.ID, limit, &view); err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *store) loadKnowledgeAssetEvidence(ctx context.Context, tenantID, datasetID string, limit int, view *KnowledgeAssetView) error {
	requestIDs := make([]string, 0, limit)
	var snapshots []model.AnswerSnapshot
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND citations_json LIKE ?", tenantID, "%"+datasetID+"%").
		Order("created_at DESC").Limit(limit).Find(&snapshots).Error; err != nil {
		return err
	}
	runByID := map[string]model.AnswerRun{}
	runIDs := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		runIDs = appendUniqueString(runIDs, snapshot.AnswerRunID)
	}
	if len(runIDs) > 0 {
		var runs []model.AnswerRun
		if err := s.WithContext(ctx).
			Where("tenant_id = ? AND id IN ?", tenantID, runIDs).
			Find(&runs).Error; err != nil {
			return err
		}
		for _, run := range runs {
			runByID[run.ID] = run
		}
	}
	for _, snapshot := range snapshots {
		var citations []model.AnswerCitation
		if err := json.Unmarshal([]byte(snapshot.CitationsJSON), &citations); err != nil {
			return err
		}
		run := runByID[snapshot.AnswerRunID]
		for _, citation := range citations {
			if citation.DatasetID != datasetID {
				continue
			}
			view.ChunkEvidence = append(view.ChunkEvidence, KnowledgeAssetEvidence{
				AnswerSnapshotID: snapshot.ID, RequestID: run.RequestID,
				CitationID: citation.ID, ChunkID: citation.ChunkID,
				DocumentID: citation.DocumentID, DocumentVersion: citation.DocumentVersion,
				CitationLocator: citation.CitationLocator, CitationHash: citation.CitationContentHash,
				CreatedAt: snapshot.CreatedAt,
			})
			if citation.ChunkID != "" && run.RequestID != "" {
				requestIDs = appendUniqueString(requestIDs, run.RequestID)
			}
		}
	}
	if len(view.ChunkEvidence) > limit {
		view.ChunkEvidence = view.ChunkEvidence[:limit]
	}
	return nil
}

func (s *store) loadKnowledgeAssetTraces(ctx context.Context, tenantID string, view *KnowledgeAssetView, limit int) error {
	requestIDs := make([]string, 0, len(view.ChunkEvidence))
	for _, evidence := range view.ChunkEvidence {
		if evidence.ChunkID != "" {
			requestIDs = appendUniqueString(requestIDs, evidence.RequestID)
		}
	}
	if len(requestIDs) == 0 {
		return nil
	}
	var traces []model.TraceRun
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND request_id IN ?", tenantID, requestIDs).
		Order("created_at DESC").Limit(limit).Find(&traces).Error; err != nil {
		return err
	}
	for _, trace := range traces {
		view.TraceRuns = append(view.TraceRuns, KnowledgeAssetTrace{
			TraceID: trace.TraceID, RequestID: trace.RequestID,
			AssistantID: trace.AssistantID, AppType: trace.AppType,
			Channel: trace.Channel, Status: trace.Status,
		})
	}
	return nil
}

func (s *store) loadKnowledgeAssetTasks(ctx context.Context, tenantID, datasetID string, limit int, view *KnowledgeAssetView) error {
	var tasks []model.KnowledgeTask
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND source_dataset_id = ?", tenantID, datasetID).
		Order("created_at DESC").Limit(limit).Find(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		view.KnowledgeTasks = append(view.KnowledgeTasks, KnowledgeAssetTask{
			ID: task.ID, Title: task.Title, Category: task.Category,
			Status: task.Status, Priority: task.Priority, OwnerID: task.OwnerID,
			SourceAnswerRunID: task.SourceAnswerRunID, SourceAnswerSnapID: task.SourceAnswerSnapshotID,
			SourceCitationID: task.SourceCitationID, SourceChunkID: task.SourceChunkID,
			SourceCitationHash: task.SourceCitationHash, SourceDocVersion: task.SourceDocumentVersion,
			RegressionStatus: task.RegressionStatus, CreatedAt: task.CreatedAt,
		})
	}
	return nil
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

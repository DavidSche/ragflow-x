package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// DatasetSummary is the API-facing representation of a dataset bound to a tenant.
type DatasetSummary struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	Name             string `json:"name"`
	RAGFlowDatasetID string `json:"ragflow_dataset_id"`
	DocumentCount    int64  `json:"document_count"`
	TenantName       string `json:"tenant_name,omitempty"`
}

// CreateDataset creates the dataset in RAGFlow and records the mapping link.
func (s *Service) CreateDataset(ctx context.Context, tenantID, name string) (*DatasetSummary, error) {
	name, err := normalizeDisplayName(name, "dataset name is required", 40010)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetDatasetLinkByName(ctx, tenantID, name, "")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("dataset")
	}
	ds, err := s.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: name})
	if err != nil {
		return nil, httperr.New(502, 50200, "ragflow create dataset failed")
	}
	link := &model.DatasetLink{
		ID:               id.New(),
		TenantID:         tenantID,
		RAGFlowDatasetID: ds.ID,
		Name:             name,
	}
	if err := s.Store.CreateDatasetLink(ctx, link); err != nil {
		if deleteErr := s.RAGFlow.DeleteDataset(ctx, ds.ID); deleteErr != nil {
			logger.Warn("failed to roll back external dataset after local persistence failure",
				"ragflow_dataset_id", ds.ID, "error", deleteErr)
		}
		return nil, err
	}
	s.invalidateDatasets()
	return &DatasetSummary{
		ID: link.ID, TenantID: tenantID, Name: name,
		RAGFlowDatasetID: ds.ID,
	}, nil
}

// ListDatasets returns all datasets bound to a tenant, joined with live
// document counts where available.
// ListDatasets results are memoized briefly so hot paths do not hit RAGFlow.
func (s *Service) ListDatasets(ctx context.Context, tenantID string, filter repository.DatasetFilter) ([]DatasetSummary, error) {
	load := func(ctx context.Context) ([]DatasetSummary, error) {
		links, err := s.Store.ListByTenant(ctx, tenantID, filter)
		if err != nil {
			return nil, err
		}
		return composeDatasetSummaries(links, nil, s.datasetCounts(ctx)), nil
	}
	// Filtered requests must not share the unfiltered cache entry: one user's
	// filter would otherwise be served to every later caller within the TTL.
	if filter.IsActive() {
		return load(ctx)
	}
	return s.cacheDatasets(ctx, "t:"+tenantID, load)
}

// ListDatasetsForScope is the Resolver-backed governance read path. Unlike the
// current-workspace cache, governance results are loaded per request so filters
// and explicit tenant boundaries cannot leak across callers.
func (s *Service) ListDatasetsForScope(ctx context.Context, scope TenantScope, filter repository.DatasetFilter) ([]DatasetSummary, error) {
	links, err := s.Store.ListDatasetLinksForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), filter)
	if err != nil {
		return nil, err
	}
	tenantName := map[string]string{}
	if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
		for _, tenant := range tenants {
			tenantName[tenant.ID] = tenant.Name
		}
	}
	return composeDatasetSummaries(links, tenantName, s.datasetCounts(ctx)), nil
}

// resolveDataset loads a dataset link scoped to a tenant.
func (s *Service) resolveDataset(ctx context.Context, tenantID, datasetID string) (*model.DatasetLink, error) {
	d, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	return d, nil
}

// GetDataset returns a single dataset bound to a tenant, with its live document count.
func (s *Service) GetDataset(ctx context.Context, tenantID, datasetID string) (*DatasetSummary, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	count := int64(0)
	if live, err := s.RAGFlow.ListDatasets(ctx); err == nil {
		for _, ds := range live {
			if ds.ID == d.RAGFlowDatasetID {
				count = ds.DocumentCount
				break
			}
		}
	}
	return &DatasetSummary{
		ID:               d.ID,
		TenantID:         d.TenantID,
		Name:             d.Name,
		RAGFlowDatasetID: d.RAGFlowDatasetID,
		DocumentCount:    count,
	}, nil
}

// GetDatasetForScope reads a dataset only when its owning tenant is authorized
// by the resolved governance scope.
func (s *Service) GetDatasetForScope(ctx context.Context, scope TenantScope, datasetID string) (*DatasetSummary, error) {
	found, err := s.Store.GetDatasetLinkForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), datasetID)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	tenantName := ""
	if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
		for _, tenant := range tenants {
			if tenant.ID == found.TenantID {
				tenantName = tenant.Name
				break
			}
		}
	}
	count := int64(0)
	if live, err := s.RAGFlow.ListDatasets(ctx); err == nil {
		for _, ds := range live {
			if ds.ID == found.RAGFlowDatasetID {
				count = ds.DocumentCount
				break
			}
		}
	}
	return &DatasetSummary{
		ID:               found.ID,
		TenantID:         found.TenantID,
		Name:             found.Name,
		RAGFlowDatasetID: found.RAGFlowDatasetID,
		DocumentCount:    count,
		TenantName:       tenantName,
	}, nil
}

// UpdateDataset renames a dataset link within a tenant.
func (s *Service) UpdateDataset(ctx context.Context, tenantID, datasetID, name string) (*DatasetSummary, error) {
	name, err := normalizeDisplayName(name, "dataset name is required", 40010)
	if err != nil {
		return nil, err
	}
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetDatasetLinkByName(ctx, tenantID, name, datasetID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("dataset")
	}
	if err := s.Store.UpdateDatasetLinkName(ctx, tenantID, datasetID, name); err != nil {
		return nil, err
	}
	s.invalidateDatasets()
	return &DatasetSummary{
		ID: d.ID, TenantID: tenantID, Name: name, RAGFlowDatasetID: d.RAGFlowDatasetID,
	}, nil
}

// DeleteDataset removes the dataset from RAGFlow and drops its link.
func (s *Service) DeleteDataset(ctx context.Context, tenantID, datasetID string) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.RAGFlow.DeleteDataset(ctx, d.RAGFlowDatasetID); err != nil {
		return httperr.New(502, 50207, "ragflow delete dataset failed")
	}
	if err := s.Store.DeleteDocumentOwnershipByDataset(ctx, tenantID, datasetID); err != nil {
		return httperr.Internal("delete document ownership failed")
	}
	if err := s.Store.DeleteDatasetLinkByTenant(ctx, tenantID, datasetID); err != nil {
		return err
	}
	s.invalidateDatasets()
	return nil
}

// ListAllDatasets returns all datasets across tenants, used by platform admins.
func (s *Service) ListAllDatasets(ctx context.Context, filter repository.DatasetFilter) ([]DatasetSummary, error) {
	load := func(ctx context.Context) ([]DatasetSummary, error) {
		links, err := s.Store.ListAllDatasetLinks(ctx, filter)
		if err != nil {
			return nil, err
		}
		tenantName := map[string]string{}
		if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
			for _, t := range tenants {
				tenantName[t.ID] = t.Name
			}
		}
		return composeDatasetSummaries(links, tenantName, s.datasetCounts(ctx)), nil
	}
	// Filtered cross-tenant requests must not be served from the unfiltered
	// "all" cache entry, which would leak one filter's rows to every caller.
	if filter.IsActive() {
		return load(ctx)
	}
	return s.cacheDatasets(ctx, "all", load)
}

// DeleteDatasets deletes multiple datasets for a tenant.
func (s *Service) DeleteDatasets(ctx context.Context, tenantID string, ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return httperr.BadRequest(40011, "dataset ids must be between 1 and 100")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return httperr.BadRequest(40011, "dataset ids must be unique and non-empty")
		}
		seen[id] = true
		if err := s.DeleteDataset(ctx, tenantID, id); err != nil {
			return err
		}
	}
	return nil
}

const datasetExportLimit = 10000

// ExportDatasets renders the dataset list as CSV.
func (s *Service) ExportDatasets(ctx context.Context, tenantID string, scopeAll bool) ([]byte, error) {
	var list []DatasetSummary
	var err error
	if scopeAll {
		list, err = s.ListAllDatasets(ctx, repository.DatasetFilter{})
	} else {
		list, err = s.ListDatasets(ctx, tenantID, repository.DatasetFilter{})
	}
	if err != nil {
		return nil, err
	}
	if int64(len(list)) > datasetExportLimit {
		return nil, httperr.BadRequest(40104, "导出结果超过 10,000 行，请缩小筛选范围")
	}
	return datasetCSV(list)
}

// ExportDatasetsForScope renders only datasets visible through the resolved
// governance scope.
func (s *Service) ExportDatasetsForScope(ctx context.Context, scope TenantScope) (int64, []byte, error) {
	links, total, err := s.Store.ListDatasetLinksForScopePage(
		ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), repository.DatasetFilter{}, 1, datasetExportLimit,
	)
	if err != nil {
		return 0, nil, err
	}
	if total > datasetExportLimit {
		return 0, nil, httperr.BadRequest(40104, "导出结果超过 10,000 行，请缩小筛选范围")
	}
	tenantName := map[string]string{}
	if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
		for _, tenant := range tenants {
			tenantName[tenant.ID] = tenant.Name
		}
	}
	list := composeDatasetSummaries(links, tenantName, s.datasetCounts(ctx))
	data, err := datasetCSV(list)
	if err != nil {
		return 0, nil, err
	}
	return int64(len(list)), data, nil
}

func datasetCSV(list []DatasetSummary) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"id", "tenant", "name", "ragflow_dataset_id", "document_count"})
	for _, dataset := range list {
		_ = w.Write([]string{
			sanitizeCSVCell(dataset.ID),
			sanitizeCSVCell(dataset.TenantName),
			sanitizeCSVCell(dataset.Name),
			sanitizeCSVCell(dataset.RAGFlowDatasetID),
			strconv.FormatInt(dataset.DocumentCount, 10),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// DocumentSummary is the API-facing representation of a document.
type DocumentSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Enabled     bool    `json:"enabled"`
	ChunkCount  int64   `json:"chunk_count"`
	TokenCount  int64   `json:"token_count"`
	Progress    float64 `json:"progress"`
	ProgressMsg string  `json:"progress_msg"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
	Size        int64   `json:"size"`
	OwnedByMe   bool    `json:"owned_by_me,omitempty"`
	ParseTaskID string  `json:"parse_task_id,omitempty"`
}

func summaryFromRAGDoc(doc ragflow.Document) DocumentSummary {
	return DocumentSummary{
		ID:          doc.ID,
		Name:        doc.Name,
		Status:      doc.Status,
		Enabled:     doc.Enabled == "1",
		ChunkCount:  doc.ChunkCount,
		TokenCount:  doc.TokenCount,
		Progress:    doc.Progress,
		ProgressMsg: doc.ProgressMsg,
		CreatedAt:   doc.CreateTime,
		UpdatedAt:   doc.UpdateTime,
		Size:        doc.Size,
	}
}

// UploadDocument uploads a document into a tenant's dataset.
func (s *Service) UploadDocument(ctx context.Context, tenantID, datasetID, uploaderID, name string, content []byte) (*DocumentSummary, error) {
	return s.UploadDocumentWithSourceKey(ctx, tenantID, datasetID, uploaderID, name, "", content)
}

// ListDocuments lists documents of a tenant's dataset.
func (s *Service) ListDocuments(ctx context.Context, tenantID, datasetID string) ([]DocumentSummary, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	docs, err := s.RAGFlow.ListDocuments(ctx, d.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		out = append(out, summaryFromRAGDoc(doc))
	}
	return out, nil
}

// ListDocumentsForUser lists documents and marks the documents uploaded by the
// caller so clients can expose contribution-scoped deletion safely.
func (s *Service) ListDocumentsForUser(ctx context.Context, tenantID, datasetID, userID string) ([]DocumentSummary, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	docs, err := s.RAGFlow.ListDocuments(ctx, d.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	documentIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc.ID != "" {
			documentIDs = append(documentIDs, doc.ID)
		}
	}
	owners, err := s.Store.ListDocumentOwners(ctx, tenantID, d.ID, documentIDs)
	if err != nil {
		return nil, httperr.Internal("list document ownership failed")
	}
	uploadedBy := make(map[string]string, len(owners))
	for _, owner := range owners {
		uploadedBy[owner.DocumentID] = owner.UploaderID
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		summary := summaryFromRAGDoc(doc)
		summary.OwnedByMe = uploadedBy[doc.ID] == userID
		out = append(out, summary)
	}
	return out, nil
}

// ListDocumentsForScope lists a dataset's documents for governance reads.
func (s *Service) ListDocumentsForScope(ctx context.Context, scope TenantScope, datasetID string) ([]DocumentSummary, error) {
	dataset, err := s.GetDatasetForScope(ctx, scope, datasetID)
	if err != nil {
		return nil, err
	}
	docs, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		out = append(out, summaryFromRAGDoc(doc))
	}
	return out, nil
}

// ListDocumentChunksForScope returns parsed chunks for a governed dataset read.
func (s *Service) ListDocumentChunksForScope(ctx context.Context, scope TenantScope, datasetID, documentID string, page, pageSize int) ([]ChunkSummary, int64, error) {
	dataset, err := s.GetDatasetForScope(ctx, scope, datasetID)
	if err != nil {
		return nil, 0, err
	}
	chunks, total, err := s.RAGFlow.ListDocumentChunks(ctx, dataset.RAGFlowDatasetID, documentID, page, pageSize)
	if err != nil {
		return nil, 0, httperr.New(502, 50210, "ragflow list document chunks failed")
	}
	out := make([]ChunkSummary, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, ChunkSummary{ID: chunk.ID, Content: chunk.Content, DocName: chunk.DocName, Keywords: chunk.Keywords, Questions: chunk.Questions})
	}
	return out, total, nil
}

// GetDocumentContentForScope returns document bytes for a governed read.
func (s *Service) GetDocumentContentForScope(ctx context.Context, scope TenantScope, datasetID, documentID string) ([]byte, string, error) {
	_, err := s.GetDatasetForScope(ctx, scope, datasetID)
	if err != nil {
		return nil, "", err
	}
	return s.RAGFlow.GetDocumentContent(ctx, documentID)
}

// ParseDocuments triggers parsing for the given documents of a dataset.
func (s *Service) ParseDocuments(ctx context.Context, tenantID, datasetID string, docIDs []string) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.prepareDatasetParseReadiness(ctx, d); err != nil {
		return err
	}
	if err := s.RAGFlow.ParseDocuments(ctx, d.RAGFlowDatasetID, docIDs); err != nil {
		return httperr.New(502, 50203, "ragflow parse documents failed")
	}
	for _, docID := range docIDs {
		_ = s.CreateTask(ctx, &model.Task{TenantID: tenantID, DatasetID: d.ID, DocID: docID, TaskType: model.TaskTypeParse, Status: model.TaskStatusRunning})
	}
	return nil
}

// StopDocuments stops parsing for the given documents of a dataset.
func (s *Service) StopDocuments(ctx context.Context, tenantID, datasetID string, docIDs []string) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.RAGFlow.StopDocuments(ctx, d.RAGFlowDatasetID, docIDs); err != nil {
		return httperr.New(502, 50205, "ragflow stop documents failed")
	}
	_ = s.CreateTask(ctx, &model.Task{TenantID: tenantID, DatasetID: d.ID, TaskType: model.TaskTypeStop, Status: model.TaskStatusDone, Detail: fmt.Sprintf("%d documents", len(docIDs))})
	return nil
}

// DeleteDocuments removes the given documents from a tenant's dataset. Due
// tombstones stay protected because the public API path is not the governance
// approval executor.
func (s *Service) DeleteDocuments(ctx context.Context, tenantID, datasetID string, docIDs []string) error {
	return s.deleteDocuments(ctx, tenantID, datasetID, docIDs, false)
}

// DeleteApprovedDocuments is the only delete entry point that may remove an
// expired tombstone. The approval executor invokes it after the approval
// workflow has recorded requester, approver and snapshot evidence.
func (s *Service) DeleteApprovedDocuments(ctx context.Context, tenantID, datasetID string, docIDs []string) error {
	return s.deleteDocuments(ctx, tenantID, datasetID, docIDs, true)
}

func (s *Service) deleteDocuments(ctx context.Context, tenantID, datasetID string, docIDs []string, allowDueTombstone bool) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if reason := s.tombstoneDeleteBlock(ctx, tenantID, d.ID, docIDs, allowDueTombstone); reason != "" {
		return httperr.New(409, 40294, reason)
	}
	if err := s.RAGFlow.DeleteDocuments(ctx, d.RAGFlowDatasetID, docIDs); err != nil {
		return httperr.New(502, 50206, "ragflow delete documents failed")
	}
	s.markIncrementalLedgerDeleted(ctx, tenantID, d.ID, docIDs)
	_ = s.CreateTask(ctx, &model.Task{TenantID: tenantID, DatasetID: d.ID, TaskType: model.TaskTypeDelete, Status: model.TaskStatusDone, Detail: fmt.Sprintf("%d documents", len(docIDs))})
	s.invalidateDatasets()
	return nil
}

// DeleteOwnedDocuments removes only documents uploaded by the caller. It is
// used by roles with contribution rights but without dataset governance.
func (s *Service) DeleteOwnedDocuments(ctx context.Context, tenantID, datasetID, uploaderID string, rawDocIDs []string) error {
	documentIDs := uniqueDocumentIDs(rawDocIDs)
	if len(documentIDs) == 0 {
		return httperr.BadRequest(40012, "document_ids is required")
	}
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	ownedCount, err := s.Store.CountDocumentOwnership(ctx, tenantID, d.ID, uploaderID, documentIDs)
	if err != nil {
		return httperr.Internal("verify document ownership failed")
	}
	if ownedCount != int64(len(documentIDs)) {
		return httperr.Forbidden("users can only delete documents they uploaded")
	}
	if reason := s.tombstoneDeleteBlock(ctx, tenantID, d.ID, documentIDs, false); reason != "" {
		return httperr.New(409, 40294, reason)
	}
	if err := s.RAGFlow.DeleteDocuments(ctx, d.RAGFlowDatasetID, documentIDs); err != nil {
		return httperr.New(502, 50206, "ragflow delete documents failed")
	}
	if err := s.Store.DeleteDocumentOwnership(ctx, tenantID, d.ID, documentIDs); err != nil {
		return httperr.Internal("delete document ownership failed")
	}
	s.markIncrementalLedgerDeleted(ctx, tenantID, d.ID, documentIDs)
	_ = s.CreateTask(ctx, &model.Task{TenantID: tenantID, DatasetID: d.ID, TaskType: model.TaskTypeDelete, Status: model.TaskStatusDone, Detail: fmt.Sprintf("%d documents", len(documentIDs))})
	s.invalidateDatasets()
	return nil
}

func uniqueDocumentIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// SetDocumentsStatus enables or disables documents in a tenant's dataset.
func (s *Service) SetDocumentsStatus(ctx context.Context, tenantID, datasetID string, documentIDs []string, enabled bool) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.RAGFlow.SetDocumentsStatus(ctx, d.RAGFlowDatasetID, documentIDs, enabled); err != nil {
		return httperr.New(502, 50208, "ragflow set document status failed")
	}
	return nil
}

// UpdateDocumentMetadata replaces a document's metadata configuration.
func (s *Service) UpdateDocumentMetadata(ctx context.Context, tenantID, datasetID, documentID string, metadata map[string]interface{}) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.RAGFlow.UpdateDocumentMetadata(ctx, d.RAGFlowDatasetID, documentID, metadata); err != nil {
		return httperr.New(502, 50209, "ragflow update document metadata failed")
	}
	return nil
}

// ChunkSummary is the API-facing representation of a parsed chunk.
type ChunkSummary struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	DocName   string   `json:"doc_name"`
	Keywords  []string `json:"keywords"`
	Questions []string `json:"questions"`
}

// ListDocumentChunks returns a document's parsed chunks, confirming whether
// parsing actually completed even when the progress field is unreliable.
func (s *Service) ListDocumentChunks(ctx context.Context, tenantID, datasetID, documentID string, page, pageSize int) ([]ChunkSummary, int64, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, 0, err
	}
	chunks, total, err := s.RAGFlow.ListDocumentChunks(ctx, d.RAGFlowDatasetID, documentID, page, pageSize)
	if err != nil {
		return nil, 0, httperr.New(502, 50210, "ragflow list document chunks failed")
	}
	out := make([]ChunkSummary, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, ChunkSummary{ID: c.ID, Content: c.Content, DocName: c.DocName, Keywords: c.Keywords, Questions: c.Questions})
	}
	return out, total, nil
}

// GetDocumentContent returns a document's raw file bytes and content type.
func (s *Service) GetDocumentContent(ctx context.Context, tenantID, datasetID, documentID string) ([]byte, string, error) {
	_, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, "", err
	}
	return s.RAGFlow.GetDocumentContent(ctx, documentID)
}

// DeleteChunks removes the given chunks of a document.
func (s *Service) DeleteChunks(ctx context.Context, tenantID, datasetID, documentID string, chunkIDs []string) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	return s.RAGFlow.DeleteChunks(ctx, d.RAGFlowDatasetID, documentID, chunkIDs)
}

// SetChunksAvailable enables or disables the given chunks of a document.
func (s *Service) SetChunksAvailable(ctx context.Context, tenantID, datasetID, documentID string, chunkIDs []string, enabled bool) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	return s.RAGFlow.SetChunksAvailable(ctx, d.RAGFlowDatasetID, documentID, chunkIDs, enabled)
}

// GetDatasetConfig returns a dataset's parsing/embedding/permission config.
func (s *Service) GetDatasetConfig(ctx context.Context, tenantID, datasetID string) (*ragflow.DatasetConfig, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	return s.RAGFlow.GetDatasetConfig(ctx, d.RAGFlowDatasetID)
}

// GetDatasetConfigForScope reads dataset configuration for governance views.
func (s *Service) GetDatasetConfigForScope(ctx context.Context, scope TenantScope, datasetID string) (*ragflow.DatasetConfig, error) {
	dataset, err := s.GetDatasetForScope(ctx, scope, datasetID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.RAGFlow.GetDatasetConfig(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50294, "ragflow get dataset config failed")
	}
	return cfg, nil
}

// UpdateDatasetConfig updates a dataset's config.
func (s *Service) UpdateDatasetConfig(ctx context.Context, tenantID, datasetID string, cfg ragflow.DatasetConfigUpdate) error {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if err := s.canonicalizeDatasetEmbeddingUpdate(ctx, &cfg); err != nil {
		return err
	}
	if err := s.RAGFlow.UpdateDatasetConfig(ctx, d.RAGFlowDatasetID, cfg); err != nil {
		return httperr.New(502, 50295, "update dataset config failed")
	}
	return nil
}

type dsCacheEntry struct {
	items []DatasetSummary
	at    time.Time
}

const datasetCacheTTL = 15 * time.Second

// cacheDatasets memoizes dataset summaries per key for a short TTL so hot
// list endpoints do not fan out to the RAGFlow engine on every request.
func (s *Service) cacheDatasets(ctx context.Context, key string, load func(context.Context) ([]DatasetSummary, error)) ([]DatasetSummary, error) {
	now := time.Now()
	s.dsCacheMu.Lock()
	if e, ok := s.dsCache[key]; ok && now.Sub(e.at) < datasetCacheTTL {
		items := e.items
		s.dsCacheMu.Unlock()
		return append([]DatasetSummary(nil), items...), nil
	}
	s.dsCacheMu.Unlock()
	items, err := load(ctx)
	if err != nil {
		return nil, err
	}
	s.dsCacheMu.Lock()
	s.dsCache[key] = dsCacheEntry{items: items, at: time.Now()}
	s.dsCacheMu.Unlock()
	return items, nil
}

// invalidateDatasets clears the dataset cache after any structural write.
func (s *Service) invalidateDatasets() {
	s.dsCacheMu.Lock()
	s.dsCache = map[string]dsCacheEntry{}
	s.dsCacheMu.Unlock()
}

// datasetCounts fetches live RAGFlow document counts once, shared by loaders.
func (s *Service) datasetCounts(ctx context.Context) map[string]int64 {
	counts := map[string]int64{}
	if live, err := s.RAGFlow.ListDatasets(ctx); err == nil {
		for _, d := range live {
			counts[d.ID] = d.DocumentCount
		}
	}
	return counts
}

// composeDatasetSummaries builds summaries from links, optional tenant names,
// and document counts.
func composeDatasetSummaries(links []model.DatasetLink, tenantName map[string]string, counts map[string]int64) []DatasetSummary {
	out := make([]DatasetSummary, 0, len(links))
	for _, l := range links {
		out = append(out, DatasetSummary{
			ID: l.ID, TenantID: l.TenantID, Name: l.Name,
			TenantName: tenantName[l.TenantID], RAGFlowDatasetID: l.RAGFlowDatasetID,
			DocumentCount: counts[l.RAGFlowDatasetID],
		})
	}
	return out
}

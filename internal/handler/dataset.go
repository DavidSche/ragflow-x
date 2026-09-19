package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// pageParams reads and sanitizes the page/page_size query parameters.
func pageParams(c *gin.Context) (int, int) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return page, pageSize
}

type createDatasetRequest struct {
	Name string `json:"name" binding:"required"`
}

type updateDatasetRequest struct {
	Name string `json:"name" binding:"required"`
}

// ListDatasets lists datasets bound to the authenticated tenant.
func (h *Handler) ListDatasets(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	filter := repository.DatasetFilter{
		Name:        c.Query("name"),
		CreatedFrom: c.Query("created_from"),
		CreatedTo:   c.Query("created_to"),
	}
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	items, err := h.Service.ListDatasetsForScope(c.Request.Context(), scope, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// ListAllDatasets lists datasets across tenants for platform admins.
func (h *Handler) ListAllDatasets(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	filter := repository.DatasetFilter{
		Name:        c.Query("name"),
		TenantID:    c.Query("tenant_id"),
		CreatedFrom: c.Query("created_from"),
		CreatedTo:   c.Query("created_to"),
	}
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	if scope.ScopeAll() {
		items, err := h.Service.ListAllDatasets(c.Request.Context(), filter)
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OK(c, items)
		return
	}
	items, err := h.Service.ListDatasetsForScope(c.Request.Context(), scope, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// GetDataset returns a single dataset bound to the authenticated tenant.
func (h *Handler) GetDataset(c *gin.Context) {
	datasetID := c.Param("id")
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	ds, err := h.Service.GetDatasetForScope(c.Request.Context(), scope, datasetID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ds)
}

// GetDatasetModelOptions returns the tenant's chat/rerank/embedding models and
// defaults for the dataset config editor (embedding picker).
func (h *Handler) GetDatasetModelOptions(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	opts, err := h.Service.GetModelOptions(ctx)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, opts)
}

// DeleteDataset deletes a dataset and its RAGFlow backing knowledge base.
func (h *Handler) DeleteDataset(c *gin.Context) {
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, userID, "manage", "dataset", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	datasetID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDataset, model.ApprovalActionDelete, datasetID, map[string]any{}) {
		return
	}
	if err := h.Service.DeleteDataset(ctx, tenantID, datasetID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "dataset.delete", Resource: "dataset", ResourceID: datasetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": datasetID})
}

// DeleteDatasets batch-deletes datasets.
func (h *Handler) DeleteDatasets(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "ids is required")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	if tenantID != scope.ActorTenantID {
		response.Fail(c, 400, 40000, "cross-tenant dataset batch delete is not enabled")
		return
	}
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, userID, "manage", "dataset", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.DeleteDatasets(ctx, tenantID, req.IDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "dataset.delete.batch", Resource: "dataset", DetailJSON: strings.Join(req.IDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"deleted": len(req.IDs)})
}

// ExportDatasets streams the dataset list as CSV.
func (h *Handler) ExportDatasets(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "read", "dataset-export"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	rows, data, err := h.Service.ExportDatasetsForScope(ctx, scope)
	if err != nil {
		response.Err(c, err)
		return
	}
	entry := &model.AuditLog{
		TenantID: scope.ActorTenantID, UserID: c.GetString(middleware.ContextUserID),
		Action: "dataset.exported", Resource: "dataset-export", ResourceID: "csv",
		DetailJSON: fmt.Sprintf(`{"rows":%d,"limit":10000,"scope":%q,"target_tenant_id":%q}`,
			rows, scope.Kind, scope.TargetTenantID),
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	}
	entry.Scope = string(scope.Kind)
	entry.ActorTenantID = scope.ActorTenantID
	entry.TargetTenantID = scope.TargetTenantID
	entry.AuthorizationDecision = "ALLOW"
	entry.AuthorizationPermission = "read:dataset-export"
	entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
	if err := h.RequireAudit(ctx, entry); err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="datasets.csv"`)
	c.Data(200, "text/csv; charset=utf-8", data)
}

// BindDatasetProject assigns a dataset to a tenant project.
func (h *Handler) BindDatasetProject(c *gin.Context) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid project binding payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	datasetID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDataset, model.ApprovalActionUpdate, datasetID, map[string]any{"project_id": req.ProjectID}) {
		return
	}
	if err := h.Service.BindDatasetProject(ctx, tenantID, datasetID, req.ProjectID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "dataset.project", Resource: "dataset", ResourceID: datasetID, DetailJSON: req.ProjectID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": datasetID, "project_id": req.ProjectID})
}

// CreateDataset creates a dataset for the authenticated tenant.
func (h *Handler) CreateDataset(c *gin.Context) {
	var req createDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid dataset payload")
		return
	}
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	ctx := c.Request.Context()
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDataset, model.ApprovalActionCreate, "new:"+req.Name, map[string]any{"name": req.Name}) {
		return
	}
	ds, err := h.Service.CreateDataset(ctx, tenantID, req.Name)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "dataset.create", Resource: "dataset",
		ResourceID: ds.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, ds)
}

// ListDocuments lists documents of a dataset.
func (h *Handler) ListDocuments(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "document"); err != nil {
		response.Err(c, err)
		return
	}
	datasetID := c.Param("id")
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	actorTenantID := c.GetString(middleware.ContextTenantID)
	currentTenantID := scope.CurrentTenantID()
	var items []service.DocumentSummary
	var err error
	if currentTenantID == actorTenantID {
		items, err = h.Service.ListDocumentsForUser(c.Request.Context(), currentTenantID, datasetID, c.GetString(middleware.ContextUserID))
	} else {
		items, err = h.Service.ListDocumentsForScope(c.Request.Context(), scope, datasetID)
	}
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// UploadDocument uploads a document into a dataset via multipart form.
func (h *Handler) UploadDocument(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	datasetID := c.Param("id")

	file, header, err := c.Request.FormFile("file")
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "append", "document"); err != nil {
		response.Err(c, err)
		return
	}
	if err != nil {
		response.Err(c, httperr.BadRequest(40000, "file is required"))
		return
	}
	defer file.Close()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	if scope.CurrentTenantID() != tenantID {
		response.Fail(c, 400, 40000, "cross-tenant document upload is not supported")
		return
	}

	buf := make([]byte, header.Size)
	buf, err = io.ReadAll(file)
	if err != nil {
		response.Err(c, err)
		return
	}
	sourceKey := strings.TrimSpace(c.PostForm("source_key"))
	doc, err := h.Service.UploadDocumentWithSourceKey(c.Request.Context(), tenantID, datasetID, userID, header.Filename, sourceKey, buf)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, doc)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "document.upload", Resource: "document", ResourceID: datasetID, DetailJSON: header.Filename, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
}

// ListIncrementalLedger exposes the tenant's logical revision projection.
func (h *Handler) ListIncrementalLedger(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "read", "document"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "document")
	if !ok {
		return
	}
	rows, err := h.Service.Store.ListIncrementalLedger(ctx, scope.CurrentTenantID(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

type incrementalScanRequest struct {
	Events   []service.IncrementalSourceEvent `json:"events"`
	Snapshot bool                             `json:"snapshot"`
}

// ApplyIncrementalSourceBatch accepts a bounded connector snapshot. The batch
// can add/change documents and tombstone sources absent from the snapshot.
func (h *Handler) ApplyIncrementalSourceBatch(c *gin.Context) {
	var req incrementalScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40295, "invalid incremental batch payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	result, err := h.Service.ApplyIncrementalSourceBatch(ctx, scope.CurrentTenantID(), c.Param("id"), userID, req.Events, req.Snapshot)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

type parseRequest struct {
	DocumentIDs []string `json:"document_ids"`
}

// ParseDocuments triggers parsing for documents of a dataset.
func (h *Handler) ParseDocuments(c *gin.Context) {
	var req parseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid parse payload")
		return
	}
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	datasetID := c.Param("id")
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	if crossTenant {
		if len(req.DocumentIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant document operations require one selected document")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{"dataset_id": datasetID, "document_id": req.DocumentIDs[0]})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocument, model.ApprovalActionParse, req.DocumentIDs[0], payload) {
			return
		}
	}
	if err := h.Service.ParseDocuments(c.Request.Context(), tenantID, datasetID, req.DocumentIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "document.parse", Resource: "document", ResourceID: datasetID, DetailJSON: strings.Join(req.DocumentIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"parsed": len(req.DocumentIDs)})
}

// StopDocuments stops parsing for documents of a dataset.
func (h *Handler) StopDocuments(c *gin.Context) {
	var req parseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid stop payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	datasetID := c.Param("id")
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	if crossTenant {
		if len(req.DocumentIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant document operations require one selected document")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{"dataset_id": datasetID, "document_id": req.DocumentIDs[0]})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocument, model.ApprovalActionStop, req.DocumentIDs[0], payload) {
			return
		}
	}
	if err := h.Service.StopDocuments(ctx, tenantID, datasetID, req.DocumentIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: c.GetString(middleware.ContextUserID), Action: "document.stop", Resource: "document", ResourceID: datasetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"stopped": len(req.DocumentIDs)})
}

// DeleteDocuments removes documents from a dataset.
func (h *Handler) DeleteDocuments(c *gin.Context) {
	var req parseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid delete payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	datasetID := c.Param("id")
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "delete:own", "document"); err != nil {
		response.Err(c, err)
		return
	}
	if len(req.DocumentIDs) == 0 {
		response.Fail(c, 400, 40000, "document_ids is required")
		return
	}
	canDeleteAll := h.Service.Authorize(ctx, userID, "execute", "document") == nil
	if !canDeleteAll {
		if crossTenant {
			response.Fail(c, 403, 40300, "cross-tenant document deletion requires document governance")
			return
		}
		if err := h.Service.DeleteOwnedDocuments(ctx, tenantID, datasetID, userID, req.DocumentIDs); err != nil {
			response.Err(c, err)
			return
		}
		h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "document.delete.own", Resource: "document", ResourceID: datasetID, DetailJSON: strings.Join(req.DocumentIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
		response.OK(c, gin.H{"deleted": len(req.DocumentIDs)})
		return
	}
	if crossTenant {
		if len(req.DocumentIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant document operations require one selected document")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{"dataset_id": datasetID, "document_id": req.DocumentIDs[0]})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocument, model.ApprovalActionDelete, req.DocumentIDs[0], payload) {
			return
		}
	}
	if err := h.Service.DeleteDocuments(ctx, tenantID, datasetID, req.DocumentIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: c.GetString(middleware.ContextUserID), Action: "document.delete", Resource: "document", ResourceID: datasetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"deleted": len(req.DocumentIDs)})
}

// UpdateDataset renames a dataset.
// SetDocumentsStatus enables or disables the selected documents of a dataset.
func (h *Handler) SetDocumentsStatus(c *gin.Context) {
	var req struct {
		DocumentIDs []string `json:"document_ids"`
		Enabled     bool     `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid document status payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	if len(req.DocumentIDs) == 0 {
		response.Fail(c, 400, 40000, "document_ids is required")
		return
	}
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	action := model.ApprovalActionDisable
	if req.Enabled {
		action = model.ApprovalActionEnable
	}
	if crossTenant {
		if len(req.DocumentIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant document operations require one selected document")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{
			"dataset_id": datasetID, "document_id": req.DocumentIDs[0], "enabled": req.Enabled,
		})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocument, action, req.DocumentIDs[0], payload) {
			return
		}
	}
	if err := h.Service.SetDocumentsStatus(ctx, tenantID, datasetID, req.DocumentIDs, req.Enabled); err != nil {
		response.Err(c, err)
		return
	}
	actionName := "document.disable"
	if req.Enabled {
		actionName = "document.enable"
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: actionName, Resource: "document", ResourceID: strings.Join(req.DocumentIDs, ","), DetailJSON: strings.Join(req.DocumentIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"enabled": req.Enabled, "updated": len(req.DocumentIDs)})
}

// UpdateDocumentMetadata replaces a document's metadata configuration.
func (h *Handler) UpdateDocumentMetadata(c *gin.Context) {
	var req struct {
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid metadata payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	docID := c.Param("docId")
	if req.Metadata == nil {
		response.Fail(c, 400, 40000, "metadata is required")
		return
	}
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	payload, payloadErr := payloadFromRequest(map[string]any{
		"dataset_id": datasetID, "document_id": docID, "metadata": req.Metadata,
	})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocument, model.ApprovalActionUpdate, docID, payload) {
		return
	}
	if err := h.Service.UpdateDocumentMetadata(ctx, tenantID, datasetID, docID, req.Metadata); err != nil {
		response.Err(c, err)
		return
	}
	metaJSON, _ := json.Marshal(req.Metadata)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "document.metadata", Resource: "document", ResourceID: docID, DetailJSON: string(metaJSON), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"document_id": docID, "updated": true})
}

// ListDocumentChunks returns a document's parsed chunks for verification.
func (h *Handler) ListDocumentChunks(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	docID := c.Param("docId")
	if err := h.Service.Authorize(ctx, userID, "read", "document"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "document")
	if !ok {
		return
	}
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	chunks, total, err := h.Service.ListDocumentChunksForScope(ctx, scope, datasetID, docID, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, chunks, total, page, pageSize)
}

// PreviewDocument streams a document's raw file so the original can be shown
// alongside its parsed chunks.
func (h *Handler) PreviewDocument(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	docID := c.Param("docId")
	if err := h.Service.Authorize(ctx, userID, "read", "document"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "document")
	if !ok {
		return
	}
	data, ct, err := h.Service.GetDocumentContentForScope(ctx, scope, datasetID, docID)
	if err != nil {
		response.Err(c, err)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(200, ct, data)
}

// DeleteDocumentChunks deletes the selected chunks of a document.
func (h *Handler) DeleteDocumentChunks(c *gin.Context) {
	var req struct {
		ChunkIDs []string `json:"chunk_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid chunk payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	docID := c.Param("docId")
	if len(req.ChunkIDs) == 0 {
		response.Fail(c, 400, 40000, "chunk_ids is required")
		return
	}
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	if crossTenant {
		if len(req.ChunkIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant chunk operations require one selected chunk")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{
			"dataset_id": datasetID, "document_id": docID, "chunk_id": req.ChunkIDs[0],
		})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocumentChunk, model.ApprovalActionDelete, req.ChunkIDs[0], payload) {
			return
		}
	}
	if err := h.Service.DeleteChunks(ctx, tenantID, datasetID, docID, req.ChunkIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "chunk.delete", Resource: "document", ResourceID: docID, DetailJSON: strings.Join(req.ChunkIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"deleted": len(req.ChunkIDs)})
}

// SetDocumentChunksAvailable enables or disables the selected chunks.
func (h *Handler) SetDocumentChunksAvailable(c *gin.Context) {
	var req struct {
		ChunkIDs []string `json:"chunk_ids"`
		Enabled  bool     `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid chunk payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "document")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	docID := c.Param("docId")
	if len(req.ChunkIDs) == 0 {
		response.Fail(c, 400, 40000, "chunk_ids is required")
		return
	}
	if err := h.Service.Authorize(ctx, userID, "execute", "document"); err != nil {
		response.Err(c, err)
		return
	}
	action := model.ApprovalActionDisable
	if req.Enabled {
		action = model.ApprovalActionEnable
	}
	if crossTenant {
		if len(req.ChunkIDs) != 1 {
			response.Fail(c, 400, 40000, "cross-tenant chunk operations require one selected chunk")
			return
		}
		payload, payloadErr := payloadFromRequest(map[string]any{
			"dataset_id": datasetID, "document_id": docID, "chunk_id": req.ChunkIDs[0], "enabled": req.Enabled,
		})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDocumentChunk, action, req.ChunkIDs[0], payload) {
			return
		}
	}
	if err := h.Service.SetChunksAvailable(ctx, tenantID, datasetID, docID, req.ChunkIDs, req.Enabled); err != nil {
		response.Err(c, err)
		return
	}
	actionName := "chunk.disable"
	if req.Enabled {
		actionName = "chunk.enable"
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: actionName, Resource: "document", ResourceID: docID, DetailJSON: strings.Join(req.ChunkIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"enabled": req.Enabled, "updated": len(req.ChunkIDs)})
}

// GetDatasetConfig returns a dataset's parsing/embedding/permission config.
func (h *Handler) GetDatasetConfig(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	if err := h.Service.Authorize(ctx, userID, "read", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "dataset")
	if !ok {
		return
	}
	cfg, err := h.Service.GetDatasetConfigForScope(ctx, scope, datasetID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, cfg)
}

// UpdateDatasetConfig updates a dataset's config (parser, embedding, permission).
func (h *Handler) UpdateDatasetConfig(c *gin.Context) {
	var req ragflow.DatasetConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid dataset config payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	datasetID := c.Param("id")
	if err := h.Service.Authorize(ctx, userID, "manage", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	payload, err := payloadFromRequest(req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDataset, model.ApprovalActionUpdate, datasetID, payload) {
		return
	}
	if err := h.Service.UpdateDatasetConfig(ctx, tenantID, datasetID, req); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "dataset.config", Resource: "dataset", ResourceID: datasetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": datasetID, "updated": true})
}
func (h *Handler) UpdateDataset(c *gin.Context) {

	var req updateDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid dataset payload")
		return
	}
	ctx := c.Request.Context()
	scope, ok := h.resolveGovernanceWriteScope(c, "dataset")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "dataset"); err != nil {
		response.Err(c, err)
		return
	}
	datasetID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectDataset, model.ApprovalActionUpdate, datasetID, map[string]any{"name": req.Name}) {
		return
	}
	ds, err := h.Service.UpdateDataset(ctx, tenantID, datasetID, req.Name)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "dataset.update", Resource: "dataset", ResourceID: datasetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, ds)
}

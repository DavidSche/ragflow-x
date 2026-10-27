package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestLogicalDocumentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "logical-document-handler")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(t.Context(), tenant.ID, "Logical Documents")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, "admin")
		c.Next()
	})
	RegisterLogicalDocumentRoutes(group)

	payload, _ := json.Marshal(service.LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Policy", SourceURI: "connector:policy", Description: "governed",
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/logical-documents", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create logical document: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var createBody struct {
		Data model.LogicalDocument `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.Data.ID == "" || createBody.Data.DatasetID != dataset.ID {
		t.Fatalf("unexpected logical document: %+v", createBody.Data)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-documents?dataset_id="+dataset.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list logical documents: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var listBody struct {
		Data struct {
			Items []model.LogicalDocument `json:"items"`
			Total int64                   `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data.Items) != 1 || listBody.Data.Total != 1 {
		t.Fatalf("unexpected logical document page: %+v", listBody.Data)
	}

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	version, _, err := svc.CreateDocumentVersion(t.Context(), tenant.ID, createBody.Data.ID, "admin", service.DocumentVersionInput{
		RAGFlowDocumentID: "ragflow-doc-1", ContentHash: "7777777777777777777777777777777777777777777777777777777777777777",
		EffectiveFrom: from, ChangeSummary: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-documents/"+createBody.Data.ID+"/versions", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list versions: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var versionBody struct {
		Data struct {
			Items []model.DocumentVersion `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &versionBody); err != nil {
		t.Fatal(err)
	}
	if len(versionBody.Data.Items) != 1 || versionBody.Data.Items[0].ID != version.ID {
		t.Fatalf("unexpected version page: %+v", versionBody.Data)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/logical-documents/"+createBody.Data.ID, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete with versions: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	updated := "updated"
	payload, _ = json.Marshal(service.LogicalDocumentPatchInput{Description: &updated})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/v1/logical-documents/"+createBody.Data.ID, bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update logical document: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updateBody struct {
		Data model.LogicalDocument `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updateBody); err != nil {
		t.Fatal(err)
	}
	if updateBody.Data.Description != "updated" {
		t.Fatalf("unexpected updated logical document: %+v", updateBody.Data)
	}

	otherTenant, err := svc.CreateTenant(t.Context(), "logical-document-handler-other")
	if err != nil {
		t.Fatal(err)
	}
	otherRouter := gin.New()
	otherGroup := otherRouter.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, otherTenant.ID)
		c.Set(middleware.ContextUserID, "admin")
		c.Next()
	})
	RegisterLogicalDocumentRoutes(otherGroup)
	recorder = httptest.NewRecorder()
	otherRouter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-documents/"+createBody.Data.ID, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant logical document read: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLogicalDocumentSupersedeAndPublishAttemptsRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "logical-document-publish-handler")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(t.Context(), tenant.ID, "Publish Documents")
	if err != nil {
		t.Fatal(err)
	}
	logical, err := svc.CreateLogicalDocument(t.Context(), tenant.ID, "admin", service.LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Publish Policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	previousDoc, err := svc.RAGFlow.CreateDocument(t.Context(), dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "publish-v1"})
	if err != nil {
		t.Fatal(err)
	}
	newDoc, err := svc.RAGFlow.CreateDocument(t.Context(), dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "publish-v2"})
	if err != nil {
		t.Fatal(err)
	}
	previousFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	newFrom := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(t.Context(), tenant.ID, logical.ID, "admin", service.DocumentVersionInput{
		RAGFlowDocumentID: previousDoc.ID, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EffectiveFrom: previousFrom,
	})
	if err != nil {
		t.Fatal(err)
	}
	newVersion, _, err := svc.CreateDocumentVersion(t.Context(), tenant.ID, logical.ID, "admin", service.DocumentVersionInput{
		RAGFlowDocumentID: newDoc.ID, ContentHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		EffectiveFrom: newFrom,
	})
	if err != nil {
		t.Fatal(err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	previous.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateDocumentVersion(t.Context(), previous); err != nil {
		t.Fatal(err)
	}
	if err := svc.TransitionDocumentVersion(t.Context(), tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatal(err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(t.Context(), tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive); err != nil || !changed {
		t.Fatalf("activate previous: changed=%t err=%v", changed, err)
	}

	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, "admin")
		c.Next()
	})
	RegisterLogicalDocumentRoutes(group)
	payload, _ := json.Marshal(service.SupersedeDocumentVersionInput{VersionID: newVersion.ID, Reason: "handler contract"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/logical-documents/"+logical.ID+"/supersede", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("supersede version: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var publishBody struct {
		Data service.VersionPublishResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &publishBody); err != nil {
		t.Fatal(err)
	}
	if publishBody.Data.PublishAttemptID == "" || publishBody.Data.PublishedVersion != newVersion.Version {
		t.Fatalf("unexpected publish result: %+v", publishBody.Data)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-documents/"+logical.ID+"/publish-attempts", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list publish attempts: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var attemptsBody struct {
		Data struct {
			Items []model.VersionPublishAttempt `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &attemptsBody); err != nil {
		t.Fatal(err)
	}
	if len(attemptsBody.Data.Items) != 1 || attemptsBody.Data.Items[0].State != model.VersionPublishCommitted {
		t.Fatalf("unexpected publish attempts: %+v", attemptsBody.Data)
	}

	recorder = httptest.NewRecorder()
	restorePayload, _ := json.Marshal(service.SupersedeDocumentVersionInput{VersionID: previous.ID, Reason: "restore handler contract"})
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/logical-documents/"+logical.ID+"/restore", bytes.NewReader(restorePayload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore version: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var restoreBody struct {
		Data service.VersionPublishResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &restoreBody); err != nil {
		t.Fatal(err)
	}
	if restoreBody.Data.PublishedVersion != previous.Version {
		t.Fatalf("unexpected restore result: %+v", restoreBody.Data)
	}
}

func openHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "logical-document-handler.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

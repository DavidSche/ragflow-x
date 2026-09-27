package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// newPushdownTestService builds an isolated sqlite-backed service with the
// real mock provider, mirroring the agent_attachment_test setup.
func newPushdownTestService(t *testing.T) (*Service, *model.Tenant) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "pushdown.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	tenant, err := svc.CreateTenant(context.Background(), "PushdownTenant")
	if err != nil {
		t.Fatal(err)
	}
	return svc, &tenant
}

// mustCreatePushdownDataset creates a dataset link with the given governance
// classification and pushdown enrollment (doc/123 §3).
func (s *Service) mustCreatePushdownDataset(t *testing.T, tenantID, sensitivity string, pushdown bool) *model.DatasetLink {
	t.Helper()
	ctx := context.Background()
	created, err := s.CreateDataset(ctx, tenantID, "ds-"+sensitivity+"-"+id.New())
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.Store.GetDatasetLink(ctx, tenantID, created.ID)
	if err != nil || link == nil {
		t.Fatalf("dataset link missing: %v", err)
	}
	link.Sensitivity = sensitivity
	link.PushdownEnabled = pushdown
	updated, err := s.Store.UpdateDatasetLifecycle(ctx, link)
	if err != nil || !updated {
		t.Fatalf("lifecycle update failed: %v %v", updated, err)
	}
	fresh, err := s.Store.GetDatasetLink(ctx, tenantID, created.ID)
	if err != nil || fresh == nil {
		t.Fatal(err)
	}
	return fresh
}

// mustUploadDocuments uploads n documents into a dataset via the mock.
func (s *Service) mustUploadDocuments(t *testing.T, tenantID, datasetID string, n int) {
	t.Helper()
	ctx := context.Background()
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || link == nil {
		t.Fatalf("dataset link missing: %v", err)
	}
	for i := 0; i < n; i++ {
		_, err := s.RAGFlow.CreateDocument(ctx, link.RAGFlowDatasetID, &ragflow.DocumentUpload{
			Name: fmt.Sprintf("doc-%d.txt", i), Content: []byte("pushdown fixture"),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

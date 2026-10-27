package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestReconcileExternalResourcesRemovesMissingShadows(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Reconciliation")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "missing-dataset")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "missing-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	mock := svc.RAGFlow.(*ragflow.Mock)
	if err := mock.DeleteDataset(ctx, dataset.RAGFlowDatasetID); err != nil {
		t.Fatal(err)
	}
	if err := mock.DeleteChat(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileExternalResources(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetDataset(ctx, tenant.ID, dataset.ID); err == nil {
		t.Fatal("missing dataset shadow must be reconciled")
	}
	if _, err := svc.GetChat(ctx, tenant.ID, chat.ID, false); err == nil {
		t.Fatal("missing chat shadow must be reconciled")
	}
}

func TestResourceReconciliationWorkerRuns(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "ReconciliationWorker")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "missing-dataset")
	if err != nil {
		t.Fatal(err)
	}
	mock := svc.RAGFlow.(*ragflow.Mock)
	if err := mock.DeleteDataset(ctx, dataset.RAGFlowDatasetID); err != nil {
		t.Fatal(err)
	}
	svc.SetupWorker(fastWorkerConfig())
	svc.Runner.Start(ctx)
	t.Cleanup(func() { _ = svc.Runner.Stop(context.Background()) })

	inserted, err := svc.EnqueueResourceReconciliation(ctx, tenant.ID)
	if err != nil || !inserted {
		t.Fatalf("enqueue reconciliation: inserted=%v err=%v", inserted, err)
	}
	job := waitJobStatus(t, svc.Store, tenant.ID, model.JobKindResourceReconciliation, model.JobStatusSucceeded, 8*time.Second)
	if job.LastError != "" {
		t.Fatalf("job failed: %s", job.LastError)
	}
	if _, err := svc.GetDataset(ctx, tenant.ID, dataset.ID); err == nil {
		t.Fatal("worker did not remove missing dataset shadow")
	}
}

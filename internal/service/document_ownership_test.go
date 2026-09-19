package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestDocumentUploadAutoParseAndOwnedDelete(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Document Ownership Tenant")
	if err != nil {
		t.Fatal(err)
	}
	uploader, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "doc-uploader", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "other-user", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Contributor Docs")
	if err != nil {
		t.Fatal(err)
	}
	document, err := svc.UploadDocument(ctx, tenant.ID, dataset.ID, uploader.ID, "policy.md", []byte("business content"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}

	uploaderDocs, err := svc.ListDocumentsForUser(ctx, tenant.ID, dataset.ID, uploader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploaderDocs) != 1 || !uploaderDocs[0].OwnedByMe || uploaderDocs[0].Status != "parsed" {
		t.Fatalf("uploader view should own an auto-parsed document: %+v", uploaderDocs)
	}
	otherDocs, err := svc.ListDocumentsForUser(ctx, tenant.ID, dataset.ID, otherUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherDocs) != 1 || otherDocs[0].OwnedByMe {
		t.Fatalf("other user should not own uploaded document: %+v", otherDocs)
	}

	if err := svc.DeleteOwnedDocuments(ctx, tenant.ID, dataset.ID, otherUser.ID, []string{document.ID}); err == nil {
		t.Fatal("other user should not delete another user's document")
	}
	if err := svc.DeleteOwnedDocuments(ctx, tenant.ID, dataset.ID, uploader.ID, []string{document.ID}); err != nil {
		t.Fatalf("uploader should delete owned document: %v", err)
	}
	docs, err := svc.ListDocuments(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("owned document should be deleted: %+v", docs)
	}
	owners, err := svc.Store.ListDocumentOwners(ctx, tenant.ID, dataset.ID, []string{document.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("ownership should be removed: %+v", owners)
	}
}

func TestDeleteDatasetCleansDocumentOwnership(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Document Ownership Cleanup Tenant")
	if err != nil {
		t.Fatal(err)
	}
	uploader, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "doc-cleanup-uploader", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Cleanup Docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UploadDocument(ctx, tenant.ID, dataset.ID, uploader.ID, "cleanup.md", []byte("cleanup content")); err != nil {
		t.Fatalf("upload document: %v", err)
	}
	if err := svc.DeleteDataset(ctx, tenant.ID, dataset.ID); err != nil {
		t.Fatalf("delete dataset: %v", err)
	}
	owners, err := svc.Store.ListDocumentOwners(ctx, tenant.ID, dataset.ID, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("dataset ownership should be removed: %+v", owners)
	}
}

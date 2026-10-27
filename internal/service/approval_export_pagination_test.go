package service

// ScenarioID: SC-APPROVAL-001

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"testing"
	"time"

	"path/filepath"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type stableExportStore struct {
	repository.Store
	approvals   map[string]model.Approval
	idCalls     int
	detailCalls []int
}

func (store *stableExportStore) ListApprovals(context.Context, string, repository.ApprovalFilter, repository.ApprovalListQuery) ([]model.Approval, int64, error) {
	return nil, 0, fmt.Errorf("export must not use offset pagination")
}

func (store *stableExportStore) ListAllApprovals(context.Context, repository.ApprovalFilter, repository.ApprovalListQuery) ([]model.Approval, int64, error) {
	return nil, 0, fmt.Errorf("export must not use offset pagination")
}

func (store *stableExportStore) ListApprovalExportIDs(
	context.Context, bool, string, repository.ApprovalFilter, repository.ApprovalListQuery,
) ([]string, int64, error) {
	store.idCalls++
	ids := make([]string, 0, len(store.approvals))
	for index := 0; index < len(store.approvals); index++ {
		ids = append(ids, fmt.Sprintf("approval-%03d", index))
	}
	return ids, int64(len(ids)), nil
}

func (store *stableExportStore) ListApprovalsByIDs(
	_ context.Context, _ bool, _ string, ids []string,
) ([]model.Approval, error) {
	store.detailCalls = append(store.detailCalls, len(ids))
	approvals := make([]model.Approval, 0, len(ids))
	for _, approvalID := range ids {
		approvals = append(approvals, store.approvals[approvalID])
	}
	return approvals, nil
}

func TestP0_APPROVAL_001_ExportUsesStableIDSnapshotPages(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "approval-export.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, model.PlatformTenantID, "platform_admin", CreateUserRequest{
		Username: "export-admin-" + id.New()[:8], Password: "password123", Role: "platform_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	const approvalCount = 101
	base := &stableExportStore{Store: svc.Store, approvals: make(map[string]model.Approval, approvalCount)}
	now := time.Now().UTC()
	for index := range approvalCount {
		approvalID := fmt.Sprintf("approval-%03d", index)
		base.approvals[approvalID] = model.Approval{
			ID: approvalID, TenantID: model.PlatformTenantID, RequestNo: "APR-" + strconv.Itoa(index),
			ObjectType: model.ApprovalObjectDataset, ObjectID: "dataset", Action: model.ApprovalActionDelete,
			Title: "approval " + strconv.Itoa(index), Status: model.ApprovalStatusPendingApproval,
			RequesterID: admin.ID, CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
	}
	svc.Store = base

	var output bytes.Buffer
	rows, err := svc.ExportApprovals(ctx, admin.ID, model.PlatformTenantID, ApprovalListRequest{}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if rows != approvalCount {
		t.Fatalf("rows = %d, want %d", rows, approvalCount)
	}
	if base.idCalls != 1 {
		t.Fatalf("stable ID calls = %d, want 1", base.idCalls)
	}
	if len(base.detailCalls) != 2 || base.detailCalls[0] != 100 || base.detailCalls[1] != 1 {
		t.Fatalf("detail page sizes = %v, want [100 1]", base.detailCalls)
	}
	reader := csv.NewReader(&output)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != approvalCount+1 {
		t.Fatalf("records = %d, want %d", len(records), approvalCount+1)
	}
	seen := make(map[string]bool, approvalCount)
	for index, record := range records[1:] {
		requestNo := "APR-" + strconv.Itoa(index)
		if record[0] != requestNo {
			t.Fatalf("record %d request_no = %q, want %q", index, record[0], requestNo)
		}
		if seen[requestNo] {
			t.Fatalf("duplicate request_no %q", requestNo)
		}
		seen[requestNo] = true
	}
}

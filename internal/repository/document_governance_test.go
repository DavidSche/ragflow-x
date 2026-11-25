package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/logger"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func newGovernanceRepoStore(t *testing.T) Store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "governance-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func governanceRecord(tenantID, datasetID, documentID, level string, status string, state string) *model.DocumentGovernance {
	now := time.Now().UTC()
	rec := &model.DocumentGovernance{
		ID: id.New(), TenantID: tenantID, DatasetID: datasetID, DocumentID: documentID,
		Level: level, Authority: model.GovernanceDefaultAuthority, Status: status,
		Source: model.GovernanceSourceManual, ApprovalState: state,
		CreatedBy: id.New(), CreatedAt: now, UpdatedAt: now,
	}
	if state == model.GovernanceApprovalApproved {
		rec.ApprovedBy = &rec.CreatedBy
		rec.ApprovedAt = &now
	}
	return rec
}

func TestUpsertDocumentGovernanceIsIdempotentPerDocument(t *testing.T) {
	ctx := context.Background()
	s := newGovernanceRepoStore(t)
	tenant, ds, doc := id.New(), id.New(), id.New()

	if err := s.UpsertDocumentGovernance(ctx, governanceRecord(tenant, ds, doc, model.GovernanceLevelInternal, model.GovernanceStatusPublished, model.GovernanceApprovalApproved)); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.UpsertDocumentGovernance(ctx, governanceRecord(tenant, ds, doc, model.GovernanceLevelTopSecret, model.GovernanceStatusDraft, model.GovernanceApprovalPending)); err != nil {
		t.Fatalf("second upsert should be an on-conflict update: %v", err)
	}
	got, err := s.GetDocumentGovernance(ctx, tenant, ds, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected annotation after upsert")
	}
	if got.Level != model.GovernanceLevelTopSecret {
		t.Fatalf("expected level updated to %s, got %s", model.GovernanceLevelTopSecret, got.Level)
	}
	if got.Status != model.GovernanceStatusDraft {
		t.Fatalf("expected status updated to draft, got %s", got.Status)
	}
	if got.ApprovalState != model.GovernanceApprovalPending {
		t.Fatalf("expected approval_state pending after update, got %s", got.ApprovalState)
	}
	// OnConflict must not duplicate rows for the same (dataset, document).
	items, err := s.ListDatasetDocumentGovernance(ctx, tenant, ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one row, got %d", len(items))
	}
}

func TestListDocumentGovernanceFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	s := newGovernanceRepoStore(t)
	tenant := id.New()
	dsA, dsB := id.New(), id.New()
	doc := id.New()

	mustUp := func(r *model.DocumentGovernance) {
		t.Helper()
		if err := s.UpsertDocumentGovernance(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	mustUp(governanceRecord(tenant, dsA, doc, model.GovernanceLevelInternal, model.GovernanceStatusPublished, model.GovernanceApprovalApproved))
	mustUp(governanceRecord(tenant, dsA, doc, model.GovernanceLevelTopSecret, model.GovernanceStatusDraft, model.GovernanceApprovalPending))
	mustUp(governanceRecord(tenant, dsB, doc, model.GovernanceLevelConfidential, model.GovernanceStatusPublished, model.GovernanceApprovalApproved))

	byDataset, total, err := s.ListDocumentGovernance(ctx, tenant, DocumentGovernanceFilter{DatasetID: dsA}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byDataset) != 1 || byDataset[0].ApprovalState != model.GovernanceApprovalPending {
		t.Fatalf("dsA expected the single updated row (pending), got total=%d len=%d", total, len(byDataset))
	}
	byLevel, total, err := s.ListDocumentGovernance(ctx, tenant, DocumentGovernanceFilter{Level: model.GovernanceLevelConfidential}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byLevel) != 1 || byLevel[0].DatasetID != dsB {
		t.Fatalf("level filter expected dsB only, total=%d", total)
	}
	pending, total, err := s.ListDocumentGovernance(ctx, tenant, DocumentGovernanceFilter{ApprovePending: true}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(pending) != 1 || pending[0].ApprovalState != model.GovernanceApprovalPending {
		t.Fatalf("approve_pending expected 1, got total=%d", total)
	}
	if _, total, err = s.ListDocumentGovernance(ctx, tenant, DocumentGovernanceFilter{ApprovalState: model.GovernanceApprovalApproved}, 1, 10); err != nil {
		t.Fatal(err)
	} else if total != 1 {
		t.Fatalf("approved filter expected 1, got %d", total)
	}
	// Pagination: page 1 size 1 must return 1 row while total stays 2.
	page1, total, err := s.ListDocumentGovernance(ctx, tenant, DocumentGovernanceFilter{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page1) != 1 {
		t.Fatalf("pagination total=2 got total=%d len=%d", total, len(page1))
	}
}

// captureSQLLogger records the SQL statements gorm builds. The dialect accepts the
// grouped read silently (id is the primary key, so PostgreSQL allows the projection
// through functional dependency), which means only the statement shape can catch a
// GROUP BY leaking from the count into the paging read.
type captureSQLLogger struct {
	logger.Interface
	stmts []string
}

func (c *captureSQLLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	c.stmts = append(c.stmts, sql)
}

func TestListDocumentGovernanceGroupsCountOnly(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "governance-sql-shape.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	// The statements are captured on a live connection: dry-run leaves the previous
	// statement's SELECT clause on the builder, so its SQL shape proves nothing here.
	capture := &captureSQLLogger{}
	gdb.Logger = capture
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })

	if _, _, err := s.ListDocumentGovernance(context.Background(), "tenant", DocumentGovernanceFilter{}, 1, 10); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(capture.stmts) != 2 {
		t.Fatalf("expected count + read statements, got %d: %q", len(capture.stmts), capture.stmts)
	}
	var countSQL, readSQL string
	for _, stmt := range capture.stmts {
		if strings.Contains(strings.ToUpper(stmt), "GROUP BY") {
			countSQL = stmt
		} else {
			readSQL = stmt
		}
	}
	if countSQL == "" {
		t.Fatalf("count query lost its GROUP BY: %q", capture.stmts)
	}
	if readSQL == "" {
		t.Fatalf("paging read must not carry GROUP BY: %q", capture.stmts)
	}
	if !strings.Contains(strings.ToUpper(readSQL), "ORDER BY") {
		t.Fatalf("expected the paging read to keep its ORDER BY, got %q", readSQL)
	}
}

func TestListDatasetDocumentGovernanceTenantScoped(t *testing.T) {
	ctx := context.Background()
	s := newGovernanceRepoStore(t)
	tenantA, tenantB := id.New(), id.New()
	ds, doc := id.New(), id.New()

	if err := s.UpsertDocumentGovernance(ctx, governanceRecord(tenantA, ds, doc, model.GovernanceLevelInternal, model.GovernanceStatusPublished, model.GovernanceApprovalApproved)); err != nil {
		t.Fatal(err)
	}
	itemsB, err := s.ListDatasetDocumentGovernance(ctx, tenantB, ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsB) != 0 {
		t.Fatalf("tenant B must not see tenant A annotations, list=%d", len(itemsB))
	}
	itemsA, err := s.ListDatasetDocumentGovernance(ctx, tenantA, ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsA) != 1 {
		t.Fatalf("tenant A expected 1 annotation, got %d", len(itemsA))
	}
}

func TestDeleteDocumentGovernance(t *testing.T) {
	ctx := context.Background()
	s := newGovernanceRepoStore(t)
	tenant, ds, doc := id.New(), id.New(), id.New()

	if err := s.UpsertDocumentGovernance(ctx, governanceRecord(tenant, ds, doc, model.GovernanceLevelConfidential, model.GovernanceStatusPublished, model.GovernanceApprovalApproved)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDocumentGovernance(ctx, tenant, ds, doc); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetDocumentGovernance(ctx, tenant, ds, doc); err != nil || got != nil {
		t.Fatalf("expected deleted record, got=%v err=%v", got, err)
	}
}

func TestUserClearanceUpsertAndAbsentDefault(t *testing.T) {
	ctx := context.Background()
	s := newGovernanceRepoStore(t)
	tenant, userA, userB := id.New(), id.New(), id.New()

	if got, err := s.GetUserClearance(ctx, tenant, userA); err != nil || got != nil {
		t.Fatalf("absent clearance should be nil, got=%v err=%v", got, err)
	}
	c := &model.UserClearance{ID: id.New(), TenantID: tenant, UserID: userA, Level: model.GovernanceLevelTopSecret, UpdatedBy: id.New(), UpdatedAt: time.Now().UTC()}
	if err := s.SetUserClearance(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Upsert again must not duplicate the (tenant,user) row.
	c.Level = model.GovernanceLevelInternal
	elevated := &model.UserClearance{ID: id.New(), TenantID: tenant, UserID: userA, Level: model.GovernanceLevelInternal, UpdatedBy: id.New(), UpdatedAt: time.Now().UTC()}
	if err := s.SetUserClearance(ctx, elevated); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserClearance(ctx, tenant, userA)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Level != model.GovernanceLevelInternal {
		t.Fatalf("expected downgraded clearance internal, got=%+v", got)
	}
	if _, err := s.GetUserClearance(ctx, tenant, userB); err != nil || got == nil {
		t.Fatalf("user B must be unaffected")
	}
}

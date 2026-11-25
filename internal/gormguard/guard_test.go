package gormguard

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mustAnalyze(t *testing.T, name, src string) []Finding {
	t.Helper()
	findings, err := analyzeSource(name, []byte(src))
	if err != nil {
		t.Fatalf("analyze %s: %v", name, err)
	}
	return findings
}

// TestFlagsTheKnowledgeOpsShape is the regression the check exists for: the code that
// produced `SELECT * ... GROUP BY id` on PostgreSQL and a 500 on the dashboard.
func TestFlagsTheKnowledgeOpsShape(t *testing.T) {
	src := `package fixture

func (s *store) ListDocumentGovernance(ctx context.Context, tenantID string) ([]Row, int64, error) {
	q := s.WithContext(ctx).Model(&Row{}).Where("tenant_id = ?", tenantID)
	var items []Row
	var total int64
	q.Group("id")
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("updated_at DESC").Limit(10).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}`
	findings := mustAnalyze(t, "fixture.go", src)
	if len(findings) != 1 {
		t.Fatalf("the shared builder must be reported once, got %d: %v", len(findings), findings)
	}
	got := findings[0]
	if got.Builder != "q" || got.Func != "ListDocumentGovernance" {
		t.Fatalf("wrong builder/function: %+v", got)
	}
	if len(got.Clauses) != 1 || got.Clauses[0] != "Group" {
		t.Fatalf("expected the leaked clause to be Group, got %v", got.Clauses)
	}
	if got.CountLine == 0 || got.ReadLine == 0 || got.CountLine == got.ReadLine {
		t.Fatalf("count and read lines must both be reported: %+v", got)
	}
}

// TestFlagsReassignedShapeClause covers the other spelling of the same mutation:
// `q = q.Group("id")` rather than a bare call.
func TestFlagsReassignedShapeClause(t *testing.T) {
	src := `package fixture

func List(q *gorm.DB, items *[]Row, total *int64) error {
	q = q.Group("id")
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Find(items).Error
}`
	if findings := mustAnalyze(t, "fixture.go", src); len(findings) != 1 {
		t.Fatalf("reassignment mutates the same builder, so it must be reported: %v", findings)
	}
}

// TestJoinOnlyShareIsAWarning covers the join that is shared on purpose:
// ListEnterpriseConnections counts and pages the same joined rows, which is correct
// while the join is one-to-one. It is reported so the next Group/Distinct on that
// builder is a visible escalation, but it must not fail the build.
func TestJoinOnlyShareIsAWarning(t *testing.T) {
	src := `package fixture

func ListEnterpriseConnections(q *gorm.DB, list *[]Row, total *int64) error {
	q.Joins("JOIN version ON version.id = row.id")
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Order("row.created_at DESC").Find(list).Error
}`
	findings := mustAnalyze(t, "fixture.go", src)
	if len(findings) != 1 {
		t.Fatalf("a shared join must be reported, got %v", findings)
	}
	if findings[0].Severity != SeverityWarning {
		t.Fatalf("a join alone multiplies both statements identically, so it is a warning: %+v", findings[0])
	}
}

// TestJoinWithGroupIsAnError is the escalation the incident's comment predicted: the
// join is what turns the tolerated `GROUP BY <primary key>` into a 42803.
func TestJoinWithGroupIsAnError(t *testing.T) {
	src := `package fixture

func List(q *gorm.DB, list *[]Row, total *int64) error {
	q.Joins("JOIN version ON version.id = row.id").Group("row.id")
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Find(list).Error
}`
	findings := mustAnalyze(t, "fixture.go", src)
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("Group must escalate the shared join to an error: %v", findings)
	}
}

// TestFlagsEachShapeClause keeps the clause list honest: the incident named Group, but
// every clause in clauseMethods changes the statement the read receives.
func TestFlagsEachShapeClause(t *testing.T) {
	for clause := range clauseMethods {
		src := `package fixture

func List(q *gorm.DB, items *[]Row, total *int64) error {
	q.` + clause + `("tenant_id")
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Find(items).Error
}`
		findings := mustAnalyze(t, "fixture.go", src)
		if len(findings) != 1 {
			t.Fatalf("%s must be reported as leaking into the read, got %v", clause, findings)
		}
		if findings[0].Clauses[0] != clause {
			t.Fatalf("expected %s, got %v", clause, findings[0].Clauses)
		}
	}
}

// TestAcceptsSessionIsolatedCount is the fix for the incident, and the shape the error
// message recommends. It must stay green or the check will teach people to ignore it.
func TestAcceptsSessionIsolatedCount(t *testing.T) {
	src := `package fixture

func List(q *gorm.DB, items *[]Row, total *int64) error {
	if err := q.Session(&gorm.Session{}).Group("id").Count(total).Error; err != nil {
		return err
	}
	return q.Order("updated_at DESC").Find(items).Error
}`
	if findings := mustAnalyze(t, "fixture.go", src); len(findings) != 0 {
		t.Fatalf("Session(&gorm.Session{}) gives the count its own statement: %v", findings)
	}
}

// TestAcceptsSeparateBuilders covers listTenantPage's shape: one builder per statement.
func TestAcceptsSeparateBuilders(t *testing.T) {
	src := `package fixture

func listTenantPage[T any](db *gorm.DB, dest *[]T, query, order string, page, pageSize int) ([]T, int64, error) {
	var total int64
	countQuery := db.WithContext(ctx).Model(dest).Where(query)
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	listQuery := db.WithContext(ctx).Model(dest).Where(query)
	if err := listQuery.Group("id").Order(order).Offset(0).Limit(10).Find(dest).Error; err != nil {
		return nil, 0, err
	}
	return *dest, total, nil
}`
	if findings := mustAnalyze(t, "fixture.go", src); len(findings) != 0 {
		t.Fatalf("two builders cannot leak into each other: %v", findings)
	}
}

// TestIgnoresPlainPagination is the 50-method case: Where/Order/Offset/Limit shared by
// count and read is correct, because none of those changes the statement's shape.
func TestIgnoresPlainPagination(t *testing.T) {
	src := `package fixture

func ListJobs(q *gorm.DB, items *[]Row, total *int64) error {
	q.Where("tenant_id = ?", "t").Order("created_at DESC")
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Offset(0).Limit(20).Find(items).Error
}`
	if findings := mustAnalyze(t, "fixture.go", src); len(findings) != 0 {
		t.Fatalf("plain pagination is not a shape leak: %v", findings)
	}
}

// TestFlagsShapeOnlyOnTheRead is the mirrored bug: the count is ungrouped while the read
// groups, so the two statements describe different result sets.
func TestFlagsShapeOnlyOnTheRead(t *testing.T) {
	src := `package fixture

func List(q *gorm.DB, items *[]Row, total *int64) error {
	if err := q.Count(total).Error; err != nil {
		return err
	}
	return q.Group("dataset_id").Find(items).Error
}`
	findings := mustAnalyze(t, "fixture.go", src)
	if len(findings) != 1 {
		t.Fatalf("a read that groups where the count does not is also a shared-builder bug: %v", findings)
	}
}

// TestRepositoryIsClean is the standing check: this repository's pagination methods all
// use either a separate builder or a Session-isolated count. A new shared Group/Select/
// Joins must fail here, in review, rather than as a PostgreSQL 500.
func TestRepositoryIsClean(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the package directory")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "internal")
	findings, err := ScanDirs(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	var problems, warnings []string
	for _, f := range findings {
		if f.Severity == SeverityError {
			problems = append(problems, f.String())
		} else {
			warnings = append(warnings, f.String())
		}
	}
	for _, w := range warnings {
		t.Logf("shared join (one-to-one today): %s", w)
	}
	if len(problems) != 0 {
		t.Fatalf("shared GORM builders found:\n%s", strings.Join(problems, "\n"))
	}
}

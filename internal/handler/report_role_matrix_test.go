package handler

// A19 (complete) — the six-role × report-route matrix (doc/288 §7.6 / §14.3).
//
// P0-A proved the *isolation* faces (repository by-id, handler object-level,
// download, list) for one principal. A19's remaining half is the RBAC axis: every
// role must be answered by the *right* gate on every report route, because the two
// answers mean different things to a caller and to an auditor:
//
//   - `403` "your role may not do this" — a property of the role, independent of
//     whether the addressed object exists. It is safe to answer precisely because it
//     says nothing about rows.
//   - `404` / 44109 "there is nothing here for you" — the object gate, reached only
//     after the role gate passed. A role that *may* read reports must get this on a
//     guessed id, never `403` (which would be a lie about its permissions) and never
//     `200` (which would be a leak).
//
// The test is written as a table over the routes the surface actually registers, so
// adding a report route without deciding who may call it fails here instead of
// shipping a silently-open endpoint. `platform_admin` is included even though it is
// built at platform scope: it is one of the six roles and its read access across
// tenants is a deliberate governance exception (§13), asserted explicitly rather
// than assumed.

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// reportMatrixRoute is one row of the matrix: the surface address plus the action
// that governs it. `body` is what a permitted caller would send — the request has to
// be *plausible* so a `403` cannot be blamed on malformed input.
type reportMatrixRoute struct {
	name   string
	method string
	path   string
	action string
	// key carries an Idempotency-Key where the route demands one. A matrix probe
	// must be a *well-formed* request: a 400 for a missing header would hide the
	// RBAC answer behind a validation answer and the matrix would prove nothing.
	key  string
	body interface{}
}

// reportMatrixRoutes is the P0-B report surface minus the two retention routes. Those
// two are gated by the router's access rules (their handlers read/write the tenant
// overlay and never touch a report row), so their role matrix lives in
// internal/router/report_access_matrix_test.go where the real rule table is visible.
func reportMatrixRoutes(reportID, jobID, key string) []reportMatrixRoute {
	return []reportMatrixRoute{
		{"create", http.MethodPost, "/api/v1/reports", "execute", "key-matrix-create-0001", reportCreateBody("ds-any")},
		{"list", http.MethodGet, "/api/v1/reports", "read", "", nil},
		{"detail", http.MethodGet, "/api/v1/reports/" + reportID, "read", "", nil},
		{"delete", http.MethodDelete, "/api/v1/reports/" + reportID, "delete:own", "", nil},
		// A47: restoring is the same action as deleting, so it belongs in the same row
		// of the table — a route that undoes a delete must not be easier to reach than
		// the delete itself.
		{"restore", http.MethodPost, "/api/v1/reports/" + reportID + "/restore", "delete:own", "", nil},
		{"cancel", http.MethodPost, "/api/v1/reports/" + reportID + "/cancel", "execute", "", nil},
		// Sharing and the governed budget request (doc/296 §2.1–§2.2) are both
		// `execute` on the report resource, so they must answer the same role matrix
		// as a rerun: a guessed id is 44109 for a permitted role, 403 for `viewer`.
		{"access", http.MethodPut, "/api/v1/reports/" + reportID + "/access", "execute", "",
			map[string]interface{}{"policy": "owner_only"}},
		{"budget-extension", http.MethodPost, "/api/v1/reports/" + reportID + "/budget-extension", "execute", "",
			map[string]interface{}{"tokens": 100}},
		{"regenerate", http.MethodPost, "/api/v1/reports/" + reportID + "/revisions", "execute", key, nil},
		{"revision", http.MethodGet, "/api/v1/reports/" + reportID + "/revisions/1", "read", "", nil},
		// v2.11: the reading surface is two more read routes. They belong in the same
		// table: "can a viewer read a report" is one question, no matter how many
		// endpoints the reader is split across.
		{"revision-list", http.MethodGet, "/api/v1/reports/" + reportID + "/revisions", "read", "", nil},
		{"revision-sections", http.MethodGet, "/api/v1/reports/" + reportID + "/revisions/1/sections", "read", "", nil},
		{"outline", http.MethodPost, "/api/v1/reports/" + reportID + "/revisions/1/outline/confirm", "execute", "",
			map[string]interface{}{"sections": []map[string]string{{"section_key": "intro", "title": "概述"}}}},
		{"export", http.MethodPost, "/api/v1/reports/" + reportID + "/revisions/1/artifacts", "execute", "",
			map[string]interface{}{"format": "docx"}},
		{"download", http.MethodGet, "/api/v1/reports/" + reportID + "/revisions/1/artifacts/art-any/download", "read", "", nil},
		{"templates", http.MethodGet, "/api/v1/report-templates", "read", "", nil},
		{"capability", http.MethodPost, "/api/v1/report-templates/report-compliance/capability-check", "read", "",
			map[string]interface{}{"datasets": []map[string]string{
				{"role": "primary", "dataset_id": "ds-guessed-0001"},
			}}},
		{"clone", http.MethodPost, "/api/v1/report-templates/report-compliance/clone", "manage", "",
			map[string]interface{}{"template_key": "report-compliance-tenant"}},
		{"job", http.MethodGet, "/api/v1/report-jobs/" + jobID, "read", "", nil},
		{"events", http.MethodGet, "/api/v1/report-jobs/" + jobID + "/events", "read", "", nil},
		{"retry", http.MethodPost, "/api/v1/report-jobs/" + jobID + "/sections/1/retry", "execute", "", nil},
	}
}

// reportRoleGrants is the authoritative 6-role × 19-route permission table. It is
// written out rather than derived from `BuiltinPermissionMatrix()` on purpose: a
// derived expectation would agree with any change to the matrix, which is exactly
// the change this test exists to catch (a new route granted to `viewer`).
//
// `true` = the role reaches the object gate (so a guessed id answers 404/44109);
// `false` = denied at the role gate (403).
func reportRoleGrants(action string) map[string]bool {
	// The four actions as granted by BuiltinPermissionMatrix (verified by
	// internal/router's report access matrix test):
	//   read:        platform_admin, tenant_admin, operator, team_admin, business_user
	//   execute:     platform_admin, tenant_admin, operator, team_admin, business_user
	//   manage:      platform_admin, tenant_admin
	//   delete:own:  platform_admin, tenant_admin, operator, team_admin, business_user
	// viewer receives no report permission at all.
	allowed := map[string]bool{
		model.RolePlatformAdmin: true,
		model.RoleTenantAdmin:   true,
		model.RoleOperator:      true,
		model.RoleTeamAdmin:     true,
		model.RoleBusinessUser:  true,
		model.RoleViewer:        false,
	}
	if action == "manage" {
		return map[string]bool{
			model.RolePlatformAdmin: true,
			model.RoleTenantAdmin:   true,
			model.RoleOperator:      false,
			model.RoleTeamAdmin:     false,
			model.RoleBusinessUser:  false,
			model.RoleViewer:        false,
		}
	}
	return allowed
}

// tenantRoleOrder keeps the failure message stable and readable.
var tenantRoleOrder = []string{
	model.RoleViewer,
	model.RoleBusinessUser,
	model.RoleOperator,
	model.RoleTeamAdmin,
	model.RoleTenantAdmin,
}

// TestReportHandlerRoleMatrixGatesEveryRoute walks the matrix at HTTP level for the
// five tenant-scoped roles, using ids that do not exist *in the caller's own
// workspace*. That is the sharpest form of the assertion: a permitted role must fail
// with "no such report" (404/44109) rather than "forbidden", and a denied role must
// never reach the row layer at all.
func TestReportHandlerRoleMatrixGatesEveryRoute(t *testing.T) {
	svc, engine := newReportService(t)
	routes := reportMatrixRoutes("rep-guessed-0001", "job-guessed-0001", "key-matrix-0001")

	for _, role := range tenantRoleOrder {
		role := role
		t.Run(role, func(t *testing.T) {
			h := newReportHarness(t, svc, engine, "matrix-"+role, role)
			for _, route := range routes {
				rec, envelope := h.do(t, route.method, route.path, route.key, route.body)
				granted := reportRoleGrants(route.action)[role]
				if !granted {
					if rec.Code != http.StatusForbidden {
						t.Fatalf("%s %s: %s lacks %s:report, want 403, got %d (%s)",
							route.method, route.path, role, route.action, rec.Code, rec.Body.String())
					}
					continue
				}
				if rec.Code == http.StatusForbidden {
					t.Fatalf("%s %s: %s holds %s:report, must not be 403",
						route.method, route.path, role, route.action)
				}
				// Permitted-but-nonexistent must be the object gate's answer, not a
				// success and not a permission error.
				if rec.Code == http.StatusOK || rec.Code == http.StatusCreated || rec.Code == http.StatusAccepted {
					// List and catalog routes legitimately succeed for a permitted
					// reader: they answer "nothing in this workspace", which is the
					// isolation answer in page form (asserted for the foreign case in
					// TestReportHandlerObjectLevelIsolationAcrossWorkspaces).
					// `clone` likewise really does clone: a `manage:report-template`
					// holder copying the platform template into its own workspace is
					// the route's whole job, not a leak.
					switch route.name {
					case "list", "templates", "clone":
						continue
					}
					t.Fatalf("%s %s: %s must not be able to address a guessed id (got %d)",
						route.method, route.path, role, rec.Code)
				}
				// 404 is the plain "the dataset/citation you named is not in this
				// workspace" answer the create path gives before a report row exists;
				// it is still the object gate, not a permission answer.
				if code := envelopeCode(t, envelope); code != 44109 && code != 40130 &&
					code != 44104 && code != 44105 && code != 44107 && code != 44106 &&
					code != 404 {
					t.Fatalf("%s %s: %s must be answered by the object gate, got %d/%d (%s)",
						route.method, route.path, role, rec.Code, code, rec.Body.String())
				}
			}
		})
	}
}

// TestReportPlatformAdminCannotReachForeignReportRows pins the platform-admin row
// of the matrix, which is the counter-intuitive one: `platform_admin` is *not* given
// a cross-workspace report read. T19 made every by-id report lookup tenant-scoped at
// the repository, so governance read (`governance.read`) has nothing to widen — the
// row is simply not addressable from another workspace, and the platform role gets
// the same 404/44109 as anyone else.
//
// That is the intended shape, not a regression: report rows are derived from one
// workspace's evidence, and a platform-wide join over them would be a different
// surface with its own audit trail. Asserting it here keeps a future "let admins
// read everything" change from silently widening report isolation.
func TestReportPlatformAdminCannotReachForeignReportRows(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "gov-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-gov-owner")
	_, created := owner.do(t, http.MethodPost, "/api/v1/reports", "key-gov-0001", reportCreateBody(datasetID))
	reportID := createReportIDRow(t, created, "report")
	jobID := createReportIDRow(t, created, "job")
	if reportID == "" || jobID == "" {
		t.Fatalf("owner create must return report+job: %v", created)
	}

	admin := newReportPlatformHarness(t, svc, engine, "gov-admin")
	// The role gate lets the platform admin through (no 403) — the answer is the row
	// gate's 404, which is exactly the "permitted, but nothing here for you" shape.
	for _, path := range []string{
		"/api/v1/reports/" + reportID,
		"/api/v1/reports/" + reportID + "/revisions/1",
		"/api/v1/report-jobs/" + jobID,
	} {
		rec, envelope := admin.do(t, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusNotFound || envelopeCode(t, envelope) != 44109 {
			t.Fatalf("%s: platform admin must not read foreign rows, got %d (%s)",
				path, rec.Code, rec.Body.String())
		}
	}
	// And a governance read must not be usable as a cross-tenant write either.
	if rec, _ := admin.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/cancel", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("platform admin cancel on a foreign report must be 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// newReportPlatformHarness builds a platform-scope principal: `platform_admin` is
// created in the system tenant (the only place the identity service will mint it),
// and the router group carries that tenant as the request scope.
func newReportPlatformHarness(t *testing.T, svc *service.Service, engine *reportHandlerEngine, name string) *reportHarness {
	t.Helper()
	if err := svc.BootstrapAdmin(context.Background(), "report-platform-"+name, "secret123"); err != nil {
		t.Fatalf("bootstrap platform admin: %v", err)
	}
	user, err := svc.Store.GetUserByUsername(context.Background(), "report-platform-"+name)
	if err != nil || user == nil {
		t.Fatalf("load platform admin: %v", err)
	}
	return newReportHarnessFor(t, svc, engine, user.TenantID, user.ID)
}

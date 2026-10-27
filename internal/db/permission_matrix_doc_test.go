package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestPermissionMatrixDocSync guards that doc/user/zh-CN/permission-matrix.md
// (§3 detailed matrix) stays accurate against BuiltinPermissionMatrix(), the
// single source of truth for built-in grants (see migrate.go:3952). Every
// resource exposed by the matrix must appear in the document, and every
// (role, resource) cell must match the built-in action set. New governance /
// memory / compilation resources added to the matrix must be mirrored in the
// user documentation, or this test fails.
func TestPermissionMatrixDocSync(t *testing.T) {
	docPath := filepath.Join("..", "..", "doc", "user", "zh-CN", "permission-matrix.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read doc: %v", err)
	}

	// Column order in the doc matches:
	//   platform_admin tenant_admin operator business_user viewer team_admin
	roles := []struct {
		docCol  string
		grantID string
	}{
		{"`platform_admin`", model.RolePlatformAdmin},
		{"`tenant_admin`", model.RoleTenantAdmin},
		{"`operator`", model.RoleOperator},
		{"`business_user`", model.RoleBusinessUser},
		{"`viewer`", model.RoleViewer},
		{"`team_admin`", model.RoleTeamAdmin},
	}

	abbr := map[string]string{
		"read": "R", "manage": "M", "execute": "E", "append": "A",
		"delete:own": "D", "session:create": "S", "test": "T",
	}

	// Resource rows whose displayed name is an alias of the underlying
	// "tenant" resource (governance.* / tenant.* actions vs plain read/manage).
	aliases := map[string]struct {
		resource string
		exact    map[string]bool // nil means filter by these prefixes; loose tolerates compounds
		prefixes []string
		loose    bool // compound governance actions live in prose (§4), not per-cell abbreviations
	}{
		"tenant / workspace": {resource: "tenant", exact: map[string]bool{"read": true, "manage": true}},
		"governance-tenant": {
			resource: "tenant",
			prefixes: []string{"governance.", "tenant."},
			loose:    true,
		},
	}

	// expectedRaw[roleID][resource] = set of raw action strings from the matrix.
	expectedRaw := map[string]map[string]map[string]bool{}
	for _, g := range BuiltinPermissionMatrix() {
		if expectedRaw[g.Role] == nil {
			expectedRaw[g.Role] = map[string]map[string]bool{}
		}
		if expectedRaw[g.Role][g.Resource] == nil {
			expectedRaw[g.Role][g.Resource] = map[string]bool{}
		}
		expectedRaw[g.Role][g.Resource][g.Action] = true
	}

	// Parse the markdown table rows (after the §3 header + separator).
	type rowMap map[string]string // docCol -> cell text
	rows := map[string]rowMap{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) != len(roles)+3 { // leading/trailing empty + resource col + role cols
			continue
		}
		resource := strings.TrimSpace(cells[1])
		if resource == "" || strings.HasPrefix(resource, ">") || strings.HasPrefix(resource, "-") || strings.Contains(trimmed, "platform_admin") {
			continue
		}
		rm := rowMap{}
		for i, r := range roles {
			rm[r.docCol] = strings.TrimSpace(cells[i+2])
		}
		rows[resource] = rm
	}

	// Every non-tenant resource in the matrix must have a document row.
	for res := range expectedRaw[model.RolePlatformAdmin] {
		if res == "tenant" {
			continue // covered by the two alias rows
		}
		if _, ok := rows[res]; !ok {
			t.Errorf("doc §3 missing row for built-in resource %q", res)
		}
	}

	// The two alias rows must exist.
	if _, ok := rows["tenant / workspace"]; !ok {
		t.Errorf("doc §3 missing alias row %q", "tenant / workspace")
	}
	if _, ok := rows["governance-tenant"]; !ok {
		t.Errorf("doc §3 missing alias row %q", "governance-tenant")
	}

	for rowName, rm := range rows {
		var resource string
		var exact map[string]bool
		var prefixes []string
		var loose bool
		if a, ok := aliases[rowName]; ok {
			resource, exact, prefixes, loose = a.resource, a.exact, a.prefixes, a.loose
		} else {
			resource = rowName
		}
		for _, r := range roles {
			cell := rm[r.docCol]
			cell = strings.ReplaceAll(cell, "（own）", "")
			cell = strings.ReplaceAll(cell, " ", "")
			if loose {
				// Compound governance actions (governance.*, tenant.*) are
				// described in prose in §4, not as per-cell abbreviations —
				// verify that a role holding any such grant has a non-empty cell.
				for a := range expectedRaw[r.grantID][resource] {
					if strings.HasPrefix(a, "governance.") || strings.HasPrefix(a, "tenant.") {
						if cell == "-" {
							t.Errorf("row %q role %s: doc cell %q but builtin %q granted", rowName, r.grantID, cell, a)
						}
						break
					}
				}
				continue
			}
			var docActs map[string]bool
			if cell != "-" {
				docActs = map[string]bool{}
				for _, c := range cell {
					docActs[string(c)] = true
				}
			}
			got := map[string]bool{}
			for a := range expectedRaw[r.grantID][resource] {
				if exact != nil && !exact[a] {
					continue
				}
				if prefixes != nil {
					matched := false
					for _, p := range prefixes {
						if strings.HasPrefix(a, p) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				if ab, ok := abbr[a]; ok {
					got[ab] = true
				}
			}
			if len(docActs) != len(got) {
				t.Errorf("row %q role %s: doc cell %q != builtin %v", rowName, r.grantID, cell, keys(got))
				continue
			}
			for a := range got {
				if !docActs[a] {
					t.Errorf("row %q role %s: doc cell %q != builtin %v", rowName, r.grantID, cell, keys(got))
					break
				}
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

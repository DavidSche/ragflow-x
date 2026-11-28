package model

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Table ownership (doc/319 §5.3 S-4, second half).
//
// The three settings registries answer "did this key get wired?". This one answers
// a different question: **may this file touch that table?** doc/306 adds a new
// domain (source heads, change events, freshness policies) next to doc/301's
// report domain, and the two failure modes are symmetric and silent:
//
//   - the citation audit starts writing `rgx_report_assurance_run` to mark a
//     citation as disputed — the report's assurance verdict now depends on a
//     knowledge job, and a re-run of the audit changes a published report;
//   - the freshness evaluation gate starts reading `rgx_report_*` to decide
//     whether a source is fresh — the knowledge domain now has a hidden dependency
//     on the report schema, and "delete a report" becomes "change a freshness
//     verdict".
//
// Neither shows up as a failing test; both show up as an argument between two
// owners a quarter later. So ownership is declared here, per prefix, with the file
// set that may touch it, and the checker is a pure function over a file map — which
// is what lets the negative cases be *constructed* instead of waited for.
type tableDomain struct {
	Domain string
	// TablePrefixes are the table names this domain owns.
	TablePrefixes []string
	// Files are the globs (repo-relative, slash-separated) allowed to name the
	// domain's tables. Only `*` is used, so a pattern never crosses a directory.
	Files []string
	// Reserved marks a domain whose tables do not exist yet. A reserved domain
	// must match no table (the same rule the error-code bands use: a reservation
	// that already has rows was not a reservation).
	Reserved bool
}

var tableDomains = []tableDomain{
	{
		Domain:        "report",
		TablePrefixes: []string{"rgx_report_"},
		Files: []string{
			"internal/service/report*.go",
			"internal/repository/report*.go",
			"internal/handler/report*.go",
			"internal/handler/report_*.go",
		},
	},
	{
		Domain: "knowledge",
		TablePrefixes: []string{
			"rgx_chat_citation_audit_",
			"rgx_document_",
			"rgx_compilation_",
			"rgx_connector_",
			"rgx_duplicate_",
		},
		Files: []string{
			"internal/service/chat_citation_audit*.go",
			"internal/service/compilation*.go",
			"internal/service/connector*.go",
			"internal/service/document_*.go",
			"internal/service/duplicate*.go",
			"internal/repository/document_*.go",
			"internal/service/report_citation*.go",
		},
	},
	{
		// doc/306 P0a/P0b. These prefixes are reserved *now* so the tables cannot
		// be introduced under a name that already looks like someone else's.
		Domain: "knowledge-freshness",
		// `rgx_source_` is NOT available: `rgx_source_routing_rule` already belongs
		// to the routing domain, and a reservation that collides with a live table is
		// not a reservation — the reserved-empty test below is what found it.
		TablePrefixes: []string{
			"rgx_fresh_source_",
			"rgx_source_change_",
			"rgx_freshness_",
			"rgx_knowledge_audit_",
		},
		Files: []string{
			"internal/service/freshness*.go",
			"internal/service/source_*.go",
			"internal/repository/freshness*.go",
		},
		Reserved: true,
	},
}

// tableNamePattern reads `func (X) TableName() string { return "rgx_…" }`.
var tableNamePattern = regexp.MustCompile(`return "(rgx_[a-z0-9_]+)"`)

// tableMentionPattern finds a table named anywhere in a file — a gorm tag, a raw
// `Table("…")`, a quoted literal in a query builder. Narrower forms (only GORM
// model references) would miss exactly the hand-written SQL that cross-domain
// access is usually written as.
var tableMentionPattern = regexp.MustCompile(`"(rgx_[a-z0-9_]+)"`)

// declaredTables reads the `TableName()` returns out of internal/model. Reading
// the source rather than listing tables keeps the gate honest as models are added.
func declaredTables(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "model", "*.go"))
	if err != nil {
		t.Fatalf("glob model files: %v", err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range tableNamePattern.FindAllStringSubmatch(string(body), -1) {
			seen[match[1]] = true
		}
	}
	tables := make([]string, 0, len(seen))
	for table := range seen {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	if len(tables) == 0 {
		t.Fatal("no tables found: the scanner is blind, so a green result means nothing")
	}
	return tables
}

func domainOfTable(domains []tableDomain, table string) (tableDomain, bool) {
	for _, domain := range domains {
		for _, prefix := range domain.TablePrefixes {
			if strings.HasPrefix(table, prefix) {
				return domain, true
			}
		}
	}
	return tableDomain{}, false
}

// TestTableDomainPrefixesAreDisjoint keeps the ownership map a partition: a table
// claimed by two domains would make "who may write this" answerable two ways, and
// the checker would report whichever rule came first.
func TestTableDomainPrefixesAreDisjoint(t *testing.T) {
	type claim struct {
		prefix string
		domain string
	}
	var claims []claim
	for _, domain := range tableDomains {
		if domain.Domain == "" {
			t.Fatal("a domain has no name")
		}
		if len(domain.TablePrefixes) == 0 {
			t.Fatalf("domain %s owns no prefixes", domain.Domain)
		}
		if len(domain.Files) == 0 {
			t.Fatalf("domain %s allows no files, so every mention is a violation", domain.Domain)
		}
		for _, prefix := range domain.TablePrefixes {
			if !strings.HasPrefix(prefix, "rgx_") {
				t.Fatalf("domain %s claims %q, which is not a table prefix", domain.Domain, prefix)
			}
			claims = append(claims, claim{prefix: prefix, domain: domain.Domain})
		}
	}
	var problems []string
	for i := range claims {
		for j := range claims {
			if i == j {
				continue
			}
			if strings.HasPrefix(claims[i].prefix, claims[j].prefix) ||
				strings.HasPrefix(claims[j].prefix, claims[i].prefix) {
				problems = append(problems, claims[i].prefix+" ("+claims[i].domain+") overlaps "+
					claims[j].prefix+" ("+claims[j].domain+")")
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("table prefixes overlap:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestReservedDomainTablesDoNotExistYet is the "declared but not built" check.
// doc/306 has not started; if a `rgx_source_*` table already existed, the prefix
// reservation would be describing the past instead of the future, and the
// isolation rule would already be unenforceable for it.
func TestReservedDomainTablesDoNotExistYet(t *testing.T) {
	tables := declaredTables(t)
	for _, domain := range tableDomains {
		if !domain.Reserved {
			continue
		}
		for _, table := range tables {
			for _, prefix := range domain.TablePrefixes {
				if strings.HasPrefix(table, prefix) {
					t.Fatalf("%s exists but the %s domain is still reserved; registering the table means "+
						"removing Reserved and naming the files that may write it", table, domain.Domain)
				}
			}
		}
	}
}

// crossDomainTableMentions is the checker. It takes the files as data so the
// negative cases are ordinary table-driven tests rather than a temporary edit to
// the repository that nobody runs twice.
func crossDomainTableMentions(domains []tableDomain, files map[string]string) []string {
	var violations []string
	for file, body := range files {
		slash := filepath.ToSlash(file)
		var owner *tableDomain
		for index := range domains {
			for _, pattern := range domains[index].Files {
				if matched, _ := path.Match(pattern, slash); matched {
					owner = &domains[index]
					break
				}
			}
			if owner != nil {
				break
			}
		}
		if owner == nil {
			continue
		}
		for _, match := range tableMentionPattern.FindAllStringSubmatch(body, -1) {
			table := match[1]
			domain, ok := domainOfTable(domains, table)
			if !ok || domain.Domain == owner.Domain {
				continue
			}
			violations = append(violations, slash+" ("+owner.Domain+" domain) names "+table+" ("+domain.Domain+" domain)")
		}
	}
	sort.Strings(violations)
	return violations
}

// TestCrossDomainTableMentionsAreRefused proves the checker has a bite before it
// is used to bless the real tree: the two documented failure modes must be
// reported, and the same-domain case must stay silent (a checker that flags
// everything is as useless as one that flags nothing).
func TestCrossDomainTableMentionsAreRefused(t *testing.T) {
	domains := []tableDomain{
		{Domain: "report", TablePrefixes: []string{"rgx_report_"}, Files: []string{"internal/service/report*.go"}},
		{Domain: "knowledge", TablePrefixes: []string{"rgx_document_"}, Files: []string{"internal/service/document_*.go"}},
	}

	cases := []struct {
		name    string
		files   map[string]string
		want    int
		wantHas string
	}{
		{
			name: "report code writing a knowledge table",
			files: map[string]string{
				"internal/service/report_assurance.go": `tx.Table("rgx_document_version")`,
			},
			want:    1,
			wantHas: "knowledge domain",
		},
		{
			name: "knowledge code writing a report table",
			files: map[string]string{
				"internal/service/document_governance.go": `tx.Where("id = ?", id).Table("rgx_report_assurance_run")`,
			},
			want:    1,
			wantHas: "report domain",
		},
		{
			name: "same domain is silent",
			files: map[string]string{
				"internal/service/report_codes.go":     `gorm:"table:rgx_report_assurance_run"`,
				"internal/service/document_version.go": `gorm:"table:rgx_document_version"`,
			},
			want: 0,
		},
		{
			name: "a file no domain owns is not judged",
			files: map[string]string{
				"internal/cmd/migrate.go": `gorm:"table:rgx_report_assurance_run"`,
			},
			want: 0,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := crossDomainTableMentions(domains, testCase.files)
			if len(got) != testCase.want {
				t.Fatalf("got %d violation(s) %v, want %d", len(got), got, testCase.want)
			}
			if testCase.wantHas != "" && !strings.Contains(strings.Join(got, "\n"), testCase.wantHas) {
				t.Fatalf("violation does not name %q: %v", testCase.wantHas, got)
			}
		})
	}
}

// TestNoFileTouchesAnotherDomainsTables runs the checker over the tree.
func TestNoFileTouchesAnotherDomainsTables(t *testing.T) {
	root := filepath.Join("..", "..")
	files := map[string]string{}
	matched := map[string]int{}
	for _, directory := range []string{"internal/service", "internal/repository", "internal/handler"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatalf("read %s: %v", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			relative := filepath.ToSlash(filepath.Join(directory, entry.Name()))
			body, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatalf("read %s: %v", relative, err)
			}
			files[relative] = string(body)
			for _, domain := range tableDomains {
				for _, pattern := range domain.Files {
					if ok, _ := path.Match(pattern, relative); ok {
						matched[domain.Domain]++
					}
				}
			}
		}
	}

	// A green isolation scan whose file set is empty is not evidence: it says the
	// globs match nothing, not that the domains are separate.
	for _, domain := range tableDomains {
		if domain.Reserved {
			continue
		}
		if matched[domain.Domain] == 0 {
			t.Fatalf("no file matched the %s domain globs; the isolation scan is blind for it", domain.Domain)
		}
	}

	if violations := crossDomainTableMentions(tableDomains, files); len(violations) > 0 {
		t.Fatalf("cross-domain table access (doc/319 §5.3 S-4):\n  %s", strings.Join(violations, "\n  "))
	}
}

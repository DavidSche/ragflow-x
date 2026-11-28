// ScenarioID: SC-DOC-007
//
// doc/319 §5.3 S-2 is "词表与权限" — two ways the same decision gets spelled twice. The
// vocabulary half is `web_vocabulary_parity_test.go`. This is the permission half.
//
// The failure mode is quiet and one-directional: `canAccess({resource: "report", action:
// "manag"})` is never true, so the button is *hidden* rather than wrong. Nobody files a bug for
// a button that is not there, and TypeScript cannot help — a permission is a string until the
// server answers 403. `db.PermissionCatalog()` is the closed set on the Go side (it drives the
// role matrix UI and `accessRules` enforces it), so the client's questions can be checked
// against it.
//
// There are two ways the client asks, and they need different ground truth:
//
//  1. `RESOURCE_ACCESS` in `authProvider.ts` maps each sidebar resource to the RBAC pair its
//     list view requires. Those pairs must be *grantable* — a typo there hides a whole menu
//     entry for everyone, including the role the entry was built for.
//  2. `NAVIGATION_PERMISSION_ALIASES` in `lib/navigationPermissions.ts` groups several checks
//     into one sidebar row (`asset-governance` = four read permissions). Its rules must be
//     grantable for the same reason, and its resource names are additional legal call-site
//     resources — an alias whose rule can never be granted silently disables a row in the role
//     editor, which is worse than a hidden button because it disables *granting* the thing.
//  3. `canAccess({resource, action})` at a call site may name an RBAC pair or one of those
//     sidebar resources. Anything else can never be true.
package engdocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
)

// permissionCheckPair matches `resource: "x", action: "y"` in either order of the two keys being
// impossible (the object literal always lists resource first in this codebase, and a matcher
// that accepted both orders would also match `{action, resource}` pairs from unrelated option
// objects). Whole-object literals are not required: `useCanAccess({ resource: "tenant", action:
// "read" })` and the multi-line form both carry the same two strings.
var (
	resourceAccessRow = regexp.MustCompile(`"?([a-z0-9-]+)"?:\s*\{\s*resource:\s*"([a-z0-9-]+)",\s*action:\s*"([a-z0-9._:-]+)"\s*\}`)
	permissionPairUse = regexp.MustCompile(`resource:\s*"([a-z0-9-]+)"\s*,\s*action:\s*"([a-z0-9._:-]+)"`)
	aliasResource     = regexp.MustCompile(`(?m)^\s*resource:\s*"([a-z0-9-]+)",\s*$`)
	aliasRule         = regexp.MustCompile(`\{\s*resource:\s*"([a-z0-9-]+)",\s*action:\s*"([a-z0-9._:-]+)"\s*\}`)
)

// permissionPairDebt freezes the call sites that ask about a pair the role matrix cannot grant.
// Every entry is a real defect, not a judgement call; none is repaired here because the repair
// is a product decision (which capability a delegated cross-workspace reader needs) and the
// alternative — quietly renaming the pair — would change what the UI shows without any test
// that says what it should show.
var permissionPairDebt = map[[2]string]string{
	{"agent", "governance.read"}:            "cross-workspace agent list; the server's delegated model is `tenant.audit.read` / `tenant.usage.read` on resource `tenant`, so this pair is never granted and only a platform admin (wildcard) ever sees the control",
	{"dataset", "governance.read"}:          "same shape on the dataset list",
	{"user", "governance.read"}:             "same shape on the user list and team detail",
	{"model-provider", "governance.read"}:   "same shape on the model provider list",
	{"model-provider", "governance.manage"}: "same shape on the provider select dialog",
}

// catalogPairs and aliasRows read the two sides: the grantable matrix, and the sidebar table.
func catalogPairs(t *testing.T) map[[2]string]bool {
	t.Helper()
	pairs := map[[2]string]bool{}
	for _, entry := range db.PermissionCatalog() {
		for _, action := range entry.Actions {
			pairs[[2]string{entry.Resource, action}] = true
		}
	}
	// An empty or misread catalogue would make every check fail, and a catalogue whose actions
	// were read from the wrong field would make every check pass: both directions are floored.
	if len(pairs) < 40 {
		t.Fatalf("the permission catalogue yielded %d (resource, action) pairs, too few to be the real matrix: the reader is broken", len(pairs))
	}
	return pairs
}

func resourceAccessRows(t *testing.T, root string) map[string][2]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "web", "src", "authProvider.ts"))
	if err != nil {
		t.Fatalf("read authProvider.ts: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "const RESOURCE_ACCESS")
	if start < 0 {
		t.Fatalf("authProvider.ts no longer declares RESOURCE_ACCESS: the sidebar's permission mapping moved, and this gate is now measuring nothing")
	}
	end := strings.Index(text[start:], "\n};")
	if end < 0 {
		t.Fatalf("RESOURCE_ACCESS has no closing brace: cannot bound the table")
	}
	block := text[start : start+end]
	rows := map[string][2]string{}
	for _, match := range resourceAccessRow.FindAllStringSubmatch(block, -1) {
		rows[match[1]] = [2]string{match[2], match[3]}
	}
	if len(rows) < 25 {
		t.Fatalf("parsed %d RESOURCE_ACCESS rows, which is too few: the parser went blind (a row that grew a second line or lost its quotes would silently drop out)", len(rows))
	}
	return rows
}

// navigationAliases reads the alias table: resource name -> the pairs it requires.
func navigationAliases(t *testing.T, root string) (map[string]bool, [][2]string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "web", "src", "lib", "navigationPermissions.ts"))
	if err != nil {
		t.Fatalf("read navigationPermissions.ts: %v", err)
	}
	start := strings.Index(string(body), "NAVIGATION_PERMISSION_ALIASES")
	if start < 0 {
		t.Fatalf("navigationPermissions.ts no longer declares NAVIGATION_PERMISSION_ALIASES: this gate is measuring nothing")
	}
	block := string(body)[start:]
	names := map[string]bool{}
	var rules [][2]string
	for _, match := range aliasResource.FindAllStringSubmatch(block, -1) {
		names[match[1]] = true
	}
	seen := map[[2]string]bool{}
	for _, match := range aliasRule.FindAllStringSubmatch(block, -1) {
		key := [2]string{match[1], match[2]}
		if !seen[key] {
			seen[key] = true
			rules = append(rules, key)
		}
	}
	if len(names) < 3 || len(rules) < 3 {
		t.Fatalf("parsed %d aliases and %d rules: the parser went blind", len(names), len(rules))
	}
	return names, rules
}

// TestEveryNavigationAliasRuleIsGrantable is (2): an alias whose rule is not grantable makes the
// whole row unusable in the role editor.
func TestEveryNavigationAliasRuleIsGrantable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	pairs := catalogPairs(t)
	names, rules := navigationAliases(t, root)

	var broken []string
	for _, rule := range rules {
		if !pairs[rule] {
			broken = append(broken, rule[0]+":"+rule[1])
		}
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		t.Fatalf("%d navigation alias rule(s) name a permission the role matrix cannot grant — the alias can never be satisfied, so its row in the role editor never enables:\n  %s",
			len(broken), strings.Join(broken, "\n  "))
	}
	if len(names) == 0 {
		t.Fatal("no alias resources parsed: the alias half of this gate is not running")
	}
}

// TestEverySidebarResourceMapsToAGrantablePair is (1): the sidebar's permission table cannot name
// a permission no role can be granted.
func TestEverySidebarResourceMapsToAGrantablePair(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	pairs := catalogPairs(t)
	rows := resourceAccessRows(t, root)

	var broken []string
	for navResource, pair := range rows {
		if pairs[pair] {
			continue
		}
		broken = append(broken, navResource+" -> "+pair[0]+":"+pair[1])
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		t.Fatalf("%d sidebar resource(s) map to a permission the role matrix cannot grant — the menu entry is hidden for every role, including the one it was built for:\n  %s",
			len(broken), strings.Join(broken, "\n  "))
	}
}

// TestEveryWebPermissionCheckIsGrantable is (2): a call site may ask about an RBAC pair or about
// a sidebar resource, and nothing else.
func TestEveryWebPermissionCheckIsGrantable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	pairs := catalogPairs(t)
	rows := resourceAccessRows(t, root)
	aliases, _ := navigationAliases(t, root)
	for name := range aliases {
		rows[name] = [2]string{"", ""} // the nav name is legal; its rules are checked in (2)
	}

	uses := map[[2]string][]string{}
	scanned := 0
	walkErr := filepath.WalkDir(filepath.Join(root, "web", "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for _, match := range permissionPairUse.FindAllStringSubmatch(string(body), -1) {
			key := [2]string{match[1], match[2]}
			uses[key] = append(uses[key], rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk web/src: %v", walkErr)
	}
	if scanned < 100 {
		t.Fatalf("scanned %d files under web/src: the scanner is blind", scanned)
	}
	if len(uses) < 50 {
		t.Fatalf("found %d distinct permission checks: the scanner is blind (the client asks far more)", len(uses))
	}

	var unknown []string
	unknownKeys := map[[2]string][]string{}
	for key, files := range uses {
		if pairs[key] {
			continue
		}
		if _, isSidebarResource := rows[key[0]]; isSidebarResource {
			continue // `authProvider` resolves it through the table, already checked above
		}
		if _, frozen := permissionPairDebt[key]; frozen {
			continue
		}
		unknown = append(unknown, key[0]+":"+key[1])
		unknownKeys[key] = files
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		var report strings.Builder
		for _, literal := range unknown {
			for key, files := range unknownKeys {
				if key[0]+":"+key[1] != literal {
					continue
				}
				report.WriteString("\n  " + literal + "  (" + strings.Join(uniqueStrings(files), ", ") + ")")
			}
		}
		t.Fatalf("%d permission check(s) can never be true — the control they guard is hidden instead of failing:%s",
			len(unknown), report.String())
	}

	// The debt list is checked in both directions: a repaired pair has to leave it, and an entry
	// whose call sites disappeared is not debt anymore.
	for key, reason := range permissionPairDebt {
		files, stillUsed := uses[key]
		if !stillUsed {
			t.Fatalf("debt entry %s:%s is no longer used by web/src: delete the row (%s)", key[0], key[1], reason)
		}
		if pairs[key] {
			t.Fatalf("debt entry %s:%s is grantable now — delete the row (%s)", key[0], key[1], reason)
		}
		if len(files) == 0 {
			t.Fatalf("debt entry %s:%s has no call site: delete the row", key[0], key[1])
		}
	}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range in {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

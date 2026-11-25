// Package gormguard is a static check for one GORM mistake: a paging read that
// shares its builder with the count query, where the builder carries a clause that
// changes the shape of the statement (Group/Select/Joins/Distinct/Unscoped/Having).
//
// The failure this exists to prevent is the 2026-11 knowledge-ops 500:
//
//	q.Group("id")            // bare call: mutates q's shared Statement
//	q.Count(&total)          // count(*) ... GROUP BY id   — correct
//	q.Order(...).Find(&out)  // SELECT * ... GROUP BY id   — PostgreSQL 42803 / wrong rows
//
// SQLite accepts the second statement, so unit tests built on it stay green while
// PostgreSQL rejects the same code. Only PostgreSQL's semantics make the bug visible,
// and by then it is a production 500, so the guard is static and runs in CI.
//
// Two shapes are deliberately accepted, because both give the count its own state and
// are the sanctioned ways to write this:
//
//	q.Session(&gorm.Session{}).Group("id").Count(&total)  // count-only clause
//	countQuery := db.Model(&X{}); listQuery := db.Model(&X{})  // separate builders
//
// A third shape cannot be seen without type information and is not flagged: a builder
// built inside a helper and returned to the caller. The check is intentionally
// syntactic — it needs no build tags, no database, and no compiled packages, so it can
// run before `go build` on a tree that does not compile yet.
package gormguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity separates a clause that is always wrong to share from one that is only
// wrong in combination.
const (
	// SeverityError: the clause changes the statement's shape (grouping or projection),
	// so the count and the read no longer describe the same result set — and PostgreSQL
	// rejects the read outright once it selects a non-grouped column.
	SeverityError = "error"
	// SeverityWarning: the shared clause is a join. A join multiplies rows identically
	// for both statements, so it is usually intentional (count the joined rows, page the
	// joined rows); it becomes fatal only once a grouping clause is added, which is what
	// SeverityError already reports.
	SeverityWarning = "warning"
)

// Finding is one builder that serves both a count and a read while carrying a clause
// the read never asked for.
type Finding struct {
	Path      string   // file, relative to the scanned root
	Func      string   // enclosing function
	Builder   string   // the variable that is shared, e.g. "q"
	Clauses   []string // the clauses that leak into the read, e.g. ["Group"]
	CountLine int      // line of the Count call
	ReadLine  int      // line of the read call
	Severity  string   // SeverityError or SeverityWarning
}

func (f Finding) String() string {
	verdict := "give the count its own builder (`" + f.Builder + ".Session(&gorm.Session{})`) or hoist the clause to the read only"
	if f.Severity == SeverityWarning {
		verdict = "harmless while the join is one-to-one, fatal once Group/Distinct joins it on the same builder"
	}
	return fmt.Sprintf("%s:%d: %s: builder %q shares Count (line %d) and a read (line %d) after %s — %s",
		f.Path, f.CountLine, f.Func, f.Builder, f.CountLine, f.ReadLine,
		strings.Join(f.Clauses, "+"), verdict)
}

// clauseMethods change what a statement selects or groups by, so sharing them between
// a count and a read means the two statements describe different result sets.
// Preload is not here: it issues its own follow-up queries and never rewrites the
// count or the paging read. Omit is the opposite of Select and does not introduce a
// grouping the read would have to satisfy.
var clauseMethods = map[string]bool{
	"Group":    true,
	"Select":   true,
	"Distinct": true,
	"Unscoped": true,
	"Having":   true,
}

// joinMethod is tracked separately: see SeverityWarning.
const joinMethod = "Joins"

// readMethods are the terminal calls that consume a builder into a result.
var readMethods = map[string]bool{
	"Find": true, "Scan": true, "Pluck": true, "First": true,
	"Take": true, "Last": true, "Row": true, "Rows": true,
}

// builderMethods start a query; a chain that contains one of these produces a builder
// rather than some other object. Session is in the list but not in freshMethods: a
// session chain reuses the conditions of the builder it is called on.
var builderMethods = map[string]bool{
	"Model": true, "Table": true, "WithContext": true, "Session": true, "Raw": true,
}

// freshMethods start an independent statement. `tx.Model(...)` is a new query even when
// tx already carried clauses for an earlier statement, which is why a variable reused
// for several statements in one transaction is not one shared builder.
var freshMethods = map[string]bool{
	"Model": true, "Table": true, "WithContext": true, "Raw": true,
}

// terminalMethods end a chain. A chain that reaches one is a query in progress, not a
// builder being handed to another variable.
var terminalMethods = map[string]bool{
	"Count": true, "Error": true, "Delete": true, "Update": true, "Updates": true,
	"Create": true, "Exec": true,
}

func init() {
	for m := range readMethods {
		terminalMethods[m] = true
	}
}

// ScanDirs walks every non-test .go file under each root and returns the findings,
// sorted by file and line.
func ScanDirs(roots ...string) ([]Finding, error) {
	var findings []Finding
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			fs, err := scanFile(root, root)
			if err != nil {
				return nil, err
			}
			findings = append(findings, fs...)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "vendor", "node_modules", "testdata", "test-results", ".git":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found, err := scanFile(path, root)
			if err != nil {
				return err
			}
			findings = append(findings, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].CountLine < findings[j].CountLine
	})
	return findings, nil
}

func scanFile(path, root string) ([]Finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = path
	}
	return analyzeSource(filepath.ToSlash(rel), src)
}

// analyzeSource is the whole check for one file. It parses without comments and
// without type information: every pattern it looks for is a method call on a
// variable, which the syntax tree already spells out.
func analyzeSource(name string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		findings = append(findings, analyzeFunc(fset, name, fn)...)
	}
	return findings, nil
}

// builder is the state the check accumulates for one variable inside one function.
type builder struct {
	shape    map[string]bool // clauses that will be in the read's statement
	count    bool
	read     bool
	countPos token.Position
	readPos  token.Position
}

func (b *builder) clone() *builder {
	c := &builder{shape: map[string]bool{}, count: b.count, read: b.read,
		countPos: b.countPos, readPos: b.readPos}
	for k := range b.shape {
		c.shape[k] = true
	}
	return c
}

func analyzeFunc(fset *token.FileSet, path string, fn *ast.FuncDecl) []Finding {
	// Assignment targets, so `err := q.Count(&n).Error` is attributed to q and not to
	// a variable called err.
	targets := map[ast.Expr]string{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		id, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if end := chainEnd(assign.Rhs[0]); end != nil {
			targets[end] = id.Name
		}
		return true
	})

	// Only the outer end of a chain is analysed, once: `q.Count(&n).Error` is one chain
	// (`.Error` is a field, not a call), and its inner links must not be counted again.
	inner := map[ast.Node]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			inner[node.X] = true
		case *ast.CallExpr:
			inner[node.Fun] = true
		}
		return true
	})

	states := map[string]*builder{}
	var order []string // first-seen order, for deterministic output
	state := func(name string) *builder {
		if b, ok := states[name]; ok {
			return b
		}
		b := &builder{shape: map[string]bool{}}
		states[name] = b
		order = append(order, name)
		return b
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		var end ast.Expr
		switch n.(type) {
		case *ast.CallExpr, *ast.SelectorExpr:
			end = n.(ast.Expr)
		default:
			return true
		}
		if inner[end] {
			return true
		}
		root, methods := chainOf(end)
		if root == "" || len(methods) == 0 {
			return true
		}
		target := targets[end]
		current, tracked := states[root]
		fresh := freshChain(methods)

		// `Session` starts a new statement: clauses after it belong to a copy, and a
		// copy cannot pollute the variable it was derived from.
		lastSession := -1
		for i, m := range methods {
			if m == "Session" {
				lastSession = i
			}
		}
		inherited := map[string]bool{} // clauses that mutate the root's own statement
		for i, m := range methods {
			if (clauseMethods[m] || m == joinMethod) && (lastSession < 0 || i < lastSession) {
				inherited[m] = true
			}
		}

		isResult := false
		for _, m := range methods {
			if terminalMethods[m] {
				isResult = true
				break
			}
		}
		hasBuilderMethod := false
		for _, m := range methods {
			if builderMethods[m] {
				hasBuilderMethod = true
				break
			}
		}

		// A chain assigned to another variable is a copy of the query, not a use of it:
		// `listQuery := q.Where(...)` must not make the count and the read look shared.
		carriesResult := false
		for _, m := range methods {
			if m == "Count" || readMethods[m] {
				carriesResult = true
				break
			}
		}
		copyTarget := target != "" && target != root && !isResult

		if tracked && fresh {
			// The same variable, a new statement: a transaction handle used for several
			// unrelated queries, or a builder rebuilt from scratch. Nothing accumulated
			// under this name applies to it.
			if target != "" && !isResult && !carriesResult {
				if _, ok := states[target]; !ok {
					b := &builder{shape: map[string]bool{}}
					for i, m := range methods {
						if (clauseMethods[m] || m == joinMethod) && i > lastSession {
							b.shape[m] = true
						}
					}
					states[target] = b
					order = append(order, target)
				}
			}
			return true
		}

		if !tracked {
			// The chain starts from a value this function does not own. Which of the two
			// meanings it has decides whether its state may be followed:
			//
			//   - `db.Model(...)` / `db.WithContext(...)` starts a *new* statement, so two
			//     such chains are two statements and must never be merged — merging them is
			//     how `tx.Model(a).Distinct().Pluck(...)` plus `tx.Model(b).Count(...)`
			//     becomes one phantom finding.
			//   - `q.Group(...)` / `q.Count(...)` on a builder received from outside (a
			//     parameter) mutates that builder, so it is exactly the state to follow.
			switch {
			case target != "" && target != root && !isResult && hasBuilderMethod:
				// `q := s.WithContext(ctx).Model(&X{})` — a builder of this function's own.
				b := state(target)
				for i, m := range methods {
					if (clauseMethods[m] || m == joinMethod) && i > lastSession {
						b.shape[m] = true
					}
				}
			case !fresh && looksLikeQuery(methods):
				// `q.Group(...)` on a builder received from outside (a parameter): the call
				// mutates that builder, which is exactly the state to follow. Only clauses
				// before a Session reach it — after a Session they belong to the copy.
				b := state(root)
				for m := range inherited {
					b.shape[m] = true
				}
				if carriesResult {
					record(b, fset, methods, end)
				}
			}
			return true
		}

		if copyTarget {
			// A derived builder keeps the clauses already applied to the parent
			// (clones share them) plus its own.
			b := current.clone()
			for _, m := range methods {
				if clauseMethods[m] || m == joinMethod {
					b.shape[m] = true
				}
			}
			b.count, b.read = false, false
			states[target] = b
			order = append(order, target)
			return true
		}

		for m := range inherited {
			current.shape[m] = true
		}
		if carriesResult {
			record(current, fset, methods, end)
		}
		return true
	})

	var findings []Finding
	for _, name := range order {
		b := states[name]
		if !b.count || !b.read || len(b.shape) == 0 {
			continue
		}
		clauses := make([]string, 0, len(b.shape))
		severity := SeverityWarning
		for c := range b.shape {
			clauses = append(clauses, c)
			if clauseMethods[c] {
				severity = SeverityError
			}
		}
		sort.Strings(clauses)
		findings = append(findings, Finding{
			Path: path, Func: fn.Name.Name, Builder: name, Severity: severity,
			Clauses: clauses, CountLine: b.countPos.Line, ReadLine: b.readPos.Line,
		})
	}
	return findings
}

func record(b *builder, fset *token.FileSet, methods []string, end ast.Expr) {
	for _, m := range methods {
		switch {
		case m == "Count":
			if !b.count {
				b.count = true
				b.countPos = fset.Position(end.Pos())
			}
		case readMethods[m]:
			if !b.read {
				b.read = true
				b.readPos = fset.Position(end.Pos())
			}
		}
	}
}

// chainOf peels a chain down to the variable it starts from:
// `q.Group("id").Count(&n)` -> ("q", ["Group", "Count"]). Both calls and field
// selectors continue a chain, because GORM's result readers are fields: the `.Error`
// in `q.Find(&items).Error` is where the statement is executed and its error read.
func chainOf(expr ast.Expr) (string, []string) {
	var methods []string
peel:
	for {
		switch node := expr.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				break peel
			}
			methods = append(methods, sel.Sel.Name)
			expr = sel.X
		case *ast.SelectorExpr:
			methods = append(methods, node.Sel.Name)
			expr = node.X
		default:
			break peel
		}
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", nil
	}
	for i, j := 0, len(methods)-1; i < j; i, j = i+1, j-1 {
		methods[i], methods[j] = methods[j], methods[i]
	}
	return ident.Name, methods
}

// freshChain reports whether the first call on the chain starts a new statement
// (`db.Model(...)`), as opposed to mutating an existing builder (`q.Group(...)`).
func freshChain(methods []string) bool {
	return len(methods) > 0 && freshMethods[methods[0]]
}

// looksLikeQuery keeps the mutation branch honest: only a chain that calls something a
// GORM builder has is treated as one, so `logger.Info(...)` is not followed forever.
func looksLikeQuery(methods []string) bool {
	for _, m := range methods {
		switch m {
		case "Where", "Or", "Not", "Order", "Limit", "Offset", "Scopes", "Debug", "Unscoped":
			return true
		}
		if clauseMethods[m] || m == joinMethod || m == "Count" || readMethods[m] || builderMethods[m] {
			return true
		}
	}
	return false
}

// chainEnd returns expr when it is the end of a chain (a call or a field selector),
// so an assignment target can be attributed to it.
func chainEnd(expr ast.Expr) ast.Expr {
	switch expr.(type) {
	case *ast.CallExpr, *ast.SelectorExpr:
		return expr
	}
	return nil
}

package service

import (
	"strings"

	"github.com/pingcap/tidb/parser"
	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/mysql"
	_ "github.com/pingcap/tidb/parser/test_driver"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

type queryScope struct {
	tableSet   map[string]struct{}
	columnSets map[string]map[string]struct{}
	cteSet     map[string]struct{}
	cteColumns map[string]map[string]struct{}
	aliases    map[string]string
	referenced []string
	topWith    map[ast.Node]struct{}
	cteBodies  map[ast.Node]struct{}
}

func parseReadOnlySQL(rawSQL string) (ast.StmtNode, error) {
	statementParser := parser.New()
	statementParser.SetSQLMode(mysql.ModeANSIQuotes)
	statements, _, err := statementParser.ParseSQL(rawSQL)
	if err != nil {
		return nil, httperr.BadRequest(40189, "sql_template must be one supported read-only SQL statement")
	}
	if len(statements) != 1 {
		return nil, httperr.BadRequest(40190, "sql_template only allows SELECT, UNION or WITH ... SELECT")
	}
	switch statements[0].(type) {
	case *ast.SelectStmt, *ast.SetOprStmt:
		return statements[0], nil
	default:
		return nil, httperr.BadRequest(40190, "sql_template only allows SELECT, UNION or WITH ... SELECT")
	}
}

func validateQueryTemplateSQL(raw string, policy QueryExecutionPolicy) error {
	normalized, _, _, err := normalizeQueryBindVariables(raw)
	if err != nil {
		return err
	}
	statement, err := parseReadOnlySQL(strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(normalized, " \t\r\n"), ";")))
	if err != nil {
		return err
	}
	physicalColumns := make(map[string]map[string]struct{}, len(policy.AllowedColumns))
	for table, columns := range policy.AllowedColumns {
		normalizedTable := strings.ToLower(table)
		physicalColumns[normalizedTable] = make(map[string]struct{}, len(columns))
		for _, column := range columns {
			physicalColumns[normalizedTable][strings.ToLower(column)] = struct{}{}
		}
	}

	physicalScope := &queryScope{
		tableSet:   make(map[string]struct{}, len(policy.AllowedTables)),
		columnSets: physicalColumns, cteSet: map[string]struct{}{},
		cteColumns: map[string]map[string]struct{}{}, aliases: map[string]string{},
		topWith: map[ast.Node]struct{}{}, cteBodies: map[ast.Node]struct{}{},
	}
	for _, table := range policy.AllowedTables {
		physicalScope.tableSet[strings.ToLower(table)] = struct{}{}
	}

	cteColumns := make(map[string]map[string]struct{})
	var with *ast.WithClause
	var query ast.StmtNode
	switch typed := statement.(type) {
	case *ast.SelectStmt:
		with, query = typed.With, typed
	case *ast.SetOprStmt:
		with, query = typed.With, typed
	}
	if with != nil {
		if with.IsRecursive {
			return httperr.BadRequest(40191, "sql_template only supports non-recursive CTEs")
		}
		for _, cte := range with.CTEs {
			if cte == nil || cte.Query == nil {
				return httperr.BadRequest(40189, "sql_template contains an invalid CTE")
			}
			if cte.IsRecursive {
				return httperr.BadRequest(40191, "sql_template only supports non-recursive CTEs")
			}
			name := strings.ToLower(cte.Name.L)
			if !validIdentifier.MatchString(name) {
				return httperr.BadRequest(40191, "sql_template contains an invalid CTE name")
			}
			if _, exists := cteColumns[name]; exists {
				return httperr.BadRequest(40191, "sql_template contains duplicate CTE names")
			}
			if len(cte.ColNameList) == 0 {
				return httperr.BadRequest(40194, "sql_template CTEs must declare output columns")
			}
			columns := make(map[string]struct{}, len(cte.ColNameList))
			for _, rawColumn := range cte.ColNameList {
				column := strings.ToLower(rawColumn.L)
				if !validIdentifier.MatchString(column) {
					return httperr.BadRequest(40197, "sql_template CTE contains an invalid output column name")
				}
				if _, exists := columns[column]; exists {
					return httperr.BadRequest(40197, "sql_template CTE contains duplicate output columns")
				}
				columns[column] = struct{}{}
			}
			body, ok := cte.Query.Query.(*ast.SelectStmt)
			if !ok {
				return httperr.BadRequest(40190, "sql_template CTE bodies only allow SELECT")
			}
			if _, physical := physicalScope.tableSet[name]; physical {
				return httperr.BadRequest(40191, "sql_template CTE name conflicts with an allowed table")
			}
			cteColumns[name] = columns
			if err := validateQuerySource(body, physicalScope); err != nil {
				return err
			}
			physicalScope.cteBodies[body] = struct{}{}
			if len(body.Fields.Fields) != len(cte.ColNameList) {
				return httperr.BadRequest(40197, "sql_template CTE body columns do not match output columns")
			}
		}
	}

	scope := &queryScope{
		tableSet:   physicalScope.tableSet,
		columnSets: make(map[string]map[string]struct{}, len(physicalScope.columnSets)+len(cteColumns)),
		cteSet:     make(map[string]struct{}, len(cteColumns)), cteColumns: cteColumns,
		aliases: map[string]string{}, topWith: map[ast.Node]struct{}{},
		cteBodies: physicalScope.cteBodies,
	}
	for table, columns := range physicalScope.columnSets {
		scope.columnSets[table] = columns
	}
	for name := range cteColumns {
		scope.cteSet[name] = struct{}{}
		scope.columnSets[name] = cteColumns[name]
	}
	if with != nil {
		scope.topWith[with] = struct{}{}
	}
	return validateQuerySource(query, scope)
}

func validateQuerySource(source ast.Node, scope *queryScope) error {
	collector := &queryTableCollector{scope: scope}
	if _, ok := source.Accept(collector); !ok {
		return collector.err
	}
	if len(scope.referenced) == 0 && len(scope.aliases) == 0 {
		return httperr.BadRequest(40192, "sql_template must reference at least one allowed table")
	}
	validator := &queryExpressionValidator{scope: scope}
	if _, ok := source.Accept(validator); !ok {
		return validator.err
	}
	return nil
}

type queryTableCollector struct {
	scope *queryScope
	err   error
}

func (collector *queryTableCollector) Enter(node ast.Node) (ast.Node, bool) {
	if collector.err != nil {
		return node, true
	}
	if _, skipped := collector.scope.cteBodies[node]; skipped {
		return node, true
	}
	switch typed := node.(type) {
	case *ast.WithClause:
		if _, top := collector.scope.topWith[node]; !top {
			collector.fail(httperr.BadRequest(40190, "sql_template only allows one top-level WITH clause"))
		}
	case *ast.SelectStmt:
		if typed.LockInfo != nil || len(typed.TableHints) != 0 || typed.SelectIntoOpt != nil || len(typed.WindowSpecs) != 0 {
			collector.fail(httperr.BadRequest(40193, "sql_template must not lock rows, use hints, select into, or window functions"))
		}
	case *ast.TableSource:
		collector.collectTableSource(typed)
	case *ast.SelectField:
		if typed.WildCard != nil {
			collector.fail(httperr.BadRequest(40194, "sql_template must explicitly list allowed columns"))
		}
	}
	return node, collector.err != nil
}

func (collector *queryTableCollector) Leave(node ast.Node) (ast.Node, bool) {
	return node, collector.err == nil
}

func (collector *queryTableCollector) collectTableSource(source *ast.TableSource) {
	table, ok := source.Source.(*ast.TableName)
	if !ok {
		return
	}
	if table.Schema.L != "" {
		collector.fail(httperr.BadRequest(40191, "sql_template table names must not include database qualifiers"))
		return
	}
	name := strings.ToLower(table.Name.L)
	if _, isCTE := collector.scope.cteColumns[name]; isCTE {
		alias := name
		if source.AsName.L != "" {
			alias = strings.ToLower(source.AsName.L)
		}
		collector.scope.aliases[alias] = name
		if !containsString(collector.scope.referenced, name) {
			collector.scope.referenced = append(collector.scope.referenced, name)
		}
		return
	}
	if _, allowed := collector.scope.tableSet[name]; !allowed {
		collector.fail(httperr.BadRequest(40191, "sql_template references a table outside the allowlist"))
		return
	}
	alias := name
	if source.AsName.L != "" {
		alias = strings.ToLower(source.AsName.L)
	}
	collector.scope.aliases[alias] = name
	if !containsString(collector.scope.referenced, name) {
		collector.scope.referenced = append(collector.scope.referenced, name)
	}
}

func (collector *queryTableCollector) fail(err error) {
	if collector.err == nil {
		collector.err = err
	}
}

type queryExpressionValidator struct {
	scope *queryScope
	err   error
}

func (validator *queryExpressionValidator) Enter(node ast.Node) (ast.Node, bool) {
	if validator.err != nil {
		return node, true
	}
	if _, skipped := validator.scope.cteBodies[node]; skipped {
		return node, true
	}
	switch typed := node.(type) {
	case *ast.WithClause:
		if _, top := validator.scope.topWith[node]; !top {
			validator.fail(httperr.BadRequest(40190, "sql_template only allows one top-level WITH clause"))
		}
	case *ast.SelectStmt:
		if typed.LockInfo != nil || len(typed.TableHints) != 0 || typed.SelectIntoOpt != nil || len(typed.WindowSpecs) != 0 {
			validator.fail(httperr.BadRequest(40193, "sql_template must not lock rows, use hints, select into, or window functions"))
		}
	case *ast.SelectField:
		if typed.WildCard != nil {
			validator.fail(httperr.BadRequest(40194, "sql_template must explicitly list allowed columns"))
		}
	case *ast.VariableExpr:
		validator.fail(httperr.BadRequest(40196, "sql_template only supports named bind variables"))
	case *ast.FuncCastExpr:
		validator.fail(httperr.BadRequest(40195, "sql_template contains a function outside the read-only allowlist"))
	case *ast.FuncCallExpr:
		if typed.Schema.L != "" {
			validator.fail(httperr.BadRequest(40195, "sql_template contains a function outside the read-only allowlist"))
		} else {
			validator.validateFunction(typed.FnName.L)
		}
	case *ast.AggregateFuncExpr:
		validator.validateFunction(strings.ToLower(typed.F))
	case ast.ParamMarkerExpr:
	case *ast.ColumnNameExpr:
		validator.validateColumn(typed)
	}
	return node, validator.err != nil
}

func (validator *queryExpressionValidator) Leave(node ast.Node) (ast.Node, bool) {
	return node, validator.err == nil
}

func (validator *queryExpressionValidator) validateFunction(name string) {
	if _, allowed := readOnlyScalarFunctions[name]; !allowed {
		validator.fail(httperr.BadRequest(40195, "sql_template contains a function outside the read-only allowlist"))
	}
}

func (validator *queryExpressionValidator) validateColumn(column *ast.ColumnNameExpr) {
	if column.Name.Schema.L != "" {
		validator.fail(httperr.BadRequest(40191, "sql_template table names must not include database qualifiers"))
		return
	}
	table, err := resolvePingcapQueryColumnTable(column, validator.scope)
	if err != nil {
		validator.fail(err)
		return
	}
	columnName := strings.ToLower(column.Name.Name.L)
	if _, allowed := validator.scope.columnSets[table][columnName]; !allowed {
		validator.fail(httperr.BadRequest(40197, "sql_template references a column outside the allowlist"))
	}
}

func (validator *queryExpressionValidator) fail(err error) {
	if validator.err == nil {
		validator.err = err
	}
}

func resolvePingcapQueryColumnTable(column *ast.ColumnNameExpr, scope *queryScope) (string, error) {
	qualifier := strings.ToLower(column.Name.Table.L)
	if qualifier != "" {
		table, ok := scope.aliases[qualifier]
		if !ok {
			return "", httperr.BadRequest(40198, "sql_template references an unknown table or alias")
		}
		return table, nil
	}
	columnName := strings.ToLower(column.Name.Name.L)
	matches := make([]string, 0, 1)
	for _, table := range scope.referenced {
		if _, exists := scope.columnSets[table][columnName]; exists {
			matches = append(matches, table)
		}
	}
	if len(matches) != 1 {
		return "", httperr.BadRequest(40198, "sql_template unqualified column is ambiguous or unknown")
	}
	return matches[0], nil
}

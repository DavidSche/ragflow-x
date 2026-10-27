package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const crossDatabaseRowCount = 2000

func TestSQLToolPostgreSQLReadOnlyAccountTruncationAndTimeout(t *testing.T) {
	adminDSN := os.Getenv("RGX_TEST_POSTGRES_ADMIN_DSN")
	readOnlyDSN := os.Getenv("RGX_TEST_POSTGRES_READONLY_DSN")
	if adminDSN == "" {
		t.Skip("set RGX_TEST_POSTGRES_ADMIN_DSN to run SQL Tool PostgreSQL integration")
	}
	if readOnlyDSN == "" {
		readOnlyDSN = provisionPostgreSQLReadOnlyRole(t, adminDSN)
	}

	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open postgres admin database: %v", err)
	}
	defer func() { _ = adminDB.Close() }()
	readOnlyDB, err := sql.Open("pgx", readOnlyDSN)
	if err != nil {
		t.Fatalf("open postgres read-only database: %v", err)
	}
	defer func() { _ = readOnlyDB.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Skipf("postgres admin integration database unavailable: %v", err)
	}
	if err := readOnlyDB.PingContext(ctx); err != nil {
		t.Skipf("postgres read-only integration database unavailable: %v", err)
	}

	table := fmt.Sprintf("rgx_sql_tool_%x", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE %s (tenant_id TEXT NOT NULL, exposure NUMERIC NOT NULL)", quotePostgreSQLIdentifier(table),
	)); err != nil {
		t.Fatalf("create postgres integration table: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(cleanupContext, "DROP TABLE IF EXISTS "+quotePostgreSQLIdentifier(table))
	})
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s (tenant_id, exposure) SELECT 'tenant-a', 10.5 FROM generate_series(1, $1)",
		quotePostgreSQLIdentifier(table),
	), crossDatabaseRowCount); err != nil {
		t.Fatalf("seed postgres integration table: %v", err)
	}

	var readOnlyRole string
	if err := readOnlyDB.QueryRowContext(ctx, "SELECT current_user").Scan(&readOnlyRole); err != nil {
		t.Fatalf("read postgres read-only role: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT ON %s TO %s", quotePostgreSQLIdentifier(table), quotePostgreSQLIdentifier(readOnlyRole),
	)); err != nil {
		t.Fatalf("grant postgres select privilege: %v", err)
	}
	assertCrossDatabaseAccountIsReadOnly(t, ctx, readOnlyDB, fmt.Sprintf(
		"INSERT INTO %s (tenant_id, exposure) VALUES ('tenant-write', 1)", quotePostgreSQLIdentifier(table),
	))

	svc, tool, sourceRuleID := newCrossDatabaseSQLToolFixture(t, model.DBDriverPostgres, readOnlyDSN, table,
		"Cross database truncation", "SELECT tenant_id, exposure FROM "+table+" WHERE tenant_id = :tenant_id",
		`{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		`{"type":"object","required":["tenant_id","exposure"],"properties":{"tenant_id":{"type":"string"},"exposure":{"type":"number"}}}`,
		`{"read_only":true,"max_rows":2,"timeout_ms":5000,"allowed_tables":["`+table+`"],"allowed_columns":{"`+table+`":["tenant_id","exposure"]}}`,
	)
	assertCrossDatabaseSQLTruncation(t, ctx, svc, tool, sourceRuleID)
	timeoutSvc, timeoutTool, timeoutRuleID := newCrossDatabaseSQLToolFixture(t, model.DBDriverPostgres, readOnlyDSN, table,
		"Cross database timeout", "SELECT COUNT(*) AS total FROM "+table+" AS row_a JOIN "+table+
			" AS row_b ON row_b.tenant_id = row_a.tenant_id WHERE row_a.tenant_id = :tenant_id",
		`{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		`{"type":"object","required":["total"],"properties":{"total":{"type":"number"}}}`,
		`{"read_only":true,"max_rows":1,"timeout_ms":1,"allowed_tables":["`+table+`"],"allowed_columns":{"`+table+`":["tenant_id","exposure"]}}`,
	)
	assertCrossDatabaseSQLTimeout(t, ctx, timeoutSvc, timeoutTool, timeoutRuleID)
}

func provisionPostgreSQLReadOnlyRole(t *testing.T, adminDSN string) string {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse postgres admin connection: %v", err)
	}
	adminConfig.User = "rgx_sql_tool_ro_" + id.New()[:12]
	adminConfig.Password = id.New() + id.New()
	role := adminConfig.User
	password := adminConfig.Password
	readOnlyDSN := postgresReadOnlyDSN(t, adminDSN, role, password)

	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open postgres admin database: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Skipf("postgres admin integration database unavailable: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT",
		quotePostgreSQLIdentifier(role), quotePostgreSQLLiteral(password),
	)); err != nil {
		t.Fatalf("create postgres read-only role: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(cleanupContext, "DROP ROLE IF EXISTS "+quotePostgreSQLIdentifier(role))
	})
	for _, statement := range []string{
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s",
			quotePostgreSQLIdentifier(adminConfig.Database), quotePostgreSQLIdentifier(role)),
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s",
			quotePostgreSQLIdentifier("public"), quotePostgreSQLIdentifier(role)),
	} {
		if _, err := adminDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("grant postgres read-only access: %v", err)
		}
	}
	return readOnlyDSN
}

func postgresReadOnlyDSN(t *testing.T, adminDSN, role, password string) string {
	t.Helper()
	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse postgres admin connection URL: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatal("postgres admin DSN must use a postgres URL")
	}
	parsed.User = url.UserPassword(role, password)
	return parsed.String()
}

func TestSQLToolMySQLReadOnlyAccountTruncationAndTimeout(t *testing.T) {
	adminDSN := os.Getenv("RGX_TEST_MYSQL_ADMIN_DSN")
	readOnlyDSN := os.Getenv("RGX_TEST_MYSQL_READONLY_DSN")
	if adminDSN == "" {
		t.Skip("set RGX_TEST_MYSQL_ADMIN_DSN to run SQL Tool MySQL integration")
	}
	if readOnlyDSN == "" {
		readOnlyDSN = provisionMySQLReadOnlyUser(t, adminDSN)
	}

	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatalf("open mysql admin database: %v", err)
	}
	defer func() { _ = adminDB.Close() }()
	readOnlyDB, err := sql.Open("mysql", readOnlyDSN)
	if err != nil {
		t.Fatalf("open mysql read-only database: %v", err)
	}
	defer func() { _ = readOnlyDB.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Skipf("mysql admin integration database unavailable: %v", err)
	}
	if err := readOnlyDB.PingContext(ctx); err != nil {
		t.Skipf("mysql read-only integration database unavailable: %v", err)
	}

	table := fmt.Sprintf("rgx_sql_tool_%x", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE %s (tenant_id VARCHAR(64) NOT NULL, exposure DECIMAL(18,2) NOT NULL)", quoteMySQLIdentifier(table),
	)); err != nil {
		t.Fatalf("create mysql integration table: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(cleanupContext, "DROP TABLE IF EXISTS "+quoteMySQLIdentifier(table))
	})
	values := make([]string, 0, crossDatabaseRowCount)
	args := make([]interface{}, 0, crossDatabaseRowCount*2)
	for index := 0; index < crossDatabaseRowCount; index++ {
		values = append(values, "(?, ?)")
		args = append(args, "tenant-a", 10.5)
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s (tenant_id, exposure) VALUES %s", quoteMySQLIdentifier(table), strings.Join(values, ","),
	), args...); err != nil {
		t.Fatalf("seed mysql integration table: %v", err)
	}

	var readOnlyAccount string
	if err := readOnlyDB.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&readOnlyAccount); err != nil {
		t.Fatalf("read mysql read-only account: %v", err)
	}
	databaseName, err := crossDatabaseMySQLName(readOnlyDSN)
	if err != nil {
		t.Fatalf("read mysql database name: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT ON %s.%s TO %s",
		quoteMySQLIdentifier(databaseName), quoteMySQLIdentifier(table), quoteMySQLAccountIdentifier(readOnlyAccount),
	)); err != nil {
		t.Fatalf("grant mysql select privilege: %v", err)
	}
	assertCrossDatabaseAccountIsReadOnly(t, ctx, readOnlyDB, fmt.Sprintf(
		"INSERT INTO %s (tenant_id, exposure) VALUES ('tenant-write', 1)", quoteMySQLIdentifier(table),
	))

	svc, tool, sourceRuleID := newCrossDatabaseSQLToolFixture(t, model.DBDriverMySQL, readOnlyDSN, table,
		"Cross database truncation", "SELECT tenant_id, exposure FROM "+table+" WHERE tenant_id = :tenant_id",
		`{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		`{"type":"object","required":["tenant_id","exposure"],"properties":{"tenant_id":{"type":"string"},"exposure":{"type":"number"}}}`,
		`{"read_only":true,"max_rows":2,"timeout_ms":5000,"allowed_tables":["`+table+`"],"allowed_columns":{"`+table+`":["tenant_id","exposure"]}}`,
	)
	assertCrossDatabaseSQLTruncation(t, ctx, svc, tool, sourceRuleID)
	timeoutSvc, timeoutTool, timeoutRuleID := newCrossDatabaseSQLToolFixture(t, model.DBDriverMySQL, readOnlyDSN, table,
		"Cross database timeout", "SELECT COUNT(*) AS total FROM "+table+" AS row_a JOIN "+table+
			" AS row_b ON row_b.tenant_id = row_a.tenant_id WHERE row_a.tenant_id = :tenant_id",
		`{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		`{"type":"object","required":["total"],"properties":{"total":{"type":"number"}}}`,
		`{"read_only":true,"max_rows":1,"timeout_ms":1,"allowed_tables":["`+table+`"],"allowed_columns":{"`+table+`":["tenant_id","exposure"]}}`,
	)
	assertCrossDatabaseSQLTimeout(t, ctx, timeoutSvc, timeoutTool, timeoutRuleID)
}

func provisionMySQLReadOnlyUser(t *testing.T, adminDSN string) string {
	t.Helper()
	adminConfig, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatalf("parse mysql admin connection: %v", err)
	}
	user := "rgx_sql_ro_" + id.New()[:16]
	password := id.New() + id.New()
	readOnlyConfig := adminConfig.Clone()
	readOnlyConfig.User = user
	readOnlyConfig.Passwd = password
	readOnlyDSN := readOnlyConfig.FormatDSN()

	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatalf("open mysql admin database: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Skipf("mysql admin integration database unavailable: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE USER %s IDENTIFIED BY %s", quoteMySQLAccount(user, "%"), quoteMySQLStringLiteral(password),
	)); err != nil {
		t.Fatalf("create mysql read-only user: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(cleanupContext, "DROP USER IF EXISTS "+quoteMySQLAccount(user, "%"))
	})
	databaseName := adminConfig.DBName
	if databaseName == "" {
		if err := adminDB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
			t.Fatalf("read mysql database name: %v", err)
		}
		if databaseName == "" {
			t.Fatal("mysql admin DSN does not select a database")
		}
	}
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT ON %s.* TO %s",
		quoteMySQLIdentifier(databaseName), quoteMySQLAccount(user, "%"),
	)); err != nil {
		t.Fatalf("grant mysql database select privilege: %v", err)
	}
	return readOnlyDSN
}

func newCrossDatabaseSQLToolFixture(
	t *testing.T, driver, dsn, table, name, sqlTemplate, parameterSchema, resultSchema, executionPolicy string,
) (*Service, *model.ToolRegistry, string) {
	t.Helper()
	svc := newToolRegistryService(t)
	tenantID, userID := id.New(), id.New()
	if err := svc.Store.CreateUser(context.Background(), &model.User{
		ID: userID, TenantID: tenantID, Username: "sql-runtime-" + userID,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create cross-database fixture user: %v", err)
	}
	connection, err := svc.CreateDBConnection(context.Background(), tenantID, userID, DBConnectionInput{
		Name: "Cross database", Driver: driver, DSNRef: "secret://tenant/cross-database",
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
	})
	if err != nil {
		t.Fatalf("create cross-database connection: %v", err)
	}
	template, err := svc.CreateQueryTemplate(context.Background(), tenantID, userID, QueryTemplateInput{
		Name: name, SQLTemplate: sqlTemplate, ParameterSchema: parameterSchema, ResultSchema: resultSchema,
		ExecutionPolicy: executionPolicy, AuthorizationScope: `{"roles":["tenant_admin"]}`, ConnectionID: connection.ID,
	})
	if err != nil {
		t.Fatalf("create cross-database query template: %v", err)
	}
	outputSchema, err := queryTemplateToolOutputSchema(template.ResultSchema)
	if err != nil {
		t.Fatalf("build cross-database tool output schema: %v", err)
	}
	tool, err := svc.CreateToolRegistry(context.Background(), tenantID, userID, ToolRegistryInput{
		ToolID: "sql_query", Version: "v1", ToolType: model.ToolTypeSQLQuery, Name: name,
		InputSchema: template.ParameterSchema, OutputSchema: outputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "query-template:" + template.ID,
	})
	if err != nil {
		t.Fatalf("create cross-database SQL tool: %v", err)
	}
	sourceRule, err := svc.CreateSourceRoutingRule(context.Background(), tenantID, userID, SourceRoutingRuleInput{
		AuthorizationScope:  `{"roles":["tenant_admin"]}`,
		SourceType:          model.SourceTypeDB,
		Matcher:             `{"intent":["cross_database_query"]}`,
		ToolID:              tool.ToolID,
		ToolVersion:         tool.Version,
		PolicyVersion:       "policy-v1",
		Priority:            10,
		ConfidenceThreshold: 0.8,
	})
	if err != nil {
		t.Fatalf("create cross-database source routing rule: %v", err)
	}
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(context.Context, *model.DBConnection) (string, error) {
		return dsn, nil
	})
	return svc, tool, sourceRule.ID
}

func assertCrossDatabaseSQLTruncation(
	t *testing.T, ctx context.Context, svc *Service, tool *model.ToolRegistry, sourceRuleID string,
) {
	t.Helper()
	result, err := svc.ExecuteToolRegistry(ctx, tool.TenantID, tool.CreatedBy, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: sourceRuleID, Input: json.RawMessage(`{"tenant_id":"tenant-a"}`),
	})
	if err != nil {
		t.Fatalf("execute cross-database SQL tool: %v", err)
	}
	var output struct {
		RowCount  int  `json:"row_count"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode cross-database SQL output: %v", err)
	}
	if output.RowCount != 2 || !output.Truncated {
		t.Fatalf("expected 2 truncated rows, got %+v", output)
	}
}

func assertCrossDatabaseSQLTimeout(
	t *testing.T, ctx context.Context, svc *Service, tool *model.ToolRegistry, sourceRuleID string,
) {
	t.Helper()
	if _, err := svc.ExecuteToolRegistry(ctx, tool.TenantID, tool.CreatedBy, tool.ID, ToolExecutionInput{
		SourceRoutingRuleID: sourceRuleID, Input: json.RawMessage(`{"tenant_id":"tenant-a"}`),
	}); err == nil {
		t.Fatal("cross-database SQL timeout must fail")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 502 || businessErr.Code != 50201 {
		t.Fatalf("expected 502/50201, got %#v", err)
	}
}

func assertCrossDatabaseAccountIsReadOnly(t *testing.T, ctx context.Context, database *sql.DB, writeQuery string) {
	t.Helper()
	if _, err := database.ExecContext(ctx, writeQuery); err == nil {
		t.Fatal("integration account must not have write privileges")
	}
}

func quotePostgreSQLIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quotePostgreSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func quoteMySQLAccountIdentifier(value string) string {
	user, host := value, "%"
	if index := strings.LastIndex(value, "@"); index >= 0 {
		user, host = value[:index], value[index+1:]
	}
	return quoteMySQLAccount(user, host)
}

func quoteMySQLAccount(user, host string) string {
	return quoteMySQLStringLiteral(user) + "@" + quoteMySQLStringLiteral(host)
}

func quoteMySQLStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func crossDatabaseMySQLName(dsn string) (string, error) {
	source, err := sql.Open("mysql", dsn)
	if err != nil {
		return "", err
	}
	defer func() { _ = source.Close() }()
	var databaseName string
	if err := source.QueryRow("SELECT DATABASE()").Scan(&databaseName); err != nil {
		return "", err
	}
	if databaseName == "" {
		return "", fmt.Errorf("mysql dsn does not select a database")
	}
	return databaseName, nil
}

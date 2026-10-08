package service

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

var dbConnectionDSNRefPattern = regexp.MustCompile(`^(secret|vault|kms)://[A-Za-z0-9._/-]{1,240}$`)

type DBConnectionInput struct {
	Name               string `json:"name" binding:"required"`
	Driver             string `json:"driver" binding:"required"`
	DSNRef             string `json:"dsn_ref" binding:"required"`
	AuthorizationScope string `json:"authorization_scope" binding:"required"`
	ReadOnly           *bool  `json:"read_only"`
	MaxOpenConns       int    `json:"max_open_conns"`
	MaxIdleConns       int    `json:"max_idle_conns"`
}

type DBConnectionSecretResolver interface {
	ResolveDBDSN(ctx context.Context, connection *model.DBConnection) (string, error)
}

type DBConnectionSecretResolverFunc func(ctx context.Context, connection *model.DBConnection) (string, error)

func (fn DBConnectionSecretResolverFunc) ResolveDBDSN(ctx context.Context, connection *model.DBConnection) (string, error) {
	return fn(ctx, connection)
}

type DBConnectionTestResult struct {
	Status         string     `json:"status"`
	Message        string     `json:"message"`
	ReadOnly       bool       `json:"read_only"`
	LastVerifiedAt *time.Time `json:"last_verified_at"`
}

func notFoundDBConnection() error { return httperr.NotFound("db connection not found") }

func (s *Service) ListDBConnections(
	ctx context.Context, tenantID string, filter repository.DBConnectionFilter, page, pageSize int,
) ([]model.DBConnection, int64, error) {
	filter.Driver = strings.TrimSpace(filter.Driver)
	filter.HealthStatus = strings.TrimSpace(filter.HealthStatus)
	return s.Store.ListDBConnections(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetDBConnection(ctx context.Context, tenantID, connectionID string) (*model.DBConnection, error) {
	connection, err := s.Store.GetDBConnection(ctx, tenantID, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, notFoundDBConnection()
	}
	return connection, nil
}

func (s *Service) CreateDBConnection(
	ctx context.Context, tenantID, userID string, input DBConnectionInput,
) (*model.DBConnection, error) {
	now := time.Now().UTC()
	connection, err := buildDBConnection(tenantID, userID, input, now)
	if err != nil {
		return nil, err
	}
	if err := s.Store.CreateDBConnection(ctx, connection); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, auditForDBConnection(connection, "db_connection.created", userID, now)); err != nil {
		return nil, err
	}
	return connection, nil
}

func (s *Service) UpdateDBConnection(
	ctx context.Context, tenantID, userID, connectionID string, input DBConnectionInput,
) (*model.DBConnection, error) {
	existing, err := s.GetDBConnection(ctx, tenantID, connectionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	updated, err := buildDBConnection(tenantID, existing.CreatedBy, input, now)
	if err != nil {
		return nil, err
	}
	updated.ID = existing.ID
	updated.HealthStatus = existing.HealthStatus
	updated.LastVerifiedAt = existing.LastVerifiedAt
	updated.CreatedAt = existing.CreatedAt
	if err := s.Store.UpdateDBConnection(ctx, updated); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, auditForDBConnection(updated, "db_connection.updated", userID, now)); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteDBConnection(ctx context.Context, tenantID, userID, connectionID string) error {
	connection, err := s.GetDBConnection(ctx, tenantID, connectionID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.Store.DeleteDBConnection(ctx, tenantID, connection.ID); err != nil {
		return err
	}
	return s.Store.CreateAudit(ctx, auditForDBConnection(connection, "db_connection.deleted", userID, now))
}

func (s *Service) TestDBConnection(ctx context.Context, tenantID, userID, connectionID string) (*DBConnectionTestResult, error) {
	connection, err := s.GetDBConnection(ctx, tenantID, connectionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result := &DBConnectionTestResult{
		Status: model.DBHealthUnhealthy, Message: "connection failed", ReadOnly: connection.ReadOnly,
		LastVerifiedAt: &now,
	}
	if s.SecretResolver == nil {
		return nil, httperr.New(503, 50300, "database connection secret resolver is not configured")
	}
	dsn, err := s.SecretResolver.ResolveDBDSN(ctx, connection)
	if err != nil {
		result.Message = "credential resolution failed"
		return nil, s.finishDBConnectionTest(ctx, connection, userID, result, now, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pingDBConnection(probeCtx, connection.Driver, dsn); err != nil {
		return nil, s.finishDBConnectionTest(ctx, connection, userID, result, now, err)
	}
	if err := verifyDBConnectionReadOnly(probeCtx, connection.Driver, dsn); err != nil {
		result.Message = "read-only verification failed"
		result.ReadOnly = false
		return nil, s.finishDBConnectionTest(ctx, connection, userID, result, now, err)
	}
	verifiedAt := now
	result.Status = model.DBHealthHealthy
	result.Message = "connection verified"
	result.LastVerifiedAt = &verifiedAt
	return result, s.finishDBConnectionTest(ctx, connection, userID, result, now, nil)
}

func (s *Service) finishDBConnectionTest(
	ctx context.Context, connection *model.DBConnection, userID string,
	result *DBConnectionTestResult, now time.Time, cause error,
) error {
	connection.HealthStatus = result.Status
	connection.LastVerifiedAt = result.LastVerifiedAt
	if err := s.Store.UpdateDBConnection(ctx, connection); err != nil {
		return err
	}
	audit := &model.AuditLog{
		ID: id.New(), TenantID: connection.TenantID, ActorTenantID: connection.TenantID,
		TargetTenantID: connection.TenantID, UserID: userID, Action: "db_connection.tested",
		Resource: "db-connection", ResourceID: connection.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"name": connection.Name, "driver": connection.Driver, "read_only": connection.ReadOnly,
			"health_status": result.Status, "message": result.Message,
		}), At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
	if err := s.Store.CreateAudit(ctx, audit); err != nil {
		return err
	}
	if cause != nil {
		return httperr.New(502, 50200, "database connection test failed")
	}
	return nil
}

func pingDBConnection(ctx context.Context, driver, dsn string) error {
	var database *sql.DB
	var err error
	switch driver {
	case model.DBDriverPostgres:
		database, err = sql.Open("pgx", dsn)
	case model.DBDriverMySQL:
		database, err = sql.Open("mysql", dsn)
	case model.DBDriverSQLite:
		database, err = sql.Open("sqlite", dsn)
	default:
		return httperr.BadRequest(40171, "driver must be postgres, mysql or sqlite")
	}
	if err != nil {
		return err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxIdleTime(0)
	if err := database.PingContext(ctx); err != nil {
		return err
	}
	var one int
	return database.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

var mysqlWritePrivilegePattern = regexp.MustCompile(
	`(?i)\b(all privileges|insert|update|delete|create|drop|alter|index|trigger|references|file|super|shutdown)\b`,
)

func verifyDBConnectionReadOnly(ctx context.Context, driver, dsn string) error {
	database, err := sql.Open(databaseDriverName(driver), dsn)
	if err != nil {
		return err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	database.SetConnMaxIdleTime(0)
	if err := database.PingContext(ctx); err != nil {
		return err
	}
	switch driver {
	case model.DBDriverSQLite:
		sqliteConnection, err := database.Conn(ctx)
		if err != nil {
			return err
		}
		defer sqliteConnection.Close()
		if _, err := sqliteConnection.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return err
		}
		if _, err := sqliteConnection.ExecContext(ctx,
			"CREATE TABLE rgx_read_only_probe (id INTEGER)",
		); err == nil {
			return httperr.New(502, 50201, "database account is writable")
		}
	case model.DBDriverPostgres:
		var superuser, writableTable bool
		if err := database.QueryRowContext(ctx, `SELECT
			COALESCE((SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user), false),
			COALESCE(BOOL_OR(
				pg_catalog.has_table_privilege(current_user, c.oid, 'INSERT') OR
				pg_catalog.has_table_privilege(current_user, c.oid, 'UPDATE') OR
				pg_catalog.has_table_privilege(current_user, c.oid, 'DELETE') OR
				pg_catalog.has_table_privilege(current_user, c.oid, 'TRUNCATE')
			), false)
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema')`,
		).Scan(&superuser, &writableTable); err != nil {
			return err
		}
		if superuser || writableTable {
			return httperr.New(502, 50201, "database account is writable")
		}
	case model.DBDriverMySQL:
		rows, err := database.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var grant string
			if err := rows.Scan(&grant); err != nil {
				return err
			}
			if mysqlWritePrivilegePattern.MatchString(grant) {
				return httperr.New(502, 50201, "database account is writable")
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	default:
		return httperr.BadRequest(40171, "driver must be postgres, mysql or sqlite")
	}
	return nil
}

func buildDBConnection(tenantID, userID string, input DBConnectionInput, now time.Time) (*model.DBConnection, error) {
	name := strings.TrimSpace(input.Name)
	driver := strings.TrimSpace(input.Driver)
	dsnRef := strings.TrimSpace(input.DSNRef)
	authorizationScope, err := validateAuthorizationScope(input.AuthorizationScope, "authorization_scope")
	if err != nil {
		return nil, err
	}
	if name == "" || len(name) > 128 {
		return nil, httperr.BadRequest(40170, "name must contain 1 to 128 characters")
	}
	switch driver {
	case model.DBDriverPostgres, model.DBDriverMySQL, model.DBDriverSQLite:
	default:
		return nil, httperr.BadRequest(40171, "driver must be postgres, mysql or sqlite")
	}
	if len(dsnRef) > 256 || !dbConnectionDSNRefPattern.MatchString(dsnRef) {
		return nil, httperr.BadRequest(40172, "dsn_ref must be a secret, vault or kms reference")
	}
	if input.ReadOnly != nil && !*input.ReadOnly {
		return nil, httperr.BadRequest(40173, "db connection must be read_only in v0.2.0")
	}
	maxOpenConns := input.MaxOpenConns
	if maxOpenConns == 0 {
		maxOpenConns = 5
	}
	if maxOpenConns < 1 || maxOpenConns > 100 {
		return nil, httperr.BadRequest(40174, "max_open_conns must be between 1 and 100")
	}
	maxIdleConns := input.MaxIdleConns
	if maxIdleConns == 0 {
		maxIdleConns = 1
	}
	if maxIdleConns < 0 || maxIdleConns > maxOpenConns {
		return nil, httperr.BadRequest(40175, "max_idle_conns must be between 0 and max_open_conns")
	}
	return &model.DBConnection{
		ID: id.New(), TenantID: tenantID, Name: name, Driver: driver, DSNRef: dsnRef,
		AuthorizationScope: authorizationScope,
		ReadOnly:           true, MaxOpenConns: maxOpenConns, MaxIdleConns: maxIdleConns,
		HealthStatus: model.DBHealthUnknown, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func auditForDBConnection(
	connection *model.DBConnection, action, userID string, now time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: connection.TenantID, ActorTenantID: connection.TenantID, TargetTenantID: connection.TenantID,
		UserID: userID, Action: action, Resource: "db-connection", ResourceID: connection.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"name": connection.Name, "driver": connection.Driver, "read_only": connection.ReadOnly,
			"max_open_conns": connection.MaxOpenConns, "max_idle_conns": connection.MaxIdleConns,
			"health_status": connection.HealthStatus,
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

package db

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// runtimeRoleAttributes captures the PostgreSQL role flags that must never be
// enabled for the application's runtime connection.
type runtimeRoleAttributes struct {
	Superuser  bool
	CreateDB   bool
	CreateRole bool
	BypassRLS  bool
}

// VerifyPostgresRuntimePrivileges enforces the database-side least privilege
// baseline. It intentionally fails closed: an over-privileged runtime account
// prevents startup instead of depending only on application checks.
func VerifyPostgresRuntimePrivileges(gdb *gorm.DB) error {
	if gdb == nil {
		return fmt.Errorf("database connection is nil")
	}
	if gdb.Dialector.Name() != "postgres" {
		return fmt.Errorf("runtime privilege checks apply only to postgres")
	}

	var attributes runtimeRoleAttributes
	if err := gdb.Raw(`SELECT rolsuper, rolcreatedb, rolcreaterole, rolbypassrls
		FROM pg_roles WHERE rolname = current_user`).Scan(&attributes).Error; err != nil {
		return fmt.Errorf("inspect runtime database role: %w", err)
	}
	if attributes.Superuser {
		return fmt.Errorf("runtime database role must not be superuser")
	}
	if attributes.CreateDB {
		return fmt.Errorf("runtime database role must not have CREATEDB")
	}
	if attributes.CreateRole {
		return fmt.Errorf("runtime database role must not have CREATEROLE")
	}
	if attributes.BypassRLS {
		return fmt.Errorf("runtime database role must not have BYPASSRLS")
	}

	var ownedTables int64
	if err := gdb.Raw(`SELECT COUNT(*)
		FROM pg_tables
		WHERE schemaname = ANY (current_schemas(false))
			AND tablename LIKE 'rgx\_%'
			AND pg_has_role(current_user, tableowner, 'USAGE')`).Scan(&ownedTables).Error; err != nil {
		return fmt.Errorf("inspect runtime table ownership: %w", err)
	}
	if ownedTables > 0 {
		return fmt.Errorf("runtime database role owns or can inherit ownership of %d rgx table(s)", ownedTables)
	}

	var writableSchemas int64
	if err := gdb.Raw(`SELECT COUNT(*)
		FROM pg_namespace
		WHERE nspname = ANY (current_schemas(false))
			AND has_schema_privilege(current_user, oid, 'CREATE')`).Scan(&writableSchemas).Error; err != nil {
		return fmt.Errorf("inspect runtime schema privileges: %w", err)
	}
	if writableSchemas > 0 {
		return fmt.Errorf("runtime database role must not have CREATE privilege in accessible schemas")
	}

	var rowSecurity struct {
		Enabled bool
		Forced  bool
	}
	if err := gdb.Raw(`SELECT relrowsecurity AS enabled, relforcerowsecurity AS forced
		FROM pg_class
		JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
		WHERE nspname = ANY (current_schemas(false)) AND relname = 'rgx_tenant'`).Scan(&rowSecurity).Error; err != nil {
		return fmt.Errorf("inspect tenant row security: %w", err)
	}
	if !rowSecurity.Enabled || !rowSecurity.Forced {
		return fmt.Errorf("tenant row security must be enabled and forced")
	}

	var deletePolicies []struct {
		Name string
		Expr string
	}
	if err := gdb.Raw(`SELECT polname AS name, COALESCE(pg_get_expr(polqual, polrelid), '') AS expr
		FROM pg_policy
		JOIN pg_class ON pg_class.oid = pg_policy.polrelid
		JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
		WHERE nspname = ANY (current_schemas(false))
			AND relname = 'rgx_tenant'
			AND polcmd = 'd'
		ORDER BY polname`).Scan(&deletePolicies).Error; err != nil {
		return fmt.Errorf("inspect tenant delete policy: %w", err)
	}
	if len(deletePolicies) != 1 || deletePolicies[0].Name != "rgx_tenant_runtime_delete" {
		return fmt.Errorf("tenant table must have exactly the runtime delete policy")
	}
	if !strings.Contains(deletePolicies[0].Expr, "type") || !strings.Contains(deletePolicies[0].Expr, "platform") {
		return fmt.Errorf("tenant delete policy must exclude platform rows")
	}

	var grants struct {
		Select   bool
		Insert   bool
		Update   bool
		Delete   bool
		Truncate bool
	}
	if err := gdb.Raw(`SELECT
			has_table_privilege(current_user, 'rgx_tenant', 'SELECT') AS select,
			has_table_privilege(current_user, 'rgx_tenant', 'INSERT') AS insert,
			has_table_privilege(current_user, 'rgx_tenant', 'UPDATE') AS update,
			has_table_privilege(current_user, 'rgx_tenant', 'DELETE') AS delete,
			has_table_privilege(current_user, 'rgx_tenant', 'TRUNCATE') AS truncate`).Scan(&grants).Error; err != nil {
		return fmt.Errorf("inspect tenant table privileges: %w", err)
	}
	missing := make([]string, 0, 4)
	if !grants.Select {
		missing = append(missing, "SELECT")
	}
	if !grants.Insert {
		missing = append(missing, "INSERT")
	}
	if !grants.Update {
		missing = append(missing, "UPDATE")
	}
	if !grants.Delete {
		missing = append(missing, "DELETE")
	}
	if len(missing) > 0 {
		return fmt.Errorf("runtime database role lacks tenant privileges: %s", strings.Join(missing, ", "))
	}
	if grants.Truncate {
		return fmt.Errorf("runtime database role must not have tenant TRUNCATE privilege")
	}

	var migrationTablePrivileges struct {
		Insert   bool
		Update   bool
		Delete   bool
		Truncate bool
	}
	if err := gdb.Raw(`SELECT
			has_table_privilege(current_user, 'rgx_schema_version', 'INSERT') AS insert,
			has_table_privilege(current_user, 'rgx_schema_version', 'UPDATE') AS update,
			has_table_privilege(current_user, 'rgx_schema_version', 'DELETE') AS delete,
			has_table_privilege(current_user, 'rgx_schema_version', 'TRUNCATE') AS truncate`).
		Scan(&migrationTablePrivileges).Error; err != nil {
		return fmt.Errorf("inspect migration table privileges: %w", err)
	}
	if migrationTablePrivileges.Insert || migrationTablePrivileges.Update ||
		migrationTablePrivileges.Delete || migrationTablePrivileges.Truncate {
		return fmt.Errorf("runtime database role must not mutate migration metadata")
	}
	return nil
}

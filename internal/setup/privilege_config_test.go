package setup

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestMigrateDatabaseRequiresSeparatePostgresRolesWhenEnforced(t *testing.T) {
	dbc := config.Database{
		Driver: "postgres", Host: "localhost", Port: 5432,
		User: "ragflow_x_runtime", Password: "runtime-secret", Name: "ragflow_x",
		EnforcePrivileges: true,
	}
	err := migrateDatabase(dbc)
	if err == nil || !strings.Contains(err.Error(), "migration credentials are required") {
		t.Fatalf("expected migration credentials error, got %v", err)
	}

	dbc.MigrationUser = dbc.User
	dbc.MigrationPassword = "migration-secret"
	err = migrateDatabase(dbc)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("expected distinct migration role error, got %v", err)
	}
}

func TestMigrateDatabaseRequiresCompletePostgresMigrationCredentials(t *testing.T) {
	dbc := config.Database{Driver: "postgres", MigrationUser: "ragflow_x_migration"}
	err := migrateDatabase(dbc)
	if err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("expected incomplete credentials error, got %v", err)
	}
}

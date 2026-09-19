package db

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ScenarioID: SC-PG-001
func TestP0_PG_001_PostgresRuntimePrivilegeBaseline(t *testing.T) {
	adminDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if adminDSN == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL baseline")
	}

	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get postgres admin db: %v", err)
	}
	defer adminSQL.Close()

	if err := Migrate(admin); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := VerifyPostgresRuntimePrivileges(admin); err == nil {
		t.Fatal("migration/bootstrap role must fail the runtime privilege baseline")
	}

	var staleRoles []string
	if err := admin.Raw(`SELECT rolname FROM pg_roles WHERE rolname LIKE 'rgx_runtime_probe_%'`).
		Scan(&staleRoles).Error; err != nil {
		t.Fatalf("list stale runtime roles: %v", err)
	}
	for _, staleRole := range staleRoles {
		cleanupRuntimeRole(t, admin, staleRole)
	}

	role := "rgx_runtime_probe_" + id.New()
	password := id.New()
	if err := admin.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", role)).Error; err != nil {
		t.Fatalf("drop stale runtime role: %v", err)
	}
	if err := admin.Exec(fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", role, password)).Error; err != nil {
		t.Fatalf("create runtime role: %v", err)
	}
	defer func() {
		cleanupRuntimeRole(t, admin, role)
	}()

	for _, query := range []string{
		`SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), ?::name)`,
		`SELECT format('GRANT USAGE ON SCHEMA %I TO %I', current_schema(), ?::name)`,
	} {
		var statement string
		if err := admin.Raw(query, role).Scan(&statement).Error; err != nil {
			t.Fatalf("build runtime grant: %v", err)
		}
		if err := admin.Exec(statement).Error; err != nil {
			t.Fatalf("grant runtime access: %v", err)
		}
	}

	var tableGrants []string
	if err := admin.Raw(`SELECT format(
			'GRANT SELECT, INSERT, UPDATE, DELETE ON %I.%I TO %I',
			schemaname, tablename, ?::name)
		FROM pg_tables
		WHERE schemaname = current_schema() AND tablename LIKE 'rgx\_%'`, role).
		Scan(&tableGrants).Error; err != nil {
		t.Fatalf("build runtime table grants: %v", err)
	}
	for _, statement := range tableGrants {
		if err := admin.Exec(statement).Error; err != nil {
			t.Fatalf("grant runtime table access: %v", err)
		}
	}
	if err := admin.Exec(fmt.Sprintf(
		"REVOKE ALL PRIVILEGES ON %s.rgx_schema_version FROM %s", "public", role)).Error; err != nil {
		t.Fatalf("revoke migration metadata privileges: %v", err)
	}

	var sequenceGrant string
	if err := admin.Raw(`SELECT format(
			'GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I',
			current_schema(), ?::name)`, role).Scan(&sequenceGrant).Error; err != nil {
		t.Fatalf("build runtime sequence grant: %v", err)
	}
	if err := admin.Exec(sequenceGrant).Error; err != nil {
		t.Fatalf("grant runtime sequence access: %v", err)
	}

	runtimeDSN := postgresRuntimeDSN(adminDSN, role, password)
	if runtimeDSN == "" {
		t.Fatalf("unsupported PostgreSQL DSN format: %q", adminDSN)
	}
	runtime, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres runtime: %v", err)
	}
	runtimeSQL, err := runtime.DB()
	if err != nil {
		t.Fatalf("get postgres runtime db: %v", err)
	}
	defer runtimeSQL.Close()

	if err := VerifyPostgresRuntimePrivileges(runtime); err != nil {
		t.Fatalf("runtime privilege verification: %v", err)
	}

	tenant := model.Tenant{
		ID: "pg-rt-" + id.New()[:8], Name: "runtime privilege probe",
		Type: model.TenantTypeWorkspace, Status: model.TenantStatusActive,
	}
	if err := admin.Create(&tenant).Error; err != nil {
		t.Fatalf("create probe tenant: %v", err)
	}
	if err := runtime.Delete(&model.Tenant{}, "id = ?", tenant.ID).Error; err != nil {
		t.Fatalf("runtime role must delete a workspace tenant: %v", err)
	}
	platformDelete := runtime.Where("type = ?", model.TenantTypePlatform).
		Delete(&model.Tenant{})
	if platformDelete.Error != nil {
		t.Fatalf("platform delete probe: %v", platformDelete.Error)
	}
	if platformDelete.RowsAffected != 0 {
		t.Fatal("runtime role must not delete the platform tenant")
	}
	var platformCount int64
	if err := admin.Model(&model.Tenant{}).Where("type = ?", model.TenantTypePlatform).
		Count(&platformCount).Error; err != nil {
		t.Fatalf("count platform tenant: %v", err)
	}
	if platformCount != 1 {
		t.Fatalf("platform tenant count = %d, want 1", platformCount)
	}
}

func postgresRuntimeDSN(baseDSN string, user string, password string) string {
	if parsed, err := url.Parse(baseDSN); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = url.UserPassword(user, password)
		return parsed.String()
	}
	fields := strings.Fields(baseDSN)
	if len(fields) == 0 {
		return ""
	}
	out := make([]string, 0, len(fields)+2)
	replacedUser := false
	replacedPassword := false
	for _, field := range fields {
		switch {
		case strings.HasPrefix(strings.ToLower(field), "user="):
			out = append(out, "user="+user)
			replacedUser = true
		case strings.HasPrefix(strings.ToLower(field), "password="):
			out = append(out, "password="+password)
			replacedPassword = true
		default:
			out = append(out, field)
		}
	}
	if !replacedUser {
		out = append(out, "user="+user)
	}
	if !replacedPassword {
		out = append(out, "password="+password)
	}
	return strings.Join(out, " ")
}

func TestPostgresRuntimeDSNSupportsURLAndKeywordForms(t *testing.T) {
	urlDSN := postgresRuntimeDSN("postgres://admin:secret@localhost:5432/ragflow-x?sslmode=disable", "probe", "probe-password")
	if urlDSN != "postgres://probe:probe-password@localhost:5432/ragflow-x?sslmode=disable" {
		t.Fatalf("unexpected URL DSN: %s", urlDSN)
	}
	keywordDSN := postgresRuntimeDSN("host=localhost port=5432 user=admin password=secret dbname=ragflow-x sslmode=disable", "probe", "probe-password")
	for _, expected := range []string{"user=probe", "password=probe-password", "sslmode=disable"} {
		if !strings.Contains(keywordDSN, expected) {
			t.Fatalf("keyword DSN missing %s: %s", expected, keywordDSN)
		}
	}
}

func cleanupRuntimeRole(t *testing.T, admin *gorm.DB, role string) {
	t.Helper()
	if err := admin.Exec(fmt.Sprintf("DROP OWNED BY %s", role)).Error; err != nil {
		t.Errorf("revoke runtime role ownership: %v", err)
	}
	if err := admin.Exec(fmt.Sprintf("DROP ROLE %s", role)).Error; err != nil {
		t.Errorf("drop runtime role: %v", err)
	}
}

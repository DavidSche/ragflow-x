// Command testcontract-pg runs the internal/db PostgreSQL contract tests
// (freeze guards, migration idempotency, approval baseline, audit anchor,
// runtime privileges, enterprise reference contract) against a disposable
// database so immutable probe rows and temporary roles never pollute the
// shared development database.
//
// Usage:
//
//	RGX_TEST_POSTGRES_DSN="host=... dbname=..." go run ./cmd/testcontract-pg
//
// The DSN only needs to point at any database on the target instance; the
// tool creates a sibling "rgx_freeze_contract_*" database, points the tests
// at it, runs `go test`, and drops the database afterwards (pass -keep to
// retain it for debugging). Set PATTERN via -run to select other tests.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// defaultPattern covers every internal/db test that requires
// RGX_TEST_POSTGRES_DSN.
const defaultPattern = "TestP0_PG_001_PostgresMigrationIsIdempotentUnderContract|" +
	"TestP0_PG_001_PostgresApprovalOrganizationAdaptability|" +
	"TestP0_AUDIT_001_PostgresAuditAnchorImmutability|" +
	"TestP0_PG_001_PostgresRuntimePrivilegeBaseline|" +
	"TestP0_PG_001_EnterpriseConnectionReferenceContractPostgreSQL|" +
	"TestAnswerDeliveryFreezeGuardsPostgreSQL"

const dbPrefix = "rgx_freeze_contract_"

func main() {
	run := flag.String("run", defaultPattern, "go test -run pattern")
	pkg := flag.String("pkg", "./internal/db", "package to test")
	timeout := flag.Duration("timeout", 600*time.Second, "go test -timeout")
	keep := flag.Bool("keep", false, "keep the disposable database after the run")
	flag.Parse()

	baseDSN := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if strings.TrimSpace(baseDSN) == "" {
		fatal("RGX_TEST_POSTGRES_DSN is required (any database on the target instance)")
	}

	admin, err := openAdmin(baseDSN)
	if err != nil {
		fatal("connect admin: %v", err)
	}
	defer admin.Close()

	probe := dbPrefix + strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()))
	if err := createDB(admin, probe); err != nil {
		fatal("create disposable database: %v", err)
	}

	testDSN := swapDatabase(baseDSN, probe)
	fmt.Printf("[testcontract-pg] disposable database: %s\n", probe)

	code := runTests(testDSN, *run, *pkg, *timeout)

	if *keep {
		fmt.Printf("[testcontract-pg] kept disposable database: %s (-keep was set)\n", probe)
	} else if err := dropDB(admin, probe); err != nil {
		fmt.Fprintf(os.Stderr, "[testcontract-pg] WARNING drop disposable database: %v\n", err)
	} else {
		fmt.Printf("[testcontract-pg] dropped disposable database: %s\n", probe)
	}
	os.Exit(code)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[testcontract-pg] "+format+"\n", args...)
	os.Exit(1)
}

func runTests(dsn, pattern, pkg string, timeout time.Duration) int {
	cmd := exec.Command("go", "test", pkg, "-run", pattern, "-count=1", "-v", "-timeout", timeout.String())
	cmd.Env = append(os.Environ(), "RGX_TEST_POSTGRES_DSN="+dsn)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[testcontract-pg] run tests: %v\n", err)
		return 1
	}
	return 0
}

// swapDatabase rewrites the target database of a DSN in either URL or
// keyword form without touching credentials.
func swapDatabase(dsn, dbname string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" && strings.Contains(parsed.Scheme, "postgres") {
		parsed.Path = "/" + dbname
		return parsed.String()
	}
	fields := strings.Fields(dsn)
	out := make([]string, 0, len(fields)+1)
	replaced := false
	for _, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "dbname=") {
			out = append(out, "dbname="+dbname)
			replaced = true
			continue
		}
		out = append(out, field)
	}
	if !replaced {
		out = append(out, "dbname="+dbname)
	}
	return strings.Join(out, " ")
}

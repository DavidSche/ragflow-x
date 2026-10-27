package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// openAdmin opens a connection to the admin DSN for database bookkeeping.
func openAdmin(dsn string) (*sql.DB, error) {
	return sql.Open("pgx", dsn)
}

// createDB creates the disposable database. CREATE DATABASE cannot run
// inside a transaction, so retry once if the connection pool hands back a
// connection that still sits in one.
func createDB(admin *sql.DB, dbname string) error {
	if err := validateDBName(dbname); err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		// Identifiers cannot be parameterized; the name is validated above.
		_, err := admin.Exec(fmt.Sprintf(`CREATE DATABASE %q`, dbname))
		if err == nil {
			return nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "transaction") {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}

// dropDB drops the disposable database, force-closing lingering test
// connections first so a slow-exiting go test cannot block the drop.
func dropDB(admin *sql.DB, dbname string) error {
	if err := validateDBName(dbname); err != nil {
		return err
	}
	if _, err := admin.Exec(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, dbname); err != nil {
		return err
	}
	// Identifiers cannot be parameterized; the name is validated above.
	_, err := admin.Exec(fmt.Sprintf(`DROP DATABASE %q`, dbname))
	return err
}

func validateDBName(dbname string) error {
	if !strings.HasPrefix(dbname, dbPrefix) {
		return fmt.Errorf("refusing to touch a database not prefixed %s", dbPrefix)
	}
	for _, r := range dbname {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("refusing unsafe database name %q", dbname)
		}
	}
	return nil
}

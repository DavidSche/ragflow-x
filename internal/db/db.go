// Package db manages the operational (shadow) store connection.
package db

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// withSQLitePragmas appends connection-level SQLite pragmas that are safe for
// production development setups and tests alike: WAL journaling (readers do
// not block the writer, far fewer fsyncs), NORMAL synchronous mode (the
// recommended pairing with WAL) and a busy timeout so transient lock
// contention waits instead of failing immediately. Pragmas already supplied
// by the caller win over the defaults.
func withSQLitePragmas(dsn string) (string, error) {
	base, query := dsn, ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		base, query = dsn[:i], dsn[i+1:]
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		// Not a parseable query string; open the DSN as-is.
		return dsn, nil
	}
	defaults := []string{"busy_timeout(5000)", "synchronous(NORMAL)"}
	for _, def := range defaults {
		name := def[:strings.IndexByte(def, '(')]
		direct := values["_"+name]
		covered := len(direct) > 0
		for _, p := range values["_pragma"] {
			if strings.HasPrefix(p, name+"(") {
				covered = true
				break
			}
		}
		if !covered {
			values.Add("_pragma", def)
		}
	}
	return base + "?" + values.Encode(), nil
}

// Open creates a GORM connection based on configuration. Supported drivers:
// "postgres" and "sqlite" (sqlite is for local development and tests).
func Open(cfg config.Database) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "postgres":
		dsn := fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode,
		)
		dialector = postgres.Open(dsn)
	case "sqlite":
		dsn := cfg.DSN
		if dsn == "" {
			dsn = "file:./ragflow-x.db?cache=shared"
		}
		dsn, err := withSQLitePragmas(dsn)
		if err != nil {
			return nil, err
		}
		dialector = sqlite.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	gdb, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	maxOpen := cfg.MaxOpenConns
	maxIdle := cfg.MaxIdleConns
	if cfg.Driver == "sqlite" {
		if maxOpen <= 0 {
			maxOpen = 1
		}
		if maxIdle < 0 {
			maxIdle = 1
		}
		// MaxIdleConns(0) would drop the pool after every query, forcing a
		// fresh SQLite file connection per query. Keep the single connection
		// cached by default.
		if maxIdle == 0 {
			maxIdle = maxOpen
		}
	} else {
		if maxOpen <= 0 {
			maxOpen = 25
		}
		if maxIdle < 0 {
			maxIdle = 5
		}
		if maxIdle == 0 {
			maxIdle = maxOpen
		}
		if maxIdle > maxOpen {
			maxIdle = maxOpen
		}
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	if cfg.ConnMaxLifetimeSec > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeSec) * time.Second)
	}
	if cfg.ConnMaxIdleTimeSec > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(cfg.ConnMaxIdleTimeSec) * time.Second)
	}
	return gdb, nil
}

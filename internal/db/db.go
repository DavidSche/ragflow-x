// Package db manages the operational (shadow) store connection.
package db

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

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

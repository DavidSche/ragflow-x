package db

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestVerifyPostgresRuntimePrivilegesRequiresPostgres(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: "file::memory:?cache=private"})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	defer sqlDB.Close()
	err = VerifyPostgresRuntimePrivileges(gdb)
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("expected postgres-only check, got %v", err)
	}
}

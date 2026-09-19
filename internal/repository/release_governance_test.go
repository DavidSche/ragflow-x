package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestListReleaseCandidatesUsesIndependentCountAndListQueries(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "release.db")
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}

	store := NewStore(gdb)
	ctx := context.Background()
	tenantID := id.New()
	now := time.Now().UTC()
	for version := int64(1); version <= 2; version++ {
		candidate := &model.ReleaseCandidate{
			ID: id.New(), CandidateID: id.New(), TenantID: tenantID, CandidateVersion: version,
			TargetType: "assistant", TargetID: id.New(), TargetVersion: "1",
			CandidateManifest: "{}", CandidateHash: id.New(), HashAlgorithm: "sha256",
			Status: model.CandidateDraft, CreatedBy: id.New(), CreatedAt: now, UpdatedAt: now,
		}
		if err := store.CreateReleaseCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := store.ListReleaseCandidates(ctx, tenantID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 || items[0].CandidateVersion != 2 {
		t.Fatalf("unexpected result: total=%d len=%d first_version=%d", total, len(items), items[0].CandidateVersion)
	}
}

func TestListReleaseCandidatesPostgreSQLPagination(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL pagination contract")
	}
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	store := NewStore(gdb)
	ctx := context.Background()
	tenantID := id.New()
	err = store.WithinTransaction(ctx, func(tx Store) error {
		now := time.Now().UTC()
		for version := int64(1); version <= 2; version++ {
			candidate := &model.ReleaseCandidate{
				ID: id.New(), CandidateID: id.New(), TenantID: tenantID, CandidateVersion: version,
				TargetType: "assistant", TargetID: id.New(), TargetVersion: "1",
				CandidateManifest: "{}", CandidateHash: id.New(), HashAlgorithm: "sha256",
				Status: model.CandidateDraft, CreatedBy: id.New(), CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.CreateReleaseCandidate(ctx, candidate); err != nil {
				return err
			}
		}
		items, total, err := tx.ListReleaseCandidates(ctx, tenantID, 1, 10)
		if err != nil {
			return err
		}
		if total != 2 || len(items) != 2 || items[0].CandidateVersion != 2 {
			return fmt.Errorf("unexpected result: total=%d len=%d first_version=%d", total, len(items), items[0].CandidateVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

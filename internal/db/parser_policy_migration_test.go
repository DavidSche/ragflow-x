package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestParserPolicyActiveScopeUniqueIndex(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "parser-policy-index.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := model.ParserPolicy{
		ID: id.New(), TenantID: id.New(), DocumentType: "pdf_table",
		ParseMode: model.ParseModeBuiltin, ChunkMethod: "table",
		ParserConfig: "{}", FallbackPolicy: "{}", QualityProfileID: id.New(),
		Active: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := policy
	duplicate.ID = id.New()
	if err := gdb.Create(&duplicate).Error; err == nil {
		t.Fatal("expected active parser policy uniqueness violation")
	}
	inactive := map[string]interface{}{
		"id": id.New(), "tenant_id": policy.TenantID, "document_type": policy.DocumentType,
		"parse_mode": policy.ParseMode, "chunk_method": policy.ChunkMethod,
		"parser_config": "{}", "fallback_policy": "{}", "quality_profile_id": policy.QualityProfileID,
		"active": false, "created_at": now, "updated_at": now,
	}
	if err := gdb.Model(&model.ParserPolicy{}).Create(inactive).Error; err != nil {
		t.Fatal(err)
	}
}

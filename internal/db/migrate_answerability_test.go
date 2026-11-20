package db

// Migration 185's backfill (doc/301 T12).
//
// A backfill that is never executed in a test is a claim, not a mechanism. These
// tests run the real function against a real schema and check the two shapes that
// matter: a parsable claim is projected, and an unparsable one is left empty rather
// than guessed at.

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func backfillFixture(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "backfill.db")})
	if err != nil {
		t.Fatal(err)
	}
	// Windows will not delete a temp dir whose sqlite file is still open, and a
	// failing cleanup hides whether the test itself passed.
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	// Migrate only to the version before the column existed, so the seed looks like
	// production data: JSON present, projection column absent.
	if err := MigrateTo(gdb, 184); err != nil {
		t.Fatal(err)
	}
	return gdb
}

func TestBackfillKnowledgeOpsAbstainReason(t *testing.T) {
	gdb := backfillFixture(t)
	rows := []model.KnowledgeOpsEvent{
		{
			ID: "ev-parsable", RequestID: "r1", TenantID: "t1", UserID: "u1",
			AppType: "chat", AppID: "c1", Question: "q1", QuestionHash: "h1",
			Status:            model.KnowledgeOpsCompleted,
			AnswerabilityJSON: `{"answerable":false,"abstain_reason":"no_evidence"}`,
		},
		{
			ID: "ev-garbage", RequestID: "r2", TenantID: "t1", UserID: "u1",
			AppType: "chat", AppID: "c1", Question: "q2", QuestionHash: "h2",
			Status:            model.KnowledgeOpsCompleted,
			AnswerabilityJSON: `{not json`,
		},
		{
			ID: "ev-none", RequestID: "r3", TenantID: "t1", UserID: "u1",
			AppType: "chat", AppID: "c1", Question: "q3", QuestionHash: "h3",
			Status:            model.KnowledgeOpsCompleted,
			AnswerabilityJSON: `{"answerable":true,"abstain_reason":"none"}`,
		},
		{
			ID: "ev-absent", RequestID: "r4", TenantID: "t1", UserID: "u1",
			AppType: "chat", AppID: "c1", Question: "q4", QuestionHash: "h4",
			Status: model.KnowledgeOpsCompleted,
		},
	}
	for index := range rows {
		if err := gdb.Create(&rows[index]).Error; err != nil {
			t.Fatalf("seed row %s: %v", rows[index].ID, err)
		}
	}

	// The column exists only after the migration runs.
	if err := MigrateTo(gdb, 185); err != nil {
		t.Fatalf("migrate to 185: %v", err)
	}
	if err := backfillKnowledgeOpsAbstainReason(gdb); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	// Idempotent: a second pass must not change anything and must not error.
	if err := backfillKnowledgeOpsAbstainReason(gdb); err != nil {
		t.Fatalf("backfill re-run: %v", err)
	}

	got := map[string]string{}
	var stored []model.KnowledgeOpsEvent
	if err := gdb.Select("id", "abstain_reason").Find(&stored).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, row := range stored {
		got[row.ID] = row.AbstainReason
	}
	if got["ev-parsable"] != "no_evidence" {
		t.Fatalf("parsable claim must be projected, got %q", got["ev-parsable"])
	}
	if got["ev-none"] != "none" {
		t.Fatalf("`none` is a real value and must be projected, got %q", got["ev-none"])
	}
	if got["ev-garbage"] != "" {
		t.Fatalf("an unparsable claim must stay empty, got %q", got["ev-garbage"])
	}
	if got["ev-absent"] != "" {
		t.Fatalf("a turn with no claim must stay empty, got %q", got["ev-absent"])
	}
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests need a live database and are therefore opt-in, exactly like the
// PostgreSQL migration contract in internal/db: without RGX_GOLDTOOL_DSN they skip
// instead of failing, because "no database here" is not a defect in the tool.
//
// They are worth having anyway, because the SQL in pool.go is the one part of this
// command that cannot be checked offline: a column rename would leave every unit
// test green and turn the export into a runtime error the first time someone tries
// to build the gold set.
func liveDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	if dsn == "" {
		t.Skipf("set %s to run the live pool export tests", envDSN)
	}
	return dsn
}

// TestOpenReadOnlyRefusesWritesIsTheWholePoint proves the promise in main.go's
// comment: the transaction handed to the exporter cannot write. Without this test,
// "read-only" is a comment that a future refactor can quietly delete.
func TestOpenReadOnlyRefusesWrites(t *testing.T) {
	dsn := liveDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, tx, err := openReadOnly(ctx, dsn)
	if err != nil {
		t.Fatalf("openReadOnly: %v", err)
	}
	defer db.Close()
	defer tx.Rollback() //nolint:errcheck

	var one int
	if err := tx.QueryRowContext(ctx, "select 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("a read must work: %v", err)
	}

	// Any write must be refused by the database, not by our discipline.
	_, err = tx.ExecContext(ctx, `insert into rgx_report (id, tenant_id, title, owner_user_id, template_key, status, expires_at, created_at, updated_at)
		values ('goldtool-readonly-probe', 'probe', 'probe', 'probe', 'probe', 'active', now(), now(), now())`)
	if err == nil {
		t.Fatal("INSERT succeeded inside a read-only transaction: the gold tool could corrupt a customer database")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("the refusal must name read-only, got %v", err)
	}
}

// TestPoolExportAgainstLiveDatabase walks real published revisions and asserts the
// shape the annotator receives. The single most important assertion is that pool
// items carry *full text*: a redacted pool would make annotation impossible, while a
// redacted gold file is mandatory.
func TestPoolExportAgainstLiveDatabase(t *testing.T) {
	dsn := liveDSN(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "pool.jsonl")

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	code := run([]string{"pool", "--dsn", dsn, "--out", out, "--max-revisions", "6"}, devnull, devnull)
	// 0 = coverage floors met, 3 = exported but the database cannot meet them yet.
	// 1 and 2 mean the tool or the SQL is broken.
	if code != exitOK && code != exitIncomplete {
		t.Fatalf("pool exited %d; a live export must be 0 (complete) or 3 (incomplete), never a failure", code)
	}

	items, err := readItems(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no items exported: either the database has no published revision with citations, or the joins are wrong")
	}

	for _, it := range items {
		if it.ClaimID == "" || it.ClaimText == "" {
			t.Fatalf("item %q has no claim identity: %+v", it.ItemID, it)
		}
		if it.SectionPrompt == "" {
			t.Fatalf("item %q has no section prompt: the judging context cannot be reconstructed", it.ItemID)
		}
		if it.Source.RevisionID == "" || it.Source.RevisionStatus != "published" {
			t.Fatalf("item %q comes from %q/%q; only published revisions are immutable enough to annotate", it.ItemID, it.Source.RevisionID, it.Source.RevisionStatus)
		}
		if len(it.Evidence) == 0 {
			t.Fatalf("item %q has no evidence", it.ItemID)
		}
		for _, ev := range it.Evidence {
			if ev.Content == "" {
				t.Fatalf("pool item %q evidence %q has no text: the pool is the file the annotator reads", it.ItemID, ev.SnapshotID)
			}
			if ev.ContentHash == "" {
				t.Fatalf("pool item %q evidence %q has no hash", it.ItemID, ev.SnapshotID)
			}
			if ev.ContentChars == 0 {
				t.Fatalf("pool item %q evidence %q has no length", it.ItemID, ev.SnapshotID)
			}
		}
		// A pool item has no labels yet; if one appears, assemble is being skipped.
		if it.GoldVerdict != "" {
			t.Fatalf("pool item %q already carries a verdict %q: a pool is not a gold file", it.ItemID, it.GoldVerdict)
		}
		// ...and it must never mix a cited chunk into the candidate pool.
		for _, c := range it.CandidatePool {
			for _, ev := range it.Evidence {
				if c.ChunkID == ev.ChunkID && c.ContentHash == ev.ContentHash {
					t.Fatalf("item %q lists chunk %q as both cited and candidate", it.ItemID, c.ChunkID)
				}
			}
		}
	}
}

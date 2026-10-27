package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newConnectorSyncTestService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "connector-sync.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "connector-sync-test-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

// TestSyncConnectorOnceS3TenThousandUnchangedObjects exercises the exact
// doc/227 C2.2 acceptance boundary: 10k S3 objects, 100-object pages, and a
// previously checkpointed cursor. The run must consume every page, classify
// every object as unchanged, and leave a complete cursor without downloading
// a single object.
func TestSyncConnectorOnceS3TenThousandUnchangedObjects(t *testing.T) {
	const total = 10000
	const pageSize = model.ConnectorMaxBatch

	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/bucket" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("prefix") != "docs" {
			t.Fatalf("unexpected list query: %s", r.URL.RawQuery)
		}
		offset, err := strconv.Atoi(r.URL.Query().Get("continuation-token"))
		if err != nil && r.URL.Query().Get("continuation-token") != "" {
			t.Fatalf("invalid continuation token: %v", err)
		}
		requests++
		end := offset + pageSize
		if end > total {
			end = total
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)
		if end < total {
			fmt.Fprintf(w, "<IsTruncated>true</IsTruncated><NextContinuationToken>%d</NextContinuationToken>", end)
		} else {
			fmt.Fprint(w, "<IsTruncated>false</IsTruncated>")
		}
		for i := offset; i < end; i++ {
			fmt.Fprintf(w,
				"<Contents><Key>docs/f%05d.txt</Key><LastModified>2026-10-25T01:02:03.000Z</LastModified><ETag>\"etag-%d\"</ETag><Size>10</Size></Contents>",
				i, i)
		}
		fmt.Fprint(w, `</ListBucketResult>`)
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	cursor := make(map[string]string, total)
	for i := 0; i < total; i++ {
		cursor[fmt.Sprintf("docs/f%05d.txt", i)] = fmt.Sprintf("etag-%d", i)
	}
	credential, hasCred, _, err := svc.sealConnectorCredential(`{"access_key_id":"ak","access_secret":"sk"}`)
	if err != nil {
		t.Fatalf("seal credential: %v", err)
	}
	c := &model.Connector{
		ID:                 "conn-10k-s3",
		TenantID:           "tenant-10k",
		Name:               "s3-10k",
		Provider:           model.ConnectorProviderS3,
		AdapterKind:        model.ConnectorAdapterNative,
		TargetDatasetID:    "ds-10k",
		SourceOwnershipKey: "tenk:s3:tenant-10k",
		SpecJSON:           fmt.Sprintf(`{"endpoint":%q,"region":"us-east-1","bucket":"bucket","prefix":"docs"}`, srv.URL),
		CredentialCrypted:  credential,
		HasCredential:      hasCred,
		CredentialVersion:  1,
		CursorJSON:         mustCursorJSON(cursor),
		ScheduleKind:       model.ConnectorScheduleManual,
		Enabled:            true,
		Status:             model.ConnectorStatusIdle,
	}
	if err := svc.Store.CreateConnector(context.Background(), c); err != nil {
		t.Fatalf("seed connector: %v", err)
	}

	start := time.Now()
	stats, err := svc.syncConnectorOnce(context.Background(), c)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sync 10k: %v", err)
	}
	if stats.Skipped != total || stats.Unchanged != total {
		t.Fatalf("stats = skipped:%d unchanged:%d, want %d", stats.Skipped, stats.Unchanged, total)
	}
	if stats.Added != 0 || stats.Changed != 0 || stats.Deleted != 0 || stats.Failed != 0 || len(stats.SkippedItems) != 0 {
		t.Fatalf("10k unchanged run must not mutate anything: %+v", stats)
	}
	if requests != total/pageSize {
		t.Fatalf("list requests = %d, want %d", requests, total/pageSize)
	}
	persisted, err := svc.Store.GetConnector(context.Background(), c.TenantID, c.ID)
	if err != nil {
		t.Fatalf("reload connector: %v", err)
	}
	if got := len(parseConnectorCursor(persisted.CursorJSON)); got != total {
		t.Fatalf("persisted cursor entries = %d, want %d", got, total)
	}
	t.Logf("10k S3 unchanged sync: %s, %d list pages, cursor preserved", elapsed, requests)
}

// TestSyncConnectorOnceTombstonesOnlyAfterCompleteScan protects doc/225 F5:
// a cancellation mid-scan must never treat the unscanned tail as deleted.
func TestSyncConnectorOnceTombstonesOnlyAfterCompleteScan(t *testing.T) {
	secondPageStarted := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken><Contents><Key>docs/only.txt</Key><LastModified>2026-10-25T01:02:03.000Z</LastModified><ETag>"e1"</ETag><Size>10</Size></Contents></ListBucketResult>`)
			return
		}
		once.Do(func() { close(secondPageStarted) })
		<-r.Context().Done()
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	credential, hasCred, _, err := svc.sealConnectorCredential(`{"access_key_id":"ak","access_secret":"sk"}`)
	if err != nil {
		t.Fatal(err)
	}
	c := &model.Connector{
		ID: "conn-cancel", TenantID: "tenant-cancel", Name: "s3-cancel",
		Provider: model.ConnectorProviderS3, AdapterKind: model.ConnectorAdapterNative,
		TargetDatasetID: "ds-cancel", SourceOwnershipKey: "cancel:s3",
		SpecJSON:          fmt.Sprintf(`{"endpoint":%q,"bucket":"bucket","prefix":"docs"}`, srv.URL),
		CredentialCrypted: credential, HasCredential: hasCred, CredentialVersion: 1,
		CursorJSON: `{"docs/old.txt":"old","docs/only.txt":"e1"}`, ScheduleKind: model.ConnectorScheduleManual,
		Enabled: true, Status: model.ConnectorStatusIdle,
	}
	if err := svc.Store.CreateConnector(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-secondPageStarted
		cancel()
	}()
	stats, err := svc.syncConnectorOnce(ctx, c)
	if err == nil {
		t.Fatal("cancelled scan must return an error")
	}
	if stats.Deleted != 0 {
		t.Fatalf("cancelled scan must not tombstone the unscanned tail: %+v", stats)
	}
	persisted, err := svc.Store.GetConnector(context.Background(), c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(persisted.CursorJSON, `docs/old.txt`) || !strings.Contains(persisted.CursorJSON, `docs/only.txt`) {
		t.Fatalf("partial checkpoint must preserve the scanned front of the cursor, got %s", persisted.CursorJSON)
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// seedPreviewS3Connector stores a native S3 connector whose endpoint is the
// supplied test server. The credential is sealed through the same production
// path so PreviewConnector exercises decryption, not a test-only shortcut.
func seedPreviewS3Connector(t *testing.T, svc *Service, endpoint, cursorJSON string) *model.Connector {
	t.Helper()
	credential, hasCred, _, err := svc.sealConnectorCredential(`{"access_key_id":"ak","access_secret":"sk"}`)
	if err != nil {
		t.Fatalf("seal credential: %v", err)
	}
	c := &model.Connector{
		ID:                 "conn-preview",
		TenantID:           "tenant-preview",
		Name:               "s3-preview",
		Provider:           model.ConnectorProviderS3,
		AdapterKind:        model.ConnectorAdapterNative,
		TargetDatasetID:    "ds-preview",
		SourceOwnershipKey: "preview:s3:tenant-preview",
		SpecJSON:           fmt.Sprintf(`{"endpoint":%q,"region":"us-east-1","bucket":"bucket","prefix":"docs"}`, endpoint),
		CredentialCrypted:  credential,
		HasCredential:      hasCred,
		CredentialVersion:  1,
		CursorJSON:         cursorJSON,
		ScheduleKind:       model.ConnectorScheduleManual,
		Enabled:            true,
		Status:             model.ConnectorStatusIdle,
	}
	if err := svc.Store.CreateConnector(context.Background(), c); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return c
}

func TestPreviewConnectorClassifiesAgainstCursorWithoutWritingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/bucket" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated>`)
		for _, e := range []struct{ key, etag string }{
			{"docs/a.txt", "e1"},
			{"docs/b.txt", "e2"},
			{"docs/c.txt", "e3"},
		} {
			fmt.Fprintf(w, `<Contents><Key>%s</Key><LastModified>2026-10-25T01:02:03.000Z</LastModified><ETag>"%s"</ETag><Size>10</Size></Contents>`, e.key, e.etag)
		}
		fmt.Fprint(w, `</ListBucketResult>`)
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	cursor := `{"docs/a.txt":"e1","docs/c.txt":"old","docs/gone.txt":"e9"}`
	c := seedPreviewS3Connector(t, svc, srv.URL, cursor)

	out, err := svc.PreviewConnector(context.Background(), false, []string{c.TenantID}, c.ID, 0)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.Added != 1 || out.Changed != 1 || out.Deleted != 1 || out.Skipped != 1 {
		t.Fatalf("preview counts = added:%d changed:%d deleted:%d skipped:%d, want 1/1/1/1",
			out.Added, out.Changed, out.Deleted, out.Skipped)
	}
	changes := map[string]string{}
	for _, e := range out.Entries {
		changes[e.Key] = e.Change
	}
	want := map[string]string{
		"docs/a.txt":    "unchanged",
		"docs/b.txt":    "added",
		"docs/c.txt":    "changed",
		"docs/gone.txt": "deleted",
	}
	for key, change := range want {
		if changes[key] != change {
			t.Fatalf("%s change = %q, want %q (all=%v)", key, changes[key], change, changes)
		}
	}

	// Preview must be read-only: the cursor is a run-owned checkpoint and is
	// only committed by syncConnectorOnce after a page has actually been ingested.
	stored, err := svc.Store.GetConnector(context.Background(), c.TenantID, c.ID)
	if err != nil {
		t.Fatalf("reload connector: %v", err)
	}
	if stored.CursorJSON != cursor {
		t.Fatalf("preview mutated cursor: got %q, want %q", stored.CursorJSON, cursor)
	}
}

func TestPreviewConnectorHonorsAndCapsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated>`)
		for i := 0; i < 60; i++ {
			fmt.Fprintf(w, `<Contents><Key>docs/f%02d.txt</Key><LastModified>2026-10-25T01:02:03.000Z</LastModified><ETag>"e%d"</ETag><Size>10</Size></Contents>`, i, i)
		}
		fmt.Fprint(w, `</ListBucketResult>`)
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	c := seedPreviewS3Connector(t, svc, srv.URL, "")

	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"default", 0, 20},
		{"explicit", 5, 5},
		{"cap", 50, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := svc.PreviewConnector(context.Background(), false, []string{c.TenantID}, c.ID, tc.limit)
			if err != nil {
				t.Fatalf("preview limit %d: %v", tc.limit, err)
			}
			if len(out.Entries) != tc.want {
				t.Fatalf("entries = %d, want %d", len(out.Entries), tc.want)
			}
		})
	}

	if _, err := svc.PreviewConnector(context.Background(), false, []string{c.TenantID}, c.ID, 51); err == nil {
		t.Fatal("limit 51 must be rejected")
	} else if he, ok := err.(*httperr.Error); !ok || he.Status != http.StatusBadRequest || he.Code != 40337 {
		t.Fatalf("limit 51 error = %#v, want 400/40337", err)
	}
}

func TestRunConnectorSyncTimeoutPersistsFailedRunAndDegradedStatus(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	svc.SetConnectorConfig(config.Connector{
		MaxConcurrent: 1, RunTimeoutSec: 1, RetentionDays: 90, PreviewLimit: 20, MaxObjectMB: 25,
	})
	c := seedPreviewS3Connector(t, svc, srv.URL, "")

	begin := time.Now()
	run, err := svc.runConnectorSync(context.Background(), c, model.ConnectorRunTriggerManual, "tester")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("timeout run took %s, want bounded by run_timeout_sec", elapsed)
	}
	if run == nil {
		t.Fatal("run record missing")
	}
	stored, gerr := svc.Store.GetConnectorRun(context.Background(), c.TenantID, run.ID)
	if gerr != nil {
		t.Fatalf("get run: %v", gerr)
	}
	if stored == nil || stored.Status != model.ConnectorRunStatusFailed {
		t.Fatalf("stored run = %#v, want failed", stored)
	}
	conn, cerr := svc.Store.GetConnector(context.Background(), c.TenantID, c.ID)
	if cerr != nil {
		t.Fatalf("get connector: %v", cerr)
	}
	if conn.Status != model.ConnectorStatusDegraded {
		t.Fatalf("connector status = %q, want degraded", conn.Status)
	}
}

func TestRunConnectorSyncCancellationPersistsCancelledRun(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	svc.SetConnectorConfig(config.Connector{
		MaxConcurrent: 1, RunTimeoutSec: 30, RetentionDays: 90, PreviewLimit: 20, MaxObjectMB: 25,
	})
	c := seedPreviewS3Connector(t, svc, srv.URL, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		run *model.ConnectorRun
		err error
	}, 1)
	go func() {
		run, err := svc.runConnectorSync(ctx, c, model.ConnectorRunTriggerManual, "tester")
		done <- struct {
			run *model.ConnectorRun
			err error
		}{run, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not start")
	}
	cancel()
	res := <-done
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", res.err)
	}
	stored, err := svc.Store.GetConnectorRun(context.Background(), c.TenantID, res.run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if stored == nil || stored.Status != model.ConnectorRunStatusCancelled {
		t.Fatalf("stored run = %#v, want cancelled", stored)
	}
}

func TestRunConnectorSyncEnforcesProcessWideConcurrency(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	svc := newConnectorSyncTestService(t)
	svc.SetConnectorConfig(config.Connector{
		MaxConcurrent: 1, RunTimeoutSec: 30, RetentionDays: 90, PreviewLimit: 20, MaxObjectMB: 25,
	})
	c := seedPreviewS3Connector(t, svc, srv.URL, "")

	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.runConnectorSync(firstCtx, c, model.ConnectorRunTriggerManual, "first")
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first sync did not start")
	}

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer secondCancel()
	begin := time.Now()
	if _, err := svc.runConnectorSync(secondCtx, c, model.ConnectorRunTriggerManual, "second"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second run error = %v, want deadline waiting for semaphore", err)
	}
	if elapsed := time.Since(begin); elapsed < 150*time.Millisecond {
		t.Fatalf("second run returned in %s, want to wait on the semaphore", elapsed)
	}

	firstCancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first run error = %v, want canceled", err)
	}
	active, err := svc.Store.ListActiveConnectorRuns(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("active runs: %v", err)
	}
	if active != 0 {
		t.Fatalf("active runs = %d after cancellation, want 0", active)
	}
}

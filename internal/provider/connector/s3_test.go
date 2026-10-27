package connector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestS3ProviderListPaginatesAndSkipsDirectoryPlaceholders(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		if r.URL.Path != "/bucket" {
			t.Fatalf("path = %q, want /bucket", r.URL.Path)
		}
		if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("prefix") != "docs" {
			t.Fatalf("unexpected list query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>tok-2</NextContinuationToken>
  <Contents><Key>docs/</Key><Size>0</Size></Contents>
  <Contents><Key>docs/a.md</Key><LastModified>2026-10-25T01:02:03.000Z</LastModified><ETag>"etag-a"</ETag><Size>12</Size></Contents>
  <Contents><Key>docs/b.txt</Key><LastModified>2026-10-25T04:05:06.000Z</LastModified><ETag></ETag><Size>34</Size></Contents>
</ListBucketResult>`)
			return
		}
		if r.URL.Query().Get("continuation-token") != "tok-2" {
			t.Fatalf("unexpected continuation token: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>docs/c.md</Key><LastModified>2026-10-25T07:08:09.000Z</LastModified><ETag>"etag-c"</ETag><Size>56</Size></Contents>
</ListBucketResult>`)
	}))
	defer srv.Close()

	p := &S3Provider{}
	spec := map[string]any{"endpoint": srv.URL, "region": "us-east-1", "bucket": "bucket", "prefix": "docs"}
	cred := map[string]string{"access_key_id": "ak", "access_secret": "sk"}

	entries, next, err := p.List(context.Background(), spec, cred, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "tok-2" || len(entries) != 2 {
		t.Fatalf("first page: next=%q entries=%+v", next, entries)
	}
	if entries[0].Key != "docs/a.md" || entries[0].Revision != "etag-a" || entries[0].Size != 12 {
		t.Fatalf("first entry: %+v", entries[0])
	}
	if entries[1].Key != "docs/b.txt" || entries[1].Revision == "" {
		t.Fatalf("etag-less entry should fall back to mtime-size: %+v", entries[1])
	}

	entries, next, err = p.List(context.Background(), spec, cred, next)
	if err != nil {
		t.Fatal(err)
	}
	if next != "" || len(entries) != 1 || entries[0].Key != "docs/c.md" || entries[0].Revision != "etag-c" {
		t.Fatalf("second page: next=%q entries=%+v", next, entries)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2: %v", len(requests), requests)
	}
}

// TestEncodeCanonicalQuerySortsByKeyThenValue pins the SigV4 canonical query
// order: parameters by name, then by value. Sorting "k=v" strings as a whole
// is only equivalent for single-valued parameters.
func TestEncodeCanonicalQuerySortsByKeyThenValue(t *testing.T) {
	// Multi-valued parameters: a=[2,1] and b=[2,10] arrive unsorted.
	got := encodeCanonicalQuery("b=2&a=2&a=1&b=10")
	want := "a=1&a=2&b=10&b=2"
	if got != want {
		t.Fatalf("encodeCanonicalQuery = %q, want %q", got, want)
	}

	// Single-valued parameters keep the previous (equivalent) behavior,
	// including SigV4 strict encoding of special characters.
	got = encodeCanonicalQuery("prefix=a/b c&list-type=2&max-keys=500")
	want = "list-type=2&max-keys=500&prefix=a%2Fb%20c"
	if got != want {
		t.Fatalf("encodeCanonicalQuery = %q, want %q", got, want)
	}

	if got := encodeCanonicalQuery(""); got != "" {
		t.Fatalf("empty query = %q, want empty", got)
	}
}

// TestS3ProviderReusesClientsWithinInstance pins the pooling contract: one
// provider instance (one sync run) shares its list and download clients, so
// keep-alive connections survive across pages and objects.
func TestS3ProviderReusesClientsWithinInstance(t *testing.T) {
	p := &S3Provider{}
	if p.listClient() != p.listClient() {
		t.Fatal("list client should be stable within a provider instance")
	}
	if p.downloadClient() != p.downloadClient() {
		t.Fatal("download client should be stable within a provider instance")
	}
	if p.listClient() == p.downloadClient() {
		t.Fatal("list and download clients should be distinct (different timeouts)")
	}
}

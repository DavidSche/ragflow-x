package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func TestValidateConnectorSchedule(t *testing.T) {
	cases := []struct {
		kind  string
		value int
		ok    bool
	}{
		{model.ConnectorScheduleManual, 0, true},
		{model.ConnectorScheduleInterval, 5, true},
		{model.ConnectorScheduleInterval, 30, true},
		{model.ConnectorScheduleInterval, 4, false},
		{model.ConnectorScheduleInterval, 0, false},
		{model.ConnectorScheduleDaily, 0, true},
		{model.ConnectorScheduleDaily, 720, true},
		{model.ConnectorScheduleDaily, 1440, false},
		{model.ConnectorScheduleDaily, -1, false},
		{"cron", 0, false},
		{"", 0, true}, // empty -> manual
	}
	for _, tc := range cases {
		err := validateConnectorSchedule(tc.kind, tc.value)
		if (err == nil) != tc.ok {
			t.Errorf("validateConnectorSchedule(%q,%d) err=%v, want ok=%v", tc.kind, tc.value, err, tc.ok)
		}
	}
}

func TestParseConnectorInputs(t *testing.T) {
	spec, err := parseConnectorSpec(`{"root":"/tmp/x","recursive":true}`)
	if err != nil || spec["root"] != "/tmp/x" || spec["recursive"] != true {
		t.Fatalf("parseConnectorSpec = %v, %v", spec, err)
	}
	if _, err := parseConnectorSpec(`{bad`); err == nil {
		t.Fatal("invalid spec should error")
	}
	empty, err := parseConnectorSpec("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty spec = %v, %v", empty, err)
	}

	cred, err := parseConnectorCred(`{"access_key":"AK","secret_key":"SK"}`)
	if err != nil || cred["access_key"] != "AK" {
		t.Fatalf("parseConnectorCred = %v, %v", cred, err)
	}
	nonString, err := parseConnectorCred(`{"n":1}`)
	if err == nil {
		t.Fatalf("non-string credential value should error, got %v", nonString)
	}

	cursor := parseConnectorCursor(`{"a.txt":"1-11","b.txt":"2-22"}`)
	if len(cursor) != 2 || cursor["b.txt"] != "2-22" {
		t.Fatalf("parseConnectorCursor = %v", cursor)
	}
	if got := parseConnectorCursor(`{garbage`); len(got) != 0 {
		t.Fatalf("broken cursor should reset to empty, got %v", got)
	}
}

func TestConnectorSourceKeyNamespace(t *testing.T) {
	c := &model.Connector{ID: "abc123", Provider: model.ConnectorProviderS3}
	k := connectorSourceKey(c, "bucket/dir/file.txt")
	want := "connector:abc123:bucket/dir/file.txt"
	if k != want {
		t.Fatalf("connectorSourceKey = %q, want %q", k, want)
	}
}

func TestConnectorOwnershipKeyIsCanonicalAndCredentialNeutral(t *testing.T) {
	base := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderLocalFolder, "ds-1", map[string]any{"root": "/data/./docs/"})
	if got := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderLocalFolder, "ds-1", map[string]any{"root": "/data/docs"}); got != base {
		t.Fatalf("equivalent roots must share identity: %q != %q", got, base)
	}
	if got := connectorSourceOwnershipKey("tenant-b", model.ConnectorProviderLocalFolder, "ds-1", map[string]any{"root": "/data/docs"}); got == base {
		t.Fatalf("tenant must be part of identity")
	}
	if got := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderLocalFolder, "ds-2", map[string]any{"root": "/data/docs"}); got == base {
		t.Fatalf("target dataset must be part of identity")
	}
	s3a := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderS3, "ds-1", map[string]any{
		"endpoint": "https://minio.example.com/", "region": "us-east-1", "bucket": "kb", "prefix": "/docs/",
	})
	s3b := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderS3, "ds-1", map[string]any{
		"endpoint": "https://MINIO.example.com", "region": "us-east-1", "bucket": "kb", "prefix": "docs",
	})
	if s3a != s3b {
		t.Fatalf("s3 endpoint casing/trailing slash must not fork identity")
	}
	if s3a == base {
		t.Fatalf("provider must be part of identity")
	}
	davA := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderWebDAV, "ds-1", map[string]any{
		"base_url": "https://DAV.example.com/dav/", "path": "/docs/",
	})
	davB := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderWebDAV, "ds-1", map[string]any{
		"base_url": "https://dav.example.com", "path": "/dav/docs",
	})
	if davA != davB {
		t.Fatalf("equivalent webdav base/path spellings must share identity")
	}
	davC := connectorSourceOwnershipKey("tenant-a", model.ConnectorProviderWebDAV, "ds-1", map[string]any{
		"base_url": "https://dav.example.com/dav", "path": "/Docs",
	})
	if davC == davA {
		t.Fatalf("webdav collection paths are case-sensitive and must not be folded")
	}
}

func TestValidateConnectorAdapterFailsClosed(t *testing.T) {
	if err := validateConnectorAdapter(model.ConnectorAdapterNative, ""); err != nil {
		t.Fatalf("native adapter = %v", err)
	}
	if err := validateConnectorAdapter(model.ConnectorAdapterRAGFlow, "conn-1"); err == nil {
		t.Fatal("ragflow adapter must be rejected until the business loop is closed")
	}
	if err := validateConnectorAdapter(model.ConnectorAdapterNative, "conn-1"); err == nil {
		t.Fatal("native adapter must reject an external connector id")
	}
}

func TestConnectorCreateInputFromPayload(t *testing.T) {
	payload := map[string]any{
		"name":              "local-src",
		"provider":          "local_folder",
		"adapter_kind":      "native",
		"target_dataset_id": "ds-9",
		"spec":              map[string]any{"root": "/tmp/x"},
		"credential":        map[string]any{"k1": "v1"},
		"schedule":          "daily",
		"schedule_value":    float64(720),
		"sensitive":         true,
		"enabled":           false,
	}
	in, err := connectorCreateInputFromPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "local-src" || in.Provider != "local_folder" || in.TargetDatasetID != "ds-9" || in.AdapterKind != "native" {
		t.Fatalf("input head = %+v", in)
	}
	if in.ScheduleKind != "daily" || in.ScheduleValue != 720 || !in.Sensitive {
		t.Fatalf("input schedule %+v", in)
	}
	if in.Enabled == nil || *in.Enabled {
		t.Fatalf("enabled should round-trip from payload, got %v", in.Enabled)
	}
	if in.SpecJSON != `{"root":"/tmp/x"}` {
		t.Fatalf("specJson = %q", in.SpecJSON)
	}
	if in.CredentialJSON != `{"k1":"v1"}` {
		t.Fatalf("credJson = %q", in.CredentialJSON)
	}
}

func TestConnectorCredentialDecryptFailureIsFailClosed(t *testing.T) {
	other := make([]byte, 32)
	for i := range other {
		other[i] = 0x01
	}
	sealed, err := crypto.Encrypt(other, `{"access_key":"AK"}`)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{EncryptKey: make([]byte, 32)}
	_, gotErr := svc.connectorCredential(context.Background(), &model.Connector{ID: "co-1", CredentialCrypted: sealed})
	he, ok := gotErr.(*httperr.Error)
	if !ok || he.Code != 40340 {
		t.Fatalf("decrypt failure = %v, want 40340", gotErr)
	}
}

func TestValidateConnectorApprovalDeleteNeedsNoProvider(t *testing.T) {
	err := validateConnectorApproval(context.Background(), &Service{}, &model.Approval{
		Action: model.ApprovalActionDelete, ObjectID: "co-1",
	})
	if err != nil {
		t.Fatalf("delete approval validation = %v", err)
	}
	err = validateConnectorApproval(context.Background(), &Service{}, &model.Approval{
		Action: model.ApprovalActionDelete,
	})
	if err == nil {
		t.Fatal("delete approval without object id should fail")
	}
	// Credential-only rotation payload has no provider and must validate.
	err = validateConnectorApproval(context.Background(), &Service{}, &model.Approval{
		Action: model.ApprovalActionRotateCredential, ObjectID: "co-1", PayloadJSON: `{"credential":{"access_key":"AK"}}`,
	})
	if err != nil {
		t.Fatalf("credential-only rotate validation = %v", err)
	}
}

func TestSensitiveConnectorRequiresApproval(t *testing.T) {
	svc := &Service{}
	_, hold, err := svc.ApprovalGate(context.Background(), "u1", model.ApprovalObjectConnector, model.ApprovalActionCreate, "new:s3", map[string]any{"sensitive": true}, "")
	if err != nil || hold {
		t.Fatalf("unforced gate without policy = hold=%v err=%v", hold, err)
	}
	_, _, err = svc.ApprovalGateForce(context.Background(), "u1", model.ApprovalObjectConnector, model.ApprovalActionCreate, "new:s3", map[string]any{"sensitive": true}, "")
	he, ok := err.(*httperr.Error)
	if !ok || he.Code != 40086 {
		t.Fatalf("forced gate with approval disabled = %v, want 40086", err)
	}
}

func TestConnectorApprovalPayloadSealsCredential(t *testing.T) {
	svc := &Service{EncryptKey: []byte("0123456789abcdef0123456789abcdef")}
	payload := map[string]any{
		"provider":   model.ConnectorProviderS3,
		"credential": map[string]any{"access_key": "AK", "secret_key": "SK"},
	}
	sealed, err := svc.SealConnectorApprovalPayload(payload)
	if err != nil {
		t.Fatalf("seal = %v", err)
	}
	if _, leaked := sealed["credential"]; leaked {
		t.Fatal("sealed approval payload must not carry plaintext credential")
	}
	if sealed["credential_sealed"] == "" || sealed["credential_sealed"] == nil {
		t.Fatal("sealed approval payload missing ciphertext")
	}
	opened, err := svc.openConnectorApprovalCredential(sealed)
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	cred, _ := opened["credential"].(map[string]any)
	if cred["access_key"] != "AK" || cred["secret_key"] != "SK" {
		t.Fatalf("round-trip credential = %#v", opened["credential"])
	}
	in, err := connectorUpdateInputFromPayload(opened)
	if err != nil || !strings.Contains(in.CredentialJSON, `"access_key":"AK"`) {
		t.Fatalf("opened payload must feed the executor: %#v err=%v", in, err)
	}
	other := &Service{EncryptKey: []byte("ffffffffffffffffffffffffffffffff")}
	if _, err := other.openConnectorApprovalCredential(sealed); err == nil {
		t.Fatal("opening with a different key must fail")
	}
}

func TestConnectorSyncRetryPolicy(t *testing.T) {
	transient := errors.New("provider scan failed: connection reset")
	if !connectorSyncRetryable(transient) {
		t.Fatal("transient provider error must be retryable")
	}
	for _, err := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		httperr.BadRequest(40333, "unsupported provider"),
		httperr.New(500, 40340, "credential decrypt failed"),
	} {
		if connectorSyncRetryable(err) {
			t.Fatalf("%v must not be retried by the job runner", err)
		}
	}
	if connectorSyncRetryable(nil) {
		t.Fatal("nil error is not a retry")
	}
}

func TestConnectorFinalAttempt(t *testing.T) {
	cases := []struct {
		attempts int
		maxRetry int
		final    bool
	}{
		{0, 3, false},
		{1, 3, false},
		{2, 3, true},
		{0, 1, true},
		{0, 0, false}, // repository default is 3
	}
	for _, tc := range cases {
		got := connectorFinalAttempt(&model.Job{Attempts: tc.attempts, MaxRetry: tc.maxRetry})
		if got != tc.final {
			t.Errorf("final(attempts=%d,max=%d)=%v want %v", tc.attempts, tc.maxRetry, got, tc.final)
		}
	}
	if !connectorFinalAttempt(nil) {
		t.Fatal("nil job must be treated as final")
	}
}

func TestConnectorSkippedDetailIsBoundedAndReasonsPreserved(t *testing.T) {
	stats := &connectorRunStats{}
	stats.addSkip("huge.bin", "large", 26<<20)
	if stats.Skipped != 1 || len(stats.SkippedItems) != 1 {
		t.Fatalf("skip stats = %+v", stats)
	}
	if stats.SkippedItems[0].Key != "huge.bin" || stats.SkippedItems[0].Reason != "large" {
		t.Fatalf("skip detail = %+v", stats.SkippedItems[0])
	}
	for i := 0; i < connectorSkippedDetailLimit+5; i++ {
		stats.addSkip(fmt.Sprintf("k-%d", i), "large", 1)
	}
	if len(stats.SkippedItems) != connectorSkippedDetailLimit {
		t.Fatalf("skip detail len = %d, want cap %d", len(stats.SkippedItems), connectorSkippedDetailLimit)
	}
}

// TestTruncateAuditDetailIsRuneSafe guards the audit-detail truncation: a raw
// byte cut at 1800 on CJK content produces invalid UTF-8, which PostgreSQL
// rejects on write. The result must stay valid UTF-8 and within the budget.
func TestTruncateAuditDetailIsRuneSafe(t *testing.T) {
	// 1000 CJK chars = 3000 bytes; boundary 1800 is rune-aligned here.
	cjk := strings.Repeat("知", 1000)
	got := truncateAuditDetail(cjk)
	if len(got) > 1800 {
		t.Fatalf("len = %d, want <= 1800", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated detail is invalid UTF-8")
	}
	if want := 1800; len(got) != want {
		t.Fatalf("len = %d, want %d", len(got), want)
	}

	// Off-boundary: 601 CJK chars = 1803 bytes; cutting at 1800 lands inside
	// the last rune, so the result must back off to 1800 (600 full runes).
	got = truncateAuditDetail(strings.Repeat("知", 601))
	if len(got) > 1800 || !utf8.ValidString(got) {
		t.Fatalf("len = %d valid=%v, want <=1800 and valid", len(got), utf8.ValidString(got))
	}
	if want := 1800; len(got) != want {
		t.Fatalf("len = %d, want %d", len(got), want)
	}
}

// TestIsConnectorOwnershipConflictPrefersGormSignal pins the error mapping:
// GORM's normalized duplicate-key error must map without relying on message
// text, and raw driver text still maps as a fallback.
func TestIsConnectorOwnershipConflictPrefersGormSignal(t *testing.T) {
	if !isConnectorOwnershipConflict(gorm.ErrDuplicatedKey) {
		t.Fatal("gorm.ErrDuplicatedKey should map to the ownership conflict")
	}
	if !isConnectorOwnershipConflict(errors.New(`pq: duplicate key value violates unique constraint "uk_connector_ownership"`)) {
		t.Fatal("postgres 23505-style text should still map")
	}
	if !isConnectorOwnershipConflict(errors.New("Error 1062: Duplicate entry 'x' for key 'uk'")) {
		t.Fatal("mysql duplicate text should still map")
	}
	if isConnectorOwnershipConflict(errors.New("dial tcp: i/o timeout")) {
		t.Fatal("unrelated transport error must not map")
	}
	if isConnectorOwnershipConflict(nil) {
		t.Fatal("nil error must not map")
	}
}

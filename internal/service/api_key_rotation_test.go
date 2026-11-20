package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// keyRotationFixture is one tenant with one key, built through the public service API so
// the test exercises the same path the handler does.
type keyRotationFixture struct {
	svc      *Service
	ctx      context.Context
	tenantID string
	secret   string
	key      *model.APIKey
}

func newKeyRotationFixture(t *testing.T) *keyRotationFixture {
	t.Helper()
	fx := newReportFixture(t, "key-rotation")
	ctx := context.Background()
	scopes := &APIKeyScopes{Models: []string{"life-model"}}
	secret, key, err := fx.svc.CreateAPIKey(ctx, fx.tenantID, fx.owner.ID, "rotate-me",
		nil, 1_000, 10, scopes, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return &keyRotationFixture{svc: fx.svc, ctx: ctx, tenantID: fx.tenantID, secret: secret, key: key}
}

// mustHTTPError asserts the shape a handler needs: a status and a code, not just a string.
func mustHTTPError(t *testing.T, err error, status, code int) {
	t.Helper()
	converted, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("expected an *httperr.Error, got %T: %v", err, err)
	}
	if converted.Status != status || converted.Code != code {
		t.Fatalf("expected %d/%d, got %d/%d (%s)", status, code, converted.Status, converted.Code, converted.Message)
	}
}

// TestRotateAPIKeyDefaultsClampsAndRefuses covers the argument edges. Each of these is a
// way a single number can silently mean something else: an omitted grace, a negative one,
// and one so large the "retired" key would outlive the person who rotated it.
func TestRotateAPIKeyDefaultsClampsAndRefuses(t *testing.T) {
	f := newKeyRotationFixture(t)

	zero := 0
	_, _, retired, err := f.svc.RotateAPIKey(f.ctx, f.tenantID, f.key.ID, &zero)
	if err != nil {
		t.Fatalf("rotate with grace 0: %v", err)
	}
	if retired.ExpiredAt == nil || time.Until(retired.ExpiredAt.UTC()) > time.Minute {
		t.Fatalf("grace 0 must retire immediately, got %v", retired.ExpiredAt)
	}

	second := newKeyRotationFixture(t)
	negative := -1
	_, _, _, err = second.svc.RotateAPIKey(second.ctx, second.tenantID, second.key.ID, &negative)
	mustHTTPError(t, err, 400, 40041)

	third := newKeyRotationFixture(t)
	huge := 100_000
	_, _, retired, err = third.svc.RotateAPIKey(third.ctx, third.tenantID, third.key.ID, &huge)
	if err != nil {
		t.Fatalf("rotate with a huge grace: %v", err)
	}
	window := time.Until(retired.ExpiredAt.UTC())
	if window > time.Duration(apiKeyRotationMaxGraceHours)*time.Hour+time.Minute {
		t.Fatalf("grace must clamp to %dh, got %.1fh", apiKeyRotationMaxGraceHours, window.Hours())
	}

	fourth := newKeyRotationFixture(t)
	_, _, _, err = fourth.svc.RotateAPIKey(fourth.ctx, fourth.tenantID, "missing", nil)
	mustHTTPError(t, err, 404, 404)

	_, _, _, err = fourth.svc.RotateAPIKey(fourth.ctx, fourth.tenantID, "  ", nil)
	mustHTTPError(t, err, 400, 40040)
}

// TestRotateAPIKeyIsTenantScoped — a key id from another tenant must read as "not found",
// never as "rotated": a rotation is a credential mint, so a cross-tenant probe must not
// even learn that the id exists.
func TestRotateAPIKeyIsTenantScoped(t *testing.T) {
	f := newKeyRotationFixture(t)
	other := newReportFixture(t, "key-rotation-other")

	_, _, _, err := f.svc.RotateAPIKey(f.ctx, other.tenantID, f.key.ID, nil)
	mustHTTPError(t, err, 404, 404)

	// The key must be untouched: a refused rotation that still stamped the old row would
	// leave a key expiring at a window nobody opened.
	untouched, err := f.svc.Store.GetAPIKey(f.ctx, f.tenantID, f.key.ID)
	if err != nil || untouched == nil {
		t.Fatalf("reload key: %v", err)
	}
	if untouched.RotatedToKeyID != "" || untouched.ExpiredAt != nil {
		t.Fatalf("a refused rotation must not touch the key: %+v", untouched)
	}
}

// TestRotateAPIKeySuccessorAuthenticatesAndOldOneWaits — the end-to-end shape of a
// rotation at the service seam: the raw secret of the successor resolves to a different
// row, and the old secret still resolves because its window has not closed.
func TestRotateAPIKeySuccessorAuthenticatesAndOldOneWaits(t *testing.T) {
	f := newKeyRotationFixture(t)
	grace := 24
	raw, successor, retired, err := f.svc.RotateAPIKey(f.ctx, f.tenantID, f.key.ID, &grace)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if raw == f.secret {
		t.Fatal("the successor must have a new secret")
	}

	resolved, err := f.svc.ResolveAPIKey(f.ctx, raw)
	if err != nil || resolved == nil {
		t.Fatalf("the successor must authenticate: %v", err)
	}
	if resolved.ID != successor.ID {
		t.Fatalf("the raw secret resolved to %s, want the successor %s", resolved.ID, successor.ID)
	}
	if resolved.ScopesJSON != f.key.ScopesJSON || resolved.TokenQuota != f.key.TokenQuota {
		t.Fatalf("the successor must inherit constraints: %+v", resolved)
	}

	old, err := f.svc.ResolveAPIKey(f.ctx, f.secret)
	if err != nil || old == nil {
		t.Fatalf("the retired key must still authenticate inside its window: %v", err)
	}
	if old.RotatedToKeyID != successor.ID {
		t.Fatalf("the retired row must link to its successor, got %q", old.RotatedToKeyID)
	}
	if retired.ID != old.ID {
		t.Fatalf("the returned retired row is %s, want %s", retired.ID, old.ID)
	}

	// Revoking the retired key inside its window must end the window. Two authorities on
	// when a key stops working would eventually disagree, so `enabled=false` wins.
	if err := f.svc.RevokeAPIKey(f.ctx, f.tenantID, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResolveAPIKey(f.ctx, f.secret); err == nil {
		t.Fatal("a revoked key must not authenticate even inside its rotation window")
	}
}

// TestRetireAPIKeyForRotationIsTheCAS pins the repository contract the service relies on:
// zero rows affected means "somebody else already retired it", which is a race to lose,
// not an error to report.
func TestRetireAPIKeyForRotationIsTheCAS(t *testing.T) {
	f := newKeyRotationFixture(t)
	when := time.Now().UTC().Add(time.Hour)

	ok, err := f.svc.Store.RetireAPIKeyForRotation(f.ctx, f.tenantID, "no-such-key", "successor", when)
	if err != nil || ok {
		t.Fatalf("a missing row must report (false, nil), got (%v, %v)", ok, err)
	}
	ok, err = f.svc.Store.RetireAPIKeyForRotation(f.ctx, f.tenantID, f.key.ID, "successor-a", when)
	if err != nil || !ok {
		t.Fatalf("first retire must win: (%v, %v)", ok, err)
	}
	ok, err = f.svc.Store.RetireAPIKeyForRotation(f.ctx, f.tenantID, f.key.ID, "successor-b", when.Add(time.Hour))
	if err != nil || ok {
		t.Fatalf("second retire must lose: (%v, %v)", ok, err)
	}
	row, err := f.svc.Store.GetAPIKey(f.ctx, f.tenantID, f.key.ID)
	if err != nil || row == nil {
		t.Fatalf("reload: %v", err)
	}
	if row.RotatedToKeyID != "successor-a" {
		t.Fatalf("the loser must not overwrite the winner, got %q", row.RotatedToKeyID)
	}
}

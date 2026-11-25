package service

import (
	"testing"
	"time"
)

// TestAPIKeyExpiryIsInclusiveAtTheInstant pins the boundary that made
// s07.go-test-all flake: a rotation with grace 0 stores `expired_at = time.Now()`,
// and the wall clock is quantised, so the next request can read *the same* instant.
// With `now.After(expired_at)` that request was still authorised, once in ~25 runs.
// Equal must be dead (doc/304 §4 E144).
func TestAPIKeyExpiryIsInclusiveAtTheInstant(t *testing.T) {
	now := time.Date(2026, 11, 25, 3, 4, 5, 123456789, time.UTC)
	earlier := now.Add(-time.Nanosecond)
	later := now.Add(time.Nanosecond)

	cases := []struct {
		name      string
		expiredAt *time.Time
		now       time.Time
		want      bool
	}{
		{"no expiry at all is not an expiry", nil, now, false},
		{"a key expiring later is alive", &later, now, false},
		{"the instant of expiry is already dead", &now, now, true},
		{"a past expiry is dead", &earlier, now, true},
	}
	for _, c := range cases {
		if got := apiKeyExpiredAt(c.expiredAt, c.now); got != c.want {
			t.Fatalf("%s: apiKeyExpiredAt = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestAPastExpiryWalkedBackwardsIsStillAnExpiry keeps the inclusive comparison from
// being "fixed" by making the whole check vacuous: the same instant must straddle the
// answer, not collapse it.
func TestAPastExpiryWalkedBackwardsIsStillAnExpiry(t *testing.T) {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	if !apiKeyExpiredAt(&yesterday, now) {
		t.Fatal("a key that expired yesterday must be rejected")
	}
	if apiKeyExpiredAt(&tomorrow, now) {
		t.Fatal("a key that expires tomorrow must be accepted")
	}
}

package jwt

import (
	"strings"
	"testing"
)

func TestValidateSecret(t *testing.T) {
	cases := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{"strong random hex", "3f9a1c7e5b2d8f4a6c0e9d1b7a3f5c8e2d4b6a8c0e1f3a5d7b9c1e3f5a7d9b2c", false},
		{"exactly min length", strings.Repeat("x", MinSecretLen), false},
		{"padded short value", "  short  ", true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"too short", "short-secret", true},
		{"placeholder change-me-in-prod", "change-me-in-prod", true},
		{"placeholder case-insensitive", "Change-Me-In-Prod", true},
	}

	for _, tc := range cases {
		err := ValidateSecret(tc.secret)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
	}
}

func TestNewManagerStillSignsWithTestFixtures(t *testing.T) {
	// NewManager stays permissive (unit tests build managers directly);
	// the strict baseline is enforced by ValidateSecret at engine-build time.
	m := NewManager("test-secret", 1)
	token, err := m.Issue("u1", "t1", "alice", "platform_admin")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := m.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "u1" {
		t.Fatalf("unexpected subject: %+v", claims)
	}
}

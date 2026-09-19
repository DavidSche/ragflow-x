package rsaseal

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	kp, err := Ensure(filepath.Join(t.TempDir(), "rsa.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if kp.PublicPEM() == "" {
		t.Fatal("expected public key PEM")
	}
	sealed, err := kp.Encrypt("s3cr3t-value")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := kp.Decrypt(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "s3cr3t-value" {
		t.Fatalf("round trip mismatch: %q", plain)
	}
}

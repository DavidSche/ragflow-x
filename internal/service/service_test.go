package service

import "testing"

func TestEffectiveEncryptSecret(t *testing.T) {
	if effectiveEncryptSecret("") == "" {
		t.Fatal("empty secret must produce a generated key, not empty")
	}
	if effectiveEncryptSecret(defaultAllZeroKey) == defaultAllZeroKey ||
		effectiveEncryptSecret(defaultAllZeroKey) == "" {
		t.Fatal("all-zero placeholder must not be used as the encryption key")
	}
	if effectiveEncryptSecret("real-key") != "real-key" {
		t.Fatal("explicit non-default secret must be preserved")
	}
	a := effectiveEncryptSecret("")
	b := effectiveEncryptSecret("")
	if a == b {
		t.Fatal("ephemeral keys must differ between instantiations")
	}
}

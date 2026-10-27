package secretstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	masterKey, err := LoadMasterKey("", filepath.Join(dir, ".master.key"))
	if err != nil {
		t.Fatal(err)
	}

	sec := &Secrets{
		Database: Database{Driver: "postgres", Host: "10.0.0.5", User: "u", Password: "p@ss", Name: "db"},
		RAGFlow:  RAGFlow{Provider: "http", BaseURL: "http://engine:8001", APIKey: "secret-key"},
		Admin:    &Admin{Username: "admin", Password: "hunter2"},
	}
	if err := Save(path, masterKey, sec); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.Database.Password != "p@ss" || got.RAGFlow.APIKey != "secret-key" || got.Admin.Password != "hunter2" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// The on-disk file must not contain plaintext secrets.
	if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), "hunter2") {
		t.Fatal("plaintext secret present in secrets file")
	}
}

func TestLoadMissingReturnsNil(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

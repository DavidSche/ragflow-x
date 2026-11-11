package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveConfigPathFindsConventionalFile pins the release-archive UX: the
// binary must read config/config.yaml next to it, because that is what the README
// tells operators to create after unpacking.
func TestResolveConfigPathFindsConventionalFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if got := resolveConfigPath(""); got != "" {
		t.Fatalf("no config present: expected empty path, got %q", got)
	}

	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config", "config.yaml"))
	if got := resolveConfigPath(""); got != "config/config.yaml" {
		t.Fatalf("expected config/config.yaml, got %q", got)
	}

	// config/config.yaml wins over a bare config.yaml.
	write(t, filepath.Join(dir, "config.yaml"))
	if got := resolveConfigPath(""); got != "config/config.yaml" {
		t.Fatalf("config/config.yaml must take priority, got %q", got)
	}

	// Explicit -config always wins, even for a path that does not exist.
	if got := resolveConfigPath("/nowhere/x.yaml"); got != "/nowhere/x.yaml" {
		t.Fatalf("explicit path must win, got %q", got)
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("server:\n  port: 9191\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

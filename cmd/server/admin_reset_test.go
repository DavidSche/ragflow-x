package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
	"github.com/ragflow-x/ragflow-x/internal/pkg/secretstore"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// setupOnDiskFixture writes a config + encrypted secrets + a real sqlite
// database with one account, wired the way a deployed server has them: the
// connection lives in the secrets file, not in the YAML.
func setupOnDiskFixture(t *testing.T, username, plain string) (configPath, dbPath string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "app.db")
	secretsPath := filepath.Join(dir, "runtime.secrets.json")

	key, err := secretstore.LoadMasterKey("", filepath.Join(dir, ".master.key"))
	if err != nil {
		t.Fatal(err)
	}
	sec := &secretstore.Secrets{
		Database: secretstore.Database{Driver: "sqlite", DSN: dbPath},
		RAGFlow:  secretstore.RAGFlow{Provider: "mock", BaseURL: "http://engine", APIKey: "k"},
		Admin:    &secretstore.Admin{Username: username},
	}
	if err := secretstore.Save(secretsPath, key, sec); err != nil {
		t.Fatal(err)
	}

	gdb, err := db.Open(secretDB(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeGorm(gdb) })
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	hash, err := password.Hash(plain)
	if err != nil {
		t.Fatal(err)
	}
	// Migration already seeds the system tenant; only create it if absent.
	const systemTenant = "00000000000000000000000000000000"
	if existing, err := store.GetTenant(context.Background(), systemTenant); err != nil {
		t.Fatal(err)
	} else if existing == nil {
		if err := store.CreateTenant(context.Background(), &model.Tenant{ID: systemTenant, Name: "Platform", Status: model.TenantStatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	u := &model.User{ID: "u-reset", TenantID: systemTenant, Username: username, PasswordHash: hash, Role: model.RolePlatformAdmin, Status: model.UserStatusActive}
	if err := store.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}

	configPath = filepath.Join(dir, "config.yaml")
	yaml := fmt.Sprintf("setup:\n  secrets_file: %q\n", filepath.ToSlash(secretsPath))
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, dbPath
}

func closeGorm(gdb *gorm.DB) {
	if raw, err := gdb.DB(); err == nil {
		_ = raw.Close()
	}
}

func secretDB(dsn string) config.Database {
	return config.Database{Driver: "sqlite", DSN: dsn}
}

// TestResetAdminPasswordSetsANewPassword covers the operator who is locked out of
// their own first-run deployment: the command must find the same database the
// server uses (stored in the encrypted secrets file) and write a password that
// actually authenticates.
func TestResetAdminPasswordSetsANewPassword(t *testing.T) {
	configPath, dbPath := setupOnDiskFixture(t, "root", "forgotten-password")

	var out, errOut bytes.Buffer
	code := runResetAdmin([]string{"-config", configPath, "-username", "root", "-password", "recovered-password"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"root"`) {
		t.Fatalf("output must name the account: %s", out.String())
	}

	gdb, err := db.Open(secretDB(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeGorm(gdb) })
	u, err := repository.NewStore(gdb).GetUserByUsername(context.Background(), "root")
	if err != nil || u == nil {
		t.Fatalf("lookup after reset: %v", err)
	}
	if !password.Verify(u.PasswordHash, "recovered-password") {
		t.Fatal("the new password must verify")
	}
	if password.Verify(u.PasswordHash, "forgotten-password") {
		t.Fatal("the old password must no longer verify")
	}
}

// TestResetAdminPasswordListsAccounts keeps the command useful as a diagnostic:
// "login fails" is often "the account is not the one you think it is".
func TestResetAdminPasswordListsAccounts(t *testing.T) {
	configPath, _ := setupOnDiskFixture(t, "bp-admin", "whatever-password")

	var out, errOut bytes.Buffer
	if code := runResetAdmin([]string{"-config", configPath, "-list"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "bp-admin") || !strings.Contains(out.String(), string(model.RolePlatformAdmin)) {
		t.Fatalf("list output must carry the account and its role: %s", out.String())
	}

	// An unknown username must fail loudly and show what does exist.
	out.Reset()
	errOut.Reset()
	if code := runResetAdmin([]string{"-config", configPath, "-username", "typo-admin"}, &out, &errOut); code == 0 {
		t.Fatal("an unknown account must not report success")
	}
	if !strings.Contains(errOut.String(), "bp-admin") {
		t.Fatalf("stderr must list the existing accounts: %s", errOut.String())
	}
}

// TestResetAdminPasswordRefusesShortPasswords mirrors the wizard's own rule; a
// recovery path that silently accepts "1234" would be worse than none.
func TestResetAdminPasswordRefusesShortPasswords(t *testing.T) {
	configPath, _ := setupOnDiskFixture(t, "root", "real-password-1")

	var out, errOut bytes.Buffer
	if code := runResetAdmin([]string{"-config", configPath, "-username", "root", "-password", "short"}, &out, &errOut); code == 0 {
		t.Fatal("a short password must be rejected")
	}
	if !strings.Contains(errOut.String(), "at least 8") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

// TestResolveDatabasePrefersSecrets pins the precedence the server uses: the
// wizard writes the connection into the secrets file, so a YAML that still holds
// the built-in defaults must not win.
func TestResolveDatabasePrefersSecrets(t *testing.T) {
	base := config.Database{Driver: "postgres", Host: "localhost", Port: 5432, User: "ragflow_x", Password: "change-me", Name: "ragflow_x"}
	got := resolveDatabase(base, &secretstore.Secrets{Database: secretstore.Database{
		Driver: "sqlite", DSN: "/data/app.db",
	}})
	if got.Driver != "sqlite" || got.DSN != "/data/app.db" {
		t.Fatalf("secrets must win: %+v", got)
	}
	// An empty stored password must never wipe the configured one.
	if got.Password != base.Password {
		t.Fatalf("password must fall back to the YAML value, got %q", got.Password)
	}
	// No secrets file at all: the YAML stands alone.
	if same := resolveDatabase(base, nil); same != base {
		t.Fatalf("no secrets must mean unchanged config: %+v", same)
	}
}

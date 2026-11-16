package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
	"github.com/ragflow-x/ragflow-x/internal/pkg/secretstore"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// The first-run wizard creates exactly one account. If that password is lost —
// mistyped, or overwritten by a wizard re-run that could not reach an existing
// user — a self-hosted deployment has no way back in short of dropping the
// database. `ragflow-x reset-admin-password` is that way back: it uses the same
// config + secrets the server uses, so it can never touch a different database
// than the one the operator is failing to sign in to.
//
// It only resets an account that already exists (never creates one) and prints
// the new password exactly once.

const resetAdminUsage = `usage: ragflow-x reset-admin-password [-config <path>] [-username <name>] [-password <new>] [-list]

  -config    same -config the server uses (default: config/config.yaml if present)
  -username  account to reset (default: setup.admin_user from config, else "admin")
  -password  new password; when empty a random one is generated and printed
  -list      only print the existing usernames, change nothing
`

// fprintf / fprintln write to the operator's terminal. A failed write (closed
// pipe, full disk) cannot be recovered from here and must not change the exit
// code, so the error is discarded deliberately — hence helpers instead of a
// `_, _ =` marker repeated at every call site.
func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func fprintln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

// runResetAdmin is the subcommand entry point. It returns a process exit code.
func runResetAdmin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-admin-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to YAML config file")
	username := fs.String("username", "", "account to reset")
	newPassword := fs.String("password", "", "new password (generated when empty)")
	listOnly := fs.Bool("list", false, "list existing usernames only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fprintf(stdout, "%s", resetAdminUsage)
			return 0
		}
		return 2
	}

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		fprintf(stderr, "load config failed: %v\n", err)
		return 1
	}
	// The database may have been configured by the wizard, in which case the
	// connection lives in the encrypted secrets file rather than the YAML.
	secretsPath := cfg.Setup.SecretsFile
	masterKey, err := secretstore.LoadMasterKey(cfg.Setup.MasterKey, masterKeyFileFor(secretsPath))
	if err != nil {
		fprintf(stderr, "load master key failed: %v\n", err)
		return 1
	}
	secrets, err := secretstore.Load(secretsPath, masterKey)
	if err != nil {
		fprintf(stderr, "load secrets (%s) failed: %v\n", secretsPath, err)
		return 1
	}
	dbc := resolveDatabase(cfg.Database, secrets)

	name := strings.TrimSpace(*username)
	if name == "" && secrets != nil && secrets.Admin != nil {
		name = strings.TrimSpace(secrets.Admin.Username)
	}
	if name == "" {
		name = strings.TrimSpace(cfg.Setup.AdminUser)
	}
	if name == "" {
		name = "admin"
	}

	ctx := context.Background()
	// Quiet open: this command prints its own diagnostics, and a GORM "record not
	// found" block in the middle of an operator's terminal reads like a failure.
	gdb, err := db.OpenQuiet(dbc)
	if err != nil {
		fprintf(stderr, "open database failed: %v\n", err)
		fprintf(stderr, "hint: the wizard stores its connection in %s; make sure the server runs with the same -config\n", secretsPath)
		return 1
	}
	defer func() {
		if raw, derr := gdb.DB(); derr == nil {
			_ = raw.Close()
		}
	}()
	store := repository.NewStore(gdb)

	if *listOnly {
		users, _, err := store.ListUsers(ctx, 1, 50, repository.UserFilter{})
		if err != nil {
			fprintf(stderr, "list users failed: %v\n", err)
			return 1
		}
		for _, u := range users {
			fprintf(stdout, "%s\t%s\t%s\n", u.Username, u.Role, u.Status)
		}
		if len(users) == 0 {
			fprintln(stdout, "(no users: the first-run wizard has not been completed)")
		}
		return 0
	}

	u, err := store.GetUserByUsername(ctx, name)
	if err != nil {
		fprintf(stderr, "look up %q failed: %v\n", name, err)
		return 1
	}
	if u == nil {
		fprintf(stderr, "no such account: %s\n", name)
		fprintf(stderr, "existing accounts (rerun with -username <name>):\n")
		if users, _, lerr := store.ListUsers(ctx, 1, 20, repository.UserFilter{}); lerr == nil {
			for _, x := range users {
				fprintf(stderr, "  %s (%s)\n", x.Username, x.Role)
			}
			if len(users) == 0 {
				fprintln(stderr, "  (none: complete the first-run wizard at the web UI)")
			}
		}
		return 1
	}

	plain := *newPassword
	generated := false
	if plain == "" {
		plain, err = generatePassword(16)
		if err != nil {
			fprintf(stderr, "generate password failed: %v\n", err)
			return 1
		}
		generated = true
	}
	if len(plain) < 8 {
		fprintln(stderr, "password must be at least 8 characters")
		return 2
	}
	hash, err := password.Hash(plain)
	if err != nil {
		fprintf(stderr, "hash password failed: %v\n", err)
		return 1
	}
	u.PasswordHash = hash
	if err := store.UpdateUser(ctx, u); err != nil {
		fprintf(stderr, "update password failed: %v\n", err)
		return 1
	}

	fprintf(stdout, "password updated for %q (tenant %s, role %s)\n", u.Username, u.TenantID, u.Role)
	if generated {
		fprintf(stdout, "new password: %s\n", plain)
		fprintln(stdout, "store it now: it is not written anywhere and will not be shown again")
	}
	fprintln(stdout, "sign in at the web UI and change it under your profile once you are in")
	return 0
}

// resolveDatabase merges the YAML/env database settings with the secrets the
// wizard persisted. Secrets win for the secret fields (password), non-empty
// secret values win over defaults for the rest — the same precedence the server
// applies when it starts.
func resolveDatabase(base config.Database, sec *secretstore.Secrets) config.Database {
	if sec == nil {
		return base
	}
	s := sec.Database
	pick := func(secretValue, fallback string) string {
		if strings.TrimSpace(secretValue) != "" {
			return secretValue
		}
		return fallback
	}
	out := base
	out.Driver = pick(s.Driver, base.Driver)
	out.Host = pick(s.Host, base.Host)
	if s.Port != 0 {
		out.Port = s.Port
	}
	out.User = pick(s.User, base.User)
	out.Name = pick(s.Name, base.Name)
	out.SSLMode = pick(s.SSLMode, base.SSLMode)
	out.DSN = pick(s.DSN, base.DSN)
	// The password is only ever taken from secrets when present: an empty stored
	// password means "use the YAML value", never "wipe it".
	if s.Password != "" {
		out.Password = s.Password
	}
	return out
}

// masterKeyFileFor mirrors setup.defaultMasterKeyFile: the key sits next to the
// secrets file, so a reset run from any working directory finds the same key.
func masterKeyFileFor(secretsPath string) string {
	dir := "./config/"
	if i := strings.LastIndexAny(secretsPath, `/\`); i >= 0 {
		dir = secretsPath[:i+1]
	}
	return dir + ".master.key"
}

// generatePassword returns a random alphanumeric password of the given length,
// using crypto/rand. Ambiguous glyphs (0/O, 1/l/I) are skipped so an operator
// can retype what the terminal printed.
func generatePassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 0, n)
	for len(out) < n {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out = append(out, alphabet[idx.Int64()])
	}
	return string(out), nil
}

// maybeRunSubcommand dispatches the CLI subcommands before the server boots. It
// reports whether it handled the invocation.
func maybeRunSubcommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "reset-admin-password":
		os.Exit(runResetAdmin(args[2:], os.Stdout, os.Stderr))
		return true
	case "help", "-h", "--help":
		fprintf(os.Stdout, "%s", "usage: ragflow-x [-config <path>]\n       ragflow-x reset-admin-password [-config <path>] [-username <name>] [-password <new>] [-list]\n")
		os.Exit(0)
		return true
	}
	return false
}

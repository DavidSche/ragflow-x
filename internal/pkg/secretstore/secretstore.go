// Package secretstore persists the database and engine connection secrets that
// the first-run wizard collects. The whole payload is sealed with AES-GCM using
// a master key and written with restrictive permissions, so credentials never
// appear in YAML, deployment config, or repository history.
package secretstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
)

// Database holds the persisted operational-store connection settings.
type Database struct {
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Name     string `json:"name"`
	SSLMode  string `json:"sslmode"`
	DSN      string `json:"dsn"`
}

// RAGFlow holds the persisted engine connection settings.
type RAGFlow struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Timeout  int    `json:"timeout"`
	MaxConns int    `json:"max_conns"`
}

// Admin holds the optional first-run admin credentials.
type Admin struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Redis holds optional distributed rate-limit backend settings. Disabled uses
// the process-local memory limiter.
type Redis struct {
	Enabled  bool   `json:"enabled"`
	Addr     string `json:"addr"`
	Username string `json:"username"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	PoolSize int    `json:"pool_size"`
}

// Secrets is the decrypted in-memory representation of the runtime secrets.
type Secrets struct {
	Database  Database `json:"database"`
	RAGFlow   RAGFlow  `json:"ragflow"`
	Admin     *Admin   `json:"admin,omitempty"`
	CryptoKey string   `json:"crypto_key,omitempty"`
	JWTSecret string   `json:"jwt_secret,omitempty"`
	Redis     Redis    `json:"redis"`
}

// Load reads and decrypts the secrets file. A missing file yields (nil, nil)
// so callers can fall back to the first-run bootstrap path.
func Load(path string, masterKey []byte) (*Secrets, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read secrets file: %w", err)
	}
	plain, err := crypto.Decrypt(masterKey, string(raw))
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets file (wrong master key?): %w", err)
	}
	var sec Secrets
	if err := json.Unmarshal([]byte(plain), &sec); err != nil {
		return nil, fmt.Errorf("parse secrets file: %w", err)
	}
	return &sec, nil
}

// Save encrypts and writes the secrets file atomically with 0600 permissions.
func Save(path string, masterKey []byte, sec *Secrets) error {
	plain, err := json.Marshal(sec)
	if err != nil {
		return err
	}
	sealed, err := crypto.Encrypt(masterKey, string(plain))
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sealed), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadMasterKey returns the 32-byte AES key used to seal the secrets file. It
// prefers an operator-supplied value (RGX_MASTER_KEY); otherwise it generates
// and persists a random key next to the runtime config.
func LoadMasterKey(explicit, keyFile string) ([]byte, error) {
	if explicit != "" {
		return derive(explicit), nil
	}
	if raw, err := os.ReadFile(keyFile); err == nil {
		key, err := hex.DecodeString(string(raw))
		if err != nil || len(key) != 32 {
			return nil, errors.New("master key file is malformed")
		}
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func derive(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

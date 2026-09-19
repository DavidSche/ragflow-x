// Package rsaseal provides an RSA-OAEP keypair used to encrypt sensitive
// fields (database password, API keys, admin password) in transit between the
// browser setup wizard and the server. The browser encrypts with the server's
// public key; only the server's private key can decrypt. It is defense in
// depth on top of transport TLS, preventing secrets from being visible to
// proxies or log lines.
package rsaseal

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// KeyPair wraps a loaded or generated RSA keypair.
type KeyPair struct {
	priv *rsa.PrivateKey
}

// Ensure loads the keypair from path, generating and persisting one when
// missing. The private key is written with 0600 permissions.
func Ensure(path string) (*KeyPair, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(raw); block != nil {
			priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse rsa private key: %w", err)
			}
			k, ok := priv.(*rsa.PrivateKey)
			if !ok {
				return nil, errors.New("not an RSA private key")
			}
			return &KeyPair{priv: k}, nil
		}
		return nil, errors.New("invalid rsa key file")
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return &KeyPair{priv: priv}, nil
}

// PublicPEM returns PEM-encoded PKIX public key for client-side encryption.
func (k *KeyPair) PublicPEM() string {
	der, err := x509.MarshalPKIXPublicKey(&k.priv.PublicKey)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// Encrypt seals plaintext with RSA-OAEP (SHA-256) for tests and symmetric use.
func (k *KeyPair) Encrypt(plain string) (string, error) {
	sealed, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &k.priv.PublicKey, []byte(plain), nil)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a base64 RSA-OAEP ciphertext produced with the public key.
func (k *KeyPair) Decrypt(b64 string) (string, error) {
	sealed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, k.priv, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Package crypto provides application-level encryption helpers (AES-GCM).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// ErrInvalidKey is returned when the encryption key is unusable.
var ErrInvalidKey = errors.New("invalid encryption key")

// Envelope encodes a ciphertext nonce as the first 12 bytes.
type Envelope struct {
	Nonce []byte
	Data  []byte
}

// Encrypt seals plaintext with AES-GCM using the 32-byte key.
func Encrypt(key []byte, plaintext string) (string, error) {
	if len(key) != 32 {
		return "", ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(plaintext), nil)
	env := &Envelope{Nonce: nonce, Data: sealed}
	raw, err := marshal(env)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Decrypt opens a sealed value produced by Encrypt.
func Decrypt(key []byte, sealed string) (string, error) {
	if len(key) != 32 {
		return "", ErrInvalidKey
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	env, err := unmarshal(raw)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(env.Nonce) != aead.NonceSize() {
		return "", errors.New("invalid nonce")
	}
	plain, err := aead.Open(nil, env.Nonce, env.Data, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func marshal(env *Envelope) ([]byte, error) {
	out := make([]byte, 0, len(env.Nonce)+len(env.Data))
	out = append(out, env.Nonce...)
	out = append(out, env.Data...)
	return out, nil
}

func unmarshal(raw []byte) (*Envelope, error) {
	if len(raw) < 12 {
		return nil, errors.New("ciphertext too short")
	}
	return &Envelope{Nonce: raw[:12], Data: raw[12:]}, nil
}

// Package password provides bcrypt password hashing helpers.
package password

import "golang.org/x/crypto/bcrypt"

// Hash returns a bcrypt hash of the given plaintext password.
func Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// Verify reports whether plain matches the stored hash.
func Verify(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

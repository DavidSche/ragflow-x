// Package id provides ID generation helpers.
package id

import (
	"strings"

	"github.com/google/uuid"
)

// New returns a compact (dashless) UUID string suitable for primary keys.
func New() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

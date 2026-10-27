package service

import (
	"strings"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

const maxDisplayNameRunes = 128

func normalizeDisplayName(value, emptyMessage string, code int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", httperr.BadRequest(code, emptyMessage)
	}
	if utf8.RuneCountInString(value) > maxDisplayNameRunes {
		return "", httperr.BadRequest(code, "name must be at most 128 characters")
	}
	return value, nil
}

func displayNameConflict(resource string) error {
	return httperr.New(409, 40911, resource+" name already exists")
}

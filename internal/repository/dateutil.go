package repository

import "time"

// parseFilterDate attempts to parse a date string as either RFC3339 or
// date-only (YYYY-MM-DD). Returns the parsed time and true on success,
// or zero time and false if the string is empty or unparseable.
func parseFilterDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	// Try full RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	// Try date-only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

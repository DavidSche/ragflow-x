package main

import (
	"flag"
	"os"
)

// defaultConfigCandidates are the conventional config locations next to the
// binary, in priority order. The release archive ships config/config.example.yaml
// and the README tells operators to copy it to config/config.yaml, so the server
// must actually pick that file up when -config is not given: an ignored config
// file silently drops the process onto the built-in defaults (PostgreSQL on
// localhost), which reads as "the download is broken" instead of "DB not set up".
var defaultConfigCandidates = []string{"config/config.yaml", "config.yaml"}

// flagConfigPath returns the -config path from the command line, falling back to
// the first conventional config file that exists in the working directory.
func flagConfigPath() string {
	path := flag.String("config", "", "path to YAML config file (default: config/config.yaml if present)")
	flag.Parse()
	return resolveConfigPath(*path)
}

// resolveConfigPath applies the -config default discovery. An explicit path wins
// even when it does not exist, so a typo stays a loud startup error instead of
// silently falling back to defaults.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, candidate := range defaultConfigCandidates {
		st, err := os.Stat(candidate)
		if err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func bootstrapUser() string {
	if v := os.Getenv("RGX_ADMIN_USER"); v != "" {
		return v
	}
	return "admin"
}

func bootstrapPass() string {
	if v := os.Getenv("RGX_ADMIN_PASSWORD"); v != "" {
		return v
	}
	// No default password: returning empty disables auto-bootstrap so the
	// first admin is created through the setup wizard instead.
	return ""
}

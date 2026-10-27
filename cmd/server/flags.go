package main

import (
	"flag"
	"os"
)

// flagConfigPath returns the -config path from the command line.
func flagConfigPath() string {
	path := flag.String("config", "", "path to YAML config file")
	flag.Parse()
	return *path
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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/deployment"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "application YAML config path")
	baseURL := flag.String("base-url", "http://localhost:9191", "ragflow-x API base URL")
	timeout := flag.Duration("timeout", 30*time.Second, "overall drill timeout")
	outputBase := flag.String("output", "doc/deployment/drill", "base path for .json and .md reports (empty disables files)")
	auditTenant := flag.String("audit-tenant", "", "optional tenant ID for focused audit-chain validation")
	auditBackup := flag.String("audit-backup", "", "audit pg_dump backup path")
	auditManifest := flag.String("audit-manifest", "", "audit backup manifest path (defaults to --audit-backup.sha256)")
	latestVersion := flag.Int64("latest-version", 0, "expected schema version; 0 uses the frozen migration list")
	securityBaseline := flag.Bool("security-baseline", false, "run production security baseline probes")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	database, err := db.Open(cfg.Database)
	if err != nil {
		fatal(err)
	}
	defer func() {
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := deployment.Run(ctx, deployment.Options{
		Config:            *cfg,
		BaseURL:           *baseURL,
		Timeout:           *timeout,
		DB:                database,
		AuditTenantID:     *auditTenant,
		AuditBackupPath:   *auditBackup,
		AuditManifestPath: *auditManifest,
		LatestVersion:     *latestVersion,
		SecurityBaseline:  *securityBaseline,
		Provider:          newRAGFlowProvider(cfg.RAGFlow),
	})
	if err != nil {
		if report != nil && *outputBase != "" {
			if writeErr := writeReports(*outputBase, report); writeErr != nil {
				fmt.Fprintf(os.Stderr, "deploy-drill: save failed report: %v\n", writeErr)
			}
		}
		fatal(err)
	}
	if *outputBase != "" {
		if err := writeReports(*outputBase, report); err != nil {
			fatal(err)
		}
		fmt.Printf("deployment drill %s: %s.json, %s.md\n", report.Overall, *outputBase, *outputBase)
	} else {
		fmt.Printf("deployment drill %s\n", report.Overall)
	}
	if report.Overall != "passed" {
		os.Exit(1)
	}
}

func newRAGFlowProvider(cfg config.RAGFlow) ragflow.Client {
	switch cfg.Provider {
	case "http":
		return ragflow.NewHTTPClient(cfg.BaseURL, cfg.APIKey, time.Duration(cfg.Timeout)*time.Second, cfg.MaxConns)
	default:
		return ragflow.NewMock()
	}
}

func writeReports(outputBase string, report *deployment.DrillReport) error {
	if err := os.MkdirAll(filepath.Dir(outputBase), 0o750); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	jsonData, err := report.WriteJSON()
	if err != nil {
		return err
	}
	markdown, err := report.RenderMarkdown()
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputBase+".json", jsonData, 0o600); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return os.WriteFile(outputBase+".md", markdown, 0o600)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "deploy-drill:", err)
	os.Exit(1)
}

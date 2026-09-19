// Command ragflow-x-server is the RAGFlow-X API server (gateway + control plane).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/service"
	"github.com/ragflow-x/ragflow-x/internal/setup"
)

func main() {
	cfg, err := config.Load(flagConfigPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	setupLogger(cfg)
	defer logger.Close()

	var server *http.Server
	mgr, err := setup.New(cfg, func(h http.Handler) error {
		// In-place reconnect: after the setup wizard applies config, swap the
		// running server's handler to the fully initialized router.
		if server != nil {
			server.Handler = h
		}
		return nil
	})
	if err != nil {
		logger.Error("setup init failed", "err", err)
		os.Exit(1)
	}
	defer mgr.Close()

	engine, err := mgr.Startup()
	if err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}

	// Optional operator-driven admin bootstrap for the env/config path.
	if pass := bootstrapPass(); pass != "" {
		if err := mgr.BootstrapEnvAdmin(context.Background(), bootstrapUser(), pass); err != nil {
			logger.Error("bootstrap admin failed", "err", err)
			os.Exit(1)
		}
	}

	server = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      engine,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
	}

	go func() {
		logger.Info("ragflow-x server listening",
			"addr", server.Addr,
			"mode", cfg.Server.Mode,
			"configured", mgr.Configured(),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Task polling only runs once the full engine (with a service) is live:
	// it schedules document-sync jobs on the async worker (A2), which executes
	// them with retry/backoff; enqueue is a no-op while one is already active.
	workStop := make(chan struct{})
	defer close(workStop)
	go startTaskWorker(workStop, mgr.Service, cfg.RAGFlow.TaskPollSeconds)
	runningService := mgr.Service()
	if runningService == nil {
		logger.Info("setup wizard enabled", "mode", cfg.Server.Mode)
	} else {
		effectiveCfg, err := runningService.EffectiveSystemSettingsConfig(context.Background(), *cfg)
		if err != nil {
			logger.Error("resolve effective system settings", "err", err)
			os.Exit(1)
		}
		go startApprovalMaintenance(workStop, mgr.Service, effectiveCfg.Approval.ExpireScanIntervalSec)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	_ = obs.Get().Shutdown(ctx)
	_ = mgr.Close()
	logger.Info("server stopped")
}

// setupLogger configures the process-wide structured logger from the
// runtime config, translating MB-based size settings to bytes.
func setupLogger(cfg *config.Config) {
	var sizeMB int64
	if cfg.Logging.RotationSizeMB > 0 {
		sizeMB = cfg.Logging.RotationSizeMB * 1024 * 1024
	}
	logCfg := &logger.Config{
		Level:         cfg.Logging.Level,
		Format:        cfg.Logging.Format,
		OutputPath:    cfg.Logging.OutputPath,
		RotateDaily:   cfg.Logging.RotateDaily,
		RotationSize:  sizeMB,
		RotationCount: cfg.Logging.RotationCount,
		RetentionDays: cfg.Logging.RetentionDays,
		Service:       cfg.App.Name,
	}
	if err := logger.Init(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
}

func startTaskWorker(stop <-chan struct{}, getSvc func() *service.Service, seconds int) {
	interval := time.Duration(seconds) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if s := getSvc(); s != nil {
				if err := enqueueDocumentSyncs(context.Background(), s); err != nil {
					logger.Warn("enqueue document sync jobs failed", "err", err)
				}
			}
		}
	}
}

func startApprovalMaintenance(stop <-chan struct{}, getSvc func() *service.Service, seconds int) {
	interval := time.Duration(seconds) * time.Second
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if s := getSvc(); s != nil {
				if err := s.RunApprovalMaintenance(context.Background()); err != nil {
					logger.Warn("approval maintenance failed", "err", err)
				}
			}
		}
	}
}

// enqueueDocumentSyncs schedules one document-sync job per tenant, so each
// tenant's parse progress is synced and observable in its own job queue.
func enqueueDocumentSyncs(ctx context.Context, s *service.Service) error {
	tenants, err := s.Store.ListAllTenants(ctx)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if _, err := s.EnqueueDocumentSync(ctx, t.ID); err != nil {
			return err
		}
		if _, err := s.EnqueueResourceReconciliation(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}

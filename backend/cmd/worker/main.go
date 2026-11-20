package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"backend/internal/common/constants"
	"backend/internal/jobs"
	"backend/internal/jobs/handlers"
	"backend/internal/modules/report"
	reportports "backend/internal/modules/report/ports"
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
	"backend/internal/platform/outbox"
	"backend/internal/platform/storage"
	"backend/internal/platform/telemetry"

	"github.com/hibiken/asynq"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad(config.LoadOptions{})
	loggr, err := logger.New(cfg.Logging, cfg.App.Name+"-worker", cfg.App.Version)
	if err != nil {
		return fmt.Errorf("worker logger init: %w", err)
	}
	defer func() { _ = loggr.Sync() }()

	metrics := telemetry.NewMetrics()
	database, err := db.New(ctx, cfg.Database, loggr)
	if err != nil {
		return fmt.Errorf("worker postgres connect: %w", err)
	}
	defer database.Close()

	health := telemetry.NewHealth(
		cfg.App.Name+"-worker", cfg.App.Version,
		cfg.Telemetry.Health.ExposeErrors, cfg.Telemetry.Health.CacheTTL, loggr,
	)
	health.Register("postgres", func(ctx context.Context) error { return database.Ping(ctx) })

	var jobClient *jobs.Client
	var jobServer *jobs.Server
	var dispatcherDone chan struct{}
	defer func() {
		stop()
		if dispatcherDone != nil {
			<-dispatcherDone
		}
		if jobServer != nil {
			jobServer.Shutdown()
		}
		if jobClient != nil {
			_ = jobClient.Close()
		}
	}()

	if cfg.Outbox.Enabled {
		jobClient, err = jobs.NewClient(cfg.Redis, cfg.Asynq)
		if err != nil {
			return fmt.Errorf("worker job client init: %w", err)
		}
		health.Register("redis", func(context.Context) error { return jobClient.Ping() })

		jobServer, err = jobs.NewServer(cfg.Redis, cfg.Asynq)
		if err != nil {
			return fmt.Errorf("worker job server init: %w", err)
		}

		registry := outbox.NewRegistry()

		// Report generation task handler (gated behind cfg.Report.Enabled;
		// stays unregistered until release gates pass, REPORT_CONTRACT §22).
		if cfg.Report.Enabled {
			reportStore, serr := storage.New(cfg.Storage, loggr, metrics)
			if serr != nil {
				return fmt.Errorf("report storage init: %w", serr)
			}
			outboxStore, serr := outbox.NewStore(database, cfg.Outbox.MaxPayloadBytes)
			if serr != nil {
				return fmt.Errorf("report outbox store init: %w", serr)
			}
			reportRegistry := report.NewRegistry()
			reportRegistry.MustRegister(report.ReportDefinition{
				Code:              "sales",
				Version:           1,
				Title:             "Sales Register",
				Description:       "Finalized sale lines within a date range, grouped by branch.",
				RequiredPerms:     []string{"reports:export"},
				SourcePerms:       map[string][]string{"sales": {"sales:view"}},
				Formats:           []string{report.FormatCSV},
				MaxRows:           100000,
				MaxBytes:          50 * 1024 * 1024,
				MaxRuntimeSeconds: 300,
				RetentionDays:     cfg.Report.DefaultRetentionDays,
				ConsistencyMode:   report.ConsistencyRepeatableRead,
				DateRange:         true,
				BranchFilter:      true,
				RendererVersion:   "csv-v1",
			})
			reportRepo := report.NewRepository(database)
			salesReader := reportports.NewSalesStore(database)
			reportSvc := report.NewService(reportRepo, salesReader, reportStore, outboxStore,
				reportRegistry, cfg.Report, loggr, metrics)
			if rerr := registry.Register("report.requested",
				handlers.NewReportTaskHandler(reportSvc, loggr)); rerr != nil {
				return fmt.Errorf("report task handler registration: %w", rerr)
			}
		}
		taskHandler, err := outbox.NewTaskHandler(database, cfg.Outbox, registry, loggr, metrics)
		if err != nil {
			return fmt.Errorf("outbox task handler init: %w", err)
		}
		dispatcher, err := outbox.NewDispatcher(database, cfg.Outbox, cfg.Asynq, registry, jobClient, loggr, metrics)
		if err != nil {
			return fmt.Errorf("outbox dispatcher init: %w", err)
		}

		mux := asynq.NewServeMux()
		mux.Handle(constants.TaskOutboxPublish, taskHandler)
		if err := jobServer.Start(mux); err != nil {
			return fmt.Errorf("worker job server start: %w", err)
		}
		dispatcherDone = make(chan struct{})
		go func() {
			defer close(dispatcherDone)
			if err := dispatcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				loggr.Error("outbox dispatcher stopped", zap.Error(err))
			}
		}()
		loggr.Info("transactional outbox dispatcher and Asynq worker started",
			zap.Int("concurrency", cfg.Asynq.Concurrency),
			zap.Duration("poll_interval", cfg.Outbox.PollInterval),
		)
	} else {
		loggr.Info("transactional outbox dispatcher disabled")
	}

	mux := http.NewServeMux()
	mux.Handle("/livez", health.LivenessHandler())
	mux.Handle("/readyz", health.ReadinessHandler())
	if cfg.Telemetry.Metrics.Enabled {
		mux.Handle(cfg.Telemetry.Metrics.Path, metrics.Handler(cfg.Telemetry.Metrics.AuthToken))
	}
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	loggr.Info("Pharmaciano worker started", zap.String("metrics_address", httpServer.Addr))

	select {
	case <-ctx.Done():
		loggr.Info("Pharmaciano worker shutting down")
	case err := <-serverErrors:
		return fmt.Errorf("worker metrics server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown worker metrics server: %w", err)
	}
	return nil
}

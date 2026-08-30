package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	"backend/internal/middleware"
	"backend/internal/modules/branch"
	"backend/internal/modules/organization"
	"backend/internal/modules/warehouse"
	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
	"backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
	"backend/internal/router"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run wires everything and blocks until a shutdown signal (or a fatal server
// error) arrives. Returning an error rather than calling log.Fatal lets every
// deferred cleanup (flush spans, close pools, sync logs) actually run — os.Exit
// would skip them.
func run() error {
	// Cancel the root context on SIGINT/SIGTERM so in-flight startup work and the
	// serve loop can unwind cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Configuration (panics via os.Exit(2) on invalid config) ---------------
	cfg := config.MustLoad(config.LoadOptions{})

	// --- Logger -----------------------------------------------------------------
	log, err := logger.New(cfg.Logging, cfg.App.Name, cfg.App.Version)
	if err != nil {
		return fmt.Errorf("logger init: %w", err)
	}
	defer func() { _ = log.Sync() }() // best effort; stderr may reject sync on some OSes

	// Pin the process clock to the configured timezone so every timestamp
	// (audit rows, logs, tokens) is consistent regardless of host TZ.
	if cfg.App.Timezone != "" {
		if loc, lerr := time.LoadLocation(cfg.App.Timezone); lerr == nil {
			time.Local = loc
		} else {
			log.Warn("invalid app.timezone; using process default",
				zap.String("timezone", cfg.App.Timezone), zap.Error(lerr))
		}
	}

	// --- Tracing (always returns a usable Tracer; Shutdown is always safe) ------
	tracer, err := telemetry.InitTracing(ctx, cfg.Telemetry, cfg.App.Name, cfg.App.Version, cfg.App.Env, log)
	if err != nil {
		return fmt.Errorf("tracing init: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if serr := tracer.Shutdown(sctx); serr != nil {
			log.Warn("tracer shutdown", zap.Error(serr))
		}
	}()

	// --- Metrics ----------------------------------------------------------------
	metrics := telemetry.NewMetrics()

	// --- Postgres ---------------------------------------------------------------
	pg, err := db.New(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer pg.Close()
	// Expose live pool saturation as Prometheus gauges.
	metrics.RegisterDBPoolStats("primary", func() (int32, int32, int32, int32) {
		s := pg.Stats()
		return s.Acquired, s.Idle, s.Total, s.MaxConns
	})

	// --- Redis ------------------------------------------------------------------
	rdb, err := redis.New(ctx, cfg.Redis, log)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// --- Health registry --------------------------------------------------------
	// Liveness is dependency-free; readiness runs these checks (briefly cached).
	health := telemetry.NewHealth(
		cfg.App.Name, cfg.App.Version,
		cfg.Telemetry.Health.ExposeErrors, cfg.Telemetry.Health.CacheTTL, log,
	)
	health.Register("postgres", func(ctx context.Context) error { return pg.Ping(ctx) })
	health.Register("redis", func(ctx context.Context) error { return rdb.Ping(ctx) })

	// --- Middleware container ---------------------------------------------------
	// No domain deps yet → fail-closed stubs (New logs a warning). As modules land:
	//   mw := middleware.New(cfg, log, rdb,
	//       middleware.WithAuthenticator(authModule),
	//       middleware.WithAuthorizer(rbacModule),
	//       middleware.WithAuditSink(auditProducer),
	//   )
	mw := middleware.New(cfg, log, rdb)

	// --- Request validator ------------------------------------------------------
	// One shared, thread-safe validator (custom tags + json field names) for every
	// module handler. Built once here so a bad tag registration fails fast at
	// startup rather than on the first request.
	val, err := validator.New()
	if err != nil {
		return fmt.Errorf("validator init: %w", err)
	}

	// --- Domain modules ---------------------------------------------------------
	// Each module's New assembles its own repository → service → handler from the
	// shared Postgres pool, validator and logger, and exposes RegisterRoutes. The
	// router mounts them (in this order) under /api/v1 purely through the
	// ModuleRegistrar interface, so it never imports these packages.
	modules := []router.ModuleRegistrar{
		organization.New(pg, val, log),
		branch.New(pg, val, log),
		warehouse.New(pg, val, log),
	}

	// --- Router -----------------------------------------------------------------
	engine := router.New(router.Deps{
		Cfg:     cfg,
		Log:     log,
		Metrics: metrics,
		Health:  health,
		MW:      mw,
		Modules: modules,
	})

	// --- HTTP server ------------------------------------------------------------
	srv := &http.Server{
		Addr:           net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:        engine,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// Serve in the background so main can select between a listen failure and a
	// shutdown signal.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening",
			zap.String("addr", srv.Addr),
			zap.String("env", cfg.App.Env),
			zap.String("version", cfg.App.Version),
		)
		if lerr := srv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			serverErr <- lerr
		}
	}()

	select {
	case lerr := <-serverErr:
		return fmt.Errorf("http server: %w", lerr)
	case <-ctx.Done():
		log.Info("shutdown signal received; draining connections")
	}

	// --- Graceful shutdown ------------------------------------------------------
	shutdownTimeout := cfg.Server.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 20 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if serr := srv.Shutdown(shutdownCtx); serr != nil {
		log.Error("graceful shutdown timed out; forcing close", zap.Error(serr))
		_ = srv.Close()
		return serr
	}

	log.Info("server stopped cleanly")
	return nil
}

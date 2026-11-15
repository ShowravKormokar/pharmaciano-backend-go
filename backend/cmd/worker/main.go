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

	"backend/internal/platform/telemetry"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	metrics := telemetry.NewMetrics()
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler(os.Getenv("METRICS_AUTH_TOKEN")))
	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	log.Println("Pharmaciano worker started; metrics listening on :8080/metrics")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		log.Printf("Pharmaciano worker shutting down on %s", sig)
	case err := <-serverErrors:
		return fmt.Errorf("worker metrics server: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown worker metrics server: %w", err)
	}
	return nil
}

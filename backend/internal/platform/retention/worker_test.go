package retention

import (
	"context"
	"testing"
	"time"
)

func TestNewWorker_DefaultsBatchSize(t *testing.T) {
	w := New(nil, Config{Enabled: false}, nil)
	if w.cfg.BatchSize != 500 {
		t.Fatalf("expected default batch_size 500, got %d", w.cfg.BatchSize)
	}
}

func TestNewWorker_CustomBatchSize(t *testing.T) {
	w := New(nil, Config{Enabled: true, BatchSize: 100}, nil)
	if w.cfg.BatchSize != 100 {
		t.Fatalf("expected batch_size 100, got %d", w.cfg.BatchSize)
	}
}

func TestWorker_Run_DisabledExitsCleanly(t *testing.T) {
	w := New(nil, Config{Enabled: false}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestWorker_Sweep_NilPoolReturnsZero(t *testing.T) {
	w := New(nil, Config{Enabled: true}, nil)
	n, err := w.Sweep(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows deleted, got %d", n)
	}
}

func TestWorker_Sweep_ContextCancelled(t *testing.T) {
	w := New(nil, Config{Enabled: true}, nil)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := w.Sweep(cctx)
	if err != nil {
		t.Fatalf("expected no error from nil pool on cancelled ctx, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestDurationPG(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0 seconds"},
		{24 * time.Hour, "86400 seconds"},
		{30 * time.Minute, "1800 seconds"},
		{90 * time.Second, "90 seconds"},
	}
	for _, tt := range tests {
		got := durationPG(tt.d)
		if got != tt.want {
			t.Errorf("durationPG(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestWorker_Run_ZeroIntervalRunsOnce(t *testing.T) {
	w := New(nil, Config{Enabled: true, Interval: 0}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

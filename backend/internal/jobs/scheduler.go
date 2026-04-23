package jobs

import (
	"context"

	"github.com/go-co-op/gocron"
)

// Scheduler manages periodic background jobs
type Scheduler struct {
	scheduler *gocron.Scheduler
}

// NewScheduler creates a new job scheduler
func NewScheduler() *Scheduler {
	return &Scheduler{
		scheduler: gocron.NewScheduler(nil),
	}
}

// Start starts the scheduler
func (s *Scheduler) Start() {
	s.scheduler.StartAsync()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.scheduler.Stop()
}

// ScheduleDailyReportGeneration schedules daily report generation
func (s *Scheduler) ScheduleDailyReportGeneration(handler func(ctx context.Context) error) error {
	_, err := s.scheduler.Every(1).Day().Do(func() {
		_ = handler(context.Background())
	})
	return err
}

// ScheduleNotificationCleanup schedules notification cleanup
func (s *Scheduler) ScheduleNotificationCleanup(handler func(ctx context.Context) error) error {
	_, err := s.scheduler.Every(24).Hours().Do(func() {
		_ = handler(context.Background())
	})
	return err
}

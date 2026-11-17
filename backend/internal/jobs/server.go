package jobs

import (
	"errors"
	"fmt"
	"strings"

	"backend/internal/common/constants"
	"backend/internal/platform/config"

	"github.com/hibiken/asynq"
)

type Server struct {
	server *asynq.Server
}

func NewServer(redisCfg config.RedisConfig, jobsCfg config.AsynqConfig) (*Server, error) {
	if redisCfg.Host == "" || redisCfg.Port < 1 || redisCfg.Port > 65535 {
		return nil, errors.New("jobs: valid Redis host and port are required")
	}
	if jobsCfg.Concurrency < 1 {
		return nil, errors.New("jobs: concurrency must be positive")
	}
	if jobsCfg.RedisDB < 0 || jobsCfg.Retry.MaxRetries < 0 {
		return nil, errors.New("jobs: Redis DB and maximum retries must be non-negative")
	}
	if len(jobsCfg.Queues) == 0 {
		return nil, errors.New("jobs: at least one queue is required")
	}
	if jobsCfg.Queues[constants.QueueDefault] < 1 {
		return nil, errors.New("jobs: default queue must have a positive weight")
	}
	for name, weight := range jobsCfg.Queues {
		if strings.TrimSpace(name) == "" || weight < 1 {
			return nil, fmt.Errorf("jobs: invalid queue %q weight %d", name, weight)
		}
	}
	server := asynq.NewServer(redisOptions(redisCfg, jobsCfg), asynq.Config{
		Concurrency:    jobsCfg.Concurrency,
		Queues:         jobsCfg.Queues,
		StrictPriority: jobsCfg.StrictPriority,
	})
	return &Server{server: server}, nil
}

func (s *Server) Start(handler asynq.Handler) error {
	if s == nil || s.server == nil {
		return errors.New("jobs: server is not initialized")
	}
	if handler == nil {
		return errors.New("jobs: handler is required")
	}
	return s.server.Start(handler)
}

func (s *Server) Shutdown() {
	if s != nil && s.server != nil {
		s.server.Shutdown()
	}
}

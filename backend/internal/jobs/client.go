package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"

	"backend/internal/platform/config"

	"github.com/hibiken/asynq"
)

type Client struct {
	client *asynq.Client
}

func NewClient(redisCfg config.RedisConfig, jobsCfg config.AsynqConfig) (*Client, error) {
	if redisCfg.Host == "" || redisCfg.Port < 1 || redisCfg.Port > 65535 {
		return nil, errors.New("jobs: valid Redis host and port are required")
	}
	if jobsCfg.RedisDB < 0 || jobsCfg.Retry.MaxRetries < 0 {
		return nil, errors.New("jobs: Redis DB and maximum retries must be non-negative")
	}
	client := asynq.NewClient(redisOptions(redisCfg, jobsCfg))
	if err := client.Ping(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("jobs: Redis ping: %w", err)
	}
	return &Client{client: client}, nil
}

func (c *Client) EnqueueContext(ctx context.Context, task *asynq.Task, options ...asynq.Option) (*asynq.TaskInfo, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("jobs: client is not initialized")
	}
	return c.client.EnqueueContext(ctx, task, options...)
}

func (c *Client) Ping() error {
	if c == nil || c.client == nil {
		return errors.New("jobs: client is not initialized")
	}
	return c.client.Ping()
}

func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func redisOptions(redisCfg config.RedisConfig, jobsCfg config.AsynqConfig) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr:     net.JoinHostPort(redisCfg.Host, fmt.Sprint(redisCfg.Port)),
		Password: redisCfg.Password,
		DB:       jobsCfg.RedisDB,
	}
}

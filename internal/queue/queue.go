// Package queue wraps asynq (§4): one asynq.Server per worker pool so pools
// are isolated, a single topology (types.QueueDefinitions), per-task retry and
// timeout defaults, TaskID deduplication and dead-lettering.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Opts tunes one enqueue.
type Opts struct {
	// TaskID deduplicates: a second enqueue with the same id is a no-op while
	// the first is pending, active or retained.
	TaskID      string
	Interactive bool
	ProcessIn   time.Duration
}

// Enqueuer is what services depend on; tests substitute Inline.
type Enqueuer interface {
	Enqueue(ctx context.Context, taskType string, payload any, o Opts) error
}

// Handler processes one task payload.
type Handler func(ctx context.Context, payload []byte) error

// Policy is the retry/timeout policy of a task type (§4.4).
type Policy struct {
	MaxRetry int
	Timeout  time.Duration
}

var policies = map[string]Policy{
	types.TaskDocumentSplit:    {3, 5 * time.Minute},
	types.TaskPageRender:       {3, 10 * time.Minute},
	types.TaskPageOCR:          {3, 3 * time.Minute},
	types.TaskDocumentAssemble: {3, 10 * time.Minute},
	types.TaskIndexBuild:       {3, 30 * time.Minute},
	types.TaskIndexTree:        {5, 30 * time.Minute},
	types.TaskGraphExtract:     {3, 10 * time.Minute},
	types.TaskGraphResolve:     {10, 60 * time.Minute},
	types.TaskWikiIngest:       {10, 60 * time.Minute},
	types.TaskWikiFinalize:     {5, 30 * time.Minute},
	types.TaskDocumentDelete:   {3, time.Hour},
	types.TaskGenCleanup:       {3, time.Hour},
	types.TaskHousekeeping:     {0, 10 * time.Minute},
}

// PolicyFor returns the policy of a task type.
func PolicyFor(taskType string) Policy {
	if p, ok := policies[taskType]; ok {
		return p
	}
	return Policy{3, 30 * time.Minute}
}

// ErrSkipRetry marks a permanent failure: dead-letter now, do not retry.
var ErrSkipRetry = asynq.SkipRetry

// RedisOpt builds the asynq connection options.
func RedisOpt(c config.Redis) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr: c.Addr, Username: c.Username, Password: c.Password, DB: c.DB,
		ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second,
	}
}

// Client enqueues onto Redis.
type Client struct {
	c *asynq.Client
}

// NewClient connects and pings Redis.
func NewClient(c config.Redis) (*Client, error) {
	if c.Addr == "" {
		return nil, errors.New("queue: redis.addr (REDIS_ADDR) is required")
	}
	cl := asynq.NewClient(RedisOpt(c))
	if err := cl.Ping(); err != nil {
		cl.Close()
		return nil, fmt.Errorf("queue: redis ping: %w", err)
	}
	return &Client{c: cl}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.c.Close() }

// Enqueue implements Enqueuer. A duplicate TaskID is not an error.
func (c *Client) Enqueue(ctx context.Context, taskType string, payload any, o Opts) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	p := PolicyFor(taskType)
	opts := []asynq.Option{
		asynq.Queue(types.QueueFor(taskType, o.Interactive)),
		asynq.MaxRetry(p.MaxRetry),
		asynq.Timeout(p.Timeout),
	}
	if o.TaskID != "" {
		opts = append(opts, asynq.TaskID(o.TaskID))
	}
	if o.ProcessIn > 0 {
		opts = append(opts, asynq.ProcessIn(o.ProcessIn))
	}
	_, err = c.c.EnqueueContext(ctx, asynq.NewTask(taskType, b), opts...)
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// DeadLetterSink records tasks that exhausted their retries.
type DeadLetterSink func(ctx context.Context, taskType, queue string, payload []byte, err error, attempts int)

// Workers runs one asynq server per pool.
type Workers struct {
	servers []*asynq.Server
	mux     *asynq.ServeMux
	log     *slog.Logger
}

// NewWorkers builds servers per pool with the given concurrency map; pools
// with no entry use 2 workers.
func NewWorkers(c config.Redis, concurrency map[string]int, log *slog.Logger, handlers map[string]Handler, dl DeadLetterSink) *Workers {
	mux := asynq.NewServeMux()
	mux.Use(loggingMiddleware(log), deadLetterMiddleware(dl, log))
	for t, h := range handlers {
		h := h
		mux.HandleFunc(t, func(ctx context.Context, task *asynq.Task) error { return h(ctx, task.Payload()) })
	}
	w := &Workers{mux: mux, log: log}
	for _, pool := range types.Pools() {
		n := concurrency[pool]
		if n <= 0 {
			n = 2
		}
		w.servers = append(w.servers, asynq.NewServer(RedisOpt(c), asynq.Config{
			Concurrency:     n,
			Queues:          types.QueueWeightsForPool(pool),
			ShutdownTimeout: 20 * time.Second,
			Logger:          asynqLogger{log.With("pool", pool)},
			LogLevel:        asynq.WarnLevel,
		}))
	}
	return w
}

// Start runs every server (non-blocking).
func (w *Workers) Start() error {
	for _, s := range w.servers {
		if err := s.Start(w.mux); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown stops every server gracefully.
func (w *Workers) Shutdown() {
	for _, s := range w.servers {
		s.Shutdown()
	}
}

func loggingMiddleware(log *slog.Logger) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			start := time.Now()
			err := next.ProcessTask(ctx, t)
			id, _ := asynq.GetTaskID(ctx)
			if err != nil {
				log.Warn("task failed", "type", t.Type(), "task_id", id, "err", err, "ms", time.Since(start).Milliseconds())
			} else {
				log.Debug("task done", "type", t.Type(), "task_id", id, "ms", time.Since(start).Milliseconds())
			}
			return err
		})
	}
}

func deadLetterMiddleware(dl DeadLetterSink, log *slog.Logger) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			err := next.ProcessTask(ctx, t)
			if err == nil || dl == nil {
				return err
			}
			retried, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			if errors.Is(err, asynq.SkipRetry) || retried >= maxRetry {
				q, _ := asynq.GetQueueName(ctx)
				dl(context.WithoutCancel(ctx), t.Type(), q, t.Payload(), err, retried+1)
				log.Error("task dead-lettered", "type", t.Type(), "attempts", retried+1, "err", err)
			}
			return err
		})
	}
}

// IsFinalAttempt reports whether the running task will not be retried when
// it fails now; handlers use it to fall back instead of failing (§5.8).
func IsFinalAttempt(ctx context.Context) bool {
	retried, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	if !ok1 || !ok2 {
		return inlineFinal(ctx)
	}
	return retried >= maxRetry
}

type asynqLogger struct{ l *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.l.Debug(fmt.Sprint(args...)) }
func (a asynqLogger) Info(args ...any)  { a.l.Info(fmt.Sprint(args...)) }
func (a asynqLogger) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...)) }
func (a asynqLogger) Error(args ...any) { a.l.Error(fmt.Sprint(args...)) }
func (a asynqLogger) Fatal(args ...any) { a.l.Error(fmt.Sprint(args...)) }

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type finalKey struct{}

func inlineFinal(ctx context.Context) bool {
	v, _ := ctx.Value(finalKey{}).(bool)
	return v
}

// Inline is an in-process Enqueuer for tests and single-binary runs without
// Redis. Tasks are queued in memory and executed by Drain, honouring TaskID
// deduplication and each type's MaxRetry.
type Inline struct {
	mu       sync.Mutex
	handlers map[string]Handler
	queue    []inlineTask
	seen     map[string]bool
	// Failed collects tasks that exhausted retries.
	Failed []string
	dl     DeadLetterSink
	// HonorDelays makes Opts.ProcessIn delay a task. Off by default so tests
	// can Drain everything at once; the in-process runtime turns it on.
	HonorDelays bool
}

type inlineTask struct {
	typ       string
	payload   []byte
	id        string
	notBefore time.Time // Opts.ProcessIn
}

// NewInline returns an empty inline queue.
func NewInline() *Inline { return &Inline{handlers: map[string]Handler{}, seen: map[string]bool{}} }

// Register installs the handlers (same map as NewWorkers).
func (q *Inline) Register(handlers map[string]Handler, dl DeadLetterSink) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for k, v := range handlers {
		q.handlers[k] = v
	}
	q.dl = dl
}

// Enqueue implements Enqueuer.
func (q *Inline) Enqueue(_ context.Context, taskType string, payload any, o Opts) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if o.TaskID != "" {
		if q.seen[o.TaskID] {
			return nil
		}
		q.seen[o.TaskID] = true
	}
	t := inlineTask{typ: taskType, payload: b, id: o.TaskID}
	if o.ProcessIn > 0 && q.HonorDelays {
		t.notBefore = time.Now().Add(o.ProcessIn)
	}
	q.queue = append(q.queue, t)
	return nil
}

// Pending returns the number of queued tasks.
func (q *Inline) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

// Drain runs due tasks (including ones they enqueue) until none is due or
// limit tasks ran. With HonorDelays, delayed tasks wait for a later Drain.
// Each task is retried up to its MaxRetry.
func (q *Inline) Drain(ctx context.Context, limit int) error {
	for n := 0; n < limit; n++ {
		q.mu.Lock()
		due := -1
		now := time.Now()
		for i, t := range q.queue {
			if !t.notBefore.After(now) {
				due = i
				break
			}
		}
		if due < 0 {
			q.mu.Unlock()
			return nil
		}
		t := q.queue[due]
		q.queue = append(q.queue[:due:due], q.queue[due+1:]...)
		h := q.handlers[t.typ]
		q.mu.Unlock()
		if h == nil {
			return fmt.Errorf("inline queue: no handler for %s", t.typ)
		}
		p := PolicyFor(t.typ)
		var err error
		for attempt := 0; attempt <= p.MaxRetry; attempt++ {
			actx := context.WithValue(ctx, finalKey{}, attempt == p.MaxRetry)
			tctx, cancel := context.WithTimeout(actx, p.Timeout)
			err = h(tctx, t.payload)
			cancel()
			if err == nil || errors.Is(err, ErrSkipRetry) {
				break
			}
		}
		if t.id != "" {
			q.mu.Lock()
			delete(q.seen, t.id)
			q.mu.Unlock()
		}
		if err != nil {
			q.Failed = append(q.Failed, t.typ+": "+err.Error())
			if q.dl != nil {
				q.dl(ctx, t.typ, "inline", t.payload, err, p.MaxRetry+1)
			}
		}
	}
	return errors.New("inline queue: drain limit reached")
}

// RunLoop drains continuously until ctx ends (single-binary mode without
// Redis). It polls every interval.
func (q *Inline) RunLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		_ = q.Drain(ctx, 1000)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

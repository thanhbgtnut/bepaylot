package queue

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/thanhenti/bepaylot/internal/types"
)

// A task a user watches is retried after seconds; others keep asynq's
// default backoff (15 s and more).
func TestRetryDelay(t *testing.T) {
	job := asynq.NewTask(types.TaskDocTypeJob, nil)
	if d := retryDelay(0, nil, job); d != 2*time.Second {
		t.Fatalf("first retry of %s after %v", types.TaskDocTypeJob, d)
	}
	if d := retryDelay(2, nil, job); d != 6*time.Second {
		t.Fatalf("third retry of %s after %v", types.TaskDocTypeJob, d)
	}
	if d := retryDelay(0, nil, asynq.NewTask(types.TaskIndexBuild, nil)); d < 15*time.Second {
		t.Fatalf("first retry of %s after %v", types.TaskIndexBuild, d)
	}
}

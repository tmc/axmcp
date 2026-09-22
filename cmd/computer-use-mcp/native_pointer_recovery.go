package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// nativePointerRecovery owns one unfinished mouse-up. An exhausted retry round
// keeps the original resource alive until another round succeeds or stop closes
// it. An uncertain post is never retried. The zero value is ready for use.
type nativePointerRecovery struct {
	mu         sync.Mutex
	job        *nativePointerRecoveryJob
	closed     bool
	roundLimit time.Duration // zero selects the production 30-second limit
}

type nativePointerRecoveryJob struct {
	id                       string
	cancel                   context.CancelFunc
	done                     chan struct{}
	status                   string
	err                      error
	attempt                  func(context.Context) (bool, error)
	release                  func() error
	releaseOnce              sync.Once
	releaseErr               error
	sent, retryable, stopped bool
}

func (j *nativePointerRecoveryJob) dispose() error {
	j.releaseOnce.Do(func() { j.releaseErr = j.release() })
	return j.releaseErr
}

func (r *nativePointerRecovery) start(ctx context.Context, attempt func(context.Context) (bool, error), release func() error) error {
	if attempt == nil || release == nil {
		return fmt.Errorf("pointer recovery callbacks required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("pointer recovery is closed")
	}
	if r.job != nil && r.job.status != "recovered" {
		return fmt.Errorf("pointer recovery is %s", r.job.status)
	}
	job := &nativePointerRecoveryJob{id: rand.Text(), attempt: attempt, release: release}
	r.job = job
	r.beginRound(ctx, job)
	return nil
}

// beginRound runs under mu. Each round owns its own context and completion
// channel; a later round cannot change a previous goroutine's deferred cleanup.
func (r *nativePointerRecovery) beginRound(ctx context.Context, job *nativePointerRecoveryJob) {
	limit := r.roundLimit
	if limit == 0 {
		limit = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), limit)
	done := make(chan struct{})
	job.cancel, job.done = cancel, done
	job.status, job.err, job.retryable = "pending", nil, false
	go r.run(ctx, cancel, done, job)
}

func (r *nativePointerRecovery) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}, job *nativePointerRecoveryJob) {
	defer cancel()
	var last error
	var sent bool
	for ctx.Err() == nil {
		attemptCtx, finish := context.WithTimeout(ctx, 250*time.Millisecond)
		sent, last = job.attempt(attemptCtx)
		finish()
		if sent {
			break
		}
		if last == nil {
			last = fmt.Errorf("pointer recovery event was not posted")
		}
		if err := waitNativePointer(ctx, time.Now().Add(100*time.Millisecond)); err != nil {
			break
		}
	}
	if sent {
		last = errors.Join(last, job.dispose())
	} else {
		last = errors.Join(last, ctx.Err())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	job.sent, job.err = sent, last
	job.status = "unresolved"
	job.retryable = !sent && !job.stopped
	if sent && last == nil {
		job.status = "recovered"
	}
	// Publish completion before another caller can start a round or join it.
	close(done)
}

func (r *nativePointerRecovery) retry(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	job := r.job
	if r.closed || job == nil || id == "" || job.id != id {
		return fmt.Errorf("unknown pointer recovery")
	}
	if job.stopped || job.sent || !job.retryable {
		return fmt.Errorf("pointer recovery cannot be retried")
	}
	r.beginRound(ctx, job)
	return nil
}

func (r *nativePointerRecovery) state() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job == nil {
		return "", nil
	}
	return r.job.status, r.job.err
}

func (r *nativePointerRecovery) details() (id, status string, retryable bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job == nil {
		return "", "", false, nil
	}
	return r.job.id, r.job.status, r.job.retryable, r.job.err
}

func (r *nativePointerRecovery) blocked() error {
	status, err := r.state()
	if status == "pending" || status == "unresolved" {
		return errors.Join(fmt.Errorf("pointer recovery is %s", status), err)
	}
	return nil
}

func (r *nativePointerRecovery) close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.stop()
}

// stop cancels and joins outside mu, then closes the retained resource. It
// disables further retries even if the event was confirmed unsent.
func (r *nativePointerRecovery) stop() error {
	r.mu.Lock()
	job := r.job
	if job != nil {
		job.stopped = true
		job.retryable = false
		job.cancel()
	}
	r.mu.Unlock()
	if job == nil {
		return nil
	}
	<-job.done
	releaseErr := job.dispose()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !job.sent {
		job.err = errors.Join(job.err, releaseErr)
	}
	return job.err
}

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitPointerRecovery(t *testing.T, r *nativePointerRecovery) {
	t.Helper()
	r.mu.Lock()
	done := r.job.done
	r.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not finish")
	}
}

func TestNativePointerRecoveryResumed(t *testing.T) {
	var r nativePointerRecovery
	var attempts, releases atomic.Int32
	err := r.start(t.Context(), func(ctx context.Context) (bool, error) {
		if releases.Load() != 0 {
			t.Error("released resource used")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded recovery attempt")
		}
		if attempts.Add(1) == 1 {
			return false, errors.New("target stalled")
		}
		return true, nil
	}, func() error { releases.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	waitPointerRecovery(t, &r)
	if status, err := r.state(); status != "recovered" || err != nil {
		t.Fatalf("status=%s error=%v", status, err)
	}
	if attempts.Load() != 2 || releases.Load() != 1 {
		t.Fatalf("attempts=%d releases=%d", attempts.Load(), releases.Load())
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativePointerRecoveryUncertainPost(t *testing.T) {
	var r nativePointerRecovery
	var attempts atomic.Int32
	uncertain := errors.New("post outcome uncertain")
	if err := r.start(t.Context(), func(context.Context) (bool, error) { attempts.Add(1); return true, uncertain }, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	waitPointerRecovery(t, &r)
	if status, err := r.state(); status != "unresolved" || !errors.Is(err, uncertain) {
		t.Fatalf("status=%s error=%v", status, err)
	}
	if err := r.start(t.Context(), func(context.Context) (bool, error) {
		t.Error("new gesture allowed after uncertain post")
		return true, nil
	}, func() error { return nil }); err == nil {
		t.Fatal("unresolved recovery replaced")
	}
	if attempts.Load() != 1 {
		t.Fatalf("uncertain event retried %d times", attempts.Load())
	}
	if err := r.close(); !errors.Is(err, uncertain) {
		t.Fatal(err)
	}
}

func TestNativePointerRecoveryCloseJoins(t *testing.T) {
	var r nativePointerRecovery
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releases atomic.Int32
	if err := r.start(t.Context(), func(ctx context.Context) (bool, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-finish
		if releases.Load() != 0 {
			t.Error("resource closed while callback active")
		}
		return false, ctx.Err()
	}, func() error { releases.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- r.close() }()
	<-canceled
	select {
	case <-closed:
		t.Fatal("close returned before callback finished")
	default:
	}
	close(finish)
	if err := <-closed; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if releases.Load() != 1 {
		t.Fatalf("releases=%d", releases.Load())
	}
	if status, _ := r.state(); status != "unresolved" {
		t.Fatal(status)
	}
	if err := r.start(t.Context(), func(context.Context) (bool, error) { return true, nil }, func() error { return nil }); err == nil {
		t.Fatal("closed recovery restarted")
	}
}

func TestNativePointerRecoveryCloseWhileGateHeld(t *testing.T) {
	var r nativePointerRecovery
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	entered := make(chan struct{})
	var enterOnce sync.Once
	var posts atomic.Int32
	if err := r.start(t.Context(), func(ctx context.Context) (bool, error) {
		enterOnce.Do(func() { close(entered) })
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return false, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		posts.Add(1)
		return true, nil
	}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- r.close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// Unblock a faulty implementation so the test does not leak a worker.
		<-gate
		<-closed
		t.Fatal("close deadlocked behind the gate it already holds")
	}
	<-gate
	if posts.Load() != 0 {
		t.Fatal("cleanup dispatched after close canceled a queued attempt")
	}
}

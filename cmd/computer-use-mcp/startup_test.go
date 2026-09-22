package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServeDoesNotWaitForPermissionGrant(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := make(chan struct{})
	closed := false
	served := false
	err := serveWithOnboarding(ctx, func(ctx context.Context) error {
		select {
		case <-started:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		served = true
		return nil // Transport EOF, without a permission grant.
	}, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		closed = true
		return ctx.Err()
	})
	if err != nil || !served || !closed {
		t.Fatalf("startup/cleanup: served=%v closed=%v err=%v", served, closed, err)
	}
}

func TestServeJoinsStartupErrors(t *testing.T) {
	serveErr, onboardErr := errors.New("transport failed"), errors.New("onboarding failed")
	err := serveWithOnboarding(t.Context(), func(context.Context) error { return serveErr }, func(context.Context) error { return onboardErr })
	if !errors.Is(err, serveErr) || !errors.Is(err, onboardErr) {
		t.Fatalf("lost error: %v", err)
	}
}

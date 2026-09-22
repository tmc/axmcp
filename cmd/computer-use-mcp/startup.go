package main

import (
	"context"
	"errors"
)

// serveWithOnboarding keeps permission UI independent of MCP initialization.
// It joins onboarding on transport completion so queued UI cleanup precedes exit.
func serveWithOnboarding(ctx context.Context, serve, onboard func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- onboard(ctx) }()
	err := serve(ctx)
	cancel()
	onboardingErr := <-done
	if errors.Is(onboardingErr, context.Canceled) {
		onboardingErr = nil
	}
	return errors.Join(err, onboardingErr)
}

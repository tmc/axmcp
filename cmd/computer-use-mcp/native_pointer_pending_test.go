package main

import (
	"context"
	"errors"
	"github.com/tmc/axmcp/internal/computeruse"
	"sync"
	"testing"
)

func TestNativePendingBlocksActions(t *testing.T) {
	r, b := newNativeTestRunner(t)
	defer r.close()
	observed := nativeTestObserve(t, r)
	entered := make(chan struct{})
	var once sync.Once
	if err := r.pointerRecovery.start(t.Context(), func(ctx context.Context) (bool, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return false, ctx.Err()
	}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	// Denial happens before consuming the observation, and applies to both APIs.
	out := r.act(t.Context(), nil, nativeTestClick(observed))
	if out.Execution != "not_dispatched" || out.PointerRecovery != "pending" || b.calls != 0 {
		t.Fatalf("out=%+v calls=%d", out, b.calls)
	}
	if finish, err := beginLegacyNativeAction(t.Context(), &runtimeState{native: r}); err == nil {
		finish()
		t.Fatal("legacy action bypassed pending recovery")
	}
	if _, ok := r.store.Get(observed.StateID); !ok {
		t.Fatal("blocked action consumed observation")
	}
	if err := r.close(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if status, _ := r.pointerRecovery.state(); status != "unresolved" {
		t.Fatal(status)
	}
}

func TestNativeRecoveryRejectsDifferentScreenshotWindow(t *testing.T) {
	target := nativeTarget{Window: computeruse.WindowInfo{WindowID: 8}}
	sent, err := postNativePointer(t.Context(), target, nil, &computeruse.ScreenshotInfo{TargetWindow: 7}, nativePointerEvent{Kind: "up", Button: "left"}, true, nil)
	if sent || err == nil {
		t.Fatalf("different screenshot window accepted: sent=%v error=%v", sent, err)
	}
}

func TestNativeReleasePendingRecovery(t *testing.T) {
	r, _ := newDiscoveryTestRunner(t)
	candidates := discoverNativeTest(t, r)
	var handles []string
	for _, candidate := range candidates.Windows {
		selected, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidate.SelectionID})
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, selected.TargetHandle)
	}
	entered := make(chan struct{})
	var once sync.Once
	if err := r.pointerRecovery.start(t.Context(), func(ctx context.Context) (bool, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return false, ctx.Err()
	}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	r.recoveryHandle = handles[0]
	<-entered
	out, err := r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: handles[1]})
	if err != nil || !out.Released {
		t.Fatalf("unrelated release=%+v error=%v", out, err)
	}
	if status, _ := r.pointerRecovery.state(); status != "pending" {
		t.Fatalf("unrelated handle stopped recovery: %s", status)
	}
	out, err = r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: handles[0]})
	if !out.Released || !errors.Is(err, context.Canceled) {
		t.Fatalf("matching release=%+v error=%v", out, err)
	}
	if status, _ := r.pointerRecovery.state(); status != "unresolved" {
		t.Fatalf("unfinished release lost status: %s", status)
	}
}

func TestNativeReleaseJoinsQueuedProductionRecovery(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	candidates := discoverNativeTest(t, r)
	selected, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[0].SelectionID})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := r.observe(t.Context(), nil, nativeObserveInput{TargetHandle: selected.TargetHandle})
	if err != nil {
		t.Fatal(err)
	}
	err = r.run(t.Context(), 0, func(ctx context.Context) error {
		lease, err := r.store.Take(observed.StateID)
		if err != nil {
			return err
		}
		defer lease.Close()
		if err := r.recoverPointer(ctx, nil, selected.TargetHandle, r.observation.target, lease, nativePointerEvent{Kind: "up", Button: "left"}); err != nil {
			return err
		}
		// The production callback cannot take this gate. stop must cancel that
		// acquisition and join before releasing the selected target's resources.
		if !r.releaseTarget(nil, selected.TargetHandle) {
			t.Fatal("target not released")
		}
		if status, _ := r.pointerRecovery.state(); status != "unresolved" {
			t.Fatalf("status=%s", status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.sets[0].snapshots[0].closed != 1 {
		t.Fatal("recovery retained snapshot after release")
	}
}

package main

import (
	"testing"
	"time"
)

func TestNativeTargetLifetime(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	now := time.Now()
	r.now = func() time.Time { return now }
	candidates := discoverNativeTest(t, r)
	var handles []string
	for _, window := range candidates.Windows {
		selected, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: window.SelectionID})
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, selected.TargetHandle)
	}
	set := b.sets[0]
	if len(set.snapshots) != 0 {
		t.Fatal("selection captured an observation")
	}
	now = now.Add(70 * time.Second)
	if _, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[0].SelectionID}); err == nil {
		t.Fatal("expired candidate accepted")
	}
	if set.closed.Load() != 0 {
		t.Fatal("expiry closed retained windows")
	}
	for i, handle := range handles {
		out, err := r.observe(t.Context(), nil, nativeObserveInput{TargetHandle: handle})
		if err != nil || out.StateID == "" || out.Window.WindowID != candidates.Windows[i].WindowID {
			t.Fatalf("observe retained window: %+v %v", out, err)
		}
	}
	// Releasing another handle must preserve the current observation.
	state := r.observation.output.StateID
	released, err := r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: handles[0]})
	if err != nil || !released.Released || r.observation.output.StateID != state || set.closed.Load() != 0 {
		t.Fatalf("release unrelated handle: %+v %v", released, err)
	}
	released, err = r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: handles[1]})
	if err != nil || !released.Released || r.observation != nil || set.closed.Load() != 1 {
		t.Fatalf("release final handle: %+v %v", released, err)
	}
	if lease, err := r.store.Acquire(state); err == nil {
		lease.Close()
		t.Fatal("released observation remains usable")
	}
	released, err = r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: handles[1]})
	if err != nil || released.Released || set.closed.Load() != 1 {
		t.Fatal("release is not idempotent")
	}
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{TargetHandle: handles[1]}); err == nil {
		t.Fatal("released handle observed")
	}
}

func TestNativeTargetSurvivesRediscovery(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	candidates := discoverNativeTest(t, r)
	selected, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[1].SelectionID})
	if err != nil {
		t.Fatal(err)
	}
	discoverNativeTest(t, r)
	if b.sets[0].closed.Load() != 0 {
		t.Fatal("rediscovery closed selected window")
	}
	out, err := r.observe(t.Context(), nil, nativeObserveInput{TargetHandle: selected.TargetHandle})
	if err != nil || out.Window.WindowID != candidates.Windows[1].WindowID {
		t.Fatalf("retained observation: %+v %v", out, err)
	}
	b.sets[0].changeBirth = true
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{TargetHandle: selected.TargetHandle}); err == nil {
		t.Fatal("changed process instance accepted")
	}
	if b.sets[0].snapshots[1].closed != 1 {
		t.Fatal("rejected capture leaked")
	}
	r.close()
	for _, set := range b.sets {
		if set.closed.Load() != 1 {
			t.Fatal("runner close did not release group exactly once")
		}
	}
}

func TestNativeTargetLimit(t *testing.T) {
	r, _ := newDiscoveryTestRunner(t)
	candidates := discoverNativeTest(t, r)
	var first string
	for i := 0; i < nativeTargetLimit; i++ {
		out, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[0].SelectionID})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.TargetHandle
		}
	}
	if _, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[0].SelectionID}); err == nil {
		t.Fatal("target limit not enforced")
	}
	if _, err := r.release(t.Context(), nil, nativeReleaseInput{TargetHandle: first}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.selectTarget(t.Context(), nil, nativeSelectInput{SelectionID: candidates.Windows[0].SelectionID}); err != nil {
		t.Fatal(err)
	}
}

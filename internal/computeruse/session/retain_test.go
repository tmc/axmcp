package session

import (
	"sync"
	"testing"
)

func TestLeaseRetainConsumed(t *testing.T) {
	store := NewStore()
	snapshot := &trackedSnapshot{}
	state := bindTracked(t, store, snapshot)
	original, err := store.Take(state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := original.Retain()
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if _, err := store.Acquire(state.StateID); err == nil {
		t.Fatal("retention restored consumed token")
	}
	original.Close()
	store.Close()
	if snapshot.closes.Load() != 0 {
		t.Fatal("retained snapshot released early")
	}
	if _, _, err := retained.Resolve(1); err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Retain(); err == nil {
		t.Fatal("closed store allowed new lease")
	}
	retained.Close()
	if snapshot.closes.Load() != 1 {
		t.Fatal("final lease did not close snapshot exactly once")
	}
	if _, err := original.Retain(); err == nil {
		t.Fatal("closed lease resurrected")
	}
}

func TestLeaseRetainUnavailable(t *testing.T) {
	for _, lease := range []*Lease{nil, {}} {
		if _, err := lease.Retain(); err == nil {
			t.Fatal("unavailable lease retained")
		}
	}
}

func TestLeaseRetainConcurrentClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		store := NewStore()
		snapshot := &trackedSnapshot{}
		state := bindTracked(t, store, snapshot)
		original, err := store.Take(state.StateID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); original.Close() }()
		go func() {
			defer wg.Done()
			retained, err := original.Retain()
			if err != nil {
				return
			}
			defer retained.Close()
			if _, _, err := retained.Resolve(1); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		store.Close()
		if snapshot.closes.Load() != 1 {
			t.Fatalf("closes=%d", snapshot.closes.Load())
		}
	}
}

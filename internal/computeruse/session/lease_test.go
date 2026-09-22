package session

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

type trackedSnapshot struct {
	closes  atomic.Int32
	entered chan struct{}
	release chan struct{}
	onClose func() error
}

func (s *trackedSnapshot) State() computeruse.AppState {
	return computeruse.AppState{App: computeruse.AppInfo{BundleID: "com.example.lease", PID: 123}}
}
func (s *trackedSnapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	if s.closes.Load() != 0 {
		return nil, computeruse.ElementNode{}, errors.New("snapshot closed during Resolve")
	}
	return nil, computeruse.ElementNode{Index: index, Title: "Target"}, nil
}
func (s *trackedSnapshot) Close() error {
	s.closes.Add(1)
	if s.onClose != nil {
		return s.onClose()
	}
	return nil
}
func bindTracked(t *testing.T, store *Store, snapshot *trackedSnapshot) computeruse.AppState {
	t.Helper()
	state, err := store.Bind(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestLeaseRetainsSnapshot(t *testing.T) {
	for _, operation := range []string{"replace", "invalidate", "close"} {
		t.Run(operation, func(t *testing.T) {
			store := NewStore()
			defer store.Close()
			snapshot := &trackedSnapshot{}
			state := bindTracked(t, store, snapshot)
			a, err := store.Acquire(state.StateID)
			if err != nil {
				t.Fatal(err)
			}
			b, err := store.Acquire(state.StateID)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			defer b.Close()
			switch operation {
			case "replace":
				bindTracked(t, store, &trackedSnapshot{})
			case "invalidate":
				if err := store.InvalidateSession(state.SessionID); err != nil {
					t.Fatal(err)
				}
			case "close":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Acquire(state.StateID); err == nil {
				t.Fatal("invalidated token accepted")
			}
			if n := snapshot.closes.Load(); n != 0 {
				t.Fatalf("active snapshot closed %d times", n)
			}
			if _, node, err := a.Resolve(1); err != nil || node.Title != "Target" {
				t.Fatalf("retained Resolve: %+v, %v", node, err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if n := snapshot.closes.Load(); n != 0 {
				t.Fatalf("snapshot closed with second lease: %d", n)
			}
			if _, _, err := a.Resolve(1); err == nil {
				t.Fatal("closed lease resolved")
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if n := snapshot.closes.Load(); n != 1 {
				t.Fatalf("final closes=%d, want 1", n)
			}
		})
	}
}

func TestLeaseTakeConcurrent(t *testing.T) {
	store := NewStore()
	defer store.Close()
	snapshot := &trackedSnapshot{}
	state := bindTracked(t, store, snapshot)
	start := make(chan struct{})
	winners := make(chan *Lease, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, err := store.Take(state.StateID)
			if err == nil {
				winners <- lease
			}
		}()
	}
	close(start)
	wg.Wait()
	close(winners)
	var leases []*Lease
	for lease := range winners {
		leases = append(leases, lease)
		defer lease.Close()
	}
	if len(leases) != 1 {
		t.Fatalf("Take winners=%d, want 1", len(leases))
	}
	for _, lease := range leases {
		if _, _, err := lease.Resolve(1); err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if n := snapshot.closes.Load(); n != 1 {
		t.Fatalf("closes=%d, want 1", n)
	}
}

func TestLeaseReplacementDuringResolve(t *testing.T) {
	store := NewStore()
	defer store.Close()
	snapshot := &trackedSnapshot{entered: make(chan struct{}), release: make(chan struct{})}
	state := bindTracked(t, store, snapshot)
	lease, err := store.Acquire(state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	done := make(chan error, 1)
	go func() { _, _, err := lease.Resolve(1); done <- err }()
	<-snapshot.entered
	bindTracked(t, store, &trackedSnapshot{})
	close(snapshot.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Resolve is finished, but the returned handle is still in use by the action.
	if n := snapshot.closes.Load(); n != 0 {
		t.Fatalf("snapshot closed before action end: %d", n)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if n := snapshot.closes.Load(); n != 1 {
		t.Fatalf("closes=%d, want 1", n)
	}
}

func TestLeaseDeferredCloseError(t *testing.T) {
	store := NewStore()
	want := errors.New("close failed")
	snapshot := &trackedSnapshot{onClose: func() error { return want }}
	state := bindTracked(t, store, snapshot)
	lease, err := store.Acquire(state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("premature close error: %v", err)
	}
	if err := lease.Close(); !errors.Is(err, want) {
		t.Fatalf("lease close=%v, want %v", err, want)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("repeated close=%v", err)
	}
	rejected := &trackedSnapshot{}
	if _, err := store.Bind(rejected); err == nil {
		t.Fatal("closed store accepted Bind")
	}
	if n := rejected.closes.Load(); n != 1 {
		t.Fatalf("rejected snapshot closes=%d", n)
	}
	if _, err := store.Take(state.StateID); err == nil {
		t.Fatal("closed store accepted Take")
	}
}

func TestSnapshotCloseOutsideStoreLock(t *testing.T) {
	for _, operation := range []string{"replace", "invalidate", "close", "lease"} {
		t.Run(operation, func(t *testing.T) {
			store := NewStore()
			defer store.Close()
			snapshot := &trackedSnapshot{onClose: func() error { store.Get("reentrant"); return nil }}
			state := bindTracked(t, store, snapshot)
			switch operation {
			case "replace":
				bindTracked(t, store, &trackedSnapshot{})
			case "invalidate":
				if err := store.InvalidateSession(state.SessionID); err != nil {
					t.Fatal(err)
				}
			case "close":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "lease":
				lease, err := store.Take(state.StateID)
				if err != nil {
					t.Fatal(err)
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if n := snapshot.closes.Load(); n != 1 {
				t.Fatalf("closes=%d, want 1", n)
			}
		})
	}
}

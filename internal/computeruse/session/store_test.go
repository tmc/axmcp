//go:build darwin

package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

type fakeSnapshot struct {
	state    computeruse.AppState
	nodes    map[int]computeruse.ElementNode
	closeErr error
	closed   bool
	closes   int
}

func (f *fakeSnapshot) State() computeruse.AppState {
	return f.state
}

func (f *fakeSnapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	node, ok := f.nodes[index]
	if !ok {
		return nil, computeruse.ElementNode{}, fmt.Errorf("missing index %d", index)
	}
	return nil, node, nil
}

func (f *fakeSnapshot) Close() error {
	f.closed = true
	f.closes++
	return f.closeErr
}

func TestBindReplacesPriorState(t *testing.T) {
	store := NewStore()
	first := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
		closeErr: fmt.Errorf("close replaced snapshot"),
	}
	firstState, err := store.Bind(first)
	if err != nil {
		t.Fatalf("Bind(first): %v", err)
	}
	second := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
	}
	secondState, err := store.Bind(second)
	if err != nil {
		t.Fatalf("Bind(second) should ignore replaced snapshot cleanup error: %v", err)
	}
	if !first.closed {
		t.Fatalf("first snapshot should be closed after replacement")
	}
	if first.closes != 1 {
		t.Fatalf("first snapshot closed %d times, want once", first.closes)
	}
	if firstState.StateID == secondState.StateID {
		t.Fatalf("StateID should change across bindings")
	}
	if _, _, err := store.Resolve(firstState.StateID, 0); err == nil {
		t.Fatalf("old state_id should be stale")
	} else if got := err.Error(); !strings.Contains(got, "retry with the fresh state_id") {
		t.Fatalf("stale error = %q, want retry guidance", got)
	}
}

func TestBindMissingIdentityClosesSnapshot(t *testing.T) {
	store := NewStore()
	snapshot := &fakeSnapshot{closeErr: fmt.Errorf("close unpublished snapshot")}
	if _, err := store.Bind(snapshot); err == nil {
		t.Fatalf("Bind missing identity error = nil, want error")
	} else if got := err.Error(); !strings.Contains(got, "missing app identity") {
		t.Fatalf("Bind missing identity error = %q, want app identity error", got)
	}
	if !snapshot.closed || snapshot.closes != 1 {
		t.Fatalf("close state = closed %v closes %d, want closed once", snapshot.closed, snapshot.closes)
	}
}

func TestResolveUsesCurrentSnapshot(t *testing.T) {
	store := NewStore()
	snapshot := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
		nodes: map[int]computeruse.ElementNode{
			7: {Index: 7, Title: "Play"},
		},
	}
	state, err := store.Bind(snapshot)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	_, node, err := store.Resolve(state.StateID, 7)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if node.Title != "Play" {
		t.Fatalf("Resolve title = %q, want Play", node.Title)
	}
}

func TestSnapshotReturnsCurrentSnapshot(t *testing.T) {
	store := NewStore()
	snapshot := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
	}
	state, err := store.Bind(snapshot)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, err := store.Snapshot(state.StateID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got != snapshot {
		t.Fatalf("Snapshot returned %T, want bound snapshot", got)
	}
}

func TestSnapshotReturnsStaleStateError(t *testing.T) {
	store := NewStore()
	if _, err := store.Snapshot("old"); err == nil {
		t.Fatalf("Snapshot stale state error = nil, want error")
	} else if got := err.Error(); !strings.Contains(got, `unknown or stale state_id "old"`) || !strings.Contains(got, "retry with the fresh state_id") {
		t.Fatalf("Snapshot stale error = %q, want retry guidance", got)
	}
}

func TestCloseClosesSnapshotsAndRemovesState(t *testing.T) {
	store := NewStore()
	firstErr := fmt.Errorf("close first")
	first := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.first"},
		},
		closeErr: firstErr,
	}
	second := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.second"},
		},
	}
	firstState, err := store.Bind(first)
	if err != nil {
		t.Fatalf("Bind(first): %v", err)
	}
	secondState, err := store.Bind(second)
	if err != nil {
		t.Fatalf("Bind(second): %v", err)
	}

	if err := store.Close(); err != firstErr {
		t.Fatalf("Close error = %v, want %v", err, firstErr)
	}
	if !first.closed || first.closes != 1 {
		t.Fatalf("first close state = closed %v closes %d, want closed once", first.closed, first.closes)
	}
	if !second.closed || second.closes != 1 {
		t.Fatalf("second close state = closed %v closes %d, want closed once", second.closed, second.closes)
	}
	if _, ok := store.Get(firstState.StateID); ok {
		t.Fatalf("first state still present after Close")
	}
	if _, ok := store.Get(secondState.StateID); ok {
		t.Fatalf("second state still present after Close")
	}
	if _, ok := store.GetForApp("com.example.first"); ok {
		t.Fatalf("first session still present after Close")
	}
	if _, ok := store.GetForApp("com.example.second"); ok {
		t.Fatalf("second session still present after Close")
	}
}

func TestInvalidateSessionClosesAndRemovesState(t *testing.T) {
	store := NewStore()
	closeErr := fmt.Errorf("close session")
	snapshot := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
		closeErr: closeErr,
	}
	state, err := store.Bind(snapshot)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if err := store.InvalidateSession(state.SessionID); err != closeErr {
		t.Fatalf("InvalidateSession error = %v, want %v", err, closeErr)
	}
	if !snapshot.closed || snapshot.closes != 1 {
		t.Fatalf("close state = closed %v closes %d, want closed once", snapshot.closed, snapshot.closes)
	}
	if _, ok := store.Get(state.StateID); ok {
		t.Fatalf("state still present after InvalidateSession")
	}
	if _, ok := store.GetForApp("com.example.app"); ok {
		t.Fatalf("session still present after InvalidateSession")
	}
	if err := store.InvalidateSession(state.SessionID); err != nil {
		t.Fatalf("InvalidateSession missing session error = %v, want nil", err)
	}
	if snapshot.closes != 1 {
		t.Fatalf("snapshot closed %d times, want once", snapshot.closes)
	}
}

func TestStaleStateError(t *testing.T) {
	tests := []struct {
		name    string
		stateID string
		want    string
	}{
		{name: "missing", want: "missing state_id"},
		{name: "stale", stateID: "old", want: `unknown or stale state_id "old"`},
	}
	for _, tt := range tests {
		err := StaleStateError(tt.stateID)
		if err == nil {
			t.Fatalf("%s: StaleStateError = nil", tt.name)
		}
		got := err.Error()
		if !strings.Contains(got, tt.want) || !strings.Contains(got, "retry with the fresh state_id") {
			t.Fatalf("%s: StaleStateError = %q", tt.name, got)
		}
	}
}

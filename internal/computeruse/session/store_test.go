package session

import (
	"fmt"
	"testing"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

type fakeSnapshot struct {
	state  computeruse.AppState
	nodes  map[int]computeruse.ElementNode
	closed bool
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
	return nil
}

func TestBindReplacesPriorState(t *testing.T) {
	store := NewStore()
	first := &fakeSnapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{BundleID: "com.example.app"},
		},
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
		t.Fatalf("Bind(second): %v", err)
	}
	if !first.closed {
		t.Fatalf("first snapshot should be closed after replacement")
	}
	if firstState.StateID == secondState.StateID {
		t.Fatalf("StateID should change across bindings")
	}
	if _, err := store.Acquire(firstState.StateID); err == nil {
		t.Fatalf("old state_id should be stale")
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
	lease, err := store.Acquire(state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	_, node, err := lease.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if node.Title != "Play" {
		t.Fatalf("Resolve title = %q, want Play", node.Title)
	}
}

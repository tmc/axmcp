package session_test

import (
	"fmt"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

// exampleSnapshot supplies metadata without opening a native application.
type exampleSnapshot struct{}

func (exampleSnapshot) State() computeruse.AppState {
	return computeruse.AppState{App: computeruse.AppInfo{BundleID: "com.example.app"}}
}
func (exampleSnapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	return nil, computeruse.ElementNode{Index: index, Title: "Save"}, nil
}
func (exampleSnapshot) Close() error { return nil }

func ExampleStore_Acquire() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	lease, _ := store.Acquire(state.StateID)
	defer lease.Close()
	fmt.Println(lease.State().App.BundleID)
	// Output: com.example.app
}

func ExampleStore_Take() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	lease, _ := store.Take(state.StateID)
	defer lease.Close()
	_, err := store.Take(state.StateID)
	fmt.Println(err != nil)
	// Output: true
}

func ExampleLease_State() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	lease, _ := store.Acquire(state.StateID)
	defer lease.Close()
	fmt.Println(lease.State().SessionID)
	// Output: bundle:com.example.app
}

func ExampleLease_Resolve() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	lease, _ := store.Acquire(state.StateID)
	defer lease.Close()
	_, node, _ := lease.Resolve(1)
	fmt.Println(node.Title)
	// Output: Save
}

func ExampleLease_Close() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	lease, _ := store.Take(state.StateID)
	fmt.Println(lease.Close())
	fmt.Println(lease.Close())
	// Output:
	// <nil>
	// <nil>
}

func ExampleLease_Retain() {
	store := session.NewStore()
	defer store.Close()
	state, _ := store.Bind(exampleSnapshot{})
	original, _ := store.Take(state.StateID)
	retained, _ := original.Retain()
	original.Close()
	defer retained.Close()
	fmt.Println(retained.State().App.BundleID)
	// Output: com.example.app
}

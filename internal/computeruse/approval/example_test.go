package approval_test

import (
	"context"
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/approval"
)

func Example() {
	store := approval.NewMemory()
	state, err := store.Approve(context.Background(), "com.example.editor", false)
	fmt.Println(state.Approved, err)
	// Output: true <nil>
}

func ExampleStore_Status() {
	state, err := approval.NewMemory().Status(context.Background(), "com.example.editor")
	fmt.Println(state.Required, err)
	// Output: true <nil>
}

func ExampleStore_Resolve() {
	state, err := approval.NewMemory().Resolve(context.Background(), "com.example.editor", computeruse.ApprovalDecisionRequire)
	fmt.Println(state.Approved, err)
	// Output: false <nil>
}

func ExampleStore_Approve() {
	state, err := approval.NewMemory().Approve(context.Background(), "com.example.editor", false)
	fmt.Println(state.Approved, state.Persistent, err)
	// Output: true false <nil>
}

func ExampleStore_Revoke() {
	ctx := context.Background()
	store := approval.NewMemory()
	store.Approve(ctx, "com.example.editor", false)
	err := store.Revoke(ctx, "com.example.editor")
	state, statusErr := store.Status(ctx, "com.example.editor")
	fmt.Println(state.Approved, err, statusErr)
	// Output: false <nil> <nil>
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/approval"
)

type revokeTestBackend struct {
	*nativeTestBackend
	approvals *approval.Store
	resolves  int
	fail      bool
}

func (b *revokeTestBackend) Resolve(context.Context, string) (computeruse.AppInfo, error) {
	b.resolves++
	return computeruse.AppInfo{}, fmt.Errorf("app stopped")
}
func (b *revokeTestBackend) RevokeApproval(ctx context.Context, id string) error {
	if b.fail {
		return fmt.Errorf("write failed; commit uncertain")
	}
	return b.approvals.Revoke(ctx, id)
}

func TestNativeRevokeApprovalMCP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := approval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := approval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Approve(t.Context(), "test.fixture", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(t.Context(), "test.fixture", true); err != nil {
		t.Fatal(err)
	}
	_, base := newNativeTestRunner(t)
	b := &revokeTestBackend{nativeTestBackend: base, approvals: store}
	r := newNativeRunner(b)
	defer r.close()
	snap := &nativeTestSnapshot{fakeActionSnapshot: fakeActionSnapshot{state: computeruse.AppState{App: base.target.App, Window: base.target.Window}}}
	if _, err := r.publish(snap, base.target, computeruse.PermissionState{}, computeruse.ApprovalState{Approved: true}); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "revoke-test", Version: "1"}, nil)
	registerNativeRevokeTool(server, r)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	prompts := 0
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompts++
		return nil, fmt.Errorf("must not prompt")
	}})
	cs, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_revoke_approval", Arguments: nativeRevokeApprovalInput{BundleID: " Test.Fixture "}})
	if err != nil || result.IsError {
		t.Fatalf("revoke: %+v, %v", result, err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out nativeRevokeApprovalOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Revoked || out.BundleID != "test.fixture" || out.ErrorText != "" {
		t.Fatalf("result: %+v", out)
	}
	if state, err := reader.Status(t.Context(), "test.fixture"); err != nil || state.Approved {
		t.Fatalf("independent store retains approval: %+v %v", state, err)
	}
	if r.observation != nil || snap.closed != 1 {
		t.Fatalf("local observation retained: %+v closes=%d", r.observation, snap.closed)
	}
	if prompts != 0 || b.resolves != 0 || b.calls != 0 || len(b.snapshots) != 0 {
		t.Fatal("revocation prompted, resolved, captured or dispatched")
	}
}

func TestNativeRevokeFailure(t *testing.T) {
	_, base := newNativeTestRunner(t)
	b := &revokeTestBackend{nativeTestBackend: base, fail: true}
	r := newNativeRunner(b)
	defer r.close()
	out, err := r.revokeApproval(t.Context(), nativeRevokeApprovalInput{BundleID: "test.fixture"})
	if err != nil || out.Revoked || out.ErrorText == "" {
		t.Fatalf("uncertain result: %+v %v", out, err)
	}
	for _, in := range []nativeRevokeApprovalInput{{}, {BundleID: "test.fixture", TimeoutMS: -1}} {
		if out, err := r.revokeApproval(t.Context(), in); err == nil || out.Revoked {
			t.Fatalf("invalid input: %+v %v", out, err)
		}
	}
	unsupported := newNativeRunner(base)
	defer unsupported.close()
	if out, err := unsupported.revokeApproval(t.Context(), nativeRevokeApprovalInput{BundleID: "test.fixture"}); err != nil || out.Revoked || out.ErrorText == "" {
		t.Fatalf("unsupported: %+v %v", out, err)
	}
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/approval"
)

type nativeApprovalTestBackend struct {
	*nativeTestBackend
	rt *runtimeState
}

func (b *nativeApprovalTestBackend) Authorize(ctx context.Context, req *mcp.CallToolRequest, app computeruse.AppInfo, observe bool) (computeruse.PermissionState, computeruse.ApprovalState, error) {
	state := b.rt.approvals.Status(app.BundleID)
	if state.Approved || !observe {
		return computeruse.PermissionState{}, state, nil
	}
	state, err := elicitApproval(ctx, req, b.rt, app)
	return computeruse.PermissionState{}, state, err
}

func TestNativeRequestApproval(t *testing.T) {
	for _, decision := range []string{"accept", "decline", "cancel", "unsupported"} {
		t.Run(decision, func(t *testing.T) {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp(filepath.Join(home, "tmp"), "native-approval-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "approvals.json")
			store, err := approval.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			b := &nativeApprovalTestBackend{nativeTestBackend: &nativeTestBackend{target: nativeTarget{App: computeruse.AppInfo{PID: 42, Name: "Fixture", BundleID: "test.fixture"}}}, rt: &runtimeState{approvals: store}}
			r := newNativeRunner(b)
			defer r.close()
			server := mcp.NewServer(&mcp.Implementation{Name: "approval-test", Version: "1"}, nil)
			registerNativeApprovalTool(server, r)
			a, z := mcp.NewInMemoryTransports()
			ss, err := server.Connect(t.Context(), a, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			calls := 0
			var handler func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)
			if decision != "unsupported" {
				handler = func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					calls++
					if !strings.Contains(req.Params.Message, "Fixture (test.fixture)") || !strings.Contains(req.Params.Message, "future sessions") {
						t.Errorf("wrong prompt: %s", req.Params.Message)
					}
					return &mcp.ElicitResult{Action: decision, Content: map[string]any{}}, nil
				}
			}
			client := mcp.NewClient(&mcp.Implementation{Name: "approval-test", Version: "1"}, &mcp.ClientOptions{ElicitationHandler: handler})
			cs, err := client.Connect(t.Context(), z, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_request_approval", Arguments: nativeRequestApprovalInput{App: "Fixture"}})
			if err != nil || result.IsError {
				t.Fatalf("request=%+v %v", result, err)
			}
			if b.calls != 0 || len(b.snapshots) != 0 {
				t.Fatal("approval captured or acted on a window")
			}
			reloaded, err := approval.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			want := decision == "accept"
			if got := reloaded.Status("test.fixture"); got.Approved != want || got.Persistent != want {
				t.Fatalf("persisted state=%+v", got)
			}
			if !want && store.Status("test.fixture").Approved {
				t.Fatal("negative decision approved app")
			}
			if decision != "unsupported" && calls != 1 {
				t.Fatalf("prompt count=%d", calls)
			}
			if want {
				_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_request_approval", Arguments: nativeRequestApprovalInput{App: "Fixture"}})
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal("already-approved app prompted again")
				}
			}
		})
	}
}

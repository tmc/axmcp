package approval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type processRequest struct{ Operation, BundleID string }
type processResponse struct {
	Ready, Approved, Persistent bool
	Error                       string
}

func TestApprovalStoreProcessChild(t *testing.T) {
	path := os.Getenv("AXMCP_APPROVAL_PROCESS_TEST_PATH")
	if path == "" {
		return
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, dec := json.NewEncoder(os.Stdout), json.NewDecoder(os.Stdin)
	if err := enc.Encode(processResponse{Ready: true}); err != nil {
		t.Fatal(err)
	}
	for {
		var request processRequest
		err := dec.Decode(&request)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var response processResponse
		switch request.Operation {
		case "persistent", "session":
			state, err := store.Approve(t.Context(), request.BundleID, request.Operation == "persistent")
			response.Approved, response.Persistent = state.Approved, state.Persistent
			if err != nil {
				response.Error = err.Error()
			}
		case "status":
			state, err := store.Status(t.Context(), request.BundleID)
			response.Approved, response.Persistent = state.Approved, state.Persistent
			if err != nil {
				response.Error = err.Error()
			}
		case "revoke":
			if err := store.Revoke(t.Context(), request.BundleID); err != nil {
				response.Error = err.Error()
			}
		default:
			t.Fatalf("unknown operation %q", request.Operation)
		}
		if err := enc.Encode(response); err != nil {
			t.Fatal(err)
		}
	}
}

func approvalProcess(t *testing.T, ctx context.Context, path string) func(string, string) processResponse {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalStoreProcessChild$")
	cmd.Env = append(os.Environ(), "AXMCP_APPROVAL_PROCESS_TEST_PATH="+path)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("approval child: %v", err)
		}
	})
	enc, dec := json.NewEncoder(stdin), json.NewDecoder(stdout)
	var ready processResponse
	if err := dec.Decode(&ready); err != nil || !ready.Ready {
		t.Fatalf("child ready: %+v, %v", ready, err)
	}
	return func(operation, id string) processResponse {
		t.Helper()
		if err := enc.Encode(processRequest{operation, id}); err != nil {
			t.Fatal(err)
		}
		var response processResponse
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
}

func TestIndependentProcessesPreserveGrants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	path := filepath.Join(t.TempDir(), "approvals.json")
	a, b := approvalProcess(t, ctx, path), approvalProcess(t, ctx, path)
	for i, peer := range []func(string, string) processResponse{a, b} {
		id := []string{"test.first", "test.second"}[i]
		if got := peer("persistent", id); got.Error != "" || !got.Approved {
			t.Fatalf("grant: %+v", got)
		}
	}
	for _, peer := range []func(string, string) processResponse{a, b} {
		for _, id := range []string{"test.first", "test.second"} {
			if got := peer("status", id); got.Error != "" || !got.Approved || !got.Persistent {
				t.Fatalf("merged %s: %+v", id, got)
			}
		}
	}
}

func TestIndependentProcessesRevoke(t *testing.T) {
	for _, kind := range []string{"persistent", "session"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.Cleanup(cancel)
			path := filepath.Join(t.TempDir(), "approvals.json")
			a, b := approvalProcess(t, ctx, path), approvalProcess(t, ctx, path)
			if got := a(kind, "test.target"); got.Error != "" || !got.Approved {
				t.Fatalf("grant: %+v", got)
			}
			if got := b("persistent", "test.other"); got.Error != "" || !got.Approved {
				t.Fatalf("unrelated grant: %+v", got)
			}
			if got := b("revoke", "test.target"); got.Error != "" {
				t.Fatalf("revoke: %+v", got)
			}
			if got := a("status", "test.target"); got.Error != "" || got.Approved {
				t.Fatalf("stale grant survived: %+v", got)
			}
			if got := a("status", "test.other"); got.Error != "" || !got.Approved {
				t.Fatalf("unrelated grant lost: %+v", got)
			}
			if got := a(kind, "test.target"); got.Error != "" || !got.Approved {
				t.Fatalf("explicit reapproval: %+v", got)
			}
		})
	}
}

func TestIndependentProcessCorruptAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	path := filepath.Join(t.TempDir(), "approvals.json")
	peer := approvalProcess(t, ctx, path)
	if got := peer("session", "test.target"); got.Error != "" || !got.Approved {
		t.Fatalf("grant: %+v", got)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := peer("status", "test.target"); got.Error == "" || got.Approved {
		t.Fatalf("corruption: %+v", got)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := peer("status", "test.target"); got.Error != "" || got.Approved {
		t.Fatalf("session resurrected: %+v", got)
	}
}

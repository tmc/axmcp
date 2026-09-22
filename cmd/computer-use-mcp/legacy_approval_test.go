package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/approval"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

func TestLegacyApprovalRevokeChild(t *testing.T) {
	path := os.Getenv("AXMCP_LEGACY_REVOKE_TEST_PATH")
	if path == "" {
		return
	}
	store, err := approval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(t.Context(), "test.fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyActionRejectsCrossProcessRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := approval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(t.Context(), "test.fixture", true); err != nil {
		t.Fatal(err)
	}
	rt := &runtimeState{sessions: session.NewStore(), approvals: store}
	defer rt.sessions.Close()
	snapshot := &leasedActionSnapshot{state: computeruse.AppState{App: computeruse.AppInfo{Name: "Fixture", BundleID: "test.fixture", PID: 123}}}
	state, err := rt.sessions.Bind(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// A current grant must permit the same state before the other process revokes.
	lease, err := stateForAction(t.Context(), rt, "click", "Fixture", state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLegacyApprovalRevokeChild$")
	cmd.Env = append(os.Environ(), "AXMCP_LEGACY_REVOKE_TEST_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("revoke process: %v\n%s", err, out)
	}
	lease, err = stateForAction(t.Context(), rt, "click", "Fixture", state.StateID)
	if err == nil {
		lease.Close()
		t.Fatal("retained state admitted an action after cross-process revoke")
	}
	if !strings.Contains(err.Error(), "approval required") {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if err := rt.sessions.InvalidateSession(state.SessionID); err != nil {
		t.Fatal(err)
	}
	if !snapshot.closed {
		t.Fatal("rejection retained the snapshot lease")
	}
}

func TestLegacyActionRejectsUnreadableAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := approval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(t.Context(), "test.fixture", false); err != nil {
		t.Fatal(err)
	}
	rt := &runtimeState{sessions: session.NewStore(), approvals: store}
	defer rt.sessions.Close()
	state, err := rt.sessions.Bind(fakeActionSnapshot{state: computeruse.AppState{App: computeruse.AppInfo{Name: "Fixture", BundleID: "test.fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lease, err := stateForAction(t.Context(), rt, "click", "Fixture", state.StateID); err == nil {
		lease.Close()
		t.Fatal("action admitted with corrupt authority")
	}
}

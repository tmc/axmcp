package approval

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestMemoryStatus(t *testing.T) {
	store := NewMemory()

	state := store.Status("com.apple.Music")
	checkState(t, state, computeruse.ApprovalOutcomeRequired, true, false, false)

	state, err := store.Resolve("com.apple.Music", computeruse.ApprovalDecisionRequire)
	if err != nil {
		t.Fatalf("Resolve(..., require): %v", err)
	}
	checkState(t, state, computeruse.ApprovalOutcomeRequired, true, false, false)
}

func TestApproveSession(t *testing.T) {
	store := NewMemory()

	state, err := store.Approve("com.apple.Music", false)
	if err != nil {
		t.Fatalf("Approve(..., false): %v", err)
	}
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, false)

	state = store.Status("com.apple.music")
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, false)
}

func TestResolveUpgradesSessionApprovalToPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}

	state, err := store.Resolve("com.apple.Music", computeruse.ApprovalDecisionApprove)
	if err != nil {
		t.Fatalf("Resolve(..., approve): %v", err)
	}
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, false)

	state, err = store.Resolve("com.apple.Music", computeruse.ApprovalDecisionApprovePersistent)
	if err != nil {
		t.Fatalf("Resolve(..., approve_persistent): %v", err)
	}
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, true)

	loaded, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) after save: %v", path, err)
	}
	state = loaded.Status("com.apple.music")
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, true)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var file struct {
		Version   int `json:"version"`
		Approvals map[string]struct {
			ApprovedAt string `json:"approved_at"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", path, err)
	}
	if file.Version != 1 {
		t.Fatalf("version = %d, want 1", file.Version)
	}
	if _, ok := file.Approvals["com.apple.music"]; !ok {
		t.Fatal("persistent approvals missing normalized bundle id")
	}
}

func TestApprovePersistentFallsBackToSession(t *testing.T) {
	store := NewMemory()

	state, err := store.Approve("com.apple.Music", true)
	if !errors.Is(err, ErrApprovalPersistenceFailed) {
		t.Fatalf("Approve(..., true) error = %v, want %v", err, ErrApprovalPersistenceFailed)
	}
	checkState(t, state, computeruse.ApprovalOutcomePersistenceFailed, false, true, false)

	state = store.Status("com.apple.music")
	checkState(t, state, computeruse.ApprovalOutcomeApproved, false, true, false)
}

func TestResolveDeniedAndCanceled(t *testing.T) {
	tests := []struct {
		name        string
		decision    computeruse.ApprovalDecision
		wantErr     error
		wantOutcome computeruse.ApprovalOutcome
	}{
		{
			name:        "deny",
			decision:    computeruse.ApprovalDecisionDeny,
			wantErr:     ErrApprovalDenied,
			wantOutcome: computeruse.ApprovalOutcomeDenied,
		},
		{
			name:        "cancel",
			decision:    computeruse.ApprovalDecisionCancel,
			wantErr:     ErrApprovalCanceled,
			wantOutcome: computeruse.ApprovalOutcomeCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemory()

			state, err := store.Resolve("com.apple.Music", tt.decision)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Resolve(..., %q) error = %v, want %v", tt.decision, err, tt.wantErr)
			}
			checkState(t, state, tt.wantOutcome, true, false, false)

			state = store.Status("com.apple.music")
			checkState(t, state, computeruse.ApprovalOutcomeRequired, true, false, false)
		})
	}
}

func TestApproveRejectsEmptyBundleID(t *testing.T) {
	store := NewMemory()

	state, err := store.Approve("   ", false)
	if !errors.Is(err, ErrBundleIDRequired) {
		t.Fatalf("Approve(empty, false) error = %v, want %v", err, ErrBundleIDRequired)
	}
	checkState(t, state, computeruse.ApprovalOutcomeRequired, true, false, false)
}

func checkState(t *testing.T, got computeruse.ApprovalState, wantOutcome computeruse.ApprovalOutcome, wantRequired, wantApproved, wantPersistent bool) {
	t.Helper()

	if got.Outcome != wantOutcome {
		t.Fatalf("Outcome = %q, want %q", got.Outcome, wantOutcome)
	}
	if got.Required != wantRequired {
		t.Fatalf("Required = %v, want %v", got.Required, wantRequired)
	}
	if got.Approved != wantApproved {
		t.Fatalf("Approved = %v, want %v", got.Approved, wantApproved)
	}
	if got.Persistent != wantPersistent {
		t.Fatalf("Persistent = %v, want %v", got.Persistent, wantPersistent)
	}
}

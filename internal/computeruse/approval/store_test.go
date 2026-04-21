package approval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryStatus(t *testing.T) {
	store := NewMemory()

	state := store.Status("com.apple.Music")
	if !state.Required {
		t.Fatal("Status().Required = false, want true")
	}
	if state.Approved {
		t.Fatal("Status().Approved = true, want false")
	}
	if state.Persistent {
		t.Fatal("Status().Persistent = true, want false")
	}
}

func TestApproveSession(t *testing.T) {
	store := NewMemory()

	state, err := store.Approve("com.apple.Music", false)
	if err != nil {
		t.Fatalf("Approve(..., false): %v", err)
	}
	if !state.Approved {
		t.Fatal("Approve(..., false).Approved = false, want true")
	}
	if state.Persistent {
		t.Fatal("Approve(..., false).Persistent = true, want false")
	}

	state = store.Status("com.apple.music")
	if !state.Approved {
		t.Fatal("Status().Approved = false, want true")
	}
	if state.Persistent {
		t.Fatal("Status().Persistent = true, want false")
	}
	if state.Required {
		t.Fatal("Status().Required = true, want false")
	}
}

func TestApprovePersistentReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}

	state, err := store.Approve("com.apple.Music", true)
	if err != nil {
		t.Fatalf("Approve(..., true): %v", err)
	}
	if !state.Approved || !state.Persistent {
		t.Fatalf("Approve(..., true) = %+v, want approved persistent", state)
	}

	loaded, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) after save: %v", path, err)
	}
	state = loaded.Status("com.apple.music")
	if !state.Approved || !state.Persistent {
		t.Fatalf("Status() after reload = %+v, want approved persistent", state)
	}

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
	if err == nil {
		t.Fatal("Approve(..., true) error = nil, want error")
	}
	if !state.Approved {
		t.Fatal("Approve(..., true).Approved = false, want true")
	}
	if state.Persistent {
		t.Fatal("Approve(..., true).Persistent = true, want false")
	}

	state = store.Status("com.apple.music")
	if !state.Approved {
		t.Fatal("Status().Approved = false, want true")
	}
	if state.Persistent {
		t.Fatal("Status().Persistent = true, want false")
	}
}

func TestApproveRejectsEmptyBundleID(t *testing.T) {
	store := NewMemory()

	state, err := store.Approve("   ", false)
	if err == nil {
		t.Fatal("Approve(empty, false) error = nil, want error")
	}
	if state.Approved {
		t.Fatal("Approve(empty, false).Approved = true, want false")
	}
	if !state.Required {
		t.Fatal("Approve(empty, false).Required = false, want true")
	}
}

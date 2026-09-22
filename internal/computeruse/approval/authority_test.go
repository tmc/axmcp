package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorityMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	old := []byte(`{"version":1,"approvals":{"test.old":{"approved_at":"2026-01-01T00:00:00Z"}}}`)
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Status(t.Context(), "test.old"); err != nil || !got.Approved {
		t.Fatalf("legacy grant: %+v, %v", got, err)
	}
	if _, err := s.Approve(t.Context(), "test.new", false); err != nil {
		t.Fatal(err)
	}
	f, err := readAuthority(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 || f.Epoch == "" || f.Revision != 1 {
		t.Fatalf("migration: %+v", f)
	}
	if _, ok := f.Approvals["test.old"]; !ok {
		t.Fatal("legacy grant lost")
	}
	if _, ok := f.Approvals["test.new"]; ok {
		t.Fatal("session grant persisted")
	}
}

func TestAuthorityRejectsInvalidFiles(t *testing.T) {
	for _, data := range []string{"", "{", "null", `{"version":3}`, `{"version":2}`, `{"version":2,"epoch":"e","revision":1,"revocations":{"test.app":2}}`} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "approvals.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("accepted invalid authority")
			}
		})
	}
}

func TestAuthorityResetAndRollback(t *testing.T) {
	for _, kind := range []string{"missing", "new_epoch", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "approvals.json")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(t.Context(), "test.app", false); err != nil {
				t.Fatal(err)
			}
			f, err := readAuthority(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "new_epoch" {
					f.Epoch = "replacement"
				} else {
					f.Revision = 0
				}
				data, err := encodeAuthority(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			state, err := s.Status(t.Context(), "test.app")
			if state.Approved {
				t.Fatalf("stale grant: %+v", state)
			}
			if (err != nil) != (kind == "rollback") {
				t.Fatalf("%s error: %v", kind, err)
			}
		})
	}
}

func TestAuthorityRevisionOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	data, err := json.Marshal(approvalFile{Version: 2, Epoch: "test", Revision: math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := s.Approve(t.Context(), "test.app", true); err == nil || state.Approved {
		t.Fatalf("overflow: %+v, %v", state, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("overflow changed authority: %s, %v", after, err)
	}
}

func TestStoreCancellationAndZeroValue(t *testing.T) {
	var zero Store
	if state, err := zero.Status(t.Context(), "test.app"); err == nil || state.Approved {
		t.Fatalf("zero value: %+v, %v", state, err)
	}
	s := NewMemory()
	if err := s.enter(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	state, err := s.Approve(ctx, "test.app", false)
	s.leave()
	if !errors.Is(err, context.DeadlineExceeded) || state.Approved {
		t.Fatalf("queued grant: %+v, %v", state, err)
	}
	if state, err := s.Status(t.Context(), "test.app"); err != nil || state.Approved {
		t.Fatalf("canceled grant wrote: %+v, %v", state, err)
	}
}

func TestFailedRevokeBlocksLocally(t *testing.T) {
	s := NewMemory()
	if _, err := s.Approve(t.Context(), "test.app", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Revoke(ctx, "test.app"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state, err := s.Status(t.Context(), "test.app"); err != nil || state.Approved {
		t.Fatalf("failed revoke: %+v, %v", state, err)
	}
	if state, err := s.Approve(t.Context(), "test.app", false); err != nil || !state.Approved {
		t.Fatalf("reapproval: %+v, %v", state, err)
	}
}

func TestFileBackedCanceledMutationDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockAuthority(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	state, err := store.Approve(ctx, "test.app", true)
	if !errors.Is(err, context.DeadlineExceeded) || state.Approved {
		t.Fatalf("blocked mutation: %+v, %v", state, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled mutation wrote authority: %v", err)
	}
}

func TestStoreWriteFailures(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "approvals.json")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("write boundary fault")
			store.replace = func(path string, data []byte) (bool, error) {
				if committed {
					if ok, err := replaceAuthority(path, data); !ok || err != nil {
						return ok, err
					}
				}
				return committed, fault
			}
			state, err := store.Approve(t.Context(), "test.app", true)
			if state.Approved || !errors.Is(err, ErrApprovalPersistenceFailed) || !errors.Is(err, fault) {
				t.Fatalf("write result: %+v, %v", state, err)
			}
			current, err := store.Status(t.Context(), "test.app")
			if err != nil || current.Approved != committed {
				t.Fatalf("visible authority: %+v, %v", current, err)
			}
			if len(store.session) != 0 {
				t.Fatal("failed persistent write minted session grant")
			}
		})
	}
}

func TestFailedUpgradePreservesExplicitSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(t.Context(), "test.app", false); err != nil {
		t.Fatal(err)
	}
	store.replace = func(string, []byte) (bool, error) { return false, errors.New("write fault") }
	if state, err := store.Approve(t.Context(), "test.app", true); err == nil || state.Approved {
		t.Fatalf("upgrade: %+v, %v", state, err)
	}
	if state, err := store.Status(t.Context(), "test.app"); err != nil || !state.Approved || state.Persistent {
		t.Fatalf("existing explicit session: %+v, %v", state, err)
	}
}

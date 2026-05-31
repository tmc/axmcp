//go:build !darwin

package session

import (
	"fmt"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

type Snapshot interface {
	State() computeruse.AppState
	Close() error
}

type Store struct{}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Bind(snapshot Snapshot) (computeruse.AppState, error) {
	if snapshot != nil {
		_ = snapshot.Close()
	}
	return computeruse.AppState{}, computeruse.PlatformUnsupported("bind computer-use session")
}

func (s *Store) Get(string) (computeruse.AppState, bool) {
	return computeruse.AppState{}, false
}

func (s *Store) GetForApp(string) (computeruse.AppState, bool) {
	return computeruse.AppState{}, false
}

func (s *Store) Snapshot(stateID string) (Snapshot, error) {
	return nil, StaleStateError(stateID)
}

func StaleStateError(stateID string) error {
	stateID = strings.TrimSpace(stateID)
	if stateID == "" {
		return fmt.Errorf("missing state_id; call get_app_state again and retry with the fresh state_id")
	}
	return fmt.Errorf("unknown or stale state_id %q; call get_app_state again and retry with the fresh state_id", stateID)
}

func (s *Store) InvalidateSession(string) error {
	return nil
}

func (s *Store) Close() error {
	return nil
}

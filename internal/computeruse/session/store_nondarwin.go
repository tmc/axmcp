//go:build !darwin

package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/tmc/axmcp/internal/computeruse"
)

type Snapshot interface {
	State() computeruse.AppState
	Close() error
}

type entry struct {
	sessionID string
	stateID   string
	key       string
	state     computeruse.AppState
	snapshot  Snapshot
}

type Store struct {
	mu        sync.Mutex
	bySession map[string]*entry
	byStateID map[string]*entry
}

func NewStore() *Store {
	return &Store{
		bySession: make(map[string]*entry),
		byStateID: make(map[string]*entry),
	}
}

func (s *Store) Bind(snapshot Snapshot) (computeruse.AppState, error) {
	if snapshot == nil {
		return computeruse.AppState{}, fmt.Errorf("nil snapshot")
	}
	state := snapshot.State()
	key := sessionKey(state.App)
	if key == "" {
		_ = snapshot.Close()
		return computeruse.AppState{}, fmt.Errorf("missing app identity")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := key
	stateID, err := newID()
	if err != nil {
		_ = snapshot.Close()
		return computeruse.AppState{}, err
	}
	next := &entry{
		sessionID: sessionID,
		stateID:   stateID,
		key:       key,
		state:     state,
		snapshot:  snapshot,
	}
	next.state.SessionID = sessionID
	next.state.StateID = stateID

	if prev := s.bySession[sessionID]; prev != nil {
		delete(s.byStateID, prev.stateID)
		_ = prev.snapshot.Close()
	}
	s.bySession[sessionID] = next
	s.byStateID[stateID] = next
	return next.state, nil
}

func (s *Store) Get(stateID string) (computeruse.AppState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byStateID[stateID]
	if entry == nil {
		return computeruse.AppState{}, false
	}
	return entry.state, true
}

func (s *Store) GetForApp(selector string) (computeruse.AppState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.findLocked(selector)
	if entry == nil {
		return computeruse.AppState{}, false
	}
	return entry.state, true
}

func (s *Store) Snapshot(stateID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byStateID[stateID]
	if entry == nil {
		return nil, StaleStateError(stateID)
	}
	return entry.snapshot, nil
}

func StaleStateError(stateID string) error {
	stateID = strings.TrimSpace(stateID)
	if stateID == "" {
		return fmt.Errorf("missing state_id; call get_app_state again and retry with the fresh state_id")
	}
	return fmt.Errorf("unknown or stale state_id %q; call get_app_state again and retry with the fresh state_id", stateID)
}

func (s *Store) InvalidateSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.bySession[sessionID]
	if entry == nil {
		return nil
	}
	delete(s.bySession, sessionID)
	delete(s.byStateID, entry.stateID)
	return entry.snapshot.Close()
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for sessionID, entry := range s.bySession {
		delete(s.byStateID, entry.stateID)
		delete(s.bySession, sessionID)
		if err := entry.snapshot.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) findLocked(selector string) *entry {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	if entry := s.bySession["pid:"+selector]; entry != nil {
		return entry
	}
	if entry := s.bySession["name:"+strings.ToLower(selector)]; entry != nil {
		return entry
	}
	want := strings.ToLower(selector)
	for _, entry := range s.bySession {
		app := entry.state.App
		switch {
		case strings.EqualFold(app.Name, selector):
			return entry
		case fmt.Sprintf("%d", app.PID) == selector:
			return entry
		case strings.Contains(strings.ToLower(app.Name), want):
			return entry
		}
	}
	return nil
}

func sessionKey(app computeruse.AppInfo) string {
	switch {
	case app.PID > 0:
		return fmt.Sprintf("pid:%d", app.PID)
	case app.Name != "":
		return "name:" + strings.ToLower(strings.TrimSpace(app.Name))
	default:
		return ""
	}
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate state id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

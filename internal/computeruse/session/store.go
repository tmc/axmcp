package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

// Snapshot is the live state handle stored for later actions.
type Snapshot interface {
	State() computeruse.AppState
	Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error)
	Close() error
}

type entry struct {
	sessionID string
	stateID   string
	key       string
	state     computeruse.AppState
	snapshot  Snapshot
}

// Store keeps the latest live state per app session.
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

func (s *Store) Resolve(stateID string, index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	s.mu.Lock()
	entry := s.byStateID[stateID]
	if entry == nil {
		s.mu.Unlock()
		return nil, computeruse.ElementNode{}, fmt.Errorf("unknown or stale state_id %q; call get_app_state again", stateID)
	}
	snapshot := entry.snapshot
	s.mu.Unlock()
	return snapshot.Resolve(index)
}

func (s *Store) ResolveForApp(selector string, index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	s.mu.Lock()
	entry := s.findLocked(selector)
	if entry == nil {
		s.mu.Unlock()
		return nil, computeruse.ElementNode{}, fmt.Errorf("no current app state for %q; call get_app_state again", selector)
	}
	snapshot := entry.snapshot
	s.mu.Unlock()
	return snapshot.Resolve(index)
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
	if entry := s.bySession["bundle:"+strings.ToLower(selector)]; entry != nil {
		return entry
	}
	if entry := s.bySession["name:"+strings.ToLower(selector)]; entry != nil {
		return entry
	}
	if entry := s.bySession["pid:"+selector]; entry != nil {
		return entry
	}
	want := strings.ToLower(selector)
	for _, entry := range s.bySession {
		app := entry.state.App
		switch {
		case strings.EqualFold(app.BundleID, selector):
			return entry
		case strings.EqualFold(app.Name, selector):
			return entry
		case fmt.Sprintf("%d", app.PID) == selector:
			return entry
		case strings.Contains(strings.ToLower(app.Name), want):
			return entry
		case strings.Contains(strings.ToLower(app.BundleID), want):
			return entry
		}
	}
	return nil
}

func sessionKey(app computeruse.AppInfo) string {
	switch {
	case app.BundleID != "":
		return "bundle:" + app.BundleID
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

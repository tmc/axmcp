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
	refs      int
}

// Store keeps the latest live state per app session.
type Store struct {
	mu        sync.Mutex
	closed    bool
	bySession map[string]*entry
	byStateID map[string]*entry
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		bySession: make(map[string]*entry),
		byStateID: make(map[string]*entry),
	}
}

// Bind owns snapshot, including when it returns an error. It replaces the
// previous observation for the app; active leases keep the old snapshot alive.
// Errors closing a replaced snapshot are discarded.
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
	stateID, err := newID()
	if err != nil {
		_ = snapshot.Close()
		return computeruse.AppState{}, err
	}
	next := &entry{sessionID: key, stateID: stateID, key: key, state: state, snapshot: snapshot, refs: 1}
	next.state.SessionID, next.state.StateID = key, stateID
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = snapshot.Close()
		return computeruse.AppState{}, fmt.Errorf("session store is closed")
	}
	var old Snapshot
	if prev := s.bySession[key]; prev != nil {
		delete(s.byStateID, prev.stateID)
		old = releaseEntry(prev)
	}
	s.bySession[key], s.byStateID[stateID] = next, next
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
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

// Acquire retains a snapshot until the returned lease is closed. It does not
// consume the state token or establish that the observed UI is still current.
func (s *Store) Acquire(stateID string) (*Lease, error) {
	return s.acquire(stateID, false)
}

// Take atomically consumes a state token and retains its snapshot until the
// returned lease is closed. At most one Take can succeed for a token.
func (s *Store) Take(stateID string) (*Lease, error) {
	return s.acquire(stateID, true)
}

func (s *Store) acquire(stateID string, consume bool) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("session store is closed")
	}
	e := s.byStateID[stateID]
	if e == nil {
		return nil, fmt.Errorf("unknown or stale state_id %q; call get_app_state again", stateID)
	}
	e.refs++
	if consume {
		delete(s.byStateID, e.stateID)
		delete(s.bySession, e.sessionID)
		releaseEntry(e) // The new lease retains the snapshot.
	}
	return &Lease{store: s, entry: e}, nil
}

// releaseEntry removes an owner, returning the snapshot only for the final
// release. The caller holds Store.mu and closes the returned snapshot unlocked.
func releaseEntry(e *entry) Snapshot {
	e.refs--
	if e.refs == 0 {
		return e.snapshot
	}
	return nil
}

// InvalidateSession removes its token. Existing leases remain alive until closed.
func (s *Store) InvalidateSession(sessionID string) error {
	s.mu.Lock()
	e := s.bySession[sessionID]
	if e == nil {
		s.mu.Unlock()
		return nil
	}
	delete(s.bySession, sessionID)
	delete(s.byStateID, e.stateID)
	snapshot := releaseEntry(e)
	s.mu.Unlock()
	if snapshot != nil {
		return snapshot.Close()
	}
	return nil
}

// Close invalidates all tokens and permanently closes the store. It does not
// wait for active leases. Their final Close reports deferred snapshot errors.
func (s *Store) Close() error {
	s.mu.Lock()
	s.closed = true
	var snapshots []Snapshot
	for sessionID, e := range s.bySession {
		delete(s.bySession, sessionID)
		delete(s.byStateID, e.stateID)
		if snapshot := releaseEntry(e); snapshot != nil {
			snapshots = append(snapshots, snapshot)
		}
	}
	s.mu.Unlock()
	var firstErr error
	for _, snapshot := range snapshots {
		if err := snapshot.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Lease keeps a snapshot and its AX handles alive for an action. Obtain one
// through Acquire or Take; its zero value is not usable. Do not copy a Lease.
// A lease guarantees lifetime, not UI freshness or permission to act.
type Lease struct {
	mu     sync.Mutex
	store  *Store
	entry  *entry
	closed bool
}

// State returns the captured metadata. It remains available after Close.
func (l *Lease) State() computeruse.AppState { return l.entry.state }

// Retain returns an independently owned lease for the same snapshot. It does
// not restore a consumed state token or establish freshness. Close the returned
// lease after its last use. Retaining a closed lease or a lease from a closed
// store returns an error. Retained handles must not be used concurrently unless
// the snapshot implementation permits it.
func (l *Lease) Retain() (*Lease, error) {
	if l == nil {
		return nil, fmt.Errorf("snapshot lease is unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.store == nil || l.entry == nil {
		return nil, fmt.Errorf("snapshot lease is closed or unavailable")
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if l.store.closed {
		return nil, fmt.Errorf("session store is closed")
	}
	l.entry.refs++
	return &Lease{store: l.store, entry: l.entry}, nil
}

// Resolve returns a handle owned by the lease. The caller must finish all use
// of the handle before closing the lease. Resolve after Close returns an error.
func (l *Lease) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, computeruse.ElementNode{}, fmt.Errorf("snapshot lease is closed")
	}
	return l.entry.snapshot.Resolve(index)
}

// Close releases the lease. Only the last owner closes the snapshot and receives
// its close error. Repeated calls do nothing and return nil. Do not overlap Close
// with use of a handle previously returned by Resolve.
func (l *Lease) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	l.store.mu.Lock()
	snapshot := releaseEntry(l.entry)
	l.store.mu.Unlock()
	if snapshot != nil {
		return snapshot.Close()
	}
	return nil
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

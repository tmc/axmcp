package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/axmcp/internal/computeruse"
)

const defaultFileName = "approvals.json"

// Store keeps approval state in memory and can persist it to disk.
type Store struct {
	mu         sync.RWMutex
	path       string
	session    map[string]struct{}
	persistent map[string]approvalRecord
}

type approvalFile struct {
	Version   int                       `json:"version"`
	UpdatedAt time.Time                 `json:"updated_at,omitempty"`
	Approvals map[string]approvalRecord `json:"approvals,omitempty"`
}

type approvalRecord struct {
	ApprovedAt time.Time `json:"approved_at,omitempty"`
}

var _ computeruse.ApprovalStore = (*Store)(nil)

// New returns a store backed by the default application-support path.
func New() (*Store, error) {
	return NewStore()
}

// NewStore returns a store backed by the default application-support path. If
// path is provided, it overrides the default location. An empty path disables
// persistence.
func NewStore(path ...string) (*Store, error) {
	switch len(path) {
	case 0:
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locate application support directory: %w", err)
		}
		return Open(filepath.Join(dir, "axmcp", "computer-use-mcp", defaultFileName))
	case 1:
		return Open(path[0])
	default:
		return nil, errors.New("too many paths")
	}
}

// Open returns a store backed by path. An empty path disables persistence.
func Open(path string) (*Store, error) {
	s := &Store{
		path:       path,
		session:    make(map[string]struct{}),
		persistent: make(map[string]approvalRecord),
	}
	if path == "" {
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewMemory returns a store that keeps all approvals in memory.
func NewMemory() *Store {
	return &Store{
		session:    make(map[string]struct{}),
		persistent: make(map[string]approvalRecord),
	}
}

// Path reports the configured persistence path. It is empty for memory-only
// stores.
func (s *Store) Path() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

// Status reports the current approval state for bundleID.
func (s *Store) Status(bundleID string) computeruse.ApprovalState {
	key := normalizeBundleID(bundleID)
	if key == "" {
		return approvalRequired("approval required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.persistent[key]; ok {
		return computeruse.ApprovalState{
			Approved:   true,
			Persistent: true,
			Message:    "approved persistently",
		}
	}
	if _, ok := s.session[key]; ok {
		return computeruse.ApprovalState{
			Approved: true,
			Message:  "approved for this session",
		}
	}
	return approvalRequired("approval required")
}

// Approve records approval for bundleID. Persistent approval is saved when the
// store has a configured backing file.
func (s *Store) Approve(bundleID string, persistent bool) (computeruse.ApprovalState, error) {
	key := normalizeBundleID(bundleID)
	if key == "" {
		return approvalRequired("approval required"), errors.New("bundle id required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if persistent {
		now := time.Now().UTC()
		s.persistent[key] = approvalRecord{ApprovedAt: now}
		delete(s.session, key)
		if err := s.saveLocked(); err != nil {
			s.session[key] = struct{}{}
			delete(s.persistent, key)
			return computeruse.ApprovalState{
				Approved: true,
				Message:  "approved for this session; could not persist approval",
			}, fmt.Errorf("persist approval: %w", err)
		}
		return computeruse.ApprovalState{
			Approved:   true,
			Persistent: true,
			Message:    "approved persistently",
		}, nil
	}

	s.session[key] = struct{}{}
	return computeruse.ApprovalState{
		Approved: true,
		Message:  "approved for this session",
	}, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read approvals: %w", err)
	}

	var file approvalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode approvals: %w", err)
	}
	for bundleID, record := range file.Approvals {
		key := normalizeBundleID(bundleID)
		if key == "" {
			continue
		}
		s.persistent[key] = record
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return errors.New("persistent approvals unavailable")
	}

	file := approvalFile{
		Version:   1,
		UpdatedAt: time.Now().UTC(),
		Approvals: make(map[string]approvalRecord, len(s.persistent)),
	}
	for bundleID, record := range s.persistent {
		file.Approvals[bundleID] = record
	}

	data, err := json.MarshalIndent(file, "", "\t")
	if err != nil {
		return fmt.Errorf("encode approvals: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create approvals directory: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write approvals: %w", err)
	}
	return nil
}

func approvalRequired(message string) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Required: true,
		Message:  message,
	}
}

func normalizeBundleID(bundleID string) string {
	return strings.TrimSpace(strings.ToLower(bundleID))
}

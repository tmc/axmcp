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

var (
	// ErrBundleIDRequired reports a missing bundle ID.
	ErrBundleIDRequired = errors.New("bundle id required")

	// ErrApprovalDenied reports an explicit denial.
	ErrApprovalDenied = errors.New("approval denied")

	// ErrApprovalCanceled reports a canceled approval request.
	ErrApprovalCanceled = errors.New("approval canceled")

	// ErrApprovalPersistenceFailed reports that approval succeeded for the
	// current session but could not be persisted.
	ErrApprovalPersistenceFailed = errors.New("approval persistence failed")
)

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

// Resolve reports or records approval for bundleID according to decision.
func (s *Store) Resolve(bundleID string, decision computeruse.ApprovalDecision) (computeruse.ApprovalState, error) {
	key := normalizeBundleID(bundleID)
	if key == "" {
		return approvalRequired("approval required"), ErrBundleIDRequired
	}

	if decision == "" {
		decision = computeruse.ApprovalDecisionRequire
	}

	state := s.status(key)
	if state.Approved {
		if decision == computeruse.ApprovalDecisionApprovePersistent && !state.Persistent {
			return s.persist(key)
		}
		return state, nil
	}

	switch decision {
	case computeruse.ApprovalDecisionRequire:
		return state, nil
	case computeruse.ApprovalDecisionApprove:
		return s.approveSession(key), nil
	case computeruse.ApprovalDecisionApprovePersistent:
		return s.persist(key)
	case computeruse.ApprovalDecisionDeny:
		return approvalDenied("approval denied"), ErrApprovalDenied
	case computeruse.ApprovalDecisionCancel:
		return approvalCanceled("approval canceled"), ErrApprovalCanceled
	default:
		return approvalRequired("approval required"), fmt.Errorf("unknown approval decision %q", decision)
	}
}

// Status reports the current approval state for bundleID.
func (s *Store) Status(bundleID string) computeruse.ApprovalState {
	key := normalizeBundleID(bundleID)
	if key == "" {
		return approvalRequired("approval required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked(key)
}

// Approve records approval for bundleID. Persistent approval is saved when the
// store has a configured backing file.
func (s *Store) Approve(bundleID string, persistent bool) (computeruse.ApprovalState, error) {
	if persistent {
		return s.Resolve(bundleID, computeruse.ApprovalDecisionApprovePersistent)
	}
	return s.Resolve(bundleID, computeruse.ApprovalDecisionApprove)
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

func (s *Store) approveSession(key string) computeruse.ApprovalState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session[key] = struct{}{}
	return approved("approved for this session", false)
}

func (s *Store) persist(key string) (computeruse.ApprovalState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.persistent[key]; ok {
		return approved("approved persistently", true), nil
	}

	s.persistent[key] = approvalRecord{ApprovedAt: time.Now().UTC()}
	delete(s.session, key)
	if err := s.saveLocked(); err != nil {
		s.session[key] = struct{}{}
		delete(s.persistent, key)
		return persistenceFailed("approved for this session; could not persist approval"), fmt.Errorf("%w: %v", ErrApprovalPersistenceFailed, err)
	}
	return approved("approved persistently", true), nil
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

func (s *Store) status(key string) computeruse.ApprovalState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked(key)
}

func (s *Store) statusLocked(key string) computeruse.ApprovalState {
	if _, ok := s.persistent[key]; ok {
		return approved("approved persistently", true)
	}
	if _, ok := s.session[key]; ok {
		return approved("approved for this session", false)
	}
	return approvalRequired("approval required")
}

func approved(message string, persistent bool) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Outcome:    computeruse.ApprovalOutcomeApproved,
		Approved:   true,
		Persistent: persistent,
		Message:    message,
	}
}

func approvalDenied(message string) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Outcome:  computeruse.ApprovalOutcomeDenied,
		Required: true,
		Message:  message,
	}
}

func approvalCanceled(message string) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Outcome:  computeruse.ApprovalOutcomeCanceled,
		Required: true,
		Message:  message,
	}
}

func approvalRequired(message string) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Outcome:  computeruse.ApprovalOutcomeRequired,
		Required: true,
		Message:  message,
	}
}

func persistenceFailed(message string) computeruse.ApprovalState {
	return computeruse.ApprovalState{
		Outcome:  computeruse.ApprovalOutcomePersistenceFailed,
		Approved: true,
		Message:  message,
	}
}

func normalizeBundleID(bundleID string) string {
	return strings.TrimSpace(strings.ToLower(bundleID))
}

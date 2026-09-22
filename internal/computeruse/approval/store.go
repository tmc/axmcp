package approval

import (
	"context"
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
	// ErrApprovalPersistenceFailed reports a failed or unconfirmed write. It does
	// not imply a session grant. A failure after rename may leave the change visible.
	ErrApprovalPersistenceFailed = errors.New("approval persistence failed")
)

// Store manages app-control approvals. A Store is safe for concurrent use.
// Its zero value is unusable; use Open, NewStore, or NewMemory.
type Store struct {
	replace  func(string, []byte) (bool, error)
	gate     chan struct{}
	path     string
	session  map[string]sessionRecord
	blocked  sync.Map
	epoch    string
	revision uint64
	memory   approvalFile
}

type sessionRecord struct {
	Epoch    string
	Revision uint64
}

type approvalFile struct {
	Version     int                       `json:"version"`
	Epoch       string                    `json:"epoch,omitempty"`
	Revision    uint64                    `json:"revision,omitempty"`
	UpdatedAt   time.Time                 `json:"updated_at,omitempty"`
	Approvals   map[string]approvalRecord `json:"approvals,omitempty"`
	Revocations map[string]uint64         `json:"revocations,omitempty"`
}

type approvalRecord struct {
	ApprovedAt time.Time `json:"approved_at,omitempty"`
}

var _ computeruse.ApprovalStore = (*Store)(nil)

// New returns a store backed by the default application-support path.
func New() (*Store, error) { return NewStore() }

// NewStore returns a store backed by the default application-support path.
// One optional path overrides the default; an empty path disables persistence.
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
// Existing version 1 records are preserved when the first decision migrates them.
func Open(path string) (*Store, error) {
	s := NewMemory()
	s.path = path
	if path != "" {
		file, err := readAuthority(path)
		if err != nil {
			return nil, err
		}
		if err := s.refresh(file); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// NewMemory returns a local session-only store.
func NewMemory() *Store {
	return &Store{replace: replaceAuthority, gate: make(chan struct{}, 1), session: make(map[string]sessionRecord),
		memory: approvalFile{Version: 2, Epoch: "memory", Approvals: make(map[string]approvalRecord), Revocations: make(map[string]uint64)}}
}

// Path reports the configured persistence path, or empty for memory-only stores.
func (s *Store) Path() string { return s.path }

// Status reads current authority. Errors return an unapproved state; cached
// grants never substitute for unreadable or unavailable authority.
func (s *Store) Status(ctx context.Context, bundleID string) (computeruse.ApprovalState, error) {
	key, err := s.validate(bundleID)
	if err != nil {
		return approvalRequired("approval required"), err
	}
	return s.status(ctx, key)
}

func (s *Store) status(ctx context.Context, key string) (state computeruse.ApprovalState, err error) {
	if err := s.enter(ctx); err != nil {
		return approvalRequired("approval unavailable"), err
	}
	defer s.leave()
	file, unlock, err := s.authority(ctx, false)
	if err != nil {
		return approvalRequired("approval unavailable"), err
	}
	defer func() {
		if closeErr := unlock(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close approval lock: %w", closeErr))
			state = approvalRequired("approval authority release failed; inspect current state")
		}
	}()
	return s.state(file, key), nil
}

// Resolve applies an approval decision. Deny and cancel are not revocation.
// Context bounds waiting for the local gate and file lock, not regular-file I/O.
func (s *Store) Resolve(ctx context.Context, bundleID string, decision computeruse.ApprovalDecision) (computeruse.ApprovalState, error) {
	key, err := s.validate(bundleID)
	if err != nil {
		return approvalRequired("approval required"), err
	}
	if decision == "" {
		decision = computeruse.ApprovalDecisionRequire
	}
	switch decision {
	case computeruse.ApprovalDecisionRequire:
		return s.status(ctx, key)
	case computeruse.ApprovalDecisionDeny:
		return approvalDenied("approval denied"), ErrApprovalDenied
	case computeruse.ApprovalDecisionCancel:
		return approvalCanceled("approval canceled"), ErrApprovalCanceled
	case computeruse.ApprovalDecisionApprove, computeruse.ApprovalDecisionApprovePersistent:
		return s.resolve(ctx, key, decision == computeruse.ApprovalDecisionApprovePersistent)
	default:
		return approvalRequired("approval required"), fmt.Errorf("unknown approval decision %q", decision)
	}
}

// Approve records a persistent or session-only grant. File-backed session grants
// require a writable revision journal. Failed writes never create implicit grants.
func (s *Store) Approve(ctx context.Context, bundleID string, persistent bool) (computeruse.ApprovalState, error) {
	decision := computeruse.ApprovalDecisionApprove
	if persistent {
		decision = computeruse.ApprovalDecisionApprovePersistent
	}
	return s.Resolve(ctx, bundleID, decision)
}

func (s *Store) resolve(ctx context.Context, key string, persistent bool) (state computeruse.ApprovalState, err error) {
	if err := s.enter(ctx); err != nil {
		return approvalRequired("approval unavailable"), err
	}
	defer s.leave()
	file, unlock, err := s.authority(ctx, true)
	if err != nil {
		return approvalRequired("approval unavailable"), err
	}
	defer func() {
		if closeErr := unlock(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close approval lock: %w", closeErr))
			state = approvalRequired("approval authority release failed; inspect current state")
		}
	}()
	if persistent && s.path == "" {
		return persistenceFailed("persistent approvals unavailable"), ErrApprovalPersistenceFailed
	}
	if err := file.advance(); err != nil {
		return approvalRequired("approval unavailable"), err
	}
	if persistent {
		file.Approvals[key] = approvalRecord{ApprovedAt: time.Now().UTC()}
	}
	if err := ctx.Err(); err != nil {
		return approvalRequired("approval canceled"), err
	}
	if err := s.save(file); err != nil {
		return persistenceFailed("approval write failed or unconfirmed"), err
	}
	s.blocked.Delete(key)
	if !persistent {
		s.session[key] = sessionRecord{file.Epoch, file.Revision}
	}
	return s.state(file, key), nil
}

// Revoke withdraws an app's grants in the shared authority. Failure blocks the
// app locally but cannot claim global revocation. Explicit local reapproval or
// successful revocation clears that failure override.
func (s *Store) Revoke(ctx context.Context, bundleID string) error {
	key, err := s.validate(bundleID)
	if err != nil {
		return err
	}
	err = s.revoke(ctx, key)
	if err != nil {
		s.blocked.Store(key, true)
	}
	return err
}

func (s *Store) revoke(ctx context.Context, key string) (err error) {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	file, unlock, err := s.authority(ctx, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := file.advance(); err != nil {
		return err
	}
	delete(file.Approvals, key)
	file.Revocations[key] = file.Revision
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.save(file); err != nil {
		return err
	}
	delete(s.session, key)
	s.blocked.Delete(key)
	return nil
}

func (s *Store) validate(bundleID string) (string, error) {
	if s == nil || s.gate == nil {
		return "", errors.New("approval store uninitialized")
	}
	key := normalizeBundleID(bundleID)
	if key == "" {
		return "", ErrBundleIDRequired
	}
	return key, nil
}

func (s *Store) enter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.leave()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) leave() { <-s.gate }

func (s *Store) authority(ctx context.Context, exclusive bool) (approvalFile, func() error, error) {
	if s.path == "" {
		return s.memory.clone(), func() error { return nil }, nil
	}
	lock, err := lockAuthority(ctx, s.path, exclusive)
	if err != nil {
		clear(s.session)
		return approvalFile{}, nil, err
	}
	file, err := readAuthority(s.path)
	if err == nil {
		err = s.refresh(file)
	}
	if err != nil {
		clear(s.session)
		lock.Close()
		return approvalFile{}, nil, err
	}
	return file, lock.Close, nil
}

func (s *Store) refresh(file approvalFile) error {
	if file.Epoch != "" && file.Epoch == s.epoch && file.Revision < s.revision {
		clear(s.session)
		return errors.New("approval authority revision decreased")
	}
	for key, record := range s.session {
		if record.Epoch != file.Epoch || record.Revision <= file.Revocations[key] {
			delete(s.session, key)
		}
	}
	if file.Epoch != "" {
		s.epoch, s.revision = file.Epoch, file.Revision
	}
	return nil
}

func (s *Store) save(file approvalFile) error {
	if s.path == "" {
		s.memory = file
		return s.refresh(file)
	}
	data, err := encodeAuthority(file)
	if err != nil {
		return err
	}
	committed, err := s.replace(s.path, data)
	if committed {
		err = errors.Join(err, s.refresh(file))
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrApprovalPersistenceFailed, err)
	}
	return nil
}

func (s *Store) state(file approvalFile, key string) computeruse.ApprovalState {
	if _, blocked := s.blocked.Load(key); blocked {
		return approvalRequired("approval locally revoked")
	}
	if _, ok := file.Approvals[key]; ok {
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
		Required: true,
		Message:  message,
	}
}

func normalizeBundleID(bundleID string) string {
	return strings.TrimSpace(strings.ToLower(bundleID))
}

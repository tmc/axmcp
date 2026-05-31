//go:build !darwin

package intervention

import (
	"sync"
	"time"

	"github.com/tmc/axmcp/internal/computeruse"
)

const defaultQuietPeriod = 750 * time.Millisecond

type Config struct {
	Enabled     bool
	QuietPeriod time.Duration
}

type Status struct {
	Enabled     bool
	QuietPeriod time.Duration
	LastInput   time.Time
	LastType    string
	LastKind    string
	LastPID     int64
}

type Monitor struct {
	mu          sync.Mutex
	enabled     bool
	quietPeriod time.Duration
	lastInput   time.Time
	lastType    string
	lastKind    string
	lastPID     int64
}

func New(cfg Config) *Monitor {
	quiet := cfg.QuietPeriod
	if quiet <= 0 {
		quiet = defaultQuietPeriod
	}
	return &Monitor{
		enabled:     cfg.Enabled,
		quietPeriod: quiet,
	}
}

func (m *Monitor) Start() error {
	if m == nil || !m.isEnabled() {
		return nil
	}
	return computeruse.PlatformUnsupported("human intervention monitor")
}

func (m *Monitor) Close() {}

func (m *Monitor) Record(kind string, now time.Time) {
	m.RecordEvent(kind, kind, 0, now)
}

func (m *Monitor) RecordEvent(eventType, kind string, sourcePID int64, now time.Time) {
	if m == nil || !m.isEnabled() {
		return
	}
	m.mu.Lock()
	m.lastInput = now
	m.lastType = eventType
	m.lastKind = kind
	m.lastPID = sourcePID
	m.mu.Unlock()
}

func (m *Monitor) Blocked(now time.Time) (Status, bool) {
	status := m.Status()
	if !status.Enabled || status.LastInput.IsZero() {
		return status, false
	}
	return status, now.Sub(status.LastInput) < status.QuietPeriod
}

func (m *Monitor) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Enabled:     m.enabled,
		QuietPeriod: m.quietPeriod,
		LastInput:   m.lastInput,
		LastType:    m.lastType,
		LastKind:    m.lastKind,
		LastPID:     m.lastPID,
	}
}

func (m *Monitor) isEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

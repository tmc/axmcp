package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type nativeDiscoveryTestBackend struct {
	*nativeTestBackend
	sets    []*nativeTestWindowSet
	count   int
	capture func(context.Context)
}
type nativeTestWindowSet struct {
	targets     []nativeTarget
	closed      atomic.Int32
	done        chan struct{}
	capture     func(context.Context)
	changeBirth bool
	snapshots   []*nativeTestSnapshot
}

func (b *nativeDiscoveryTestBackend) DiscoveryAccess(ctx context.Context, app computeruse.AppInfo) (computeruse.PermissionState, computeruse.ApprovalState, error) {
	return computeruse.PermissionState{Pending: b.deny}, computeruse.ApprovalState{Approved: !b.deny}, ctx.Err()
}
func (b *nativeDiscoveryTestBackend) Discover(ctx context.Context, app computeruse.AppInfo, limit int) (nativeWindowSet, error) {
	s := &nativeTestWindowSet{done: make(chan struct{}), capture: b.capture}
	for i := 0; i < b.count; i++ {
		target := b.target
		target.Window.WindowID += uint32(i)
		s.targets = append(s.targets, target)
	}
	b.sets = append(b.sets, s)
	return s, ctx.Err()
}
func (s *nativeTestWindowSet) Targets() []nativeTarget {
	return append([]nativeTarget(nil), s.targets...)
}
func (s *nativeTestWindowSet) ProcessStart() nativeStart { return nativeStart{Seconds: 1} }
func (s *nativeTestWindowSet) Truncated() bool           { return false }
func (s *nativeTestWindowSet) Close() error {
	if s.closed.Add(1) == 1 {
		close(s.done)
	}
	return nil
}
func (s *nativeTestWindowSet) Capture(ctx context.Context, index int) (session.Snapshot, nativeTarget, error) {
	if s.capture != nil {
		s.capture(ctx)
	}
	if s.closed.Load() != 0 {
		return nil, nativeTarget{}, fmt.Errorf("capture used closed candidate")
	}
	target := s.targets[index]
	if s.changeBirth {
		target.Start.Microseconds++
	}
	snapshot := &nativeTestSnapshot{fakeActionSnapshot: fakeActionSnapshot{state: computeruse.AppState{App: target.App, Window: target.Window, Tree: []computeruse.ElementNode{{Index: 0}}}}}
	s.snapshots = append(s.snapshots, snapshot)
	return snapshot, target, nil
}
func newDiscoveryTestRunner(t *testing.T) (*nativeRunner, *nativeDiscoveryTestBackend) {
	t.Helper()
	_, base := newNativeTestRunner(t)
	b := &nativeDiscoveryTestBackend{nativeTestBackend: base, count: 2}
	r := newNativeRunner(b)
	t.Cleanup(func() { r.close() })
	return r, b
}
func discoverNativeTest(t *testing.T, r *nativeRunner) nativeDiscoverOutput {
	t.Helper()
	out, err := r.discover(t.Context(), nil, nativeDiscoverInput{App: "Fixture"})
	if err != nil || len(out.Windows) != 2 {
		t.Fatalf("discover=%+v err=%v", out, err)
	}
	if out.Windows[0].SelectionID == out.Windows[1].SelectionID {
		t.Fatal("duplicate selection IDs")
	}
	return out
}
func TestNativeDiscoveryPreservesObservation(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	observed := nativeTestObserve(t, r)
	lease, err := r.store.Acquire(observed.StateID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	first := discoverNativeTest(t, r)
	_ = discoverNativeTest(t, r)
	if b.sets[0].closed.Load() != 1 {
		t.Fatal("replaced candidates not released exactly once")
	}
	if r.observation == nil || r.observation.output.StateID != observed.StateID || b.snapshots[0].closed != 0 {
		t.Fatal("discovery invalidated published observation")
	}
	if _, _, err := lease.Resolve(0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{SelectionID: first.Windows[0].SelectionID}); err == nil {
		t.Fatal("old generation accepted")
	}
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{App: "Fixture", SelectionID: "mixed"}); err == nil {
		t.Fatal("mixed selection accepted")
	}
}
func TestNativeDiscoverySelectionAndBirth(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			r, b := newDiscoveryTestRunner(t)
			out := discoverNativeTest(t, r)
			s := b.sets[0]
			s.changeBirth = changed
			observed, err := r.observe(t.Context(), nil, nativeObserveInput{SelectionID: out.Windows[1].SelectionID})
			if changed {
				if err == nil || observed.StateID != "" || s.snapshots[0].closed != 1 {
					t.Fatalf("changed instance accepted: %+v %v", observed, err)
				}
			} else if err != nil || observed.Window.WindowID != out.Windows[1].WindowID || observed.StateID == "" {
				t.Fatalf("selection=%+v err=%v", observed, err)
			}
		})
	}
}
func TestNativeDiscoveryExpiry(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	now := time.Now()
	r.now = func() time.Time { return now }
	out := discoverNativeTest(t, r)
	deadline := r.discoveries[nil].expires
	now = now.Add(30 * time.Second)
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{SelectionID: out.Windows[0].SelectionID}); err != nil {
		t.Fatal(err)
	}
	if r.discoveries[nil].expires != deadline {
		t.Fatal("selection renewed expiry")
	}
	now = deadline
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{SelectionID: out.Windows[0].SelectionID}); err == nil {
		t.Fatal("expired selection accepted")
	}
	if b.sets[0].closed.Load() != 1 {
		t.Fatal("expired candidates not released")
	}
}
func TestNativeDiscoveryTimerAndClose(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	r.discoveryTTL = 20 * time.Millisecond
	discoverNativeTest(t, r)
	select {
	case <-b.sets[0].done:
	case <-time.After(time.Second):
		t.Fatal("expiry timer did not release candidates")
	}
	r.discoveryTTL = time.Hour
	discoverNativeTest(t, r)
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if b.sets[1].closed.Load() != 1 {
		t.Fatal("Close did not release candidates exactly once")
	}
	if _, err := r.discover(t.Context(), nil, nativeDiscoverInput{App: "Fixture"}); err == nil {
		t.Fatal("closed runner accepted discovery")
	}
}
func TestNativeDiscoveryDeniedAndCanceled(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	first := discoverNativeTest(t, r)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.discover(ctx, nil, nativeDiscoverInput{App: "Fixture"}); err == nil {
		t.Fatal("canceled discovery accepted")
	}
	if b.sets[0].closed.Load() != 0 {
		t.Fatal("cancellation changed candidates")
	}
	if _, err := r.observe(t.Context(), nil, nativeObserveInput{SelectionID: first.Windows[0].SelectionID}); err != nil {
		t.Fatal(err)
	}
	b.deny = true
	out, err := r.discover(t.Context(), nil, nativeDiscoverInput{App: "Fixture"})
	if err != nil || len(out.Windows) != 0 || out.Approval.Approved {
		t.Fatalf("denied discovery=%+v %v", out, err)
	}
	if len(b.sets) != 1 || b.sets[0].closed.Load() != 1 {
		t.Fatal("denied discovery retained or enumerated candidates")
	}
}
func TestNativeDiscoveryCloseWaitsForCapture(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	entered, release := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	b.capture = func(context.Context) { close(entered); <-release }
	out := discoverNativeTest(t, r)
	observed := make(chan error, 1)
	go func() {
		_, err := r.observe(context.Background(), nil, nativeObserveInput{SelectionID: out.Windows[0].SelectionID})
		observed <- err
	}()
	<-entered
	closed := make(chan struct{})
	go func() { r.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while capture still used candidate")
	case <-time.After(20 * time.Millisecond):
	}
	if b.sets[0].closed.Load() != 0 {
		t.Fatal("candidate released during capture")
	}
	release <- struct{}{}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	if b.sets[0].closed.Load() != 1 {
		t.Fatal("Close did not release candidate")
	}
}

func TestNativeDiscoveryMCPOwnership(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "discovery-test", Version: "1"}, nil)
	registerNativeTools(server, r)
	connect := func() *mcp.ClientSession {
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(t.Context(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		cs, err := c.Connect(t.Context(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	a, c := connect(), connect()
	result, err := a.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_discover", Arguments: nativeDiscoverInput{App: "Fixture"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP discovery=%v %v", result, err)
	}
	var out nativeDiscoverOutput
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Windows) != 2 {
		t.Fatal("missing windows")
	}
	other, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_observe", Arguments: nativeObserveInput{SelectionID: out.Windows[0].SelectionID}})
	if err == nil && !other.IsError {
		t.Fatal("another client used selection")
	}
	own, err := a.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_observe", Arguments: nativeObserveInput{SelectionID: out.Windows[0].SelectionID}})
	if err != nil || own.IsError {
		t.Fatalf("own selection failed: %v %v", own, err)
	}
	a.Close()
	select {
	case <-b.sets[0].done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not release candidates")
	}
}

func TestNativeDiscoveryCaptureRevocationAndExpiry(t *testing.T) {
	for _, kind := range []string{"approval", "expiry", "cancellation"} {
		t.Run(kind, func(t *testing.T) {
			r, b := newDiscoveryTestRunner(t)
			now := time.Now()
			r.now = func() time.Time { return now }
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			b.capture = func(context.Context) {
				switch kind {
				case "approval":
					b.deny = true
				case "expiry":
					now = now.Add(time.Minute)
				case "cancellation":
					cancel()
				}
			}
			out := discoverNativeTest(t, r)
			observed, err := r.observe(ctx, nil, nativeObserveInput{SelectionID: out.Windows[0].SelectionID})
			if observed.StateID != "" || (kind != "approval" && err == nil) {
				t.Fatalf("invalid capture published: %+v %v", observed, err)
			}
			if b.sets[0].snapshots[0].closed != 1 {
				t.Fatal("rejected capture leaked")
			}
		})
	}
}
func TestNativeDiscoveryQueuedCancellation(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	discoverNativeTest(t, r)
	r.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.discover(ctx, nil, nativeDiscoverInput{App: "Fixture"}); done <- err }()
	err := <-done
	<-r.gate
	if err == nil || b.sets[0].closed.Load() != 0 || len(b.sets) != 1 {
		t.Fatal("queued cancellation changed discovery")
	}
}
func TestNativeDiscoveryLimit(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	b.count = nativeDiscoveryLimit + 1
	if _, err := r.discover(t.Context(), nil, nativeDiscoverInput{App: "Fixture"}); err == nil {
		t.Fatal("oversized backend result accepted")
	}
	if b.sets[0].closed.Load() != 1 {
		t.Fatal("oversized backend result leaked")
	}
}

func (s *nativeTestWindowSet) Validate(ctx context.Context, index int) (nativeTarget, error) {
	if s.closed.Load() != 0 {
		return nativeTarget{}, fmt.Errorf("validate used closed window set")
	}
	target := s.targets[index]
	if s.changeBirth {
		target.Start.Microseconds++
	}
	return target, ctx.Err()
}

func TestNativeTargetMCPOwnership(t *testing.T) {
	r, b := newDiscoveryTestRunner(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "discovery-test", Version: "1"}, nil)
	registerNativeTools(server, r)
	connect := func() *mcp.ClientSession {
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(t.Context(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		cs, err := c.Connect(t.Context(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	a, c := connect(), connect()
	result, err := a.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_discover", Arguments: nativeDiscoverInput{App: "Fixture"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP discovery=%v %v", result, err)
	}
	var out nativeDiscoverOutput
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Windows) != 2 {
		t.Fatal("missing windows")
	}
	selectedResult, err := a.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_select", Arguments: nativeSelectInput{SelectionID: out.Windows[0].SelectionID}})
	if err != nil || selectedResult.IsError {
		t.Fatalf("select: %v %v", selectedResult, err)
	}
	var selected nativeSelectOutput
	raw, _ = json.Marshal(selectedResult.StructuredContent)
	if err := json.Unmarshal(raw, &selected); err != nil {
		t.Fatal(err)
	}
	if selected.TargetHandle == "" {
		t.Fatal("missing target handle")
	}
	other, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_observe", Arguments: nativeObserveInput{TargetHandle: selected.TargetHandle}})
	if err == nil && !other.IsError {
		t.Fatal("another client used retained handle")
	}
	foreignRelease, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_release", Arguments: nativeReleaseInput{TargetHandle: selected.TargetHandle}})
	if err != nil || foreignRelease.IsError {
		t.Fatalf("foreign release: %v %v", foreignRelease, err)
	}
	var released nativeReleaseOutput
	raw, _ = json.Marshal(foreignRelease.StructuredContent)
	if err := json.Unmarshal(raw, &released); err != nil {
		t.Fatal(err)
	}
	if released.Released {
		t.Fatal("another client released retained handle")
	}
	own, err := a.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_observe", Arguments: nativeObserveInput{TargetHandle: selected.TargetHandle}})
	if err != nil || own.IsError {
		t.Fatalf("own retained handle failed: %v %v", own, err)
	}
	var observed nativeObservationOutput
	raw, _ = json.Marshal(own.StructuredContent)
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	foreignAct, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_act", Arguments: nativeTestClick(observed)})
	if err != nil {
		t.Fatal(err)
	}
	var act nativeActOutput
	raw, _ = json.Marshal(foreignAct.StructuredContent)
	if err := json.Unmarshal(raw, &act); err != nil {
		t.Fatal(err)
	}
	if act.Execution != "not_dispatched" || act.ErrorText == "" {
		t.Fatalf("foreign action: %+v", act)
	}
	// Transport response completed; inspect shared state under the same gate.
	if err := r.run(t.Context(), 0, func(context.Context) error {
		if r.observation == nil || r.observation.output.StateID != observed.StateID || b.calls != 0 {
			return fmt.Errorf("foreign action consumed owner observation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	select {
	case <-b.sets[0].done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not release candidates")
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type nativeTestSnapshot struct {
	fakeActionSnapshot
	closed int
}

func (s *nativeTestSnapshot) Close() error { s.closed++; return nil }

type nativeTestBackend struct {
	target        nativeTarget
	snapshots     []*nativeTestSnapshot
	effects       int
	calls         int
	deny          bool
	checkErr      error
	actionErr     error
	attempted     bool
	captureErr    error
	afterCapture  func()
	beforePerform func()
}

func newNativeTestRunner(t *testing.T) (*nativeRunner, *nativeTestBackend) {
	t.Helper()
	b := &nativeTestBackend{target: nativeTarget{App: computeruse.AppInfo{PID: 42, Name: "Fixture", BundleID: "test.fixture"}, Start: nativeStart{Seconds: 1}, Window: computeruse.WindowInfo{WindowID: 7}}, attempted: true}
	r := newNativeRunner(b)
	t.Cleanup(func() { r.store.Close() })
	return r, b
}
func (b *nativeTestBackend) Resolve(context.Context, string) (computeruse.AppInfo, error) {
	return b.target.App, nil
}
func (b *nativeTestBackend) Authorize(ctx context.Context, _ *mcp.CallToolRequest, _ computeruse.AppInfo, _ bool) (computeruse.PermissionState, computeruse.ApprovalState, error) {
	return computeruse.PermissionState{Pending: b.deny}, computeruse.ApprovalState{Approved: !b.deny}, ctx.Err()
}
func (b *nativeTestBackend) Capture(ctx context.Context, _ computeruse.AppInfo, _ uint32) (session.Snapshot, nativeTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, nativeTarget{}, err
	}
	if b.captureErr != nil {
		return nil, nativeTarget{}, b.captureErr
	}
	s := &nativeTestSnapshot{fakeActionSnapshot: fakeActionSnapshot{state: computeruse.AppState{App: b.target.App, Window: b.target.Window, Tree: []computeruse.ElementNode{{Index: 99, Identifier: "count", Value: fmt.Sprint(b.effects)}}}}}
	b.snapshots = append(b.snapshots, s)
	if b.afterCapture != nil {
		b.afterCapture()
	}
	return s, b.target, nil
}
func (b *nativeTestBackend) Check(context.Context, nativeTarget, *session.Lease, nativeActInput) error {
	return b.checkErr
}
func (b *nativeTestBackend) Perform(_ context.Context, _ nativeTarget, lease *session.Lease, _ nativeActInput) (bool, error) {
	b.calls++
	if b.snapshots[0].closed != 0 {
		return false, errors.New("snapshot closed before dispatch")
	}
	if _, _, err := lease.Resolve(0); err != nil {
		return false, err
	}
	if b.beforePerform != nil {
		b.beforePerform()
	}
	if b.attempted {
		b.effects++
	}
	return b.attempted, b.actionErr
}
func nativeTestObserve(t *testing.T, r *nativeRunner) nativeObservationOutput {
	t.Helper()
	out, err := r.observe(context.Background(), nil, nativeObserveInput{App: "Fixture"})
	if err != nil || out.StateID == "" || out.TargetID == "" {
		t.Fatalf("observe = %+v, %v", out, err)
	}
	return out
}
func nativeTestClick(out nativeObservationOutput) nativeActInput {
	index := 1
	return nativeActInput{StateID: out.StateID, TargetID: out.TargetID, Action: "click", ElementIndex: &index}
}
func TestNativeActionAxes(t *testing.T) {
	for _, tt := range []struct {
		name                              string
		setup                             func(*nativeTestBackend)
		execution, observation, predicate string
		effects                           int
	}{
		{"success", func(*nativeTestBackend) {}, "completed", "captured", "met", 1},
		{"uncertain dispatch", func(b *nativeTestBackend) { b.actionErr = errors.New("AX timeout") }, "dispatched_unknown", "captured", "met", 1},
		{"no dispatch", func(b *nativeTestBackend) { b.attempted = false; b.actionErr = errors.New("focus changed") }, "not_dispatched", "captured", "unmet", 0},
		{"authorization revoked after capture", func(b *nativeTestBackend) { b.afterCapture = func() { b.deny = true } }, "completed", "unavailable", "unknown", 1},
		{"capture fails after success", func(b *nativeTestBackend) { b.captureErr = errors.New("capture failed") }, "completed", "unavailable", "unknown", 1},
		{"PID reused after success", func(b *nativeTestBackend) { b.target.Start.Seconds++ }, "completed", "unavailable", "unknown", 1},
		{"window replaced after success", func(b *nativeTestBackend) { b.target.Window.WindowID++ }, "completed", "unavailable", "unknown", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, b := newNativeTestRunner(t)
			observed := nativeTestObserve(t, r)
			tt.setup(b)
			in := nativeTestClick(observed)
			in.Expect = &nativeExpect{Identifier: "count", Attribute: "value", Text: "1"}
			out := r.act(context.Background(), nil, in)
			if out.Execution != tt.execution || out.Observation != tt.observation || out.Postcondition != tt.predicate || b.effects != tt.effects {
				t.Fatalf("out=%+v effects=%d", out, b.effects)
			}
			if b.snapshots[0].closed != 1 {
				t.Fatalf("old snapshot closes=%d", b.snapshots[0].closed)
			}
			if out.FreshState != nil && (out.FreshState.StateID == observed.StateID || out.FreshState.TargetID == observed.TargetID) {
				t.Fatal("fresh observation reused tokens")
			}
			replay := r.act(context.Background(), nil, in)
			if replay.Execution != "not_dispatched" || b.calls != 1 {
				t.Fatalf("replay=%+v calls=%d", replay, b.calls)
			}
			if tt.observation == "unavailable" && len(b.snapshots) > 1 && b.snapshots[1].closed != 1 {
				t.Fatal("rejected postcapture leaked")
			}
		})
	}
}
func TestNativeStateConsumption(t *testing.T) {
	for _, tt := range []struct {
		name     string
		mutate   func(*nativeActInput, *nativeTestBackend)
		consumed bool
	}{
		{"unknown state", func(in *nativeActInput, _ *nativeTestBackend) { in.StateID = "unknown" }, false},
		{"bad timeout", func(in *nativeActInput, _ *nativeTestBackend) { in.TimeoutMS = -1 }, false},
		{"wrong target", func(in *nativeActInput, _ *nativeTestBackend) { in.TargetID = "other" }, true},
		{"bad action", func(in *nativeActInput, _ *nativeTestBackend) { in.Action = "guess" }, true},
		{"revoked permission", func(_ *nativeActInput, b *nativeTestBackend) { b.deny = true }, true},
		{"stale window", func(_ *nativeActInput, b *nativeTestBackend) { b.checkErr = errors.New("window moved") }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, b := newNativeTestRunner(t)
			observed := nativeTestObserve(t, r)
			in := nativeTestClick(observed)
			tt.mutate(&in, b)
			out := r.act(context.Background(), nil, in)
			if out.Execution != "not_dispatched" || b.calls != 0 || out.ErrorText == "" {
				t.Fatalf("out=%+v calls=%d", out, b.calls)
			}
			b.deny = false
			b.checkErr = nil
			retry := r.act(context.Background(), nil, nativeTestClick(observed))
			if (retry.Execution == "completed") == tt.consumed {
				t.Fatalf("retry=%+v consumed=%v", retry, tt.consumed)
			}
		})
	}
}
func TestNativeConcurrentConsume(t *testing.T) {
	r, b := newNativeTestRunner(t)
	in := nativeTestClick(nativeTestObserve(t, r))
	var wg sync.WaitGroup
	results := make(chan nativeActOutput, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- r.act(context.Background(), nil, in) }()
	}
	wg.Wait()
	close(results)
	completed := 0
	for out := range results {
		if out.Execution == "completed" {
			completed++
		}
	}
	if completed != 1 || b.effects != 1 {
		t.Fatalf("completed=%d effects=%d", completed, b.effects)
	}
}
func TestNativeCanceledQueuePreservesState(t *testing.T) {
	r, b := newNativeTestRunner(t)
	in := nativeTestClick(nativeTestObserve(t, r))
	r.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := r.act(ctx, nil, in)
	<-r.gate
	if out.Execution != "not_dispatched" || !strings.Contains(out.ErrorText, "canceled") || b.calls != 0 {
		t.Fatalf("canceled=%+v", out)
	}
	if next := r.act(context.Background(), nil, in); next.Execution != "completed" {
		t.Fatalf("next=%+v", next)
	}
}
func TestNativePredicate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		nodes  []computeruse.ElementNode
		expect nativeExpect
		want   bool
	}{
		{"raw value", []computeruse.ElementNode{{Identifier: "entry", Value: " x "}}, nativeExpect{"entry", "value", " x "}, true},
		{"no trimming", []computeruse.ElementNode{{Identifier: "entry", Value: " x "}}, nativeExpect{"entry", "value", "x"}, false},
		{"duplicate", []computeruse.ElementNode{{Identifier: "entry", Value: "x"}, {Identifier: "entry", Value: "x"}}, nativeExpect{"entry", "value", "x"}, false},
		{"missing", nil, nativeExpect{"entry", "value", ""}, false},
		{"empty title", []computeruse.ElementNode{{Identifier: "entry"}}, nativeExpect{"entry", "title", ""}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := nativeMatches(tt.nodes, tt.expect); got != tt.want {
				t.Fatalf("match=%v", got)
			}
		})
	}
}
func TestNativeToolsMCP(t *testing.T) {
	ctx := context.Background()
	r, b := newNativeTestRunner(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "native-test", Version: "1"}, nil)
	registerNativeTools(server, r)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args any, out any) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("tool error: %+v", res)
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	var observed nativeObservationOutput
	call("native_observe", nativeObserveInput{App: "Fixture"}, &observed)
	var out nativeActOutput
	call("native_act", nativeTestClick(observed), &out)
	if out.Execution != "completed" || out.Observation != "captured" || b.effects != 1 {
		t.Fatalf("out=%+v effects=%d", out, b.effects)
	}
	call("native_act", nativeTestClick(observed), &out)
	if out.Execution != "not_dispatched" || b.effects != 1 {
		t.Fatalf("replay=%+v", out)
	}
}

// This tests the same focus guard called at Check and immediately before
// dispatch. It establishes validation behavior, not background OS delivery.
func TestNativeBackgroundFocusGuard(t *testing.T) {
	for _, action := range []string{"click", "set_value", "secondary_action", "type_text", "press_key", "scroll"} {
		for _, same := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/focused=%v", action, same), func(t *testing.T) {
				queries := 0
				dispatches := 0
				err := checkNativeFocus(action, 7, func() (uint32, error) {
					queries++
					if same {
						return 7, nil
					}
					return 8, nil
				})
				if err == nil {
					dispatches++
				}
				direct := action == "click" || action == "set_value" || action == "secondary_action"
				if direct && queries != 0 {
					t.Fatal("direct AX operation queried unrelated focus")
				}
				want := 0
				if direct || same {
					want = 1
				}
				if dispatches != want {
					t.Fatalf("dispatches=%d want=%d err=%v", dispatches, want, err)
				}
			})
		}
	}
	sentinel := errors.New("focus unavailable")
	if err := checkNativeFocus("press_key", 7, func() (uint32, error) { return 0, sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
}

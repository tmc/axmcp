package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNativePointerRetryMCP(t *testing.T) {
	r, _ := newNativeTestRunner(t)
	defer r.close()
	r.pointerRecovery.roundLimit = 10 * time.Millisecond
	server := mcp.NewServer(&mcp.Implementation{Name: "recovery", Version: "1"}, nil)
	registerNativeTools(server, r)
	connect := func() (*mcp.ClientSession, *mcp.ServerSession) {
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(t.Context(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		cs, err := client.Connect(t.Context(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs, ss
	}
	own, owner := connect()
	other, _ := connect()
	var resumed atomic.Bool
	var releases, posts atomic.Int32
	r.recoveryOwner = owner // No selected target handle is needed to bind ownership.
	if err := r.pointerRecovery.start(t.Context(), func(context.Context) (bool, error) {
		if releases.Load() != 0 {
			t.Error("retry used released snapshot")
		}
		if !resumed.Load() {
			return false, errors.New("target stalled")
		}
		posts.Add(1)
		return true, nil
	}, func() error { releases.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	waitPointerRecovery(t, &r.pointerRecovery)
	id, status, canRetry, _ := r.pointerRecovery.details()
	if status != "unresolved" || !canRetry || releases.Load() != 0 {
		t.Fatalf("status=%s retryable=%v releases=%d", status, canRetry, releases.Load())
	}
	call := func(c *mcp.ClientSession, in nativeRecoverPointerInput, success bool) nativeRecoverPointerOutput {
		result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_recover_pointer", Arguments: in})
		if err != nil || result.IsError {
			if success {
				t.Fatalf("tool failed: %+v %v", result, err)
			}
			return nativeRecoverPointerOutput{}
		}
		if !success {
			t.Fatalf("tool unexpectedly accepted %+v", in)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out nativeRecoverPointerOutput
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	result, err := other.CallTool(t.Context(), &mcp.CallToolParams{Name: "native_act", Arguments: nativeActInput{StateID: "unused", TargetID: "unused", Action: "click"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var denied nativeActOutput
	if err := json.Unmarshal(raw, &denied); err != nil {
		t.Fatal(err)
	}
	if denied.Execution != "not_dispatched" || denied.RecoveryID != "" || denied.PointerRecovery != "" {
		t.Fatalf("foreign action disclosed recovery: %+v", denied)
	}
	call(other, nativeRecoverPointerInput{Mode: "status"}, false)
	call(other, nativeRecoverPointerInput{Mode: "retry", RecoveryID: id}, false)
	call(own, nativeRecoverPointerInput{Mode: "retry"}, false)
	call(own, nativeRecoverPointerInput{Mode: "retry", RecoveryID: "wrong"}, false)
	out := call(own, nativeRecoverPointerInput{Mode: "status"}, true)
	if out.RecoveryID != id || !out.Retryable {
		t.Fatalf("status=%+v", out)
	}
	resumed.Store(true)
	call(own, nativeRecoverPointerInput{Mode: "retry", RecoveryID: id}, true)
	waitPointerRecovery(t, &r.pointerRecovery)
	out = call(own, nativeRecoverPointerInput{Mode: "status", RecoveryID: id}, true)
	if out.Status != "recovered" || out.Retryable || posts.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("out=%+v posts=%d releases=%d", out, posts.Load(), releases.Load())
	}
	call(own, nativeRecoverPointerInput{Mode: "retry", RecoveryID: id}, false)
	if posts.Load() != 1 {
		t.Fatal("successful release was replayed")
	}
}

func TestNativePointerRetryUncertainAndStopped(t *testing.T) {
	for _, sent := range []bool{false, true} {
		var r nativePointerRecovery
		r.roundLimit = time.Millisecond
		uncertain := errors.New("uncertain post")
		if err := r.start(t.Context(), func(context.Context) (bool, error) { return sent, uncertain }, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		waitPointerRecovery(t, &r)
		id, _, _, _ := r.details()
		if sent {
			if err := r.retry(t.Context(), id); err == nil {
				t.Fatal("uncertain event retried")
			}
		}
		_ = r.stop()
		if err := r.retry(t.Context(), id); err == nil {
			t.Fatal("disposed snapshot retried")
		}
		_ = r.close()
	}
}

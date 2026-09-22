package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

func TestNativeMCPImages(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 20, 20))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	ctx := context.Background()
	r, b := newNativeTestRunner(t)
	b.afterCapture = func() {
		s := &b.snapshots[len(b.snapshots)-1].state
		s.ScreenshotPNGBase64 = base64.StdEncoding.EncodeToString(data)
		s.ScreenshotMetadata = &computeruse.ScreenshotInfo{ImageID: hash, SourceKind: "window", TargetWindow: 7, Width: 20, Height: 20, GlobalRect: computeruse.CaptureRect{Width: 10, Height: 10}, ScaleX: 2, ScaleY: 2}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
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
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "native_observe", Arguments: nativeObserveInput{App: "Fixture"}})
	if err != nil || result.IsError {
		t.Fatalf("observe=%+v error=%v", result, err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var observed nativeObservationOutput
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.StateID == "" || observed.ScreenshotPNGBase64 != "" || observed.ScreenshotMetadata.ImageID != hash {
		t.Fatalf("wire observation=%+v", observed)
	}
	images := 0
	for _, c := range result.Content {
		switch c := c.(type) {
		case *mcp.ImageContent:
			images++
			if !bytes.Equal(c.Data, data) || c.MIMEType != "image/png" {
				t.Fatal("image mismatch")
			}
		case *mcp.TextContent:
			if bytes.Contains([]byte(c.Text), []byte("screenshot_png_base64")) {
				t.Fatal("duplicate image in text")
			}
		}
	}
	if images != 1 || r.observation.output.ScreenshotPNGBase64 == "" {
		t.Fatal("missing image or retained PNG")
	}
	// Schema rejects a point without y before the handler consumes its token.
	malformed := map[string]any{"state_id": observed.StateID, "target_id": observed.TargetID, "action": "click", "image_id": hash, "point": map[string]any{"x": 1}}
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "native_act", Arguments: malformed})
	if err == nil && !result.IsError {
		t.Fatal("missing point coordinate accepted")
	}
	if r.observation == nil {
		t.Fatal("schema error consumed state")
	}
	// A fully decoded OOB point reaches the handler and consumes the state.
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "native_act", Arguments: nativeActInput{StateID: observed.StateID, TargetID: observed.TargetID, Action: "click", ImageID: hash, Point: &nativePoint{20, 1}}})
	if err != nil || result.IsError {
		t.Fatalf("handler response=%+v error=%v", result, err)
	}
	if r.observation != nil || b.calls != 0 {
		t.Fatal("OOB validation did not consume without dispatch")
	}
}

func TestNativeImageFailurePreservesExecution(t *testing.T) {
	out := nativeActOutput{Execution: "completed", Observation: "captured", Postcondition: "not_requested", FreshState: &nativeObservationOutput{AppState: computeruse.AppState{ScreenshotPNGBase64: "invalid"}}}
	result, wire, err := nativeActResult(out)
	if err != nil || result == nil || wire.Execution != "completed" || wire.Observation != "unavailable" || wire.FreshState != nil || wire.ErrorText == "" {
		t.Fatalf("wire=%+v err=%v", wire, err)
	}
	if out.FreshState.ScreenshotPNGBase64 != "invalid" {
		t.Fatal("mutated retained output")
	}
}

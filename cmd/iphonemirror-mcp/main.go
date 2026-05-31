//go:build darwin

// Command iphonemirror-mcp is an MCP server that drives Apple's iPhone
// Mirroring app on macOS via screen capture, OCR, and synthetic input.
//
// The mirrored iPhone screen is rendered as a single opaque AXHostingView, so
// AX-tree traversal is not viable; this server captures pixels and runs Apple
// Vision OCR to surface UI elements to LLM clients. Synthetic clicks/drags
// are routed through the macOS iPhone Mirroring app, which translates them
// into iOS gestures.
//
// Status: scaffold. Wired tools: iphone_describe, iphone_tap, iphone_swipe,
// iphone_type, iphone_action, iphone_long_press, iphone_double_tap,
// iphone_zoom_in, iphone_zoom_out, iphone_drag_and_drop, iphone_focus,
// iphone_wait_until.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse/imagehash"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/ocrwindow"
	"github.com/tmc/axmcp/internal/skylightinput"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/macgo"
	"github.com/tmc/macgo/permissions"
)

const screenContinuityApp = "iPhone Mirroring"

// ---------- iphone_describe ----------

type describeInput struct{}

type describeHit struct {
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence"`
	NX         float64 `json:"nx"`
	NY         float64 `json:"ny"`
	NW         float64 `json:"nw"`
	NH         float64 `json:"nh"`
}

type windowOut struct {
	ID    uint32  `json:"id"`
	Title string  `json:"title"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

type describeOutput struct {
	Window windowOut `json:"window"`
	Image  struct {
		W int `json:"w"`
		H int `json:"h"`
	} `json:"image"`
	Hits []describeHit `json:"hits"`
}

func iphoneDescribe(_ context.Context, _ *mcp.CallToolRequest, _ describeInput) (*mcp.CallToolResult, any, error) {
	out, _, err := describeIPhone()
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(out)
}

// ---------- iphone_tap ----------

type tapInput struct {
	Text  string   `json:"text,omitempty"`
	NX    *float64 `json:"nx,omitempty"`
	NY    *float64 `json:"ny,omitempty"`
	Match int      `json:"match,omitempty"`
}

type tapOutput struct {
	ScreenX int       `json:"screen_x"`
	ScreenY int       `json:"screen_y"`
	Matched string    `json:"matched,omitempty"`
	Window  windowOut `json:"window"`
}

// requireAccessibility returns an error when the process lacks Accessibility
// permission. CGEventPost / CGEventPostToPid silently no-op without it, so a
// pre-check turns a confusing "tool reports success but nothing happened"
// into a loud, actionable error. Call from every input-emitting tool.
func requireAccessibility() error {
	if axuiautomation.AXIsProcessTrusted() {
		return nil
	}
	return fmt.Errorf("accessibility not granted: synthetic events would silently drop. Grant in System Settings -> Privacy & Security -> Accessibility (toggle off then on if a stale entry exists)")
}

// focusIPhoneMirroring makes iPhone Mirroring frontmost before input dispatch.
func focusIPhoneMirroring(pid int32, windowID uint32) {
	res := focusIPhoneMirroringWithOptions(pid, windowID, true)
	if res.Err != nil {
		log.Printf("focusIPhoneMirroring(pid=%d, wid=%d): %v", pid, windowID, res.Err)
	}
}

func iphoneTap(_ context.Context, _ *mcp.CallToolRequest, args tapInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return nil, nil, fmt.Errorf("find window: %w", err)
	}
	var sx, sy int
	var matched string
	switch {
	case args.Text != "":
		png, iw, ih, err := ocrwindow.Capture(win)
		if err != nil {
			return nil, nil, fmt.Errorf("capture: %w", err)
		}
		hits, err := ocrwindow.Recognize(png, iw, ih)
		if err != nil {
			return nil, nil, fmt.Errorf("ocr: %w", err)
		}
		want := strings.ToLower(args.Text)
		picks := []ocrwindow.Hit{}
		for _, h := range hits {
			if strings.Contains(strings.ToLower(h.Text), want) {
				picks = append(picks, h)
			}
		}
		picks = dedupOCRHits(picks, 10)
		if len(picks) == 0 {
			return nil, nil, fmt.Errorf("no OCR hit matching %q", args.Text)
		}
		idx := args.Match
		if idx <= 0 {
			idx = 1
		}
		if idx > len(picks) {
			return nil, nil, fmt.Errorf("match=%d but only %d hits for %q", idx, len(picks), args.Text)
		}
		h := picks[idx-1]
		matched = h.Text
		hitCX := float64(h.X+h.W/2) / float64(iw)
		hitCY := float64(h.Y+h.H/2) / float64(ih)
		sx = int(win.X + hitCX*win.W)
		sy = int(win.Y + hitCY*win.H)
	case args.NX != nil || args.NY != nil:
		if args.NX == nil || args.NY == nil {
			return nil, nil, fmt.Errorf("provide both nx and ny")
		}
		if *args.NX < 0 || *args.NX > 1 || *args.NY < 0 || *args.NY > 1 {
			return nil, nil, fmt.Errorf("nx/ny must be in [0,1]")
		}
		sx = int(win.X + *args.NX*win.W)
		sy = int(win.Y + *args.NY*win.H)
	default:
		return nil, nil, fmt.Errorf("provide either text or nx/ny")
	}
	focusIPhoneMirroring(int32(win.OwnerPID), win.ID)
	if err := clickIPhoneMirroring(win, sx, sy); err != nil {
		return nil, nil, fmt.Errorf("click: %w", err)
	}
	return jsonResult(tapOutput{ScreenX: sx, ScreenY: sy, Matched: matched, Window: windowOut{
		ID: win.ID, Title: win.Title, X: win.X, Y: win.Y, W: win.W, H: win.H,
	}})
}

// ---------- iphone_long_press / iphone_double_tap ----------

type longPressInput struct {
	NX         *float64 `json:"nx"`
	NY         *float64 `json:"ny"`
	DurationMs int      `json:"duration_ms,omitempty"`
	WithJitter bool     `json:"with_jitter,omitempty"`
}

type longPressOutput struct {
	ScreenX    int       `json:"screen_x"`
	ScreenY    int       `json:"screen_y"`
	DurationMs int       `json:"duration_ms"`
	WithJitter bool      `json:"with_jitter"`
	Window     windowOut `json:"window"`
}

type doubleTapInput struct {
	NX *float64 `json:"nx"`
	NY *float64 `json:"ny"`
}

func iphoneLongPress(_ context.Context, _ *mcp.CallToolRequest, args longPressInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	win, sx, sy, err := screenPointForNormalizedPointers(args.NX, args.NY)
	if err != nil {
		return nil, nil, err
	}
	durationMs := args.DurationMs
	if durationMs == 0 {
		durationMs = 600
	}
	if durationMs < 50 {
		durationMs = 50
	}
	if durationMs > 5000 {
		durationMs = 5000
	}
	focusIPhoneMirroring(int32(win.OwnerPID), win.ID)
	duration := time.Duration(durationMs) * time.Millisecond
	if err := input.LongPressScreenPoint(sx, sy, duration, args.WithJitter); err != nil {
		return nil, nil, fmt.Errorf("long press: %w", err)
	}
	return jsonResult(longPressOutput{ScreenX: sx, ScreenY: sy, DurationMs: durationMs, WithJitter: args.WithJitter, Window: windowForOutput(win)})
}

func iphoneDoubleTap(_ context.Context, _ *mcp.CallToolRequest, args doubleTapInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	win, sx, sy, err := screenPointForNormalizedPointers(args.NX, args.NY)
	if err != nil {
		return nil, nil, err
	}
	focusIPhoneMirroring(int32(win.OwnerPID), win.ID)
	if err := input.MultiClickScreenPoint(sx, sy, 2); err != nil {
		return nil, nil, fmt.Errorf("double tap: %w", err)
	}
	return jsonResult(tapOutput{ScreenX: sx, ScreenY: sy, Window: windowForOutput(win)})
}

// ---------- iphone_drag_and_drop ----------

type dragAndDropInput struct {
	NX1    float64 `json:"nx1"`
	NY1    float64 `json:"ny1"`
	NX2    float64 `json:"nx2"`
	NY2    float64 `json:"ny2"`
	HoldMs int     `json:"hold_ms,omitempty"`
}

type dragAndDropOutput struct {
	Start  [2]int    `json:"start"`
	End    [2]int    `json:"end"`
	HoldMs int       `json:"hold_ms"`
	Window windowOut `json:"window"`
}

func iphoneDragAndDrop(_ context.Context, _ *mcp.CallToolRequest, args dragAndDropInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	for _, v := range [...]float64{args.NX1, args.NY1, args.NX2, args.NY2} {
		if v < 0 || v > 1 {
			return nil, nil, fmt.Errorf("normalized coords must be in [0,1]")
		}
	}
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return nil, nil, fmt.Errorf("find window: %w", err)
	}
	sx := int(win.X + args.NX1*win.W)
	sy := int(win.Y + args.NY1*win.H)
	ex := int(win.X + args.NX2*win.W)
	ey := int(win.Y + args.NY2*win.H)
	holdMs := args.HoldMs
	if holdMs <= 0 {
		holdMs = 600
	}
	focusIPhoneMirroring(int32(win.OwnerPID), win.ID)
	if err := input.LongPressDragScreenPoint(sx, sy, ex, ey, time.Duration(holdMs)*time.Millisecond); err != nil {
		return nil, nil, fmt.Errorf("drag and drop: %w", err)
	}
	return jsonResult(dragAndDropOutput{Start: [2]int{sx, sy}, End: [2]int{ex, ey}, HoldMs: holdMs, Window: windowForOutput(win)})
}

// ---------- iphone_swipe ----------

type swipeInput struct {
	NX1 float64 `json:"nx1"`
	NY1 float64 `json:"ny1"`
	NX2 float64 `json:"nx2"`
	NY2 float64 `json:"ny2"`
}

type swipeOutput struct {
	Start  [2]int    `json:"start"`
	End    [2]int    `json:"end"`
	Window windowOut `json:"window"`
}

func iphoneSwipe(_ context.Context, _ *mcp.CallToolRequest, args swipeInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	for _, v := range [...]float64{args.NX1, args.NY1, args.NX2, args.NY2} {
		if v < 0 || v > 1 {
			return nil, nil, fmt.Errorf("normalized coords must be in [0,1]")
		}
	}
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return nil, nil, fmt.Errorf("find window: %w", err)
	}
	sx := int(win.X + args.NX1*win.W)
	sy := int(win.Y + args.NY1*win.H)
	ex := int(win.X + args.NX2*win.W)
	ey := int(win.Y + args.NY2*win.H)
	focusIPhoneMirroring(int32(win.OwnerPID), win.ID)
	if err := input.DragScreenPoint(sx, sy, ex, ey); err != nil {
		return nil, nil, fmt.Errorf("drag: %w", err)
	}
	return jsonResult(swipeOutput{Start: [2]int{sx, sy}, End: [2]int{ex, ey}, Window: windowOut{
		ID: win.ID, Title: win.Title, X: win.X, Y: win.Y, W: win.W, H: win.H,
	}})
}

// ---------- iphone_type ----------

type typeInput struct {
	Text    string `json:"text"`
	DelayMs int    `json:"delay_ms,omitempty"`
}

type typeOutput struct {
	Chars int `json:"chars"`
}

func iphoneType(_ context.Context, _ *mcp.CallToolRequest, args typeInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	if args.Text == "" {
		return nil, nil, fmt.Errorf("text is required")
	}
	pid, err := iphoneMirroringPID()
	if err != nil {
		return nil, nil, err
	}
	focusIPhoneMirroring(int32(pid), 0)
	delay := time.Duration(args.DelayMs) * time.Millisecond
	if delay <= 0 {
		delay = 30 * time.Millisecond
	}
	for _, r := range args.Text {
		spec, err := keystrokeSpecForRune(r)
		if err != nil {
			return nil, nil, fmt.Errorf("rune %q: %w", string(r), err)
		}
		if err := input.SendKeyComboToPID(int32(pid), spec); err != nil {
			return nil, nil, fmt.Errorf("keystroke %q: %w", spec, err)
		}
		time.Sleep(delay)
	}
	return jsonResult(typeOutput{Chars: len([]rune(args.Text))})
}

func keystrokeSpecForRune(r rune) (string, error) {
	switch r {
	case ' ':
		return "space", nil
	case '\n':
		return "return", nil
	case '\t':
		return "tab", nil
	}
	if unicode.IsLetter(r) {
		lower := unicode.ToLower(r)
		if unicode.IsUpper(r) {
			return "shift+" + string(lower), nil
		}
		return string(lower), nil
	}
	if unicode.IsDigit(r) {
		return string(r), nil
	}
	switch r {
	case '-', '=', '.', ',', '/', ';', '\'', '[', ']', '\\', '`':
		return string(r), nil
	case '!':
		return "shift+1", nil
	case '@':
		return "shift+2", nil
	case '#':
		return "shift+3", nil
	case '$':
		return "shift+4", nil
	case '%':
		return "shift+5", nil
	case '^':
		return "shift+6", nil
	case '&':
		return "shift+7", nil
	case '*':
		return "shift+8", nil
	case '(':
		return "shift+9", nil
	case ')':
		return "shift+0", nil
	case '_':
		return "shift+-", nil
	case '+':
		return "shift+=", nil
	case ':':
		return "shift+;", nil
	case '"':
		return "shift+'", nil
	case '<':
		return "shift+,", nil
	case '>':
		return "shift+.", nil
	case '?':
		return "shift+/", nil
	case '~':
		return "shift+`", nil
	case '{':
		return "shift+[", nil
	case '}':
		return "shift+]", nil
	case '|':
		return "shift+\\", nil
	}
	return "", fmt.Errorf("unsupported character")
}

// ---------- iphone_action ----------

type actionInput struct {
	Action string `json:"action"`
}

type actionOutput struct {
	Action string `json:"action"`
	Spec   string `json:"spec"`
}

var actionMap = map[string]string{
	"home":           "cmd+1",    // verified from View > Home Screen.
	"app_switcher":   "cmd+2",    // verified from View > App Switcher.
	"spotlight":      "cmd+3",    // verified from View > Spotlight.
	"siri":           "cmd+s",    // guessed; no menu-bar binding found.
	"notifications":  "cmd+n",    // guessed; no menu-bar binding found.
	"control_center": "cmd+c",    // guessed; no menu-bar binding found.
	"back":           "cmd+left", // guessed; no menu-bar binding found.
}

func iphoneAction(_ context.Context, _ *mcp.CallToolRequest, args actionInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	spec, ok := actionMap[strings.ToLower(args.Action)]
	if !ok {
		return nil, nil, fmt.Errorf("unknown action %q (known: home, app_switcher, spotlight, siri, notifications, control_center, back)", args.Action)
	}
	pid, err := iphoneMirroringPID()
	if err != nil {
		return nil, nil, err
	}
	focusIPhoneMirroring(int32(pid), 0)
	if err := input.SendKeyComboToPID(int32(pid), spec); err != nil {
		return nil, nil, fmt.Errorf("keystroke: %w", err)
	}
	return jsonResult(actionOutput{Action: args.Action, Spec: spec})
}

// ---------- iphone_focus ----------

type focusInput struct {
	Raise *bool `json:"raise,omitempty"`
}

type focusOutput struct {
	PID             int64  `json:"pid"`
	WindowID        uint32 `json:"window_id"`
	FrontmostBefore string `json:"frontmost_before"`
	FrontmostAfter  string `json:"frontmost_after"`
	Raise           bool   `json:"raise"`
	Raised          bool   `json:"raised"`
}

func iphoneFocus(_ context.Context, _ *mcp.CallToolRequest, args focusInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	before, err := frontmostProcessName()
	if err != nil {
		return nil, nil, fmt.Errorf("frontmost before: %w", err)
	}
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return nil, nil, fmt.Errorf("find window: %w", err)
	}
	if win.OwnerPID == 0 {
		return nil, nil, fmt.Errorf("iPhone Mirroring window has no owner pid")
	}
	raise := true
	if args.Raise != nil {
		raise = *args.Raise
	}
	res := focusIPhoneMirroringWithOptions(int32(win.OwnerPID), win.ID, raise)
	if res.Err != nil {
		return nil, nil, res.Err
	}
	after, err := frontmostProcessName()
	if err != nil {
		return nil, nil, fmt.Errorf("frontmost after: %w", err)
	}
	return jsonResult(focusOutput{
		PID:             win.OwnerPID,
		WindowID:        win.ID,
		FrontmostBefore: before,
		FrontmostAfter:  after,
		Raise:           raise,
		Raised:          res.Raised,
	})
}

// ---------- iphone_wait_until ----------

type waitUntilInput struct {
	Text                string `json:"text,omitempty"`
	ViewportStableForMs *int   `json:"viewport_stable_for_ms,omitempty"`
	TimeoutMs           int    `json:"timeout_ms"`
}

type waitUntilOutput struct {
	Mode                string         `json:"mode"`
	Text                string         `json:"text,omitempty"`
	ViewportStableForMs int            `json:"viewport_stable_for_ms,omitempty"`
	Matched             string         `json:"matched,omitempty"`
	Hash                string         `json:"hash,omitempty"`
	ElapsedMs           int            `json:"elapsed_ms"`
	Polls               int            `json:"polls"`
	Last                describeOutput `json:"last"`
}

func iphoneWaitUntil(ctx context.Context, _ *mcp.CallToolRequest, args waitUntilInput) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	hasText := strings.TrimSpace(args.Text) != ""
	hasStable := args.ViewportStableForMs != nil
	if hasText == hasStable {
		return nil, nil, fmt.Errorf("set exactly one of text or viewport_stable_for_ms")
	}
	if args.TimeoutMs <= 0 {
		return nil, nil, fmt.Errorf("timeout_ms must be positive")
	}
	stableFor := 0
	if hasStable {
		stableFor = *args.ViewportStableForMs
		if stableFor <= 0 {
			return nil, nil, fmt.Errorf("viewport_stable_for_ms must be positive")
		}
	}
	start := time.Now()
	deadline := start.Add(time.Duration(args.TimeoutMs) * time.Millisecond)
	target := strings.ToLower(strings.TrimSpace(args.Text))
	var polls int
	var last *describeOutput
	var lastErr error
	var stableSince time.Time
	var lastHash uint64
	var haveHash bool
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		out, png, err := describeIPhone()
		polls++
		now := time.Now()
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
			last = &out
			if hasText {
				for _, h := range out.Hits {
					if strings.Contains(strings.ToLower(h.Text), target) {
						return jsonResult(waitUntilOutput{
							Mode:      "text",
							Text:      args.Text,
							Matched:   h.Text,
							ElapsedMs: int(now.Sub(start) / time.Millisecond),
							Polls:     polls,
							Last:      out,
						})
					}
				}
			} else {
				hash, err := imagehash.DHash8(png)
				if err != nil {
					lastErr = err
				} else {
					if !haveHash || hash != lastHash {
						lastHash = hash
						haveHash = true
						stableSince = now
					}
					if now.Sub(stableSince) >= time.Duration(stableFor)*time.Millisecond {
						return jsonResult(waitUntilOutput{
							Mode:                "viewport_stable",
							ViewportStableForMs: stableFor,
							Hash:                fmt.Sprintf("%016x", hash),
							ElapsedMs:           int(now.Sub(start) / time.Millisecond),
							Polls:               polls,
							Last:                out,
						})
					}
				}
			}
		}
		if !now.Before(deadline) {
			return nil, nil, waitTimeoutError(hasText, args.Text, stableFor, args.TimeoutMs, last, lastErr)
		}
		sleep := 200 * time.Millisecond
		if until := time.Until(deadline); until < sleep {
			sleep = until
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// ---------- iphone_zoom_in / iphone_zoom_out ----------

type zoomInput struct{}

type zoomOutput struct {
	Spec string `json:"spec"`
}

func iphoneZoomIn(ctx context.Context, req *mcp.CallToolRequest, args zoomInput) (*mcp.CallToolResult, any, error) {
	return iphoneZoom(ctx, req, args, "cmd+shift+=")
}

func iphoneZoomOut(ctx context.Context, req *mcp.CallToolRequest, args zoomInput) (*mcp.CallToolResult, any, error) {
	return iphoneZoom(ctx, req, args, "cmd+-")
}

func iphoneZoom(_ context.Context, _ *mcp.CallToolRequest, _ zoomInput, spec string) (*mcp.CallToolResult, any, error) {
	if err := requireAccessibility(); err != nil {
		return nil, nil, err
	}
	pid, err := iphoneMirroringPID()
	if err != nil {
		return nil, nil, err
	}
	focusIPhoneMirroring(int32(pid), 0)
	if err := input.SendKeyComboToPID(int32(pid), spec); err != nil {
		return nil, nil, fmt.Errorf("keystroke: %w", err)
	}
	return jsonResult(zoomOutput{Spec: spec})
}

// ---------- helpers ----------

func iphoneMirroringPID() (int64, error) {
	w, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return 0, fmt.Errorf("find iPhone Mirroring window: %w", err)
	}
	if w.OwnerPID == 0 {
		return 0, fmt.Errorf("iPhone Mirroring window has no owner pid")
	}
	return w.OwnerPID, nil
}

func describeIPhone() (describeOutput, []byte, error) {
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return describeOutput{}, nil, fmt.Errorf("find iPhone Mirroring window: %w", err)
	}
	png, imgW, imgH, err := ocrwindow.Capture(win)
	if err != nil {
		return describeOutput{}, nil, fmt.Errorf("capture: %w", err)
	}
	hits, err := ocrwindow.Recognize(png, imgW, imgH)
	if err != nil {
		return describeOutput{}, nil, fmt.Errorf("ocr: %w", err)
	}
	out := describeOutput{Window: windowForOutput(win)}
	out.Image.W, out.Image.H = imgW, imgH
	for _, h := range hits {
		out.Hits = append(out.Hits, describeHit{
			Text:       h.Text,
			Confidence: h.Confidence,
			NX:         float64(h.X) / float64(imgW),
			NY:         float64(h.Y) / float64(imgH),
			NW:         float64(h.W) / float64(imgW),
			NH:         float64(h.H) / float64(imgH),
		})
	}
	return out, png, nil
}

func waitTimeoutError(textMode bool, text string, stableFor, timeoutMs int, last *describeOutput, lastErr error) error {
	var mode string
	if textMode {
		mode = fmt.Sprintf("text %q", text)
	} else {
		mode = fmt.Sprintf("viewport stable for %dms", stableFor)
	}
	if last == nil {
		if lastErr != nil {
			return fmt.Errorf("timeout waiting for %s after %dms; last describe error: %w", mode, timeoutMs, lastErr)
		}
		return fmt.Errorf("timeout waiting for %s after %dms; no describe state", mode, timeoutMs)
	}
	body, err := json.Marshal(last)
	if err != nil {
		return fmt.Errorf("timeout waiting for %s after %dms; marshal last describe: %w", mode, timeoutMs, err)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s after %dms; last describe error: %v; last describe: %s", mode, timeoutMs, lastErr, body)
	}
	return fmt.Errorf("timeout waiting for %s after %dms; last describe: %s", mode, timeoutMs, body)
}

func screenPointForNormalized(nx, ny float64) (ocrwindow.Window, int, int, error) {
	if nx < 0 || nx > 1 || ny < 0 || ny > 1 {
		return ocrwindow.Window{}, 0, 0, fmt.Errorf("nx/ny must be in [0,1]")
	}
	win, err := ocrwindow.FindWindow(screenContinuityApp)
	if err != nil {
		return ocrwindow.Window{}, 0, 0, fmt.Errorf("find window: %w", err)
	}
	sx := int(win.X + nx*win.W)
	sy := int(win.Y + ny*win.H)
	return win, sx, sy, nil
}

func screenPointForNormalizedPointers(nx, ny *float64) (ocrwindow.Window, int, int, error) {
	if nx == nil || ny == nil {
		return ocrwindow.Window{}, 0, 0, fmt.Errorf("provide both nx and ny")
	}
	return screenPointForNormalized(*nx, *ny)
}

func dedupOCRHits(hits []ocrwindow.Hit, threshold int) []ocrwindow.Hit {
	out := make([]ocrwindow.Hit, 0, len(hits))
	for _, h := range hits {
		merged := false
		for i := range out {
			if absInt(hitCenterX(h)-hitCenterX(out[i])) <= threshold &&
				absInt(hitCenterY(h)-hitCenterY(out[i])) <= threshold {
				if h.Confidence > out[i].Confidence {
					out[i] = h
				}
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, h)
		}
	}
	return out
}

func hitCenterX(h ocrwindow.Hit) int { return h.X + h.W/2 }
func hitCenterY(h ocrwindow.Hit) int { return h.Y + h.H/2 }

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func windowForOutput(win ocrwindow.Window) windowOut {
	return windowOut{ID: win.ID, Title: win.Title, X: win.X, Y: win.Y, W: win.W, H: win.H}
}

type focusResult struct {
	Raised bool
	Err    error
}

func focusIPhoneMirroringWithOptions(pid int32, windowID uint32, raise bool) focusResult {
	if pid <= 0 {
		return focusResult{Err: fmt.Errorf("invalid pid %d", pid)}
	}
	var errs []string
	if err := skylightinput.ActivateWithoutRaise(pid, windowID); err != nil {
		errs = append(errs, fmt.Sprintf("activate without raise: %v", err))
	}
	time.Sleep(50 * time.Millisecond)
	front, err := frontmostProcessInfo()
	if err == nil && front.PID == int64(pid) {
		return focusResult{}
	}
	if err != nil {
		errs = append(errs, fmt.Sprintf("frontmost after no-raise: %v", err))
	}
	if !raise {
		if len(errs) == 0 {
			return focusResult{Err: fmt.Errorf("iPhone Mirroring is not frontmost after focus without raise")}
		}
		return focusResult{Err: fmt.Errorf("%s", strings.Join(errs, "; "))}
	}
	if err := raiseProcess(int64(pid)); err != nil {
		errs = append(errs, fmt.Sprintf("raise: %v", err))
		return focusResult{Err: fmt.Errorf("%s", strings.Join(errs, "; "))}
	}
	time.Sleep(120 * time.Millisecond)
	front, err = frontmostProcessInfo()
	if err != nil {
		errs = append(errs, fmt.Sprintf("frontmost after raise: %v", err))
		return focusResult{Raised: true, Err: fmt.Errorf("%s", strings.Join(errs, "; "))}
	}
	if front.PID != int64(pid) {
		errs = append(errs, fmt.Sprintf("frontmost after raise is %s (%d)", front.Name, front.PID))
		return focusResult{Raised: true, Err: fmt.Errorf("%s", strings.Join(errs, "; "))}
	}
	return focusResult{Raised: true}
}

type processInfo struct {
	Name string
	PID  int64
}

func frontmostProcessName() (string, error) {
	info, err := frontmostProcessInfo()
	if err != nil {
		return "", err
	}
	return info.Name, nil
}

func frontmostProcessInfo() (processInfo, error) {
	out, err := exec.Command("osascript", "-e", `tell application "System Events" to name of first process whose frontmost is true`).Output()
	if err != nil {
		return processInfo{}, err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return processInfo{}, fmt.Errorf("empty frontmost process name")
	}
	pidOut, err := exec.Command("osascript", "-e", `tell application "System Events" to unix id of first process whose frontmost is true`).Output()
	if err != nil {
		return processInfo{}, err
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(string(pidOut)), 10, 64)
	if err != nil {
		return processInfo{}, fmt.Errorf("parse frontmost pid: %w", err)
	}
	return processInfo{Name: name, PID: pid}, nil
}

func raiseProcess(pid int64) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	script := fmt.Sprintf(`tell application "System Events" to set frontmost of first process whose unix id is %d to true`, pid)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// clickIPhoneMirroring posts a left-click at absolute screen point (sx, sy)
// to iPhone Mirroring's host process. Transport is selected via
// IPHONEMIRROR_MOUSE_TRANSPORT:
//
//   - "cgpost" (default): public CGEventPost on kCGHIDEventTap. Verified
//     2026-04-28 to be the path that iPhone Mirroring's input filter
//     accepts: per-pid posts (cgpostpid below) silently drop, but
//     hardware-tap events with the existing 3-step move + ClickState=1
//     stamps reach the receiver. Same path used by ax_click against
//     other apps.
//   - "cgpostpid": CGEventPostToPid - the public SPI the keyboard path
//     uses (input.SendKeyComboToPID). Works for keystrokes but the
//     mouse-side gate on iPhone Mirroring is stricter; events drop.
//     Kept for A/B harness only.
//   - "skylight": SLEventPostToPid + yabai focus-without-raise (cua-driver
//     recipe). Kept for A/B / future fallback. Requires win.OwnerPID.
//
// cgpost ignores win.OwnerPID; the others fall back to cgpost when pid
// is unknown.
func clickIPhoneMirroring(win ocrwindow.Window, sx, sy int) error {
	transport := strings.ToLower(strings.TrimSpace(os.Getenv("IPHONEMIRROR_MOUSE_TRANSPORT")))
	if transport == "" {
		// cgpost (kCGHIDEventTap) is the default because empirically
		// iPhone Mirroring's input filter accepts hardware-tap events
		// but silently drops per-pid posted events. cgpostpid mirrors
		// the keyboard path but the receiver gates mouse on a stricter
		// trust path than keyboard. Override via env if you need to A/B.
		transport = "cgpost"
	}
	switch transport {
	case "cgpostpid":
		if win.OwnerPID <= 0 {
			log.Printf("clickIPhoneMirroring: pid unknown, falling back to cgpost")
			return input.ClickScreenPoint(sx, sy)
		}
		return clickViaCGEventPostToPid(int32(win.OwnerPID), win, sx, sy)
	case "skylight":
		if win.OwnerPID <= 0 {
			log.Printf("clickIPhoneMirroring: pid unknown, falling back to cgpost")
			return input.ClickScreenPoint(sx, sy)
		}
		screen := skylightinput.Point{X: float64(sx), Y: float64(sy)}
		local := skylightinput.Point{X: float64(sx) - win.X, Y: float64(sy) - win.Y}
		if err := skylightinput.MouseClick(int32(win.OwnerPID), screen, local, win.ID, 1); err != nil {
			log.Printf("skylightinput.MouseClick failed (%v); falling back to cgpost", err)
			return input.ClickScreenPoint(sx, sy)
		}
		return nil
	case "cgpost":
		return input.ClickScreenPoint(sx, sy)
	default:
		log.Printf("unknown IPHONEMIRROR_MOUSE_TRANSPORT=%q; using cgpost", transport)
		return input.ClickScreenPoint(sx, sy)
	}
}

// clickViaCGEventPostToPid posts a left-click using CGEventPostToPid, the
// same public SPI used by input.SendKeyComboToPID for keystrokes. The
// keyboard path is empirically known to reach iPhone Mirroring; this is
// the matching mouse path. Stamps ClickState=1, Subtype=3, ButtonNumber=0
// so receivers that gate on those fields don't drop the event.
func clickViaCGEventPostToPid(pid int32, win ocrwindow.Window, sx, sy int) error {
	pt := corefoundation.CGPoint{X: float64(sx), Y: float64(sy)}
	src := coregraphics.CGEventSourceCreate(coregraphics.KCGEventSourceStateHIDSystemState)
	if src == 0 {
		return fmt.Errorf("CGEventSourceCreate failed")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(src))

	stamp := func(ev coregraphics.CGEventRef) {
		coregraphics.CGEventSetIntegerValueField(ev, coregraphics.KCGMouseEventButtonNumber, 0)
		coregraphics.CGEventSetIntegerValueField(ev, coregraphics.CGEventField(8), 3)
		coregraphics.CGEventSetIntegerValueField(ev, coregraphics.KCGMouseEventClickState, 1)
		// Deliberately do NOT stamp KCGMouseEventWindowUnderMousePointer
		// with a real window id. cua-driver's MouseInput.swift documents
		// that "passing a non-zero windowNumber causes the resulting
		// event to be rejected by the HID-tap dispatcher even when the
		// window genuinely owns the click point" - leave it 0 so the
		// receiver hit-tests on its own.
	}
	mk := func(t coregraphics.CGEventType) coregraphics.CGEventRef {
		ev := coregraphics.CGEventCreateMouseEvent(src, t, pt, coregraphics.KCGMouseButtonLeft)
		if ev != 0 {
			stamp(ev)
		}
		return ev
	}
	move := mk(coregraphics.KCGEventMouseMoved)
	if move == 0 {
		return fmt.Errorf("create mouseMoved failed")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(move))
	down := mk(coregraphics.KCGEventLeftMouseDown)
	if down == 0 {
		return fmt.Errorf("create mouseDown failed")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(down))
	up := mk(coregraphics.KCGEventLeftMouseUp)
	if up == 0 {
		return fmt.Errorf("create mouseUp failed")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(up))

	log.Printf("clickViaCGEventPostToPid pid=%d wid=%d sx=%d sy=%d", pid, win.ID, sx, sy)
	coregraphics.CGEventPostToPid(pid, move)
	time.Sleep(15 * time.Millisecond)
	coregraphics.CGEventPostToPid(pid, down)
	time.Sleep(time.Millisecond)
	coregraphics.CGEventPostToPid(pid, up)
	return nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, v, nil
}

// ---------- main ----------

func init() {
	// libdispatch's _dispatch_main() asserts it is called from the
	// application's main OS thread. Go's main goroutine starts on the
	// process's main thread but the Go scheduler is free to migrate it
	// unless we pin it. Calling LockOSThread in init runs before main and
	// runs on goroutine 1, which is the goroutine that will end up calling
	// dispatch.Main() at the bottom of main().
	runtime.LockOSThread()
}

func main() {
	cfg := macgo.NewConfig().
		WithAppName("iphonemirror-mcp").
		WithPermissions(permissions.ScreenRecording, permissions.Accessibility).
		WithUsageDescription("NSScreenCaptureUsageDescription",
			"iphonemirror-mcp captures the iPhone Mirroring window so Vision OCR can surface iOS UI text to LLM clients.").
		WithUsageDescription("NSAccessibilityUsageDescription",
			"iphonemirror-mcp routes synthetic clicks and key events to the iPhone Mirroring app.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory)
	cfg.BundleID = "dev.tmc.iphonemirror-mcp"
	cfg = macsigning.Configure(cfg)
	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}
	// Trigger TCC registration for Screen Recording on first launch.
	coregraphics.CGRequestScreenCaptureAccess()

	// Disable ghostcursor: an MCP server has no terminal context to flash a
	// visual cursor in. Disabling it sidesteps the runOnMain main-queue
	// dispatch path entirely, which is the safe-by-default posture for a
	// headless server. The dispatch.Main() drive at the bottom keeps the
	// queue serviceable in case some other code path uses it.
	ghostcursor.Configure(ghostcursor.Config{Enabled: false})

	// Resolve SkyLight SPIs eagerly and report status. A silent fallback
	// to CGEventPost on every iphone_tap is the failure mode we are most
	// likely to hit in the field (see commit history for v0.1.0); logging
	// the resolver state here makes that diagnosable from MCP stderr.
	if err := skylightinput.Available(); err != nil {
		log.Printf("skylightinput: NOT available - %v (status: %s); iphone_tap will fall back to CGEventPost",
			err, skylightinput.Status())
	} else {
		log.Printf("skylightinput: available (%s)", skylightinput.Status())
	}
	// Accessibility TCC is required for synthesised events to actually
	// reach foreground processes. Without it, SLEventPostToPid still
	// returns ok but the input layer drops the events. Report the live
	// state at startup so a missing grant is visible in the spy log.
	if axuiautomation.AXIsProcessTrusted() {
		log.Printf("accessibility: granted (events should reach iPhone Mirroring)")
	} else {
		// Loud banner. Without Accessibility, every CGEvent post silently
		// no-ops, so iphone_tap/iphone_swipe/iphone_type return success
		// but the receiver sees nothing. cdhash invalidates on every
		// rebuild of the .app bundle (ad-hoc signed), so this missing-
		// grant state is the most common rebuild-time regression.
		// Re-grant by toggling iphonemirror-mcp in
		//   System Settings -> Privacy & Security -> Accessibility.
		log.Printf("============================================================")
		log.Printf("ACCESSIBILITY: *NOT GRANTED* - iphone_tap/swipe/type WILL DROP")
		log.Printf("Re-grant in System Settings -> Privacy & Security -> Accessibility.")
		log.Printf("Tools will return an error until granted (no silent drops).")
		log.Printf("============================================================")
		// Floating Open-Settings/Reset-TCC window. Polls until granted.
		ui.CheckTrust()
	}
	// Trace every SkyLight tap call. Driver runs under mcpspy which only
	// surfaces JSON-RPC; this logs to stderr where the spy can't filter
	// them, so the per-call recipe is auditable post-hoc.
	skylightinput.Trace = func(event string, fields map[string]any) {
		log.Printf("skylightinput.trace event=%s fields=%+v", event, fields)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "iphonemirror-mcp",
		Version: "0.1.17",
	}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_describe",
		Description: `Capture the iPhone Mirroring window, run Apple Vision OCR, and return recognized text with normalized 0-1 coordinates relative to the captured image. The mirrored iPhone screen is opaque to the macOS Accessibility API; OCR is the path to seeing iOS UI text.`,
	}, iphoneDescribe)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_tap",
		Description: `Tap inside the iPhone Mirroring window. Provide either text (OCR find-and-tap) or normalized coords nx/ny in [0,1].`,
	}, iphoneTap)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_swipe",
		Description: `Drag from (nx1, ny1) to (nx2, ny2), normalized 0-1. Use to scroll lists, swipe between home pages, swipe up from bottom, etc.`,
	}, iphoneSwipe)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_type",
		Description: `Type a string into the focused iOS text field. Routes char-by-char; programmatic Cmd+V is blocked by Continuity. Supports ASCII letters, digits, common punctuation, space/return/tab.`,
	}, iphoneType)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_action",
		Description: `Trigger an iPhone Mirroring app shortcut. Action names: home, app_switcher, spotlight, back, notifications, control_center, siri.`,
	}, iphoneAction)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_focus",
		Description: `Focus iPhone Mirroring and return pid, window_id, and frontmost app names before and after. raise defaults true; raise:false only attempts focus without raising and errors if that does not make the app frontmost.`,
	}, iphoneFocus)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_wait_until",
		Description: `Poll iPhone Mirroring until text appears or the viewport stays visually stable for the requested duration, using a 200ms cadence and timeout_ms.`,
	}, iphoneWaitUntil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_long_press",
		Description: `Press-and-hold at normalized coord (nx, ny in [0,1]) for duration_ms (default 600). Use to open iOS contextual menus (long-press app icon, long-press canvas in drawing apps). Single-finger only. with_jitter:true injects a 1px move during the hold for gesture recognizers that filter zero-delta presses.`,
	}, iphoneLongPress)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_double_tap",
		Description: `Two consecutive taps at the same normalized coord. Use to enter text-edit on a label, double-tap-to-zoom in supporting apps. Single-finger only.`,
	}, iphoneDoubleTap)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_zoom_in",
		Description: `Send Cmd+Plus to iPhone Mirroring. Triggers UIKeyCommand-based zoom in iOS apps that implement keyboard zoom (Photos, Maps, Safari, Freeform). Apps without keyboard-zoom support will not respond - synthetic pinch-to-zoom is not currently supported.`,
	}, iphoneZoomIn)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_zoom_out",
		Description: `Send Cmd+Minus to iPhone Mirroring. Triggers UIKeyCommand-based zoom in iOS apps that implement keyboard zoom (Photos, Maps, Safari, Freeform). Apps without keyboard-zoom support will not respond - synthetic pinch-to-zoom is not currently supported.`,
	}, iphoneZoomOut)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "iphone_drag_and_drop",
		Description: `Long-press at (nx1, ny1), hold for hold_ms (default 600), drag to (nx2, ny2), then release. Use for iOS pickup-and-move gestures; single-finger only.`,
	}, iphoneDragAndDrop)

	// The MCP server runs in a sub-goroutine; the main goroutine drives the
	// libdispatch main queue. Tool handlers that synthesize input route
	// through internal/computeruse/input.ClickScreenPoint, which calls
	// internal/ghostcursor.PressAt, which dispatches visual-feedback work to
	// the main queue and synchronously waits for it to run. Without a main-
	// queue drainer here, those handlers deadlock. dispatch.Main() drains
	// libdispatch's main queue forever without requiring AppKit (which
	// SIGTRAPs in this exec context). See task #16 for the longer-term plan
	// to centralize this pattern across all MCP servers.
	go func() {
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Printf("server error: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	dispatch.Main()
}

package appstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

type captureGeometry struct {
	window   uint32
	rect     computeruse.CaptureRect
	displays []computeruse.DisplayInfo
}

func (g captureGeometry) equal(h captureGeometry) bool {
	return g.window == h.window && g.rect == h.rect && slices.Equal(g.displays, h.displays)
}

// stableWindowCapture requires consecutive matching geometry and image dimensions.
// Pixel content may change between captures, as it does for a clock or animation.
func stableWindowCapture(ctx context.Context, geometry func() (captureGeometry, error), capture func(context.Context, uint32) ([]byte, error)) ([]byte, *computeruse.ScreenshotInfo, error) {
	var previous captureGeometry
	var width, height int
	var decodeErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		before, err := geometry()
		if err != nil {
			return nil, nil, err
		}
		r := before.rect
		if before.window == 0 || !finiteRect(r) {
			return nil, nil, fmt.Errorf("invalid capture geometry")
		}
		data, err := capture(ctx, before.window)
		if err != nil {
			return nil, nil, err
		}
		// Decode the whole PNG: a valid header alone does not prove a usable image.
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			decodeErr = fmt.Errorf("decode window screenshot: %w", err)
			previous = captureGeometry{}
			continue
		}
		after, err := geometry()
		if err != nil {
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		w, h := img.Bounds().Dx(), img.Bounds().Dy()
		if before.equal(after) && previous.equal(before) && width == w && height == h {
			hash := sha256.Sum256(data)
			return data, &computeruse.ScreenshotInfo{
				ImageID: fmt.Sprintf("%x", hash), Width: w, Height: h, SourceKind: "window",
				TargetWindow: before.window, GlobalRect: r,
				ScaleX: float64(w) / r.Width, ScaleY: float64(h) / r.Height,
				Displays: slices.Clone(before.displays),
			}, nil
		}
		previous = captureGeometry{}
		if before.equal(after) {
			previous = before
		}
		width, height = w, h
	}
	return nil, nil, errors.Join(fmt.Errorf("window capture geometry did not stabilize"), decodeErr)
}

func finiteRect(r computeruse.CaptureRect) bool {
	for _, v := range []float64{r.X, r.Y, r.Width, r.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.Width > 0 && r.Height > 0
}

func captureWindow(ctx context.Context, window *axuiautomation.Element) ([]byte, *computeruse.ScreenshotInfo, error) {
	if window == nil {
		return nil, nil, fmt.Errorf("nil window")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return stableWindowCapture(ctx, func() (captureGeometry, error) {
		return windowCaptureGeometry(ctx, window)
	}, captureWindowPNG)
}

func windowCaptureGeometry(ctx context.Context, window *axuiautomation.Element) (captureGeometry, error) {
	// Frame performs two AX attribute requests, followed by one window-ID
	// request. Reserve a share of the deadline for each instead of allowing
	// each request to spend the whole remaining context budget.
	if err := boundAXCalls(ctx, window, 3); err != nil {
		return captureGeometry{}, err
	}
	frame := window.Frame()
	if err := ctx.Err(); err != nil {
		return captureGeometry{}, err
	}
	rect := computeruse.CaptureRect{X: frame.Origin.X, Y: frame.Origin.Y, Width: frame.Size.Width, Height: frame.Size.Height}
	if !finiteRect(rect) {
		return captureGeometry{}, fmt.Errorf("window frame unavailable")
	}
	if err := boundAXTimeout(ctx, window); err != nil {
		return captureGeometry{}, err
	}
	id := window.WindowID()
	if err := ctx.Err(); err != nil {
		return captureGeometry{}, err
	}
	g := captureGeometry{window: id, rect: rect}
	// A full buffer is ambiguous: reject rather than silently omit a display.
	var ids [64]coregraphics.CGDirectDisplayID
	var count uint32
	if code := coregraphics.CGGetActiveDisplayList(uint32(len(ids)), &ids[0], &count); code != 0 {
		return g, fmt.Errorf("list active displays: %d", code)
	}
	if count == 0 || count >= uint32(len(ids)) {
		return g, fmt.Errorf("invalid active display count %d", count)
	}
	for _, id := range ids[:count] {
		r := coregraphics.CGDisplayBounds(id)
		d := computeruse.DisplayInfo{ID: uint32(id), Bounds: computeruse.CaptureRect{X: r.Origin.X, Y: r.Origin.Y, Width: r.Size.Width, Height: r.Size.Height}, PixelWidth: int(coregraphics.CGDisplayPixelsWide(id)), PixelHeight: int(coregraphics.CGDisplayPixelsHigh(id))}
		if !finiteRect(d.Bounds) || d.PixelWidth <= 0 || d.PixelHeight <= 0 {
			return g, fmt.Errorf("invalid display geometry %d", id)
		}
		g.displays = append(g.displays, d)
	}
	slices.SortFunc(g.displays, func(a, b computeruse.DisplayInfo) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return g, nil
}

func captureWindowPNG(ctx context.Context, window uint32) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	f, err := os.CreateTemp("", "computer-use-window-*.png")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Close(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-l", strconv.FormatUint(uint64(window), 10), "-o", "-t", "png", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("capture window %d: %w: %s", window, err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(name)
}

// checkCaptureGeometry rejects observable drift across the AX tree walk. It does
// not make the image and tree atomic or detect a move away and back between reads.
func checkCaptureGeometry(info *computeruse.ScreenshotInfo, g captureGeometry) error {
	captured := captureGeometry{window: info.TargetWindow, rect: info.GlobalRect, displays: info.Displays}
	if !captured.equal(g) {
		return fmt.Errorf("window geometry changed during observation")
	}
	return nil
}

// CheckScreenshotGeometry checks the exact window ID, unrounded logical frame,
// and active display topology recorded in info. It does not capture pixels,
// verify process birth, or make dispatch atomic. The caller retains window's
// lifetime and handles authorization and intervention. The context bounds AX
// messaging waits. Nil arguments or invalid metadata return an error.
func CheckScreenshotGeometry(ctx context.Context, window *axuiautomation.Element, info *computeruse.ScreenshotInfo) error {
	return checkScreenshotGeometry(ctx, window, info)
}

func checkScreenshotGeometry(ctx context.Context, window *axuiautomation.Element, info *computeruse.ScreenshotInfo) error {
	if window == nil || info == nil {
		return fmt.Errorf("screenshot window and metadata required")
	}
	if info.SourceKind != "window" || info.ImageID == "" || info.TargetWindow == 0 || info.Width <= 0 || info.Height <= 0 || !finiteRect(info.GlobalRect) || len(info.Displays) == 0 {
		return fmt.Errorf("invalid screenshot metadata")
	}
	if info.ScaleX <= 0 || info.ScaleY <= 0 || math.IsInf(info.ScaleX, 0) || math.IsInf(info.ScaleY, 0) || info.ScaleX != float64(info.Width)/info.GlobalRect.Width || info.ScaleY != float64(info.Height)/info.GlobalRect.Height {
		return fmt.Errorf("invalid screenshot scale")
	}
	for i, d := range info.Displays {
		if d.ID == 0 || !finiteRect(d.Bounds) || d.PixelWidth <= 0 || d.PixelHeight <= 0 || (i > 0 && d.ID <= info.Displays[i-1].ID) {
			return fmt.Errorf("invalid screenshot displays")
		}
	}
	current, err := windowCaptureGeometry(ctx, window)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkCaptureGeometry(info, current)
}

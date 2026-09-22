package appstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestStableWindowCapture(t *testing.T) {
	base := captureGeometry{window: 42, rect: computeruse.CaptureRect{X: -100.5, Y: 20.25, Width: 10.5, Height: 8.25}, displays: []computeruse.DisplayInfo{{ID: 1}}}
	for _, tt := range []struct {
		name    string
		change  string
		wantErr bool
	}{
		{"dynamic pixels", "", false}, {"transient decode", "transient", false}, {"moving window", "rect", true}, {"display changed", "display", true}, {"window replaced", "window", true}, {"corrupt PNG", "png", true}, {"canceled", "cancel", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, captures := 0, 0
			geometry := func() (captureGeometry, error) {
				reads++
				g := base
				g.displays = append([]computeruse.DisplayInfo(nil), base.displays...)
				switch tt.change {
				case "rect":
					g.rect.X += float64(reads)
				case "display":
					g.displays[0].ID = uint32(reads)
				case "window":
					g.window += uint32(reads)
				}
				return g, nil
			}
			var last []byte
			data, info, err := stableWindowCapture(ctx, geometry, func(ctx context.Context, id uint32) ([]byte, error) {
				captures++
				if id != 42 && tt.change != "window" {
					t.Fatalf("window %d", id)
				}
				if tt.change == "cancel" {
					cancel()
				}
				if tt.change == "png" || (tt.change == "transient" && captures == 1) {
					return []byte("bad png"), nil
				}
				img := image.NewNRGBA(image.Rect(0, 0, 21, 33))
				img.SetNRGBA(0, 0, color.NRGBA{R: uint8(captures), A: 255})
				var b bytes.Buffer
				if err := png.Encode(&b, img); err != nil {
					return nil, err
				}
				last = b.Bytes()
				return last, nil
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if tt.wantErr {
				if data != nil || info != nil {
					t.Fatal("failed capture returned data")
				}
				return
			}
			wantCaptures := 2
			if tt.change == "transient" {
				wantCaptures = 3
			}
			if captures != wantCaptures || !bytes.Equal(data, last) {
				t.Fatalf("captures=%d, wrong final bytes=%v", captures, !bytes.Equal(data, last))
			}
			if info.GlobalRect != base.rect || info.ScaleX != 2 || info.ScaleY != 4 || info.ImageID != fmt.Sprintf("%x", sha256.Sum256(data)) {
				t.Fatalf("metadata=%+v", info)
			}
		})
	}
}

func TestCaptureInvalidGeometry(t *testing.T) {
	for _, tt := range []struct {
		name   string
		window uint32
		rect   computeruse.CaptureRect
	}{
		{"zero window", 0, computeruse.CaptureRect{Width: 1, Height: 1}},
		{"zero width", 1, computeruse.CaptureRect{Height: 1}},
		{"negative height", 1, computeruse.CaptureRect{Width: 1, Height: -1}},
		{"nan origin", 1, computeruse.CaptureRect{X: math.NaN(), Width: 1, Height: 1}},
		{"infinite width", 1, computeruse.CaptureRect{Width: math.Inf(1), Height: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := stableWindowCapture(context.Background(), func() (captureGeometry, error) { return captureGeometry{window: tt.window, rect: tt.rect}, nil }, func(context.Context, uint32) ([]byte, error) {
				t.Fatal("capture called with invalid geometry")
				return nil, nil
			})
			if err == nil {
				t.Fatal("invalid geometry accepted")
			}
		})
	}
}

func TestCaptureDimensionStability(t *testing.T) {
	for _, tt := range []struct {
		name    string
		widths  []int
		wantErr bool
	}{
		{"settles", []int{9, 10, 10}, false},
		{"keeps changing", []int{9, 10, 11}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := 0
			_, info, err := stableWindowCapture(context.Background(), func() (captureGeometry, error) {
				return captureGeometry{window: 1, rect: computeruse.CaptureRect{Width: 5, Height: 5}}, nil
			}, func(context.Context, uint32) ([]byte, error) {
				var b bytes.Buffer
				img := image.NewNRGBA(image.Rect(0, 0, tt.widths[n], 10))
				n++
				err := png.Encode(&b, img)
				return b.Bytes(), err
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
			if n != 3 {
				t.Fatalf("captures=%d, want3", n)
			}
			if err == nil && (info.Width != 10 || info.ScaleX != 2) {
				t.Fatalf("metadata=%+v", info)
			}
		})
	}
}

func TestCaptureGeometryAfterTraversal(t *testing.T) {
	info := &computeruse.ScreenshotInfo{TargetWindow: 7, GlobalRect: computeruse.CaptureRect{X: 1.25, Y: 2.5, Width: 10, Height: 20}, Displays: []computeruse.DisplayInfo{{ID: 1}}}
	for _, tt := range []struct {
		name    string
		change  func(*captureGeometry)
		wantErr bool
	}{
		{"unchanged", func(*captureGeometry) {}, false},
		{"fractional move", func(g *captureGeometry) { g.rect.X += 0.125 }, true},
		{"window replaced", func(g *captureGeometry) { g.window++ }, true},
		{"display replaced", func(g *captureGeometry) { g.displays[0].ID++ }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := captureGeometry{window: info.TargetWindow, rect: info.GlobalRect, displays: append([]computeruse.DisplayInfo(nil), info.Displays...)}
			tt.change(&g)
			if err := checkCaptureGeometry(info, g); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

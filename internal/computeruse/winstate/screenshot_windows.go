//go:build windows

package winstate

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	biRGB               = 0
	dibRGBColors        = 0
	pwRenderFullContent = 0x00000002
	srcCopy             = 0x00cc0020
)

var (
	gdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procSelectObject           = gdi32.NewProc("SelectObject")

	procGetWindowDC = user32.NewProc("GetWindowDC")
	procPrintWindow = user32.NewProc("PrintWindow")
	procReleaseDC   = user32.NewProc("ReleaseDC")
)

func captureWindowPNG(ctx context.Context, win Window) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if win.Handle == 0 {
		return nil, fmt.Errorf("missing window handle")
	}
	if win.Rect.Width <= 0 || win.Rect.Height <= 0 {
		return nil, fmt.Errorf("window has empty bounds")
	}
	return captureWindowBitmap(win.Handle, win.Rect.Width, win.Rect.Height)
}

func captureWindowBitmap(hwnd uintptr, width, height int) ([]byte, error) {
	windowDC, _, err := procGetWindowDC.Call(hwnd)
	if windowDC == 0 {
		return nil, winCallError("get window dc", err)
	}
	defer procReleaseDC.Call(hwnd, windowDC)

	memDC, _, err := procCreateCompatibleDC.Call(windowDC)
	if memDC == 0 {
		return nil, winCallError("create compatible dc", err)
	}
	defer procDeleteDC.Call(memDC)

	bitmap, _, err := procCreateCompatibleBitmap.Call(windowDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, winCallError("create compatible bitmap", err)
	}
	defer procDeleteObject.Call(bitmap)

	oldObject, _, err := procSelectObject.Call(memDC, bitmap)
	if oldObject == 0 {
		return nil, winCallError("select compatible bitmap", err)
	}
	defer procSelectObject.Call(memDC, oldObject)

	if r1, _, _ := procPrintWindow.Call(hwnd, memDC, uintptr(pwRenderFullContent)); r1 == 0 {
		r1, _, err := procBitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height), windowDC, 0, 0, uintptr(srcCopy))
		if r1 == 0 {
			return nil, winCallError("capture window pixels", err)
		}
	}
	return bitmapPNG(memDC, bitmap, width, height)
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

func bitmapPNG(dc, bitmap uintptr, width, height int) ([]byte, error) {
	raw := make([]byte, width*height*4)
	info := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(width),
			Height:      -int32(height),
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
			SizeImage:   uint32(len(raw)),
		},
	}
	r1, _, err := procGetDIBits.Call(
		dc,
		bitmap,
		0,
		uintptr(height),
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
	)
	if r1 == 0 {
		return nil, winCallError("read window pixels", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for src, dst := 0, 0; src < len(raw); src, dst = src+4, dst+4 {
		img.Pix[dst+0] = raw[src+2]
		img.Pix[dst+1] = raw[src+1]
		img.Pix[dst+2] = raw[src+0]
		img.Pix[dst+3] = 0xff
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encode window screenshot: %w", err)
	}
	return out.Bytes(), nil
}

func winCallError(action string, err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno != 0 {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s failed", action)
}

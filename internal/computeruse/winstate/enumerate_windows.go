//go:build windows

package winstate

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
)

func enumerateWindows(ctx context.Context) ([]Window, error) {
	var out []Window
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if ctx.Err() != nil {
			return 0
		}
		win, ok := readWindow(hwnd)
		if ok {
			out = append(out, win)
		}
		return 1
	})
	r1, _, err := procEnumWindows.Call(callback, 0)
	if r1 == 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errno, ok := err.(syscall.Errno); ok && errno != 0 {
			return nil, fmt.Errorf("enum windows: %w", err)
		}
		return nil, fmt.Errorf("enum windows failed")
	}
	return out, nil
}

func readWindow(hwnd uintptr) (Window, bool) {
	if r1, _, _ := procIsWindowVisible.Call(hwnd); r1 == 0 {
		return Window{}, false
	}
	title := windowText(hwnd)
	if title == "" {
		return Window{}, false
	}
	rect, ok := windowRect(hwnd)
	if !ok || rect.Width <= 0 || rect.Height <= 0 {
		return Window{}, false
	}
	pid := windowPID(hwnd)
	return Window{
		Handle:      hwnd,
		PID:         int(pid),
		Title:       title,
		ProcessName: processName(pid),
		Rect:        rect,
	}, true
}

func windowText(hwnd uintptr) string {
	r1, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if r1 == 0 {
		return ""
	}
	buf := make([]uint16, int(r1)+1)
	r1, _, _ = procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r1 == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:int(r1)])
}

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func windowRect(hwnd uintptr) (Rect, bool) {
	var rect winRect
	r1, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if r1 == 0 {
		return Rect{}, false
	}
	return Rect{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}, true
}

func windowPID(hwnd uintptr) uint32 {
	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

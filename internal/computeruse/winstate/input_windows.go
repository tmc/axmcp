//go:build windows

package winstate

import (
	"context"
	"fmt"
	"math"
	"unicode/utf16"
	"unsafe"

	"github.com/tmc/axmcp/internal/computeruse/coords"
)

const (
	winInputMouse = 0

	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmChar        = 0x0102

	mkLButton = 0x0001
	mkRButton = 0x0002
	mkMButton = 0x0010

	mouseEventFLeftDown   = 0x0002
	mouseEventFLeftUp     = 0x0004
	mouseEventFRightDown  = 0x0008
	mouseEventFRightUp    = 0x0010
	mouseEventFMiddleDown = 0x0020
	mouseEventFMiddleUp   = 0x0040

	keyDownLParam = 1
	keyUpLParam   = 1 | 1<<30 | 1<<31
)

var (
	procPostMessageW        = user32.NewProc("PostMessageW")
	procScreenToClient      = user32.NewProc("ScreenToClient")
	procSendInput           = user32.NewProc("SendInput")
	procSetCursorPos        = user32.NewProc("SetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

type winPoint struct {
	X int32
	Y int32
}

type winInput struct {
	Type  uint32
	Mouse winMouseInput
}

type winMouseInput struct {
	DX        int32
	DY        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

func sendWindowInput(ctx context.Context, action inputAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch action.Kind {
	case inputClick:
		return postClick(ctx, action)
	case inputDrag:
		return postDrag(ctx, action)
	case inputKey:
		return postKey(ctx, action)
	case inputText:
		return postText(ctx, action)
	default:
		return fmt.Errorf("unknown input action %d", action.Kind)
	}
}

func postClick(ctx context.Context, action inputAction) error {
	if action.Foreground {
		return foregroundClick(ctx, action)
	}
	down, up, flag, err := mouseMessages(action.Button)
	if err != nil {
		return err
	}
	point, err := clientPoint(action.Target, action.Start)
	if err != nil {
		return err
	}
	if err := postMouseMessage(ctx, action.Target, wmMouseMove, 0, point); err != nil {
		return err
	}
	for range normalizeClickCount(action.ClickCount) {
		if err := postMouseMessage(ctx, action.Target, down, flag, point); err != nil {
			return err
		}
		if err := postMouseMessage(ctx, action.Target, up, 0, point); err != nil {
			return err
		}
	}
	return nil
}

func foregroundClick(ctx context.Context, action inputAction) error {
	down, up, err := foregroundMouseFlags(action.Button)
	if err != nil {
		return err
	}
	if err := setForegroundWindow(ctx, action.Target); err != nil {
		return err
	}
	if err := setCursorPos(ctx, action.Start); err != nil {
		return err
	}
	for range normalizeClickCount(action.ClickCount) {
		if err := sendMouseInput(ctx, down); err != nil {
			return err
		}
		if err := sendMouseInput(ctx, up); err != nil {
			return err
		}
	}
	return nil
}

func postDrag(ctx context.Context, action inputAction) error {
	down, up, flag, err := mouseMessages(action.Button)
	if err != nil {
		return err
	}
	start, err := clientPoint(action.Target, action.Start)
	if err != nil {
		return err
	}
	end, err := clientPoint(action.Target, action.End)
	if err != nil {
		return err
	}
	if err := postMouseMessage(ctx, action.Target, wmMouseMove, 0, start); err != nil {
		return err
	}
	if err := postMouseMessage(ctx, action.Target, down, flag, start); err != nil {
		return err
	}
	if err := postMouseMessage(ctx, action.Target, wmMouseMove, flag, end); err != nil {
		return err
	}
	return postMouseMessage(ctx, action.Target, up, 0, end)
}

func postKey(ctx context.Context, action inputAction) error {
	key, err := parseWindowsKey(action.Key)
	if err != nil {
		return err
	}
	for _, mod := range key.Modifiers {
		if err := postKeyMessage(ctx, action.Target, wmKeyDown, mod, keyDownLParam); err != nil {
			return err
		}
	}
	releaseModifiers := func() error {
		for i := len(key.Modifiers) - 1; i >= 0; i-- {
			if err := postKeyMessage(ctx, action.Target, wmKeyUp, key.Modifiers[i], keyUpLParam); err != nil {
				return err
			}
		}
		return nil
	}
	if err := postKeyMessage(ctx, action.Target, wmKeyDown, key.VK, keyDownLParam); err != nil {
		_ = releaseModifiers()
		return err
	}
	if key.Char != 0 && len(key.Modifiers) == 0 {
		if err := postKeyMessage(ctx, action.Target, wmChar, uint16(key.Char), keyDownLParam); err != nil {
			_ = releaseModifiers()
			return err
		}
	}
	if err := postKeyMessage(ctx, action.Target, wmKeyUp, key.VK, keyUpLParam); err != nil {
		_ = releaseModifiers()
		return err
	}
	return releaseModifiers()
}

func postText(ctx context.Context, action inputAction) error {
	for _, unit := range utf16.Encode([]rune(action.Text)) {
		if err := postKeyMessage(ctx, action.Target, wmChar, unit, keyDownLParam); err != nil {
			return err
		}
	}
	return nil
}

func setForegroundWindow(ctx context.Context, hwnd uintptr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r1, _, err := procSetForegroundWindow.Call(hwnd)
	if r1 == 0 {
		return winCallError("set foreground window", err)
	}
	return nil
}

func setCursorPos(ctx context.Context, point coords.ScreenPoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if point.X < math.MinInt32 || point.X > math.MaxInt32 || point.Y < math.MinInt32 || point.Y > math.MaxInt32 {
		return fmt.Errorf("screen point outside Win32 range")
	}
	r1, _, err := procSetCursorPos.Call(winIntArg(point.X), winIntArg(point.Y))
	if r1 == 0 {
		return winCallError("set cursor position", err)
	}
	return nil
}

func sendMouseInput(ctx context.Context, flags uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	inputs := []winInput{{
		Type: winInputMouse,
		Mouse: winMouseInput{
			Flags: flags,
		},
	}}
	r1, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if r1 != uintptr(len(inputs)) {
		return winCallError("send mouse input", err)
	}
	return nil
}

func clientPoint(hwnd uintptr, point coords.ScreenPoint) (winPoint, error) {
	if point.X < math.MinInt32 || point.X > math.MaxInt32 || point.Y < math.MinInt32 || point.Y > math.MaxInt32 {
		return winPoint{}, fmt.Errorf("screen point outside Win32 range")
	}
	out := winPoint{X: int32(point.X), Y: int32(point.Y)}
	r1, _, err := procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return winPoint{}, winCallError("convert screen point to client", err)
	}
	if out.X < math.MinInt16 || out.X > math.MaxInt16 || out.Y < math.MinInt16 || out.Y > math.MaxInt16 {
		return winPoint{}, fmt.Errorf("client point outside Win32 mouse-message range")
	}
	return out, nil
}

func postMouseMessage(ctx context.Context, hwnd uintptr, msg uint32, wparam uintptr, point winPoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r1, _, err := procPostMessageW.Call(hwnd, uintptr(msg), wparam, mouseLParam(point))
	if r1 == 0 {
		return winCallError("post mouse message", err)
	}
	return nil
}

func postKeyMessage(ctx context.Context, hwnd uintptr, msg uint32, wparam uint16, lparam uintptr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r1, _, err := procPostMessageW.Call(hwnd, uintptr(msg), uintptr(wparam), lparam)
	if r1 == 0 {
		return winCallError("post key message", err)
	}
	return nil
}

func mouseMessages(button mouseButton) (down, up uint32, flag uintptr, err error) {
	switch button {
	case mouseLeft:
		return wmLButtonDown, wmLButtonUp, mkLButton, nil
	case mouseRight:
		return wmRButtonDown, wmRButtonUp, mkRButton, nil
	case mouseMiddle:
		return wmMButtonDown, wmMButtonUp, mkMButton, nil
	default:
		return 0, 0, 0, fmt.Errorf("unknown mouse button %d", button)
	}
}

func winIntArg(v int) uintptr {
	return uintptr(uint32(int32(v)))
}

func foregroundMouseFlags(button mouseButton) (down, up uint32, err error) {
	switch button {
	case mouseLeft:
		return mouseEventFLeftDown, mouseEventFLeftUp, nil
	case mouseRight:
		return mouseEventFRightDown, mouseEventFRightUp, nil
	case mouseMiddle:
		return mouseEventFMiddleDown, mouseEventFMiddleUp, nil
	default:
		return 0, 0, fmt.Errorf("unknown mouse button %d", button)
	}
}

func mouseLParam(point winPoint) uintptr {
	x := uint32(uint16(int16(point.X)))
	y := uint32(uint16(int16(point.Y)))
	return uintptr(x | y<<16)
}

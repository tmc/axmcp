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

	keyDownLParam = 1
	keyUpLParam   = 1 | 1<<30 | 1<<31
)

var (
	procPostMessageW   = user32.NewProc("PostMessageW")
	procScreenToClient = user32.NewProc("ScreenToClient")
)

type winPoint struct {
	X int32
	Y int32
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

func mouseLParam(point winPoint) uintptr {
	x := uint32(uint16(int16(point.X)))
	y := uint32(uint16(int16(point.Y)))
	return uintptr(x | y<<16)
}

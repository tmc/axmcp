//go:build !darwin

package input

import (
	"fmt"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
)

type LocalPoint struct {
	X int
	Y int
}

type KeyCombo struct {
	KeyCode uint16
	Shift   bool
	Control bool
	Option  bool
	Command bool
	Label   string
}

var knownKeys = map[string]uint16{
	"return": 0x24, "enter": 0x24, "tab": 0x30, "escape": 0x35, "esc": 0x35,
	"delete": 0x33, "backspace": 0x33, "space": 0x31,
	"up": 0x7E, "down": 0x7D, "left": 0x7B, "right": 0x7C,
	"home": 0x73, "end": 0x77, "pageup": 0x74, "pagedown": 0x79,
	"-": 0x1B, "=": 0x18,
	"0": 0x1D, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15,
	"5": 0x17, "6": 0x16, "7": 0x1A, "8": 0x1C, "9": 0x19,
	"a": 0x00, "b": 0x0B, "c": 0x08, "d": 0x02, "e": 0x0E,
	"f": 0x03, "g": 0x05, "h": 0x04, "i": 0x22, "j": 0x26,
	"k": 0x28, "l": 0x25, "m": 0x2E, "n": 0x2D, "o": 0x1F,
	"p": 0x23, "q": 0x0C, "r": 0x0F, "s": 0x01, "t": 0x11,
	"u": 0x20, "v": 0x09, "w": 0x0D, "x": 0x07, "y": 0x10, "z": 0x06,
}

func ScreenshotPointToWindowLocal(window computeruse.WindowInfo, x, y int) (LocalPoint, error) {
	point, err := coords.ScreenshotPointToWindowLocal(window, x, y)
	return LocalPoint{X: point.X, Y: point.Y}, err
}

func ParseKeyCombo(spec string) (KeyCombo, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return KeyCombo{}, fmt.Errorf("keys are required")
	}
	parts := strings.Split(spec, "+")
	var combo KeyCombo
	for i, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if i < len(parts)-1 {
			switch part {
			case "cmd", "command", "super", "meta":
				combo.Command = true
				continue
			case "ctrl", "control":
				combo.Control = true
				continue
			case "alt", "option":
				combo.Option = true
				continue
			case "shift":
				combo.Shift = true
				continue
			}
		}
		keyCode, ok := knownKeys[part]
		if !ok {
			return KeyCombo{}, fmt.Errorf("unsupported key %q", part)
		}
		combo.KeyCode = keyCode
		combo.Label = part
	}
	if combo.Label == "" {
		return KeyCombo{}, fmt.Errorf("missing key in %q", spec)
	}
	return combo, nil
}

func SendKeyCombo(spec string) error {
	if _, err := ParseKeyCombo(spec); err != nil {
		return err
	}
	return computeruse.PlatformUnsupported("send key combo")
}

func SendKeyComboToPID(pid int32, spec string) error {
	if pid <= 0 {
		return fmt.Errorf("pid must be positive")
	}
	if _, err := ParseKeyCombo(spec); err != nil {
		return err
	}
	return computeruse.PlatformUnsupported("send key combo to pid")
}

func ClickScreenPoint(int, int) error {
	return computeruse.PlatformUnsupported("click screen point")
}

func MultiClickScreenPoint(int, int, int) error {
	return computeruse.PlatformUnsupported("click screen point")
}

package winstate

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	vkBack   uint16 = 0x08
	vkTab    uint16 = 0x09
	vkReturn uint16 = 0x0D
	vkShift  uint16 = 0x10
	vkCtrl   uint16 = 0x11
	vkAlt    uint16 = 0x12
	vkEscape uint16 = 0x1B
	vkSpace  uint16 = 0x20
	vkPrior  uint16 = 0x21
	vkNext   uint16 = 0x22
	vkEnd    uint16 = 0x23
	vkHome   uint16 = 0x24
	vkLeft   uint16 = 0x25
	vkUp     uint16 = 0x26
	vkRight  uint16 = 0x27
	vkDown   uint16 = 0x28
	vkDelete uint16 = 0x2E
	vkLWin   uint16 = 0x5B
)

type windowsKey struct {
	Modifiers []uint16
	VK        uint16
	Char      rune
}

func parseWindowsKey(key string) (windowsKey, error) {
	parts := strings.Split(strings.TrimSpace(key), "+")
	var out windowsKey
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return windowsKey{}, fmt.Errorf("invalid key %q", key)
		}
		if i+1 < len(parts) {
			mod, ok := windowsModifier(part)
			if !ok {
				return windowsKey{}, fmt.Errorf("unsupported Windows key modifier %q", part)
			}
			out.Modifiers = append(out.Modifiers, mod)
			continue
		}
		vk, char, err := windowsVirtualKey(part)
		if err != nil {
			return windowsKey{}, err
		}
		out.VK = vk
		out.Char = char
	}
	if out.VK == 0 {
		return windowsKey{}, fmt.Errorf("missing key")
	}
	return out, nil
}

func windowsModifier(name string) (uint16, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ctrl", "control":
		return vkCtrl, true
	case "alt", "option":
		return vkAlt, true
	case "shift":
		return vkShift, true
	case "super", "win", "windows", "cmd", "command":
		return vkLWin, true
	default:
		return 0, false
	}
}

func windowsVirtualKey(name string) (uint16, rune, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, 0, fmt.Errorf("missing key")
	}
	if r, size := utf8.DecodeRuneInString(name); r != utf8.RuneError && size == len(name) {
		switch {
		case r == ' ':
			return vkSpace, r, nil
		case r >= '0' && r <= '9':
			return uint16(r), r, nil
		case r >= 'a' && r <= 'z':
			return uint16(r - 'a' + 'A'), r, nil
		case r >= 'A' && r <= 'Z':
			return uint16(r), r, nil
		}
	}
	switch strings.ToLower(name) {
	case "backspace", "back_space", "back":
		return vkBack, 0, nil
	case "delete", "del":
		return vkDelete, 0, nil
	case "down":
		return vkDown, 0, nil
	case "end":
		return vkEnd, 0, nil
	case "enter", "return":
		return vkReturn, 0, nil
	case "esc", "escape":
		return vkEscape, 0, nil
	case "home":
		return vkHome, 0, nil
	case "left":
		return vkLeft, 0, nil
	case "pagedown", "page_down", "next":
		return vkNext, 0, nil
	case "pageup", "page_up", "prior":
		return vkPrior, 0, nil
	case "right":
		return vkRight, 0, nil
	case "space":
		return vkSpace, ' ', nil
	case "tab":
		return vkTab, 0, nil
	case "up":
		return vkUp, 0, nil
	default:
		return 0, 0, fmt.Errorf("unsupported Windows key %q", name)
	}
}

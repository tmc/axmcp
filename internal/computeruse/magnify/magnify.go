package magnify

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse/input"
)

// Strategy selects how a semantic magnify request is executed.
type Strategy string

const (
	// StrategyShortcut maps semantic magnify actions to standard app zoom
	// shortcuts.
	StrategyShortcut Strategy = "shortcut"

	// StrategyNative is reserved for future trackpad-style magnify injection.
	StrategyNative Strategy = "native"
)

// Action is a canonical semantic zoom or pinch direction.
type Action string

const (
	ActionIn    Action = "in"
	ActionOut   Action = "out"
	ActionReset Action = "reset"
)

// Shortcut identifies the standard app zoom shortcut for a semantic action.
type Shortcut struct {
	Keys  string
	Label string
}

// ShortcutNote explains the current fallback used for semantic magnify.
const ShortcutNote = "used the standard app zoom shortcut; public macOS APIs do not expose a generic cross-process magnify gesture injector"

// ErrNativeUnsupported reports that native magnify injection is not available.
var ErrNativeUnsupported = errors.New("native magnify injection is not supported")

// ParseStrategy parses a magnify backend strategy. The zero value and empty
// input select the shortcut strategy.
func ParseStrategy(value string) (Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(StrategyShortcut):
		return StrategyShortcut, nil
	case string(StrategyNative):
		return StrategyNative, nil
	default:
		return "", fmt.Errorf("invalid magnify strategy %q; use shortcut or native", value)
	}
}

// ParseZoomAction canonicalizes semantic zoom actions.
func ParseZoomAction(value string) (Action, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "in", "zoom_in", "pinch_in":
		return ActionIn, nil
	case "out", "zoom_out", "pinch_out":
		return ActionOut, nil
	case "reset", "actual", "actual_size":
		return ActionReset, nil
	default:
		return "", fmt.Errorf("invalid zoom action %q; use in, out, or reset", value)
	}
}

// ParsePinchDirection canonicalizes semantic pinch directions.
func ParsePinchDirection(value string) (Action, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "in", "zoom_in", "pinch_in":
		return ActionIn, nil
	case "out", "zoom_out", "pinch_out":
		return ActionOut, nil
	default:
		return "", fmt.Errorf("invalid pinch direction %q; use in or out", value)
	}
}

// ShortcutForAction returns the standard app zoom shortcut for action.
func ShortcutForAction(action Action) (Shortcut, error) {
	switch action {
	case ActionIn:
		return Shortcut{Keys: "cmd+shift+=", Label: "in"}, nil
	case ActionOut:
		return Shortcut{Keys: "cmd+-", Label: "out"}, nil
	case ActionReset:
		return Shortcut{Keys: "cmd+0", Label: "reset"}, nil
	default:
		return Shortcut{}, fmt.Errorf("invalid magnify action %q", action)
	}
}

// Dispatch applies action using strategy and the provided key sender.
func Dispatch(strategy Strategy, action Action, sendKeys func(string) error) (Shortcut, string, error) {
	strategy, err := ParseStrategy(string(strategy))
	if err != nil {
		return Shortcut{}, "", err
	}
	switch strategy {
	case StrategyShortcut:
		shortcut, err := ShortcutForAction(action)
		if err != nil {
			return Shortcut{}, "", err
		}
		if err := sendKeys(shortcut.Keys); err != nil {
			return shortcut, "", err
		}
		return shortcut, ShortcutNote, nil
	case StrategyNative:
		return Shortcut{}, "", fmt.Errorf("magnify strategy %q: %w", strategy, ErrNativeUnsupported)
	default:
		return Shortcut{}, "", fmt.Errorf("invalid magnify strategy %q; use shortcut or native", strategy)
	}
}

// Send applies action using the configured strategy.
func Send(strategy Strategy, action Action) (Shortcut, string, error) {
	return Dispatch(strategy, action, input.SendKeyCombo)
}

// ZoomElement focuses target when provided, then applies a semantic zoom action.
func ZoomElement(target *axuiautomation.Element, strategy Strategy, action string) (Action, string, error) {
	resolved, err := ParseZoomAction(action)
	if err != nil {
		return "", "", err
	}
	note, err := magnifyElement(target, strategy, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, note, nil
}

// PinchElement focuses target when provided, then applies a semantic pinch direction.
func PinchElement(target *axuiautomation.Element, strategy Strategy, direction string) (Action, string, error) {
	resolved, err := ParsePinchDirection(direction)
	if err != nil {
		return "", "", err
	}
	note, err := magnifyElement(target, strategy, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, note, nil
}

func magnifyElement(target *axuiautomation.Element, strategy Strategy, action Action) (string, error) {
	if target != nil {
		if err := target.Focus(); err != nil {
			return "", fmt.Errorf("focus target: %w", err)
		}
	}
	shortcut, note, err := Send(strategy, action)
	if err != nil {
		if shortcut.Label != "" {
			return "", fmt.Errorf("magnify %s: %w", shortcut.Label, err)
		}
		return "", err
	}
	return note, nil
}

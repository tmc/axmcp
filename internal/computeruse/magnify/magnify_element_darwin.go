//go:build darwin

package magnify

import (
	"fmt"

	"github.com/tmc/apple/x/axuiautomation"
)

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

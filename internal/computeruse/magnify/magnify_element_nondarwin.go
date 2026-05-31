//go:build !darwin

package magnify

// ZoomElement validates a semantic zoom action, then applies the configured
// non-Darwin dispatch path.
func ZoomElement(_ any, strategy Strategy, action string) (Action, string, error) {
	resolved, err := ParseZoomAction(action)
	if err != nil {
		return "", "", err
	}
	_, note, err := Send(strategy, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, note, nil
}

// PinchElement validates a semantic pinch direction, then applies the
// configured non-Darwin dispatch path.
func PinchElement(_ any, strategy Strategy, direction string) (Action, string, error) {
	resolved, err := ParsePinchDirection(direction)
	if err != nil {
		return "", "", err
	}
	_, note, err := Send(strategy, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, note, nil
}

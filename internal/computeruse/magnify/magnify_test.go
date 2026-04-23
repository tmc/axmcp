package magnify

import (
	"errors"
	"testing"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Strategy
		wantErr bool
	}{
		{name: "empty defaults to shortcut", input: "", want: StrategyShortcut},
		{name: "shortcut", input: "shortcut", want: StrategyShortcut},
		{name: "native", input: "native", want: StrategyNative},
		{name: "invalid", input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStrategy(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseStrategy(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Fatalf("ParseStrategy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseZoomAction(t *testing.T) {
	tests := []struct {
		input   string
		want    Action
		wantErr bool
	}{
		{input: "in", want: ActionIn},
		{input: "zoom_out", want: ActionOut},
		{input: "actual_size", want: ActionReset},
		{input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		got, err := ParseZoomAction(tt.input)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParseZoomAction(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if tt.wantErr {
			continue
		}
		if got != tt.want {
			t.Fatalf("ParseZoomAction(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParsePinchDirection(t *testing.T) {
	tests := []struct {
		input   string
		want    Action
		wantErr bool
	}{
		{input: "in", want: ActionIn},
		{input: "pinch_out", want: ActionOut},
		{input: "reset", wantErr: true},
	}

	for _, tt := range tests {
		got, err := ParsePinchDirection(tt.input)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParsePinchDirection(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if tt.wantErr {
			continue
		}
		if got != tt.want {
			t.Fatalf("ParsePinchDirection(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShortcutForAction(t *testing.T) {
	tests := []struct {
		action  Action
		want    Shortcut
		wantErr bool
	}{
		{action: ActionIn, want: Shortcut{Keys: "cmd+shift+=", Label: "in"}},
		{action: ActionOut, want: Shortcut{Keys: "cmd+-", Label: "out"}},
		{action: ActionReset, want: Shortcut{Keys: "cmd+0", Label: "reset"}},
		{action: Action("bogus"), wantErr: true},
	}

	for _, tt := range tests {
		got, err := ShortcutForAction(tt.action)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ShortcutForAction(%q) error = %v, wantErr %v", tt.action, err, tt.wantErr)
		}
		if tt.wantErr {
			continue
		}
		if got != tt.want {
			t.Fatalf("ShortcutForAction(%q) = %+v, want %+v", tt.action, got, tt.want)
		}
	}
}

func TestDispatchShortcut(t *testing.T) {
	var sent string
	shortcut, note, err := Dispatch("", ActionIn, func(keys string) error {
		sent = keys
		return nil
	})
	if err != nil {
		t.Fatalf("Dispatch shortcut: %v", err)
	}
	if shortcut != (Shortcut{Keys: "cmd+shift+=", Label: "in"}) {
		t.Fatalf("Dispatch shortcut = %+v", shortcut)
	}
	if sent != "cmd+shift+=" {
		t.Fatalf("Dispatch sent %q, want %q", sent, "cmd+shift+=")
	}
	if note != ShortcutNote {
		t.Fatalf("Dispatch note = %q, want %q", note, ShortcutNote)
	}
}

func TestDispatchNativeUnsupported(t *testing.T) {
	_, _, err := Dispatch(StrategyNative, ActionIn, func(string) error {
		t.Fatal("Dispatch should not send keys for native")
		return nil
	})
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("Dispatch native error = %v, want ErrNativeUnsupported", err)
	}
}

func TestZoomElementNativeUnsupported(t *testing.T) {
	_, _, err := ZoomElement(nil, StrategyNative, "in")
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("ZoomElement native error = %v, want ErrNativeUnsupported", err)
	}
}

func TestPinchElementRejectsReset(t *testing.T) {
	_, _, err := PinchElement(nil, StrategyShortcut, "reset")
	if err == nil {
		t.Fatal("PinchElement reset error = nil, want error")
	}
}

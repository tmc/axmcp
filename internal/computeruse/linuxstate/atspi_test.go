package linuxstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestATSPIReaderRejectsUnavailableEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		lookPath func(string) (string, error)
	}{
		{
			name: "missing dbus",
			env:  map[string]string{},
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
		},
		{
			name: "bridge disabled",
			env: map[string]string{
				"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/session",
				"NO_AT_BRIDGE":             "1",
			},
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
		},
		{
			name: "missing gdbus",
			env: map[string]string{
				"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/session",
			},
			lookPath: func(string) (string, error) {
				return "", errors.New("missing")
			},
		},
	}
	for _, tt := range tests {
		reader := &atspiReader{
			env: func(name string) string {
				return tt.env[name]
			},
			lookPath: tt.lookPath,
			run: func(context.Context, string, ...string) ([]byte, error) {
				t.Fatalf("%s: run called for unavailable environment", tt.name)
				return nil, nil
			},
		}
		_, err := reader.readWindow(context.Background(), linuxTestWindow())
		if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
			t.Fatalf("%s: error = %v, want ErrPlatformUnsupported", tt.name, err)
		}
	}
}

func TestATSPIReaderReadsInjectedTree(t *testing.T) {
	bus := fakeATSPIBus{}
	reader := &atspiReader{
		env: func(name string) string {
			if name == "DBUS_SESSION_BUS_ADDRESS" {
				return "unix:path=/tmp/session"
			}
			return ""
		},
		lookPath: func(name string) (string, error) {
			if name != "gdbus" {
				t.Fatalf("LookPath(%q), want gdbus", name)
			}
			return "/usr/bin/gdbus", nil
		},
		run: bus.run,
	}

	root, err := reader.readWindow(context.Background(), linuxTestWindow())
	if err != nil {
		t.Fatalf("readWindow: %v", err)
	}
	want := AccessibilityNode{
		Native: NativeElement{
			WindowID:   "0x03e00007",
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/window",
		},
		Role:        "frame",
		Title:       "Calculator",
		Description: "calculator window",
		Identifier:  "calc-window",
		Rect:        Rect{X: 10, Y: 20, Width: 300, Height: 200},
		Enabled:     true,
		Children: []AccessibilityNode{
			{
				Native: NativeElement{
					WindowID:   "0x03e00007",
					BusName:    ":1.10",
					ObjectPath: "/org/a11y/atspi/accessible/button",
				},
				Role:             "push button",
				Title:            "Seven",
				Identifier:       "seven",
				Rect:             Rect{X: 20, Y: 40, Width: 50, Height: 20},
				Enabled:          true,
				SecondaryActions: []string{"click"},
			},
			{
				Native: NativeElement{
					WindowID:   "0x03e00007",
					BusName:    ":1.10",
					ObjectPath: "/org/a11y/atspi/accessible/text",
				},
				Role:       "text",
				Title:      "Display",
				Value:      "42",
				Identifier: "display",
				Rect:       Rect{X: 30, Y: 70, Width: 100, Height: 30},
				Enabled:    true,
				Settable:   true,
			},
		},
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("root = %#v, want %#v", root, want)
	}
	if len(bus.calls) == 0 {
		t.Fatalf("fake bus was not called")
	}
}

func TestATSPIReaderPerformsActionByName(t *testing.T) {
	bus := fakeATSPIBus{}
	reader := &atspiReader{
		env: func(name string) string {
			if name == "DBUS_SESSION_BUS_ADDRESS" {
				return "unix:path=/tmp/session"
			}
			return ""
		},
		lookPath: func(name string) (string, error) {
			if name != "gdbus" {
				t.Fatalf("LookPath(%q), want gdbus", name)
			}
			return "/usr/bin/gdbus", nil
		},
		run: bus.run,
	}

	err := reader.performAction(context.Background(), accessibilityAction{
		Native: NativeElement{
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/button",
		},
		Name: "Click",
	})
	if err != nil {
		t.Fatalf("performAction: %v", err)
	}
	want := []atspiCall{{
		path:   "/org/a11y/atspi/accessible/button",
		method: atspiAction + ".DoAction",
		extra:  []string{"0"},
	}}
	if !reflect.DeepEqual(bus.actionCalls, want) {
		t.Fatalf("actionCalls = %#v, want %#v", bus.actionCalls, want)
	}

	err = reader.performAction(context.Background(), accessibilityAction{
		Native: NativeElement{
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/button",
		},
		Name: "missing",
	})
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("missing action error = %v, want ErrPlatformUnsupported", err)
	}
}

func TestATSPIParsers(t *testing.T) {
	refs := parseATSPIRefs([]byte("([(':1.1', objectpath '/org/a11y/atspi/accessible/1'), (':1.2', objectpath '/org/a11y/atspi/accessible/null')],)"))
	wantRefs := []atspiRef{{Bus: ":1.1", Path: "/org/a11y/atspi/accessible/1"}}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Fatalf("parseATSPIRefs = %#v, want %#v", refs, wantRefs)
	}
	ints := gvariantInts([]byte("(<int32 7>, <uint32 8>, 42, objectpath '/a11y/32')"))
	if !reflect.DeepEqual(ints, []int{7, 8, 42}) {
		t.Fatalf("gvariantInts = %#v, want [7 8 42]", ints)
	}
}

type fakeATSPIBus struct {
	calls       []string
	actionCalls []atspiCall
}

func (b *fakeATSPIBus) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "/usr/bin/gdbus" {
		return nil, fmt.Errorf("command = %q, want /usr/bin/gdbus", name)
	}
	call := atspiCallFromArgs(args)
	b.calls = append(b.calls, call.method+" "+call.path)

	switch call.method {
	case atspiBusName + ".GetAddress":
		return []byte("('unix:path=/tmp/atspi',)"), nil
	case atspiAccessible + ".GetChildren":
		return []byte(fakeATSPIChildren(call.path)), nil
	case atspiAccessible + ".GetRoleName":
		return []byte("('" + fakeATSPIRole(call.path) + "',)"), nil
	case atspiAccessible + ".GetInterfaces":
		return []byte(fakeATSPIInterfaces(call.path)), nil
	case atspiAccessible + ".GetState":
		return []byte(fakeATSPIState(call.path)), nil
	case dbusProperties + ".Get":
		if len(call.extra) < 2 {
			return nil, fmt.Errorf("missing property args")
		}
		return fakeATSPIProperty(call.path, unquoteGVariantString(call.extra[0]), unquoteGVariantString(call.extra[1]))
	case atspiComponent + ".GetExtents":
		return []byte(fakeATSPIExtents(call.path)), nil
	case atspiAction + ".GetName":
		if call.path == "/org/a11y/atspi/accessible/button" {
			return []byte("('click',)"), nil
		}
		return nil, fmt.Errorf("missing action")
	case atspiAction + ".DoAction":
		if call.path != "/org/a11y/atspi/accessible/button" {
			return nil, fmt.Errorf("missing action target")
		}
		if !reflect.DeepEqual(call.extra, []string{"0"}) {
			return nil, fmt.Errorf("DoAction args = %#v, want [0]", call.extra)
		}
		b.actionCalls = append(b.actionCalls, call)
		return []byte("(true,)"), nil
	default:
		return nil, fmt.Errorf("unexpected method %q", call.method)
	}
}

type atspiCall struct {
	path   string
	method string
	extra  []string
}

func atspiCallFromArgs(args []string) atspiCall {
	var call atspiCall
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--object-path":
			i++
			call.path = args[i]
		case "--method":
			i++
			call.method = args[i]
			call.extra = append([]string(nil), args[i+1:]...)
			return call
		}
	}
	return call
}

func fakeATSPIChildren(path string) string {
	switch path {
	case atspiRootPath:
		return "([(':1.10', objectpath '/org/a11y/atspi/accessible/app')],)"
	case "/org/a11y/atspi/accessible/app":
		return "([(':1.10', objectpath '/org/a11y/atspi/accessible/window')],)"
	case "/org/a11y/atspi/accessible/window":
		return "([(':1.10', objectpath '/org/a11y/atspi/accessible/button'), (':1.10', objectpath '/org/a11y/atspi/accessible/text')],)"
	default:
		return "(@a(so) [],)"
	}
}

func fakeATSPIRole(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/app":
		return "application"
	case "/org/a11y/atspi/accessible/window":
		return "frame"
	case "/org/a11y/atspi/accessible/button":
		return "push button"
	case "/org/a11y/atspi/accessible/text":
		return "text"
	default:
		return ""
	}
}

func fakeATSPIInterfaces(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/window":
		return "(['org.a11y.atspi.Component'],)"
	case "/org/a11y/atspi/accessible/button":
		return "(['org.a11y.atspi.Component', 'org.a11y.atspi.Action'],)"
	case "/org/a11y/atspi/accessible/text":
		return "(['org.a11y.atspi.Component', 'org.a11y.atspi.Value'],)"
	default:
		return "([],)"
	}
}

func fakeATSPIState(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/text":
		return "([7, 8],)"
	default:
		return "([8],)"
	}
}

func fakeATSPIProperty(path, iface, property string) ([]byte, error) {
	if iface == atspiAccessible {
		switch property {
		case "Name":
			return []byte("('" + fakeATSPIName(path) + "',)"), nil
		case "Description":
			if path == "/org/a11y/atspi/accessible/window" {
				return []byte("('calculator window',)"), nil
			}
		case "AccessibleId":
			return []byte("('" + fakeATSPIID(path) + "',)"), nil
		}
	}
	if iface == atspiAction && property == "NActions" && path == "/org/a11y/atspi/accessible/button" {
		return []byte("(<int32 1>,)"), nil
	}
	if iface == atspiValue && property == "Text" && path == "/org/a11y/atspi/accessible/text" {
		return []byte("('42',)"), nil
	}
	return nil, fmt.Errorf("missing property %s %s %s", path, iface, property)
}

func fakeATSPIName(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/window":
		return "Calculator"
	case "/org/a11y/atspi/accessible/button":
		return "Seven"
	case "/org/a11y/atspi/accessible/text":
		return "Display"
	default:
		return ""
	}
}

func fakeATSPIID(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/window":
		return "calc-window"
	case "/org/a11y/atspi/accessible/button":
		return "seven"
	case "/org/a11y/atspi/accessible/text":
		return "display"
	default:
		return ""
	}
}

func fakeATSPIExtents(path string) string {
	switch path {
	case "/org/a11y/atspi/accessible/window":
		return "(10, 20, 300, 200)"
	case "/org/a11y/atspi/accessible/button":
		return "(20, 40, 50, 20)"
	case "/org/a11y/atspi/accessible/text":
		return "(30, 70, 100, 30)"
	default:
		return "(0, 0, 0, 0)"
	}
}

func unquoteGVariantString(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\\'", "'"), "'")
}

func linuxTestWindow() Window {
	return Window{
		ID:     "0x03e00007",
		PID:    999901,
		Title:  "Calculator",
		X:      10,
		Y:      20,
		Width:  300,
		Height: 200,
	}
}

package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTryDirectWindowScreenshotSkipsWithoutScreenPermission(t *testing.T) {
	oldTrusted := screenRecordingTrusted
	screenRecordingTrusted = func() bool { return false }
	defer func() { screenRecordingTrusted = oldTrusted }()

	if got := tryDirectWindowScreenshot([]string{"screenshot", "Codex", "-o", "/tmp/out.png"}); got {
		t.Fatal("tryDirectWindowScreenshot returned true, want false when Screen Recording is missing")
	}
}

func TestStdinLooksLikeTransport(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{name: "named pipe", mode: os.ModeNamedPipe, want: true},
		{name: "socket", mode: os.ModeSocket, want: true},
		{name: "char device", mode: os.ModeCharDevice, want: false},
		{name: "regular file", mode: 0, want: false},
	}

	for _, tt := range tests {
		if got := stdinModeLooksLikeTransport(tt.mode); got != tt.want {
			t.Fatalf("%s: transport check = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPermissionPane(t *testing.T) {
	tests := []struct {
		service string
		want    string
	}{
		{service: "Accessibility", want: "Accessibility"},
		{service: "Screen Recording", want: "Screen Recording"},
	}

	for _, tt := range tests {
		if got := permissionPane(tt.service); got != tt.want {
			t.Fatalf("permissionPane(%q) = %q, want %q", tt.service, got, tt.want)
		}
	}
}

func TestWaitForPermissionImmediate(t *testing.T) {
	if err := waitForPermission("Accessibility", time.Millisecond, time.Microsecond, func() bool { return true }); err != nil {
		t.Fatalf("waitForPermission returned %v, want nil", err)
	}
}

func TestWaitForPermissionTimeout(t *testing.T) {
	err := waitForPermission("Accessibility", 5*time.Millisecond, time.Millisecond, func() bool { return false })
	if err == nil {
		t.Fatal("waitForPermission returned nil, want error")
	}
	if !strings.Contains(err.Error(), "Accessibility permission not granted for axmcp.app") {
		t.Fatalf("waitForPermission error = %q, want accessibility guidance", err)
	}
}

func TestShouldRunCLI(t *testing.T) {
	tests := []struct {
		name     string
		stdinTTY bool
		args     []string
		want     bool
	}{
		{name: "tty with no args does not run cli", stdinTTY: true, want: false},
		{name: "tty with subcommand runs cli", stdinTTY: true, args: []string{"tree", "Finder"}, want: true},
		{name: "server accepts ghost cursor flag", args: []string{"--ghost-cursor=false"}, want: false},
		{name: "server accepts eyecandy flag", args: []string{"--eyecandy=false"}, want: false},
		{name: "server accepts visibility flag", args: []string{"--visibility=false"}, want: false},
		{name: "server accepts visibility delay flag", args: []string{"--visibility-delay=1s"}, want: false},
		{name: "server accepts verbose flag", args: []string{"-v"}, want: false},
		{name: "subcommand requires cli", args: []string{"tree", "Finder"}, want: true},
		{name: "unknown flag falls into cli parsing", args: []string{"--bogus"}, want: true},
	}
	for _, tt := range tests {
		if got := shouldRunCLI(tt.stdinTTY, tt.args); got != tt.want {
			t.Fatalf("%s: shouldRunCLI(%v, %v) = %v, want %v", tt.name, tt.stdinTTY, tt.args, got, tt.want)
		}
	}
}

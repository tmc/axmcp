package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/macgo"
)

const (
	permissionWaitTimeout  = 120 * time.Second
	permissionPollInterval = 250 * time.Millisecond
)

var (
	diagnosticWriter io.Writer = os.Stderr
	diagnosticFile   *os.File
)

func main() {
	runtime.LockOSThread()

	if err := configureLogging(); err != nil {
		log.Fatalf("configure logging: %v", err)
	}

	cfg := macgo.NewConfig().
		WithAppName("computer-use-mcp").
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSScreenCaptureUsageDescription", "computer-use-mcp captures application windows and UI state to power stateful computer-use tools.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory).
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.computerusemcp"
	ui.ConfigureIdentity("computer-use-mcp", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "computer-use-mcp",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})
	rt, err := newRuntimeState()
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	registerComputerUseTools(server, rt)

	app := appkit.GetNSApplicationClass().SharedApplication()
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(_ appkit.NSApplication) appkit.NSApplicationTerminateReply {
			return appkit.NSTerminateCancel
		},
		ShouldTerminateAfterLastWindowClosed: func(_ appkit.NSApplication) bool {
			return false
		},
	})
	app.SetDelegate(delegate)

	procInfo := foundation.GetProcessInfoClass().ProcessInfo()
	procInfo.SetAutomaticTerminationSupportEnabled(false)
	procInfo.DisableAutomaticTermination("computer-use-mcp server")
	procInfo.DisableSuddenTermination()
	_ = procInfo.BeginActivityWithOptionsReason(
		foundation.NSActivitySuddenTerminationDisabled|foundation.NSActivityAutomaticTerminationDisabled,
		"computer-use-mcp server",
	)

	ui.CheckTrust()

	go func() {
		time.Sleep(100 * time.Millisecond)
		procInfo.SetAutomaticTerminationSupportEnabled(false)
		procInfo.DisableAutomaticTermination("computer-use-mcp server goroutine")
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Printf("server error: %v", err)
		}
		ui.WaitForWindows()
		os.Exit(0)
	}()

	for {
		app.Run()
	}
}

func diagf(format string, args ...any) {
	_, _ = fmt.Fprintf(diagnosticWriter, format, args...)
}

func configureLogging() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	logDir := filepath.Join(home, ".computer-use-mcp")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("computer-use-mcp-%d.log", os.Getpid()))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	diagnosticFile = f
	w := io.MultiWriter(os.Stderr, f)
	diagnosticWriter = w
	log.SetOutput(w)
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return nil
}

func permissionPane(service string) string {
	switch service {
	case "Screen Recording":
		return "Screen Recording"
	default:
		return service
	}
}

func waitForPermission(service string, timeout, interval time.Duration, check func() bool) error {
	if check() {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		if check() {
			return nil
		}
	}
	return fmt.Errorf("%s permission not granted for computer-use-mcp.app; grant access in System Settings > Privacy & Security > %s", service, permissionPane(service))
}

func failPermission(err error) {
	diagf("computer-use-mcp: %v\n", err)
	os.Exit(1)
}

// Command axmcp is an MCP server for macOS Accessibility API automation.
//
// It exposes the AX element tree, querying, and interaction tools over the
// Model Context Protocol, running as a macOS app bundle for Accessibility TCC.
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
	"github.com/tmc/macgo"
	"github.com/tmc/xcmcp/internal/ui"
)

// hasArg reports whether arg appears anywhere in os.Args[1:].
func hasArg(arg string) bool {
	for _, a := range os.Args[1:] {
		if a == arg {
			return true
		}
	}
	return false
}

const (
	permissionWaitTimeout  = 120 * time.Second
	permissionPollInterval = 250 * time.Millisecond
)

var diagnosticWriter io.Writer = os.Stderr

func diagf(format string, args ...any) {
	_, _ = fmt.Fprintf(diagnosticWriter, format, args...)
}

func configureLogging(verbose bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	logDir := filepath.Join(home, ".axmcp")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("axmcp-%d.log", os.Getpid()))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	w := io.MultiWriter(os.Stderr, f)
	diagnosticWriter = w
	log.SetOutput(w)

	logLevel := slog.LevelWarn
	if verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: logLevel})))
	diagf("axmcp: logging to %s\n", logPath)
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
	diagf("axmcp: waiting for %s permission…\n", service)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		if check() {
			diagf("axmcp: %s permission granted\n", service)
			return nil
		}
	}
	return fmt.Errorf("%s permission not granted for axmcp.app; grant access in System Settings > Privacy & Security > %s", service, permissionPane(service))
}

func failPermission(err error) {
	diagf("axmcp: %v\n", err)
	os.Exit(1)
}

func main() {
	runtime.LockOSThread()

	verbose := hasArg("-v")
	if err := configureLogging(verbose); err != nil {
		log.Fatalf("configure logging: %v", err)
	}

	cfg := macgo.NewConfig().
		WithAppName("axmcp").
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSScreenCaptureUsageDescription", "axmcp needs to capture screenshots of specific UI elements and windows.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory).
		WithAdHocSign()
	if verbose {
		cfg = cfg.WithDebug()
	}
	cfg.BundleID = "dev.tmc.axmcp"
	ui.ConfigureIdentity("axmcp", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "axmcp",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})

	registerAXTools(server)

	// Initialize AppKit — required for NSWindow, buttons, and DispatchMainSafe.
	app := appkit.GetNSApplicationClass().SharedApplication()

	// Set a delegate that prevents AppKit from terminating when the last
	// window closes. ShouldTerminate allows termination so that system
	// requests like TCC "Quit & Reopen" work correctly.
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminateAfterLastWindowClosed: func(_ appkit.NSApplication) bool {
			return false
		},
	})
	app.SetDelegate(delegate)

	// Prevent AppKit from automatically terminating the process when no
	// windows are open. Without this, the CLI and MCP server modes get
	// killed by the automatic termination subsystem after a brief timeout.
	procInfo := foundation.GetProcessInfoClass().ProcessInfo()
	procInfo.SetAutomaticTerminationSupportEnabled(false)
	procInfo.DisableAutomaticTermination("axmcp server")

	ui.CheckTrust()

	// Only check screen recording eagerly for the CLI screenshot subcommand.
	// MCP server mode defers the check until a screenshot tool is actually called.
	if hasArg("screenshot") {
		ui.CheckScreenCapture()
	}

	if isTTY() || len(os.Args) > 1 {
		// Run CLI in goroutine so main thread can drive the AppKit run loop.
		go func() {
			diagf("axmcp: CLI goroutine started\n")
			time.Sleep(500 * time.Millisecond)
			// Re-disable automatic termination after AppKit startup completes.
			// AppKit's window restoration re-enables it during app.Run() init.
			procInfo.SetAutomaticTerminationSupportEnabled(false)
			procInfo.DisableAutomaticTermination("axmcp cli")
			diagf("axmcp: auto-termination disabled\n")
			if err := waitForPermission("Accessibility", permissionWaitTimeout, permissionPollInterval, ui.IsTrusted); err != nil {
				failPermission(err)
			}
			// Wait for Screen Recording if screenshotting.
			if hasArg("screenshot") {
				diagf("axmcp: checking screen recording\n")
				if err := waitForPermission("Screen Recording", permissionWaitTimeout, permissionPollInterval, ui.IsScreenRecordingTrusted); err != nil {
					failPermission(err)
				}
				diagf("axmcp: screen recording OK\n")
			}
			diagf("axmcp: running CLI\n")
			runCLI()
			// runCLI calls os.Exit on completion, so this goroutine won't return
		}()
	} else {
		// Run MCP server in goroutine so main thread can drive the AppKit run loop.
		go func() {
			time.Sleep(100 * time.Millisecond)
			procInfo.SetAutomaticTerminationSupportEnabled(false)
			procInfo.DisableAutomaticTermination("axmcp server goroutine")
			if err := waitForPermission("Accessibility", permissionWaitTimeout, permissionPollInterval, ui.IsTrusted); err != nil {
				failPermission(err)
			}
			if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
				log.Printf("server error: %v", err)
			}
			ui.WaitForWindows()
			os.Exit(0)
		}()
	}

	// Run the AppKit event loop on the main thread. This drains CFRunLoop,
	// the GCD main queue, and AppKit UI events (buttons, windows, etc.).
	diagf("axmcp: starting app.Run()\n")
	app.Run()
	diagf("axmcp: app.Run() returned — this should not happen\n")
	_ = 42
}

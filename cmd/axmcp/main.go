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
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/axmcp/internal/cmdflag"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/axmcp/internal/ui/permissions"
	"github.com/tmc/macgo"
)

const (
	permissionWaitTimeout  = 120 * time.Second
	permissionPollInterval = 250 * time.Millisecond
)

var (
	diagnosticWriter       io.Writer = os.Stderr
	diagnosticFile         *os.File
	screenRecordingTrusted = ui.IsScreenRecordingTrusted
	stdinTransportReady    = stdinLooksLikeTransport
)

func diagf(format string, args ...any) {
	_, _ = fmt.Fprintf(diagnosticWriter, format, args...)
}

// flushDiagLog syncs the diagnostic log file to disk. Use before
// operations that may abruptly terminate the process.
func flushDiagLog() {
	if diagnosticFile != nil {
		diagnosticFile.Sync()
	}
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
	diagnosticFile = f
	setDiagFd(int(f.Fd()))
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

func stdinLooksLikeTransport() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return stdinModeLooksLikeTransport(info.Mode())
}

func stdinModeLooksLikeTransport(mode os.FileMode) bool {
	return mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0
}

func directWindowScreenshotArgs(args []string) (app string, out string, ok bool) {
	trimmed := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-v" || arg == "--verbose" || arg == "--ghost-cursor" || arg == "--no-ghost-cursor" || arg == "--eyecandy" || arg == "--no-eyecandy" || arg == "--visibility":
			continue
		case arg == "--visibility-delay":
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "--ghost-cursor=") || strings.HasPrefix(arg, "--eyecandy=") || strings.HasPrefix(arg, "--visibility=") || strings.HasPrefix(arg, "--visibility-delay="):
			continue
		default:
			trimmed = append(trimmed, arg)
		}
	}
	if len(trimmed) < 2 || trimmed[0] != "screenshot" {
		return "", "", false
	}
	app = trimmed[1]
	for i := 2; i < len(trimmed); i++ {
		switch trimmed[i] {
		case "--contains", "--role":
			return "", "", false
		case "-o", "--out":
			if i+1 >= len(trimmed) {
				return "", "", false
			}
			out = trimmed[i+1]
			i++
		case "":
		default:
			if strings.HasPrefix(trimmed[i], "--contains=") || strings.HasPrefix(trimmed[i], "--role=") {
				return "", "", false
			}
		}
	}
	return app, out, true
}

func tryDirectWindowScreenshot(args []string) bool {
	app, out, ok := directWindowScreenshotArgs(args)
	if !ok {
		return false
	}
	if !screenRecordingTrusted() {
		diagf("axmcp: skipping direct screenshot fast path for %q because Screen Recording is not granted\n", app)
		return false
	}
	diagf("axmcp: trying direct screenshot fast path for %q\n", app)
	png, err := captureWindowByName(app)
	if err != nil {
		diagf("axmcp: direct screenshot fast path failed: %v; falling back to app-backed flow\n", err)
		return false
	}
	if err := writeScreenshot(out, png); err != nil {
		diagf("axmcp: direct screenshot write failed: %v\n", err)
		os.Exit(1)
	}
	return true
}

func ensureAccessibilityPermission(ctx context.Context) error {
	if permissions.Check(permissions.ReqAccessibility) == permissions.StatusGranted {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := permissions.Request(requestCtx, permissions.ReqAccessibility)
	if status == permissions.StatusGranted {
		return nil
	}
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return err
	}
	if err := permissions.OnboardingWindow(ctx, permissions.ReqAccessibility); err != nil && err != context.Canceled {
		return err
	}
	return waitForPermission("Accessibility", permissionWaitTimeout, permissionPollInterval, ui.IsTrusted)
}

func main() {
	installAtexitHandler()
	runtime.LockOSThread()

	args := os.Args[1:]
	verbose := cmdflag.Bool(args, "-v", false) || cmdflag.Bool(args, "--verbose", false)
	ghostCursorEnabled := cmdflag.Bool(args, "--ghost-cursor", true)
	eyecandyEnabled := cmdflag.Bool(args, "--eyecandy", true)
	if err := configureLogging(verbose); err != nil {
		log.Fatalf("configure logging: %v", err)
	}
	if tryDirectWindowScreenshot(args) {
		return
	}

	cfg := macgo.NewConfig().
		WithAppName("axmcp").
		WithPermissions(macgo.Accessibility).
		WithUsageDescription("NSAccessibilityUsageDescription", "axmcp uses Accessibility to inspect and interact with user interface elements.").
		WithUsageDescription("NSAppleEventsUsageDescription", "axmcp may coordinate with other macOS apps while driving UI automation.").
		WithUsageDescription("NSScreenCaptureUsageDescription", "axmcp needs to capture screenshots of specific UI elements and windows.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory)
	if verbose {
		cfg = cfg.WithDebug()
	}
	cfg.BundleID = "dev.tmc.axmcp"
	cfg = macsigning.Configure(cfg)
	ui.ConfigureIdentity("axmcp", cfg.BundleID)
	permissions.ConfigureIdentity("axmcp", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}

	eyecandy := ghostcursor.DefaultEyecandyConfig()
	eyecandy.SharingVisible = envFlag("ux.ghostcursor.sharing_visible", false)
	eyecandy.RippleOnClick = eyecandyEnabled && envFlag("ux.ghostcursor.ripple_on_click", eyecandy.RippleOnClick)
	eyecandy.CometTrail = eyecandyEnabled && envFlag("ux.ghostcursor.comet_trail", eyecandy.CometTrail)
	eyecandy.VelocityTilt = eyecandyEnabled && envFlag("ux.ghostcursor.velocity_tilt", eyecandy.VelocityTilt)
	eyecandy.HolographicOCR = eyecandyEnabled && envFlag("ux.ghostcursor.holographic_ocr", false)
	eyecandy.LiquidLens = eyecandyEnabled && envFlag("ux.ghostcursor.liquid_lens", false)
	ghostcursor.Configure(ghostcursor.Config{
		Enabled:  ghostCursorEnabled,
		Theme:    ghostcursor.ThemeCodex,
		Eyecandy: eyecandy,
	})

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

	// Set a delegate that prevents AppKit from terminating the process.
	// Without this, app.Run() calls exit(0) when ScreenCaptureKit
	// dispatches work to the main thread.
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(app appkit.NSApplication) appkit.NSApplicationTerminateReply {
			reply := ui.ShouldTerminateReply(app)
			if reply == appkit.NSTerminateNow {
				diagf("axmcp: applicationShouldTerminate — allowing user quit\n")
				return reply
			}
			if ui.ScreenCaptureTerminateGuardActive() {
				diagf("axmcp: applicationShouldTerminate — cancelling during screen capture permission flow\n")
				return reply
			}
			diagf("axmcp: applicationShouldTerminate — cancelling\n")
			return reply
		},
		ShouldTerminateAfterLastWindowClosed: func(_ appkit.NSApplication) bool {
			return false
		},
	})
	app.SetDelegate(delegate)

	// Prevent AppKit from automatically or suddenly terminating the process.
	// Without this, the CLI and MCP server modes get killed when
	// ScreenCaptureKit dispatches work to the main thread.
	procInfo := foundation.GetProcessInfoClass().ProcessInfo()
	procInfo.SetAutomaticTerminationSupportEnabled(false)
	procInfo.DisableAutomaticTermination("axmcp server")
	procInfo.DisableSuddenTermination()

	// BeginActivity prevents both sudden and automatic termination for
	// the lifetime of the returned activity token.
	_ = procInfo.BeginActivityWithOptionsReason(
		foundation.NSActivitySuddenTerminationDisabled|foundation.NSActivityAutomaticTerminationDisabled,
		"axmcp server",
	)

	stdinTTY := isTTY()
	cliMode := shouldRunCLI(stdinTTY, args)
	serverTransport := stdinTransportReady()

	if cliMode {
		// Run CLI in goroutine so main thread can drive the AppKit run loop.
		go func() {
			diagf("axmcp: CLI goroutine started\n")
			time.Sleep(500 * time.Millisecond)
			// Re-disable automatic termination after AppKit startup completes.
			// AppKit's window restoration re-enables it during app.Run() init.
			procInfo.SetAutomaticTerminationSupportEnabled(false)
			procInfo.DisableAutomaticTermination("axmcp cli")
			diagf("axmcp: auto-termination disabled\n")
			diagf("axmcp: running CLI\n")
			runCLI()
			// runCLI calls os.Exit on completion, so this goroutine won't return
		}()
	} else if serverTransport {
		// Run MCP server in goroutine so main thread can drive the AppKit run loop.
		go func() {
			time.Sleep(100 * time.Millisecond)
			procInfo.SetAutomaticTerminationSupportEnabled(false)
			procInfo.DisableAutomaticTermination("axmcp server goroutine")
			ui.CheckTrust()
			ui.CheckScreenCapture()
			if err := ensureAccessibilityPermission(context.Background()); err != nil {
				failPermission(err)
			}
			if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
				log.Printf("server error: %v", err)
			}
			ui.WaitForWindows()
			os.Exit(0)
		}()
	} else {
		go func() {
			diagf("axmcp: no CLI args and no stdio transport; assuming TCC relaunch and exiting\n")
			time.Sleep(250 * time.Millisecond)
			ui.WaitForWindows()
			os.Exit(0)
		}()
	}

	// Run the AppKit event loop on the main thread. This drains CFRunLoop,
	// the GCD main queue, and AppKit UI events (buttons, windows, etc.).
	// The delegate's ShouldTerminate returns NSTerminateCancel to prevent
	// AppKit from calling exit(0) during ScreenCaptureKit dispatch.
	//
	// app.Run() can return if [NSApp stop:] is called (e.g. by
	// ScreenCaptureKit internals during TCC validation). Re-enter the
	// run loop when that happens so the process stays alive.
	for {
		diagf("axmcp: starting app.Run()\n")
		flushDiagLog()
		app.Run()
		diagf("axmcp: app.Run() returned — re-entering run loop\n")
		flushDiagLog()
	}
}

func shouldRunCLI(stdinTTY bool, args []string) bool {
	if len(args) == 0 {
		return false
	}
	if stdinTTY {
		return true
	}
	for _, arg := range args {
		switch {
		case arg == "-v", arg == "--verbose", arg == "--ghost-cursor", arg == "--no-ghost-cursor", arg == "--eyecandy", arg == "--no-eyecandy", arg == "--visibility":
			continue
		case strings.HasPrefix(arg, "--ghost-cursor="):
			continue
		case strings.HasPrefix(arg, "--eyecandy="):
			continue
		case strings.HasPrefix(arg, "--visibility="):
			continue
		case arg == "--visibility-delay":
			continue
		case strings.HasPrefix(arg, "--visibility-delay="):
			continue
		case strings.HasPrefix(arg, "--verbose="):
			continue
		case strings.HasPrefix(arg, "-"):
			return true
		default:
			return true
		}
	}
	return false
}

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
	diagnosticWriter io.Writer = os.Stderr
	diagnosticFile   *os.File
)

func main() {
	runtime.LockOSThread()
	ghostCursorEnabled := cmdflag.Bool(os.Args[1:], "--ghost-cursor", true)

	if err := configureLogging(); err != nil {
		log.Fatalf("configure logging: %v", err)
	}

	cfg := macgo.NewConfig().
		WithAppName("computer-use-mcp").
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSAccessibilityUsageDescription", "computer-use-mcp uses Accessibility to inspect and operate application UI elements.").
		WithUsageDescription("NSAppleEventsUsageDescription", "computer-use-mcp may coordinate with other macOS apps to complete computer-use tasks.").
		WithUsageDescription("NSScreenCaptureUsageDescription", "computer-use-mcp captures application windows and UI state to power stateful computer-use tools.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory)
	cfg.BundleID = "dev.tmc.computerusemcp"
	cfg = macsigning.Configure(cfg)
	ui.ConfigureIdentity("computer-use-mcp", cfg.BundleID)
	permissions.ConfigureIdentity("computer-use-mcp", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}
	ghostcursor.Configure(ghostcursor.Config{
		Enabled:  ghostCursorEnabled,
		Theme:    ghostcursor.ThemeCodex,
		Eyecandy: ghostcursor.DefaultEyecandyConfig(),
	})

	rt, err := newRuntimeState()
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	server := newComputerUseServer(rt)

	app := appkit.GetNSApplicationClass().SharedApplication()
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(app appkit.NSApplication) appkit.NSApplicationTerminateReply {
			return ui.ShouldTerminateReply(app)
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

	if permissions.Check(permissions.ReqAccessibility) != permissions.StatusGranted ||
		permissions.Check(permissions.ReqScreenRecording) != permissions.StatusGranted {
		if err := permissions.OnboardingWindow(context.Background(), permissions.ReqAccessibility, permissions.ReqScreenRecording); err != nil && err != context.Canceled {
			failPermission(err)
		}
	}

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

func computerUseInstructions() string {
	return strings.Join([]string{
		"Computer Use tools let you interact with macOS apps by performing UI actions.",
		"",
		"Some apps might have a separate dedicated plugin or skill. You may want to use that plugin or skill instead of Computer Use when it seems like a good fit for the task. While the separate plugin or skill may not expose every feature in the app, if the plugin can perform the task with its available features, prefer it. If the needed capability is not exposed there, use Computer Use may be appropriate for the missing interaction.",
		"",
		"Begin by calling `get_app_state` every turn you want to use Computer Use to get the latest state before acting. Codex will automatically stop the session after each assistant turn, so this step is required before interacting with apps in a new assistant turn.",
		"",
		"The available tools are list_apps, get_app_state, click, perform_secondary_action, scroll, drag, type_text, press_key, and set_value. If any of these are not available in your environment, use tool_search to surface one before calling any Computer Use action tools.",
		"",
		"Computer Use tools allow you to use the user's apps in the background, so while you're using an app, the user can continue to use other apps on their computer. Avoid doing anything that would disrupt the user's active session, such as overwriting the contents of their clipboard, unless they asked you to!",
		"",
		"After each action, use the action result or fetch the latest state to verify the UI changed as expected.",
		"Prefer element-targeted interactions over coordinate clicks when an index for the targeted element is available. Note that element indices are the sequential integers from the app state's accessibility tree.",
		"Prefer type_text with element_index when a text target is available; omit element_index only when you intentionally want to type into the app's currently focused element.",
		"Avoid falling back to AppleScript during a computer use session. Prefer Computer Use tools as much as possible to complete tasks.",
		"Ask the user before taking destructive or externally visible actions such as sending, deleting, or purchasing. If helpful, you can ask follow-up questions before taking action to make sure you’re understanding the user’s request correctly.",
	}, "\n")
}

//go:build darwin

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
	"github.com/tmc/axmcp/internal/computeruse/intervention"
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
	interventionMonitorEnabled := cmdflag.Bool(os.Args[1:], "--human-intervention-monitor", envBool("COMPUTER_USE_MCP_HUMAN_INTERVENTION_MONITOR", false))

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

	rt, err := newRuntimeState(runtimeOptions{
		intervention: interventionConfig(interventionMonitorEnabled),
		blockedURLs:  envList("COMPUTER_USE_MCP_BLOCKED_DOMAINS"),
	})
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

func envBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

func envList(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func interventionConfig(enabled bool) intervention.Config {
	return intervention.Config{
		Enabled:     enabled,
		QuietPeriod: 750 * time.Millisecond,
	}
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

// Command xcmcp is a MCP server that exposes various tools for interacting with Xcode projects, simulators, devices, and related resources. It is designed to be used as a companion process for development tools that need to perform operations on Xcode projects or simulators without directly invoking xcodebuild or simctl from the client side.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/resources"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/axmcp/internal/ui/permissions"
	"github.com/tmc/macgo"
)

// Define flags at package level so they can be registered before macgo.Start.
var (
	enableAll            = flag.Bool("enable-all", false, "Enable all optional toolsets at startup")
	enableApp            = flag.Bool("enable-app-tools", false, "Enable app management tools at startup")
	enableUI             = flag.Bool("enable-ui-tools", false, "Enable UI automation tools at startup")
	enableDevice         = flag.Bool("enable-device-tools", false, "Enable simulator device control tools at startup")
	enableDebugging      = flag.Bool("enable-debugging-tools", false, "Enable LLDB debugging tools at startup")
	enableIOS            = flag.Bool("enable-ios-tools", false, "Enable iOS-specific tools at startup")
	enableSimExtras      = flag.Bool("enable-sim-extras", false, "Enable simulator extras (open URL, add media, container path) at startup")
	enablePhysical       = flag.Bool("enable-physical-device-tools", false, "Enable physical device management tools at startup")
	enableVideo          = flag.Bool("enable-video-tools", false, "Enable video recording tools at startup")
	enableCrash          = flag.Bool("enable-crash-tools", false, "Enable crash reporting tools at startup")
	enableFS             = flag.Bool("enable-fs-tools", false, "Enable file system tools at startup")
	enableDeps           = flag.Bool("enable-dependency-tools", false, "Enable dependency management tools at startup")
	enableResources      = flag.Bool("enable-resources", true, "Enable resource management")
	enableASC            = flag.Bool("enable-asc-tools", false, "Enable App Store Connect and altool tools at startup")
	enableXcode          = flag.Bool("enable-xcode-tools", true, "Enable Xcode tools via xcrun mcpbridge")
	waitForXcode         = flag.Duration("wait-for-xcode", 30*time.Second, "Max time to wait for Xcode bridge tools before accepting MCP connections (0 to disable)")
	xcodeToolsPrefix     = flag.String("xcode-tools-prefix", "", "Optional prefix for proxied Xcode tool names")
	xcodeOnly            = flag.Bool("xcode-only", false, "Only register Xcode bridge tools, skip all native xcmcp tools")
	subscribeBuildErrors = flag.Bool("subscribe-build-errors", false, "Expose Xcode build errors as a subscribable resource")
	requestPermissions   = flag.Bool("request-permissions", false, "Request xcmcp Accessibility and Screen Recording permissions, then exit")
)

func main() {
	runtime.LockOSThread()

	// Handle -h/--help before macgo.Start to avoid unnecessary app bundle relaunch.
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" || arg == "-help" {
			fmt.Fprintf(os.Stderr, "Usage: xcmcp [flags]\n\nxcmcp is an MCP server for Xcode projects, simulators, and devices.\n\nFlags:\n")
			flag.PrintDefaults()
			os.Exit(0)
		}
	}

	// Force new instance to ensure each CLI invocation gets a dedicated app instance with fresh pipes
	os.Setenv("MACGO_OPEN_NEW_INSTANCE", "1")

	// Initialize macgo for TCC identity
	cfg := macgo.NewConfig().
		WithAppName("xcmcp").
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSAccessibilityUsageDescription", "xcmcp uses Accessibility to inspect and interact with macOS UI elements.").
		WithUsageDescription("NSScreenCaptureUsageDescription", "xcmcp uses Screen Recording to capture UI screenshots for MCP tools.").
		WithUIMode(macgo.UIModeAccessory).
		WithDevMode()
	cfg.BundleID = "dev.tmc.xcmcp"
	cfg = macsigning.Configure(cfg)
	cfg.ForceDirectExecution = os.Getenv("XCMCP_FORCE_DIRECT") == "1"
	removeAdHocBundle("xcmcp", cfg.CodeSignIdentity != "" || cfg.AutoSign, cfg.DevMode)
	ui.ConfigureIdentity("xcmcp", cfg.BundleID)
	permissions.ConfigureIdentity("xcmcp", cfg.BundleID)

	if os.Getenv("XCMCP_NO_MACGO") != "1" {
		if err := macgo.Start(cfg); err != nil {
			log.Fatalf("macgo start failed: %v", err)
		}
	}
	restoreMacgoDevModeStdin()
	initFileLog()

	// Initialize AppKit to satisfy LaunchServices
	app := appkit.GetNSApplicationClass().SharedApplication()

	flag.Parse()

	if *enableAll {
		*enableApp = true
		*enableUI = true
		*enableDevice = true
		*enableDebugging = true
		*enableIOS = true
		*enableSimExtras = true
		*enablePhysical = true
		*enableVideo = true
		*enableCrash = true
		*enableFS = true
		*enableDeps = true
		*enableResources = true
		*enableASC = true
		*enableXcode = true
	}

	needsUIPermissions := *requestPermissions || *enableAll || *enableUI
	var permissionReady <-chan error
	if needsUIPermissions {
		ch := make(chan error, 1)
		permissionReady = ch
		go func() {
			ch <- ensureUIPermissions()
		}()
	}
	if *requestPermissions {
		go func() {
			if err := <-permissionReady; err != nil {
				log.Printf("permission onboarding failed: %v", err)
				os.Exit(1)
			}
			os.Exit(0)
		}()
		app.Run()
		return
	}

	// Create server options based on flags
	serverOpts := &mcp.ServerOptions{
		Instructions:      serverInstructions(*enableXcode || *xcodeOnly, *xcodeToolsPrefix),
		Logger:            slog.Default(),
		CompletionHandler: completionHandler,
		RootsListChangedHandler: func(_ context.Context, req *mcp.RootsListChangedRequest) {
			slog.Debug("client roots changed", "session", req.Session.ID())
		},
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	}

	if *enableResources || *subscribeBuildErrors {
		serverOpts.Capabilities.Resources = &mcp.ResourceCapabilities{Subscribe: true}
	}
	if *subscribeBuildErrors {
		serverOpts.SubscribeHandler = func(_ context.Context, _ *mcp.SubscribeRequest) error {
			return nil
		}
		serverOpts.UnsubscribeHandler = func(_ context.Context, _ *mcp.UnsubscribeRequest) error {
			return nil
		}
	}

	// Create server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "xcmcp",
		Version: "0.1.0",
	}, serverOpts)

	// Detect context (Phase 2)
	// For now, we assume CWD is the root, or use a flag in future
	ctx := &resources.Context{
		ProjectRoot: ".",
	}

	// Register resources (Phase 2)
	if *enableResources {
		resources.Register(server, ctx)
	}

	// The xcode bridge toolset must be declared before registerToolsetTools
	// so it appears in list_toolsets / enable_toolset descriptions.
	addXcodeBridgeToolset(*xcodeToolsPrefix, *subscribeBuildErrors, *waitForXcode > 0)
	if *enableXcode || *xcodeOnly {
		_ = globalToolsets.enable(server, "xcode")
	}

	// Register native xcmcp tools (skipped with --xcode-only)
	if !*xcodeOnly {
		registerCoreTools(server)
		// list_toolsets + enable_toolset — declares all optional categories.
		registerToolsetTools(server)

		// Pre-enable toolsets selected via flags (same toolsets are also
		// available for dynamic enable via enable_toolset at runtime).
		for _, pair := range []struct {
			flag bool
			name string
		}{
			{*enableApp, "app"},
			{*enableUI, "ui"},
			{*enableDevice, "device"},
			{*enableDebugging, "debugging"},
			{*enableIOS, "ios"},
			{*enableSimExtras, "simulator_extras"},
			{*enablePhysical, "physical_device"},
			{*enableVideo, "video"},
			{*enableCrash, "crash"},
			{*enableFS, "filesystem"},
			{*enableDeps, "dependency"},
			{*enableASC, "asc"},
		} {
			if pair.flag {
				_ = globalToolsets.enable(server, pair.name)
			}
		}
	}

	log.Println("Starting xcmcp server...")

	// Create transport
	var transport mcp.Transport = &mcp.StdioTransport{}
	if os.Getenv("XCMCP_TRACE_RPC") == "1" {
		transport = &mcp.LoggingTransport{
			Transport: transport,
			Writer:    os.Stderr,
		}
	}

	serverCtx, stopServer := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopServer()

	runServer := func() {
		if permissionReady != nil {
			select {
			case err := <-permissionReady:
				if err != nil {
					log.Printf("permission onboarding failed: %v", err)
					os.Exit(1)
				}
			case <-serverCtx.Done():
				return
			}
		}
		if *waitForXcode > 0 {
			log.Printf("Waiting up to %v for Xcode bridge tools...", *waitForXcode)
			done := make(chan struct{})
			go func() { xcodeReady.Wait(); close(done) }()
			select {
			case <-done:
				log.Println("Xcode bridge ready, starting MCP server")
			case <-time.After(*waitForXcode):
				log.Println("Xcode bridge timeout, starting MCP server without bridge tools")
			case <-serverCtx.Done():
				return
			}
		}
		if err := server.Run(serverCtx, transport); err != nil {
			if serverCtx.Err() == nil {
				log.Printf("Server error: %v", err)
			}
		}
		ui.WaitForWindows()
		os.Exit(0)
	}

	if os.Getenv("XCMCP_NO_MACGO") == "1" {
		runServer()
		return
	}

	// Run server in goroutine to allow main thread to handle RunLoop.
	// When --wait-for-xcode is set, delay accepting connections until
	// the bridge tools have been discovered so the initial tools/list
	// response includes all Xcode tools.
	go runServer()

	// Run the RunLoop — must be on the main thread and must start promptly
	// so that AppKit can pump events (including the Xcode permission dialog).
	app.Run()
	_ = 42
}

func ensureUIPermissions() error {
	reqs := []permissions.Requirement{
		permissions.ReqAccessibility,
		permissions.ReqScreenRecording,
	}
	missing := make([]permissions.Requirement, 0, len(reqs))
	for _, req := range reqs {
		status := permissions.Check(req)
		slog.Info("checked permission", "permission", permissionName(req), "status", permissionStatusName(status))
		if status != permissions.StatusGranted {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := permissions.OnboardingWindow(context.Background(), missing...); err != nil && err != context.Canceled {
		return err
	}
	for _, req := range reqs {
		status := permissions.Check(req)
		slog.Info("checked permission", "permission", permissionName(req), "status", permissionStatusName(status))
		if status != permissions.StatusGranted {
			return fmt.Errorf("%s permission not granted", permissionName(req))
		}
	}
	return nil
}

func permissionName(req permissions.Requirement) string {
	switch req {
	case permissions.ReqAccessibility:
		return "Accessibility"
	case permissions.ReqScreenRecording:
		return "Screen Recording"
	default:
		return "unknown"
	}
}

func permissionStatusName(status permissions.Status) string {
	switch status {
	case permissions.StatusGranted:
		return "granted"
	case permissions.StatusDenied:
		return "denied"
	case permissions.StatusMissing:
		return "missing"
	case permissions.StatusStale:
		return "stale"
	case permissions.StatusInProgress:
		return "in_progress"
	default:
		return "unknown"
	}
}

func restoreMacgoDevModeStdin() {
	if os.Getenv("MACGO_NO_RELAUNCH") != "1" {
		return
	}
	stdinPipe := os.Getenv("MACGO_STDIN_PIPE")
	if stdinPipe == "" {
		return
	}
	f, err := os.OpenFile(stdinPipe, os.O_RDONLY, 0)
	if err != nil {
		slog.Warn("restore macgo dev-mode stdin failed", "pipe", stdinPipe, "err", err)
		return
	}
	os.Stdin = f
}

func removeAdHocBundle(appName string, wantSigned, devMode bool) {
	if appName == "" || !wantSigned || os.Getenv("MACGO_NO_RELAUNCH") != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil || strings.Contains(exe, ".app/Contents/MacOS/") {
		return
	}
	bundle := filepath.Join(filepath.Dir(exe), appName+".app")
	devTarget := filepath.Join(bundle, "Contents", "Resources", ".dev_target")
	_, statErr := os.Stat(devTarget)
	remove := !devMode && statErr == nil
	out, err := exec.Command("/usr/bin/codesign", "-dv", bundle).CombinedOutput()
	remove = remove || err == nil && strings.Contains(string(out), "Signature=adhoc")
	if !remove {
		plist := filepath.Join(bundle, "Contents", "Info.plist")
		bg, bgErr := exec.Command("/usr/bin/defaults", "read", plist, "LSBackgroundOnly").Output()
		remove = bgErr == nil && strings.TrimSpace(string(bg)) == "1"
	}
	if !remove {
		return
	}
	if err := os.RemoveAll(bundle); err != nil {
		slog.Warn("remove stale app bundle failed", "bundle", bundle, "err", err)
		return
	}
	slog.Info("removed stale app bundle", "bundle", bundle)
}

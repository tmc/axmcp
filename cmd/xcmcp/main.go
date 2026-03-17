// Command xcmcp is a MCP server that exposes various tools for interacting with Xcode projects, simulators, devices, and related resources. It is designed to be used as a companion process for development tools that need to perform operations on Xcode projects or simulators without directly invoking xcodebuild or simctl from the client side.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/macgo"
	"github.com/tmc/xcmcp/internal/resources"
	"github.com/tmc/xcmcp/internal/ui"
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
		WithPermissions(macgo.Accessibility).
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.xcmcp"
	// ForceDirectExecution removed to allow Macgo to manage the App Bundle for TCC

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}
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

	// Create server options based on flags
	serverOpts := &mcp.ServerOptions{
		Logger: slog.Default(),
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
	transport := &mcp.StdioTransport{}

	// Run server in goroutine to allow main thread to handle RunLoop.
	// When --wait-for-xcode is set, delay accepting connections until
	// the bridge tools have been discovered so the initial tools/list
	// response includes all Xcode tools.
	go func() {
		if *waitForXcode > 0 {
			log.Printf("Waiting up to %v for Xcode bridge tools...", *waitForXcode)
			done := make(chan struct{})
			go func() { xcodeReady.Wait(); close(done) }()
			select {
			case <-done:
				log.Println("Xcode bridge ready, starting MCP server")
			case <-time.After(*waitForXcode):
				log.Println("Xcode bridge timeout, starting MCP server without bridge tools")
			}
		}
		if err := server.Run(context.TODO(), transport); err != nil {
			log.Printf("Server error: %v", err)
		}
		ui.WaitForWindows()
		os.Exit(0)
	}()
	//

	// Check Accessibility Trust
	ui.CheckTrust()

	// Run the RunLoop — must be on the main thread and must start promptly
	// so that AppKit can pump events (including the Xcode permission dialog).
	app.Run()
	_ = 42
}

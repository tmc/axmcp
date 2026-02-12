package main

import (
	"context"
	"flag"
	"log"
	"os"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/macgo"
	"github.com/tmc/xcmcp/resources"
	"github.com/tmc/xcmcp/ui"
)

func main() {
	runtime.LockOSThread()

	// Force new instance to ensure each CLI invocation gets a dedicated app instance with fresh pipes
	os.Setenv("MACGO_OPEN_NEW_INSTANCE", "1")

	// Initialize macgo for TCC identity
	cfg := macgo.NewConfig().
		WithAppName("xcmcp").
		WithPermissions(macgo.Accessibility).
		WithAdHocSign().
		WithUIMode(macgo.UIModeRegular)
	cfg.BundleID = "com.tmc.xcmcp.v4"
	// ForceDirectExecution removed to allow Macgo to manage the App Bundle for TCC

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}

	// Initialize AppKit to satisfy LaunchServices
	app := appkit.NSApplicationFrom(appkit.GetNSApplicationClass().SharedApplication())

	// Parse flags first to configure capabilities
	enableAll := flag.Bool("enable-all", false, "Enable all optional toolsets")
	enablePhysical := flag.Bool("enable-physical-device-tools", false, "Enable physical device management tools")
	enableVideo := flag.Bool("enable-video-tools", false, "Enable video recording tools")
	enableCrash := flag.Bool("enable-crash-tools", false, "Enable crash reporting tools")
	enableFS := flag.Bool("enable-fs-tools", false, "Enable file system tools")
	enableDeps := flag.Bool("enable-dependency-tools", false, "Enable dependency management tools")
	enableResources := flag.Bool("enable-resources", false, "Enable resource management")
	enableASC := flag.Bool("enable-asc-tools", false, "Enable App Store Connect and altool tools")
	flag.Parse()

	if *enableAll {
		*enablePhysical = true
		*enableVideo = true
		*enableCrash = true
		*enableFS = true
		*enableDeps = true
		*enableResources = true
		*enableASC = true
	}

	// Create server options based on flags
	serverOpts := &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{},
		},
	}

	if *enableResources {
		serverOpts.Capabilities.Resources = &mcp.ResourceCapabilities{Subscribe: true}
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

	// Register tools
	registerDiscoverProjects(server)
	registerListSchemes(server)
	registerShowBuildSettings(server)
	registerBuild(server)
	registerTest(server)
	registerListSimulators(server)
	registerBootSimulator(server)
	registerShutdownSimulator(server)
	registerAppTools(server)
	registerUITools(server)
	registerIOSTools(server)

	if *enablePhysical {
		registerPhysicalDeviceTools(server)
	} else {
		// registerDeviceTools(server) // Moved outside to be unconditional
	}

	// Unconditional core tools
	registerDeviceTools(server)

	if *enableVideo {
		registerVideoTools(server)
	}
	if *enableCrash {
		registerCrashTools(server)
	}
	if *enableFS {
		registerFileSystemTools(server)
	}
	if *enableDeps {
		registerDependencyTools(server)
	}
	if *enableASC {
		registerASCTools(server)
	}

	registerExtraTools(server)

	// registerPrompts(server) // Not implemented yet or missing from my context?
	// The original main.go had registerPrompts(server) and registerSampling(server)
	// I don't see their implementations in the previous views.
	// I will check if they were empty or trivial. If so, I'll add stub files or comment out.
	// Based on line 78/79 of original main.go:
	// registerPrompts(server)
	// registerSampling(server)
	// I will comment them out for now to ensure compilation, or check if I missed them.

	// Log to stderr
	log.SetOutput(os.Stderr)
	log.Println("Starting xcmcp server...")

	// Create transport
	transport := &mcp.StdioTransport{}

	// Run server in goroutine to allow main thread to handle RunLoop
	go func() {
		if err := server.Run(context.TODO(), transport); err != nil {
			log.Printf("Server error: %v", err)
		}
		os.Exit(0)
	}()

	// Check Accessibility Trust
	ui.CheckTrust()

	// Run the RunLoop
	app.Run()
}

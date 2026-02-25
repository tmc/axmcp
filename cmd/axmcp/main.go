// Command axmcp is an MCP server for macOS Accessibility API automation.
//
// It exposes the AX element tree, querying, and interaction tools over the
// Model Context Protocol, running as a macOS app bundle for Accessibility TCC.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/macgo"
	"github.com/tmc/xcmcp/axuiautomation"
)

func main() {
	verbose := flag.Bool("v", false, "enable verbose debug logging")
	flag.Parse()

	runtime.LockOSThread()

	cfg := macgo.NewConfig().
		WithAppName("axmcp").
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSScreenCaptureUsageDescription", "axmcp needs to capture screenshots of specific UI elements and windows.").
		WithAdHocSign()
	if *verbose {
		cfg = cfg.WithDebug()
	}
	cfg.BundleID = "dev.tmc.axmcp"

	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo start failed: %v", err)
	}

	logLevel := slog.LevelWarn
	if *verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

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

	axuiautomation.CheckTrust()

	isScreenshot := len(os.Args) >= 2 && os.Args[1] == "screenshot"
	isServer := !isTTY() && len(os.Args) <= 1

	if isScreenshot || isServer {
		axuiautomation.CheckScreenCapture()
	}

	if isTTY() || len(os.Args) > 1 {
		// Run CLI in goroutine so main thread can drive the AppKit run loop.
		go func() {
			time.Sleep(100 * time.Millisecond)
			for !axuiautomation.AXIsProcessTrusted() {
				time.Sleep(500 * time.Millisecond)
			}
			// Wait for Screen Recording if screenshotting.
			if len(os.Args) >= 2 && os.Args[1] == "screenshot" {
				for !axuiautomation.IsScreenRecordingTrusted() {
					time.Sleep(500 * time.Millisecond)
				}
			}
			runCLI()
			// runCLI calls os.Exit on completion, so this goroutine won't return
		}()
	} else {
		// Run MCP server in goroutine so main thread can drive the AppKit run loop.
		go func() {
			if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
				log.Printf("server error: %v", err)
			}
			os.Exit(0)
		}()
	}

	// Run the AppKit event loop on the main thread. This drains CFRunLoop,
	// the GCD main queue, and AppKit UI events (buttons, windows, etc.).
	app.Run()
	_ = 42
}

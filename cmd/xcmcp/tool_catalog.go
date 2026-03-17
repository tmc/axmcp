package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

func registerCoreTools(s *mcp.Server) {
	registerDiscoverProjects(s)
	registerListSchemes(s)
	registerShowBuildSettings(s)
	registerBuild(s)
	registerTest(s)
	registerNativeWorkflowTools(s)
	registerListSimulators(s)
	registerBootSimulator(s)
	registerShutdownSimulator(s)
	registerSwiftUIPreviewFeatures(s)
}

func standardToolsets() []toolset {
	return []toolset{
		{
			name:        "app",
			description: "App management tools: launch, terminate, install, uninstall, logs, list apps",
			register:    registerAppTools,
		},
		{
			name:        "ui",
			description: "UI automation tools: tap, tree, screenshot, query, inspect, wait, list windows, list buttons",
			register:    registerUITools,
		},
		{
			name:        "device",
			description: "Simulator device control: orientation, appearance, location, biometry, privacy, screenshot",
			register:    registerDeviceTools,
		},
		{
			name:        "debugging",
			description: "LLDB debugging tools for attaching to running macOS apps and inspecting live state",
			register:    registerDebuggingTools,
		},
		{
			name:        "ios",
			description: "iOS-specific tools: accessibility tree, hit testing, simulator list, device info",
			register:    registerIOSTools,
		},
		{
			name:        "simulator_extras",
			description: "Simulator extras: app container path, open URL, add photos/videos to library",
			register:    registerExtraTools,
		},
		{
			name:        "physical_device",
			description: "Tools for managing physical iOS/macOS devices (install, run, logs, etc.)",
			register:    registerPhysicalDeviceTools,
		},
		{
			name:        "video",
			description: "Video recording tools for simulators",
			register:    registerVideoTools,
		},
		{
			name:        "crash",
			description: "Crash log collection and symbolication tools",
			register:    registerCrashTools,
		},
		{
			name:        "filesystem",
			description: "File system access tools for simulator and device containers",
			register:    registerFileSystemTools,
		},
		{
			name:        "dependency",
			description: "Dependency management tools (CocoaPods, Swift Package Manager)",
			register:    registerDependencyTools,
		},
		{
			name:        "asc",
			description: "App Store Connect and altool tools for distribution and TestFlight",
			register:    registerASCTools,
		},
	}
}

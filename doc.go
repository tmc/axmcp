// Package axmcp documents the axmcp module.
//
// axmcp is a macOS automation toolkit built around three MCP servers and
// matching CLIs:
//
//   - cmd/axmcp, an open Accessibility surface for any running macOS app
//   - cmd/xcmcp, for Xcode, simulators, devices, previews, and App Store
//     Connect workflows
//   - cmd/computer-use-mcp, a stateful server that implements the Codex
//     Computer Use tool contract on top of the same primitives
//
// The module is command-first. Internal packages are shared implementation
// libraries, not a public import surface.
//
// # Commands
//
// The main entry points are:
//
//   - cmd/axmcp, a stdio MCP server for macOS Accessibility automation
//   - cmd/xcmcp, a stdio MCP server for project inspection, build and test,
//     simulator control, device control, UI inspection, and Xcode integration
//   - cmd/computer-use-mcp, a stdio MCP server implementing the 9-tool Codex
//     Computer Use contract with per-session application state
//   - cmd/xc, a direct CLI built on the same packages
//   - cmd/ax, a direct CLI for the macOS Accessibility API
//   - cmd/ascript and cmd/ascriptmcp, tools for scriptable macOS applications
//
// # Internal Packages
//
// The main implementation packages are:
//
//   - internal/project, for discovering Xcode projects and schemes
//   - internal/xcodebuild, for build and test execution
//   - internal/xcodewizard, for File > New > Target UI automation shared
//     by cmd/xc and cmd/xcmcp
//   - internal/simctl, for simulator management through xcrun simctl
//   - internal/devicectl, for physical device management
//   - internal/ui, for macOS Accessibility access and UI screenshots
//   - internal/screen, for screen capture helpers
//   - internal/ghostcursor, for an animated cursor overlay
//   - internal/computeruse, for the primitives behind cmd/computer-use-mcp
//     (app state, input, coords, policy, session, approval, intervention)
//   - internal/crash, for crash report inspection
//   - internal/resources, for MCP resource registration
//
// # Environment
//
// axmcp targets macOS and assumes Xcode and the simulator tooling are
// installed. Packages that drive the UI require Accessibility permission
// in System Settings > Privacy & Security > Accessibility. Simulator and
// device features also depend on the corresponding runtime state, such as
// a booted simulator or a connected device.
//
// cmd/axmcp honors a small set of optional AXMCP_* environment variables
// for ghost-cursor pacing, demo highlighting, and crash diagnostics. See
// the repository README for the full list with defaults and units.
//
// This package exists to document the module as a whole. The supported entry
// points are the commands under cmd/. Library code lives in internal/
// packages and is not intended as a public import surface.
package axmcp

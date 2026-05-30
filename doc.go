// Package axmcp documents the axmcp module.
//
// axmcp is a macOS automation toolkit built around MCP servers, direct CLIs,
// and a Chrome DevTools Protocol endpoint:
//
//   - cmd/axmcp, an open Accessibility surface for any running macOS app
//   - cmd/xcmcp, for Xcode, simulators, devices, previews, and App Store
//     Connect workflows
//   - cmd/computer-use-mcp, a stateful server that implements the Codex
//     Computer Use tool contract on top of the same primitives
//   - cmd/iphonemirror-mcp, an OCR and input surface for Apple's iPhone
//     Mirroring app
//   - cmd/axcdp, a CDP remote-debugging endpoint backed by macOS
//     Accessibility, screen capture, and native overlays
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
//   - cmd/iphonemirror-mcp, a stdio MCP server for controlling iPhone
//     Mirroring through OCR, focus, and synthetic input
//   - cmd/axcdp, a Chrome DevTools Protocol endpoint for native macOS UI
//     inspection; it is not an MCP server
//   - cmd/xc, a direct CLI built on the same packages
//   - cmd/ax, a direct CLI for the macOS Accessibility API
//   - cmd/ascript and cmd/ascriptmcp, tools for scriptable macOS applications
//   - cmd/rapport-probe, a research command for inspecting Rapport.framework
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
//   - internal/spacedetect, for off-Space window detection via private
//     SkyLight symbols (powers the "off_space" field on ax_list_windows)
//   - internal/ghostcursor, for an animated cursor overlay
//   - internal/computeruse, for the primitives behind cmd/computer-use-mcp
//     and cmd/iphonemirror-mcp (appstate, input, coords, imagehash, policy,
//     session, approval, intervention, instruction, magnify, overlay)
//   - internal/ocrwindow, for window capture plus Vision OCR shared by
//     iPhone Mirroring tools
//   - internal/skylightinput, for optional private SkyLight input posting
//     with public-API fallback
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
//
// # Repository Hygiene
//
// The .github/workflows/hygiene-verify.yml workflow gates main and PR
// commits against six rules: structured AI-trailer scan, subject length,
// lowercase-imperative subject, go mod tidy, build/vet/test, and a
// tracked-scratch / leaked-path scan. See design/hygiene-verify.md for
// the rationale and the open review punch list.
package axmcp

# axmcp

**Drive macOS apps the way a human does — through the Accessibility API, from an MCP client.**

`axmcp` is a macOS automation toolkit built around three MCP servers and matching CLIs. It lets an LLM (or a shell script) inspect and operate native apps, Xcode projects, and iOS simulators with the same primitives Apple's own assistive technologies use.

| Server | What it drives | Shape |
| --- | --- | --- |
| `cmd/axmcp` | Any running macOS app via AX tree, OCR, pointer, keyboard, windows | Open primitive surface |
| `cmd/xcmcp` | Xcode, simulators, physical devices, previews, App Store Connect | Toolset-gated, ~40 tools on demand |
| `cmd/computer-use-mcp` | Codex Computer Use contract on top of axmcp primitives | Exactly the 9-tool spec, session-stateful |

If you want an LLM to click through a real app: `axmcp`. If you want it to build, test, boot a simulator, or add an Xcode target via the File > New UI: `xcmcp`. If you need a drop-in for the Codex Computer Use tool contract: `computer-use-mcp`.

## Why this exists

- **Accessibility-first, not screenshot-first.** Pointer actions target AX elements by role and title, so an LLM can say "click the Build button" and get the actual button — not the pixel that looked right a moment ago.
- **OCR and screenshots are the fallback, not the plan.** `ax_ocr`, `ax_ocr_diff`, and `ax_ocr_click` exist for Electron apps, custom canvases, and partially inaccessible UI. They ride on top of the AX flow, not around it.
- **Xcode, fully drivable.** See the Xcode automation section below — from `xcodebuild` wrappers and simulator control all the way up to driving the File > New > Target wizard through the live UI when `xcodebuild` can't do the job.
- **One repo, three audiences.** The primitive surface (`axmcp`) is for open exploration; the Codex contract (`computer-use-mcp`) is for drop-in replacement; the Xcode surface (`xcmcp`) is the IDE-adjacent tool belt. They share one set of internal packages so behavior stays consistent.

## Xcode automation, top to bottom

`xcmcp` and its CLI twin `xc` cover the whole Apple-developer loop, not just `xcodebuild` calls. Across its toolsets it speaks to:

- **Projects and schemes** — `discover_projects`, `list_schemes`, `show_build_settings` read `.xcodeproj` / `.xcworkspace` directly.
- **Build and test** — `build` and `test` wrap `xcodebuild` with structured output and `GetTestList` / `RunSomeTests` for targeted runs; `GetBuildLog` returns the last run's log for triage.
- **Simulators** — `list_simulators`, `boot_simulator`, `shutdown_simulator`, plus the `simulator_extras` toolset for `open_url`, `add_media`, app containers, orientation, appearance, location, privacy toggles, and screen recording.
- **Physical devices** — `physical_device` toolset for inspection and app lifecycle over `devicectl`.
- **App lifecycle** — `install`, `uninstall`, `launch`, `terminate`, `logs`, `list_apps`, `running_apps` across sim and device.
- **IDE bridge.** The always-on Xcode bridge toolset exposes Xcode's own IDE-side helpers — `XcodeRead`, `XcodeWrite`, `XcodeUpdate`, `XcodeGlob`, `XcodeGrep`, `XcodeLS`, `XcodeMV`, `XcodeRM`, `XcodeMakeDir`, `XcodeListNavigatorIssues`, `XcodeRefreshCodeIssuesInFile`, `XcodeListWindows`, `ExecuteSnippet`, `DocumentationSearch` — so any MCP client (Cursor, Zed, Windsurf, Aider, etc.) can read project files with the IDE's own picture of the workspace, list Navigator issues, refresh diagnostics on save, and run snippets in Xcode's context.
- **Preview rendering from any agent.** `RenderPreview` and `render_all_previews` return real SwiftUI preview renders, so an agent editing views can close the visual loop without a human opening Xcode to check the canvas. Pair with `XcodeListNavigatorIssues` and `XcodeRefreshCodeIssuesInFile` and the edit → render → compile-check loop runs end-to-end over MCP.
- **File > New > Target, automated.** The headline: `xcode_add_target` drives the target-creation sheet end-to-end over AX — platform tab, template picker, product name, team, bundle ID, and the "Embed in Application" popup for extensions — then re-reads the project on disk to verify the new target/scheme actually landed. The wizard logic lives in `internal/xcodewizard` and is shared by both the MCP tool and `xc xcode add-target`.
- **Code signing and shipping** — the `asc` toolset wraps App Store Connect and `altool` for authentication key management, app record queries, build uploads, and TestFlight handoff.
- **Crash reports and SPM** — `crash` toolset lists and reads `.ips` reports; `dependency` toolset covers Swift Package Manager operations.

Everything is toolset-gated, so you only load what you need:

```sh
xcmcp --enable-all           # every toolset on
xcmcp --enable-ui-tools      # just UI automation of the sim
xcmcp --enable-asc-tools     # App Store Connect + altool
```

Or turn toolsets on and off dynamically inside a session via `list_toolsets` and `enable_toolset`.

## Requirements

- macOS with Xcode installed.
- Command Line Tools available through `xcrun`.
- Go 1.26 or newer to build from source.
- Accessibility permission for commands that drive the UI: `axmcp`, `ax`, `xcmcp`, `xc`, `computer-use-mcp`.
- A booted simulator or connected device for simulator and device workflows.

## Setup

Follow these steps in order. Every command is safe to run unattended.

### 1. Install the binaries

From a clone of this repo:

```sh
go install ./cmd/axmcp ./cmd/xcmcp ./cmd/computer-use-mcp ./cmd/ax ./cmd/xc ./cmd/ascript ./cmd/ascriptmcp
```

Or directly from the module path without cloning:

```sh
go install github.com/tmc/axmcp/cmd/axmcp@latest
go install github.com/tmc/axmcp/cmd/xcmcp@latest
go install github.com/tmc/axmcp/cmd/computer-use-mcp@latest
go install github.com/tmc/axmcp/cmd/ax@latest
go install github.com/tmc/axmcp/cmd/xc@latest
```

`go install` writes to `$(go env GOPATH)/bin` (default `$HOME/go/bin`), or `$(go env GOBIN)` if set.

### 2. Put the install directory on `PATH`

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Add that line to your shell rc if you want it permanent. Verify:

```sh
command -v axmcp xcmcp computer-use-mcp xc ax
```

Five absolute paths should print. If any are missing, step 1 failed for that binary.

### 3. Note the absolute binary paths for your MCP client

```sh
echo "AXMCP=$(command -v axmcp)"
echo "XCMCP=$(command -v xcmcp)"
echo "COMPUTER_USE_MCP=$(command -v computer-use-mcp)"
```

You will paste these exact paths into your MCP client config in step 5.

### 4. Grant Accessibility permission

macOS refuses every pointer, keystroke, and AX tree call until the calling binary is explicitly approved in **System Settings → Privacy & Security → Accessibility**.

The first time any of `axmcp`, `xcmcp`, `ax`, `xc`, or `computer-use-mcp` issues an AX call, macOS refuses and adds the binary as an unchecked row in that pane. Open it and toggle each entry on.

If an action later no-ops silently or returns "not permitted," the entry was probably turned off again by a software update — re-check the toggle.

### 5. Register the servers with your MCP client

See **MCP client configuration** below for ready-to-paste snippets for Claude Code, Cursor, Zed, and the raw JSON schema.

### 6. Verify the server surfaces are reachable

Once your client has loaded the config, confirm the tools showed up. A quick sanity check outside any client:

```sh
printf '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}\n' | axmcp 2>/dev/null | head -c 400
```

Non-empty JSON with a `"tools"` array means the binary is healthy and its stdio MCP transport is wired up.

## Quick Start

**Drive any running app:**

```sh
axmcp
```

Exposes `ax_apps`, `ax_tree`, `ax_find`, `ax_focus`, `ax_click`, `ax_drag`, `ax_type`, `ax_menu`, `ax_set_value`, `ax_perform_action`, `ax_keystroke`, `ax_zoom`, `ax_pinch`, `ax_screenshot`, `ax_ocr`, `ax_ocr_diff`, `ax_action_screenshot`, `ax_ocr_action_diff`, `ax_ocr_click`, `ax_ocr_hover`, plus window-scoped variants (`ax_window_*`).

**Work with Xcode and simulators:**

```sh
xcmcp
```

Always-on: `discover_projects`, `list_schemes`, `show_build_settings`, `build`, `test`, `list_simulators`, `boot_simulator`, `shutdown_simulator`, `xcode_add_target`, `list_toolsets`, `enable_toolset`. The Xcode bridge toolset (IDE-facing tools from `xcrun mcpbridge`) is enabled by default.

Optional toolsets are loaded on demand:

```sh
xcmcp --enable-ui-tools --enable-device-tools --enable-ios-tools
```

Or all at once:

```sh
xcmcp --enable-all
```

**Drop-in Codex Computer Use contract:**

```sh
computer-use-mcp
```

Exposes exactly `list_apps`, `get_app_state`, `click`, `perform_secondary_action`, `set_value`, `scroll`, `drag`, `press_key`, `type_text`. Call `get_app_state` once per turn, then act against `element_index` strings from the snapshot.

## MCP client configuration

All three servers speak MCP over stdio. In every config below, replace `/Users/you/go/bin/...` with the absolute paths you printed in setup step 3.

### Claude Code

```sh
claude mcp add axmcp /Users/you/go/bin/axmcp
claude mcp add xcmcp /Users/you/go/bin/xcmcp -- --enable-all
claude mcp add computer-use-mcp /Users/you/go/bin/computer-use-mcp
```

Or edit `~/.claude.json` directly:

```json
{
  "mcpServers": {
    "axmcp": { "command": "/Users/you/go/bin/axmcp" },
    "xcmcp": { "command": "/Users/you/go/bin/xcmcp", "args": ["--enable-all"] },
    "computer-use-mcp": { "command": "/Users/you/go/bin/computer-use-mcp" }
  }
}
```

### Cursor

Edit `~/.cursor/mcp.json` (same schema as above):

```json
{
  "mcpServers": {
    "axmcp": { "command": "/Users/you/go/bin/axmcp" },
    "xcmcp": { "command": "/Users/you/go/bin/xcmcp", "args": ["--enable-all"] },
    "computer-use-mcp": { "command": "/Users/you/go/bin/computer-use-mcp" }
  }
}
```

### Zed

Edit `~/.config/zed/settings.json`:

```json
{
  "context_servers": {
    "axmcp": { "command": { "path": "/Users/you/go/bin/axmcp", "args": [] } },
    "xcmcp": { "command": { "path": "/Users/you/go/bin/xcmcp", "args": ["--enable-all"] } },
    "computer-use-mcp": { "command": { "path": "/Users/you/go/bin/computer-use-mcp", "args": [] } }
  }
}
```

### Any other MCP client

Point it at the absolute binary path with stdio transport. Optional arguments for `xcmcp`:

- `--enable-all` — turn every optional toolset on
- `--enable-ui-tools --enable-device-tools --enable-ios-tools` — pick toolsets individually
- `--enable-asc-tools` — App Store Connect + altool
- `--wait-for-xcode=0s` — skip waiting for Xcode at startup (for headless/CI use)

Within a session, `list_toolsets` and `enable_toolset` turn optional `xcmcp` toolsets on and off without restarting.

## Commands

### `axmcp`

`axmcp` is the open Accessibility surface. It targets running macOS applications directly through the Accessibility API, with OCR and screenshot fallbacks for custom-drawn or partially inaccessible UIs.

Primitive tools cover element discovery, pointer and keyboard input, window manipulation, screenshots, and OCR-driven interactions.

### `computer-use-mcp`

`computer-use-mcp` is the stateful, session-oriented compatibility server. It holds the narrow Codex Computer Use tool contract on top of the same accessibility and screenshot primitives.

It is tools-only — no MCP resources, no resource templates. The tool surface is app-scoped: call `get_app_state` first, then pass returned `element_index` strings to the action tools.

### `xcmcp`

`xcmcp` serves tools and resources over stdio. See the Xcode automation section above for the full surface; this section just lists the optional toolsets.

Optional toolsets:

- `app`: app lifecycle, install, uninstall, logs, and app listing
- `ui`: UI tree, tap, inspect, query, screenshot, and wait
- `device`: simulator orientation, privacy, location, appearance, and screenshots
- `ios`: direct CoreSimulator-based accessibility tree and hit-testing
- `simulator_extras`: open URL, add media, and app container lookup
- `physical_device`: connected device inspection and app lifecycle actions
- `video`: simulator recording
- `crash`: crash report listing and reading
- `filesystem`: file access helpers
- `dependency`: Swift Package Manager helpers
- `asc`: App Store Connect and `altool` helpers

### `xc`

`xc` exposes the same Xcode automation as a direct CLI, so you can script loops that would otherwise need the IDE.

```sh
xc project                                   # resolve the Xcode project from cwd
xc sims list                                 # see all available simulators
xc sims boot 8A2F1C4D-...                    # boot one
xc build --scheme MyApp                      # xcodebuild wrapper
xc test --scheme MyApp                       # run the full test plan
xc app install ./build/MyApp.app             # install to booted sim
xc app launch com.example.MyApp --udid booted
xc app logs com.example.MyApp                # stream device/sim logs
xc ui tree --bundle-id com.apple.finder      # AX tree of a running app
xc ios tree --udid booted                    # direct CoreSimulator AX tree
xc screen shot ~/Desktop/sim.png             # simulator screenshot

# drive the File > New > Target wizard, end-to-end
xc xcode add-target \
    --template "Widget Extension" \
    --product MyWidget \
    --platform iOS \
    --embed-in MyApp
```

### `ax`

`ax` is the direct CLI companion to `axmcp`:

```sh
ax apps
ax tree com.apple.finder
ax find com.apple.dt.Xcode --role AXButton --title Build
```

### `ascript` and `ascriptmcp`

These commands inspect scriptable applications and run AppleScript-backed operations:

```sh
ascript list /Applications/Xcode.app
ascript classes /Applications/Finder.app
ascript script /Applications/Finder.app activate
```

## Resources

`xcmcp` registers these MCP resources by default:

- `xcmcp://project`
- `xcmcp://simulators`
- `xcmcp://apps`
- `xcmcp://apps/{bundle_id}/tree`
- `xcmcp://apps/{bundle_id}/logs`

## Internal layout

This module is command-first. Reusable helpers live under `internal/` and are not intended as a public import surface.

- `internal/project`: discover Xcode projects, inspect schemes and build settings
- `internal/xcodebuild`: build and test wrappers
- `internal/xcodewizard`: File > New > Target wizard automation, shared by `xc` and `xcmcp`
- `internal/simctl`: simulator management through `xcrun simctl`
- `internal/devicectl`: physical device management
- `internal/ui`: macOS Accessibility access and UI screenshots
- `internal/screen`: screen capture helpers
- `internal/ghostcursor`: animated cursor overlay for demonstration and recording
- `internal/computeruse`: shared primitives behind `computer-use-mcp` (app state, input, coords, policy, session, approval, intervention)
- `internal/crash`: crash report listing and reading
- `internal/resources`: MCP resource registration
- `internal/sdef`: parser for AppleScript scripting definitions
- `internal/altool` and `internal/asc`: App Store Connect helpers
- `internal/tccprompt`: TCC permission prompt inspection

## Environment variables

`axmcp` reads a small set of optional environment variables. Defaults are picked for unattended use; the tunables exist mostly for debugging and demos.

| Variable | Default | Effect |
|---|---|---|
| `AXMCP_CURSOR_GLIDE_MS` | `280` | Duration in milliseconds the ghost cursor takes to glide to a click target. Set to `0` to teleport. |
| `AXMCP_CURSOR_SETTLE_MS` | `90` | Milliseconds the cursor pauses on target in its Pressed state before the click is dispatched. `0` disables the dwell. |
| `AXMCP_CURSOR_HOLD_MS` | `200` | Milliseconds the cursor stays visible after the click before fading. `0` disables the hold. |
| `AXMCP_HIGHLIGHT_HUMAN` | unset | When set to a truthy value, draws a visible highlight around OCR matches and click targets — useful when recording demos. |
| `AXMCP_C_SIGNAL_BACKTRACE` | unset | When set to `1`, installs a C-level signal handler that prints a native backtrace on crash. Off by default to avoid interfering with Go's own crash reporter. |

## Notes

- This repository targets macOS. Many packages use AppKit, Accessibility, or Apple developer tools directly.
- The repository name is `axmcp`, but it intentionally contains `axmcp`, `xcmcp`, and `computer-use-mcp`. They cover different layers of the same workflow surface.
- UI automation depends on macOS Accessibility permission and on the target app being reachable through the Accessibility tree.
- Some simulator and Xcode automation features rely on private or implementation-defined behavior and are best treated as developer tooling rather than a stable public protocol.
- The supported entry points are the commands in `cmd/`. Internal packages may change without notice.

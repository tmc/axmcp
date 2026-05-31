# CUA and OpenAI Computer Use parity

This note records the current compatibility boundary against:

- CUA Driver:
  <https://cua.ai/docs/cua-driver/guide/getting-started/introduction>
- CUA Driver comparison:
  <https://cua.ai/docs/cua-driver/guide/getting-started/comparison>
- OpenAI Computer Use:
  <https://developers.openai.com/api/docs/guides/tools-computer-use>

## Current match

- `cmd/computer-use-mcp` is the Codex-compatible MCP surface in this
  repo. It exposes `list_apps`, `get_app_state`, `click`,
  `perform_secondary_action`, `set_value`, `scroll`, `drag`,
  `press_key`, and `type_text` over stdio.
- The action loop is stateful: call `get_app_state` each turn, pass the
  returned `state_id` to actions, and refresh state when an action
  reports stale or missing state.
- `get_app_state` returns an accessibility tree plus a screenshot by
  default. `omit_screenshot=true` suppresses the PNG when a caller only
  needs metadata and element IDs.
- Element-index actions use Accessibility when available. Pixel actions
  can target screenshot coordinates, with `foreground_hid=true` as an
  explicit fallback for opaque canvas, WebGL, Metal, and game-like views
  that reject background events.

## CUA Driver comparison

CUA Driver documents three capture modes: `vision` for pixels, `ax` for
the accessibility tree, and `som` for both. axmcp does not expose a
single `capture_mode` switch today. Its current shape is:

- `get_app_state`: SOM-like by default, because it returns both the AX
  tree and a screenshot.
- `get_app_state` with `omit_screenshot=true`: AX-like, because it
  keeps element metadata and IDs without the PNG payload.
- `ax_screenshot`, `ax_ocr`, and pixel-coordinate click paths:
  vision-oriented helper paths, not a separate Computer Use mode.

CUA Driver also documents a default no-foreground contract. axmcp's
default direction is background operation through Accessibility and
pid-routed input where possible, but it is not a full no-foreground
equivalent. `click.foreground_hid` intentionally activates the target
application and may steal focus; it exists for targets that reject
background delivery.

## Known gaps

- No top-level CUA-compatible `capture_mode` setting.
- No cross-platform CUA parity. This repo targets macOS; non-darwin
  builds are for installability, not functional desktop automation.
- No claim that every action preserves the user's foreground app.
- `SLEventPostToPid` support is present in `internal/skylightinput`,
  but app coverage still depends on target behavior and fallback paths.
- `internal/axpump` exists to keep Chromium-family AX trees populated,
  but it is an implementation helper, not a public event stream or
  daemon contract.

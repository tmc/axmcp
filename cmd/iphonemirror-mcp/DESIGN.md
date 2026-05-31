# iphonemirror-mcp design doc

Status: living document. Started 2026-04-28.

This doc captures the gesture surface plan, the implementation tiers, and
the queue of follow-up work after v0.1.7.

## Goals

- Make `iphonemirror-mcp` a complete remote-control surface for iOS apps
  through Apple's iPhone Mirroring host on macOS.
- Match the gesture vocabulary an iOS user has on the device itself, to the
  extent the host forwards each gesture.
- Be honest in tool descriptions about what the tool can and cannot do.

## Verified facts

- Transport for mouse events: public `CGEventPost(kCGHIDEventTap, ...)`
  with `kCGEventSourceStateHIDSystemState` source, 3-step move-to-target,
  ClickState=1 stamp. `CGEventPostToPid` works for keystrokes but the
  iPhone Mirroring receiver gates mouse on a stricter trust path; per-pid
  mouse events silently drop.
- Required precondition: the running .app bundle must hold an
  Accessibility TCC grant. cdhash invalidates on every rebuild
  (ad-hoc-signed) so each rebuild requires re-grant. The MCP startup
  banner makes this loud (v0.1.5+); each input tool returns a clear error
  if the grant is missing (v0.1.5+).
- Between read calls (e.g. ax_screenshot) iPhone Mirroring may lose
  AppKit-frontmost. `focusIPhoneMirroring(pid, windowID)` uses
  `skylightinput.ActivateWithoutRaise` to re-activate without raising
  the window or following the user across Spaces. The helper can focus
  by pid when a window id is not available. Wired into every input tool
  (v0.1.6+).
- Keystrokes sent via `CGEventPostToPid` reach iOS as native HID keyboard
  events; iOS apps that implement `UIKeyCommand` (Photos, Maps, Safari,
  Freeform) respond as if the user pressed the key.

## Implementation tiers

### Tier 1 - public CGEvent / keyboard recipes

These need no SPI. v0.1.7 ships all four:

- `iphone_long_press` - mouseDown -> sleep -> mouseUp on kCGHIDEventTap.
  Optional 1px jitter mid-hold to keep gesture recognizers alive on
  receivers that filter zero-delta presses.
- `iphone_double_tap` - `clickScreenPoint(count=2)`.
- `iphone_zoom_in` - `Cmd+Plus` via SendKeyComboToPID.
- `iphone_zoom_out` - `Cmd+Minus` via SendKeyComboToPID.

### Tier 2 - research, kCGEventGesture or SkyLight tablet

Time-boxed probes. Don't ship until verified end-to-end.

- `iphone_pinch` - synthesize `kCGEventGesture` magnify (CGEventType=29)
  and post on kCGHIDEventTap. AppKit validates timestamp, momentum
  phase, and event sub-type before forwarding, so a malformed event is
  dropped before iPhone Mirroring sees it. Probe target: any iOS app
  with a UIKit pinch recognizer that does NOT honor `UIKeyCommand` for
  zoom (so a positive result rules out the keyboard explanation).
- `iphone_two_finger_swipe` - same SPI surface as magnify; needs
  scroll-gesture sub-type.

### Tier 3 - out of scope

Continuity / RemoteUI IPC interception is too large a project. Don't.

## Tool inventory after v0.1.7

| Tool | Args | Status |
|---|---|---|
| iphone_describe | (none) | ships, OCR over CGWindow capture |
| iphone_tap | nx, ny, text, match | ships |
| iphone_long_press | nx, ny, duration_ms, with_jitter | v0.1.7 |
| iphone_double_tap | nx, ny | v0.1.7 |
| iphone_swipe | nx1, ny1, nx2, ny2 | ships |
| iphone_type | text, delay_ms | ships |
| iphone_action | action (mapped) | ships |
| iphone_zoom_in | (none) | v0.1.7 |
| iphone_zoom_out | (none) | v0.1.7 |
| iphone_drag_and_drop | nx1, ny1, nx2, ny2, hold_ms | current stack |
| iphone_focus | raise | current stack |
| iphone_wait_until | text, viewport_stable_for_ms, timeout_ms | current stack |

## Completed follow-ups

- F1 `iphone_drag_and_drop` is implemented as long-press, hold, drag, and
  release through `input.LongPressDragScreenPoint`.
- F2 `iphone_focus` is implemented with frontmost-before/after reporting and
  optional raise behavior.
- F5 `iphone_wait_until` is implemented for OCR text waits and perceptual
  viewport-stability waits.

## Remaining follow-up work (queued, original IDs preserved)

Each item includes scope, blast radius, and verification plan.

### F3. iphone_describe edge-coord support / iphone_tap pointerization

**Why (task #27):** Currently `nx=0` and `ny=0` are treated as "not
provided" because iphone_tap uses `args.NX != 0 || args.NY != 0` to
distinguish from the text-search path. iOS edge gestures (back-from-edge
swipe, status-bar swipe) require touches at exactly `nx=0` or `ny=0`.

**Scope:**
- Change tapInput to use `*float64` pointers, so JSON null vs. 0 is
  distinguishable; OR add an explicit `mode` string field
  (`"text"|"coords"`); OR keep zero-default but require explicit
  text="" + nx/ny pair to disambiguate.
- Whichever shape: `iphone_tap{nx:0, ny:0.5}` must be a legitimate
  left-edge tap, not an "unspecified" error.
- Same change applies to iphone_long_press, iphone_double_tap.

**Verification:**
- iphone_tap{nx:0, ny:0.5} on a navigation-controller screen: starts
  back-from-edge swipe.
- iphone_tap{nx:1, ny:0.5} on a screen with a right-edge swipe: same.

### F4. iphone_tap text-mode spatial dedup (task #23)

**Why:** OCR returns multiple hits for the same visible word at slightly
different bounding boxes (e.g. "Freeform" matches both the Top Hit row
and the "Search the web for Freeform" row). Current `match=N` selection
trips on this - observed during the 2026-04-28 demo.

**Scope:**
- In iphoneTap's text-mode branch, after collecting all hits, dedup by
  spatial proximity: if two hits are within ~10px of each other in both
  x and y, treat them as one and keep the highest-confidence.
- Then apply `match=N` to the deduped list.

**Verification:**
- Spotlight 'freeform' search: iphone_tap{text:"Freeform", match:1}
  hits the Top Hit app row, not the web-search row.

### F6. iphone_action keymap audit (task #22)

**Why:** Some actions in the map (`siri`, `notifications`, `control_center`,
`back`) were guessed at, not verified. iPhone Mirroring's menu bar
(View > Mirror Menu, Window > etc.) lists the official keyboard
shortcuts; we should source the map from those.

**Scope:**
- Inspect iPhone Mirroring's menu bar via axmcp's ax_menu tool; collect
  every menu item with a key equivalent.
- Cross-check against `actionMap` in main.go. Update keys that don't
  match. Document which actions are confirmed-from-menubar vs.
  guessed.

**Verification:**
- iphone_action{action:"siri"} actually opens Siri (long-press home in
  iOS, but in iPhone Mirroring it should be the menubar's binding, e.g.
  Cmd+Option+Space - verify).
- iphone_action{action:"notifications"} pulls down notification center.
- iphone_action{action:"control_center"} opens Control Center.

### F7. Tier 2 magnify / pinch probe

**Why:** Tier 2 was deferred. Time-box this when there's a demand for
real pinch-zoom on apps that don't honor UIKeyCommand.

**Scope:**
- 2-hour time-box.
- Implement minimum kCGEventGesture magnify recipe (Hammerspoon
  reverse-engineered the field layout for `hs.eventtap.event.gestures`;
  port the relevant fields).
- Probe target: a third-party iOS app with pinch-to-zoom but no
  Cmd+Plus binding (a game, or a custom canvas app).
- If the magnify reaches iOS: ship `iphone_pinch{nx, ny, scale,
  duration_ms}`.
- If magnify is intercepted by the host (zooms the macOS view of the
  mirror, not iOS): document and stop. Don't ship.

### F8. Server struct refactor (task #28)

**Why:** Right now every iphone_* handler calls
`ocrwindow.FindWindow(screenContinuityApp)` independently. For a
multi-tool flow ("describe then tap then describe again") that's three
window lookups when one would do. Also makes it hard to share state
(focus tracking, OCR cache).

**Scope:**
- Pull the bare-function handlers into methods on a `server` struct.
- Cache the iPhone Mirroring window for ~1s per call so back-to-back
  tools don't re-FindWindow.
- Single owner for `focusIPhoneMirroring` last-activated-at timestamp,
  so we can skip the activate if it was just done.

**Risk:** caching window state can mask bugs (e.g. user repositioned
the window between calls). Cache expiration must be conservative.

### F9. Tool description honesty pass

**Why:** Existing descriptions are accurate but terse. After v0.1.7
ships, do a single review of all iphone_* descriptions and ensure each
one says:
- What it does.
- What it CANNOT do (so the LLM doesn't try to hammer at the wrong tool).
- Whether it returns immediately on dispatch or waits for confirmation
  (task #20 - currently iphone_tap returns on dispatch, which is
  visible to the LLM only if we say so).

**Scope:** docstring edits only, no code changes.

## Things deliberately NOT in scope

- Bundle TCC stabilization across rebuilds. Requires a Developer ID
  Application certificate; deferred. Tracked as task #14.
- Migrating ocrwindow.Capture from CGWindowListCreateImage to
  ScreenCaptureKit. Tracked as task #15.
- Video recording of the iPhone Mirroring window. Tracked as task #2.
- Pinch / two-finger swipe implementation (Tier 2 deferred until
  demanded).
- Continuity IPC reverse-engineering (Tier 3 - never).

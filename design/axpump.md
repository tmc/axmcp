# axpump: keep Chromium AX trees populated via observer presence

`internal/axpump` is the planned v0.3.x package that registers a no-op
`AXObserver` per process pid axmcp inspects, so Chromium-family targets
(VS Code, Slack, Chrome, Discord, every Electron shell) keep their
renderer-side AX pipeline engaged. Without an observer registered,
Blink short-circuits AX-tree generation when the window isn't focused
and `ax_tree` / `ax_snapshot` against the backgrounded target return a
chrome-only stub. `axpump` is the smallest fix that closes that gap; it
is *not* a switch from synchronous walk to event-driven observation.

The package does not exist yet — this doc precedes the implementation
and pins design decisions made during the v0.3 research dispatch.

## Why observer presence, not events

cua-driver's `AppState.swift:registerAccessibilityObserver(pid:)` calls
`AXObserverCreateWithInfoCallback` with a callback that *discards every
argument*, attaches the run-loop source to the main run loop, and
subscribes to ~13 notifications. None of the notifications are read.
The cua source comment ("Chromium detects the presence of at least one
`AXObserver` with at least one `AXObserverAddNotification` subscription
and engages its full accessibility pipeline") matches the observable
behavior: registering *any* observer flips Blink into full-tree mode;
the events themselves are uninteresting.

axmcp's tools today don't react to AX events — `ax_tree` and
`ax_snapshot` walk the tree synchronously. There is no plan to change
that in v0.3.x; the user model stays "snapshot on demand," and the
internal architecture stays a one-shot MCP subprocess. The observer
exists purely as a side-effect generator: standing it up is what nudges
Chromium; whether anyone consumes the events is irrelevant.

This framing matters because it scopes the design tightly. Three things
are explicitly *out of scope*:

- Reactive event-driven `ax_*` tools.
- Long-lived daemon mode for AX observation across MCP sessions.
- Exposing an event stream to MCP clients.

All three are reasonable v0.4+ work; none is required to close the
"backgrounded Chromium tree is stripped" gap.

## Symbol-availability matrix

`/tmp/skylight-c2-spike-dlsym.go` re-derived the runtime-resolution
state on the host that ships v0.3.x research. Probed via
`purego.Dlopen` against the `HIServices.framework` cache entry and
`purego.Dlsym` for each candidate; cross-checked with `dyld_info
-exports`.

| Symbol | macOS 26.4.1 | Use |
|--------|--------------|-----|
| `AXObserverCreateWithInfoCallback` | present | create the no-op observer |
| `AXObserverGetRunLoopSource` | present | attach to the pumped run loop |
| `AXObserverAddNotification` | present | subscribe (the load-bearing call) |
| `AXObserverRemoveNotification` | present | per-pid teardown (deferred work) |
| `_AXObserverAddNotificationAndCheckRemote` | **absent** | optional optimisation |

The absent symbol is the one the v0.3 roadmap originally named as the
load-bearing piece. cua-driver's source dlsym's it via `RTLD_DEFAULT`
and falls back to public `AXObserverAddNotification` when the lookup
returns nil; on this host cua itself runs the public-fallback path. The
private symbol's presence on earlier macOS versions is unverified from
this host — cua-driver was first publicly released 2026-04-23 with the
nil-fallback already in place, so even the upstream authors' code
treats it as best-effort.

The honest cutoff statement: **absent on macOS 26.4.x; presence on
earlier versions unverified.** Do not condition any design on the
symbol resolving.

## Why the dlsym is dropped, not retained

Three options were on the table for the dlsym:

1. Keep it — try the private symbol, fall back to public.
2. Drop it — public-only, log a comment-link to this doc.
3. Keep it but gate on `sw_vers` parse to skip on macOS ≥ 26.

(2) wins. On macOS 26.4.x the lookup costs two `dlsym` calls per
observer to confirm what the host already told us at design time. (3)
adds runtime version-detection complexity for a path the symbol-table
probe says is dead. (1) carries cua's cargo-cult: the comment in cua
admits "the **likely** reason" Chromium's pipeline stays alive is the
private path, but the empirical evidence — Slack and VS Code populate
fine on cua's own public-fallback path on macOS 26 — argues against the
load-bearing claim.

The design takes (2) and ships public-only. If a v0.4+ user reports
stripped trees on a macOS ≤ 25 host where the symbol exists, the dlsym
gets re-added behind a version gate. Until then, dead code does not
ship.

## API sketch

```go
package axpump

// ErrAXPumpUnavailable is wrapped into errors returned by Pump and
// Ensure when the HIServices observer surface cannot be reached, when
// the run-loop pump cannot be started, or when AXObserverCreate fails
// for the target pid. Callers branch via errors.Is, never ==, because
// the sentinel is always wrapped with the underlying cause via
// fmt.Errorf("%w: ...", ErrAXPumpUnavailable). Same contract spacedetect
// established for ErrSkyLightUnavailable in v0.2.x.
var ErrAXPumpUnavailable = errors.New("axpump: AX observer unavailable")

// Pump arranges for observer run-loop sources to be serviced for the
// life of the process. Spawns one runtime.LockOSThread goroutine that
// runs CFRunLoopRun on a private CFRunLoop; subsequent Ensure calls
// attach each pid's source to that run loop. Idempotent: repeat calls
// are no-ops. Must be called once before any Ensure call. Returns an
// error wrapping ErrAXPumpUnavailable when the run-loop scaffolding
// cannot be set up.
func Pump() error

// Ensure registers a no-op AXObserver for pid (if not already retained
// from a prior call), attaches its run-loop source to Pump's run loop,
// and subscribes to a broad notification set so Chromium-family
// targets keep their renderer-side AX pipeline engaged. Idempotent.
// Returns nil on success and on partial-success — Chromium only
// requires presence, not coverage, so a subset of failed subscribes is
// acceptable as long as at least one is live. Returns an error
// wrapping ErrAXPumpUnavailable when HIServices is unreachable or
// AXObserverCreate fails for the pid.
func Ensure(pid int32) error

// Active reports whether Ensure has registered an observer for pid in
// this process. ax_tree / ax_snapshot stamp this as "ax_pump_active"
// metadata so callers debugging "tree looks stripped" can disambiguate
// "Chromium isn't cooperating" from "we never tried."
func Active(pid int32) bool
```

Splitting `Pump` from `Ensure` mirrors `internal/spacedetect`'s
`sync.Once`-gated `load()` versus per-call `IsOffSpace`. `Pump` owns
process-wide scaffolding; `Ensure` is the per-pid hot path.

## Graceful-degrade contract

Same shape as `spacedetect.ErrSkyLightUnavailable` (see
`design/spacedetect.md`):

- `ErrAXPumpUnavailable` is a sentinel `errors.New(...)`.
- The darwin implementation returns it wrapped via `fmt.Errorf("%w:
  ...", ErrAXPumpUnavailable, cause)`.
- Callers MUST branch with `errors.Is`, never `==`.
- Non-darwin builds return `ErrAXPumpUnavailable` unconditionally from
  a `//go:build !darwin` stub so the package stays importable from any
  host.

The non-error JSON contract is also parallel: `Active(pid)` returns
`false` whenever `Ensure` has not succeeded — no separate
`available bool`, no tri-state. The metadata field on `ax_tree` /
`ax_snapshot` payloads stays `omitempty`-quiet on the common path.

Caller integration in `cmd/axmcp/tools.go` would mirror the existing
`spacedetect` site:

```go
if err := axpump.Ensure(app.PID()); err != nil {
    if !errors.Is(err, axpump.ErrAXPumpUnavailable) {
        slog.Debug("axpump: ensure failed", "pid", app.PID(), "err", err)
    }
}
// snapshot proceeds regardless; ax_pump_active stamps via Active(pid)
```

`ErrAXPumpUnavailable` is silenced (the framework or the pump is gone
on this host; nothing else to say). Other errors — e.g., a sandboxed
pid where `AXObserverCreate` fails — log at debug level for inspection
without drowning structured output. The snapshot itself never errors
because of axpump.

## Non-goals

`axpump` is opt-in metadata-stamp scaffolding for the tools that
already exist; it is not a structural change to those tools.

- **axmcp does not switch synchronous walk to event-driven in v0.3.**
  `ax_tree` and `ax_snapshot` continue to call `AXUIElementCopy*`
  synchronously. The observer registered by `axpump` exists for its
  side effect on Chromium, never for its events.
- **No daemon-mode coupling.** axpump's run-loop pump goroutine spins
  inside the existing one-shot MCP subprocess. When the subprocess
  exits, the observer goes with it. Long-lived AX observation across
  MCP sessions is `cmd/axmcpd` work, deferred per `ROADMAP.md`'s
  mid-term section.
- **No exposed observer events.** The callback discards every
  argument. No public Go channel, no MCP tool that streams AX
  notifications. Adding either would commit axmcp to an event model
  the rest of the surface doesn't share.
- **No NSApplication bootstrap.** cua-driver attaches its observer
  source to the main run loop, which is alive because cua runs
  `NSApplication.shared.run()` for its agent-cursor overlay. axmcp has
  no NSApplication and is not gaining one for axpump; the dedicated
  `runtime.LockOSThread` + `CFRunLoopRun` goroutine substitutes.

## Corroboration from cua-driver

cua-driver is the upstream reference for the observer-presence pattern.
The relevant invariants we mirror:

- The callback is a no-op
  (`cuaDriverObserverNoopCallback: AXObserverCallbackWithInfo = { _, _,
  _, _, _ in }`).
- The observer is created via `AXObserverCreateWithInfoCallback`, not
  `AXObserverCreate`, because the info-callback variant gives Chromium
  a richer "AX client is here" signal even when the info dict is
  ignored.
- Subscriptions cover ~13 notifications (focused-element, focused-
  window, application-activated/deactivated/hidden/shown, window-
  created/moved/resized, value/title-changed, selected-children-
  changed, layout-changed). The breadth matters because Chrome checks
  not just "is there an observer" but "does it subscribe to the
  notifications a screen reader would care about."
- Per-pid retention is enforced via a `[Int32: AXObserverRef]` map
  that is never cleaned up — process exit handles teardown.

The invariant we *don't* mirror is run-loop attachment: cua attaches
to `CFRunLoopGetMain()` (alive via `NSApplication.shared.run`); axpump
attaches to its private `CFRunLoop` running on a `LockOSThread`
goroutine. The substitution is documented in `Pump`'s contract.

The empirical falsifier — "is observer presence enough on macOS 26
without the private SPI" — is currently inferred, not directly
measured. cua's own code paths execute the public fallback on macOS 26
and Chromium populates trees there, so the inference is well-grounded;
a manual smoke against backgrounded VS Code or Slack should confirm
before the package ships. See ROADMAP.md's "Process" section: every
v0.3.x near-term item gets a manual smoke recipe in the relevant
`design/` doc before merge.

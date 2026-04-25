# spacedetect: off-Space window detection via SkyLight

`internal/spacedetect` reports whether a `CGWindowID` lives on a macOS
Space other than the user's currently active Space. It is the smallest
adoption of the SkyLight private-symbol pattern; the same dlsym
workflow generalises to the larger `SLEventPostToPid` and
`AXObserverAddNotificationAndCheckRemote` adoptions on the v0.3.x
roadmap.

## Why purego dlsym, not cgo

The three SkyLight symbols this package needs — `SLSMainConnectionID`,
`SLSGetActiveSpace`, `SLSCopySpacesForWindows` — are private SPI on
`/System/Library/PrivateFrameworks/SkyLight.framework`. They are not
declared in any public header.

cgo would require either reverse-engineered C declarations (brittle:
signature drift across macOS releases would fail at compile time
rather than at runtime) or `dlopen` / `dlsym` from C, which adds a
cgo dependency without saving any complexity. purego resolves the
symbols at runtime against the framework's `Dlopen` handle and binds
them to typed Go function variables, matching the pattern the
upstream `github.com/tmc/apple` module already uses for everything
under `apple/private/skylight/`.

The runtime-resolution model has two operational consequences:

1. **A missing symbol is a startup-time failure, not a build-time one.**
   If Apple removes `SLSCopySpacesForWindows` in a future macOS
   release, the package compiles fine and `IsOffSpace` returns an
   error wrapping `ErrSkyLightUnavailable` instead of crashing. This
   is the right failure mode for a detector — callers can fall back
   to the existing "no windows found" string and lose nothing.
2. **Non-darwin builds stay clean.** `spacedetect.go` is
   `//go:build darwin`; `spacedetect_other.go` provides
   `IsOffSpace(uint32) (bool, error)` returning
   `ErrSkyLightUnavailable` unconditionally. The package is
   importable from non-darwin hosts (so `go install ./...` works
   anywhere) without ever touching purego.

## ErrSkyLightUnavailable: the wrap + errors.Is contract

`ErrSkyLightUnavailable` is defined twice — once in each build-tagged
file — as a sentinel `errors.New("spacedetect: SkyLight unavailable")`.
The darwin implementation returns it wrapped with the underlying
cause via `fmt.Errorf("%w: ...", ErrSkyLightUnavailable, cause)`, so
callers MUST branch with `errors.Is`, never `==`.

Three failure modes in `load()` produce wrapped errors:

- `purego.Dlopen` fails — framework not present (would mean a
  fundamentally broken macOS install).
- Any of the three `Dlsym` lookups returns a zero pointer — that
  specific symbol was renamed or removed between macOS versions.
- `purego.RegisterFunc` panics during signature binding — recovered
  inside `registerLib` and re-wrapped as `ErrSkyLightUnavailable`.

Three additional failure modes in `IsOffSpace` itself return errors
that do *not* wrap the sentinel:

- `SLSGetActiveSpace` returns 0.
- `CFNumberCreate` or `CFArrayCreate` returns nil.
- The lookup succeeds but the window has no Space membership (transient
  / system / off-screen-only windows can land here).

These are runtime lookup failures, not framework-availability failures,
so callers shouldn't treat them with the same fallback. The existing
caller in `cmd/axmcp/tools.go` discriminates accordingly:

```go
if off, err := spacedetect.IsOffSpace(cw.WindowID); err != nil {
    if !errors.Is(err, spacedetect.ErrSkyLightUnavailable) {
        slog.Debug("spacedetect: lookup failed", "windowID", cw.WindowID, "err", err)
    }
} else if off {
    wi.OffSpace = true
}
```

`ErrSkyLightUnavailable` is silenced (the framework is absent on this
host; nothing else to say). Other errors are logged at debug level so
they're inspectable but don't drown structured output. The `off` value
is only consulted on the no-error path.

## Quiet-default JSON convention with ax_list_windows

`winInfo` in `cmd/axmcp/tools.go` declares `OffSpace bool
` json:"off_space,omitempty"``. The `omitempty` is load-bearing: a
window on the active Space (the common case) emits no `off_space` key
at all, so `ax_list_windows` JSON payloads stay minimal and look the
same as they did before `spacedetect` existed.

`OffSpace` is therefore additive metadata in two senses:

1. **Field-level additive.** Old clients that never read `off_space`
   see no difference in the wire shape.
2. **Failure-additive.** When `IsOffSpace` returns an error, the
   `OffSpace` field is left at its zero value (`false`) and the JSON
   omits it — same as a non-darwin host or a future macOS that
   strips the symbols. The integration is robust to detector
   regressions: the only thing a detector failure costs is the
   informational `"off_space": true` flag, never a crash and never a
   wrong-direction signal.

The detection only runs on the CGWindowList fallback path inside
`ax_list_windows` — the AX path is tried first, and AX only returns
on-Space windows for the resolved app, so off-Space residency is by
construction not relevant there. This avoids a per-window SkyLight
round trip on the common (AX-found) path.

## Tests

`spacedetect_test.go` covers two contracts:

- `TestIsOffSpaceUnknownWindow` calls `IsOffSpace(0)` (window ID 0 is
  not real). Both outcomes — `ErrSkyLightUnavailable` and a non-wrapped
  lookup error — are valid; the test asserts only that `off` stays
  false on the error path and that the function does not crash. This
  is the "graceful failure" contract.
- `TestErrSkyLightUnavailableUnwraps` joins the sentinel with another
  error via `errors.Join` and asserts `errors.Is` finds it. Pins the
  contract that future refactors of the wrap path (replacing
  `fmt.Errorf` with `errors.Join` or any other shape) cannot silently
  break the `errors.Is` discipline that callers rely on.

Tests are honest about platform: the SkyLight call may succeed on a
developer's Mac and fail on a CI runner without a logged-in
WindowServer session. The test design treats both as valid outcomes
of the same contract.

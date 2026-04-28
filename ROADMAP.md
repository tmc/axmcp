# axmcp roadmap

This file tracks where the project is going. Items are dated by target
version, not by calendar date. Anything not listed here is not committed
work — see "Out of scope" for the boundaries.

## Current state

v0.2.x ships three MCP servers — `cmd/axmcp`, `cmd/xcmcp`,
`cmd/computer-use-mcp` — built on shared internal packages. The
Accessibility surface walks live AX trees, posts pointer and keyboard
events, captures and OCRs screen regions, and drives windows with
explicit raise / move / drag actions. The Codex Computer Use server
wraps the same primitives in the 9-tool contract with per-session app
state. The Xcode surface drives `xcodebuild`, simulators, devices, App
Store Connect, and the File > New > Target wizard. As of v0.2.x +
two follow-up PRs, off-Space windows are detected via a private
SkyLight binding (`internal/spacedetect`) and surfaced as `off_space:
true` on `ax_list_windows` results. The hygiene-verify GitHub Actions
workflow gates every PR on six checks (no AI provenance trailers,
subject discipline, `go mod tidy` clean, build/vet/test, no scratch
leakage). Ghost cursor rendering for click and drag actions lives in
`internal/ghostcursor`.

## Near-term (v0.3.x)

- `internal/axpump` to keep backgrounded Electron AX trees populated
  by registering a no-op `AXObserver` per inspected pid. Medium scope,
  not the originally-projected one-line dlsym swap: a
  `runtime.LockOSThread` run-loop pump, per-pid `Ensure` that creates
  the observer and subscribes to a broad notification set, and an
  `Active(pid)` query stamped as `ax_pump_active` metadata on
  `ax_tree` / `ax_snapshot` payloads. The originally-projected
  `_AXObserverAddNotificationAndCheckRemote` private SPI is absent on
  macOS 26.4.x (verified by `purego.Dlsym` and `dyld_info -exports`)
  and was always best-effort upstream — the load-bearing piece is
  observer presence itself, not the private call. See
  `design/axpump.md` for the full design and graceful-degrade contract
  parallel to `spacedetect`'s `ErrSkyLightUnavailable`.
- Upstream `AXObserver*` wrappers in `tmc/apple/x/axuiautomation`
  (`AXObserverCreateWithInfoCallback`, `AXObserverGetRunLoopSource`,
  `AXObserverAddNotification`, `AXObserverRemoveNotification`). Today
  the package exports only `AXObserverCreate`. `internal/axpump` needs
  the other three; binding them upstream first is the path that keeps
  axpump free of the ad-hoc `purego.Dlopen` pattern that
  `cmd/axmcp/tools.go` just retired. See `design/axpump.md` §"Upstream
  prerequisite".
- `SLEventPostToPid` + auth-message envelope + primer click for
  Chromium-family pointer delivery. Concentrates in
  `internal/computeruse/input/input.go`. Highest user-visible payoff
  of the cua-driver adoption set; also the most invasive — wants the
  most integration-test investment.
- `internal/ghostcursor` split: separate the input-event side from the
  overlay rendering side so the cursor animation can be reused outside
  the click/drag paths without dragging the input dependencies.
- `doc.go` refresh: the package doc still does not mention
  `internal/spacedetect`, the `off_space` field on `ax_list_windows`,
  or the hygiene-verify workflow. README already covers all three in
  its "What's new in v0.2.x" section; doc.go needs to catch up so the
  godoc surface matches.
- Out-of-tree contradiction sweep: anything claimed in README but not
  reachable in code (or vice versa) gets either the doc removed or the
  feature added; nothing left half-described.

## Mid-term (v0.4+) — speculative

- `cmd/axmcpd` long-running daemon, if a use case appears that needs
  the lifetime of an AppKit listener (focus-steal prevention,
  long-lived AX observers across multiple MCP client sessions). Today
  the short-lived subprocess shape is the simpler default.
- Migration off `CGWindowListCreateImage` (deprecated since macOS 14)
  to `SCContentFilter(desktopIndependentWindow:)`. Unlocks
  minimized-window capture as a side effect. Separate work item from
  the cua-driver adoption set.
- Structured error type for window-resolution failures, replacing the
  current "no windows found …" string with a typed error carrying
  pid, current-Space ID, and per-window Space IDs. Lands once
  `spacedetect` callers have settled on the shape.
- Optional integration-test gating on the hygiene-verify workflow. VS
  Code is the most stable Electron fixture; System Settings is the
  most stable off-Space fixture. Today these are local-only smoke
  recipes documented in commit messages.

## Out of scope

These are explicitly not part of the roadmap:

- **Cross-Space window migration.** `CGSMoveWindowsToManagedSpace` and
  `SLSSpaceAddWindowsAndRemoveFromSpaces` require a private
  WindowServer entitlement Apple does not grant outside its own
  processes. `spacedetect` reports off-Space residency; it never tries
  to move windows between Spaces.
- **Cross-platform parity beyond darwin.** The Accessibility surface,
  the SkyLight bindings, the AX private SPI, the Xcode automation,
  and the simulator/device tooling are all macOS-only. Non-darwin
  builds compile (`spacedetect_other.go` returns
  `ErrSkyLightUnavailable`) so the module stays `go install`-able from
  any host, but no functional parity is targeted.
- **Kernel extensions or anything requiring SIP-disabled hosts.** The
  bar is "runs on a stock developer-mode macOS with Accessibility
  permission granted." Nothing on the roadmap moves that bar.
- **Supply-chain-hostile dependencies.** No third-party GitHub Actions
  in the hygiene-verify workflow (the trailer / subject scans are
  five-line bash steps inline). No replacing stdlib `regexp` with
  third-party engines. No tools that require background services we
  do not control.
- **AI provenance trailers in commits.** Per CLAUDE.md, no
  `Co-Authored-By: Claude` (or any other model) trailer in any commit
  authored against this repo. The hygiene-verify workflow enforces
  this on every PR.
- **`workflow_dispatch` on the hygiene-verify gate.** It is a gate,
  not a button. Manual re-runs happen via the Actions UI; nobody hand-
  triggers a hygiene check.

## Process

Decisions are made in PR review against `main`. Every change passes
the hygiene-verify GitHub Actions workflow (six gates listed under
"Current state"). Before merging anything that touches darwin-only
code paths, run a manual smoke test on an actual Mac — the workflow
type-checks and unit-tests, but the user-visible behavior of an
Accessibility action, a SkyLight call, or a screenshot capture is
only verifiable against a real WindowServer. The audit / verify
discipline applied to the v0.2.0 ship — re-derive every claim from
diffs, run independent verification from a fresh clone, fix bugs
caught inside the verifier rather than in the work being verified —
is the same standard for any subsequent release tag. Roadmap items
move from "near-term" to "shipped" only when (a) the PR merges with
a green hygiene-verify run and (b) the manual smoke recipe is
recorded in either the commit message or the relevant `design/` doc.

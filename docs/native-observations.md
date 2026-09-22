# Native observations and actions

The `computer-use-mcp` server provides `native_discover`, `native_select`,
`native_release`, `native_observe` and `native_act` for
interactions tied to one process instance and window. Start each turn with an
observation. Existing `get_app_state` and legacy action tools remain separate;
their state tokens cannot be used with the native pair.

Discover the windows of one running app with `native_discover`:

```json
{"app":"12345"}
```

Discovery requires existing system permissions and app approval; it never asks
for new approval, launches or activates an app, or captures a screenshot. The
result includes process start time, window metadata and an opaque `selection_id`
for each available window. At most 128 candidates are returned; `truncated: true`
means the window list exceeded that limit.

Pass a selection to `native_observe` on the same MCP connection:

```json
{"selection_id":"returned selection_id"}
```

Do not combine `selection_id` with `app` or `window_id`. Selection matches the
retained accessibility window reference against a fresh window list, with no
name or numeric-ID fallback. It checks the process start time before and after
capture. Same-title replacement rejection has been exercised in an AppKit
fixture; actual numeric window-ID reuse and other accessibility servers remain
unqualified. Checks and capture are not an atomic operation with the target app.

Candidates expire 60 seconds after discovery, without renewal on selection. A
successful new discovery replaces that client's candidates. Expiry, disconnect
and server shutdown release discovery handles after any in-flight operation
using them finishes. A new discovery or failed selection leaves the current
observation intact. A successful observation replaces it. The native backend
still has one current action observation; it is not a multi-surface state store.
A selection token alone can never authorize `native_act`.

For a longer-lived selection, call `native_select` with an unexpired
`selection_id`. It returns a `target_handle` bound to the retained window and
process instance, without capturing or activating it. Observe that handle with:

```json
{"target_handle":"returned target_handle"}
```

Do not combine this mode with `selection_id`, `app` or `window_id`. The handle
survives candidate expiry, rediscovery and replacement of the current observation.
Post-action capture uses the same retained window reference. Permission and
approval checks still apply on each operation.

Call `native_release` with the `target_handle` when finished. It returns
`released: true` once and invalidates the current observation if it came from
that handle. Unknown or foreign handles return `released: false`. Release never
closes the application or window. Handles belong to one MCP connection; disconnect
and server shutdown release them after in-flight operations finish. They do not
provide cross-client handoff. Each client can retain at most 64 targets. Targets
share their discovery's window group, so its native references remain retained
until the candidates and all selected targets using that group are released.

Observe a running app by its PID, unique full name or bundle identifier:

```json
{"app":"12345","window_id":678}
```

Omitting `window_id` selects the app's focused window. An explicit ID must exist
in that app. Direct observation never launches an app; use a discovery selection
when identity must remain bound across listing and capture.
The result includes the accessibility tree, screenshot, `state_id`, `target_id`
and process start time. Permissions and app approval must be granted before
observation returns an action token.

Act using both returned tokens and an index from that observation:

```json
{
  "state_id":"returned state_id",
  "target_id":"returned target_id",
  "action":"click",
  "element_index":3,
  "expect":{"identifier":"counter","attribute":"value","text":"1"}
}
```

A matching state is consumed before action validation. Failed validation therefore
requires another observation. Unknown state IDs, invalid timeouts and cancellation
while queued do not consume a different valid state. A later observation replaces
the earlier token. Do not retry an action merely because capture or verification
failed.

| Action | Required arguments | Behavior |
| --- | --- | --- |
| `click` | `element_index` or `point` plus `image_id` | Direct accessibility press or window-routed pointer input |
| `drag` | `from_point`, `to_point`, `image_id` | Window-routed pointer drag |
| `set_value` | `element_index`, `value` | Set AXValue; empty values are allowed |
| `secondary_action` | `element_index`, `secondary_action` | Perform an exact advertised accessibility action |
| `type_text` | `element_index`, nonempty `text` | Explicitly focus and verify the element, then post text to its process |
| `press_key` | `key` | Post a parsed key combination to the process |
| `scroll` | `element_index`, `direction`, positive `pages` | Post process-directed scrolling at the element |

Direct accessibility operations can address an explicitly observed background
window. Keyboard and scroll operations require the target window to remain the
app's focused window. The action checks process birth identity, window identity
and bounds, and observed element properties. It does not search by name for a
replacement element.

Read the three result fields independently:

- `execution`: `not_dispatched`, `dispatched_unknown`, or `completed`. Completed
  means the requested native calls returned successfully; it is not an app
  acknowledgement.
- `observation`: `captured` or `unavailable`. A failed post-action capture preserves
  the execution result. A successful capture supplies fresh tokens.
- `postcondition`: `not_requested`, `unknown`, `met`, or `unmet`. An optional
  `expect` matches exactly one fresh accessibility identifier and compares its
  raw `value` or `title`, including whitespace. It never reuses the old index.

`timeout_ms` defaults to 30000 and accepts values from 0 through 60000. Cancellation
stops future dispatch and bounds screenshot subprocesses and individual AX
messaging waits. It cannot undo native calls already in flight. Keyboard cleanup pairs
only key-down events posted by this action to the original process instance.

URL policy is checked from a fresh pre-action snapshot; it is not continuously
refreshed between individual text or pointer events. This limitation matters when
selecting a workflow.

Screenshots use the exact window ID, with shadows omitted. The optional
`screenshot_metadata` binds the returned PNG to its SHA-256 `image_id`, raw pixel
dimensions, unrounded global logical frame, pixel-to-point scales, and active
display topology. The image is not a screen-region capture, so another window
covering the target does not become the captured target.

Capture requires consecutive matching geometry and image dimensions; changing
pixel content is allowed. A final geometry check rejects observable movement or
display changes during the accessibility tree walk. The image and tree are not an
atomic snapshot: content can change, and movement away and back between checks
can escape detection.

For screenshot input, provide `image_id` from `screenshot_metadata` and points in
raw PNG pixels. Both coordinates are required and must lie inside the image:
`0 <= x < width`, `0 <= y < height`. Coordinates are not rounded or clamped.
`click` accepts either `point` or `element_index`, never both. `mouse_button` is
`left` (default), `right`, or `middle`; `click_count` is 1 (default), 2, or 3.
Nondefault button/count options require a point. `drag` uses `from_point` and
`to_point`, with `duration_ms` from 100 through 5000 (default 300).

Pointer routing checks the captured geometry and display topology before each
new down or movement event, and posts each event once to the original process
and window. It does not explicitly activate the app. Cleanup sends only the
matching up for a down this action posted; if the original instance or geometry
cannot be verified, cleanup reports an error instead of releasing into another
target. If an app stops responding after a down, its AX geometry check can fail
and leave the matching up undispatched. The action reports the cleanup error;
it does not retain pending input for later recovery. A bounded cleanup wait
does not establish that the app received a release.

Mouse events carry uptime timestamps and matching event numbers for each down/up
pair. Drag scheduling includes validation work rather than adding a fixed delay
after every check; slow system calls can still extend the requested duration.

Native routing uses private macOS window-location support and returns an
error when that support is unavailable. AppKit behavior may differ across controls;
a native call returning is not evidence that a requested UI effect occurred. A
background view can reject its first mouse event. Check the application effect;
do not replay a click automatically or assume that a local event-monitor record
means the view handled it.

The native pair emits the PNG as MCP image content alongside structured state and
text, without repeating its base64 in the JSON. An image-encoding failure after
an action preserves the execution result and reports the observation unavailable.
Transport/schema errors before the handler do not consume a token. Image identity
and point bounds are validated in the handler after state consumption.

To approve an app before discovering its windows, explicitly call
`native_request_approval` with the app name, bundle ID or PID. It resolves one
running app and requests persistent approval through MCP form elicitation.
It does not launch, activate, capture or act on the app. Acceptance stores
approval for future sessions; decline and cancel grant no approval. The result
preserves approval and permission status, including persistence errors.
Accessibility and Screen Recording permissions must be granted separately.
`native_discover` continues to list only approved windows without prompting.
There is currently no tool for revoking stored app approvals.

### Withdrawing approval

Call `native_revoke_approval` with an exact `bundle_id` to withdraw approval,
including for a stopped app. Successful revocation removes persistent grants
and invalidates session grants in cooperating backends sharing the same store.
The current matching observation is invalidated; other backends check shared
approval before dispatch. A later explicit acceptance can grant access again.

This operation does not prompt, launch, capture, close windows, change macOS
permissions, or undo an already-dispatched event. `revoked: false` with
`error_text`, or an MCP error, means withdrawal was not confirmed. A write can
be visible despite a later durability error: inspect current state and never
automatically retry. Optional `timeout_ms` uses the normal 30-second default and
60-second maximum; it bounds lock waiting, not OS regular-file I/O.

### Startup while permissions are pending

The stdio server initializes independently of the permission onboarding window.
Clients can discover tools and read pending permission status before granting
access. Pending permissions still prevent window discovery and native actions.
Closing the transport cancels onboarding and lets queued UI cleanup finish
before the app exits. No direct-execution fallback is used to bypass bundle
launch or macOS permissions.

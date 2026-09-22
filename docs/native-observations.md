# Native observations and actions

The `computer-use-mcp` server provides `native_observe` and `native_act` for
interactions tied to one process instance and window. Start each turn with an
observation. Existing `get_app_state` and legacy action tools remain separate;
their state tokens cannot be used with the native pair.

Observe a running app by its PID, unique full name or bundle identifier:

```json
{"app":"12345","window_id":678}
```

Omitting `window_id` selects the app's focused window. An explicit ID must exist
in that app; observation never launches an app or selects a replacement window.
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
| `click` | `element_index` | Direct accessibility press; no coordinate fallback |
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
messaging waits. It cannot undo native calls already in flight or impose a hard
wall-clock limit on the synchronous screenshot helper. Keyboard cleanup pairs
only key-down events posted by this action to the original process instance.

URL policy is checked from a fresh pre-action snapshot; it is not continuously
refreshed between individual text events. Coordinate click and drag are not
provided by this pair. These limitations matter when selecting a workflow.

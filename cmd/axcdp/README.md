# axcdp

`axcdp` exposes macOS Accessibility through a Chrome DevTools Protocol
remote-debugging endpoint.

The server is intentionally an AX-backed CDP subset. It advertises only
methods backed by macOS Accessibility, screen capture, native overlay/input, or
harmless DevTools setup controls. Browser-only features such as arbitrary page
navigation, reload, network response bodies, browser JavaScript object
execution, storage, IndexedDB, and Chrome's internal page DOM are not
synthesized.

DevTools inspection uses a DOM-shaped tree derived from real AX elements. That
tree is simulated only as a protocol shape; `Page.captureScreenshot` and
`Page.startScreencast` use screen capture, and highlight commands draw a native
overlay on the real macOS view bounds.

Window screenshots and screencast frames are captured with
`CGWindowListCreateImage`, so the relevant macOS TCC grant is Screen Recording
for the `dev.tmc.axcdp` app identity.

Run the server:

```sh
go build -o ~/go/bin/axcdp ./cmd/axcdp
~/go/bin/axcdp
```

The default listen address is `:9221`.
Use `-v` or `-debug` to enable structured debug logs for permission checks,
AX tree refreshes, preview screenshots, and screencast frames.

Live AX-backed modes run under the macgo identity `dev.tmc.axcdp`, with
Accessibility and Screen Recording permissions. Verifier-only modes do not
start macgo; they only talk to an already-running CDP endpoint.
Use a built binary for live serving. `go run` uses transient Go build-cache
paths, which are not a stable TCC identity.

If macOS does not show TCC prompts because a previous grant is stale or denied,
open the relevant System Settings pane and inspect the existing `axcdp` row
rather than resetting automatically. Use reset only when you intentionally want
to clear the current manual state:

```sh
~/go/bin/axcdp -reset-tcc -listen :9221
```

To compose real browser-CDP targets with AX targets, pass a browser DevTools
endpoint:

```sh
~/go/bin/axcdp -browser-cdp http://127.0.0.1:9222
```

Browser targets from that endpoint are added to `/json/list` with proxied
WebSocket URLs under `/devtools/browser-proxy/`. Commands sent to those browser
targets are forwarded to the real browser endpoint; AX targets continue to use
the AX-backed handlers and do not advertise browser-only domains.
When `-browser-cdp` is configured, browser target creation and close are also
proxied through the real browser for both WebSocket `Target.createTarget` /
`Target.closeTarget` and HTTP `/json/new` / `/json/close/<id>`.

Useful endpoints:

```text
/json/version
/json/list
/json/protocol
/json/coverage
```

`/json/list` reports one root page target, one root Node-compatible target for
dedicated Node DevTools, and one page target for each visible macOS window. It
does not report per-app page or Node targets in the general inspect list because
AX does not provide a single per-app viewport or separate Node runtimes for
apps.

`/json/activate/<id>` activates an app target when AX can map the target to a
process. On AX-only servers, `/json/new` and `/json/close/<id>` return
`501 Not Implemented` because AX cannot create or close browser targets. With
`-browser-cdp`, those HTTP endpoints are proxied to the real browser backend.

`/json/coverage` is the source of truth for the advertised surface. Each entry
names the CDP method, whether it is advertised, and the backing mechanism. It
also lists major unsupported browser-CDP methods with the reason they are not
implemented. The tests fail if `/json/protocol` and `/json/coverage` drift.
See `AUDIT.md` for the prompt-to-artifact completion checklist and the explicit
unsupported boundary.

Verify the command package:

```sh
go test ./cmd/axcdp
```

Verify a running endpoint:

```sh
go run ./cmd/axcdp -verify-cdp http://127.0.0.1:9221
```

The live verifier checks discovery, protocol coverage, HTTP compatibility
endpoints, WebSocket command execution, Schema metadata, AX pass-through,
live screenshot capture, limited AX-target `Page.navigate`, live screencast
frames, AX hit testing, highlight commands, and unsupported-method behavior. It
also checks the root
Node-compatible target and verifies that apps are not duplicated as fake Node
targets. Every method listed in `/json/coverage` as unsupported is sent over
WebSocket and must return JSON-RPC method-not-found (`-32601`).
The browser verifier checks browser-backed target creation, direct target
WebSocket proxying, `Runtime.evaluate`, `Network.enable`, `Page.navigate`, and
browser-backed target close against a combined server started with
`-browser-cdp`.
The target verifier checks one concrete app target end to end: the target URL
must be an HTTP preview page with a PNG screenshot endpoint, the DOM tree must
include an `AXApplication` with an `AXWindow` that has children, and
`Page.startScreencast` must emit a real frame. It requires `-target` so it does
not silently pick the first page when multiple windows are visible.

```sh
go run ./cmd/axcdp -verify-target http://127.0.0.1:9221 -target app/11527
go run ./cmd/axcdp -verify-target http://127.0.0.1:9221 -target TextEdit
```

Verify the native bundled DevTools UI with axmcp:

```sh
cmd/axcdp/verify-native-devtools.sh
AXCDP_TARGET=TextEdit cmd/axcdp/verify-native-devtools.sh
```

The native DevTools verifier opens a per-window target in
`devtools://devtools/bundled/inspector.html`, checks the target DOM, layout
metrics, and screenshot through the sibling `../cdp` CLI, then uses `axmcp` to
find and screenshot the DevTools window. It writes evidence to
`/tmp/axcdp-native-devtools`.

Live checks with the sibling CDP CLI:

```sh
cd ../cdp
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port 9221 -list-tabs
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port 9221 -tab <target-id> -command 'DOM.getDocument {"depth":1}' -format json -timeout 5
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port 9221 -tab <target-id> -command 'Page.captureScreenshot {}' -format json -timeout 10
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port 9221 -tab <browser-target-id> -command 'Page.navigate {"url":"https://example.com"}' -format json -timeout 10
```

Unsupported AX-only methods should return JSON-RPC method-not-found (`-32601`)
instead of fake data.

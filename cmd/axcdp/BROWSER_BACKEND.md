# Browser backend proxy

`axcdp` can compose macOS Accessibility-backed CDP targets with browser-backed
targets from a real browser DevTools endpoint. Browser-only domains are proxied
to the browser; they are not invented from AX data.

The proxy is enabled with:

```sh
go build -o ~/go/bin/axcdp ./cmd/axcdp
~/go/bin/axcdp -browser-cdp http://127.0.0.1:9222
```

## Backed by the browser

The browser proxy connects to a real browser DevTools endpoint and forwards
browser target WebSocket traffic for domains such as:

- `CSS`
- `Network`
- `Storage`
- `IndexedDB`
- `CacheStorage`
- browser-backed `Page.navigate`, `Page.reload`, and target creation
- browser-backed `Runtime.callFunctionOn`, promises, scripts, and object groups

The AX server can still own native macOS targets, overlays, screenshots, and AX
tree inspection. The browser backend should own browser internals.

## Integration shape

The combined server routes by target kind:

- AX targets continue to use `cmd/axcdp` handlers and coverage.
- Browser targets are discovered from the real browser `/json/list` endpoint,
  then WebSocket messages for those targets are proxied to the browser.
- Browser-backed `Target.createTarget` and `Target.closeTarget` proxy to the
  browser endpoint and return combined-server target IDs.
- `/json/coverage` documents the AX-backed command surface. Browser-backed
  methods must be proven with the browser verifier and sibling CDP CLI checks
  against a combined server.

## Verification gates

Run these gates before claiming browser-CDP coverage:

```sh
# AX contract must keep passing.
go test ./cmd/axcdp
go run ./cmd/axcdp -verify-cdp http://127.0.0.1:9221

# Browser contract must be checked against the combined axcdp endpoint.
go run ./cmd/axcdp -verify-browser-cdp http://127.0.0.1:<combined-port>

# Sibling CLI must prove raw browser commands work through the combined server.
cd ../cdp
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port <combined-port> -command 'Page.navigate {"url":"https://example.com"}' -format json -timeout 10
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port <combined-port> -command 'Runtime.evaluate {"expression":"document.body.innerText","returnByValue":true}' -format json -timeout 10
GOWORK=off go run ./cmd/cdp -remote-host 127.0.0.1 -remote-port <combined-port> -command 'Network.enable' -format json -timeout 10
```

Browser-only methods should keep returning JSON-RPC method-not-found (`-32601`)
or HTTP `501` from AX-only targets.

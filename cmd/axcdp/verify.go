package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func verifyCDPTarget(endpoint, selector string) error {
	if strings.TrimSpace(selector) == "" {
		return fmt.Errorf("-verify-target requires -target with a target title, id, or URL substring")
	}
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	if base.Host == "" {
		return fmt.Errorf("endpoint must include host")
	}
	client := &http.Client{Timeout: 8 * time.Second}
	var targets []map[string]any
	if err := getEndpointJSON(client, base, "/json/list", &targets); err != nil {
		return err
	}
	target, err := findTarget(targets, selector)
	if err != nil {
		return err
	}
	if err := verifyPreviewURL(client, target); err != nil {
		return err
	}
	if err := verifyTargetWebSocket(target); err != nil {
		return err
	}
	return nil
}

func findTarget(targets []map[string]any, selector string) (map[string]any, error) {
	selector = strings.ToLower(selector)
	var candidates []string
	for _, target := range targets {
		if target["type"] != "page" {
			continue
		}
		candidates = append(candidates, targetSummary(target))
		haystack := strings.ToLower(strings.Join([]string{
			fmt.Sprint(target["id"]),
			fmt.Sprint(target["title"]),
			fmt.Sprint(target["url"]),
		}, "\n"))
		if selector == "" || strings.Contains(haystack, selector) {
			return target, nil
		}
	}
	if selector == "" {
		return nil, fmt.Errorf("no page target found")
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no page target matching %q; no page targets available", selector)
	}
	return nil, fmt.Errorf("no page target matching %q; available page targets: %s", selector, strings.Join(candidates, "; "))
}

func targetSummary(target map[string]any) string {
	parts := make([]string, 0, 3)
	if title := targetString(target, "title"); title != "" {
		parts = append(parts, "title="+title)
	}
	if id := targetString(target, "id"); id != "" {
		parts = append(parts, "id="+id)
	}
	if rawURL := targetString(target, "url"); rawURL != "" {
		parts = append(parts, "url="+rawURL)
	}
	if len(parts) == 0 {
		return "(untitled target)"
	}
	return strings.Join(parts, " ")
}

func targetString(target map[string]any, name string) string {
	v, ok := target[name]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func verifyPreviewURL(client *http.Client, target map[string]any) error {
	rawURL := fmt.Sprint(target["url"])
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("target %q has non-preview URL %q", target["title"], rawURL)
	}
	u, err := url.Parse(strings.TrimRight(rawURL, "/") + "/screenshot.png")
	if err != nil {
		return fmt.Errorf("parse preview URL: %w", err)
	}
	resp, err := client.Get(u.String())
	if err != nil {
		return fmt.Errorf("GET %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u.String(), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", u.String(), err)
	}
	if len(data) < 32 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return fmt.Errorf("preview %s returned %d non-PNG bytes", u.String(), len(data))
	}
	return nil
}

func verifyTargetWebSocket(target map[string]any) error {
	wsURL := fmt.Sprint(target["webSocketDebuggerUrl"])
	if wsURL == "" {
		return fmt.Errorf("target %q has no websocket url", target["title"])
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial target websocket: %w", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(12 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	if err := writeCDPMessage(conn, cdpMessage{ID: 1, Method: "DOM.getDocument", Params: json.RawMessage(`{"depth":3}`)}); err != nil {
		return err
	}
	resp, err := readCDPResponse(conn, 1)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("DOM.getDocument failed: %s", resp.Error.Message)
	}
	if err := verifyDOMHasWindowChildren(resp.Result); err != nil {
		return err
	}
	if err := verifyScreencast(conn); err != nil {
		return err
	}
	return nil
}

func verifyDOMHasWindowChildren(result map[string]any) error {
	root, _ := result["root"].(map[string]any)
	if root == nil {
		return fmt.Errorf("DOM.getDocument returned no root")
	}
	if root["nodeName"] != "#document" {
		return fmt.Errorf("DOM root = %v, want #document", root["nodeName"])
	}
	child := firstDOMChild(root)
	if child == nil {
		return fmt.Errorf("DOM document has no child")
	}
	if fmt.Sprint(child["description"]) == "AX tree timed out" {
		return fmt.Errorf("DOM returned AX timeout fallback")
	}
	if child["nodeName"] == "AXWindow" {
		return verifyDOMWindowHasChildren(child)
	}
	if child["nodeName"] != "AXApplication" {
		return fmt.Errorf("DOM document child = %v, want AXApplication or AXWindow", child["nodeName"])
	}
	for _, window := range domChildren(child) {
		if window["nodeName"] != "AXWindow" {
			continue
		}
		return verifyDOMWindowHasChildren(window)
	}
	return fmt.Errorf("AXApplication has no AXWindow child")
}

func verifyDOMWindowHasChildren(window map[string]any) error {
	if numericResult(window["childNodeCount"]) <= 0 {
		return fmt.Errorf("AXWindow %q has no children", window["attributes"])
	}
	return nil
}

func firstDOMChild(node map[string]any) map[string]any {
	children := domChildren(node)
	if len(children) == 0 {
		return nil
	}
	return children[0]
}

func domChildren(node map[string]any) []map[string]any {
	raw, _ := node["children"].([]any)
	children := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		child, _ := item.(map[string]any)
		if child != nil {
			children = append(children, child)
		}
	}
	return children
}

func verifyCDPEndpoint(endpoint string) error {
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	if base.Host == "" {
		return fmt.Errorf("endpoint must include host")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	var version map[string]any
	if err := getEndpointJSON(client, base, "/json/version", &version); err != nil {
		return err
	}
	if version["Protocol-Version"] != "1.3" {
		return fmt.Errorf("unexpected protocol version %v", version["Protocol-Version"])
	}

	var allTargets []map[string]any
	if err := getEndpointJSON(client, base, "/json/list", &allTargets); err != nil {
		return err
	}
	nodeTarget, hasNodeTarget := optionalTargetOfType(allTargets, "node")
	for _, target := range allTargets {
		if target["type"] == "node" && strings.HasPrefix(fmt.Sprint(target["url"]), "file://axcdp/app/") {
			return fmt.Errorf("app target %q is incorrectly advertised as node", target["title"])
		}
	}

	var tabTargets []map[string]any
	if err := getEndpointJSON(client, base, "/json/list?for_tab", &tabTargets); err != nil {
		return err
	}
	target, err := firstTargetOfType(tabTargets, "page")
	if err != nil {
		return err
	}
	for _, target := range tabTargets {
		if target["type"] != "page" {
			return fmt.Errorf("for_tab target %q has type %q, want page", target["title"], target["type"])
		}
	}

	var protocol struct {
		Domains []map[string]any `json:"domains"`
	}
	if err := getEndpointJSON(client, base, "/json/protocol", &protocol); err != nil {
		return err
	}
	if err := verifyProtocolDomains(protocol.Domains); err != nil {
		return err
	}
	advertised := advertisedProtocolMethods(protocol.Domains)
	if len(advertised) == 0 {
		return fmt.Errorf("protocol advertises no commands")
	}

	var coverage struct {
		Entries     []cdpCoverageEntry     `json:"entries"`
		Unsupported []cdpUnsupportedMethod `json:"unsupported"`
	}
	if err := getEndpointJSON(client, base, "/json/coverage", &coverage); err != nil {
		return err
	}
	if err := verifyCoverageMatchesProtocol(coverage.Entries, advertised); err != nil {
		return err
	}
	if err := verifyUnsupportedCoverage(coverage.Unsupported, advertised); err != nil {
		return err
	}
	if err := verifyHTTPEndpoints(client, base, target); err != nil {
		return err
	}

	wsURL, _ := target["webSocketDebuggerUrl"].(string)
	if wsURL == "" {
		return fmt.Errorf("page target has no websocket url")
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial target websocket: %w", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}

	if err := writeCDPMessage(conn, cdpMessage{ID: 1, Method: "Runtime.evaluate", Params: json.RawMessage(`{"expression":"document.title"}`)}); err != nil {
		return err
	}
	resp, err := readCDPResponse(conn, 1)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Runtime.evaluate failed: %s", resp.Error.Message)
	}
	if resp.Result["result"] == nil {
		return fmt.Errorf("Runtime.evaluate returned no result")
	}

	if err := writeCDPMessage(conn, cdpMessage{ID: 7, Method: "Schema.getDomains"}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 7)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Schema.getDomains failed: %s", resp.Error.Message)
	}
	domains, _ := resp.Result["domains"].([]any)
	if len(domains) == 0 {
		return fmt.Errorf("Schema.getDomains returned no domains")
	}
	if err := writeCDPMessage(conn, cdpMessage{ID: 8, Method: "AX.getVersion"}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 8)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("AX.getVersion failed: %s", resp.Error.Message)
	}
	if resp.Result["version"] == "" || resp.Result["methods"] == nil {
		return fmt.Errorf("AX.getVersion returned incomplete result")
	}

	if err := writeCDPMessage(conn, cdpMessage{ID: 2, Method: "Page.captureScreenshot"}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 2)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Page.captureScreenshot failed: %s", resp.Error.Message)
	}
	data, _ := resp.Result["data"].(string)
	if len(data) < 20 {
		return fmt.Errorf("Page.captureScreenshot returned short image data")
	}
	if err := verifyScreencast(conn); err != nil {
		return err
	}

	if err := writeCDPMessage(conn, cdpMessage{ID: 3, Method: "DOM.getNodeForLocation", Params: json.RawMessage(`{"x":10,"y":10}`)}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 3)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("DOM.getNodeForLocation failed: %s", resp.Error.Message)
	}
	nodeID := numericResult(resp.Result["nodeId"])
	if nodeID != 0 {
		params, err := json.Marshal(map[string]any{"nodeId": nodeID})
		if err != nil {
			return fmt.Errorf("encode highlight params: %w", err)
		}
		if err := writeCDPMessage(conn, cdpMessage{ID: 4, Method: "Overlay.highlightNode", Params: params}); err != nil {
			return err
		}
		resp, err = readCDPResponse(conn, 4)
		if err != nil {
			return err
		}
		if resp.Error != nil {
			return fmt.Errorf("Overlay.highlightNode failed: %s", resp.Error.Message)
		}
		if err := writeCDPMessage(conn, cdpMessage{ID: 5, Method: "Overlay.hideHighlight"}); err != nil {
			return err
		}
		resp, err = readCDPResponse(conn, 5)
		if err != nil {
			return err
		}
		if resp.Error != nil {
			return fmt.Errorf("Overlay.hideHighlight failed: %s", resp.Error.Message)
		}
	}

	nextID := int64(11)
	for _, item := range coverage.Unsupported {
		if err := writeCDPMessage(conn, cdpMessage{ID: nextID, Method: item.Method}); err != nil {
			return err
		}
		resp, err = readCDPResponse(conn, nextID)
		if err != nil {
			return err
		}
		if resp.Error == nil || resp.Error.Code != -32601 {
			return fmt.Errorf("%s response = %+v, want method-not-found -32601", item.Method, resp)
		}
		nextID++
	}

	if hasNodeTarget {
		return verifyNodeTarget(nodeTarget)
	}
	return nil
}

func verifyBrowserCDPEndpoint(endpoint string) error {
	base, err := parseEndpoint(endpoint)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var version map[string]any
	if err := getEndpointJSON(client, base, "/json/version", &version); err != nil {
		return err
	}
	wsURL, _ := version["webSocketDebuggerUrl"].(string)
	if wsURL == "" {
		return fmt.Errorf("endpoint has no browser websocket")
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial browser websocket: %w", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set browser read deadline: %w", err)
	}
	if err := writeCDPMessage(conn, cdpMessage{ID: 1, Method: "Target.createTarget", Params: json.RawMessage(`{"url":"about:blank"}`)}); err != nil {
		return err
	}
	resp, err := readCDPResponse(conn, 1)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Target.createTarget failed: %s", resp.Error.Message)
	}
	targetID, _ := resp.Result["targetId"].(string)
	if targetID == "" {
		return fmt.Errorf("Target.createTarget returned no targetId")
	}
	targetURL := *base
	targetURL.Scheme = "ws"
	if base.Scheme == "https" {
		targetURL.Scheme = "wss"
	}
	targetURL.Path = "/devtools/page/" + targetID
	targetURL.RawQuery = ""
	targetConn, _, err := websocket.DefaultDialer.Dial(targetURL.String(), nil)
	if err != nil {
		return fmt.Errorf("dial browser target websocket: %w", err)
	}
	defer targetConn.Close()
	if err := targetConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set browser target read deadline: %w", err)
	}
	for _, check := range []cdpMessage{
		{ID: 2, Method: "Runtime.evaluate", Params: json.RawMessage(`{"expression":"document.title","returnByValue":true}`)},
		{ID: 3, Method: "Network.enable"},
		{ID: 4, Method: "Page.navigate", Params: json.RawMessage(`{"url":"about:blank"}`)},
	} {
		if err := writeCDPMessage(targetConn, check); err != nil {
			return err
		}
		resp, err := readCDPResponse(targetConn, check.ID)
		if err != nil {
			return err
		}
		if resp.Error != nil {
			return fmt.Errorf("%s failed: %s", check.Method, resp.Error.Message)
		}
	}
	params, _ := json.Marshal(map[string]string{"targetId": targetID})
	if err := writeCDPMessage(conn, cdpMessage{ID: 5, Method: "Target.closeTarget", Params: params}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 5)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Target.closeTarget failed: %s", resp.Error.Message)
	}
	return nil
}

func parseEndpoint(endpoint string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	if base.Host == "" {
		return nil, fmt.Errorf("endpoint must include host")
	}
	return base, nil
}

func verifyScreencast(conn *websocket.Conn) error {
	if err := writeCDPMessage(conn, cdpMessage{ID: 9, Method: "Page.startScreencast"}); err != nil {
		return err
	}
	resp, err := readCDPResponse(conn, 9)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Page.startScreencast failed: %s", resp.Error.Message)
	}
	event, err := readCDPEvent(conn, "Page.screencastFrame")
	if err != nil {
		return err
	}
	params := event.Params
	data, _ := params["data"].(string)
	if len(data) < 20 {
		return fmt.Errorf("Page.screencastFrame returned short image data")
	}
	if params["metadata"] == nil {
		return fmt.Errorf("Page.screencastFrame returned no metadata")
	}
	if err := writeCDPMessage(conn, cdpMessage{ID: 10, Method: "Page.stopScreencast"}); err != nil {
		return err
	}
	resp, err = readCDPResponse(conn, 10)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("Page.stopScreencast failed: %s", resp.Error.Message)
	}
	return nil
}

func verifyHTTPEndpoints(client *http.Client, base *url.URL, target map[string]any) error {
	targetID := fmt.Sprint(target["id"])
	tests := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPut, "/json/new?file://axcdp/verify", http.StatusNotImplemented},
		{http.MethodGet, "/json/close/" + targetID, http.StatusNotImplemented},
	}
	if !strings.HasPrefix(fmt.Sprint(target["url"]), "file://axcdp/app/") {
		tests = append(tests, struct {
			method string
			path   string
			status int
		}{http.MethodGet, "/json/activate/" + targetID, http.StatusOK})
	}
	for _, test := range tests {
		if err := verifyHTTPStatus(client, base, test.method, test.path, test.status); err != nil {
			return err
		}
	}
	return nil
}

func verifyHTTPStatus(client *http.Client, base *url.URL, method, path string, want int) error {
	u := *base
	u.Path = path
	u.RawQuery = ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		u.Path = path[:i]
		u.RawQuery = path[i+1:]
	}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, u.String(), err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: %s, want %d", method, u.String(), resp.Status, want)
	}
	return nil
}

func getEndpointJSON(client *http.Client, base *url.URL, path string, dst any) error {
	u := *base
	u.Path = path
	u.RawQuery = ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		u.Path = path[:i]
		u.RawQuery = path[i+1:]
	}
	resp, err := client.Get(u.String())
	if err != nil {
		return fmt.Errorf("GET %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u.String(), resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", u.String(), err)
	}
	return nil
}

func firstTargetOfType(targets []map[string]any, typ string) (map[string]any, error) {
	target, ok := optionalTargetOfType(targets, typ)
	if ok {
		return target, nil
	}
	return nil, fmt.Errorf("no %s target found", typ)
}

func optionalTargetOfType(targets []map[string]any, typ string) (map[string]any, bool) {
	for _, target := range targets {
		if target["type"] == typ {
			return target, true
		}
	}
	return nil, false
}

func verifyNodeTarget(target map[string]any) error {
	wsURL, _ := target["webSocketDebuggerUrl"].(string)
	if wsURL == "" {
		return fmt.Errorf("node target has no websocket url")
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial node target websocket: %w", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set node read deadline: %w", err)
	}
	if err := writeCDPMessage(conn, cdpMessage{ID: 1, Method: "Runtime.evaluate", Params: json.RawMessage(`{"expression":"document.title"}`)}); err != nil {
		return err
	}
	resp, err := readCDPResponse(conn, 1)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("node Runtime.evaluate failed: %s", resp.Error.Message)
	}
	result, _ := resp.Result["result"].(map[string]any)
	if result["value"] == "" {
		return fmt.Errorf("node Runtime.evaluate returned empty title")
	}
	return nil
}

func advertisedProtocolMethods(domains []map[string]any) map[string]bool {
	out := make(map[string]bool)
	for _, domain := range domains {
		domainName := fmt.Sprint(domain["domain"])
		list, _ := domain["commands"].([]any)
		for _, item := range list {
			command, _ := item.(map[string]any)
			name := fmt.Sprint(command["name"])
			if domainName != "" && name != "" {
				out[domainName+"."+name] = true
			}
		}
	}
	return out
}

func verifyProtocolDomains(domains []map[string]any) error {
	forbidden := map[string]bool{
		"CSS":          true,
		"Network":      true,
		"Storage":      true,
		"IndexedDB":    true,
		"CacheStorage": true,
		"Database":     true,
	}
	for _, domain := range domains {
		name := fmt.Sprint(domain["domain"])
		if forbidden[name] {
			return fmt.Errorf("protocol advertises non-AX domain %s", name)
		}
	}
	return nil
}

func verifyCoverageMatchesProtocol(entries []cdpCoverageEntry, advertised map[string]bool) error {
	uncovered := make(map[string]bool, len(advertised))
	for method := range advertised {
		uncovered[method] = true
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.Method == "" || entry.Status == "" || entry.Backend == "" {
			return fmt.Errorf("incomplete coverage entry: %+v", entry)
		}
		if seen[entry.Method] {
			return fmt.Errorf("duplicate coverage entry for %s", entry.Method)
		}
		seen[entry.Method] = true
		if entry.Advertised != advertised[entry.Method] {
			return fmt.Errorf("coverage for %s disagrees with protocol", entry.Method)
		}
		delete(uncovered, entry.Method)
	}
	if len(uncovered) != 0 {
		return fmt.Errorf("protocol methods missing coverage: %v", sortedMapKeys(uncovered))
	}
	return nil
}

func verifyUnsupportedCoverage(entries []cdpUnsupportedMethod, advertised map[string]bool) error {
	if len(entries) == 0 {
		return fmt.Errorf("coverage lists no unsupported methods")
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.Method == "" || entry.Reason == "" {
			return fmt.Errorf("incomplete unsupported entry: %+v", entry)
		}
		if seen[entry.Method] {
			return fmt.Errorf("duplicate unsupported entry for %s", entry.Method)
		}
		seen[entry.Method] = true
		if advertised[entry.Method] {
			return fmt.Errorf("%s is both advertised and unsupported", entry.Method)
		}
	}
	return nil
}

func writeCDPMessage(conn *websocket.Conn, msg cdpMessage) error {
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("write %s: %w", msg.Method, err)
	}
	return nil
}

func readCDPResponse(conn *websocket.Conn, id int64) (cdpResponse, error) {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			return cdpResponse{}, fmt.Errorf("read response %d: %w", id, err)
		}
		var resp cdpResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return cdpResponse{}, fmt.Errorf("decode response %d: %w", id, err)
		}
		if resp.ID == id {
			return resp, nil
		}
	}
}

func readCDPEvent(conn *websocket.Conn, method string) (cdpEvent, error) {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			return cdpEvent{}, fmt.Errorf("read event %s: %w", method, err)
		}
		var event cdpEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return cdpEvent{}, fmt.Errorf("decode event %s: %w", method, err)
		}
		if event.Method == method {
			return event, nil
		}
	}
}

func numericResult(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

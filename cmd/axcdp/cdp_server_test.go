package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type failingPNGWriter struct{}

func (failingPNGWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWritePNGBase64ReportsWriteError(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{255, 255, 255, 255})
	err := writePNGBase64(failingPNGWriter{}, img)
	if err == nil {
		t.Fatal("writePNGBase64 succeeded with failing writer")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("writePNGBase64 error = %v, want wrapped write failure", err)
	}
}

func TestCDPWebSocketEchoesFlattenedSessionID(t *testing.T) {
	s := &cdpServer{}
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(cdpMessage{ID: 1, Method: "Target.attachToTarget"}); err != nil {
		t.Fatalf("attach write: %v", err)
	}
	readUntilResponse(t, conn, 1)

	raw := json.RawMessage(`{"expression":"document.title"}`)
	if err := conn.WriteJSON(cdpMessage{ID: 2, Method: "Runtime.evaluate", Params: raw, SessionID: "axcdp-session-1"}); err != nil {
		t.Fatalf("evaluate write: %v", err)
	}
	resp := readUntilResponse(t, conn, 2)
	if resp.SessionID != "axcdp-session-1" {
		t.Fatalf("sessionId = %q, want axcdp-session-1", resp.SessionID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestCDPWebSocketSendMessageToTargetEmitsNestedResponse(t *testing.T) {
	s := &cdpServer{}
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(cdpMessage{ID: 1, Method: "Target.attachToTarget", Params: json.RawMessage(`{"targetId":"` + cdpTargetID + `"}`)}); err != nil {
		t.Fatalf("attach write: %v", err)
	}
	attach := readUntilResponse(t, conn, 1)
	sessionID, _ := attach.Result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("attach result = %#v, want sessionId", attach.Result)
	}

	inner := `{"id":7,"method":"Runtime.evaluate","params":{"expression":"42"}}`
	params, err := json.Marshal(map[string]string{"sessionId": sessionID, "message": inner})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if err := conn.WriteJSON(cdpMessage{ID: 2, Method: "Target.sendMessageToTarget", Params: params}); err != nil {
		t.Fatalf("sendMessage write: %v", err)
	}
	nested := readUntilEvent(t, conn, "Target.receivedMessageFromTarget")
	if nested.Params["sessionId"] != sessionID {
		t.Fatalf("event sessionId = %v, want %s", nested.Params["sessionId"], sessionID)
	}
	var nestedResp cdpResponse
	if err := json.Unmarshal([]byte(fmt.Sprint(nested.Params["message"])), &nestedResp); err != nil {
		t.Fatalf("decode nested response: %v", err)
	}
	if nestedResp.ID != 7 || nestedResp.Error != nil {
		t.Fatalf("nested response = %#v, want id 7 success", nestedResp)
	}
	result := nestedResp.Result["result"].(map[string]any)
	if result["value"] != float64(42) {
		t.Fatalf("nested value = %v, want 42", result["value"])
	}
	resp := readUntilResponse(t, conn, 2)
	if resp.Error != nil {
		t.Fatalf("sendMessage response error: %+v", resp.Error)
	}
}

func TestCDPEndToEndHTTPAndWebSocketCoverage(t *testing.T) {
	s := &cdpServer{
		addr:     "127.0.0.1:0",
		nodes:    make(map[int]*cdpNode),
		backend:  make(map[int]*cdpNode),
		sessions: make(map[string]*cdpServer),
		casts:    make(map[string]chan struct{}),
		searches: make(map[string][]int),
	}
	child := &cdpNode{NodeID: 2, BackendID: 2, ParentID: 1, NodeName: "AXButton", LocalName: "button", Role: "AXButton", Title: "Save", Identifier: "save", Bounds: axRect{X: 15, Y: 25, Width: 10, Height: 10}}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Title: "Fixture", Bounds: axRect{X: 10, Y: 20, Width: 100, Height: 80}, Children: []*cdpNode{child}}
	s.nodes[1] = s.root
	s.nodes[2] = child
	s.backend[1] = s.root
	s.backend[2] = child

	ts := httptest.NewServer(s.mux())
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	var version map[string]any
	getJSON(t, ts.URL+"/json/version", &version)
	if version["Protocol-Version"] != "1.3" || version["webSocketDebuggerUrl"] == "" {
		t.Fatalf("version = %#v, want protocol and browser websocket", version)
	}

	var targets []map[string]any
	getJSON(t, ts.URL+"/json/list?for_tab", &targets)
	if len(targets) == 0 || targets[0]["type"] != "page" {
		t.Fatalf("targets = %#v, want page target", targets)
	}

	var protocol struct {
		Domains []map[string]any `json:"domains"`
	}
	getJSON(t, ts.URL+"/json/protocol", &protocol)
	advertised := make(map[string]bool)
	for _, domain := range protocol.Domains {
		name := fmt.Sprint(domain["domain"])
		list, _ := domain["commands"].([]any)
		for _, item := range list {
			command := item.(map[string]any)
			advertised[name+"."+fmt.Sprint(command["name"])] = true
		}
	}

	var coverage struct {
		Entries []cdpCoverageEntry `json:"entries"`
	}
	getJSON(t, ts.URL+"/json/coverage", &coverage)
	for _, entry := range coverage.Entries {
		if entry.Advertised != advertised[entry.Method] {
			t.Fatalf("coverage entry %#v disagrees with protocol", entry)
		}
		delete(advertised, entry.Method)
	}
	if len(advertised) != 0 {
		t.Fatalf("protocol has uncovered methods: %#v", advertised)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+host+"/devtools/page/"+cdpTargetID, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	writeCDP(t, conn, cdpMessage{ID: 1, Method: "Runtime.evaluate", Params: json.RawMessage(`{"expression":"document.title"}`)})
	resp := readUntilResponse(t, conn, 1)
	if resp.Error != nil {
		t.Fatalf("Runtime.evaluate error: %+v", resp.Error)
	}
	result := resp.Result["result"].(map[string]any)
	if result["value"] != "macOS Accessibility" {
		t.Fatalf("Runtime.evaluate result = %#v, want target title", result)
	}

	writeCDP(t, conn, cdpMessage{ID: 2, Method: "DOM.getDocument", Params: json.RawMessage(`{"depth":1}`)})
	resp = readUntilResponse(t, conn, 2)
	if resp.Error != nil {
		t.Fatalf("DOM.getDocument error: %+v", resp.Error)
	}
	root := resp.Result["root"].(map[string]any)
	if root["nodeName"] != "#document" || root["nodeType"] != float64(9) {
		t.Fatalf("DOM root = %#v, want document", root)
	}

	writeCDP(t, conn, cdpMessage{ID: 3, Method: "Page.getLayoutMetrics"})
	resp = readUntilResponse(t, conn, 3)
	if resp.Error != nil {
		t.Fatalf("Page.getLayoutMetrics error: %+v", resp.Error)
	}
	size := resp.Result["contentSize"].(map[string]any)
	if size["width"] == float64(0) || size["height"] == float64(0) {
		t.Fatalf("contentSize = %#v, want non-zero AX viewport", size)
	}

	writeCDP(t, conn, cdpMessage{ID: 4, Method: "Page.navigate", Params: json.RawMessage(`{"url":"https://example.com"}`)})
	resp = readUntilResponse(t, conn, 4)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("Page.navigate response = %#v, want -32601", resp)
	}
}

func TestCDPBrowserBackendListAndWebSocketProxy(t *testing.T) {
	upstreamMux := http.NewServeMux()
	var upstreamURL string
	upstreamMux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, []map[string]any{{
			"id":                   "browser-target-1",
			"type":                 "page",
			"title":                "Browser Fixture",
			"url":                  "https://example.com/",
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(upstreamURL, "http") + "/devtools/page/browser-target-1",
		}})
	})
	upstreamMux.HandleFunc("/json/new", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, map[string]any{
			"id":                   "browser-target-2",
			"type":                 "page",
			"title":                "New Browser Fixture",
			"url":                  firstNonEmpty(r.URL.RawQuery, "about:blank"),
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(upstreamURL, "http") + "/devtools/page/browser-target-2",
		})
	})
	upstreamMux.HandleFunc("/json/close/browser-target-2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Target is closing"))
	})
	handleBrowserPage := func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg cdpMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(cdpResponse{ID: msg.ID, Result: map[string]any{"proxied": msg.Method}})
		}
	}
	upstreamMux.HandleFunc("/devtools/page/browser-target-1", handleBrowserPage)
	upstreamMux.HandleFunc("/devtools/page/browser-target-2", handleBrowserPage)
	upstream := httptest.NewServer(upstreamMux)
	defer upstream.Close()
	upstreamURL = upstream.URL

	backend, err := newBrowserBackend(upstream.URL)
	if err != nil {
		t.Fatalf("newBrowserBackend: %v", err)
	}
	s := &cdpServer{addr: "127.0.0.1:0", browser: backend, staticList: true}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	var targets []map[string]any
	getJSON(t, ts.URL+"/json/list", &targets)
	var browserTarget map[string]any
	for _, target := range targets {
		if target["browserTargetId"] == "browser-target-1" {
			browserTarget = target
			break
		}
	}
	if browserTarget == nil {
		t.Fatalf("targets = %#v, want proxied browser target", targets)
	}
	id, _ := browserTarget["id"].(string)
	if len(id) != 32 || strings.HasPrefix(id, "browser-") {
		t.Fatalf("id = %q, want Chrome-compatible 32 hex target id", id)
	}
	wsURL, _ := browserTarget["webSocketDebuggerUrl"].(string)
	if !strings.Contains(wsURL, "/devtools/browser-proxy/") {
		t.Fatalf("webSocketDebuggerUrl = %q, want browser proxy path", wsURL)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy websocket: %v", err)
	}
	defer conn.Close()
	writeCDP(t, conn, cdpMessage{ID: 42, Method: "Page.navigate", Params: json.RawMessage(`{"url":"https://example.com"}`)})
	resp := readUntilResponse(t, conn, 42)
	if resp.Error != nil || resp.Result["proxied"] != "Page.navigate" {
		t.Fatalf("proxy response = %#v, want proxied Page.navigate", resp)
	}

	pageWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/devtools/page/" + id
	conn, _, err = websocket.DefaultDialer.Dial(pageWS, nil)
	if err != nil {
		t.Fatalf("dial page proxy websocket: %v", err)
	}
	defer conn.Close()
	writeCDP(t, conn, cdpMessage{ID: 43, Method: "Runtime.evaluate", Params: json.RawMessage(`{"expression":"document.title"}`)})
	resp = readUntilResponse(t, conn, 43)
	if resp.Error != nil || resp.Result["proxied"] != "Runtime.evaluate" {
		t.Fatalf("page proxy response = %#v, want proxied Runtime.evaluate", resp)
	}

	rootConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/devtools/page/"+cdpTargetID, nil)
	if err != nil {
		t.Fatalf("dial root websocket: %v", err)
	}
	defer rootConn.Close()
	writeCDP(t, rootConn, cdpMessage{ID: 43, Method: "Target.setDiscoverTargets", Params: json.RawMessage(`{"discover":true}`)})
	readUntilEvent(t, rootConn, "Target.targetCreated")
	if resp := readUntilResponse(t, rootConn, 43); resp.Error != nil {
		t.Fatalf("Target.setDiscoverTargets error: %+v", resp.Error)
	}
	writeCDP(t, rootConn, cdpMessage{ID: 44, Method: "Target.getTargets"})
	resp = readUntilResponse(t, rootConn, 44)
	if resp.Error != nil {
		t.Fatalf("Target.getTargets error: %+v", resp.Error)
	}
	infos := resp.Result["targetInfos"].([]any)
	found := false
	for _, item := range infos {
		info := item.(map[string]any)
		if info["targetId"] == id && info["url"] == "https://example.com/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("targetInfos = %#v, want browser target %s", infos, id)
	}

	writeCDP(t, rootConn, cdpMessage{ID: 45, Method: "Target.createTarget", Params: json.RawMessage(`{"url":"about:blank"}`)})
	resp = readUntilResponse(t, rootConn, 45)
	if resp.Error != nil {
		t.Fatalf("Target.createTarget error: %+v", resp.Error)
	}
	newID, _ := resp.Result["targetId"].(string)
	if len(newID) != 32 {
		t.Fatalf("Target.createTarget result = %#v, want proxied target id", resp.Result)
	}
	writeCDP(t, rootConn, cdpMessage{ID: 46, Method: "Target.closeTarget", Params: json.RawMessage(`{"targetId":"` + newID + `"}`)})
	resp = readUntilResponse(t, rootConn, 46)
	if resp.Error != nil || resp.Result["success"] != true {
		t.Fatalf("Target.closeTarget response = %#v, want success", resp)
	}

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/json/new?about:blank", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /json/new: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /json/new status = %s, want 200", httpResp.Status)
	}
	var created map[string]any
	if err := json.NewDecoder(httpResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode /json/new: %v", err)
	}
	createdID, _ := created["id"].(string)
	if len(createdID) != 32 || created["browserTargetId"] != "browser-target-2" {
		t.Fatalf("/json/new = %#v, want proxied browser target", created)
	}
	httpResp, err = http.Get(ts.URL + "/json/close/" + createdID)
	if err != nil {
		t.Fatalf("GET /json/close: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /json/close status = %s, want 200", httpResp.Status)
	}
}

func TestVerifyBrowserCDPEndpoint(t *testing.T) {
	upstreamMux := http.NewServeMux()
	var upstreamURL string
	upstreamMux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, []map[string]any{})
	})
	upstreamMux.HandleFunc("/json/new", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, map[string]any{
			"id":                   "browser-target-2",
			"type":                 "page",
			"title":                "New Browser Fixture",
			"url":                  firstNonEmpty(r.URL.RawQuery, "about:blank"),
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(upstreamURL, "http") + "/devtools/page/browser-target-2",
		})
	})
	upstreamMux.HandleFunc("/json/close/browser-target-2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Target is closing"))
	})
	upstreamMux.HandleFunc("/devtools/page/browser-target-2", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg cdpMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(cdpResponse{ID: msg.ID, Result: map[string]any{"ok": true}})
		}
	})
	upstream := httptest.NewServer(upstreamMux)
	defer upstream.Close()
	upstreamURL = upstream.URL

	backend, err := newBrowserBackend(upstream.URL)
	if err != nil {
		t.Fatalf("newBrowserBackend: %v", err)
	}
	s := &cdpServer{addr: "127.0.0.1:0", browser: backend}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	if err := verifyBrowserCDPEndpoint(ts.URL); err != nil {
		t.Fatalf("verifyBrowserCDPEndpoint: %v", err)
	}
}

func TestVerifyCDPEndpoint(t *testing.T) {
	s := &cdpServer{
		addr:       "127.0.0.1:0",
		staticList: true,
		nodes:      make(map[int]*cdpNode),
		backend:    make(map[int]*cdpNode),
		sessions:   make(map[string]*cdpServer),
		casts:      make(map[string]chan struct{}),
		searches:   make(map[string][]int),
	}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Title: "Fixture", Bounds: axRect{X: 10, Y: 20, Width: 100, Height: 80}}
	s.nodes[1] = s.root
	s.backend[1] = s.root
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	if err := verifyCDPEndpoint(ts.URL); err != nil {
		t.Fatalf("verifyCDPEndpoint: %v", err)
	}
}

func TestVerifyCDPEndpointAllowsAppScopedPageOnlyDiscovery(t *testing.T) {
	s := &cdpServer{
		addr:     "127.0.0.1:0",
		appArg:   "Fixture",
		nodes:    make(map[int]*cdpNode),
		backend:  make(map[int]*cdpNode),
		sessions: make(map[string]*cdpServer),
		casts:    make(map[string]chan struct{}),
		searches: make(map[string][]int),
	}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Title: "Fixture", Bounds: axRect{X: 10, Y: 20, Width: 100, Height: 80}}
	s.nodes[1] = s.root
	s.backend[1] = s.root
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	if err := verifyCDPEndpoint(ts.URL); err != nil {
		t.Fatalf("verifyCDPEndpoint app scoped: %v", err)
	}
}

func TestCDPHTTPJSONSetsContentLength(t *testing.T) {
	s := &cdpServer{}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/version", nil)
	rec := httptest.NewRecorder()

	s.handleVersion(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	if got := res.Header.Get("Transfer-Encoding"); got != "" {
		t.Fatalf("Transfer-Encoding = %q, want empty", got)
	}
	if got := res.Header.Get("Content-Length"); got == "" {
		t.Fatal("Content-Length is empty")
	} else if n, err := strconv.Atoi(got); err != nil || n <= 0 {
		t.Fatalf("Content-Length = %q, want positive integer", got)
	}
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func writeCDP(t *testing.T, conn *websocket.Conn, msg cdpMessage) {
	t.Helper()
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("%s write: %v", msg.Method, err)
	}
}

func TestCDPRemoteDebuggingHTTPCompatibility(t *testing.T) {
	s := &cdpServer{}
	tests := []struct {
		name       string
		req        *http.Request
		call       func(http.ResponseWriter, *http.Request)
		wantStatus int
		want       string
	}{
		{
			name:       "new unsupported",
			req:        httptest.NewRequest("PUT", "http://localhost:9221/json/new?file://axcdp/accessibility", nil),
			call:       s.handleNewTarget,
			wantStatus: http.StatusNotImplemented,
			want:       "new targets are not AX-backed",
		},
		{
			name:       "activate",
			req:        httptest.NewRequest("GET", "http://localhost:9221/json/activate/"+cdpTargetID, nil),
			call:       s.handleActivateTarget,
			wantStatus: http.StatusOK,
			want:       "Target activated",
		},
		{
			name:       "close unsupported",
			req:        httptest.NewRequest("GET", "http://localhost:9221/json/close/"+cdpTargetID, nil),
			call:       s.handleCloseTarget,
			wantStatus: http.StatusNotImplemented,
			want:       "closing AX targets is not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.call(rec, tt.req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want substring %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestCDPTargetSpecificRuntimeAndPageMetadata(t *testing.T) {
	s := &cdpServer{appArg: "12345"}

	eval := s.runtimeEvaluate(json.RawMessage(`{"expression":"document.title"}`))
	result := eval["result"].(map[string]any)
	if result["value"] != "pid 12345" {
		t.Fatalf("document.title = %v, want pid 12345", result["value"])
	}

	frameTree, err := s.dispatchCDP(nil, nil, "", "Page.getFrameTree", nil)
	if err != nil {
		t.Fatalf("Page.getFrameTree: %v", err)
	}
	frame := frameTree["frameTree"].(map[string]any)["frame"].(map[string]any)
	if frame["url"] != "http://127.0.0.1:9221/axcdp/app/12345" {
		t.Fatalf("frame url = %v, want preview URL", frame["url"])
	}
}

func TestCDPRuntimeEvaluateTypedPrimitives(t *testing.T) {
	s := &cdpServer{}
	tests := []struct {
		expr string
		typ  string
		want any
	}{
		{expr: "true", typ: "boolean", want: true},
		{expr: "42", typ: "number", want: float64(42)},
		{expr: `"hello"`, typ: "string", want: "hello"},
		{expr: "document.URL", typ: "string", want: "http://127.0.0.1:9221/axcdp/accessibility"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got := s.runtimeEvaluate(json.RawMessage(fmt.Sprintf(`{"expression":%q}`, tt.expr)))
			result := got["result"].(map[string]any)
			if result["type"] != tt.typ || result["value"] != tt.want {
				t.Fatalf("result = %#v, want type %s value %#v", result, tt.typ, tt.want)
			}
		})
	}
}

func TestCDPRuntimeEvaluateAXContext(t *testing.T) {
	child := &cdpNode{NodeID: 2, BackendID: 2, ParentID: 1, NodeName: "AXButton", LocalName: "button", Role: "AXButton", Title: "Save", Identifier: "save", Bounds: axRect{X: 15, Y: 25, Width: 10, Height: 10}}
	s := &cdpServer{}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Title: "Fixture", Bounds: axRect{X: 10, Y: 20, Width: 30, Height: 40}, Children: []*cdpNode{child}, ChildrenReady: true}
	s.nodes = map[int]*cdpNode{1: s.root, 2: child}
	s.backend = map[int]*cdpNode{1: s.root, 2: child}

	got := s.runtimeEvaluate(json.RawMessage(`{"expression":"ax.query('save').title"}`))
	result := got["result"].(map[string]any)
	if result["type"] != "string" || result["value"] != "Save" {
		t.Fatalf("ax query title result = %#v, want Save string", result)
	}

	got = s.runtimeEvaluate(json.RawMessage(`{"expression":"ax.query('save')"}`))
	result = got["result"].(map[string]any)
	if result["type"] != "object" || result["subtype"] != "node" || result["objectId"] != fmt.Sprintf("node:%d", domNodeID(2)) {
		t.Fatalf("ax query node result = %#v, want node object", result)
	}
}

func TestDedupeRunningAppsKeepsFirstName(t *testing.T) {
	apps := dedupeRunningApps([]runningApp{
		{PID: 1, Name: "Brave Browser"},
		{PID: 2, Name: "Brave Browser"},
		{PID: 3, Name: "Google Chrome Canary"},
	})
	if len(apps) != 2 {
		t.Fatalf("len(apps) = %d, want 2", len(apps))
	}
	if apps[0].PID != 1 || apps[1].PID != 3 {
		t.Fatalf("apps = %#v, want first Brave and Chrome", apps)
	}
}

func TestParseLSAppInfoVisibleKeepsSingleWordApps(t *testing.T) {
	visible := parseLSAppInfoVisible(`ASN:0x0-0x16016-"Finder": ASN:0x0-0x1581580-"System_Settings": ASN:0x0-0x1f31f3-"Brave_Browser":`)
	for _, name := range []string{"Finder", "System Settings", "Brave Browser"} {
		if !visible[name] {
			t.Fatalf("visible[%q] = false, want true in %#v", name, visible)
		}
	}
}

func TestCDPAdvertisesRootNodeTarget(t *testing.T) {
	s := &cdpServer{}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/list", nil)
	rec := httptest.NewRecorder()

	s.handleList(rec, req)

	var targets []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	found := false
	for _, target := range targets {
		if target["type"] == "node" && target["id"] == cdpNodeTargetID && strings.Contains(fmt.Sprint(target["webSocketDebuggerUrl"]), "/devtools/node/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("root node target not found in %#v", targets)
	}
}

func TestCDPForTabListShowsOneRowPerPageTarget(t *testing.T) {
	s := &cdpServer{}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/list?for_tab", nil)
	rec := httptest.NewRecorder()

	s.handleList(rec, req)

	var targets []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	seenURL := make(map[string]bool)
	for _, target := range targets {
		if target["type"] != "page" {
			t.Fatalf("target type = %v, want only page targets in for_tab list", target["type"])
		}
		url := fmt.Sprint(target["url"])
		if seenURL[url] {
			t.Fatalf("duplicate url %q in targets %#v", url, targets)
		}
		seenURL[url] = true
	}
	if !seenURL["http://localhost:9221/axcdp/accessibility"] {
		t.Fatalf("root page target missing from %#v", targets)
	}
}

func TestCDPListDoesNotDuplicateAppsAsNodeTargets(t *testing.T) {
	s := &cdpServer{}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/list", nil)
	rec := httptest.NewRecorder()

	s.handleList(rec, req)

	var targets []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	for _, target := range targets {
		if target["type"] == "node" && strings.Contains(fmt.Sprint(target["url"]), "/axcdp/app/") {
			t.Fatalf("app node target should not be advertised: %#v", target)
		}
	}
}

func TestCDPAppScopedListShowsOnlySelectedAppPage(t *testing.T) {
	s := &cdpServer{appArg: "12345"}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/list", nil)
	rec := httptest.NewRecorder()

	s.handleList(rec, req)

	var targets []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want selected app page only: %#v", len(targets), targets)
	}
	target := targets[0]
	if target["type"] != "page" || target["title"] != "pid 12345" || target["url"] != "http://localhost:9221/axcdp/app/12345" {
		t.Fatalf("target = %#v, want selected app page metadata", target)
	}
	if strings.Contains(fmt.Sprint(target["webSocketDebuggerUrl"]), "/devtools/node/") {
		t.Fatalf("target websocket = %v, should not be node target", target["webSocketDebuggerUrl"])
	}
}

func TestCDPAdvertisesAcceptedFrontendDomains(t *testing.T) {
	domains := make(map[string]bool)
	for _, domain := range cdpProtocolDomains() {
		domains[fmt.Sprint(domain["domain"])] = true
	}
	for _, name := range []string{"Browser", "Target", "Page", "Runtime", "Schema", "DOM", "Overlay", "Input", "Accessibility", "AX"} {
		if !domains[name] {
			t.Fatalf("domain %s is not advertised", name)
		}
	}
	for _, name := range []string{"CSS", "Network", "Storage", "IndexedDB", "CacheStorage", "Database"} {
		if domains[name] {
			t.Fatalf("domain %s is advertised but is not AX-backed", name)
		}
	}
}

func TestVerifyProtocolDomainsRejectsBrowserOnlyDomains(t *testing.T) {
	if err := verifyProtocolDomains(cdpProtocolDomains()); err != nil {
		t.Fatalf("verifyProtocolDomains(cdpProtocolDomains): %v", err)
	}
	err := verifyProtocolDomains([]map[string]any{{"domain": "Network"}})
	if err == nil || !strings.Contains(err.Error(), "non-AX domain Network") {
		t.Fatalf("verifyProtocolDomains(Network) = %v, want non-AX domain error", err)
	}
}

func TestCDPProtocolAdvertisesCommandNames(t *testing.T) {
	commands := make(map[string]bool)
	for _, domain := range cdpProtocolDomains() {
		name := fmt.Sprint(domain["domain"])
		list, _ := domain["commands"].([]map[string]any)
		for _, command := range list {
			commands[name+"."+fmt.Sprint(command["name"])] = true
		}
	}
	for _, method := range []string{
		"Browser.getWindowForTarget",
		"Target.attachToTarget",
		"Page.startScreencast",
		"Runtime.evaluate",
		"DOM.performSearch",
		"Overlay.highlightNode",
		"Input.dispatchMouseEvent",
		"Input.dispatchKeyEvent",
		"Accessibility.getFullAXTree",
		"Accessibility.getPartialAXTree",
		"Accessibility.getRootAXNode",
		"Accessibility.getAXNodeAndAncestors",
		"Accessibility.getChildAXNodes",
		"Accessibility.queryAXTree",
		"AX.copyAttributeValue",
	} {
		if !commands[method] {
			t.Fatalf("%s is not advertised", method)
		}
	}
}

func TestCDPCoverageMatchesAdvertisedProtocol(t *testing.T) {
	advertised := make(map[string]bool)
	for _, domain := range cdpProtocolDomains() {
		name := fmt.Sprint(domain["domain"])
		list, _ := domain["commands"].([]map[string]any)
		for _, command := range list {
			advertised[name+"."+fmt.Sprint(command["name"])] = true
		}
	}
	covered := make(map[string]cdpCoverageEntry)
	for _, entry := range cdpCoverageEntries() {
		if entry.Method == "" || entry.Status == "" || entry.Backend == "" {
			t.Fatalf("incomplete coverage entry: %#v", entry)
		}
		if old, ok := covered[entry.Method]; ok {
			t.Fatalf("duplicate coverage entry for %s: %#v and %#v", entry.Method, old, entry)
		}
		covered[entry.Method] = entry
		if entry.Advertised && !advertised[entry.Method] {
			t.Fatalf("coverage advertises %s but /json/protocol does not", entry.Method)
		}
		if !entry.Advertised && advertised[entry.Method] {
			t.Fatalf("coverage marks %s unadvertised but /json/protocol advertises it", entry.Method)
		}
	}
	for method := range advertised {
		if _, ok := covered[method]; !ok {
			t.Fatalf("%s is advertised without a coverage entry", method)
		}
	}
	for _, method := range []string{"Network.getResponseBody", "Overlay.setInspectMode", "Input.insertText"} {
		if covered[method].Method != "" {
			t.Fatalf("%s should not have coverage because it is not AX-backed", method)
		}
	}
	unsupported := make(map[string]bool)
	for _, item := range cdpUnsupportedMethods() {
		if item.Method == "" || item.Reason == "" {
			t.Fatalf("incomplete unsupported entry: %#v", item)
		}
		if advertised[item.Method] {
			t.Fatalf("%s is both advertised and unsupported", item.Method)
		}
		if covered[item.Method].Method != "" {
			t.Fatalf("%s is both covered and unsupported", item.Method)
		}
		unsupported[item.Method] = true
	}
	for _, method := range []string{"Network.getResponseBody", "Overlay.setInspectMode", "Input.insertText"} {
		if !unsupported[method] {
			t.Fatalf("%s should be explicitly listed as unsupported", method)
		}
	}
}

func TestCDPAuditListsUnsupportedMethods(t *testing.T) {
	data, err := os.ReadFile("AUDIT.md")
	if err != nil {
		t.Fatalf("read AUDIT.md: %v", err)
	}
	audit := string(data)
	got := auditUnsupportedMethods(audit)
	want := make(map[string]bool)
	for _, item := range cdpUnsupportedMethods() {
		want[item.Method] = true
		if !strings.Contains(audit, "`"+item.Method+"`") {
			t.Fatalf("AUDIT.md does not list unsupported method %s", item.Method)
		}
	}
	for method := range got {
		if !want[method] {
			t.Fatalf("AUDIT.md lists stale unsupported method %s", method)
		}
	}
}

func auditUnsupportedMethods(audit string) map[string]bool {
	out := make(map[string]bool)
	inList := false
	for _, line := range strings.Split(audit, "\n") {
		if strings.TrimSpace(line) == "The explicit unsupported method list is:" {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "- `") && strings.HasSuffix(trimmed, "`") {
			out[strings.TrimSuffix(strings.TrimPrefix(trimmed, "- `"), "`")] = true
		}
	}
	return out
}

func TestCDPCoverageEndpoint(t *testing.T) {
	s := &cdpServer{}
	req := httptest.NewRequest("GET", "http://localhost:9221/json/coverage", nil)
	rec := httptest.NewRecorder()

	s.handleCoverage(rec, req)

	var got struct {
		Policy      string                 `json:"policy"`
		Entries     []cdpCoverageEntry     `json:"entries"`
		Unsupported []cdpUnsupportedMethod `json:"unsupported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if got.Policy == "" || len(got.Entries) == 0 || len(got.Unsupported) == 0 {
		t.Fatalf("coverage = %#v, want policy, entries, and unsupported methods", got)
	}
}

func TestCDPSchemaDomainsUseSchemaShape(t *testing.T) {
	s := &cdpServer{}
	got, err := s.dispatchCDP(nil, nil, "", "Schema.getDomains", nil)
	if err != nil {
		t.Fatalf("Schema.getDomains: %v", err)
	}
	domains := got["domains"].([]map[string]any)
	if len(domains) == 0 {
		t.Fatal("no schema domains")
	}
	if domains[0]["name"] == "" || domains[0]["version"] == "" {
		t.Fatalf("schema domain = %#v, want name and version", domains[0])
	}
	if _, ok := domains[0]["commands"]; ok {
		t.Fatalf("schema domain = %#v, should not include protocol descriptor commands", domains[0])
	}
}

func TestCDPAdvertisedCommandsDispatch(t *testing.T) {
	s := &cdpServer{}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Bounds: axRect{X: 10, Y: 20, Width: 30, Height: 40}}
	s.nodes = map[int]*cdpNode{1: s.root}
	s.backend = map[int]*cdpNode{1: s.root}
	s.sessions = make(map[string]*cdpServer)
	s.casts = make(map[string]chan struct{})
	s.searches = make(map[string][]int)
	search, err := s.dispatchCDP(nil, nil, "", "DOM.performSearch", json.RawMessage(`{"query":"*"}`))
	if err != nil {
		t.Fatalf("DOM.performSearch setup: %v", err)
	}
	searchID := fmt.Sprint(search["searchId"])

	params := map[string]json.RawMessage{
		"Browser.getWindowBounds":             json.RawMessage(`{"windowId":1}`),
		"Browser.bringToFront":                json.RawMessage(`{}`),
		"Target.attachToTarget":               json.RawMessage(`{"targetId":"` + cdpTargetID + `"}`),
		"Target.attachToBrowserTarget":        json.RawMessage(`{}`),
		"Target.detachFromTarget":             json.RawMessage(`{"sessionId":"axcdp-session-test"}`),
		"Target.sendMessageToTarget":          json.RawMessage(`{"sessionId":"axcdp-session-test","message":"{\"id\":9,\"method\":\"Runtime.evaluate\",\"params\":{\"expression\":\"1\"}}"}`),
		"Target.getTargetInfo":                json.RawMessage(`{"targetId":"` + cdpTargetID + `"}`),
		"Target.activateTarget":               json.RawMessage(`{"targetId":"` + cdpTargetID + `"}`),
		"Page.bringToFront":                   json.RawMessage(`{}`),
		"Page.setLifecycleEventsEnabled":      json.RawMessage(`{"enabled":true}`),
		"Page.screencastFrameAck":             json.RawMessage(`{"sessionId":1}`),
		"Runtime.evaluate":                    json.RawMessage(`{"expression":"1"}`),
		"Runtime.getProperties":               json.RawMessage(fmt.Sprintf(`{"objectId":"node:%d"}`, domNodeID(1))),
		"Runtime.releaseObject":               json.RawMessage(`{"objectId":"x"}`),
		"Runtime.releaseObjectGroup":          json.RawMessage(`{"objectGroup":"x"}`),
		"DOM.requestChildNodes":               json.RawMessage(`{"nodeId":1}`),
		"DOM.describeNode":                    json.RawMessage(`{"nodeId":1}`),
		"DOM.resolveNode":                     json.RawMessage(`{"nodeId":1}`),
		"DOM.requestNode":                     json.RawMessage(fmt.Sprintf(`{"objectId":"node:%d"}`, domNodeID(1))),
		"DOM.pushNodesByBackendIdsToFrontend": json.RawMessage(fmt.Sprintf(`{"backendNodeIds":[%d]}`, domBackendNodeID(1))),
		"DOM.querySelector":                   json.RawMessage(`{"nodeId":1,"selector":"*"}`),
		"DOM.querySelectorAll":                json.RawMessage(`{"nodeId":1,"selector":"*"}`),
		"DOM.performSearch":                   json.RawMessage(`{"query":"*"}`),
		"DOM.getSearchResults":                json.RawMessage(fmt.Sprintf(`{"searchId":%q,"fromIndex":0,"toIndex":1}`, searchID)),
		"DOM.discardSearchResults":            json.RawMessage(fmt.Sprintf(`{"searchId":%q}`, searchID)),
		"DOM.getOuterHTML":                    json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.getAttributes":                   json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.setAttributeValue":               json.RawMessage(fmt.Sprintf(`{"nodeId":%d,"name":"title","value":"x"}`, domNodeID(1))),
		"DOM.getBoxModel":                     json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.getContentQuads":                 json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.getNodeForLocation":              json.RawMessage(`{"x":10,"y":20}`),
		"DOM.getFrameOwner":                   json.RawMessage(`{"frameId":"` + cdpFrameID + `"}`),
		"DOM.setInspectedNode":                json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.focus":                           json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"DOM.scrollIntoViewIfNeeded":          json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"Overlay.highlightNode":               json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
		"Overlay.highlightRect":               json.RawMessage(`{"x":10,"y":20,"width":30,"height":40}`),
		"Overlay.highlightQuad":               json.RawMessage(`{"quad":[10,20,40,20,40,60,10,60]}`),
		"Overlay.hideHighlight":               json.RawMessage(`{}`),
		"Input.dispatchMouseEvent":            json.RawMessage(`{"type":"mouseMoved","x":10,"y":20}`),
	}
	skip := map[string]bool{
		"DOM.getDocument":                    true,
		"DOM.getFlattenedDocument":           true,
		"DOM.setAttributeValue":              true,
		"Page.navigate":                      true,
		"Input.dispatchKeyEvent":             true,
		"AX.createApplication":               true,
		"AX.getSystemWideElement":            true,
		"AX.release":                         true,
		"AX.setMessagingTimeout":             true,
		"AX.getPid":                          true,
		"AX.getWindow":                       true,
		"AX.copyAttributeNames":              true,
		"AX.copyAttributeValue":              true,
		"AX.copyAttributeValues":             true,
		"AX.copyMultipleAttributeValues":     true,
		"AX.getAttributeValueCount":          true,
		"AX.isAttributeSettable":             true,
		"AX.setAttributeValue":               true,
		"AX.copyParameterizedAttributeNames": true,
		"AX.copyParameterizedAttributeValue": true,
		"AX.copyActionNames":                 true,
		"AX.copyActionDescription":           true,
		"AX.performAction":                   true,
		"AX.copyElementAtPosition":           true,
		"AX.postKeyboardEvent":               true,
		"AX.createValue":                     true,
		"AX.createObserver":                  true,
		"AX.createObserverWithInfo":          true,
		"AX.addNotification":                 true,
		"AX.removeNotification":              true,
		"AX.pollEvents":                      true,
		"AX.closeObserver":                   true,
	}
	for _, domain := range cdpProtocolDomains() {
		domainName := fmt.Sprint(domain["domain"])
		commands, _ := domain["commands"].([]map[string]any)
		for _, command := range commands {
			method := domainName + "." + fmt.Sprint(command["name"])
			if skip[method] {
				continue
			}
			t.Run(method, func(t *testing.T) {
				if _, err := s.dispatchCDP(nil, nil, "", method, params[method]); err != nil {
					t.Fatalf("%s: %v", method, err)
				}
			})
		}
	}
}

func TestCDPCommonFrontendCommands(t *testing.T) {
	s := &cdpServer{}
	child := &cdpNode{NodeID: 2, BackendID: 2, ParentID: 1, NodeName: "AXButton", LocalName: "button", Role: "AXButton", Title: "Save", Identifier: "save", Bounds: axRect{X: 15, Y: 25, Width: 10, Height: 10}}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Bounds: axRect{X: 10, Y: 20, Width: 30, Height: 40}, Children: []*cdpNode{child}}
	s.nodes = map[int]*cdpNode{1: s.root, 2: child}
	s.backend = map[int]*cdpNode{1: s.root, 2: child}

	tests := []struct {
		name   string
		method string
		params json.RawMessage
		check  func(*testing.T, map[string]any)
	}{
		{
			name:   "browser window",
			method: "Browser.getWindowForTarget",
			params: json.RawMessage(`{"targetId":"` + cdpTargetID + `"}`),
			check: func(t *testing.T, got map[string]any) {
				if got["windowId"] != 1 {
					t.Fatalf("windowId = %v, want 1", got["windowId"])
				}
				bounds := got["bounds"].(map[string]any)
				if bounds["left"] != float64(10) || bounds["width"] != float64(30) {
					t.Fatalf("bounds = %#v, want AX window bounds", bounds)
				}
			},
		},
		{
			name:   "target info",
			method: "Target.getTargetInfo",
			check: func(t *testing.T, got map[string]any) {
				info := got["targetInfo"].(map[string]any)
				if info["targetId"] != cdpTargetID {
					t.Fatalf("targetId = %v, want %s", info["targetId"], cdpTargetID)
				}
			},
		},
		{
			name:   "layout metrics",
			method: "Page.getLayoutMetrics",
			check: func(t *testing.T, got map[string]any) {
				size := got["contentSize"].(map[string]any)
				if fmt.Sprint(size["x"]) != "0" || fmt.Sprint(size["y"]) != "0" || fmt.Sprint(size["width"]) != "30" {
					t.Fatalf("contentSize = %#v, want viewport-local AX window bounds", size)
				}
			},
		},
		{
			name:   "content quads",
			method: "DOM.getContentQuads",
			params: json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(1))),
			check: func(t *testing.T, got map[string]any) {
				quads := got["quads"].([]any)
				if len(quads) != 1 {
					t.Fatalf("len(quads) = %d, want 1", len(quads))
				}
				quad := quads[0].([]float64)
				if quad[0] != 0 || quad[1] != 0 || quad[4] != 30 || quad[5] != 40 {
					t.Fatalf("quad = %v, want viewport-local root bounds", quad)
				}
			},
		},
		{
			name:   "query selector",
			method: "DOM.querySelector",
			params: json.RawMessage(`{"nodeId":1,"selector":"button"}`),
			check: func(t *testing.T, got map[string]any) {
				if got["nodeId"] != domNodeID(2) {
					t.Fatalf("nodeId = %v, want %d", got["nodeId"], domNodeID(2))
				}
			},
		},
		{
			name:   "query selector all",
			method: "DOM.querySelectorAll",
			params: json.RawMessage(`{"nodeId":1,"selector":"[role=AXButton]"}`),
			check: func(t *testing.T, got map[string]any) {
				ids := got["nodeIds"].([]int)
				if len(ids) != 1 || ids[0] != domNodeID(2) {
					t.Fatalf("nodeIds = %v, want [%d]", ids, domNodeID(2))
				}
			},
		},
		{
			name:   "attributes",
			method: "DOM.getAttributes",
			params: json.RawMessage(fmt.Sprintf(`{"nodeId":%d}`, domNodeID(2))),
			check: func(t *testing.T, got map[string]any) {
				attrs := got["attributes"].([]string)
				if !stringPairsContain(attrs, "id", "save") || !stringPairsContain(attrs, "title", "Save") {
					t.Fatalf("attributes = %v, want AX id/title", attrs)
				}
			},
		},
		{
			name:   "node for location",
			method: "DOM.getNodeForLocation",
			params: json.RawMessage(`{"x":6,"y":6}`),
			check: func(t *testing.T, got map[string]any) {
				if got["nodeId"] != domNodeID(2) || got["backendNodeId"] != domBackendNodeID(2) {
					t.Fatalf("location result = %#v, want child node 2", got)
				}
			},
		},
		{
			name:   "frame owner",
			method: "DOM.getFrameOwner",
			params: json.RawMessage(`{"frameId":"` + cdpFrameID + `"}`),
			check: func(t *testing.T, got map[string]any) {
				if got["nodeId"] != domNodeID(1) || got["backendNodeId"] != domBackendNodeID(1) {
					t.Fatalf("frame owner = %#v, want root node", got)
				}
			},
		},
		{
			name:   "mouse moved",
			method: "Input.dispatchMouseEvent",
			params: json.RawMessage(`{"type":"mouseMoved","x":16,"y":26}`),
			check: func(t *testing.T, got map[string]any) {
				if len(got) != 0 {
					t.Fatalf("mouse result = %#v, want empty result", got)
				}
			},
		},
		{
			name:   "runtime node properties",
			method: "Runtime.getProperties",
			params: json.RawMessage(fmt.Sprintf(`{"objectId":"node:%d"}`, domNodeID(2))),
			check: func(t *testing.T, got map[string]any) {
				props := got["result"].([]any)
				if !runtimePropertiesContain(props, "role", "AXButton") || !runtimePropertiesContain(props, "title", "Save") {
					t.Fatalf("properties = %#v, want AX role/title", props)
				}
			},
		},
		{
			name:   "runtime bounds properties",
			method: "Runtime.getProperties",
			params: json.RawMessage(fmt.Sprintf(`{"objectId":"bounds:%d"}`, domNodeID(2))),
			check: func(t *testing.T, got map[string]any) {
				props := got["result"].([]any)
				if !runtimeNumberPropertiesContain(props, "x", 15) || !runtimeNumberPropertiesContain(props, "height", 10) {
					t.Fatalf("bounds properties = %#v, want AX bounds", props)
				}
			},
		},
		{
			name:   "request node",
			method: "DOM.requestNode",
			params: json.RawMessage(fmt.Sprintf(`{"objectId":"node:%d"}`, domNodeID(2))),
			check: func(t *testing.T, got map[string]any) {
				if got["nodeId"] != domNodeID(2) {
					t.Fatalf("nodeId = %v, want %d", got["nodeId"], domNodeID(2))
				}
			},
		},
		{
			name:   "partial ax tree",
			method: "Accessibility.getPartialAXTree",
			params: json.RawMessage(`{"nodeId":2,"fetchRelatives":true}`),
			check: func(t *testing.T, got map[string]any) {
				nodes := got["nodes"].([]any)
				if len(nodes) != 2 {
					t.Fatalf("len(nodes) = %d, want selected node and parent", len(nodes))
				}
			},
		},
		{
			name:   "root ax node",
			method: "Accessibility.getRootAXNode",
			check: func(t *testing.T, got map[string]any) {
				node := got["node"].(map[string]any)
				if node["nodeId"] != "1" {
					t.Fatalf("root node = %#v, want nodeId 1", node)
				}
			},
		},
		{
			name:   "ax node and ancestors",
			method: "Accessibility.getAXNodeAndAncestors",
			params: json.RawMessage(`{"nodeId":2}`),
			check: func(t *testing.T, got map[string]any) {
				nodes := got["nodes"].([]any)
				if len(nodes) != 2 {
					t.Fatalf("len(nodes) = %d, want child and root", len(nodes))
				}
				first := nodes[0].(map[string]any)
				if first["nodeId"] != "2" {
					t.Fatalf("first node = %#v, want nodeId 2", first)
				}
			},
		},
		{
			name:   "child ax nodes",
			method: "Accessibility.getChildAXNodes",
			params: json.RawMessage(`{"id":"1"}`),
			check: func(t *testing.T, got map[string]any) {
				nodes := got["nodes"].([]any)
				if len(nodes) != 1 {
					t.Fatalf("len(nodes) = %d, want one child", len(nodes))
				}
			},
		},
		{
			name:   "query ax tree",
			method: "Accessibility.queryAXTree",
			params: json.RawMessage(`{"role":"AXButton"}`),
			check: func(t *testing.T, got map[string]any) {
				nodes := got["nodes"].([]any)
				if len(nodes) != 1 {
					t.Fatalf("len(nodes) = %d, want one AXButton", len(nodes))
				}
			},
		},
		{
			name:   "perform search",
			method: "DOM.performSearch",
			params: json.RawMessage(`{"query":"Save"}`),
			check: func(t *testing.T, got map[string]any) {
				if got["resultCount"] != 1 {
					t.Fatalf("resultCount = %v, want 1", got["resultCount"])
				}
				searchID := fmt.Sprint(got["searchId"])
				results, err := s.dispatchCDP(nil, nil, "", "DOM.getSearchResults", json.RawMessage(fmt.Sprintf(`{"searchId":%q,"fromIndex":0,"toIndex":1}`, searchID)))
				if err != nil {
					t.Fatalf("DOM.getSearchResults: %v", err)
				}
				ids := results["nodeIds"].([]int)
				if len(ids) != 1 || ids[0] != domNodeID(2) {
					t.Fatalf("nodeIds = %v, want [%d]", ids, domNodeID(2))
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.dispatchCDP(nil, nil, "", tt.method, tt.params)
			if err != nil {
				t.Fatalf("%s: %v", tt.method, err)
			}
			tt.check(t, got)
		})
	}
}

func TestWindowTargetURLAndID(t *testing.T) {
	const (
		pid      = 1268
		windowID = 117
	)
	id := windowTargetID(pid, windowID)
	if id == "" || id == appTargetID("page", pid) {
		t.Fatalf("windowTargetID(%d, %d) = %q, want distinct target id", pid, windowID, id)
	}
	url := axcdpWindowPreviewURL("127.0.0.1:9221", pid, windowID)
	if url != "http://127.0.0.1:9221/axcdp/window/1268/117" {
		t.Fatalf("window URL = %q", url)
	}
	gotPID, gotWindowID, ok := previewPathWindow("/axcdp/window/1268/117")
	if !ok || gotPID != pid || gotWindowID != windowID {
		t.Fatalf("previewPathWindow = %d, %d, %v", gotPID, gotWindowID, ok)
	}
	s := (&cdpServer{addr: ":9221"}).serverForWindow(pid, windowID, "page")
	if got := s.currentTargetID(); got != id {
		t.Fatalf("currentTargetID = %q, want %q", got, id)
	}
	if got := s.previewURL("127.0.0.1:9221"); got != url {
		t.Fatalf("previewURL = %q, want %q", got, url)
	}
	if got := s.targetURL(); got != "file://axcdp/window/1268/117" {
		t.Fatalf("targetURL = %q", got)
	}
}

func TestCDPUnknownMethodReturnsError(t *testing.T) {
	s := &cdpServer{}
	if _, err := s.dispatchCDP(nil, nil, "", "Nope.nope", nil); err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("dispatch unknown error = %v, want method not found", err)
	}
	if _, err := s.dispatchCDP(nil, nil, "", "Network.getResponseBody", json.RawMessage(`{"requestId":"x"}`)); err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("dispatch unbacked web domain error = %v, want method not found", err)
	}
	if _, err := s.dispatchCDP(nil, nil, "", "Log.enable", nil); err != nil {
		t.Fatalf("Log.enable compatibility control should be accepted: %v", err)
	}
	if _, err := s.dispatchCDP(nil, nil, "", "Network.enable", nil); err != nil {
		t.Fatalf("Network.enable compatibility control should be accepted: %v", err)
	}
	if _, err := s.dispatchCDP(nil, nil, "", "Inspector.enable", nil); err != nil {
		t.Fatalf("Inspector.enable compatibility control should be accepted: %v", err)
	}
	if _, err := s.dispatchCDP(nil, nil, "", "CSS.enable", nil); err != nil {
		t.Fatalf("CSS.enable compatibility control should be accepted: %v", err)
	}
}

func TestCDPRejectsUnadvertisedSameDomainMethods(t *testing.T) {
	s := &cdpServer{}
	for _, method := range []string{
		"Browser.setWindowBounds",
		"Target.createTarget",
		"Target.closeTarget",
		"Page.reload",
		"Browser.close",
		"Page.createIsolatedWorld",
		"Runtime.awaitPromise",
		"Runtime.runScript",
		"Runtime.queryObjects",
		"Overlay.setInspectMode",
		"Input.insertText",
	} {
		t.Run(method, func(t *testing.T) {
			if _, err := s.dispatchCDP(nil, nil, "", method, nil); err == nil || !strings.Contains(err.Error(), "method not found") {
				t.Fatalf("%s error = %v, want method not found", method, err)
			}
		})
	}
}

func TestCDPDispatchKeyEventValidatesPayload(t *testing.T) {
	s := &cdpServer{}
	_, err := s.dispatchCDP(nil, nil, "", "Input.dispatchKeyEvent", json.RawMessage(`{"type":"keyDown"}`))
	if err == nil || !strings.Contains(err.Error(), "key event requires") {
		t.Fatalf("Input.dispatchKeyEvent error = %v, want key event requires", err)
	}
	_, err = s.dispatchCDP(nil, nil, "", "Input.dispatchKeyEvent", json.RawMessage(`{"type":"bogus","text":"a"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported key event type") {
		t.Fatalf("Input.dispatchKeyEvent error = %v, want unsupported key event type", err)
	}
}

func TestCDPSetAttributeValueValidatesAXMapping(t *testing.T) {
	s := &cdpServer{}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXButton", LocalName: "button", Role: "AXButton"}
	s.nodes = map[int]*cdpNode{1: s.root}
	_, err := s.dispatchCDP(nil, nil, "", "DOM.setAttributeValue", json.RawMessage(fmt.Sprintf(`{"nodeId":%d,"name":"class","value":"primary"}`, domNodeID(1))))
	if err == nil || !strings.Contains(err.Error(), "unsupported AX-backed DOM attribute") {
		t.Fatalf("DOM.setAttributeValue error = %v, want unsupported AX-backed DOM attribute", err)
	}
	_, err = s.dispatchCDP(nil, nil, "", "DOM.setAttributeValue", json.RawMessage(fmt.Sprintf(`{"nodeId":%d,"name":"title","value":"Save"}`, domNodeID(1))))
	if err == nil || !strings.Contains(err.Error(), "node has no AX ref") {
		t.Fatalf("DOM.setAttributeValue error = %v, want node has no AX ref", err)
	}
}

func TestCDPGetFrameOwnerRejectsUnknownFrame(t *testing.T) {
	s := &cdpServer{}
	_, err := s.dispatchCDP(nil, nil, "", "DOM.getFrameOwner", json.RawMessage(`{"frameId":"missing"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown frameId") {
		t.Fatalf("DOM.getFrameOwner error = %v, want unknown frameId", err)
	}
}

func TestCDPRequestNodeRejectsUnsupportedObject(t *testing.T) {
	s := &cdpServer{}
	_, err := s.dispatchCDP(nil, nil, "", "DOM.requestNode", json.RawMessage(`{"objectId":"bounds:1"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported objectId") {
		t.Fatalf("DOM.requestNode error = %v, want unsupported objectId", err)
	}
}

func TestCDPUnknownMethodUsesJSONRPCMethodNotFoundCode(t *testing.T) {
	resp := cdpErrorFromError(cdpMethodNotFoundError{Method: "Nope.nope"})
	if resp.Code != -32601 {
		t.Fatalf("code = %d, want -32601", resp.Code)
	}
	resp = cdpErrorFromError(fmt.Errorf("connect ax: failed"))
	if resp.Code != -32000 {
		t.Fatalf("code = %d, want -32000", resp.Code)
	}
}

func TestCDPStartScreencastSendsFrame(t *testing.T) {
	s := &cdpServer{}
	s.root = &cdpNode{NodeID: 1, BackendID: 1, NodeName: "AXWindow", LocalName: "window", Role: "AXWindow", Bounds: axRect{Width: 33, Height: 44}}
	s.nodes = map[int]*cdpNode{1: s.root}
	s.backend = map[int]*cdpNode{1: s.root}
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(cdpMessage{ID: 1, Method: "Page.startScreencast", SessionID: "s1"}); err != nil {
		t.Fatalf("startScreencast write: %v", err)
	}
	visible := readUntilEvent(t, conn, "Page.screencastVisibilityChanged")
	if visible.SessionID != "s1" || visible.Params["visible"] != true {
		t.Fatalf("visibility event = %#v, want visible true for session", visible)
	}
	resp := readUntilResponse(t, conn, 1)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	frame := readUntilEvent(t, conn, "Page.screencastFrame")
	if frame.SessionID != "s1" {
		t.Fatalf("sessionId = %q, want s1", frame.SessionID)
	}
	if frame.Params["data"] == "" {
		t.Fatal("screencast frame data is empty")
	}
	if frame.Params["sessionId"] != float64(1) {
		t.Fatalf("frame sessionId = %v, want 1", frame.Params["sessionId"])
	}
	metadata := frame.Params["metadata"].(map[string]any)
	if metadata["deviceWidth"] != float64(33) || metadata["deviceHeight"] != float64(44) {
		t.Fatalf("metadata = %#v, want AX viewport dimensions", metadata)
	}
	if err := conn.WriteJSON(cdpMessage{ID: 2, Method: "Page.stopScreencast", SessionID: "s1"}); err != nil {
		t.Fatalf("stopScreencast write: %v", err)
	}
	hidden := readUntilEvent(t, conn, "Page.screencastVisibilityChanged")
	if hidden.SessionID != "s1" || hidden.Params["visible"] != false {
		t.Fatalf("visibility event = %#v, want visible false for session", hidden)
	}
	resp = readUntilResponse(t, conn, 2)
	if resp.Error != nil {
		t.Fatalf("unexpected stop error: %+v", resp.Error)
	}
}

func TestCDPDOMEnableSendsDocumentUpdated(t *testing.T) {
	s := &cdpServer{}
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	writeCDP(t, conn, cdpMessage{ID: 1, Method: "Target.setDiscoverTargets", Params: json.RawMessage(`{"discover":true}`)})
	readUntilEvent(t, conn, "Target.targetCreated")
	if resp := readUntilResponse(t, conn, 1); resp.Error != nil {
		t.Fatalf("Target.setDiscoverTargets error: %+v", resp.Error)
	}

	if err := conn.WriteJSON(cdpMessage{ID: 2, Method: "DOM.enable", SessionID: "s1"}); err != nil {
		t.Fatalf("DOM.enable write: %v", err)
	}
	updated := readUntilEvent(t, conn, "DOM.documentUpdated")
	if updated.SessionID != "s1" {
		t.Fatalf("sessionId = %q, want s1", updated.SessionID)
	}
	resp := readUntilResponse(t, conn, 2)
	if resp.Error != nil {
		t.Fatalf("DOM.enable error: %+v", resp.Error)
	}
}

func TestCDPViewportUsesLargestBoundedNodeWhenNoWindow(t *testing.T) {
	s := &cdpServer{}
	s.root = &cdpNode{
		NodeID:    1,
		BackendID: 1,
		NodeName:  "AXApplication",
		LocalName: "application",
		Role:      "AXApplication",
		Children: []*cdpNode{
			{NodeID: 2, BackendID: 2, NodeName: "AXMenuBar", LocalName: "menubar", Role: "AXMenuBar", Bounds: axRect{X: 1, Y: 2, Width: 200, Height: 20}},
			{NodeID: 3, BackendID: 3, NodeName: "AXGroup", LocalName: "group", Role: "AXGroup", Bounds: axRect{X: 10, Y: 20, Width: 90, Height: 80}},
		},
	}

	got := s.viewportBounds()
	if got.X != 10 || got.Y != 20 || got.Width != 90 || got.Height != 80 {
		t.Fatalf("viewportBounds = %#v, want largest bounded AX subtree", got)
	}
}

func readUntilResponse(t *testing.T, conn *websocket.Conn, id int64) cdpResponse {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var raw map[string]json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			t.Fatalf("read message: %v", err)
		}
		if _, ok := raw["id"]; !ok {
			continue
		}
		var resp cdpResponse
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ID == id {
			return resp
		}
	}
}

func readUntilEvent(t *testing.T, conn *websocket.Conn, method string) cdpEvent {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var raw map[string]json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			t.Fatalf("read message: %v", err)
		}
		if _, ok := raw["id"]; ok {
			continue
		}
		var event cdpEvent
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if event.Method == method {
			return event
		}
	}
}

func stringPairsContain(values []string, key, value string) bool {
	for i := 0; i+1 < len(values); i += 2 {
		if values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}

func runtimePropertiesContain(props []any, name, value string) bool {
	for _, prop := range props {
		m, ok := prop.(map[string]any)
		if !ok || m["name"] != name {
			continue
		}
		v, ok := m["value"].(map[string]any)
		if ok && v["value"] == value {
			return true
		}
	}
	return false
}

func runtimeNumberPropertiesContain(props []any, name string, value float64) bool {
	for _, prop := range props {
		m, ok := prop.(map[string]any)
		if !ok || m["name"] != name {
			continue
		}
		v, ok := m["value"].(map[string]any)
		if !ok {
			continue
		}
		switch got := v["value"].(type) {
		case float64:
			return got == value
		case int:
			return float64(got) == value
		}
	}
	return false
}

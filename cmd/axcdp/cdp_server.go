package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unsafe"

	"github.com/dop251/goja"
	"github.com/gorilla/websocket"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ocrwindow"
	"github.com/tmc/axmcp/internal/ui/permissions"
	xdraw "golang.org/x/image/draw"
)

const (
	cdpTargetID          = "F0000000000000000000000000A11CDC"
	cdpNodeTargetID      = "E0000000000000000000000000A11CDC"
	cdpBrowserID         = "85010ad8-e7dd-476f-ada8-14852b385a9e"
	cdpFrameID           = "axcdp-frame-1"
	cdpDocumentNodeID    = 1
	cdpDocumentBackendID = 1
	cdpAXNodeOffset      = 10
	browserProduct       = "Chrome/120.0.0.0"
	browserRevision      = "387bd2e6c197bcf842c162506c1de117af0513fb"
	nodeProduct          = "node.js/axcdp"
	nodeVersion          = "v24.0.0-axcdp"
)

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpMethodNotFoundError struct {
	Method string
}

func (e cdpMethodNotFoundError) Error() string {
	return "method not found: " + e.Method
}

type cdpResponse struct {
	ID        int64          `json:"id"`
	Result    map[string]any `json:"result"`
	Error     *cdpError      `json:"error,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type cdpEvent struct {
	Method    string         `json:"method"`
	Params    map[string]any `json:"params,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type cdpServer struct {
	addr       string
	appArg     string
	targetType string
	windowID   uint32
	windowName string
	castMaxDim int
	staticList bool

	mu       sync.Mutex
	writeMu  *sync.Mutex
	nextID   int
	root     *cdpNode
	nodes    map[int]*cdpNode
	backend  map[int]*cdpNode
	sessions map[string]*cdpServer
	casts    map[string]chan struct{}
	castAcks map[string]chan int
	domWatch map[string]chan struct{}
	searches map[string][]int

	browser *browserBackend
}

var nextCDPConnID atomic.Uint64

type cdpConnIDContextKey struct{}

func cdpConnID(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(cdpConnIDContextKey{}).(uint64)
	return id
}

type cdpNode struct {
	NodeID        int
	BackendID     int
	ParentID      int
	NodeName      string
	LocalName     string
	NodeValue     string
	Role          string
	Title         string
	Description   string
	Identifier    string
	Bounds        axRect
	Ref           axuiautomation.AXUIElementRef
	Children      []*cdpNode
	ChildCount    int
	ChildrenReady bool
}

type axRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func runCDPServer(addr, appArg, browserEndpoint string, screencastMaxDim int) error {
	s := &cdpServer{
		addr:       addr,
		appArg:     appArg,
		castMaxDim: screencastMaxDim,
		nodes:      make(map[int]*cdpNode),
		backend:    make(map[int]*cdpNode),
		writeMu:    new(sync.Mutex),
		sessions:   make(map[string]*cdpServer),
		casts:      make(map[string]chan struct{}),
		castAcks:   make(map[string]chan int),
		domWatch:   make(map[string]chan struct{}),
		searches:   make(map[string][]int),
	}
	if browserEndpoint != "" {
		backend, err := newBrowserBackend(browserEndpoint)
		if err != nil {
			return err
		}
		s.browser = backend
	}
	if err := s.refreshTree(); err != nil {
		return err
	}
	mux := s.mux()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	slog.Info("axcdp listening", "url", "http://"+ln.Addr().String(), "app", appArg, "browser_cdp", browserEndpoint, "screencast_max_dim", screencastMaxDim)
	return http.Serve(ln, mux)
}

func (s *cdpServer) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", s.handleVersion)
	mux.HandleFunc("/json/list", s.handleList)
	mux.HandleFunc("/json/new", s.handleNewTarget)
	mux.HandleFunc("/json/activate/", s.handleActivateTarget)
	mux.HandleFunc("/json/close/", s.handleCloseTarget)
	mux.HandleFunc("/json", s.handleList)
	mux.HandleFunc("/json/protocol", s.handleProtocol)
	mux.HandleFunc("/json/coverage", s.handleCoverage)
	mux.HandleFunc("/axcdp/", s.handleAXCDPPage)
	mux.HandleFunc("/devtools/browser/"+cdpBrowserID, s.handleWS)
	mux.HandleFunc("/devtools/page/"+cdpTargetID, s.handleWS)
	mux.HandleFunc("/devtools/node/"+cdpNodeTargetID, s.handleWS)
	mux.HandleFunc("/devtools/browser-proxy/", s.handleBrowserProxyWS)
	mux.HandleFunc("/devtools/page/", s.handleMaybeBrowserProxyWS)
	mux.HandleFunc("/devtools/page/app-", s.handleWS)
	mux.HandleFunc("/devtools/node/app-", s.handleWS)
	mux.HandleFunc("/", s.handleRoot)
	return mux
}

func (s *cdpServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if websocket.IsWebSocketUpgrade(r) {
		s.handleWS(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *cdpServer) handleBrowserProxyWS(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/devtools/browser-proxy/")
	target, err := s.browser.targetByProxyID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.browser.proxyWebSocket(w, r, target)
}

func (s *cdpServer) handleMaybeBrowserProxyWS(w http.ResponseWriter, r *http.Request) {
	if s.browser != nil {
		id := path.Base(r.URL.Path)
		if target, err := s.browser.targetByProxyID(id); err == nil {
			s.browser.proxyWebSocket(w, r, target)
			return
		}
	}
	s.handleWS(w, r)
}

func (s *cdpServer) handleAXCDPPage(w http.ResponseWriter, r *http.Request) {
	slog.Debug("serve preview page", "path", r.URL.Path, "remote", r.RemoteAddr)
	if strings.HasSuffix(r.URL.Path, "/screenshot.png") {
		s.handleAXCDPScreenshot(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title := s.previewTitle(r.URL.Path)
	target := s.serverForPreviewPath(r.URL.Path)
	initial := target.captureTargetPNGBounded(1500 * time.Millisecond)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<title>%s</title>
<style>
html,body{margin:0;width:100%%;height:100%%;background:#282828;overflow:hidden}
img{display:block;width:100vw;height:100vh;object-fit:contain}
</style>
<img id="frame" alt="%s" src="data:image/png;base64,%s">
<script>
const img = document.getElementById("frame");
function refresh() {
  img.src = location.pathname.replace(/\/$/, "") + "/screenshot.png?t=" + Date.now();
}
img.onload = () => setTimeout(refresh, 750);
img.onerror = () => setTimeout(refresh, 1000);
setTimeout(refresh, 750);
</script>`, html.EscapeString(title), html.EscapeString(title), initial)
}

func (s *cdpServer) handleAXCDPScreenshot(w http.ResponseWriter, r *http.Request) {
	target := s.serverForPreviewPath(strings.TrimSuffix(r.URL.Path, "/screenshot.png"))
	start := time.Now()
	data, err := base64.StdEncoding.DecodeString(target.captureTargetPNGBounded(1500 * time.Millisecond))
	if err != nil {
		slog.Warn("preview screenshot failed", "path", r.URL.Path, "target", target.appArg, "err", err)
		http.Error(w, "capture screenshot", http.StatusInternalServerError)
		return
	}
	slog.Debug("preview screenshot served", "path", r.URL.Path, "target", target.appArg, "bytes", len(data), "duration", time.Since(start))
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *cdpServer) previewTitle(p string) string {
	if pid, windowID, ok := previewPathWindow(p); ok {
		if win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), windowID); err == nil {
			return windowTargetTitle(runningApp{PID: pid, Name: appNameForPID(pid)}, win)
		}
		return "window " + strconv.FormatUint(uint64(windowID), 10)
	}
	if pid, ok := previewPathPID(p); ok {
		if name := appNameForPID(pid); name != "" {
			return name
		}
		return "pid " + strconv.Itoa(pid)
	}
	return "macOS Accessibility"
}

func (s *cdpServer) serverForPreviewPath(p string) *cdpServer {
	if pid, windowID, ok := previewPathWindow(p); ok {
		return s.serverForWindow(pid, windowID, "page")
	}
	if pid, ok := previewPathPID(p); ok {
		return s.serverForAppArg(strconv.Itoa(pid), "page")
	}
	return s.serverForAppArg("", "page")
}

func previewPathPID(p string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSuffix(p, "/"), "/axcdp/app/")
	if !ok || rest == "" {
		return 0, false
	}
	pid, err := strconv.Atoi(path.Base(rest))
	return pid, err == nil && pid > 0
}

func previewPathWindow(p string) (int, uint32, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSuffix(p, "/"), "/axcdp/window/")
	if !ok || rest == "" {
		return 0, 0, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return 0, 0, false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return 0, 0, false
	}
	id, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || id == 0 {
		return 0, 0, false
	}
	return pid, uint32(id), true
}

func (s *cdpServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = s.addr
	}
	writeHTTPJSON(w, map[string]any{
		"Browser":              browserProduct,
		"Protocol-Version":     "1.3",
		"User-Agent":           "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"V8-Version":           "12.0.0",
		"WebKit-Version":       "537.36 (@" + browserRevision + ")",
		"webSocketDebuggerUrl": "ws://" + host + "/devtools/browser/" + cdpBrowserID,
	})
}

func (s *cdpServer) handleList(w http.ResponseWriter, r *http.Request) {
	targets := []map[string]any{s.target(r)}
	if s.appArg == "" {
		targets = append(targets, s.nodeTarget(r))
		if s.browser != nil {
			if browserTargets, err := s.browser.targets(r); err == nil {
				targets = append(targets, browserTargets...)
			}
		}
		if !s.staticList {
			for _, app := range runningApps() {
				windows := appWindows(app)
				for _, win := range windows {
					targets = append(targets, s.windowTarget(r, app, win))
				}
			}
		}
	}
	if _, ok := r.URL.Query()["for_tab"]; ok {
		entries := targets
		targets = make([]map[string]any, 0, len(entries))
		for _, target := range entries {
			if target["type"] != "page" {
				continue
			}
			targets = append(targets, target)
		}
	}
	writeHTTPJSON(w, targets)
}

func (s *cdpServer) handleNewTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.browser != nil {
		result, err := s.browser.createTarget(json.RawMessage(fmt.Sprintf(`{"url":%q}`, firstNonEmpty(r.URL.RawQuery, "about:blank"))))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		id, _ := result["targetId"].(string)
		target, err := s.browser.targetByProxyID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeHTTPJSON(w, s.browser.targetForHTTP(r, id, target))
		return
	}
	http.Error(w, "new targets are not AX-backed", http.StatusNotImplemented)
}

func (s *cdpServer) handleActivateTarget(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/json/activate/")
	if err := s.serverForTargetID(id).activateTarget(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Target activated\n"))
}

func (s *cdpServer) handleCloseTarget(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/json/close/")
	if s.browser != nil {
		if _, err := s.browser.closeTarget(json.RawMessage(fmt.Sprintf(`{"targetId":%q}`, id))); err == nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("Target is closing\n"))
			return
		}
	}
	http.Error(w, "closing AX targets is not supported", http.StatusNotImplemented)
}

func tabTargetID(id string) string {
	return hexTargetID("tab", id)
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *cdpServer) target(r *http.Request) map[string]any {
	id := s.currentTargetID()
	wsPath := "/devtools/page/" + id
	host := firstNonEmpty(r.Host, s.httpHost())
	return map[string]any{
		"id":                   id,
		"type":                 "page",
		"title":                s.targetTitle(),
		"description":          "",
		"url":                  s.previewURL(host),
		"canScreencast":        true,
		"webSocketDebuggerUrl": s.wsURL(r, wsPath),
		"devtoolsFrontendUrl":  devtoolsFrontendURL(r.Host, wsPath),
	}
}

func (s *cdpServer) nodeTarget(r *http.Request) map[string]any {
	host := r.Host
	if host == "" {
		host = s.addr
	}
	ws := "ws://" + host + "/devtools/node/" + cdpNodeTargetID
	return map[string]any{
		"id":                   cdpNodeTargetID,
		"type":                 "node",
		"title":                "macOS Accessibility",
		"description":          nodeProduct,
		"url":                  axcdpPreviewURL(host, ""),
		"faviconUrl":           "https://nodejs.org/static/images/favicons/favicon.ico",
		"webSocketDebuggerUrl": ws,
		"devtoolsFrontendUrl":  devtoolsFrontendURL(host, "/devtools/node/"+cdpNodeTargetID),
	}
}

type runningApp struct {
	PID  int
	Name string
}

func (s *cdpServer) appTarget(r *http.Request, app runningApp, typ string) map[string]any {
	host := r.Host
	if host == "" {
		host = s.addr
	}
	id := appTargetID(typ, app.PID)
	wsPath := "/devtools/" + typ + "/" + id
	url := axcdpPreviewURL(host, strconv.Itoa(app.PID))
	out := map[string]any{
		"id":                   id,
		"type":                 typ,
		"title":                app.Name,
		"description":          "",
		"url":                  url,
		"canScreencast":        typ == "page",
		"webSocketDebuggerUrl": "ws://" + host + wsPath,
		"devtoolsFrontendUrl":  devtoolsFrontendURL(host, wsPath),
	}
	if typ == "node" {
		out["description"] = nodeProduct
		out["faviconUrl"] = "https://nodejs.org/static/images/favicons/favicon.ico"
	}
	return out
}

func (s *cdpServer) windowTarget(r *http.Request, app runningApp, win ocrwindow.Window) map[string]any {
	host := r.Host
	if host == "" {
		host = s.addr
	}
	id := windowTargetID(app.PID, win.ID)
	wsPath := "/devtools/page/" + id
	title := windowTargetTitle(app, win)
	return map[string]any{
		"id":                   id,
		"type":                 "page",
		"title":                title,
		"description":          app.Name,
		"url":                  axcdpWindowPreviewURL(host, app.PID, win.ID),
		"canScreencast":        true,
		"webSocketDebuggerUrl": "ws://" + host + wsPath,
		"devtoolsFrontendUrl":  devtoolsFrontendURL(host, wsPath),
	}
}

func devtoolsFrontendURL(host, wsPath string) string {
	return "https://chrome-devtools-frontend.appspot.com/serve_rev/@" + browserRevision + "/inspector.html?ws=" + host + wsPath
}

func appTargetID(typ string, pid int) string {
	return hexTargetID(typ, strconv.Itoa(pid))
}

func windowTargetID(pid int, id uint32) string {
	return hexTargetID("window", strconv.Itoa(pid), strconv.FormatUint(uint64(id), 10))
}

func hexTargetID(parts ...string) string {
	h := md5.New()
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return strings.ToUpper(fmt.Sprintf("%x", h.Sum(nil)))
}

func (s *cdpServer) wsURL(r *http.Request, path string) string {
	host := r.Host
	if host == "" {
		host = s.addr
	}
	return "ws://" + host + path
}

type browserBackend struct {
	base  *url.URL
	mu    sync.Mutex
	cache map[string]browserTarget
}

type browserTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	Description          string `json:"description"`
	URL                  string `json:"url"`
	FaviconURL           string `json:"faviconUrl"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	DevToolsFrontendURL  string `json:"devtoolsFrontendUrl"`
}

func newBrowserBackend(endpoint string) (*browserBackend, error) {
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse browser endpoint: %w", err)
	}
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	if base.Host == "" {
		return nil, fmt.Errorf("browser endpoint must include host")
	}
	return &browserBackend{base: base, cache: make(map[string]browserTarget)}, nil
}

func (b *browserBackend) targets(r *http.Request) ([]map[string]any, error) {
	upstream, err := b.fetchTargets()
	if err != nil {
		return nil, err
	}
	targets := make([]map[string]any, 0, len(upstream))
	host := r.Host
	for _, target := range upstream {
		if target.WebSocketDebuggerURL == "" {
			continue
		}
		id := browserProxyID(target.WebSocketDebuggerURL)
		b.mu.Lock()
		b.cache[id] = target
		b.mu.Unlock()
		targets = append(targets, b.targetMap(host, id, target))
	}
	return targets, nil
}

func (b *browserBackend) targetForHTTP(r *http.Request, id string, target browserTarget) map[string]any {
	host := r.Host
	return b.targetMap(host, id, target)
}

func (b *browserBackend) targetMap(host, id string, target browserTarget) map[string]any {
	wsPath := "/devtools/browser-proxy/" + id
	out := map[string]any{
		"id":                   id,
		"type":                 firstNonEmpty(target.Type, "page"),
		"title":                target.Title,
		"description":          target.Description,
		"url":                  target.URL,
		"webSocketDebuggerUrl": "ws://" + host + wsPath,
		"devtoolsFrontendUrl":  devtoolsFrontendURL(host, wsPath),
		"browserTargetId":      target.ID,
		"browserBackend":       b.base.String(),
	}
	if target.FaviconURL != "" {
		out["faviconUrl"] = target.FaviconURL
	}
	return out
}

func (b *browserBackend) targetByProxyID(id string) (browserTarget, error) {
	b.mu.Lock()
	if target, ok := b.cache[id]; ok {
		b.mu.Unlock()
		return target, nil
	}
	b.mu.Unlock()
	targets, err := b.fetchTargets()
	if err != nil {
		return browserTarget{}, err
	}
	for _, target := range targets {
		if browserProxyID(target.WebSocketDebuggerURL) == id {
			return target, nil
		}
	}
	return browserTarget{}, fmt.Errorf("unknown browser proxy target %q", id)
}

func (b *browserBackend) targetInfos(attached bool) []map[string]any {
	targets, err := b.fetchTargets()
	if err != nil {
		return nil
	}
	infos := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.WebSocketDebuggerURL == "" {
			continue
		}
		infos = append(infos, map[string]any{
			"targetId":        browserProxyID(target.WebSocketDebuggerURL),
			"type":            firstNonEmpty(target.Type, "page"),
			"title":           target.Title,
			"url":             target.URL,
			"attached":        attached,
			"canAccessOpener": false,
		})
	}
	return infos
}

func (b *browserBackend) fetchTargets() ([]browserTarget, error) {
	u := *b.base
	u.Path = "/json/list"
	u.RawQuery = ""
	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u.String(), resp.Status)
	}
	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("decode %s: %w", u.String(), err)
	}
	return targets, nil
}

func (b *browserBackend) createTarget(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.URL == "" {
		p.URL = "about:blank"
	}
	u := *b.base
	u.Path = "/json/new"
	u.RawQuery = p.URL
	req, err := http.NewRequest(http.MethodPut, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create browser target: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create browser target: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create browser target: %s", resp.Status)
	}
	var target browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return nil, fmt.Errorf("decode browser target: %w", err)
	}
	if target.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("browser target has no websocket URL")
	}
	id := browserProxyID(target.WebSocketDebuggerURL)
	b.mu.Lock()
	b.cache[id] = target
	b.mu.Unlock()
	return map[string]any{"targetId": id}, nil
}

func (b *browserBackend) closeTarget(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(raw, &p)
	target, err := b.targetByProxyID(p.TargetID)
	if err != nil {
		return nil, err
	}
	u := *b.base
	u.Path = "/json/close/" + target.ID
	u.RawQuery = ""
	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("close browser target: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("close browser target: %s", resp.Status)
	}
	return map[string]any{"success": true}, nil
}

func (b *browserBackend) proxyWebSocket(w http.ResponseWriter, r *http.Request, target browserTarget) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()

	upstream, _, err := websocket.DefaultDialer.Dial(target.WebSocketDebuggerURL, nil)
	if err != nil {
		_ = client.WriteJSON(cdpResponse{Error: &cdpError{Code: -32000, Message: fmt.Sprintf("dial browser target: %v", err)}})
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	copyConn := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
	go copyConn(upstream, client)
	go copyConn(client, upstream)
	<-done
}

func browserProxyID(wsURL string) string {
	return hexTargetID("browser", wsURL)
}

func (s *cdpServer) handleProtocol(w http.ResponseWriter, r *http.Request) {
	writeHTTPJSON(w, map[string]any{
		"version": map[string]string{"major": "1", "minor": "3"},
		"domains": cdpProtocolDomains(),
	})
}

func (s *cdpServer) handleCoverage(w http.ResponseWriter, r *http.Request) {
	writeHTTPJSON(w, map[string]any{
		"policy":      "advertise only methods backed by macOS Accessibility, screen capture, or harmless DevTools setup controls",
		"entries":     cdpCoverageEntries(),
		"unsupported": cdpUnsupportedMethods(),
	})
}

type cdpCoverageEntry struct {
	Method     string `json:"method"`
	Status     string `json:"status"`
	Backend    string `json:"backend"`
	Advertised bool   `json:"advertised"`
	Notes      string `json:"notes,omitempty"`
}

type cdpUnsupportedMethod struct {
	Method string `json:"method"`
	Reason string `json:"reason"`
}

func cdpCoverageEntries() []cdpCoverageEntry {
	var entries []cdpCoverageEntry
	add := func(method, status, backend, notes string, advertised bool) {
		entries = append(entries, cdpCoverageEntry{Method: method, Status: status, Backend: backend, Advertised: advertised, Notes: notes})
	}
	for _, method := range []string{"Browser.getWindowForTarget", "Browser.getWindowBounds", "Browser.bringToFront"} {
		add(method, "supported", "AX", "uses AX target metadata or System Events activation", true)
	}
	add("Browser.getVersion", "supported", "compat", "returns stable DevTools-compatible product metadata", true)
	for _, method := range []string{"Target.setDiscoverTargets", "Target.getTargets", "Target.attachToTarget", "Target.attachToBrowserTarget", "Target.detachFromTarget", "Target.sendMessageToTarget", "Target.getTargetInfo", "Target.activateTarget"} {
		add(method, "supported", "AX target registry", "maps CDP targets to AX roots", true)
	}
	for _, method := range []string{"Target.setAutoAttach", "Target.setAttachToFrames", "Target.setRemoteLocations"} {
		add(method, "setup-control", "compat", "accepted as harmless DevTools setup state", true)
	}
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree", "Page.getLayoutMetrics", "Page.getNavigationHistory", "Page.bringToFront"} {
		add(method, "supported", "AX metadata", "reports the synthetic AX document frame for the selected target", true)
	}
	add("Page.navigate", "supported", "AX target registry", "retargets only axcdp preview URLs for AX app/window targets; arbitrary browser navigation is unsupported", true)
	for _, method := range []string{"Page.captureScreenshot", "Page.startScreencast"} {
		add(method, "supported", "screencapture", "captures real screen pixels for the AX viewport", true)
	}
	for _, method := range []string{"Page.enable", "Page.screencastFrameAck", "Page.stopScreencast", "Page.setLifecycleEventsEnabled"} {
		add(method, "setup-control", "compat", "accepted for DevTools frontend lifecycle", true)
	}
	for _, method := range []string{"Runtime.evaluate", "Runtime.getProperties"} {
		add(method, "supported", "AX metadata", "supports primitive expressions and AX node object inspection, not a JavaScript VM", true)
	}
	for _, method := range []string{"Runtime.enable", "Runtime.runIfWaitingForDebugger", "Runtime.discardConsoleEntries", "Runtime.releaseObject", "Runtime.releaseObjectGroup"} {
		add(method, "setup-control", "compat", "accepted for DevTools frontend lifecycle", true)
	}
	add("Schema.getDomains", "supported", "protocol", "returns advertised domain names", true)
	for _, method := range []string{"DOM.getDocument", "DOM.getFlattenedDocument", "DOM.requestChildNodes", "DOM.describeNode", "DOM.resolveNode", "DOM.requestNode", "DOM.pushNodesByBackendIdsToFrontend", "DOM.querySelector", "DOM.querySelectorAll", "DOM.performSearch", "DOM.getSearchResults", "DOM.discardSearchResults", "DOM.getOuterHTML", "DOM.getAttributes", "DOM.setAttributeValue", "DOM.getBoxModel", "DOM.getContentQuads", "DOM.getNodeForLocation", "DOM.getFrameOwner", "DOM.setInspectedNode", "DOM.focus", "DOM.scrollIntoViewIfNeeded"} {
		add(method, "supported", "AX tree", "maps AX elements into a DOM-shaped tree", true)
	}
	for _, method := range []string{"DOM.enable", "Overlay.enable", "Accessibility.enable"} {
		add(method, "setup-control", "compat", "accepted for DevTools frontend setup", true)
	}
	for _, method := range []string{"Overlay.highlightNode", "Overlay.highlightRect", "Overlay.highlightQuad", "Overlay.hideHighlight"} {
		add(method, "supported", "AX overlay", "draws or clears a native overlay on real screen coordinates", true)
	}
	for _, method := range []string{"Input.dispatchMouseEvent", "Input.dispatchKeyEvent"} {
		add(method, "supported", "AX input", "dispatches mouse or keyboard actions through AX-backed macOS APIs", true)
	}
	for _, method := range []string{"Accessibility.getFullAXTree", "Accessibility.getPartialAXTree", "Accessibility.getRootAXNode", "Accessibility.getAXNodeAndAncestors", "Accessibility.getChildAXNodes", "Accessibility.queryAXTree"} {
		add(method, "supported", "AX tree", "returns Accessibility-domain nodes derived from the AX tree", true)
	}
	for _, name := range axCommandNames() {
		add("AX."+name, "supported", "AX pass-through", "direct macOS Accessibility API wrapper", true)
	}
	for _, method := range cdpCompatSetupMethods() {
		add(method, "setup-control", "compat", "accepted for DevTools frontend setup; does not report browser runtime data", false)
	}
	return entries
}

func cdpUnsupportedMethods() []cdpUnsupportedMethod {
	reason := "not backed by macOS Accessibility; returning synthetic data would be misleading"
	methods := []string{
		"Browser.close",
		"Browser.setWindowBounds",
		"Target.createTarget",
		"Target.closeTarget",
		"Page.reload",
		"Page.createIsolatedWorld",
		"Runtime.awaitPromise",
		"Runtime.runScript",
		"Runtime.queryObjects",
		"Network.getResponseBody",
		"Storage.getStorageKeyForFrame",
		"IndexedDB.requestDatabaseNames",
		"CacheStorage.requestCacheNames",
		"Overlay.setInspectMode",
		"Input.insertText",
	}
	out := make([]cdpUnsupportedMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, cdpUnsupportedMethod{Method: method, Reason: reason})
	}
	return out
}

func cdpProtocolDomains() []map[string]any {
	specs := []struct {
		domain   string
		commands []string
		events   []string
	}{
		{"Browser", []string{"getVersion", "getWindowForTarget", "getWindowBounds", "bringToFront"}, nil},
		{"Target", []string{"setDiscoverTargets", "getTargets", "attachToTarget", "attachToBrowserTarget", "detachFromTarget", "sendMessageToTarget", "getTargetInfo", "setAutoAttach", "setAttachToFrames", "activateTarget", "setRemoteLocations"}, []string{"targetCreated", "attachedToTarget", "detachedFromTarget", "receivedMessageFromTarget"}},
		{"Page", []string{"enable", "getFrameTree", "getResourceTree", "getLayoutMetrics", "getNavigationHistory", "navigate", "bringToFront", "captureScreenshot", "startScreencast", "screencastFrameAck", "stopScreencast", "setLifecycleEventsEnabled"}, []string{"frameNavigated", "domContentEventFired", "loadEventFired", "screencastFrame", "screencastVisibilityChanged"}},
		{"Runtime", []string{"enable", "runIfWaitingForDebugger", "discardConsoleEntries", "evaluate", "getProperties", "releaseObject", "releaseObjectGroup"}, []string{"executionContextCreated"}},
		{"Schema", []string{"getDomains"}, nil},
		{"DOM", []string{"enable", "getDocument", "getFlattenedDocument", "requestChildNodes", "describeNode", "resolveNode", "requestNode", "pushNodesByBackendIdsToFrontend", "querySelector", "querySelectorAll", "performSearch", "getSearchResults", "discardSearchResults", "getOuterHTML", "getAttributes", "setAttributeValue", "getBoxModel", "getContentQuads", "getNodeForLocation", "getFrameOwner", "setInspectedNode", "focus", "scrollIntoViewIfNeeded"}, []string{"setChildNodes", "documentUpdated"}},
		{"Overlay", []string{"enable", "highlightNode", "highlightRect", "highlightQuad", "hideHighlight"}, nil},
		{"Input", []string{"dispatchMouseEvent", "dispatchKeyEvent"}, nil},
		{"Accessibility", []string{"enable", "getFullAXTree", "getPartialAXTree", "getRootAXNode", "getAXNodeAndAncestors", "getChildAXNodes", "queryAXTree"}, nil},
		{"AX", axCommandNames(), nil},
	}
	domains := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		domain := map[string]any{"domain": spec.domain}
		if len(spec.commands) > 0 {
			commands := make([]map[string]any, 0, len(spec.commands))
			for _, name := range spec.commands {
				commands = append(commands, map[string]any{"name": name})
			}
			domain["commands"] = commands
		}
		if len(spec.events) > 0 {
			events := make([]map[string]any, 0, len(spec.events))
			for _, name := range spec.events {
				events = append(events, map[string]any{"name": name})
			}
			domain["events"] = events
		}
		domains = append(domains, domain)
	}
	return domains
}

func cdpCompatSetupMethods() []string {
	return []string{
		"Animation.enable",
		"Animation.disable",
		"Audits.enable",
		"Audits.disable",
		"Autofill.enable",
		"Autofill.disable",
		"Autofill.setAddresses",
		"CSS.enable",
		"CSS.disable",
		"CSS.getComputedStyleForNode",
		"CSS.getInlineStylesForNode",
		"CSS.getMatchedStylesForNode",
		"CSS.getPlatformFontsForNode",
		"CSS.trackComputedStyleUpdates",
		"CSS.trackComputedStyleUpdatesForNode",
		"CSS.takeComputedStyleUpdates",
		"Debugger.enable",
		"Debugger.disable",
		"Debugger.setPauseOnExceptions",
		"Debugger.setAsyncCallStackDepth",
		"Debugger.setBlackboxPatterns",
		"DOMDebugger.setBreakOnCSPViolation",
		"Emulation.setEmulatedMedia",
		"Emulation.setEmulatedVisionDeficiency",
		"Emulation.setFocusEmulationEnabled",
		"Inspector.enable",
		"Log.enable",
		"Log.disable",
		"Log.clear",
		"Log.startViolationsReport",
		"Network.enable",
		"Network.disable",
		"Network.setAttachDebugStack",
		"Network.setBlockedURLs",
		"Network.emulateNetworkConditionsByRule",
		"Network.overrideNetworkState",
		"Network.clearAcceptedEncodingsOverride",
		"Page.addScriptToEvaluateOnNewDocument",
		"Overlay.setShowViewportSizeOnResize",
		"Overlay.setShowHinge",
		"Overlay.setShowGridOverlays",
		"Overlay.setShowFlexOverlays",
		"Overlay.setShowScrollSnapOverlays",
		"Overlay.setShowContainerQueryOverlays",
		"Overlay.setShowIsolatedElements",
		"Page.setAdBlockingEnabled",
		"Profiler.enable",
		"Profiler.disable",
		"Runtime.addBinding",
		"Runtime.compileScript",
		"Runtime.globalLexicalScopeNames",
		"ServiceWorker.enable",
		"ServiceWorker.disable",
		"Storage.getStorageKey",
	}
}

func axCommandNames() []string {
	methods := supportedMethods()
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		domain, name, ok := strings.Cut(method, ".")
		if ok && domain == "AX" && name != "" {
			names = append(names, name)
		}
	}
	return names
}

func cdpSchemaDomains() []map[string]any {
	protocol := cdpProtocolDomains()
	domains := make([]map[string]any, 0, len(protocol))
	for _, domain := range protocol {
		domains = append(domains, map[string]any{
			"name":    domain["domain"],
			"version": "1.3",
		})
	}
	return domains
}

func (s *cdpServer) handleWS(w http.ResponseWriter, r *http.Request) {
	cs := s.connectionServer(r)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connID := nextCDPConnID.Add(1)
	slog.Info("cdp websocket open", "conn", connID, "path", r.URL.Path, "target", cs.appArg, "remote", r.RemoteAddr, "user_agent", r.UserAgent())
	defer func() {
		slog.Info("cdp websocket close", "conn", connID, "path", r.URL.Path, "target", cs.appArg)
		conn.Close()
	}()
	for {
		var msg cdpMessage
		if err := conn.ReadJSON(&msg); err != nil {
			slog.Info("cdp websocket read closed", "conn", connID, "path", r.URL.Path, "target", cs.appArg, "err", err)
			return
		}
		slog.Debug("recv cdp message", "conn", connID, "path", r.URL.Path, "target", cs.appArg, "method", msg.Method, "session", msg.SessionID, "id", msg.ID, "params", compactJSON(msg.Params, 240))
		if logCDPInfo(msg.Method) {
			slog.Info("cdp call", "conn", connID, "target", cs.appArg, "method", msg.Method, "session", msg.SessionID, "id", msg.ID, "params", compactJSON(msg.Params, 240))
		}
		dispatch := cs
		if msg.SessionID != "" && !strings.HasPrefix(msg.Method, "Target.") {
			cs.mu.Lock()
			dispatch = cs.sessions[msg.SessionID]
			cs.mu.Unlock()
			if dispatch == nil {
				dispatch = cs
			}
		}
		result, err := dispatch.dispatchCDP(context.WithValue(context.Background(), cdpConnIDContextKey{}, connID), conn, msg.SessionID, msg.Method, msg.Params)
		resp := cdpResponse{ID: msg.ID, SessionID: msg.SessionID}
		if err != nil {
			resp.Error = cdpErrorFromError(err)
		} else {
			resp.Result = result
		}
		if logCDPInfo(msg.Method) {
			slog.Info("cdp response", "conn", connID, "target", dispatch.appArg, "method", msg.Method, "session", msg.SessionID, "id", msg.ID, "error", resp.Error != nil, "result_keys", mapKeys(resp.Result))
		}
		if err := dispatch.writeJSON(conn, resp); err != nil {
			return
		}
	}
}

func logCDPInfo(method string) bool {
	switch method {
	case "Browser.getVersion",
		"Target.setDiscoverTargets",
		"Target.getTargets",
		"Target.attachToTarget",
		"Target.detachFromTarget",
		"Page.navigate",
		"Page.startScreencast",
		"Page.stopScreencast",
		"DOM.getDocument",
		"DOM.requestChildNodes",
		"Runtime.evaluate":
		return true
	}
	return false
}

func (s *cdpServer) connectionServer(r *http.Request) *cdpServer {
	appArg := s.appArg
	targetType := "page"
	var windowID uint32
	var windowName string
	base := path.Base(r.URL.Path)
	if s.root != nil && (base == "/" || base == "." || base == "") {
		return s
	}
	if base == s.currentTargetID() {
		return s
	}
	switch {
	case strings.HasPrefix(base, "app-page-"):
		appArg = strings.TrimPrefix(base, "app-page-")
	case strings.HasPrefix(base, "app-node-"):
		appArg = strings.TrimPrefix(base, "app-node-")
		targetType = "node"
	case base == cdpNodeTargetID:
		targetType = "node"
	case len(base) == 32:
		if win, ok := windowForTargetID(base); ok {
			appArg = strconv.Itoa(win.App.PID)
			windowID = win.Window.ID
			windowName = win.Window.Title
		} else if pid, ok := appPIDForTargetID(base); ok {
			appArg = strconv.Itoa(pid)
			if isNodeTargetID(base, pid) {
				targetType = "node"
			}
		}
	}
	return &cdpServer{
		addr:       s.addr,
		appArg:     appArg,
		targetType: targetType,
		windowID:   windowID,
		windowName: windowName,
		castMaxDim: s.castMaxDim,
		staticList: s.staticList,
		nodes:      make(map[int]*cdpNode),
		backend:    make(map[int]*cdpNode),
		writeMu:    s.writeMu,
		sessions:   make(map[string]*cdpServer),
		casts:      make(map[string]chan struct{}),
		castAcks:   make(map[string]chan int),
		domWatch:   make(map[string]chan struct{}),
		searches:   make(map[string][]int),
		browser:    s.browser,
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func appPIDForTargetID(id string) (int, bool) {
	for _, app := range runningApps() {
		pageID := appTargetID("page", app.PID)
		if id == pageID || id == tabTargetID(pageID) || id == appTargetID("node", app.PID) {
			return app.PID, true
		}
	}
	return 0, false
}

type runningWindow struct {
	App    runningApp
	Window ocrwindow.Window
}

func windowForTargetID(id string) (runningWindow, bool) {
	for _, app := range runningApps() {
		for _, win := range appWindows(app) {
			if id == windowTargetID(app.PID, win.ID) || id == tabTargetID(windowTargetID(app.PID, win.ID)) {
				return runningWindow{App: app, Window: win}, true
			}
		}
	}
	return runningWindow{}, false
}

func isNodeTargetID(id string, pid int) bool {
	return id == appTargetID("node", pid)
}

func appNameForPID(pid int) string {
	for _, app := range runningApps() {
		if app.PID == pid {
			return app.Name
		}
	}
	return ""
}

func (s *cdpServer) dispatchCDP(ctx context.Context, conn *websocket.Conn, sessionID, method string, raw json.RawMessage) (map[string]any, error) {
	if s.browser != nil {
		switch method {
		case "Target.createTarget":
			return s.browser.createTarget(raw)
		case "Target.closeTarget":
			return s.browser.closeTarget(raw)
		}
	}
	if !isSupportedCDPDomain(method) {
		return nil, cdpMethodNotFoundError{Method: method}
	}
	switch method {
	case "Browser.getVersion":
		return map[string]any{"protocolVersion": "1.3", "product": browserProduct, "userAgent": "axcdp/0.1", "jsVersion": "axcdp"}, nil
	case "Browser.getWindowForTarget":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(raw, &p)
		target := s
		if p.TargetID != "" && p.TargetID != s.currentTargetID() {
			target = s.serverForTargetID(p.TargetID)
		}
		return map[string]any{"windowId": 1, "bounds": target.browserWindowBounds()}, nil
	case "Browser.getWindowBounds":
		return map[string]any{"bounds": s.browserWindowBounds()}, nil
	case "Browser.bringToFront":
		return map[string]any{}, s.activateTarget()
	case "Target.setDiscoverTargets":
		var p struct {
			Discover bool `json:"discover"`
		}
		_ = json.Unmarshal(raw, &p)
		if !p.Discover {
			return map[string]any{}, nil
		}
		info := s.targetInfo(false)
		s.sendEvent(conn, "Target.targetCreated", map[string]any{"targetInfo": info})
		currentID, _ := info["targetId"].(string)
		go s.sendDiscoveredTargetEvents(conn, false, currentID)
		return map[string]any{}, nil
	case "Target.getTargets":
		infos := s.targetInfos(false)
		out := make([]any, 0, len(infos))
		for _, info := range infos {
			out = append(out, info)
		}
		return map[string]any{"targetInfos": out}, nil
	case "Target.attachToTarget":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(raw, &p)
		target := s.serverForTargetID(p.TargetID)
		sessionID := "axcdp-session-" + hexTargetID(firstNonEmpty(p.TargetID, cdpTargetID))[:8]
		s.mu.Lock()
		s.sessions[sessionID] = target
		s.mu.Unlock()
		s.sendEvent(conn, "Target.attachedToTarget", map[string]any{"sessionId": sessionID, "targetInfo": target.targetInfo(true), "waitingForDebugger": false})
		return map[string]any{"sessionId": sessionID}, nil
	case "Target.attachToBrowserTarget":
		sessionID := "axcdp-browser-session"
		s.mu.Lock()
		s.sessions[sessionID] = s.serverForTargetID(cdpTargetID)
		s.mu.Unlock()
		return map[string]any{"sessionId": sessionID}, nil
	case "Target.detachFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.SessionID != "" {
			s.mu.Lock()
			delete(s.sessions, p.SessionID)
			s.mu.Unlock()
			s.sendEvent(conn, "Target.detachedFromTarget", map[string]any{"sessionId": p.SessionID})
		}
		return map[string]any{}, nil
	case "Target.sendMessageToTarget":
		var p struct {
			SessionID string `json:"sessionId"`
			Message   string `json:"message"`
		}
		_ = json.Unmarshal(raw, &p)
		var inner cdpMessage
		if err := json.Unmarshal([]byte(p.Message), &inner); err != nil {
			return nil, fmt.Errorf("decode target message: %w", err)
		}
		target := s
		if p.SessionID != "" {
			s.mu.Lock()
			target = s.sessions[p.SessionID]
			s.mu.Unlock()
			if target == nil {
				target = s
			}
		}
		result, err := target.dispatchCDP(ctx, conn, p.SessionID, inner.Method, inner.Params)
		innerResp := cdpResponse{ID: inner.ID}
		if err != nil {
			innerResp.Error = cdpErrorFromError(err)
		} else {
			innerResp.Result = result
		}
		data, err := json.Marshal(innerResp)
		if err != nil {
			return nil, fmt.Errorf("encode target response: %w", err)
		}
		s.sendEvent(conn, "Target.receivedMessageFromTarget", map[string]any{"sessionId": p.SessionID, "message": string(data)})
		return map[string]any{}, nil
	case "Target.getTargetInfo":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"targetInfo": s.serverForTargetID(p.TargetID).targetInfo(false)}, nil
	case "Target.activateTarget":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{}, s.serverForTargetID(p.TargetID).activateTarget()
	case "Target.setAutoAttach", "Target.setAttachToFrames", "Target.setRemoteLocations":
		return map[string]any{}, nil
	case "Page.enable", "DOM.enable", "Runtime.enable", "Overlay.enable", "Accessibility.enable", "CSS.enable", "CSS.disable", "Log.enable", "Network.enable", "Inspector.enable":
		if method == "Page.enable" {
			s.sendSessionEvent(conn, sessionID, "Page.frameStartedLoading", map[string]any{"frameId": cdpFrameID})
			s.sendSessionEvent(conn, sessionID, "Page.frameNavigated", map[string]any{"frame": s.frame()})
			s.sendSessionEvent(conn, sessionID, "Page.domContentEventFired", map[string]any{"timestamp": float64(time.Now().UnixNano()) / 1e9})
			s.sendSessionEvent(conn, sessionID, "Page.loadEventFired", map[string]any{"timestamp": float64(time.Now().UnixNano()) / 1e9})
			s.sendSessionEvent(conn, sessionID, "Page.frameStoppedLoading", map[string]any{"frameId": cdpFrameID})
		}
		if method == "Runtime.enable" {
			s.sendSessionEvent(conn, sessionID, "Runtime.executionContextCreated", map[string]any{"context": map[string]any{"id": 1, "origin": "axcdp://accessibility", "name": "axcdp", "uniqueId": "axcdp-context-1", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": cdpFrameID}}})
		}
		if method == "DOM.enable" {
			s.sendSessionEvent(conn, sessionID, "DOM.documentUpdated", map[string]any{})
			s.startDOMWatch(conn, sessionID)
		}
		return map[string]any{}, nil
	case "Page.getFrameTree":
		return map[string]any{"frameTree": map[string]any{"frame": s.frame()}}, nil
	case "Page.getResourceTree":
		return map[string]any{"frameTree": map[string]any{"frame": s.frame(), "resources": []any{}}}, nil
	case "Page.getLayoutMetrics":
		r := s.viewportBounds()
		return map[string]any{
			"layoutViewport":    map[string]any{"pageX": 0, "pageY": 0, "clientWidth": r.Width, "clientHeight": r.Height},
			"visualViewport":    map[string]any{"offsetX": 0, "offsetY": 0, "pageX": 0, "pageY": 0, "clientWidth": r.Width, "clientHeight": r.Height, "scale": 1, "zoom": 1},
			"contentSize":       map[string]any{"x": 0, "y": 0, "width": r.Width, "height": r.Height},
			"cssLayoutViewport": map[string]any{"pageX": 0, "pageY": 0, "clientWidth": r.Width, "clientHeight": r.Height},
			"cssVisualViewport": map[string]any{"offsetX": 0, "offsetY": 0, "pageX": 0, "pageY": 0, "clientWidth": r.Width, "clientHeight": r.Height, "scale": 1, "zoom": 1},
			"cssContentSize":    map[string]any{"x": 0, "y": 0, "width": r.Width, "height": r.Height},
		}, nil
	case "Page.getNavigationHistory":
		return map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": s.previewURL(""), "userTypedURL": s.previewURL(""), "title": s.targetTitle()}}}, nil
	case "Page.navigate":
		return s.pageNavigate(conn, sessionID, raw)
	case "Page.bringToFront":
		return map[string]any{}, s.activateTarget()
	case "Page.setLifecycleEventsEnabled", "Page.setAdBlockingEnabled":
		return map[string]any{}, nil
	case "Page.addScriptToEvaluateOnNewDocument":
		return map[string]any{"identifier": "axcdp-script-1"}, nil
	case "Page.captureScreenshot":
		opts := parseScreenshotOptions(raw)
		data := s.captureTargetPNG()
		data = encodeScreenshot(data, opts.Format, opts.Quality)
		return map[string]any{"data": data}, nil
	case "Page.startScreencast":
		s.sendSessionEvent(conn, sessionID, "Page.screencastVisibilityChanged", map[string]any{"visible": true})
		s.startScreencast(conn, cdpConnID(ctx), sessionID, parseScreencastOptions(raw))
		return map[string]any{}, nil
	case "Page.screencastFrameAck":
		var p struct {
			SessionID int `json:"sessionId"`
		}
		_ = json.Unmarshal(raw, &p)
		s.noteScreencastAck(sessionID, p.SessionID)
		slog.Info("screencast frame ack", "conn", cdpConnID(ctx), "target", s.appArg, "session", sessionID, "frame_id", p.SessionID)
		return map[string]any{}, nil
	case "Page.stopScreencast":
		s.stopScreencast(sessionID)
		s.sendSessionEvent(conn, sessionID, "Page.screencastVisibilityChanged", map[string]any{"visible": false})
		return map[string]any{}, nil
	case "Runtime.runIfWaitingForDebugger", "Runtime.discardConsoleEntries", "Runtime.addBinding":
		return map[string]any{}, nil
	case "Runtime.evaluate":
		return s.runtimeEvaluate(raw), nil
	case "Runtime.callFunctionOn":
		return map[string]any{"result": map[string]any{"type": "undefined"}}, nil
	case "Runtime.compileScript":
		return map[string]any{}, nil
	case "Runtime.globalLexicalScopeNames":
		return map[string]any{"names": []string{}}, nil
	case "Runtime.getProperties":
		return s.runtimeGetProperties(raw)
	case "Runtime.releaseObject", "Runtime.releaseObjectGroup":
		return map[string]any{}, nil
	case "CSS.getComputedStyleForNode":
		return map[string]any{"computedStyle": []any{}}, nil
	case "CSS.getInlineStylesForNode":
		return map[string]any{}, nil
	case "CSS.getMatchedStylesForNode":
		return map[string]any{"matchedCSSRules": []any{}, "pseudoElements": []any{}, "inherited": []any{}, "cssKeyframesRules": []any{}}, nil
	case "CSS.getPlatformFontsForNode":
		return map[string]any{"fonts": []any{}}, nil
	case "CSS.trackComputedStyleUpdates", "CSS.trackComputedStyleUpdatesForNode":
		return map[string]any{}, nil
	case "CSS.takeComputedStyleUpdates":
		return map[string]any{"nodeIds": []int{}}, nil
	case "Schema.getDomains":
		return map[string]any{"domains": cdpSchemaDomains()}, nil
	case "DOM.getDocument":
		if err := s.refreshTreeBounded(12 * time.Second); err != nil {
			return nil, err
		}
		depth := intParamDefault(raw, "depth", 2)
		if depth > 0 && depth < 3 {
			depth = 3
		}
		slog.Info("cdp dom document", "target", s.appArg, "title", s.targetTitle(), "depth", depth, "nodes", len(s.nodes))
		return map[string]any{"root": s.documentNode(depth)}, nil
	case "DOM.getFlattenedDocument":
		if err := s.refreshTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		depth := intParamDefault(raw, "depth", -1)
		return map[string]any{"nodes": s.flattenedDOM(depth)}, nil
	case "DOM.requestChildNodes":
		var p struct {
			NodeID int `json:"nodeId"`
			Depth  int `json:"depth"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.NodeID == cdpDocumentNodeID {
			if err := s.ensureTreeBounded(4 * time.Second); err != nil {
				return nil, err
			}
			s.mu.Lock()
			var children []any
			if s.root != nil {
				depth := p.Depth
				if depth == 0 {
					depth = 1
				}
				children = append(children, s.domNode(s.root, depth-1))
			}
			count := len(children)
			s.mu.Unlock()
			slog.Info("cdp dom children", "target", s.appArg, "node", p.NodeID, "depth", p.Depth, "children", count)
			s.sendSessionEvent(conn, sessionID, "DOM.setChildNodes", map[string]any{"parentId": cdpDocumentNodeID, "nodes": children})
			return map[string]any{}, nil
		}
		node := s.node(p.NodeID)
		if node == nil {
			if err := s.refreshTreeBounded(4 * time.Second); err != nil {
				return nil, err
			}
			node = s.node(p.NodeID)
			if node == nil {
				return nil, fmt.Errorf("unknown nodeId %d", p.NodeID)
			}
		}
		depth := p.Depth
		if depth == 0 {
			depth = 1
		}
		if !node.ChildrenReady {
			if err := s.expandNodeChildren(node, depth); err != nil {
				return nil, err
			}
		}
		children := make([]any, 0, len(node.Children))
		for _, child := range node.Children {
			children = append(children, s.domNode(child, depth-1))
		}
		slog.Info("cdp dom children", "target", s.appArg, "node", p.NodeID, "depth", p.Depth, "children", len(children))
		s.sendSessionEvent(conn, sessionID, "DOM.setChildNodes", map[string]any{"parentId": domNodeID(node.NodeID), "nodes": children})
		return map[string]any{}, nil
	case "DOM.describeNode":
		if s.documentNodeParam(raw) {
			return map[string]any{"node": s.documentNode(1)}, nil
		}
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"node": s.domNode(node, 1)}, nil
	case "DOM.resolveNode":
		if s.documentNodeParam(raw) {
			return map[string]any{"object": map[string]any{"type": "object", "subtype": "node", "objectId": "document", "description": "#document"}}, nil
		}
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"object": map[string]any{"type": "object", "subtype": "node", "objectId": fmt.Sprintf("node:%d", domNodeID(node.NodeID)), "description": node.NodeName}}, nil
	case "DOM.requestNode":
		var p struct {
			ObjectID string `json:"objectId"`
		}
		_ = json.Unmarshal(raw, &p)
		if !strings.HasPrefix(p.ObjectID, "node:") {
			return nil, fmt.Errorf("unsupported objectId %q", p.ObjectID)
		}
		id, err := strconv.Atoi(strings.TrimPrefix(p.ObjectID, "node:"))
		if err != nil {
			return nil, fmt.Errorf("parse node object id %q: %w", p.ObjectID, err)
		}
		if s.node(id) == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"nodeId": id}, nil
	case "DOM.pushNodesByBackendIdsToFrontend":
		var p struct {
			BackendNodeIDs []int `json:"backendNodeIds"`
		}
		_ = json.Unmarshal(raw, &p)
		ids := make([]int, 0, len(p.BackendNodeIDs))
		for _, backendID := range p.BackendNodeIDs {
			if node := s.backendNode(backendID); node != nil {
				ids = append(ids, domNodeID(node.NodeID))
			}
		}
		return map[string]any{"nodeIds": ids}, nil
	case "DOM.querySelector":
		var p struct {
			NodeID   int    `json:"nodeId"`
			Selector string `json:"selector"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"nodeId": s.querySelector(p.NodeID, p.Selector)}, nil
	case "DOM.querySelectorAll":
		var p struct {
			NodeID   int    `json:"nodeId"`
			Selector string `json:"selector"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"nodeIds": s.querySelectorAll(p.NodeID, p.Selector)}, nil
	case "DOM.performSearch":
		var p struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &p)
		ids := s.searchDOM(p.Query)
		searchID := "axcdp-search-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		s.mu.Lock()
		if s.searches == nil {
			s.searches = make(map[string][]int)
		}
		s.searches[searchID] = ids
		s.mu.Unlock()
		return map[string]any{"searchId": searchID, "resultCount": len(ids)}, nil
	case "DOM.getSearchResults":
		var p struct {
			SearchID  string `json:"searchId"`
			FromIndex int    `json:"fromIndex"`
			ToIndex   int    `json:"toIndex"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"nodeIds": s.searchResults(p.SearchID, p.FromIndex, p.ToIndex)}, nil
	case "DOM.discardSearchResults":
		var p struct {
			SearchID string `json:"searchId"`
		}
		_ = json.Unmarshal(raw, &p)
		s.mu.Lock()
		delete(s.searches, p.SearchID)
		s.mu.Unlock()
		return map[string]any{}, nil
	case "DOM.getOuterHTML":
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"outerHTML": s.outerHTML(node)}, nil
	case "DOM.getAttributes":
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"attributes": domAttributes(node)}, nil
	case "DOM.setAttributeValue":
		return s.domSetAttributeValue(raw)
	case "DOM.getBoxModel":
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"model": boxModel(s.viewportRelativeRect(node.Bounds))}, nil
	case "DOM.getContentQuads":
		node := s.nodeFromParams(raw)
		if node == nil {
			return nil, fmt.Errorf("node not found")
		}
		return map[string]any{"quads": []any{boxQuad(s.viewportRelativeRect(node.Bounds))}}, nil
	case "DOM.getNodeForLocation":
		var p struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		_ = json.Unmarshal(raw, &p)
		node := s.nodeForLocation(p.X, p.Y)
		if node == nil {
			return map[string]any{"frameId": cdpFrameID}, nil
		}
		go highlightAXRect(node.Bounds, 1500*time.Millisecond)
		return map[string]any{"backendNodeId": domBackendNodeID(node.BackendID), "frameId": cdpFrameID, "nodeId": domNodeID(node.NodeID)}, nil
	case "DOM.getFrameOwner":
		return s.domFrameOwner(raw)
	case "DOM.setInspectedNode":
		node := s.nodeFromParams(raw)
		if node != nil {
			go highlightAXRect(node.Bounds, 1500*time.Millisecond)
		}
		return map[string]any{}, nil
	case "DOM.focus", "DOM.scrollIntoViewIfNeeded":
		node := s.nodeFromParams(raw)
		if node != nil {
			go highlightAXRect(node.Bounds, 1500*time.Millisecond)
		}
		return map[string]any{}, nil
	case "Overlay.highlightNode":
		node := s.nodeFromParams(raw)
		if node != nil {
			go highlightAXRect(node.Bounds, 1500*time.Millisecond)
		}
		return map[string]any{}, nil
	case "Overlay.highlightRect":
		var p struct {
			X      float64 `json:"x"`
			Y      float64 `json:"y"`
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		}
		_ = json.Unmarshal(raw, &p)
		go highlightAXRect(s.viewportAbsoluteRect(axRect{X: p.X, Y: p.Y, Width: p.Width, Height: p.Height}), 1500*time.Millisecond)
		return map[string]any{}, nil
	case "Overlay.highlightQuad":
		var p struct {
			Quad []float64 `json:"quad"`
		}
		_ = json.Unmarshal(raw, &p)
		if r, ok := rectFromQuad(p.Quad); ok {
			go highlightAXRect(s.viewportAbsoluteRect(r), 1500*time.Millisecond)
		}
		return map[string]any{}, nil
	case "Overlay.setShowViewportSizeOnResize", "Overlay.setShowHinge", "Overlay.setShowGridOverlays", "Overlay.setShowFlexOverlays", "Overlay.setShowScrollSnapOverlays", "Overlay.setShowContainerQueryOverlays", "Overlay.setShowIsolatedElements":
		return map[string]any{}, nil
	case "Overlay.hideHighlight":
		hideAXHighlight()
		return map[string]any{}, nil
	case "Input.dispatchMouseEvent":
		return s.dispatchMouseEvent(raw)
	case "Input.dispatchKeyEvent":
		return s.dispatchKeyEvent(raw)
	case "Accessibility.getFullAXTree":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"nodes": s.accessibilityNodes()}, nil
	case "Accessibility.getPartialAXTree":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"nodes": s.partialAccessibilityNodes(raw)}, nil
	case "Accessibility.getRootAXNode":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"node": s.rootAccessibilityNode()}, nil
	case "Accessibility.getAXNodeAndAncestors":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"nodes": s.accessibilityNodeAndAncestors(raw)}, nil
	case "Accessibility.getChildAXNodes":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"nodes": s.childAccessibilityNodes(raw)}, nil
	case "Accessibility.queryAXTree":
		if err := s.ensureTreeBounded(4 * time.Second); err != nil {
			return nil, err
		}
		return map[string]any{"nodes": s.queryAccessibilityNodes(raw)}, nil
	case "Storage.getStorageKey":
		return map[string]any{"storageKey": s.previewURL("")}, nil
	case "Animation.enable", "Animation.disable", "Audits.enable", "Audits.disable", "Autofill.enable", "Autofill.disable", "Autofill.setAddresses", "Debugger.enable", "Debugger.disable", "Debugger.setPauseOnExceptions", "Debugger.setAsyncCallStackDepth", "Debugger.setBlackboxPatterns", "DOMDebugger.setBreakOnCSPViolation", "Emulation.setEmulatedMedia", "Emulation.setEmulatedVisionDeficiency", "Emulation.setFocusEmulationEnabled", "Log.clear", "Log.disable", "Log.startViolationsReport", "Network.disable", "Network.setAttachDebugStack", "Network.setBlockedURLs", "Network.emulateNetworkConditionsByRule", "Network.overrideNetworkState", "Network.clearAcceptedEncodingsOverride", "Profiler.enable", "Profiler.disable", "ServiceWorker.enable", "ServiceWorker.disable":
		return map[string]any{}, nil
	default:
		if strings.HasPrefix(method, "AX.") {
			local := &server{refs: make(map[string]axuiautomation.AXUIElementRef), obs: make(map[string]*observer), strings: make(map[string]corefoundation.CFStringRef)}
			var params map[string]any
			_ = json.Unmarshal(raw, &params)
			return local.dispatch(method, params)
		}
		return nil, cdpMethodNotFoundError{Method: method}
	}
}

func isSupportedCDPDomain(method string) bool {
	for _, compat := range cdpCompatSetupMethods() {
		if method == compat {
			return true
		}
	}
	switch method {
	case "Log.enable", "Log.disable", "Log.clear":
		return true
	case "Network.enable", "Network.disable":
		return true
	case "Inspector.enable":
		return true
	}
	for _, domain := range cdpProtocolDomains() {
		domainName := fmt.Sprint(domain["domain"])
		commands, _ := domain["commands"].([]map[string]any)
		for _, command := range commands {
			if method == domainName+"."+fmt.Sprint(command["name"]) {
				return true
			}
		}
	}
	return false
}

func cdpErrorFromError(err error) *cdpError {
	var notFound cdpMethodNotFoundError
	if errors.As(err, &notFound) {
		return &cdpError{Code: -32601, Message: err.Error()}
	}
	return &cdpError{Code: -32000, Message: err.Error()}
}

func (s *cdpServer) serverForTargetID(targetID string) *cdpServer {
	appArg := s.appArg
	targetType := "page"
	var windowID uint32
	var windowName string
	if targetID == cdpNodeTargetID {
		targetType = "node"
	} else if targetID != "" && targetID != cdpTargetID {
		if win, ok := windowForTargetID(targetID); ok {
			appArg = strconv.Itoa(win.App.PID)
			windowID = win.Window.ID
			windowName = win.Window.Title
		} else if pid, ok := appPIDForTargetID(targetID); ok {
			appArg = strconv.Itoa(pid)
			if isNodeTargetID(targetID, pid) {
				targetType = "node"
			}
		}
	}
	if windowID != 0 {
		return s.serverForWindowArg(appArg, windowID, windowName, targetType)
	}
	return s.serverForAppArg(appArg, targetType)
}

func (s *cdpServer) serverForAppArg(appArg, targetType string) *cdpServer {
	target := &cdpServer{
		addr:       s.addr,
		appArg:     appArg,
		targetType: targetType,
		castMaxDim: s.castMaxDim,
		nodes:      make(map[int]*cdpNode),
		backend:    make(map[int]*cdpNode),
		writeMu:    s.writeMu,
		sessions:   make(map[string]*cdpServer),
		casts:      make(map[string]chan struct{}),
		castAcks:   make(map[string]chan int),
		domWatch:   make(map[string]chan struct{}),
		searches:   make(map[string][]int),
	}
	return target
}

func (s *cdpServer) serverForWindow(pid int, windowID uint32, targetType string) *cdpServer {
	return s.serverForWindowArg(strconv.Itoa(pid), windowID, "", targetType)
}

func (s *cdpServer) serverForWindowArg(appArg string, windowID uint32, windowName, targetType string) *cdpServer {
	target := s.serverForAppArg(appArg, targetType)
	target.windowID = windowID
	target.windowName = windowName
	return target
}

func (s *cdpServer) sendEvent(conn *websocket.Conn, method string, params map[string]any) bool {
	if conn == nil {
		return false
	}
	if err := s.writeJSON(conn, cdpEvent{Method: method, Params: params}); err != nil {
		slog.Info("cdp event write failed", "method", method, "err", err)
		return false
	}
	return true
}

func (s *cdpServer) sendSessionEvent(conn *websocket.Conn, sessionID, method string, params map[string]any) bool {
	if conn == nil {
		return false
	}
	event := cdpEvent{Method: method, Params: params}
	if sessionID == "" {
		if err := s.writeJSON(conn, event); err != nil {
			slog.Info("cdp event write failed", "method", method, "session", sessionID, "err", err)
			return false
		}
		return true
	}
	if err := s.writeJSON(conn, cdpEvent{Method: method, Params: params, SessionID: sessionID}); err != nil {
		slog.Info("cdp event write failed", "method", method, "session", sessionID, "err", err)
		return false
	}
	data, err := json.Marshal(event)
	if err != nil {
		return false
	}
	if err := s.writeJSON(conn, cdpEvent{
		Method: "Target.receivedMessageFromTarget",
		Params: map[string]any{
			"sessionId": sessionID,
			"message":   string(data),
		},
	}); err != nil {
		slog.Info("cdp event write failed", "method", "Target.receivedMessageFromTarget", "session", sessionID, "err", err)
		return false
	}
	return true
}

func (s *cdpServer) writeJSON(conn *websocket.Conn, v any) error {
	if s.writeMu == nil {
		s.writeMu = new(sync.Mutex)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteJSON(v)
}

func compactJSON(raw json.RawMessage, limit int) string {
	if len(raw) == 0 {
		return ""
	}
	s := strings.Join(strings.Fields(string(raw)), " ")
	if limit > 0 && len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

func (s *cdpServer) refreshTree() error {
	status := permissions.Check(permissions.ReqAccessibility)
	if status != permissions.StatusGranted {
		slog.Warn("accessibility permission not granted; installing permission root", "status", permissionStatusName(status), "app", s.appArg)
		s.installPermissionRoot(status)
		return nil
	}
	start := time.Now()
	root, err := s.axRootRef()
	if err != nil {
		slog.Warn("AX root lookup failed", "app", s.appArg, "err", err)
		return err
	}
	build := &cdpServer{
		appArg:  s.appArg,
		nodes:   make(map[int]*cdpNode),
		backend: make(map[int]*cdpNode),
	}
	buildRoot := root
	selectedWindow := false
	if s.windowID != 0 {
		windowRoot, err := s.axWindowRootRef(root)
		if err != nil {
			slog.Warn("AX window root lookup failed", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "err", err)
		} else {
			buildRoot = windowRoot
			selectedWindow = true
		}
	}
	maxDepth := s.refreshDepth()
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	builtRoot := build.buildNode(buildRoot, 0, 0, maxDepth, seen)
	if s.windowID != 0 && !selectedWindow {
		windowRoot := s.selectWindowRoot(builtRoot)
		if windowRoot == nil {
			slog.Warn("AX window root not found", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName)
		} else {
			windowRoot.ParentID = 0
			builtRoot = windowRoot
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = build.nextID
	s.nodes = build.nodes
	s.backend = build.backend
	s.root = builtRoot
	nodes := len(s.nodes)
	title := ""
	if builtRoot != nil {
		title = builtRoot.Title
	}
	slog.Info("AX tree refreshed", "app", s.appArg, "window_id", s.windowID, "nodes", nodes, "depth", maxDepth, "root_title", title, "duration", time.Since(start))
	return nil
}

func (s *cdpServer) refreshDepth() int {
	if s.windowID != 0 {
		return 3
	}
	if s.appArg != "" {
		return 2
	}
	return 1
}

func (s *cdpServer) selectWindowRoot(root *cdpNode) *cdpNode {
	if root == nil || s.windowID == 0 {
		return root
	}
	pid, ok := s.appPID()
	if !ok {
		return nil
	}
	win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), s.windowID)
	if err != nil {
		slog.Warn("find window target failed", "app", s.appArg, "window_id", s.windowID, "err", err)
	}
	var titleMatch, boundsMatch, firstWindow *cdpNode
	walkDOM(root, func(n *cdpNode) {
		if n.Role != "AXWindow" {
			return
		}
		if firstWindow == nil {
			firstWindow = n
		}
		if s.windowName != "" && n.Title == s.windowName {
			titleMatch = n
		}
		if err == nil && closeRect(n.Bounds, axRect{X: win.X, Y: win.Y, Width: win.W, Height: win.H}, 4) {
			boundsMatch = n
		}
	})
	if boundsMatch != nil {
		return boundsMatch
	}
	if titleMatch != nil {
		return titleMatch
	}
	return firstWindow
}

func (s *cdpServer) axWindowRootRef(root axuiautomation.AXUIElementRef) (axuiautomation.AXUIElementRef, error) {
	if root == 0 || s.windowID == 0 {
		return 0, fmt.Errorf("missing window target")
	}
	pid, ok := s.appPID()
	if !ok {
		return 0, fmt.Errorf("missing pid")
	}
	win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), s.windowID)
	if err != nil {
		return 0, err
	}
	wanted := axRect{X: win.X, Y: win.Y, Width: win.W, Height: win.H}
	var titleMatch, boundsMatch, firstWindow axuiautomation.AXUIElementRef
	for _, ref := range axWindowElements(root) {
		if ref == 0 {
			continue
		}
		if firstWindow == 0 {
			firstWindow = ref
		}
		title := safeString(axStringAttr(ref, "AXTitle"))
		x, y := axPointAttr(ref, "AXPosition")
		w, h := axSizeAttr(ref, "AXSize")
		if s.windowName != "" && title == s.windowName {
			titleMatch = ref
		}
		if closeRect(axRect{X: x, Y: y, Width: w, Height: h}, wanted, 4) {
			boundsMatch = ref
		}
	}
	switch {
	case boundsMatch != 0:
		return boundsMatch, nil
	case titleMatch != 0:
		return titleMatch, nil
	case firstWindow != 0:
		return firstWindow, nil
	default:
		ref, err := s.axWindowRootAtPosition(wanted)
		if err == nil {
			return ref, nil
		}
		slog.Info("AX window root not resolved by hit test", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "err", err)
		return 0, err
	}
}

func (s *cdpServer) axWindowRootByWindowID(root axuiautomation.AXUIElementRef) (axuiautomation.AXUIElementRef, error) {
	if root == 0 || s.windowID == 0 {
		return 0, fmt.Errorf("missing window target")
	}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	var windowMatch axuiautomation.AXUIElementRef
	var containerMatch axuiautomation.AXUIElementRef
	var firstMatch axuiautomation.AXUIElementRef
	var walk func(axuiautomation.AXUIElementRef, int) bool
	walk = func(ref axuiautomation.AXUIElementRef, depth int) bool {
		if ref == 0 || seen[ref] || depth > 6 {
			return false
		}
		seen[ref] = true
		setAXTimeout(ref)
		children := axWindowSearchChildren(ref)
		var window uint32
		if axuiautomation.AXUIElementGetWindow(ref, &window) == 0 && window == s.windowID {
			role := safeString(axStringAttr(ref, "AXRole"))
			switch {
			case role == "AXWindow":
				windowMatch = ref
				return true
			case firstMatch == 0:
				firstMatch = ref
			}
			if containerMatch == 0 && role != "AXApplication" && role != "AXMenuBar" && len(children) > 0 {
				containerMatch = ref
			}
		}
		for _, child := range children {
			if walk(child, depth+1) {
				return true
			}
		}
		return false
	}
	walk(root, 0)
	switch {
	case windowMatch != 0:
		slog.Info("AX window resolved by AXUIElementGetWindow", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "role", "AXWindow")
		return windowMatch, nil
	case containerMatch != 0:
		slog.Info("AX window resolved by AXUIElementGetWindow", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "role", safeString(axStringAttr(containerMatch, "AXRole")))
		return containerMatch, nil
	case firstMatch != 0:
		slog.Info("AX window resolved by AXUIElementGetWindow", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "role", safeString(axStringAttr(firstMatch, "AXRole")))
		return firstMatch, nil
	default:
		return 0, fmt.Errorf("no AX elements for window %d", s.windowID)
	}
}

func (s *cdpServer) axWindowRootAtPosition(wanted axRect) (axuiautomation.AXUIElementRef, error) {
	root := ax.systemWideElement()
	if root == 0 {
		return 0, fmt.Errorf("create system-wide AX element")
	}
	setAXTimeout(root)
	points := []struct {
		x float64
		y float64
	}{
		{wanted.X + wanted.Width/2, wanted.Y + wanted.Height/2},
		{wanted.X + 24, wanted.Y + 24},
		{wanted.X + wanted.Width - 24, wanted.Y + 24},
		{wanted.X + 24, wanted.Y + wanted.Height - 24},
	}
	var firstWindow axuiautomation.AXUIElementRef
	for _, p := range points {
		if !rectContains(wanted, p.x, p.y) {
			continue
		}
		for _, y := range axHitTestYVariants(p.y) {
			var hit axuiautomation.AXUIElementRef
			if code := ax.copyElementAtPosition(root, p.x, y, &hit); code != 0 || hit == 0 {
				slog.Info("AX hit test missed", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "ax_error", code, "x", p.x, "y", y)
				continue
			}
			var hitWindow uint32
			hitWindowErr := axuiautomation.AXUIElementGetWindow(hit, &hitWindow)
			slog.Info("AX hit test result", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "role", safeString(axStringAttr(hit, "AXRole")), "title", safeString(axStringAttr(hit, "AXTitle")), "hit_window", hitWindow, "hit_window_error", hitWindowErr, "x", p.x, "y", y)
			if ref := containingAXElementForWindow(hit, s.windowID); ref != 0 {
				slog.Info("AX element resolved by hit test", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "role", safeString(axStringAttr(ref, "AXRole")), "x", p.x, "y", y)
				return ref, nil
			}
			if win := containingAXWindow(hit); win != 0 {
				if firstWindow == 0 {
					firstWindow = win
				}
				wx, wy := axPointAttr(win, "AXPosition")
				w, h := axSizeAttr(win, "AXSize")
				if closeRect(axRect{X: wx, Y: wy, Width: w, Height: h}, wanted, 8) {
					slog.Info("AX window resolved by hit test", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName, "x", p.x, "y", y)
					return win, nil
				}
			}
		}
	}
	if firstWindow != 0 {
		slog.Info("AX window resolved by hit test without exact bounds", "app", s.appArg, "window_id", s.windowID, "window_title", s.windowName)
		return firstWindow, nil
	}
	return 0, fmt.Errorf("no AX windows")
}

func axHitTestYVariants(y float64) []float64 {
	variants := []float64{y}
	bounds := coregraphics.CGDisplayBounds(coregraphics.CGMainDisplayID())
	if bounds.Size.Height <= 0 {
		return variants
	}
	flipped := bounds.Origin.Y + bounds.Size.Height - y
	if !closeFloat(flipped, y, 0.5) {
		variants = append(variants, flipped)
	}
	return variants
}

func containingAXElementForWindow(ref axuiautomation.AXUIElementRef, windowID uint32) axuiautomation.AXUIElementRef {
	if windowID == 0 {
		return 0
	}
	var first axuiautomation.AXUIElementRef
	var container axuiautomation.AXUIElementRef
	for i := 0; ref != 0 && i < 40; i++ {
		setAXTimeout(ref)
		var window uint32
		if axuiautomation.AXUIElementGetWindow(ref, &window) == 0 && window == windowID {
			if first == 0 {
				first = ref
			}
			role := safeString(axStringAttr(ref, "AXRole"))
			if role == "AXWindow" {
				return ref
			}
			if role != "AXApplication" && role != "AXMenuBar" && len(axWindowSearchChildren(ref)) > 0 {
				container = ref
			}
		}
		parents := axElementAttr(ref, "AXParent")
		if len(parents) == 0 || parents[0] == ref {
			break
		}
		ref = parents[0]
	}
	if container != 0 {
		return container
	}
	return first
}

func containingAXWindow(ref axuiautomation.AXUIElementRef) axuiautomation.AXUIElementRef {
	for i := 0; ref != 0 && i < 40; i++ {
		setAXTimeout(ref)
		if safeString(axStringAttr(ref, "AXRole")) == "AXWindow" {
			return ref
		}
		parents := axElementAttr(ref, "AXParent")
		if len(parents) == 0 || parents[0] == ref {
			return 0
		}
		ref = parents[0]
	}
	return 0
}

func closeRect(a, b axRect, delta float64) bool {
	return closeFloat(a.X, b.X, delta) &&
		closeFloat(a.Y, b.Y, delta) &&
		closeFloat(a.Width, b.Width, delta) &&
		closeFloat(a.Height, b.Height, delta)
}

func closeFloat(a, b, delta float64) bool {
	if a > b {
		return a-b <= delta
	}
	return b-a <= delta
}

func (s *cdpServer) refreshTreeBounded(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- s.refreshTree()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		slog.Warn("AX tree refresh timed out", "app", s.appArg, "timeout", timeout)
		s.installFallbackRoot()
		return nil
	}
}

func (s *cdpServer) installFallbackRoot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != nil {
		return
	}
	role := "AXApplication"
	title := s.targetTitle()
	if s.appArg == "" {
		role = "AXSystemWide"
		title = "macOS Accessibility"
	}
	node := &cdpNode{
		NodeID:      1,
		BackendID:   1,
		NodeName:    role,
		LocalName:   strings.ToLower(strings.TrimPrefix(role, "AX")),
		Role:        role,
		Title:       title,
		Description: "AX tree timed out",
	}
	s.nextID = 1
	s.nodes = map[int]*cdpNode{1: node}
	s.backend = map[int]*cdpNode{1: node}
	s.root = node
}

func (s *cdpServer) installPermissionRoot(status permissions.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := &cdpNode{
		NodeID:      1,
		BackendID:   1,
		NodeName:    "AXPermissionDenied",
		LocalName:   "permissiondenied",
		Role:        "AXPermissionDenied",
		Title:       s.targetTitle(),
		Description: "Accessibility permission " + permissionStatusName(status) + " for " + tccBundleID + "; grant " + tccAppName + " in System Settings > Privacy & Security > Accessibility",
	}
	s.nextID = 1
	s.nodes = map[int]*cdpNode{1: node}
	s.backend = map[int]*cdpNode{1: node}
	s.root = node
}

func (s *cdpServer) ensureTree() error {
	s.mu.Lock()
	ready := s.root != nil
	s.mu.Unlock()
	if ready {
		return nil
	}
	return s.refreshTree()
}

func (s *cdpServer) ensureTreeBounded(timeout time.Duration) error {
	s.mu.Lock()
	ready := s.root != nil
	s.mu.Unlock()
	if ready {
		return nil
	}
	return s.refreshTreeBounded(timeout)
}

func (s *cdpServer) axRootRef() (axuiautomation.AXUIElementRef, error) {
	if s.appArg == "" {
		ref := ax.systemWideElement()
		if ref == 0 {
			return 0, fmt.Errorf("create system-wide AX element")
		}
		setAXTimeout(ref)
		return ref, nil
	}
	if pid, err := strconv.Atoi(s.appArg); err == nil {
		ref := axuiautomation.AXUIElementCreateApplication(int32(pid))
		if ref == 0 {
			return 0, fmt.Errorf("connect to pid %d", pid)
		}
		setAXTimeout(ref)
		return ref, nil
	}
	app, err := axuiautomation.NewApplication(s.appArg)
	if err == nil && app != nil && app.Root() != nil {
		ref := app.Root().Ref()
		setAXTimeout(ref)
		return ref, nil
	}
	return 0, fmt.Errorf("open app %q: %w", s.appArg, err)
}

func setAXTimeout(ref axuiautomation.AXUIElementRef) {
	if ref != 0 {
		_ = axuiautomation.AXUIElementSetMessagingTimeout(ref, 0.25)
	}
}

func (s *cdpServer) buildNode(ref axuiautomation.AXUIElementRef, parent, depth, maxDepth int, seen map[axuiautomation.AXUIElementRef]bool) *cdpNode {
	if ref == 0 || seen[ref] {
		return nil
	}
	setAXTimeout(ref)
	seen[ref] = true
	s.nextID++
	id := s.nextID
	attrs := axAttributeNames(ref)
	role := safeString(axStringAttrKnown(ref, attrs, "AXRole"))
	title := safeString(axStringAttrKnown(ref, attrs, "AXTitle"))
	name := role
	if name == "" {
		name = "AXElement"
	}
	x, y := axPointAttrKnown(ref, attrs, "AXPosition")
	w, h := axSizeAttrKnown(ref, attrs, "AXSize")
	node := &cdpNode{
		NodeID:      id,
		BackendID:   id,
		ParentID:    parent,
		NodeName:    name,
		LocalName:   strings.ToLower(strings.TrimPrefix(name, "AX")),
		Role:        role,
		Title:       title,
		Description: safeString(axStringAttrKnown(ref, attrs, "AXDescription")),
		Identifier:  safeString(axStringAttrKnown(ref, attrs, "AXIdentifier")),
		Bounds:      axRect{X: x, Y: y, Width: w, Height: h},
		Ref:         ref,
	}
	s.nodes[id] = node
	s.backend[id] = node
	if depth < maxDepth {
		children := axChildElementsKnown(ref, attrs)
		node.ChildCount = len(children)
		childSeen := make(map[string]bool)
		for _, child := range children {
			childNode := s.buildNode(child, id, depth+1, maxDepth, seen)
			if childNode != nil {
				if redundantAXChild(node, childNode) {
					continue
				}
				key := structuralAXKey(childNode)
				if key != "" && childSeen[key] {
					continue
				}
				childSeen[key] = true
				node.Children = append(node.Children, childNode)
			}
		}
		node.ChildCount = len(node.Children)
		node.ChildrenReady = true
	} else {
		node.ChildCount = axChildElementCountKnown(ref, attrs)
		node.ChildrenReady = node.ChildCount == 0
	}
	return node
}

func (s *cdpServer) expandNodeChildren(node *cdpNode, depth int) error {
	if node == nil || node.Ref == 0 {
		return nil
	}
	if depth <= 0 {
		depth = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if node.ChildrenReady {
		return nil
	}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	seen[node.Ref] = true
	childSeen := make(map[string]bool)
	for _, child := range axChildElements(node.Ref) {
		childNode := s.buildNode(child, node.NodeID, 1, depth, seen)
		if childNode == nil {
			continue
		}
		if redundantAXChild(node, childNode) {
			continue
		}
		key := structuralAXKey(childNode)
		if key != "" && childSeen[key] {
			continue
		}
		childSeen[key] = true
		node.Children = append(node.Children, childNode)
	}
	node.ChildCount = len(node.Children)
	node.ChildrenReady = true
	return nil
}

func redundantAXChild(parent, child *cdpNode) bool {
	if parent == nil || child == nil {
		return false
	}
	if child.Role != "AXApplication" {
		return false
	}
	return parent.Role == child.Role && parent.Title == child.Title && parent.Bounds == child.Bounds
}

func structuralAXKey(n *cdpNode) string {
	if n == nil {
		return ""
	}
	switch n.Role {
	case "AXApplication", "AXMenuBar":
	default:
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%.0f\x00%.0f\x00%.0f\x00%.0f", n.Role, n.Title, n.Bounds.X, n.Bounds.Y, n.Bounds.Width, n.Bounds.Height)
}

func (s *cdpServer) domNode(n *cdpNode, depth int) map[string]any {
	childCount := n.ChildCount
	if len(n.Children) > childCount {
		childCount = len(n.Children)
	}
	out := map[string]any{
		"nodeId":         domNodeID(n.NodeID),
		"backendNodeId":  domBackendNodeID(n.BackendID),
		"nodeType":       1,
		"nodeName":       n.NodeName,
		"localName":      n.LocalName,
		"nodeValue":      "",
		"childNodeCount": childCount,
		"attributes":     domAttributes(n),
	}
	switch {
	case n.ParentID != 0:
		out["parentId"] = domNodeID(n.ParentID)
	case s.root != nil && n.NodeID == s.root.NodeID:
		out["parentId"] = cdpDocumentNodeID
	}
	if depth != 0 {
		children := make([]any, 0, len(n.Children))
		nextDepth := depth - 1
		for _, child := range n.Children {
			children = append(children, s.domNode(child, nextDepth))
		}
		out["children"] = children
	}
	return out
}

func (s *cdpServer) documentNode(depth int) map[string]any {
	out := map[string]any{
		"nodeId":         cdpDocumentNodeID,
		"backendNodeId":  cdpDocumentBackendID,
		"nodeType":       9,
		"nodeName":       "#document",
		"localName":      "",
		"nodeValue":      "",
		"documentURL":    s.previewURL(""),
		"baseURL":        s.previewURL(""),
		"xmlVersion":     "",
		"childNodeCount": 0,
	}
	if s.root != nil {
		out["childNodeCount"] = 1
		if depth != 0 {
			out["children"] = []any{s.domNode(s.root, depth-1)}
		}
	}
	return out
}

func domNodeID(id int) int {
	if id == 0 {
		return 0
	}
	return id + cdpAXNodeOffset
}

func axNodeID(id int) int {
	if id == cdpDocumentNodeID {
		return 0
	}
	if id > cdpAXNodeOffset {
		return id - cdpAXNodeOffset
	}
	return id
}

func domBackendNodeID(id int) int {
	if id == 0 {
		return 0
	}
	return id + cdpAXNodeOffset
}

func axBackendNodeID(id int) int {
	if id == cdpDocumentBackendID {
		return 0
	}
	if id > cdpAXNodeOffset {
		return id - cdpAXNodeOffset
	}
	return id
}

func domAttributes(n *cdpNode) []string {
	attrs := []string{"role", n.Role}
	if n.Title != "" {
		attrs = append(attrs, "title", n.Title)
	}
	if n.Description != "" {
		attrs = append(attrs, "aria-label", n.Description)
	}
	if n.Identifier != "" {
		attrs = append(attrs, "id", n.Identifier)
	}
	return append(attrs,
		"data-ax-node-id", strconv.Itoa(n.NodeID),
		"data-ax-x", fmt.Sprintf("%.0f", n.Bounds.X),
		"data-ax-y", fmt.Sprintf("%.0f", n.Bounds.Y),
		"data-ax-width", fmt.Sprintf("%.0f", n.Bounds.Width),
		"data-ax-height", fmt.Sprintf("%.0f", n.Bounds.Height),
	)
}

func (s *cdpServer) domSetAttributeValue(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		NodeID int    `json:"nodeId"`
		Name   string `json:"name"`
		Value  string `json:"value"`
	}
	_ = json.Unmarshal(raw, &p)
	node := s.node(p.NodeID)
	if node == nil {
		return nil, fmt.Errorf("node not found")
	}
	attr, ok := domAttributeAXName(p.Name)
	if !ok {
		return nil, fmt.Errorf("unsupported AX-backed DOM attribute %q", p.Name)
	}
	if node.Ref == 0 {
		return nil, fmt.Errorf("node has no AX ref")
	}
	attrRef := corefoundation.CFStringCreateWithCString(0, attr, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attrRef))
	value := corefoundation.CFStringCreateWithCString(0, p.Value, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	if code := axuiautomation.AXUIElementSetAttributeValue(node.Ref, uintptr(attrRef), uintptr(value)); code != 0 {
		return nil, fmt.Errorf("set %s: ax error %d", attr, code)
	}
	s.updateNodeAttribute(node.NodeID, p.Name, p.Value)
	return map[string]any{}, nil
}

func (s *cdpServer) domFrameOwner(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		FrameID string `json:"frameId"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.FrameID != "" && p.FrameID != cdpFrameID {
		return nil, fmt.Errorf("unknown frameId %q", p.FrameID)
	}
	if err := s.ensureTreeBounded(4 * time.Second); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil, fmt.Errorf("node not found")
	}
	return map[string]any{"backendNodeId": domBackendNodeID(s.root.BackendID), "nodeId": domNodeID(s.root.NodeID)}, nil
}

func domAttributeAXName(name string) (string, bool) {
	switch name {
	case "title":
		return "AXTitle", true
	case "aria-label":
		return "AXDescription", true
	case "id":
		return "AXIdentifier", true
	case "role":
		return "AXRole", true
	default:
		return "", false
	}
}

func (s *cdpServer) updateNodeAttribute(nodeID int, name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.nodes[nodeID]
	if node == nil {
		return
	}
	switch name {
	case "title":
		node.Title = value
	case "aria-label":
		node.Description = value
	case "id":
		node.Identifier = value
	case "role":
		node.Role = value
		node.NodeName = value
		node.LocalName = strings.ToLower(strings.TrimPrefix(value, "AX"))
	}
}

func (s *cdpServer) node(id int) *cdpNode {
	id = axNodeID(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodes[id]
}

func (s *cdpServer) backendNode(id int) *cdpNode {
	id = axBackendNodeID(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend[id]
}

func (s *cdpServer) nodeFromParams(raw json.RawMessage) *cdpNode {
	var p struct {
		NodeID        int    `json:"nodeId"`
		BackendNodeID int    `json:"backendNodeId"`
		ObjectID      string `json:"objectId"`
	}
	_ = json.Unmarshal(raw, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.NodeID != 0 {
		return s.nodes[axNodeID(p.NodeID)]
	}
	if p.BackendNodeID != 0 {
		return s.backend[axBackendNodeID(p.BackendNodeID)]
	}
	if strings.HasPrefix(p.ObjectID, "node:") {
		id, err := strconv.Atoi(strings.TrimPrefix(p.ObjectID, "node:"))
		if err == nil {
			return s.nodes[axNodeID(id)]
		}
	}
	return nil
}

func (s *cdpServer) documentNodeParam(raw json.RawMessage) bool {
	var p struct {
		NodeID        int `json:"nodeId"`
		BackendNodeID int `json:"backendNodeId"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.NodeID == cdpDocumentNodeID || p.BackendNodeID == cdpDocumentBackendID
}

func (s *cdpServer) querySelector(rootID int, selector string) int {
	ids := s.querySelectorAll(rootID, selector)
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func (s *cdpServer) querySelectorAll(rootID int, selector string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.root
	if rootID != 0 && rootID != cdpDocumentNodeID {
		root = s.nodes[axNodeID(rootID)]
	}
	var ids []int
	walkDOM(root, func(n *cdpNode) {
		if domSelectorMatches(n, selector) {
			ids = append(ids, domNodeID(n.NodeID))
		}
	})
	return ids
}

func (s *cdpServer) searchDOM(query string) []int {
	query = strings.TrimSpace(query)
	if query == "" || query == "*" {
		return s.allDOMNodeIDs()
	}
	needle := strings.ToLower(query)
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int
	walkDOM(s.root, func(n *cdpNode) {
		if strings.Contains(strings.ToLower(domSearchText(n)), needle) {
			ids = append(ids, domNodeID(n.NodeID))
		}
	})
	return ids
}

func (s *cdpServer) allDOMNodeIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int, 0, len(s.nodes))
	if s.root != nil {
		ids = append(ids, domNodeID(s.root.NodeID))
	}
	for id := range s.nodes {
		if s.root != nil && id == s.root.NodeID {
			continue
		}
		ids = append(ids, domNodeID(id))
	}
	return ids
}

func (s *cdpServer) nodeForLocation(x, y float64) *cdpNode {
	p := s.viewportAbsolutePoint(x, y)
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *cdpNode
	var bestArea float64
	walkDOM(s.root, func(n *cdpNode) {
		if !rectContains(n.Bounds, p.X, p.Y) {
			return
		}
		area := n.Bounds.Width * n.Bounds.Height
		if area <= 0 {
			return
		}
		if best == nil || area <= bestArea {
			best = n
			bestArea = area
		}
	})
	return best
}

func (s *cdpServer) viewportRelativeRect(r axRect) axRect {
	v := s.viewportBounds()
	r.X -= v.X
	r.Y -= v.Y
	return r
}

func (s *cdpServer) viewportAbsoluteRect(r axRect) axRect {
	v := s.viewportBounds()
	r.X += v.X
	r.Y += v.Y
	return r
}

func (s *cdpServer) viewportAbsolutePoint(x, y float64) axRect {
	v := s.viewportBounds()
	return axRect{X: x + v.X, Y: y + v.Y}
}

func (s *cdpServer) dispatchMouseEvent(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		Type       string  `json:"type"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
		Button     string  `json:"button"`
		ClickCount int     `json:"clickCount"`
	}
	_ = json.Unmarshal(raw, &p)
	node := s.nodeForLocation(p.X, p.Y)
	if node == nil {
		return map[string]any{}, nil
	}
	switch p.Type {
	case "mouseMoved":
		go highlightAXRect(node.Bounds, 500*time.Millisecond)
	case "mousePressed", "mouseReleased":
		if p.Button != "" && p.Button != "left" {
			return nil, fmt.Errorf("unsupported mouse button %q", p.Button)
		}
		if p.Type == "mouseReleased" || p.ClickCount > 0 {
			if err := performAXPress(node.Ref); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unsupported mouse event type %q", p.Type)
	}
	return map[string]any{}, nil
}

func (s *cdpServer) dispatchKeyEvent(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		Type                  string `json:"type"`
		Text                  string `json:"text"`
		UnmodifiedText        string `json:"unmodifiedText"`
		Key                   string `json:"key"`
		NativeVirtualKeyCode  int    `json:"nativeVirtualKeyCode"`
		WindowsVirtualKeyCode int    `json:"windowsVirtualKeyCode"`
	}
	_ = json.Unmarshal(raw, &p)
	keyDown := false
	switch p.Type {
	case "keyDown", "rawKeyDown", "char":
		keyDown = true
	case "keyUp":
		keyDown = false
	default:
		return nil, fmt.Errorf("unsupported key event type %q", p.Type)
	}
	charCode := firstRuneCode(firstNonEmpty(p.Text, p.UnmodifiedText, p.Key))
	virtualKey := firstPositive(p.NativeVirtualKeyCode, p.WindowsVirtualKeyCode)
	if virtualKey == 0 && charCode == 0 {
		return nil, fmt.Errorf("key event requires nativeVirtualKeyCode, windowsVirtualKeyCode, text, unmodifiedText, or key")
	}
	ref, err := s.axRootRef()
	if err != nil {
		return nil, err
	}
	if code := ax.postKeyboardEvent(ref, uint16(charCode), uint16(virtualKey), keyDown); code != 0 {
		return nil, fmt.Errorf("post keyboard event: ax error %d", code)
	}
	return map[string]any{}, nil
}

func firstRuneCode(s string) int {
	for _, r := range s {
		return int(r)
	}
	return 0
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func performAXPress(ref axuiautomation.AXUIElementRef) error {
	if ref == 0 {
		return fmt.Errorf("node has no AX ref")
	}
	action := corefoundation.CFStringCreateWithCString(0, "AXPress", uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(action))
	if code := axuiautomation.AXUIElementPerformAction(ref, uintptr(action)); code != 0 {
		return fmt.Errorf("perform AXPress: ax error %d", code)
	}
	return nil
}

func (s *cdpServer) searchResults(searchID string, from, to int) []int {
	s.mu.Lock()
	ids := append([]int(nil), s.searches[searchID]...)
	s.mu.Unlock()
	if from < 0 {
		from = 0
	}
	if to <= 0 || to > len(ids) {
		to = len(ids)
	}
	if from > to {
		from = to
	}
	return ids[from:to]
}

func walkDOM(n *cdpNode, fn func(*cdpNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, child := range n.Children {
		walkDOM(child, fn)
	}
}

func domSelectorMatches(n *cdpNode, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == "*" {
		return true
	}
	if strings.HasPrefix(selector, "#") {
		return n.Identifier == strings.TrimPrefix(selector, "#")
	}
	if strings.HasPrefix(selector, "[") && strings.HasSuffix(selector, "]") {
		name, value, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(selector, "["), "]"), "=")
		if !ok {
			return false
		}
		value = strings.Trim(value, `"'`)
		switch strings.TrimSpace(name) {
		case "role":
			return n.Role == value
		case "title":
			return n.Title == value
		case "id":
			return n.Identifier == value
		case "data-ax-node-id":
			return strconv.Itoa(n.NodeID) == value
		}
		return false
	}
	selector = strings.ToLower(selector)
	return selector == strings.ToLower(n.LocalName) || selector == strings.ToLower(n.NodeName) || selector == strings.ToLower(n.Role)
}

func domSearchText(n *cdpNode) string {
	return strings.Join([]string{
		strconv.Itoa(n.NodeID),
		n.NodeName,
		n.LocalName,
		n.Role,
		n.Title,
		n.Description,
		n.Identifier,
	}, "\n")
}

func rectContains(r axRect, x, y float64) bool {
	return r.Width > 0 && r.Height > 0 && x >= r.X && y >= r.Y && x <= r.X+r.Width && y <= r.Y+r.Height
}

func (s *cdpServer) targetInfo(attached bool) map[string]any {
	return map[string]any{"targetId": s.currentTargetID(), "type": s.currentTargetType(), "title": s.targetTitle(), "url": s.previewURL(""), "attached": attached, "canAccessOpener": false, "canScreencast": s.currentTargetType() == "page"}
}

func (s *cdpServer) targetInfos(attached bool) []map[string]any {
	infos := []map[string]any{
		{"targetId": cdpTargetID, "type": "page", "title": "macOS Accessibility", "url": axcdpPreviewURL("", ""), "attached": attached, "canAccessOpener": false, "canScreencast": true},
		{"targetId": cdpNodeTargetID, "type": "node", "title": "macOS Accessibility", "url": axcdpPreviewURL("", ""), "attached": attached, "canAccessOpener": false},
	}
	if s.appArg != "" {
		return []map[string]any{s.targetInfo(attached)}
	}
	if s.browser != nil {
		for _, info := range s.browser.targetInfos(attached) {
			infos = append(infos, info)
		}
	}
	for _, app := range runningApps() {
		for _, win := range appWindows(app) {
			infos = append(infos, windowTargetInfo(app, win, attached))
		}
	}
	return infos
}

func (s *cdpServer) sendDiscoveredTargetEvents(conn *websocket.Conn, attached bool, skipID string) {
	start := time.Now()
	infos := s.targetInfos(attached)
	sent := 0
	for _, info := range infos {
		if id, _ := info["targetId"].(string); id != "" && id == skipID {
			continue
		}
		if !s.sendEvent(conn, "Target.targetCreated", map[string]any{"targetInfo": info}) {
			return
		}
		sent++
	}
	slog.Info("cdp target discovery complete", "targets", len(infos), "sent", sent, "duration", time.Since(start))
}

func appTargetInfo(app runningApp, attached bool) map[string]any {
	return map[string]any{
		"targetId":        appTargetID("page", app.PID),
		"type":            "page",
		"title":           app.Name,
		"url":             axcdpPreviewURL("", strconv.Itoa(app.PID)),
		"attached":        attached,
		"canAccessOpener": false,
		"canScreencast":   true,
	}
}

func windowTargetInfo(app runningApp, win ocrwindow.Window, attached bool) map[string]any {
	return map[string]any{
		"targetId":        windowTargetID(app.PID, win.ID),
		"type":            "page",
		"title":           windowTargetTitle(app, win),
		"url":             axcdpWindowPreviewURL("", app.PID, win.ID),
		"attached":        attached,
		"canAccessOpener": false,
		"canScreencast":   true,
	}
}

func (s *cdpServer) currentTargetID() string {
	if pid, ok := s.appPID(); ok && s.windowID != 0 {
		return windowTargetID(pid, s.windowID)
	}
	if pid, ok := s.appPID(); ok {
		return appTargetID(s.currentTargetType(), pid)
	}
	if s.currentTargetType() == "node" {
		return cdpNodeTargetID
	}
	return cdpTargetID
}

func (s *cdpServer) currentTargetType() string {
	if s.targetType == "node" {
		return "node"
	}
	return "page"
}

func (s *cdpServer) targetTitle() string {
	if s.windowID != 0 {
		if s.windowName != "" {
			return s.windowName
		}
		if pid, ok := s.appPID(); ok {
			if win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), s.windowID); err == nil && win.Title != "" {
				return win.Title
			}
		}
		return "window " + strconv.FormatUint(uint64(s.windowID), 10)
	}
	if pid, ok := s.appPID(); ok {
		if name := appNameForPID(pid); name != "" {
			return name
		}
		return "pid " + strconv.Itoa(pid)
	}
	if s.appArg != "" {
		return s.appArg
	}
	return "macOS Accessibility"
}

func (s *cdpServer) targetURL() string {
	if pid, ok := s.appPID(); ok && s.windowID != 0 {
		return "file://axcdp/window/" + strconv.Itoa(pid) + "/" + strconv.FormatUint(uint64(s.windowID), 10)
	}
	if pid, ok := s.appPID(); ok {
		return "file://axcdp/app/" + strconv.Itoa(pid)
	}
	if s.appArg != "" {
		return "file://axcdp/app/" + s.appArg
	}
	return "file://axcdp/accessibility"
}

func (s *cdpServer) previewURL(host string) string {
	if pid, ok := s.appPID(); ok && s.windowID != 0 {
		return axcdpWindowPreviewURL(firstNonEmpty(host, s.httpHost()), pid, s.windowID)
	}
	if pid, ok := s.appPID(); ok {
		return axcdpPreviewURL(firstNonEmpty(host, s.httpHost()), strconv.Itoa(pid))
	}
	return axcdpPreviewURL(firstNonEmpty(host, s.httpHost()), "")
}

func axcdpWindowPreviewURL(host string, pid int, windowID uint32) string {
	if host == "" {
		host = "127.0.0.1:9221"
	}
	return "http://" + host + "/axcdp/window/" + strconv.Itoa(pid) + "/" + strconv.FormatUint(uint64(windowID), 10)
}

func axcdpPreviewURL(host, app string) string {
	if host == "" {
		host = "127.0.0.1:9221"
	}
	if app != "" {
		return "http://" + host + "/axcdp/app/" + app
	}
	return "http://" + host + "/axcdp/accessibility"
}

func (s *cdpServer) httpHost() string {
	if s.addr == "" || strings.HasPrefix(s.addr, ":") {
		return "127.0.0.1" + firstNonEmpty(s.addr, ":9221")
	}
	if strings.HasPrefix(s.addr, "[::]:") {
		return "127.0.0.1:" + strings.TrimPrefix(s.addr, "[::]:")
	}
	return s.addr
}

func (s *cdpServer) appPID() (int, bool) {
	pid, err := strconv.Atoi(s.appArg)
	return pid, err == nil && pid > 0
}

func (s *cdpServer) activateTarget() error {
	pid, ok := s.appPID()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	script := fmt.Sprintf(`tell application "System Events" to set frontmost of first process whose unix id is %d to true`, pid)
	if err := exec.CommandContext(ctx, "osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("activate pid %d: %w", pid, err)
	}
	return nil
}

func (s *cdpServer) pageNavigate(conn *websocket.Conn, sessionID string, raw json.RawMessage) (map[string]any, error) {
	var p struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &p)
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, fmt.Errorf("parse navigation URL: %w", err)
	}
	pid, windowID, isWindow := previewPathWindow(u.Path)
	if !isWindow {
		var ok bool
		pid, ok = previewPathPID(u.Path)
		if !ok {
			return nil, cdpMethodNotFoundError{Method: "Page.navigate"}
		}
	}
	old := s.appArg
	slog.Info("cdp navigate", "from", old, "to", pid, "window_id", windowID, "url", p.URL)
	s.stopDOMWatch(sessionID)
	s.stopScreencast(sessionID)
	s.mu.Lock()
	s.appArg = strconv.Itoa(pid)
	s.targetType = "page"
	s.windowID = windowID
	s.windowName = ""
	if isWindow {
		if win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), windowID); err == nil {
			s.windowName = win.Title
		}
	}
	s.root = nil
	s.nodes = make(map[int]*cdpNode)
	s.backend = make(map[int]*cdpNode)
	s.searches = make(map[string][]int)
	s.mu.Unlock()
	s.sendSessionEvent(conn, sessionID, "Page.frameStartedLoading", map[string]any{"frameId": cdpFrameID})
	if err := s.refreshTreeBounded(4 * time.Second); err != nil {
		return nil, err
	}
	s.sendSessionEvent(conn, sessionID, "Page.frameNavigated", map[string]any{"frame": s.frame()})
	s.sendSessionEvent(conn, sessionID, "DOM.documentUpdated", map[string]any{})
	s.sendSessionEvent(conn, sessionID, "Page.domContentEventFired", map[string]any{"timestamp": float64(time.Now().UnixNano()) / 1e9})
	s.sendSessionEvent(conn, sessionID, "Page.loadEventFired", map[string]any{"timestamp": float64(time.Now().UnixNano()) / 1e9})
	s.sendSessionEvent(conn, sessionID, "Page.frameStoppedLoading", map[string]any{"frameId": cdpFrameID})
	s.startDOMWatch(conn, sessionID)
	slog.Info("cdp navigate complete", "from", old, "to", s.appArg, "title", s.targetTitle(), "nodes", len(s.nodes))
	return map[string]any{"frameId": cdpFrameID}, nil
}

func (s *cdpServer) runtimeEvaluate(raw json.RawMessage) map[string]any {
	var p struct {
		Expression    string `json:"expression"`
		ReturnByValue bool   `json:"returnByValue"`
	}
	_ = json.Unmarshal(raw, &p)
	expr := strings.TrimSpace(p.Expression)
	rt := goja.New()
	s.installAXRuntime(rt)
	value, err := rt.RunString(expr)
	if err != nil {
		slog.Info("runtime evaluate exception", "target", s.appArg, "expression", expr, "err", err)
		return map[string]any{
			"exceptionDetails": map[string]any{
				"text": err.Error(),
				"exception": map[string]any{
					"type":        "object",
					"subtype":     "error",
					"description": err.Error(),
				},
			},
			"result": map[string]any{"type": "undefined"},
		}
	}
	slog.Info("runtime evaluate", "target", s.appArg, "expression", expr, "result", value.String())
	return map[string]any{"result": s.remoteObjectForValue(rt, value, p.ReturnByValue)}
}

func (s *cdpServer) installAXRuntime(rt *goja.Runtime) {
	root := s.root
	_ = s.ensureTreeBounded(4 * time.Second)
	s.mu.Lock()
	if s.root != nil {
		root = s.root
	}
	s.mu.Unlock()
	doc := rt.NewObject()
	_ = doc.Set("title", s.targetTitle())
	_ = doc.Set("URL", s.previewURL(""))
	_ = doc.Set("location", map[string]any{"href": s.previewURL("")})
	if root != nil {
		_ = doc.Set("root", s.jsAXNode(rt, root))
	}
	_ = rt.Set("document", doc)
	_ = rt.Set("location", map[string]any{"href": s.previewURL("")})

	ax := rt.NewObject()
	_ = ax.Set("root", func() goja.Value {
		if root == nil {
			return goja.Null()
		}
		return s.jsAXNode(rt, root)
	})
	_ = ax.Set("query", func(call goja.FunctionCall) goja.Value {
		if root == nil || len(call.Arguments) == 0 {
			return goja.Null()
		}
		id := strings.TrimSpace(call.Arguments[0].String())
		if id == "" {
			return goja.Null()
		}
		var found *cdpNode
		walkDOM(root, func(n *cdpNode) {
			if found == nil && (n.Role == id || n.Title == id || n.Identifier == id || n.NodeName == id || strconv.Itoa(domNodeID(n.NodeID)) == id) {
				found = n
			}
		})
		if found == nil {
			return goja.Null()
		}
		return s.jsAXNode(rt, found)
	})
	_ = ax.Set("all", func(call goja.FunctionCall) goja.Value {
		var out []any
		if root == nil {
			return rt.ToValue(out)
		}
		role := ""
		if len(call.Arguments) > 0 {
			role = call.Arguments[0].String()
		}
		walkDOM(root, func(n *cdpNode) {
			if role == "" || n.Role == role || n.NodeName == role {
				out = append(out, s.jsAXNode(rt, n))
			}
		})
		return rt.ToValue(out)
	})
	_ = rt.Set("ax", ax)
}

func (s *cdpServer) jsAXNode(rt *goja.Runtime, n *cdpNode) *goja.Object {
	obj := rt.NewObject()
	_ = obj.Set("nodeId", domNodeID(n.NodeID))
	_ = obj.Set("backendNodeId", domBackendNodeID(n.BackendID))
	_ = obj.Set("role", n.Role)
	_ = obj.Set("title", n.Title)
	_ = obj.Set("description", n.Description)
	_ = obj.Set("identifier", n.Identifier)
	_ = obj.Set("bounds", map[string]any{"x": n.Bounds.X, "y": n.Bounds.Y, "width": n.Bounds.Width, "height": n.Bounds.Height})
	_ = obj.Set("children", func() []any {
		if !n.ChildrenReady {
			_ = s.expandNodeChildren(n, 1)
		}
		children := make([]any, 0, len(n.Children))
		for _, child := range n.Children {
			children = append(children, s.jsAXNode(rt, child))
		}
		return children
	})
	_ = obj.Set("highlight", func() bool {
		go highlightAXRect(n.Bounds, 1500*time.Millisecond)
		return true
	})
	return obj
}

func (s *cdpServer) remoteObjectForValue(rt *goja.Runtime, value goja.Value, returnByValue bool) map[string]any {
	if value == nil || goja.IsUndefined(value) {
		return map[string]any{"type": "undefined"}
	}
	if goja.IsNull(value) {
		return map[string]any{"type": "object", "subtype": "null", "value": nil}
	}
	exported := value.Export()
	switch v := exported.(type) {
	case bool:
		return map[string]any{"type": "boolean", "value": v}
	case string:
		return remoteString(v)
	case int:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case int8:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case int16:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case int32:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case int64:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case uint:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case uint8:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case uint16:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case uint32:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case uint64:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case float32:
		return map[string]any{"type": "number", "value": float64(v), "description": value.String()}
	case float64:
		return map[string]any{"type": "number", "value": v, "description": value.String()}
	}
	if obj := value.ToObject(rt); obj != nil {
		if idv := obj.Get("nodeId"); idv != nil && !goja.IsUndefined(idv) {
			return map[string]any{"type": "object", "subtype": "node", "objectId": "node:" + idv.String(), "description": obj.Get("role").String()}
		}
	}
	if returnByValue {
		return map[string]any{"type": "object", "value": exported, "description": value.String()}
	}
	return map[string]any{"type": "object", "description": value.String()}
}

func (s *cdpServer) runtimeGetProperties(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		ObjectID string `json:"objectId"`
	}
	_ = json.Unmarshal(raw, &p)
	if strings.HasPrefix(p.ObjectID, "bounds:") {
		return s.runtimeBoundsProperties(p.ObjectID)
	}
	if !strings.HasPrefix(p.ObjectID, "node:") {
		return map[string]any{"result": []any{}}, nil
	}
	id, err := strconv.Atoi(strings.TrimPrefix(p.ObjectID, "node:"))
	if err != nil {
		return nil, fmt.Errorf("parse node object id %q: %w", p.ObjectID, err)
	}
	node := s.node(id)
	if node == nil {
		return nil, fmt.Errorf("node not found")
	}
	props := []any{
		runtimeProperty("nodeId", map[string]any{"type": "number", "value": domNodeID(node.NodeID)}),
		runtimeProperty("backendNodeId", map[string]any{"type": "number", "value": domBackendNodeID(node.BackendID)}),
		runtimeProperty("role", remoteString(node.Role)),
		runtimeProperty("title", remoteString(node.Title)),
		runtimeProperty("description", remoteString(node.Description)),
		runtimeProperty("identifier", remoteString(node.Identifier)),
		runtimeProperty("bounds", map[string]any{"type": "object", "objectId": fmt.Sprintf("bounds:%d", domNodeID(node.NodeID)), "description": fmt.Sprintf("{x:%g,y:%g,width:%g,height:%g}", node.Bounds.X, node.Bounds.Y, node.Bounds.Width, node.Bounds.Height)}),
	}
	return map[string]any{"result": props}, nil
}

func (s *cdpServer) runtimeBoundsProperties(objectID string) (map[string]any, error) {
	id, err := strconv.Atoi(strings.TrimPrefix(objectID, "bounds:"))
	if err != nil {
		return nil, fmt.Errorf("parse bounds object id %q: %w", objectID, err)
	}
	node := s.node(id)
	if node == nil {
		return nil, fmt.Errorf("node not found")
	}
	props := []any{
		runtimeProperty("x", map[string]any{"type": "number", "value": node.Bounds.X}),
		runtimeProperty("y", map[string]any{"type": "number", "value": node.Bounds.Y}),
		runtimeProperty("width", map[string]any{"type": "number", "value": node.Bounds.Width}),
		runtimeProperty("height", map[string]any{"type": "number", "value": node.Bounds.Height}),
	}
	return map[string]any{"result": props}, nil
}

func runtimeProperty(name string, value map[string]any) map[string]any {
	return map[string]any{"name": name, "value": value, "enumerable": true, "configurable": true}
}

func quotedLiteral(expr string) (string, bool) {
	if len(expr) < 2 {
		return "", false
	}
	if (expr[0] != '"' || expr[len(expr)-1] != '"') && (expr[0] != '\'' || expr[len(expr)-1] != '\'') {
		return "", false
	}
	if expr[0] == '\'' {
		return strings.ReplaceAll(expr[1:len(expr)-1], `\'`, `'`), true
	}
	var value string
	if err := json.Unmarshal([]byte(expr), &value); err != nil {
		return "", false
	}
	return value, true
}

func (s *cdpServer) frame() map[string]any {
	return map[string]any{
		"id":             cdpFrameID,
		"loaderId":       "axcdp-loader",
		"url":            s.previewURL(""),
		"securityOrigin": "http://" + s.httpHost(),
		"mimeType":       "text/html",
	}
}

func screencastFrameMetadata(r axRect) map[string]any {
	return map[string]any{
		"offsetTop":         0,
		"pageScaleFactor":   1,
		"deviceWidth":       r.Width,
		"deviceHeight":      r.Height,
		"deviceScaleFactor": 1,
		"scrollOffsetX":     0,
		"scrollOffsetY":     0,
		"timestamp":         float64(time.Now().UnixNano()) / 1e9,
	}
}

type screenshotOptions struct {
	Format  string `json:"format"`
	Quality int    `json:"quality"`
}

type screencastOptions struct {
	Format        string `json:"format"`
	Quality       int    `json:"quality"`
	MaxWidth      int    `json:"maxWidth"`
	MaxHeight     int    `json:"maxHeight"`
	EveryNthFrame int    `json:"everyNthFrame"`
}

func parseScreenshotOptions(raw json.RawMessage) screenshotOptions {
	opts := screenshotOptions{Format: "png"}
	_ = json.Unmarshal(raw, &opts)
	opts.Format = normalizedScreenshotFormat(opts.Format, "png")
	opts.Quality = normalizedJPEGQuality(opts.Quality)
	return opts
}

func parseScreencastOptions(raw json.RawMessage) screencastOptions {
	opts := screencastOptions{Format: "jpeg", Quality: 80, EveryNthFrame: 1}
	_ = json.Unmarshal(raw, &opts)
	opts.Format = normalizedScreenshotFormat(opts.Format, "jpeg")
	opts.Quality = normalizedJPEGQuality(opts.Quality)
	if opts.EveryNthFrame <= 0 {
		opts.EveryNthFrame = 1
	}
	return opts
}

func normalizedScreenshotFormat(format, def string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "jpeg"
	case "png":
		return "png"
	case "webp":
		// The AX screenshot path captures PNG. DevTools accepts jpeg and png
		// frames; use jpeg when asked for webp rather than advertising fake
		// WebP support.
		return "jpeg"
	case "":
		return def
	default:
		return def
	}
}

func normalizedJPEGQuality(quality int) int {
	if quality <= 0 {
		return 80
	}
	if quality > 100 {
		return 100
	}
	return quality
}

func encodeScreenshot(pngData, format string, quality int) string {
	return encodeScreenshotScaled(pngData, format, quality, 0, 0)
}

func encodeScreenshotScaled(pngData, format string, quality, maxWidth, maxHeight int) string {
	if format != "jpeg" {
		return pngData
	}
	data, err := base64.StdEncoding.DecodeString(pngData)
	if err != nil {
		slog.Warn("decode png screenshot for jpeg failed", "err", err)
		return pngData
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		slog.Warn("decode screenshot image failed", "err", err)
		return pngData
	}
	if maxWidth > 0 || maxHeight > 0 {
		img = scaleImage(img, maxWidth, maxHeight)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: normalizedJPEGQuality(quality)}); err != nil {
		slog.Warn("encode jpeg screenshot failed", "err", err)
		return pngData
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func scaleImage(img image.Image, maxWidth, maxHeight int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}
	if maxWidth <= 0 {
		maxWidth = w
	}
	if maxHeight <= 0 {
		maxHeight = h
	}
	if w <= maxWidth && h <= maxHeight {
		return img
	}
	scale := min(float64(maxWidth)/float64(w), float64(maxHeight)/float64(h))
	if scale <= 0 || scale >= 1 {
		return img
	}
	dstW := max(1, int(float64(w)*scale))
	dstH := max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

func (s *cdpServer) startScreencast(conn *websocket.Conn, connID uint64, sessionID string, opts screencastOptions) {
	if conn == nil {
		return
	}
	if opts.Format == "" {
		opts = parseScreencastOptions(nil)
	}
	requestedMaxWidth, requestedMaxHeight := opts.MaxWidth, opts.MaxHeight
	if s.castMaxDim > 0 {
		opts.MaxWidth = positiveMin(opts.MaxWidth, s.castMaxDim)
		opts.MaxHeight = positiveMin(opts.MaxHeight, s.castMaxDim)
	}
	slog.Info("start screencast", "conn", connID, "target", s.appArg, "session", sessionID, "format", opts.Format, "quality", opts.Quality, "requested_max_width", requestedMaxWidth, "requested_max_height", requestedMaxHeight, "max_width", opts.MaxWidth, "max_height", opts.MaxHeight, "every_nth_frame", opts.EveryNthFrame)
	stop := make(chan struct{})
	acks := make(chan int, 4)
	key := firstNonEmpty(sessionID, "root")
	s.mu.Lock()
	if s.casts == nil {
		s.casts = make(map[string]chan struct{})
	}
	if s.castAcks == nil {
		s.castAcks = make(map[string]chan int)
	}
	if old := s.casts[key]; old != nil {
		close(old)
	}
	s.casts[key] = stop
	s.castAcks[key] = acks
	s.mu.Unlock()

	go func() {
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		frameID := 1
		if !s.sendSessionEvent(conn, sessionID, "Page.screencastVisibilityChanged", map[string]any{"visible": true}) {
			return
		}
		for {
			select {
			case <-stop:
				slog.Info("stop screencast", "conn", connID, "target", s.appArg, "session", sessionID)
				return
			case <-timer.C:
				r := s.viewportBounds()
				start := time.Now()
				png := s.captureTargetPNG()
				data := encodeScreenshotScaled(png, opts.Format, opts.Quality, opts.MaxWidth, opts.MaxHeight)
				slog.Info("send screencast frame", "conn", connID, "target", s.appArg, "session", sessionID, "frame_id", frameID, "format", opts.Format, "width", r.Width, "height", r.Height, "data_chars", len(data), "png_chars", len(png), "duration", time.Since(start))
				if !s.sendSessionEvent(conn, sessionID, "Page.screencastFrame", map[string]any{
					"data":      data,
					"metadata":  screencastFrameMetadata(r),
					"sessionId": frameID,
				}) {
					s.stopScreencast(sessionID)
					return
				}
				frameID++
				select {
				case <-stop:
					slog.Info("stop screencast", "conn", connID, "target", s.appArg, "session", sessionID)
					return
				case ackID := <-acks:
					slog.Debug("screencast frame acknowledged", "conn", connID, "target", s.appArg, "session", sessionID, "frame_id", ackID)
					timer.Reset(time.Second)
				case <-time.After(2500 * time.Millisecond):
					slog.Info("screencast frame unacked", "conn", connID, "target", s.appArg, "session", sessionID, "frame_id", frameID-1)
					timer.Reset(2500 * time.Millisecond)
				}
			}
		}
	}()
}

func positiveMin(value, cap int) int {
	if cap <= 0 {
		return value
	}
	if value <= 0 || value > cap {
		return cap
	}
	return value
}

func (s *cdpServer) stopScreencast(sessionID string) {
	key := firstNonEmpty(sessionID, "root")
	s.mu.Lock()
	stop := s.casts[key]
	delete(s.casts, key)
	delete(s.castAcks, key)
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (s *cdpServer) noteScreencastAck(sessionID string, frameID int) {
	key := firstNonEmpty(sessionID, "root")
	s.mu.Lock()
	acks := s.castAcks[key]
	s.mu.Unlock()
	if acks == nil {
		return
	}
	select {
	case acks <- frameID:
	default:
	}
}

func (s *cdpServer) stopDOMWatch(sessionID string) {
	key := firstNonEmpty(sessionID, "root")
	s.mu.Lock()
	stop := s.domWatch[key]
	delete(s.domWatch, key)
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (s *cdpServer) startDOMWatch(conn *websocket.Conn, sessionID string) {
	if conn == nil {
		return
	}
	key := firstNonEmpty(sessionID, "root")
	stop := make(chan struct{})
	s.mu.Lock()
	if s.domWatch == nil {
		s.domWatch = make(map[string]chan struct{})
	}
	if old := s.domWatch[key]; old != nil {
		close(old)
	}
	s.domWatch[key] = stop
	last := s.treeFingerprintLocked()
	s.mu.Unlock()

	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				if err := s.refreshTreeBounded(4 * time.Second); err == nil {
					s.mu.Lock()
					next := s.treeFingerprintLocked()
					changed := next != "" && next != last
					if changed {
						last = next
					}
					s.mu.Unlock()
					if changed {
						s.sendSessionEvent(conn, sessionID, "DOM.documentUpdated", map[string]any{})
					}
				}
				timer.Reset(2 * time.Second)
			}
		}
	}()
}

func (s *cdpServer) treeFingerprintLocked() string {
	if s.root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*cdpNode)
	walk = func(n *cdpNode) {
		if n == nil {
			return
		}
		b.WriteString(n.Role)
		b.WriteByte('\x00')
		b.WriteString(n.Title)
		b.WriteByte('\x00')
		b.WriteString(n.Identifier)
		b.WriteByte('\x00')
		b.WriteString(fmt.Sprintf("%.0f,%.0f,%.0f,%.0f", n.Bounds.X, n.Bounds.Y, n.Bounds.Width, n.Bounds.Height))
		b.WriteByte('\n')
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(s.root)
	return b.String()
}

func remoteString(value string) map[string]any {
	return map[string]any{"type": "string", "value": value}
}

func (s *cdpServer) browserWindowBounds() map[string]any {
	r := s.viewportBounds()
	return map[string]any{
		"left":        r.X,
		"top":         r.Y,
		"width":       r.Width,
		"height":      r.Height,
		"windowState": "normal",
	}
}

func (s *cdpServer) viewportBounds() axRect {
	if r, ok := s.targetWindowBounds(); ok {
		return r
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if window := firstNodeWithBounds(s.root, "AXWindow"); window != nil {
		return window.Bounds
	}
	if largest := largestNodeWithBounds(s.root); largest != nil {
		return largest.Bounds
	}
	if s.root != nil && s.root.Bounds.Width > 0 && s.root.Bounds.Height > 0 {
		return s.root.Bounds
	}
	return axRect{Width: 1024, Height: 768}
}

func (s *cdpServer) targetWindowBounds() (axRect, bool) {
	if s.windowID == 0 {
		return axRect{}, false
	}
	pid, ok := s.appPID()
	if !ok {
		return axRect{}, false
	}
	win, err := ocrwindow.FindWindowID(strconv.Itoa(pid), s.windowID)
	if err != nil {
		slog.Debug("find target window bounds failed", "app", s.appArg, "window_id", s.windowID, "err", err)
		return axRect{}, false
	}
	if win.W <= 0 || win.H <= 0 {
		return axRect{}, false
	}
	return axRect{X: win.X, Y: win.Y, Width: win.W, Height: win.H}, true
}

func firstNodeWithBounds(n *cdpNode, role string) *cdpNode {
	var found *cdpNode
	walkDOM(n, func(candidate *cdpNode) {
		if found != nil {
			return
		}
		if candidate.Role == role && candidate.Bounds.Width > 0 && candidate.Bounds.Height > 0 {
			found = candidate
		}
	})
	return found
}

func largestNodeWithBounds(n *cdpNode) *cdpNode {
	var found *cdpNode
	var area float64
	walkDOM(n, func(candidate *cdpNode) {
		if candidate.Bounds.Width <= 0 || candidate.Bounds.Height <= 0 {
			return
		}
		candidateArea := candidate.Bounds.Width * candidate.Bounds.Height
		if found == nil || candidateArea > area {
			found = candidate
			area = candidateArea
		}
	})
	return found
}

func (s *cdpServer) accessibilityNodes() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]any, 0, len(s.nodes))
	walkDOM(s.root, func(n *cdpNode) {
		out = append(out, accessibilityNode(n))
	})
	return out
}

func (s *cdpServer) rootAccessibilityNode() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	return accessibilityNode(s.root)
}

func (s *cdpServer) partialAccessibilityNodes(raw json.RawMessage) []any {
	var p struct {
		NodeID         int  `json:"nodeId"`
		BackendNodeID  int  `json:"backendNodeId"`
		FetchRelatives bool `json:"fetchRelatives"`
	}
	_ = json.Unmarshal(raw, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.root
	if p.NodeID != 0 {
		node = s.nodes[p.NodeID]
	}
	if p.BackendNodeID != 0 {
		node = s.backend[p.BackendNodeID]
	}
	if node == nil {
		return nil
	}
	nodes := []*cdpNode{node}
	if p.FetchRelatives {
		if parent := s.nodes[node.ParentID]; parent != nil {
			nodes = append(nodes, parent)
		}
		nodes = append(nodes, node.Children...)
	}
	out := make([]any, 0, len(nodes))
	seen := make(map[int]bool)
	for _, n := range nodes {
		if n == nil || seen[n.NodeID] {
			continue
		}
		seen[n.NodeID] = true
		out = append(out, accessibilityNode(n))
	}
	return out
}

func (s *cdpServer) queryAccessibilityNodes(raw json.RawMessage) []any {
	var p struct {
		NodeID         int    `json:"nodeId"`
		BackendNodeID  int    `json:"backendNodeId"`
		AccessibleName string `json:"accessibleName"`
		Role           string `json:"role"`
	}
	_ = json.Unmarshal(raw, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.root
	if p.NodeID != 0 {
		root = s.nodes[p.NodeID]
	}
	if p.BackendNodeID != 0 {
		root = s.backend[p.BackendNodeID]
	}
	var out []any
	walkDOM(root, func(n *cdpNode) {
		if p.Role != "" && n.Role != p.Role {
			return
		}
		if p.AccessibleName != "" && firstNonEmpty(n.Title, n.Description, n.Role) != p.AccessibleName {
			return
		}
		out = append(out, accessibilityNode(n))
	})
	return out
}

func (s *cdpServer) accessibilityNodeAndAncestors(raw json.RawMessage) []any {
	node := s.accessibilityNodeFromParams(raw)
	if node == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []any
	for n := node; n != nil; n = s.nodes[n.ParentID] {
		out = append(out, accessibilityNode(n))
	}
	return out
}

func (s *cdpServer) childAccessibilityNodes(raw json.RawMessage) []any {
	node := s.accessibilityNodeFromParams(raw)
	if node == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]any, 0, len(node.Children))
	for _, child := range node.Children {
		out = append(out, accessibilityNode(child))
	}
	return out
}

func (s *cdpServer) accessibilityNodeFromParams(raw json.RawMessage) *cdpNode {
	var p struct {
		NodeID        int    `json:"nodeId"`
		BackendNodeID int    `json:"backendNodeId"`
		AXNodeID      string `json:"id"`
	}
	_ = json.Unmarshal(raw, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case p.NodeID != 0:
		return s.nodes[p.NodeID]
	case p.BackendNodeID != 0:
		return s.backend[p.BackendNodeID]
	case p.AXNodeID != "":
		id, err := strconv.Atoi(p.AXNodeID)
		if err == nil {
			return s.nodes[id]
		}
	}
	return s.root
}

func accessibilityNode(n *cdpNode) map[string]any {
	childIDs := make([]string, 0, len(n.Children))
	for _, child := range n.Children {
		childIDs = append(childIDs, strconv.Itoa(child.NodeID))
	}
	out := map[string]any{
		"nodeId":           strconv.Itoa(n.NodeID),
		"ignored":          false,
		"role":             map[string]any{"type": "role", "value": n.Role},
		"name":             map[string]any{"type": "computedString", "value": firstNonEmpty(n.Title, n.Description, n.Role)},
		"backendDOMNodeId": n.BackendID,
		"childIds":         childIDs,
	}
	out["parentId"] = strconv.Itoa(domNodeID(n.ParentID))
	if n.Description != "" {
		out["description"] = map[string]any{"type": "computedString", "value": n.Description}
	}
	return out
}

func (s *cdpServer) flattenedDOM(depth int) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{s.documentNode(0)}
	if s.root == nil || depth == 0 {
		return out
	}
	var walk func(*cdpNode, int)
	walk = func(n *cdpNode, d int) {
		out = append(out, s.domNode(n, 0))
		if depth >= 0 && d >= depth {
			return
		}
		for _, child := range n.Children {
			walk(child, d+1)
		}
	}
	walk(s.root, 1)
	return out
}

func (s *cdpServer) outerHTML(n *cdpNode) string {
	var b strings.Builder
	var walk func(*cdpNode)
	walk = func(node *cdpNode) {
		name := node.LocalName
		if name == "" {
			name = "element"
		}
		fmt.Fprintf(&b, "<%s data-ax-node-id=%q role=%q", name, strconv.Itoa(node.NodeID), node.Role)
		if node.Title != "" {
			fmt.Fprintf(&b, " title=%q", node.Title)
		}
		b.WriteString(">")
		for _, child := range node.Children {
			walk(child)
		}
		fmt.Fprintf(&b, "</%s>", name)
	}
	walk(n)
	return b.String()
}

func boxModel(r axRect) map[string]any {
	w, h := r.Width, r.Height
	quad := boxQuad(r)
	return map[string]any{"content": quad, "padding": quad, "border": quad, "margin": quad, "width": w, "height": h}
}

func boxQuad(r axRect) []float64 {
	x, y, w, h := r.X, r.Y, r.Width, r.Height
	return []float64{x, y, x + w, y, x + w, y + h, x, y + h}
}

func rectFromQuad(q []float64) (axRect, bool) {
	if len(q) < 8 {
		return axRect{}, false
	}
	minX, maxX := q[0], q[0]
	minY, maxY := q[1], q[1]
	for i := 2; i+1 < len(q); i += 2 {
		x, y := q[i], q[i+1]
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	return axRect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, true
}

func blankPNG() string {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{255, 255, 255, 255})
	var b strings.Builder
	if err := writePNGBase64(stringWriter{&b}, img); err != nil {
		slog.Warn("encode blank png failed", "err", err)
		return ""
	}
	return b.String()
}

func writePNGBase64(w io.Writer, img image.Image) error {
	enc := base64.NewEncoder(base64.StdEncoding, w)
	if err := png.Encode(enc, img); err != nil {
		_ = enc.Close()
		return fmt.Errorf("encode png: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close base64 png encoder: %w", err)
	}
	return nil
}

func (s *cdpServer) captureTargetPNG() string {
	r := s.viewportBounds()
	slog.Debug("capture target png", "target", s.appArg, "x", r.X, "y", r.Y, "width", r.Width, "height", r.Height)
	if pid, ok := s.appPID(); ok && s.windowID != 0 {
		return captureWindowIDPNG(strconv.Itoa(pid), s.windowID)
	}
	if pid, ok := s.appPID(); ok {
		return captureWindowPNG(strconv.Itoa(pid))
	}
	return blankPNG()
}

func (s *cdpServer) captureTargetPNGBounded(timeout time.Duration) string {
	ch := make(chan string, 1)
	go func() {
		ch <- s.captureTargetPNG()
	}()
	select {
	case data := <-ch:
		return data
	case <-time.After(timeout):
		slog.Warn("target screenshot timed out; falling back to full screen", "target", s.appArg, "timeout", timeout)
		return captureScreenPNG()
	}
}

func captureScreenPNG() string {
	return blankPNG()
}

func captureScreenPNGRect(r axRect) string {
	return blankPNG()
}

func captureWindowPNG(appIdentifier string) string {
	start := time.Now()
	win, err := ocrwindow.FindWindow(appIdentifier)
	if err != nil {
		slog.Warn("find capture window failed", "app", appIdentifier, "err", err)
		return blankPNG()
	}
	data, w, h, err := ocrwindow.Capture(win)
	if err != nil {
		slog.Warn("capture window failed", "app", appIdentifier, "window_id", win.ID, "title", win.Title, "err", err)
		return blankPNG()
	}
	slog.Info("window capture complete", "backend", "CGWindowListCreateImage", "app", appIdentifier, "window_id", win.ID, "title", win.Title, "width", w, "height", h, "bytes", len(data), "duration", time.Since(start))
	return base64.StdEncoding.EncodeToString(data)
}

func captureWindowIDPNG(appIdentifier string, windowID uint32) string {
	start := time.Now()
	win, err := ocrwindow.FindWindowID(appIdentifier, windowID)
	if err != nil {
		slog.Warn("find capture window failed", "app", appIdentifier, "window_id", windowID, "err", err)
		return blankPNG()
	}
	data, w, h, err := ocrwindow.Capture(win)
	if err != nil {
		slog.Warn("capture window failed", "app", appIdentifier, "window_id", win.ID, "title", win.Title, "err", err)
		return blankPNG()
	}
	slog.Info("window capture complete", "backend", "CGWindowListCreateImage", "app", appIdentifier, "window_id", win.ID, "title", win.Title, "width", w, "height", h, "bytes", len(data), "duration", time.Since(start))
	return base64.StdEncoding.EncodeToString(data)
}

func runningApps() []runningApp {
	if apps := visibleAppsFromLSAppInfo(); len(apps) > 0 {
		return dedupeRunningApps(apps)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", `Application("System Events").applicationProcesses.whose({backgroundOnly:false})().map(p => [p.unixId(), p.name()].join("\t")).join("\n")`).Output()
	if err == nil {
		if apps := parseSystemEventsApps(string(out)); len(apps) > 0 {
			return dedupeRunningApps(apps)
		}
	}
	return dedupeRunningApps(topLevelAppsFromPS())
}

func appWindows(app runningApp) []ocrwindow.Window {
	wins, err := ocrwindow.ListWindows(strconv.Itoa(app.PID))
	if err != nil {
		return nil
	}
	out := wins[:0]
	seen := make(map[uint32]bool)
	for _, win := range wins {
		if win.ID == 0 || seen[win.ID] || win.W <= 0 || win.H <= 0 {
			continue
		}
		seen[win.ID] = true
		out = append(out, win)
	}
	return out
}

func windowTargetTitle(app runningApp, win ocrwindow.Window) string {
	if win.Title != "" {
		return win.Title
	}
	if app.Name != "" {
		return app.Name + " window " + strconv.FormatUint(uint64(win.ID), 10)
	}
	return "window " + strconv.FormatUint(uint64(win.ID), 10)
}

func visibleAppsFromLSAppInfo() []runningApp {
	visibleOut, err := exec.Command("lsappinfo", "visibleProcessList").Output()
	if err != nil {
		return nil
	}
	visible := parseLSAppInfoVisible(string(visibleOut))
	if len(visible) == 0 {
		return nil
	}
	apps := runningAppsFromPS()
	out := apps[:0]
	seen := make(map[int]bool)
	for _, app := range apps {
		if seen[app.PID] || !visible[app.Name] {
			continue
		}
		seen[app.PID] = true
		out = append(out, app)
	}
	return out
}

func parseLSAppInfoVisible(out string) map[string]bool {
	visible := make(map[string]bool)
	for _, part := range strings.Split(out, "\"") {
		name := strings.TrimSpace(strings.ReplaceAll(part, "_", " "))
		if isVisibleAppName(name) {
			visible[name] = true
		}
	}
	return visible
}

func isVisibleAppName(name string) bool {
	if name == "" || strings.HasPrefix(name, "ASN:") {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func parseSystemEventsApps(out string) []runningApp {
	seen := make(map[int]bool)
	var apps []runningApp
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		name := strings.TrimSpace(fields[1])
		if err != nil || pid <= 0 || name == "" || seen[pid] {
			continue
		}
		seen[pid] = true
		apps = append(apps, runningApp{PID: pid, Name: name})
	}
	return apps
}

func runningAppsFromPS() []runningApp {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	seen := make(map[int]bool)
	var apps []runningApp
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexFunc(line, unicode.IsSpace)
		if i < 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line[:i]))
		if err != nil || pid <= 0 || seen[pid] {
			continue
		}
		cmd := strings.TrimSpace(line[i:])
		name := appNameFromCommand(cmd)
		if name == "" {
			continue
		}
		seen[pid] = true
		apps = append(apps, runningApp{PID: pid, Name: name})
	}
	return apps
}

func topLevelAppsFromPS() []runningApp {
	apps := runningAppsFromPS()
	out := apps[:0]
	for _, app := range apps {
		switch app.Name {
		case "Finder", "Terminal", "iTerm2", "Brave Browser", "Google Chrome Canary", "Google Chrome", "Safari", "Notes", "TextEdit":
			out = append(out, app)
		}
	}
	return out
}

func dedupeRunningApps(apps []runningApp) []runningApp {
	seen := make(map[string]bool)
	out := apps[:0]
	for _, app := range apps {
		if app.Name == "" || seen[app.Name] {
			continue
		}
		seen[app.Name] = true
		out = append(out, app)
	}
	return out
}

func appNameFromCommand(cmd string) string {
	const marker = ".app/Contents/MacOS/"
	i := strings.Index(cmd, marker)
	if i < 0 {
		return ""
	}
	if strings.Contains(cmd[:i], "/Contents/Frameworks/") {
		return ""
	}
	prefix := cmd[:i+len(".app")]
	base := path.Base(prefix)
	name := strings.TrimSuffix(base, ".app")
	if strings.Contains(name, " Helper") || strings.Contains(name, "Helper ") || strings.Contains(name, "crashpad") {
		return ""
	}
	return name
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func intParamDefault(raw json.RawMessage, name string, def int) int {
	var params map[string]any
	_ = json.Unmarshal(raw, &params)
	if v, ok := params[name].(float64); ok {
		return int(v)
	}
	return def
}

func writeHTTPJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)+1))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

func safeString(s string) string { return strings.TrimSpace(s) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func axAttributeNames(ref axuiautomation.AXUIElementRef) map[string]bool {
	if ref == 0 {
		return nil
	}
	var value uintptr
	if ax.copyAttributeNames(ref, &value) != 0 || value == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value)) != corefoundation.CFArrayGetTypeID() {
		return nil
	}
	count := corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(value))
	attrs := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		child := uintptr(corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(value), i))
		if child == 0 {
			continue
		}
		if name := cfString(child); name != "" {
			attrs[name] = true
		}
	}
	return attrs
}

func axHasAttr(attrs map[string]bool, name string) bool {
	return attrs == nil || attrs[name]
}

func axStringAttr(ref axuiautomation.AXUIElementRef, name string) string {
	return axStringAttrKnown(ref, nil, name)
}

func axStringAttrKnown(ref axuiautomation.AXUIElementRef, attrs map[string]bool, name string) string {
	if !axHasAttr(attrs, name) {
		return ""
	}
	var value uintptr
	attr := corefoundation.CFStringCreateWithCString(0, name, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	if axuiautomation.AXUIElementCopyAttributeValue(ref, uintptr(attr), &value) != 0 || value == 0 {
		return ""
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	return cfString(value)
}

func axPointAttr(ref axuiautomation.AXUIElementRef, name string) (float64, float64) {
	return axPointAttrKnown(ref, nil, name)
}

func axPointAttrKnown(ref axuiautomation.AXUIElementRef, attrs map[string]bool, name string) (float64, float64) {
	if !axHasAttr(attrs, name) {
		return 0, 0
	}
	var value uintptr
	attr := corefoundation.CFStringCreateWithCString(0, name, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	if axuiautomation.AXUIElementCopyAttributeValue(ref, uintptr(attr), &value) != 0 || value == 0 {
		return 0, 0
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	var p struct{ X, Y float64 }
	if !axuiautomation.AXValueGetValue(axuiautomation.AXValueRef(value), 1, unsafe.Pointer(&p)) {
		return 0, 0
	}
	return p.X, p.Y
}

func axSizeAttr(ref axuiautomation.AXUIElementRef, name string) (float64, float64) {
	return axSizeAttrKnown(ref, nil, name)
}

func axSizeAttrKnown(ref axuiautomation.AXUIElementRef, attrs map[string]bool, name string) (float64, float64) {
	if !axHasAttr(attrs, name) {
		return 0, 0
	}
	var value uintptr
	attr := corefoundation.CFStringCreateWithCString(0, name, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	if axuiautomation.AXUIElementCopyAttributeValue(ref, uintptr(attr), &value) != 0 || value == 0 {
		return 0, 0
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	var sz struct{ Width, Height float64 }
	if !axuiautomation.AXValueGetValue(axuiautomation.AXValueRef(value), 2, unsafe.Pointer(&sz)) {
		return 0, 0
	}
	return sz.Width, sz.Height
}

func axChildElements(ref axuiautomation.AXUIElementRef) []axuiautomation.AXUIElementRef {
	return axChildElementsKnown(ref, nil)
}

func axChildElementsKnown(ref axuiautomation.AXUIElementRef, supported map[string]bool) []axuiautomation.AXUIElementRef {
	names := []string{
		"AXWindows",
		"AXMainWindow",
		"AXFocusedWindow",
		"AXChildren",
		"AXContents",
		"AXToolbar",
		"AXDocument",
		"AXRows",
		"AXColumns",
		"AXVisibleRows",
		"AXVisibleColumns",
		"AXTabs",
	}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	var children []axuiautomation.AXUIElementRef
	for _, name := range names {
		for _, child := range axElementAttr(ref, name) {
			if child == 0 || seen[child] {
				continue
			}
			seen[child] = true
			children = append(children, child)
		}
	}
	return children
}

func axChildElementCount(ref axuiautomation.AXUIElementRef) int {
	return axChildElementCountKnown(ref, axAttributeNames(ref))
}

func axChildElementCountKnown(ref axuiautomation.AXUIElementRef, supported map[string]bool) int {
	names := []string{
		"AXWindows",
		"AXChildren",
		"AXContents",
		"AXRows",
		"AXColumns",
		"AXVisibleRows",
		"AXVisibleColumns",
		"AXTabs",
	}
	total := 0
	for _, name := range names {
		total += axArrayAttrCountKnown(ref, supported, name)
	}
	return total
}

func axArrayAttrCount(ref axuiautomation.AXUIElementRef, name string) int {
	return axArrayAttrCountKnown(ref, nil, name)
}

func axArrayAttrCountKnown(ref axuiautomation.AXUIElementRef, attrs map[string]bool, name string) int {
	if ref == 0 {
		return 0
	}
	if !axHasAttr(attrs, name) {
		return 0
	}
	var count int
	attr := corefoundation.CFStringCreateWithCString(0, name, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	if axuiautomation.AXUIElementGetAttributeValueCount(ref, uintptr(attr), &count) != 0 {
		return 0
	}
	return count
}

func axWindowElements(ref axuiautomation.AXUIElementRef) []axuiautomation.AXUIElementRef {
	names := []string{
		"AXWindows",
		"AXMainWindow",
		"AXFocusedWindow",
		"AXChildren",
	}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	var windows []axuiautomation.AXUIElementRef
	for _, name := range names {
		for _, child := range axElementAttr(ref, name) {
			if child == 0 || seen[child] {
				continue
			}
			seen[child] = true
			if safeString(axStringAttrKnown(child, axAttributeNames(child), "AXRole")) == "AXWindow" {
				windows = append(windows, child)
			}
		}
	}
	return windows
}

func axWindowSearchChildren(ref axuiautomation.AXUIElementRef) []axuiautomation.AXUIElementRef {
	names := []string{
		"AXWindows",
		"AXMainWindow",
		"AXFocusedWindow",
		"AXChildren",
		"AXContents",
		"AXToolbar",
		"AXDocument",
	}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	var children []axuiautomation.AXUIElementRef
	for _, name := range names {
		for _, child := range axElementAttr(ref, name) {
			if child == 0 || seen[child] {
				continue
			}
			seen[child] = true
			children = append(children, child)
		}
	}
	return children
}

func axElementAttr(ref axuiautomation.AXUIElementRef, name string) []axuiautomation.AXUIElementRef {
	return axElementAttrKnown(ref, nil, name)
}

func axElementAttrKnown(ref axuiautomation.AXUIElementRef, attrs map[string]bool, name string) []axuiautomation.AXUIElementRef {
	if !axHasAttr(attrs, name) {
		return nil
	}
	var value uintptr
	attr := corefoundation.CFStringCreateWithCString(0, name, uint32(corefoundation.KCFStringEncodingUTF8))
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	if axuiautomation.AXUIElementCopyAttributeValue(ref, uintptr(attr), &value) != 0 || value == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value)) != corefoundation.CFArrayGetTypeID() {
		corefoundation.CFRetain(corefoundation.CFTypeRef(value))
		return []axuiautomation.AXUIElementRef{axuiautomation.AXUIElementRef(value)}
	}
	count := corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(value))
	children := make([]axuiautomation.AXUIElementRef, 0, count)
	for i := 0; i < count; i++ {
		child := uintptr(corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(value), i))
		if child != 0 {
			corefoundation.CFRetain(corefoundation.CFTypeRef(child))
			children = append(children, axuiautomation.AXUIElementRef(child))
		}
	}
	return children
}

var axHighlight = struct {
	sync.Mutex
	cmd *exec.Cmd
}{}

func hideAXHighlight() {
	axHighlight.Lock()
	cmd := axHighlight.cmd
	axHighlight.cmd = nil
	axHighlight.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

func highlightAXRect(r axRect, duration time.Duration) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	ms := int(duration / time.Millisecond)
	script := fmt.Sprintf(`
import AppKit
let x = CGFloat(%f)
let yTop = CGFloat(%f)
let w = CGFloat(%f)
let h = CGFloat(%f)
let screenH = NSScreen.main?.frame.height ?? 0
let rect = NSRect(x: x - 4, y: screenH - yTop - h - 4, width: w + 8, height: h + 8)
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let win = NSWindow(contentRect: rect, styleMask: .borderless, backing: .buffered, defer: false)
win.level = .statusBar
win.ignoresMouseEvents = true
win.isOpaque = false
win.backgroundColor = .clear
let view = NSBox(frame: NSRect(x: 0, y: 0, width: rect.width, height: rect.height))
view.boxType = .custom
view.borderWidth = 4
view.borderColor = NSColor.systemOrange
view.fillColor = NSColor.systemOrange.withAlphaComponent(0.16)
view.cornerRadius = 6
win.contentView = view
win.orderFrontRegardless()
RunLoop.current.run(until: Date().addingTimeInterval(Double(%d)/1000.0))
`, r.X, r.Y, r.Width, r.Height, ms)
	cmd := exec.Command("swift", "-e", script)
	hideAXHighlight()
	if err := cmd.Start(); err != nil {
		return
	}
	axHighlight.Lock()
	axHighlight.cmd = cmd
	axHighlight.Unlock()
	_ = cmd.Wait()
	axHighlight.Lock()
	if axHighlight.cmd == cmd {
		axHighlight.cmd = nil
	}
	axHighlight.Unlock()
}

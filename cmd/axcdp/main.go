// Command axcdp exposes macOS Accessibility as CDP-style JSON commands.
//
// Commands are written as Domain.method followed by an optional JSON object:
//
//	AX.createApplication {"pid":123}
//	AX.copyAttributeNames {"element":"element-1"}
//	AX.copyAttributeValue {"element":"element-1","attribute":"AXChildren"}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/axmcp/internal/ui/permissions"
	"github.com/tmc/macgo"
)

type request struct {
	ID     any            `json:"id,omitempty"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type response struct {
	ID     any            `json:"id,omitempty"`
	Result map[string]any `json:"result,omitempty"`
	Error  *protocolError `json:"error,omitempty"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct {
	next    int
	refs    map[string]axuiautomation.AXUIElementRef
	obs     map[string]*observer
	strings map[string]corefoundation.CFStringRef
}

type observer struct {
	id     string
	ref    axuiautomation.AXObserverRef
	events chan observerEvent
}

type observerEvent struct {
	Element      axuiautomation.AXUIElementRef
	Notification string
	Info         uintptr
}

func main() {
	runtime.LockOSThread()

	var command string
	var listen string
	var app string
	var verifyCDP string
	var verifyBrowserCDP string
	var verifyTarget string
	var target string
	var browserCDP string
	var resetTCC bool
	var autoTCC bool
	var tccRemove bool
	var tccReenable bool
	tccPromptAppName := tccAppName
	var tccDismissAction string
	var screencastMaxDim int
	var verbose bool
	flag.StringVar(&command, "command", "", "run one CDP-style command and exit")
	flag.StringVar(&command, "c", "", "run one CDP-style command and exit")
	flag.StringVar(&listen, "listen", "", "listen address for Chrome DevTools Protocol")
	flag.StringVar(&app, "app", "", "app name, bundle id, or pid to expose as the CDP document; defaults to the system-wide AX tree")
	flag.StringVar(&verifyCDP, "verify-cdp", "", "verify a running axcdp endpoint, for example http://127.0.0.1:9221")
	flag.StringVar(&verifyBrowserCDP, "verify-browser-cdp", "", "verify browser-backed targets on a running combined axcdp endpoint")
	flag.StringVar(&verifyTarget, "verify-target", "", "verify one selected target on a running axcdp endpoint")
	flag.StringVar(&target, "target", "", "required target title, id, or URL substring for -verify-target")
	flag.StringVar(&browserCDP, "browser-cdp", "", "real browser DevTools endpoint to proxy for browser-backed targets, for example http://127.0.0.1:9222")
	flag.BoolVar(&resetTCC, "reset-tcc", false, "reset and re-request Accessibility and Screen Recording TCC grants for the axcdp app identity")
	flag.BoolVar(&autoTCC, "auto-tcc", false, "try to click matching macOS TCC prompts while requesting permissions")
	flag.BoolVar(&tccRemove, "tcc-remove", false, "remove existing Accessibility and Screen Recording rows for -tcc-app-name via System Settings")
	flag.BoolVar(&tccReenable, "tcc-reenable", false, "toggle existing Accessibility and Screen Recording allowances off and back on via System Settings")
	flag.StringVar(&tccPromptAppName, "tcc-app-name", tccPromptAppName, "app name to match in TCC prompts and System Settings lists")
	flag.StringVar(&tccDismissAction, "tcc-dismiss-action", "Allow", "button title to click in matching TCC prompts")
	flag.IntVar(&screencastMaxDim, "screencast-max-dim", 0, "cap DevTools screencast frame width and height in pixels; 0 honors the frontend request")
	flag.BoolVar(&verbose, "v", false, "enable verbose structured logging")
	flag.BoolVar(&verbose, "debug", false, "enable verbose structured logging")
	flag.Parse()
	configureSlog(verbose)
	if command == "" && flag.NArg() > 0 {
		command = strings.Join(flag.Args(), " ")
	}

	s := &server{
		refs:    make(map[string]axuiautomation.AXUIElementRef),
		obs:     make(map[string]*observer),
		strings: make(map[string]corefoundation.CFStringRef),
	}
	if listen == "" && command == "" && stdinIsTerminal() {
		listen = ":9221"
	}
	if verifyCDP != "" {
		if err := verifyCDPEndpoint(verifyCDP); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "axcdp verification passed")
		return
	}
	if verifyBrowserCDP != "" {
		if err := verifyBrowserCDPEndpoint(verifyBrowserCDP); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "axcdp browser verification passed")
		return
	}
	if verifyTarget != "" {
		if err := verifyCDPTarget(verifyTarget, target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "axcdp target verification passed")
		return
	}
	if err := startMacApp(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if resetTCC {
		resetMacPermissions()
	}
	if autoTCC || tccRemove || tccReenable {
		startTCCAutomation(autoTCC, tccRemove, tccReenable, tccPromptAppName, tccDismissAction)
	}
	if listen != "" && command == "" {
		promptMacPermissions()
		if err := runCDPServer(listen, app, browserCDP, screencastMaxDim); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if command != "" {
		method, params, err := parseCommand(command)
		resp := response{}
		if err != nil {
			resp.Error = &protocolError{Code: -32700, Message: err.Error()}
		} else {
			resp.Result, err = s.dispatch(method, params)
			if err != nil {
				resp.Error = &protocolError{Code: -32000, Message: err.Error()}
			}
		}
		if err := writeJSON(os.Stdout, resp); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := s.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func startMacApp() error {
	cfg := macgo.NewConfig().
		WithAppName(tccAppName).
		WithPermissions(macgo.Accessibility, macgo.ScreenRecording).
		WithUsageDescription("NSAccessibilityUsageDescription", "axcdp uses Accessibility to expose macOS UI elements through Chrome DevTools Protocol.").
		WithUsageDescription("NSScreenCaptureUsageDescription", "axcdp uses screen capture to provide real DevTools screenshots and screencast frames.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory).
		WithDevMode()
	cfg.BundleID = tccBundleID
	cfg = macsigning.Configure(cfg)
	removeAdHocBundle(tccAppName, cfg.CodeSignIdentity != "" || cfg.AutoSign)
	ui.ConfigureIdentity(tccAppName, cfg.BundleID)
	permissions.ConfigureIdentity(tccAppName, cfg.BundleID)
	return macgo.Start(cfg)
}

func removeAdHocBundle(appName string, wantSigned bool) {
	if appName == "" || !wantSigned || os.Getenv("MACGO_NO_RELAUNCH") != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil || strings.Contains(exe, ".app/Contents/MacOS/") {
		return
	}
	bundle := filepath.Join(filepath.Dir(exe), appName+".app")
	out, err := exec.Command("/usr/bin/codesign", "-dv", bundle).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Signature=adhoc") {
		return
	}
	if err := os.RemoveAll(bundle); err != nil {
		slog.Warn("remove stale app bundle failed", "bundle", bundle, "err", err)
		return
	}
	slog.Info("removed stale ad-hoc app bundle", "bundle", bundle)
}

func configureSlog(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func resetMacPermissions() {
	slog.Info("resetting TCC permissions", "bundle_id", tccBundleID)
	permissions.ResetIdentityState()
	for _, req := range []permissions.Requirement{permissions.ReqAccessibility, permissions.ReqScreenRecording} {
		requestMacPermission(req)
		if err := permissions.ResetAndRetry(req); err != nil {
			fmt.Fprintf(os.Stderr, "axcdp: reset %s permission: %v\n", permissionName(req), err)
		}
	}
}

func startTCCAutomation(autoTCC, remove, reenable bool, appName, dismissAction string) {
	opts := permissions.AutomationOptions{
		AppName:       appName,
		DismissAction: dismissAction,
		Timeout:       20 * time.Second,
		AutoPrompt:    autoTCC,
		Remove:        remove,
		Reenable:      reenable,
	}
	go func() {
		if !autoTCC && !remove && !reenable {
			return
		}
		if err := permissions.Automate(context.Background(), opts, permissions.ReqAccessibility, permissions.ReqScreenRecording); err != nil {
			fmt.Fprintf(os.Stderr, "axcdp: tcc automation: %v\n", err)
		}
	}()
}

func promptMacPermissions() {
	permissions.ResetIdentityState()
	reqs := []permissions.Requirement{permissions.ReqAccessibility, permissions.ReqScreenRecording}
	missing := make([]permissions.Requirement, 0, len(reqs))
	for _, req := range reqs {
		status := permissions.Check(req)
		slog.Info("checked permission", "permission", permissionName(req), "status", permissionStatusName(status))
		if status != permissions.StatusGranted {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return
	}
	for _, req := range missing {
		slog.Info("requesting permission", "permission", permissionName(req))
		requestMacPermission(req)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		status, err := permissions.Request(ctx, req)
		cancel()
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			fmt.Fprintf(os.Stderr, "axcdp: request permission: %v\n", err)
		}
		if status == permissions.StatusGranted {
			slog.Info("permission granted", "permission", permissionName(req))
			continue
		}
		fmt.Fprintf(os.Stderr, "axcdp: permission %s is %s\n", permissionName(req), permissionStatusName(status))
		if status == permissions.StatusDenied || status == permissions.StatusStale {
			_ = permissions.OpenSystemSettings(req)
		}
	}
	if permissions.Check(permissions.ReqAccessibility) == permissions.StatusGranted &&
		permissions.Check(permissions.ReqScreenRecording) == permissions.StatusGranted {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := permissions.OnboardingWindow(ctx, missing...); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			fmt.Fprintf(os.Stderr, "axcdp: permission onboarding: %v\n", err)
		}
	}()
}

func requestMacPermission(req permissions.Requirement) {
	switch req {
	case permissions.ReqAccessibility:
		slog.Debug("trigger accessibility permission request")
		ui.RequestAccessibilityPermission()
	case permissions.ReqScreenRecording:
		slog.Debug("trigger screen recording permission request")
		ui.RequestScreenCapturePermission()
		_ = permissions.OpenSystemSettings(req)
	}
}

func permissionName(req permissions.Requirement) string {
	switch req {
	case permissions.ReqAccessibility:
		return "Accessibility"
	case permissions.ReqScreenRecording:
		return "Screen Recording"
	default:
		return "unknown"
	}
}

func permissionStatusName(status permissions.Status) string {
	switch status {
	case permissions.StatusGranted:
		return "granted"
	case permissions.StatusDenied:
		return "denied"
	case permissions.StatusMissing:
		return "missing"
	case permissions.StatusStale:
		return "stale"
	case permissions.StatusInProgress:
		return "in progress"
	default:
		return "unknown"
	}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (s *server) serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			method, params, parseErr := parseCommand(line)
			if parseErr != nil {
				enc.Encode(response{Error: &protocolError{Code: -32700, Message: parseErr.Error()}})
				continue
			}
			req.Method = method
			req.Params = params
		}
		resp := response{ID: req.ID}
		result, err := s.dispatch(req.Method, req.Params)
		if err != nil {
			resp.Error = &protocolError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = result
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	return sc.Err()
}

func (s *server) dispatch(method string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	if err := axAvailable(); err != nil {
		return nil, err
	}
	switch method {
	case "AX.getVersion":
		return map[string]any{"version": "0.1.0", "domains": []string{"AX"}, "methods": supportedMethods()}, nil
	case "AX.isProcessTrusted":
		return map[string]any{"trusted": axuiautomation.AXIsProcessTrusted()}, nil
	case "AX.getTypeIDs":
		return map[string]any{"AXUIElement": ax.uiElementGetTypeID(), "AXValue": ax.valueGetTypeID()}, nil
	case "AX.isProcessTrustedWithOptions":
		prompt := false
		if v, ok := params["prompt"].(bool); ok {
			prompt = v
		}
		options := uintptr(0)
		release := func() {}
		if prompt {
			options = ax.trustPromptOptions()
			release = func() { corefoundation.CFRelease(corefoundation.CFTypeRef(options)) }
		}
		defer release()
		return map[string]any{"trusted": axuiautomation.AXIsProcessTrustedWithOptions(options)}, nil
	case "AX.createApplication":
		pid, err := intParam(params, "pid")
		if err != nil {
			return nil, err
		}
		ref := axuiautomation.AXUIElementCreateApplication(int32(pid))
		if ref == 0 {
			return nil, fmt.Errorf("create application: null AXUIElement")
		}
		return s.store(ref), nil
	case "AX.getSystemWideElement":
		ref := ax.systemWideElement()
		if ref == 0 {
			return nil, fmt.Errorf("create system-wide element: null AXUIElement")
		}
		return s.store(ref), nil
	case "AX.release":
		id, err := stringParam(params, "element")
		if err != nil {
			return nil, err
		}
		ref, ok := s.refs[id]
		if !ok {
			return nil, fmt.Errorf("unknown element %q", id)
		}
		corefoundation.CFRelease(corefoundation.CFTypeRef(ref))
		delete(s.refs, id)
		return map[string]any{"released": id}, nil
	case "AX.setMessagingTimeout":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		seconds, err := numberParam(params, "seconds")
		if err != nil {
			return nil, err
		}
		return axResult(axuiautomation.AXUIElementSetMessagingTimeout(ref, float32(seconds))), nil
	case "AX.getPid":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		var pid int32
		if code := axuiautomation.AXUIElementGetPid(ref, &pid); code != 0 {
			return axResult(code), nil
		}
		return map[string]any{"pid": pid, "axError": 0}, nil
	case "AX.getWindow":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		var window uint32
		if code := axuiautomation.AXUIElementGetWindow(ref, &window); code != 0 {
			return axResult(code), nil
		}
		return map[string]any{"window": window, "axError": 0}, nil
	case "AX.copyAttributeNames":
		return s.copyNames(params, ax.copyAttributeNames)
	case "AX.copyParameterizedAttributeNames":
		return s.copyNames(params, ax.copyParameterizedAttributeNames)
	case "AX.copyActionNames":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		var value uintptr
		code := axuiautomation.AXUIElementCopyActionNames(ref, &value)
		return s.valueResult(value, code, true), nil
	case "AX.copyActionDescription":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		action, err := stringParam(params, "action")
		if err != nil {
			return nil, err
		}
		var value uintptr
		code := ax.copyActionDescription(ref, uintptr(s.cfString(action)), &value)
		return s.valueResult(value, code, true), nil
	case "AX.copyAttributeValue":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		var value uintptr
		code := axuiautomation.AXUIElementCopyAttributeValue(ref, uintptr(attr), &value)
		return s.valueResult(value, code, true), nil
	case "AX.copyAttributeValues":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		index := 0
		if v, ok := params["index"].(float64); ok {
			index = int(v)
		}
		maxValues, err := intParam(params, "maxValues")
		if err != nil {
			return nil, err
		}
		var value uintptr
		code := ax.copyAttributeValues(ref, uintptr(attr), index, maxValues, &value)
		return s.valueResult(value, code, true), nil
	case "AX.copyMultipleAttributeValues":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		attributes, err := stringListParam(params, "attributes")
		if err != nil {
			return nil, err
		}
		refs := make([]uintptr, len(attributes))
		for i, attr := range attributes {
			refs[i] = uintptr(s.cfString(attr))
		}
		array := corefoundation.CFArrayCreate(0, unsafe.Pointer(&refs[0]), len(refs), nil)
		defer corefoundation.CFRelease(corefoundation.CFTypeRef(array))
		var value uintptr
		code := axuiautomation.AXUIElementCopyMultipleAttributeValues(ref, uintptr(array), 0, &value)
		return s.valueResult(value, code, true), nil
	case "AX.copyParameterizedAttributeValue":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		parameter, release, err := s.paramValue(params, "parameter")
		if err != nil {
			return nil, err
		}
		defer release()
		var value uintptr
		code := ax.copyParameterizedAttributeValue(ref, uintptr(attr), parameter, &value)
		return s.valueResult(value, code, true), nil
	case "AX.getAttributeValueCount":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		var count int
		code := axuiautomation.AXUIElementGetAttributeValueCount(ref, uintptr(attr), &count)
		if code != 0 {
			return axResult(code), nil
		}
		return map[string]any{"count": count, "axError": 0}, nil
	case "AX.isAttributeSettable":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		var settable bool
		code := axuiautomation.AXUIElementIsAttributeSettable(ref, uintptr(attr), &settable)
		if code != 0 {
			return axResult(code), nil
		}
		return map[string]any{"settable": settable, "axError": 0}, nil
	case "AX.setAttributeValue":
		ref, attr, err := s.elementAttribute(params)
		if err != nil {
			return nil, err
		}
		value, release, err := s.paramValue(params, "value")
		if err != nil {
			return nil, err
		}
		defer release()
		return axResult(axuiautomation.AXUIElementSetAttributeValue(ref, uintptr(attr), value)), nil
	case "AX.createValue":
		valueType, err := stringParam(params, "type")
		if err != nil {
			return nil, err
		}
		value, err := ax.createValue(valueType, params)
		if err != nil {
			return nil, err
		}
		return s.storeValue(value), nil
	case "AX.createObserver":
		pid, err := intParam(params, "pid")
		if err != nil {
			return nil, err
		}
		var ref axuiautomation.AXObserverRef
		if code := axuiautomation.AXObserverCreate(int32(pid), ax.observerCallback(), &ref); code != 0 {
			return axResult(code), nil
		}
		source := axuiautomation.AXObserverGetRunLoopSource(ref)
		if source != 0 {
			corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetCurrent(), corefoundation.CFRunLoopSourceRef(source), corefoundation.KCFRunLoopDefaultMode)
		}
		return s.storeObserver(ref), nil
	case "AX.createObserverWithInfo":
		pid, err := intParam(params, "pid")
		if err != nil {
			return nil, err
		}
		var ref axuiautomation.AXObserverRef
		if code := ax.observerCreateWithInfoCallback(int32(pid), ax.observerInfoCallback(), &ref); code != 0 {
			return axResult(code), nil
		}
		source := axuiautomation.AXObserverGetRunLoopSource(ref)
		if source != 0 {
			corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetCurrent(), corefoundation.CFRunLoopSourceRef(source), corefoundation.KCFRunLoopDefaultMode)
		}
		return s.storeObserver(ref), nil
	case "AX.addNotification":
		obs, err := s.observer(params)
		if err != nil {
			return nil, err
		}
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		notification, err := stringParam(params, "notification")
		if err != nil {
			return nil, err
		}
		observerMu.Lock()
		observers[obs.ref] = obs
		observerMu.Unlock()
		return axResult(axuiautomation.AXObserverAddNotification(obs.ref, ref, uintptr(s.cfString(notification)), nil)), nil
	case "AX.removeNotification":
		obs, err := s.observer(params)
		if err != nil {
			return nil, err
		}
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		notification, err := stringParam(params, "notification")
		if err != nil {
			return nil, err
		}
		return axResult(ax.removeNotification(obs.ref, ref, uintptr(s.cfString(notification)))), nil
	case "AX.pollEvents":
		obs, err := s.observer(params)
		if err != nil {
			return nil, err
		}
		timeout := 0.0
		if v, ok := params["timeoutSeconds"].(float64); ok && v > 0 {
			timeout = v
		}
		if timeout > 0 {
			corefoundation.CFRunLoopRunInMode(corefoundation.KCFRunLoopDefaultMode, timeout, true)
		} else {
			axuiautomation.SpinRunLoop(10 * time.Millisecond)
		}
		var events []any
		for {
			select {
			case event := <-obs.events:
				item := map[string]any{
					"element":      s.store(event.Element)["element"],
					"notification": event.Notification,
				}
				if event.Info != 0 {
					item["info"] = s.convertCF(event.Info)
					corefoundation.CFRelease(corefoundation.CFTypeRef(event.Info))
				}
				events = append(events, item)
			default:
				return map[string]any{"events": events, "axError": 0}, nil
			}
		}
	case "AX.closeObserver":
		id, err := stringParam(params, "observer")
		if err != nil {
			return nil, err
		}
		obs, ok := s.obs[id]
		if !ok {
			return nil, fmt.Errorf("unknown observer %q", id)
		}
		observerMu.Lock()
		delete(observers, obs.ref)
		observerMu.Unlock()
		corefoundation.CFRelease(corefoundation.CFTypeRef(obs.ref))
		delete(s.obs, id)
		return map[string]any{"closed": id, "axError": 0}, nil
	case "AX.performAction":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		action, err := stringParam(params, "action")
		if err != nil {
			return nil, err
		}
		return axResult(axuiautomation.AXUIElementPerformAction(ref, uintptr(s.cfString(action)))), nil
	case "AX.copyElementAtPosition":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		x, err := numberParam(params, "x")
		if err != nil {
			return nil, err
		}
		y, err := numberParam(params, "y")
		if err != nil {
			return nil, err
		}
		var value axuiautomation.AXUIElementRef
		if code := ax.copyElementAtPosition(ref, x, y, &value); code != 0 {
			return axResult(code), nil
		}
		return s.store(value), nil
	case "AX.postKeyboardEvent":
		ref, err := s.element(params)
		if err != nil {
			return nil, err
		}
		charCode := 0
		if v, ok := params["keyChar"].(float64); ok {
			charCode = int(v)
		}
		virtualKey, err := intParam(params, "virtualKey")
		if err != nil {
			return nil, err
		}
		keyDown := false
		if v, ok := params["keyDown"].(bool); ok {
			keyDown = v
		}
		return axResult(ax.postKeyboardEvent(ref, uint16(charCode), uint16(virtualKey), keyDown)), nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func supportedMethods() []string {
	return []string{
		"AX.getVersion",
		"AX.isProcessTrusted",
		"AX.isProcessTrustedWithOptions",
		"AX.getTypeIDs",
		"AX.createApplication",
		"AX.getSystemWideElement",
		"AX.release",
		"AX.setMessagingTimeout",
		"AX.getPid",
		"AX.getWindow",
		"AX.copyAttributeNames",
		"AX.copyAttributeValue",
		"AX.copyAttributeValues",
		"AX.copyMultipleAttributeValues",
		"AX.getAttributeValueCount",
		"AX.isAttributeSettable",
		"AX.setAttributeValue",
		"AX.copyParameterizedAttributeNames",
		"AX.copyParameterizedAttributeValue",
		"AX.copyActionNames",
		"AX.copyActionDescription",
		"AX.performAction",
		"AX.copyElementAtPosition",
		"AX.postKeyboardEvent",
		"AX.createValue",
		"AX.createObserver",
		"AX.createObserverWithInfo",
		"AX.addNotification",
		"AX.removeNotification",
		"AX.pollEvents",
		"AX.closeObserver",
	}
}

func (s *server) copyNames(params map[string]any, fn func(axuiautomation.AXUIElementRef, *uintptr) axuiautomation.AXError) (map[string]any, error) {
	ref, err := s.element(params)
	if err != nil {
		return nil, err
	}
	var value uintptr
	code := fn(ref, &value)
	return s.valueResult(value, code, true), nil
}

func (s *server) store(ref axuiautomation.AXUIElementRef) map[string]any {
	s.next++
	id := fmt.Sprintf("element-%d", s.next)
	s.refs[id] = ref
	return map[string]any{"element": id, "axError": 0}
}

func (s *server) storeValue(ref axuiautomation.AXValueRef) map[string]any {
	s.next++
	id := fmt.Sprintf("element-%d", s.next)
	s.refs[id] = axuiautomation.AXUIElementRef(ref)
	return map[string]any{"element": id, "axError": 0}
}

func (s *server) storeObserver(ref axuiautomation.AXObserverRef) map[string]any {
	s.next++
	id := fmt.Sprintf("observer-%d", s.next)
	obs := &observer{id: id, ref: ref, events: make(chan observerEvent, 100)}
	s.obs[id] = obs
	observerMu.Lock()
	observers[ref] = obs
	observerMu.Unlock()
	return map[string]any{"observer": id, "axError": 0}
}

func (s *server) element(params map[string]any) (axuiautomation.AXUIElementRef, error) {
	id, err := stringParam(params, "element")
	if err != nil {
		return 0, err
	}
	ref, ok := s.refs[id]
	if !ok {
		return 0, fmt.Errorf("unknown element %q", id)
	}
	return ref, nil
}

func (s *server) observer(params map[string]any) (*observer, error) {
	id, err := stringParam(params, "observer")
	if err != nil {
		return nil, err
	}
	obs, ok := s.obs[id]
	if !ok {
		return nil, fmt.Errorf("unknown observer %q", id)
	}
	return obs, nil
}

func (s *server) elementAttribute(params map[string]any) (axuiautomation.AXUIElementRef, corefoundation.CFStringRef, error) {
	ref, err := s.element(params)
	if err != nil {
		return 0, 0, err
	}
	attr, err := stringParam(params, "attribute")
	if err != nil {
		return 0, 0, err
	}
	return ref, s.cfString(attr), nil
}

func (s *server) cfString(str string) corefoundation.CFStringRef {
	if ref, ok := s.strings[str]; ok {
		return ref
	}
	ref := corefoundation.CFStringCreateWithCString(0, str, uint32(corefoundation.KCFStringEncodingUTF8))
	s.strings[str] = ref
	return ref
}

func (s *server) paramValue(params map[string]any, name string) (uintptr, func(), error) {
	v, ok := params[name]
	if !ok {
		return 0, func() {}, fmt.Errorf("%s is required", name)
	}
	switch v := v.(type) {
	case string:
		ref := corefoundation.CFStringCreateWithCString(0, v, uint32(corefoundation.KCFStringEncodingUTF8))
		return uintptr(ref), func() { corefoundation.CFRelease(corefoundation.CFTypeRef(ref)) }, nil
	case bool:
		if v {
			return uintptr(corefoundation.KCFBooleanTrue), func() {}, nil
		}
		return uintptr(corefoundation.KCFBooleanFalse), func() {}, nil
	case float64:
		ref := corefoundation.CFNumberCreate(0, corefoundation.KCFNumberDoubleType, unsafe.Pointer(&v))
		return uintptr(ref), func() { corefoundation.CFRelease(corefoundation.CFTypeRef(ref)) }, nil
	case map[string]any:
		if id, ok := v["element"].(string); ok {
			ref, ok := s.refs[id]
			if !ok {
				return 0, func() {}, fmt.Errorf("unknown element %q", id)
			}
			return uintptr(ref), func() {}, nil
		}
	}
	return 0, func() {}, fmt.Errorf("unsupported %s value %T", name, v)
}

func (s *server) valueResult(value uintptr, code axuiautomation.AXError, release bool) map[string]any {
	if code != 0 {
		return axResult(code)
	}
	if value == 0 {
		return map[string]any{"value": nil, "axError": 0}
	}
	if release {
		defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	}
	return map[string]any{"value": s.convertCF(value), "axError": 0}
}

func (s *server) convertCF(value uintptr) any {
	typeID := corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value))
	switch typeID {
	case corefoundation.CFStringGetTypeID():
		return cfString(value)
	case corefoundation.CFBooleanGetTypeID():
		return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(value))
	case corefoundation.CFNumberGetTypeID():
		var f float64
		if corefoundation.CFNumberGetValue(corefoundation.CFNumberRef(value), corefoundation.KCFNumberDoubleType, unsafe.Pointer(&f)) {
			return f
		}
	case corefoundation.CFArrayGetTypeID():
		count := corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(value))
		out := make([]any, 0, count)
		for i := 0; i < count; i++ {
			ref := uintptr(corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(value), i))
			out = append(out, s.convertCF(ref))
		}
		return out
	case corefoundation.CFDictionaryGetTypeID():
		count := corefoundation.CFDictionaryGetCount(corefoundation.CFDictionaryRef(value))
		keys := make([]uintptr, count)
		values := make([]uintptr, count)
		corefoundation.CFDictionaryGetKeysAndValues(corefoundation.CFDictionaryRef(value), unsafe.Pointer(&keys[0]), unsafe.Pointer(&values[0]))
		out := make(map[string]any, count)
		for i := range keys {
			out[fmt.Sprint(s.convertCF(keys[i]))] = s.convertCF(values[i])
		}
		return out
	}
	if point, ok := ax.valuePoint(value); ok {
		return point
	}
	if size, ok := ax.valueSize(value); ok {
		return size
	}
	if rect, ok := ax.valueRect(value); ok {
		return rect
	}
	id := s.store(axuiautomation.AXUIElementRef(value))["element"]
	corefoundation.CFRetain(corefoundation.CFTypeRef(value))
	return map[string]any{"element": id, "cfTypeID": typeID}
}

func cfString(ref uintptr) string {
	length := corefoundation.CFStringGetLength(corefoundation.CFStringRef(ref))
	size := corefoundation.CFStringGetMaximumSizeForEncoding(length, uint32(corefoundation.KCFStringEncodingUTF8)) + 1
	buf := make([]byte, size)
	if !corefoundation.CFStringGetCString(corefoundation.CFStringRef(ref), &buf[0], size, uint32(corefoundation.KCFStringEncodingUTF8)) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func axResult(code axuiautomation.AXError) map[string]any {
	return map[string]any{"axError": int(code), "ok": code == 0}
}

func parseCommand(command string) (string, map[string]any, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, fmt.Errorf("empty command")
	}
	method := command
	paramText := "{}"
	if i := strings.IndexFunc(command, unicode.IsSpace); i >= 0 {
		method = command[:i]
		paramText = strings.TrimSpace(command[i:])
	}
	if strings.Count(method, ".") != 1 {
		return "", nil, fmt.Errorf("invalid method %q", method)
	}
	var params map[string]any
	if paramText == "" {
		paramText = "{}"
	}
	if err := json.Unmarshal([]byte(paramText), &params); err != nil {
		return "", nil, fmt.Errorf("invalid JSON parameters: %w", err)
	}
	if params == nil {
		params = map[string]any{}
	}
	return method, params, nil
}

func stringParam(params map[string]any, name string) (string, error) {
	v, ok := params[name].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func intParam(params map[string]any, name string) (int, error) {
	v, ok := params[name].(float64)
	if !ok {
		return 0, fmt.Errorf("%s is required", name)
	}
	return int(v), nil
}

func numberParam(params map[string]any, name string) (float64, error) {
	v, ok := params[name].(float64)
	if !ok {
		return 0, fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func stringListParam(params map[string]any, name string) ([]string, error) {
	v, ok := params[name].([]any)
	if !ok || len(v) == 0 {
		return nil, fmt.Errorf("%s is required", name)
	}
	out := make([]string, len(v))
	for i, item := range v {
		str, ok := item.(string)
		if !ok || str == "" {
			return nil, fmt.Errorf("%s[%d] must be a string", name, i)
		}
		out[i] = str
	}
	return out, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json response: %w", err)
	}
	return nil
}

var ax, axLoadErr = loadAX(applicationServicesPath)

const applicationServicesPath = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"

type axAPI struct {
	systemWideElement               func() axuiautomation.AXUIElementRef
	uiElementGetTypeID              func() uint
	copyAttributeNames              func(axuiautomation.AXUIElementRef, *uintptr) axuiautomation.AXError
	copyAttributeValues             func(axuiautomation.AXUIElementRef, uintptr, int, int, *uintptr) axuiautomation.AXError
	copyParameterizedAttributeNames func(axuiautomation.AXUIElementRef, *uintptr) axuiautomation.AXError
	copyParameterizedAttributeValue func(axuiautomation.AXUIElementRef, uintptr, uintptr, *uintptr) axuiautomation.AXError
	copyActionDescription           func(axuiautomation.AXUIElementRef, uintptr, *uintptr) axuiautomation.AXError
	copyElementAtPosition           func(axuiautomation.AXUIElementRef, float64, float64, *axuiautomation.AXUIElementRef) axuiautomation.AXError
	postKeyboardEvent               func(axuiautomation.AXUIElementRef, uint16, uint16, bool) axuiautomation.AXError
	valueGetTypeID                  func() uint
	valueGetType                    func(axuiautomation.AXValueRef) axuiautomation.AXValueType
	dictionaryCreate                func(corefoundation.CFAllocatorRef, unsafe.Pointer, unsafe.Pointer, int, unsafe.Pointer, unsafe.Pointer) corefoundation.CFDictionaryRef
	removeNotification              func(axuiautomation.AXObserverRef, axuiautomation.AXUIElementRef, uintptr) axuiautomation.AXError
	observerCreateWithInfoCallback  func(int32, axuiautomation.AXObserverCallback, *axuiautomation.AXObserverRef) axuiautomation.AXError
}

func loadAX(path string) (axAPI, error) {
	lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return axAPI{}, err
	}
	var api axAPI
	purego.RegisterLibFunc(&api.systemWideElement, lib, "AXUIElementCreateSystemWide")
	purego.RegisterLibFunc(&api.uiElementGetTypeID, lib, "AXUIElementGetTypeID")
	purego.RegisterLibFunc(&api.copyAttributeNames, lib, "AXUIElementCopyAttributeNames")
	purego.RegisterLibFunc(&api.copyAttributeValues, lib, "AXUIElementCopyAttributeValues")
	purego.RegisterLibFunc(&api.copyParameterizedAttributeNames, lib, "AXUIElementCopyParameterizedAttributeNames")
	purego.RegisterLibFunc(&api.copyParameterizedAttributeValue, lib, "AXUIElementCopyParameterizedAttributeValue")
	purego.RegisterLibFunc(&api.copyActionDescription, lib, "AXUIElementCopyActionDescription")
	purego.RegisterLibFunc(&api.copyElementAtPosition, lib, "AXUIElementCopyElementAtPosition")
	purego.RegisterLibFunc(&api.postKeyboardEvent, lib, "AXUIElementPostKeyboardEvent")
	purego.RegisterLibFunc(&api.valueGetTypeID, lib, "AXValueGetTypeID")
	purego.RegisterLibFunc(&api.valueGetType, lib, "AXValueGetType")
	purego.RegisterLibFunc(&api.dictionaryCreate, lib, "CFDictionaryCreate")
	purego.RegisterLibFunc(&api.removeNotification, lib, "AXObserverRemoveNotification")
	purego.RegisterLibFunc(&api.observerCreateWithInfoCallback, lib, "AXObserverCreateWithInfoCallback")
	return api, nil
}

func axAvailable() error {
	if axLoadErr != nil {
		return fmt.Errorf("load ApplicationServices AX: %w", axLoadErr)
	}
	return nil
}

var (
	observerCallbackOnce sync.Once
	observerCallbackPtr  uintptr
	observerInfoOnce     sync.Once
	observerInfoPtr      uintptr
	observerMu           sync.Mutex
	observers            = make(map[axuiautomation.AXObserverRef]*observer)
)

func (a axAPI) observerCallback() axuiautomation.AXObserverCallback {
	observerCallbackOnce.Do(func() {
		callback := func(observerRef uintptr, element uintptr, notification uintptr, refcon uintptr) {
			observerMu.Lock()
			obs := observers[axuiautomation.AXObserverRef(observerRef)]
			observerMu.Unlock()
			if obs == nil {
				return
			}
			corefoundation.CFRetain(corefoundation.CFTypeRef(element))
			event := observerEvent{
				Element:      axuiautomation.AXUIElementRef(element),
				Notification: cfString(notification),
			}
			select {
			case obs.events <- event:
			default:
				corefoundation.CFRelease(corefoundation.CFTypeRef(element))
			}
			_ = refcon
		}
		observerCallbackPtr = purego.NewCallback(callback)
	})
	return axuiautomation.AXObserverCallback(observerCallbackPtr)
}

func (a axAPI) observerInfoCallback() axuiautomation.AXObserverCallback {
	observerInfoOnce.Do(func() {
		callback := func(observerRef uintptr, element uintptr, notification uintptr, info uintptr, refcon uintptr) {
			observerMu.Lock()
			obs := observers[axuiautomation.AXObserverRef(observerRef)]
			observerMu.Unlock()
			if obs == nil {
				return
			}
			corefoundation.CFRetain(corefoundation.CFTypeRef(element))
			if info != 0 {
				corefoundation.CFRetain(corefoundation.CFTypeRef(info))
			}
			event := observerEvent{
				Element:      axuiautomation.AXUIElementRef(element),
				Notification: cfString(notification),
				Info:         info,
			}
			select {
			case obs.events <- event:
			default:
				corefoundation.CFRelease(corefoundation.CFTypeRef(element))
				if info != 0 {
					corefoundation.CFRelease(corefoundation.CFTypeRef(info))
				}
			}
			_ = refcon
		}
		observerInfoPtr = purego.NewCallback(callback)
	})
	return axuiautomation.AXObserverCallback(observerInfoPtr)
}

func (a axAPI) trustPromptOptions() uintptr {
	key := corefoundation.CFStringCreateWithCString(0, "AXTrustedCheckOptionPrompt", uint32(corefoundation.KCFStringEncodingUTF8))
	keys := []uintptr{uintptr(key)}
	values := []uintptr{uintptr(corefoundation.KCFBooleanTrue)}
	dict := a.dictionaryCreate(0, unsafe.Pointer(&keys[0]), unsafe.Pointer(&values[0]), 1, nil, nil)
	corefoundation.CFRelease(corefoundation.CFTypeRef(key))
	return uintptr(dict)
}

func (a axAPI) createValue(valueType string, params map[string]any) (axuiautomation.AXValueRef, error) {
	switch valueType {
	case "CGPoint":
		x, err := numberParam(params, "x")
		if err != nil {
			return 0, err
		}
		y, err := numberParam(params, "y")
		if err != nil {
			return 0, err
		}
		p := struct{ X, Y float64 }{X: x, Y: y}
		return axuiautomation.AXValueCreate(1, unsafe.Pointer(&p)), nil
	case "CGSize":
		w, err := numberParam(params, "width")
		if err != nil {
			return 0, err
		}
		h, err := numberParam(params, "height")
		if err != nil {
			return 0, err
		}
		sz := struct{ Width, Height float64 }{Width: w, Height: h}
		return axuiautomation.AXValueCreate(2, unsafe.Pointer(&sz)), nil
	case "CGRect":
		x, err := numberParam(params, "x")
		if err != nil {
			return 0, err
		}
		y, err := numberParam(params, "y")
		if err != nil {
			return 0, err
		}
		w, err := numberParam(params, "width")
		if err != nil {
			return 0, err
		}
		h, err := numberParam(params, "height")
		if err != nil {
			return 0, err
		}
		r := struct {
			Origin struct{ X, Y float64 }
			Size   struct{ Width, Height float64 }
		}{}
		r.Origin.X, r.Origin.Y = x, y
		r.Size.Width, r.Size.Height = w, h
		return axuiautomation.AXValueCreate(3, unsafe.Pointer(&r)), nil
	default:
		return 0, fmt.Errorf("unsupported AXValue type %q", valueType)
	}
}

func (a axAPI) valuePoint(value uintptr) (map[string]any, bool) {
	var p struct{ X, Y float64 }
	if a.valueGetType(axuiautomation.AXValueRef(value)) != 1 {
		return nil, false
	}
	if !axuiautomation.AXValueGetValue(axuiautomation.AXValueRef(value), 1, unsafe.Pointer(&p)) {
		return nil, false
	}
	return map[string]any{"type": "CGPoint", "x": p.X, "y": p.Y}, true
}

func (a axAPI) valueSize(value uintptr) (map[string]any, bool) {
	var sz struct{ Width, Height float64 }
	if a.valueGetType(axuiautomation.AXValueRef(value)) != 2 {
		return nil, false
	}
	if !axuiautomation.AXValueGetValue(axuiautomation.AXValueRef(value), 2, unsafe.Pointer(&sz)) {
		return nil, false
	}
	return map[string]any{"type": "CGSize", "width": sz.Width, "height": sz.Height}, true
}

func (a axAPI) valueRect(value uintptr) (map[string]any, bool) {
	var r struct {
		Origin struct{ X, Y float64 }
		Size   struct{ Width, Height float64 }
	}
	if a.valueGetType(axuiautomation.AXValueRef(value)) != 3 {
		return nil, false
	}
	if !axuiautomation.AXValueGetValue(axuiautomation.AXValueRef(value), 3, unsafe.Pointer(&r)) {
		return nil, false
	}
	return map[string]any{"type": "CGRect", "x": r.Origin.X, "y": r.Origin.Y, "width": r.Size.Width, "height": r.Size.Height}, true
}

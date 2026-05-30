package permissions

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ui"
)

const (
	defaultAutomationTimeout = 12 * time.Second
	systemSettingsName       = "System Settings"
)

type AutomationOptions struct {
	AppName       string
	DismissAction string
	Timeout       time.Duration
	AutoPrompt    bool
	Remove        bool
	Reenable      bool
}

func Automate(ctx context.Context, opts AutomationOptions, reqs ...Requirement) error {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultAutomationTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	appName := strings.TrimSpace(opts.AppName)
	if appName == "" {
		appName = appNameForAutomation()
	}
	action := strings.TrimSpace(opts.DismissAction)
	if action == "" {
		action = "Allow"
	}
	if len(reqs) == 0 {
		reqs = []Requirement{ReqAccessibility, ReqScreenRecording}
	}
	if opts.AutoPrompt {
		if !opts.Remove && !opts.Reenable {
			if _, err := DismissPrompt(ctx, appName, action); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		}
		go func() {
			_, _ = DismissPrompt(ctx, appName, action)
		}()
	}
	if opts.Remove {
		for _, req := range reqs {
			if err := RemoveApp(ctx, req, appName); err != nil && !isNotFound(err) {
				return err
			}
		}
	}
	if opts.Reenable {
		for _, req := range reqs {
			if err := ReenableApp(ctx, req, appName); err != nil {
				return err
			}
		}
	}
	return nil
}

func DismissPrompt(ctx context.Context, appName, buttonName string) (bool, error) {
	appNeedle := strings.ToLower(strings.TrimSpace(appName))
	buttonNeedle := strings.ToLower(strings.TrimSpace(buttonName))
	if appNeedle == "" {
		return false, fmt.Errorf("empty app name")
	}
	if buttonNeedle == "" {
		buttonNeedle = "allow"
	}
	processes := []string{
		"UniversalAccessAuthWarn",
		"UserNotificationCenter",
		"CoreServicesUIAgent",
		"SystemUIServer",
		"System Settings",
		"System Preferences",
	}
	for {
		for _, name := range processes {
			pid := processPID(name)
			if pid == 0 {
				continue
			}
			app := axuiautomation.AXUIElementCreateApplication(pid)
			if app == 0 {
				continue
			}
			button, ok := findPromptButton(app, appNeedle, buttonNeedle)
			if !ok {
				continue
			}
			defer cfRelease(button)
			return true, axPress(button)
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func ReenableApp(ctx context.Context, req Requirement, appName string) error {
	if err := SetAppPermission(ctx, req, appName, false); err != nil && !isNotFound(err) {
		return err
	}
	return SetAppPermission(ctx, req, appName, true)
}

func SetAppPermission(ctx context.Context, req Requirement, appName string, enable bool) error {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return fmt.Errorf("empty app name")
	}
	paneURL, err := privacyPaneURL(req)
	if err != nil {
		return err
	}
	found, current, toggle, err := findAppToggle(ctx, appName, paneURL)
	if err != nil {
		return err
	}
	if !found {
		return permissionAppNotFoundError{AppName: appName}
	}
	defer cfRelease(toggle)
	if current == enable {
		return nil
	}
	if err := axPress(toggle); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

func RemoveApp(ctx context.Context, req Requirement, appName string) error {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return fmt.Errorf("empty app name")
	}
	paneURL, err := privacyPaneURL(req)
	if err != nil {
		return err
	}
	row, err := findAppRow(ctx, appName, paneURL)
	if err != nil {
		return err
	}
	defer cfRelease(row)
	if err := axPress(row); err != nil {
		_ = axPerform(row, "AXSelect")
	}
	time.Sleep(300 * time.Millisecond)
	button, err := findRemoveButton(ctx)
	if err != nil {
		return err
	}
	defer cfRelease(button)
	return axPress(button)
}

type permissionAppNotFoundError struct {
	AppName string
}

func (e permissionAppNotFoundError) Error() string {
	return fmt.Sprintf("app %q not found in permission list", e.AppName)
}

func isNotFound(err error) bool {
	var notFound permissionAppNotFoundError
	return errors.As(err, &notFound)
}

func appNameForAutomation() string {
	name := appName()
	if name == "" {
		return "axmcp"
	}
	return name
}

func privacyPaneURL(req Requirement) (string, error) {
	service := serviceName(req)
	if service == "" {
		return "", fmt.Errorf("unsupported requirement")
	}
	return ui.PrivacySettingsURL(service), nil
}

func findAppToggle(ctx context.Context, appName, paneURL string) (bool, bool, axuiautomation.AXUIElementRef, error) {
	root, err := openSettingsRoot(ctx, paneURL)
	if err != nil {
		return false, false, 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(appName))
	var out axuiautomation.AXUIElementRef
	var enabled bool
	walkAX(root, 5000, func(el axuiautomation.AXUIElementRef) bool {
		role := axString(el, "AXRole")
		title := axString(el, "AXTitle")
		desc := axString(el, "AXDescription")
		identifier := axString(el, "AXIdentifier")
		name := permissionItemName(title, desc, identifier)
		if isToggleRole(role) && matchesPermissionApp(name, title, desc, needle) {
			out = cfRetainAX(el)
			enabled = axBool(el, "AXValue")
			return true
		}
		if isContainerRole(role) && matchesPermissionApp(title, title, desc, needle) {
			for _, child := range axChildren(el) {
				if isToggleRole(axString(child, "AXRole")) {
					out = cfRetainAX(child)
					enabled = axBool(child, "AXValue")
					return true
				}
			}
		}
		return false
	})
	return out != 0, enabled, out, nil
}

func findAppRow(ctx context.Context, appName, paneURL string) (axuiautomation.AXUIElementRef, error) {
	root, err := openSettingsRoot(ctx, paneURL)
	if err != nil {
		return 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(appName))
	var row axuiautomation.AXUIElementRef
	walkAX(root, 5000, func(el axuiautomation.AXUIElementRef) bool {
		role := axString(el, "AXRole")
		if !isContainerRole(role) {
			return false
		}
		title := axString(el, "AXTitle")
		desc := axString(el, "AXDescription")
		value := axString(el, "AXValue")
		if matchesPermissionApp(title, title, desc, needle) || strings.Contains(strings.ToLower(value), needle) {
			row = cfRetainAX(el)
			return true
		}
		for _, child := range axChildren(el) {
			if matchesPermissionApp(axString(child, "AXTitle"), axString(child, "AXTitle"), axString(child, "AXDescription"), needle) ||
				strings.Contains(strings.ToLower(axString(child, "AXValue")), needle) {
				row = cfRetainAX(el)
				return true
			}
		}
		return false
	})
	if row == 0 {
		return 0, permissionAppNotFoundError{AppName: appName}
	}
	return row, nil
}

func findRemoveButton(ctx context.Context) (axuiautomation.AXUIElementRef, error) {
	root, err := systemSettingsRoot(ctx)
	if err != nil {
		return 0, err
	}
	var button axuiautomation.AXUIElementRef
	walkAX(root, 5000, func(el axuiautomation.AXUIElementRef) bool {
		if axString(el, "AXRole") != "AXButton" {
			return false
		}
		title := strings.ToLower(axString(el, "AXTitle"))
		desc := strings.ToLower(axString(el, "AXDescription"))
		identifier := strings.ToLower(axString(el, "AXIdentifier"))
		if title == "-" || title == "−" || title == "remove" ||
			strings.Contains(desc, "remove") ||
			strings.Contains(identifier, "remove") ||
			strings.Contains(identifier, "minus") {
			button = cfRetainAX(el)
			return true
		}
		return false
	})
	if button == 0 {
		return 0, fmt.Errorf("remove button not found")
	}
	return button, nil
}

func openSettingsRoot(ctx context.Context, paneURL string) (axuiautomation.AXUIElementRef, error) {
	_ = exec.Command("open", paneURL).Run()
	deadline := time.Now().Add(defaultAutomationTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	for {
		root, err := systemSettingsRoot(ctx)
		if err == nil && root != 0 {
			return root, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("system settings not available")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func systemSettingsRoot(ctx context.Context) (axuiautomation.AXUIElementRef, error) {
	pid := processPID(systemSettingsName)
	if pid == 0 {
		return 0, fmt.Errorf("system settings not running")
	}
	root := axuiautomation.AXUIElementCreateApplication(pid)
	if root == 0 {
		return 0, fmt.Errorf("create system settings AX element")
	}
	return root, nil
}

func processPID(name string) int32 {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0
	}
	var pid int32
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

func findPromptButton(root axuiautomation.AXUIElementRef, appName, buttonName string) (axuiautomation.AXUIElementRef, bool) {
	foundPrompt := false
	var button axuiautomation.AXUIElementRef
	walkAX(root, 1500, func(el axuiautomation.AXUIElementRef) bool {
		role := axString(el, "AXRole")
		title := strings.ToLower(axString(el, "AXTitle"))
		desc := strings.ToLower(axString(el, "AXDescription"))
		value := strings.ToLower(axString(el, "AXValue"))
		if role == "AXWindow" || role == "AXSheet" || role == "AXDialog" ||
			strings.Contains(title, appName) || strings.Contains(desc, appName) || strings.Contains(value, appName) ||
			strings.Contains(title, "screen recording") || strings.Contains(desc, "screen recording") ||
			strings.Contains(title, "would like to record") || strings.Contains(desc, "would like to record") ||
			strings.Contains(title, "private window picker") || strings.Contains(desc, "private window picker") {
			foundPrompt = true
		}
		if !foundPrompt || role != "AXButton" {
			return false
		}
		if strings.Contains(title, buttonName) || strings.Contains(desc, buttonName) ||
			(buttonName == "allow" && (title == "ok" || title == "continue")) {
			button = cfRetainAX(el)
			return true
		}
		return false
	})
	return button, button != 0
}

func walkAX(root axuiautomation.AXUIElementRef, max int, visit func(axuiautomation.AXUIElementRef) bool) {
	queue := []axuiautomation.AXUIElementRef{root}
	seen := make(map[axuiautomation.AXUIElementRef]bool)
	for len(queue) > 0 && len(seen) < max {
		el := queue[0]
		queue = queue[1:]
		if el == 0 || seen[el] {
			continue
		}
		seen[el] = true
		if visit(el) {
			return
		}
		queue = append(queue, axChildren(el)...)
	}
}

func permissionItemName(title, desc, identifier string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	if strings.TrimSpace(desc) != "" {
		return desc
	}
	identifier = strings.Split(identifier, "\x00")[0]
	if strings.Contains(identifier, "_Toggle") {
		return strings.Split(identifier, "_Toggle")[0]
	}
	return identifier
}

func matchesPermissionApp(name, title, desc, needle string) bool {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(name), needle) ||
		strings.Contains(strings.ToLower(title), needle) ||
		strings.Contains(strings.ToLower(desc), needle)
}

func isToggleRole(role string) bool {
	switch strings.Split(role, "\x00")[0] {
	case "AXCheckBox", "AXSwitch", "AXToggle":
		return true
	default:
		return false
	}
}

func isContainerRole(role string) bool {
	switch strings.Split(role, "\x00")[0] {
	case "AXRow", "AXCell", "AXGroup":
		return true
	default:
		return false
	}
}

func axString(el axuiautomation.AXUIElementRef, attr string) string {
	var value uintptr
	name := cfString(attr)
	defer cfRelease(uintptr(name))
	if axuiautomation.AXUIElementCopyAttributeValue(el, uintptr(name), &value) != 0 || value == 0 {
		return ""
	}
	defer cfRelease(value)
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value)) != corefoundation.CFStringGetTypeID() {
		return ""
	}
	buf := make([]byte, 4096)
	if !corefoundation.CFStringGetCString(corefoundation.CFStringRef(value), &buf[0], len(buf), uint32(corefoundation.KCFStringEncodingUTF8)) {
		return ""
	}
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		return string(buf[:i])
	}
	return string(buf)
}

func axBool(el axuiautomation.AXUIElementRef, attr string) bool {
	var value uintptr
	name := cfString(attr)
	defer cfRelease(uintptr(name))
	if axuiautomation.AXUIElementCopyAttributeValue(el, uintptr(name), &value) != 0 || value == 0 {
		return false
	}
	defer cfRelease(value)
	typeID := corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value))
	switch typeID {
	case corefoundation.CFBooleanGetTypeID():
		return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(value))
	case corefoundation.CFNumberGetTypeID():
		var n int32
		return corefoundation.CFNumberGetValue(corefoundation.CFNumberRef(value), corefoundation.KCFNumberSInt32Type, unsafe.Pointer(&n)) && n != 0
	default:
		return false
	}
}

func axChildren(el axuiautomation.AXUIElementRef) []axuiautomation.AXUIElementRef {
	var value uintptr
	name := cfString("AXChildren")
	defer cfRelease(uintptr(name))
	if axuiautomation.AXUIElementCopyAttributeValue(el, uintptr(name), &value) != 0 || value == 0 {
		return nil
	}
	defer cfRelease(value)
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value)) != corefoundation.CFArrayGetTypeID() {
		return nil
	}
	arr := corefoundation.CFArrayRef(value)
	n := corefoundation.CFArrayGetCount(arr)
	out := make([]axuiautomation.AXUIElementRef, 0, n)
	for i := range n {
		child := corefoundation.CFArrayGetValueAtIndex(arr, i)
		if child != nil {
			out = append(out, axuiautomation.AXUIElementRef(uintptr(child)))
		}
	}
	return out
}

func axPress(el axuiautomation.AXUIElementRef) error {
	return axPerform(el, "AXPress")
}

func axPerform(el axuiautomation.AXUIElementRef, action string) error {
	name := cfString(action)
	defer cfRelease(uintptr(name))
	if code := axuiautomation.AXUIElementPerformAction(el, uintptr(name)); code != 0 {
		return fmt.Errorf("%s: ax error %d", action, code)
	}
	return nil
}

func cfString(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(0, s, uint32(corefoundation.KCFStringEncodingUTF8))
}

func cfRetainAX(el axuiautomation.AXUIElementRef) axuiautomation.AXUIElementRef {
	return axuiautomation.AXUIElementRef(corefoundation.CFRetain(corefoundation.CFTypeRef(el)))
}

func cfRelease(ref uintptr) {
	if ref != 0 {
		corefoundation.CFRelease(corefoundation.CFTypeRef(ref))
	}
}

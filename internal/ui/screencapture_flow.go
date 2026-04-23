package ui

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
)

type permissionWindowRecord struct {
	Owner string
	Title string
}

type screenCaptureWindowPhase int

const (
	screenCaptureWindowPhaseRequest screenCaptureWindowPhase = iota
	screenCaptureWindowPhasePrompt
	screenCaptureWindowPhaseSettings
)

const (
	screenCapturePromptGrace = 4 * time.Second
	screenCaptureResetDelay  = 10 * time.Second
)

type screenCaptureWindowState struct {
	phase          screenCaptureWindowPhase
	requestTitle   string
	bodyText       string
	waitText       string
	showSpinner    bool
	showWait       bool
	requestEnabled bool
	showReset      bool
}

func currentScreenCaptureWindowState(requested bool, since time.Time) screenCaptureWindowState {
	elapsed := time.Duration(0)
	if requested && !since.IsZero() {
		elapsed = time.Since(since)
	}
	promptVisible, settingsVisible := currentScreenCaptureWindows()
	return screenCaptureWindowStateFor(requested, promptVisible, settingsVisible, elapsed)
}

func screenCaptureWindowStateFor(requested, promptVisible, settingsVisible bool, elapsed time.Duration) screenCaptureWindowState {
	switch {
	case !requested:
		return screenCaptureWindowState{
			phase:          screenCaptureWindowPhaseRequest,
			requestTitle:   "Request Permission",
			bodyText:       "Grant access in System Settings > Privacy & Security.",
			requestEnabled: true,
		}
	case promptVisible:
		return screenCaptureWindowState{
			phase:          screenCaptureWindowPhasePrompt,
			requestTitle:   "Waiting…",
			bodyText:       "Approve the macOS prompt, then return here.",
			waitText:       screenCaptureElapsedText("Waiting for your response", elapsed),
			showSpinner:    true,
			showWait:       true,
			requestEnabled: false,
		}
	case settingsVisible:
		return screenCaptureWindowState{
			phase:          screenCaptureWindowPhaseSettings,
			requestTitle:   "Open Settings",
			bodyText:       "Enable axmcp.app in System Settings, then return here.",
			waitText:       screenCaptureElapsedText("Waiting for the permission change", elapsed),
			showSpinner:    true,
			showWait:       true,
			requestEnabled: true,
			showReset:      elapsed >= screenCaptureResetDelay,
		}
	case elapsed < screenCapturePromptGrace:
		return screenCaptureWindowState{
			phase:          screenCaptureWindowPhasePrompt,
			requestTitle:   "Waiting…",
			bodyText:       "Look for the macOS prompt. It may open behind other windows.",
			waitText:       screenCaptureElapsedText("Waiting for the system prompt", elapsed),
			showSpinner:    true,
			showWait:       true,
			requestEnabled: false,
		}
	default:
		return screenCaptureWindowState{
			phase:          screenCaptureWindowPhaseSettings,
			requestTitle:   "Open Settings",
			bodyText:       "If no prompt appeared, enable axmcp.app in System Settings.",
			waitText:       screenCaptureElapsedText("Waiting for the permission change", elapsed),
			showSpinner:    true,
			showWait:       true,
			requestEnabled: true,
			showReset:      elapsed >= screenCaptureResetDelay,
		}
	}
}

func screenCaptureElapsedText(prefix string, elapsed time.Duration) string {
	secs := int(elapsed.Seconds())
	if secs <= 0 {
		return prefix + "…"
	}
	return fmt.Sprintf("%s… (%ds)", prefix, secs)
}

func currentScreenCaptureWindows() (promptVisible, settingsVisible bool) {
	windows := currentPermissionWindows()
	for _, window := range windows {
		if isScreenCapturePromptWindow(window) {
			promptVisible = true
		}
		if isScreenCaptureSettingsWindow(window) {
			settingsVisible = true
		}
	}
	return promptVisible, settingsVisible
}

func currentPermissionWindows() []permissionWindowRecord {
	windowList := coregraphics.CGWindowListCopyWindowInfo(coregraphics.KCGWindowListOptionOnScreenOnly, 0)
	if windowList == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(windowList))

	count := corefoundation.CFArrayGetCount(windowList)
	records := make([]permissionWindowRecord, 0, count)
	for i := range count {
		dictPtr := corefoundation.CFArrayGetValueAtIndex(windowList, i)
		dict := corefoundation.CFDictionaryRef(uintptr(dictPtr))
		owner := permissionDictGetString(dict, coregraphics.KCGWindowOwnerName)
		title := permissionDictGetString(dict, coregraphics.KCGWindowName)
		if strings.TrimSpace(owner) == "" && strings.TrimSpace(title) == "" {
			continue
		}
		records = append(records, permissionWindowRecord{Owner: owner, Title: title})
	}
	return records
}

func isScreenCapturePromptWindow(window permissionWindowRecord) bool {
	owner := strings.ToLower(strings.TrimSpace(window.Owner))
	title := strings.ToLower(strings.TrimSpace(window.Title))
	if title == "" || !strings.Contains(title, "screen recording") {
		return false
	}
	switch owner {
	case "universalaccessauthwarn", "coreservicesuiagent", "usernotificationcenter", "systemuiserver":
		return true
	default:
		return false
	}
}

func isScreenCaptureSettingsWindow(window permissionWindowRecord) bool {
	owner := strings.ToLower(strings.TrimSpace(window.Owner))
	title := strings.ToLower(strings.TrimSpace(window.Title))
	if owner != "system settings" && owner != "system preferences" && owner != "system settings (applescript)" {
		return false
	}
	return strings.Contains(title, "screen recording") || strings.Contains(title, "screen & system audio recording")
}

func permissionCFStringToGo(ref corefoundation.CFStringRef) string {
	if ref == 0 {
		return ""
	}
	buf := make([]byte, 1024)
	if corefoundation.CFStringGetCString(ref, &buf[0], len(buf), 0x08000100) {
		for i, b := range buf {
			if b == 0 {
				return string(buf[:i])
			}
		}
	}
	return ""
}

func permissionMakeCFString(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(0, s, 0x08000100)
}

func permissionCFPointer(ref uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&ref))
}

func permissionDictGetString(dict corefoundation.CFDictionaryRef, key string) string {
	k := permissionMakeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(k))
	v := corefoundation.CFDictionaryGetValue(dict, permissionCFPointer(uintptr(k)))
	if v == nil {
		return ""
	}
	return permissionCFStringToGo(corefoundation.CFStringRef(uintptr(v)))
}

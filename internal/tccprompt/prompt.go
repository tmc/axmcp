package tccprompt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/macgo"
)

const (
	kAXErrorSuccess = 0
)

var (
	setupOnce sync.Once
	setupErr  error

	bindOnce sync.Once
	bindErr  error

	axCreateApplication  func(int32) uintptr
	axCopyAttributeValue func(uintptr, uintptr, *uintptr) int32
	axPerformAction      func(uintptr, uintptr) int32
)

var popupProcesses = []string{
	"universalAccessAuthWarn",
	"UserNotificationCenter",
	"CoreServicesUIAgent",
	"SystemUIServer",
	"System Settings",
	"System Preferences",
}

type Prompt struct {
	Process     string   `json:"process"`
	PID         int32    `json:"pid"`
	Role        string   `json:"role"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Value       string   `json:"value,omitempty"`
	Texts       []string `json:"texts,omitempty"`
	Buttons     []string `json:"buttons,omitempty"`
}

func EnsureReady() error {
	setupOnce.Do(func() {
		debugf("EnsureReady: begin")
		runtime.LockOSThread()

		cfg := macgo.NewConfig().
			WithAppName("tcc-harness").
			WithPermissions(macgo.Accessibility).
			WithUsageDescription("NSAccessibilityUsageDescription", "tcc-harness uses Accessibility to inspect and click TCC system prompts for debugging.").
			WithUIMode(macgo.UIModeAccessory).
			WithSingleProcess()
		cfg.BundleID = "dev.tmc.tccharness"
		cfg = macsigning.Configure(cfg)
		ui.ConfigureIdentity("tcc-harness", cfg.BundleID)
		if os.Getenv("MACGO_DEBUG") == "1" {
			cfg = cfg.WithDebug()
		}

		debugf("EnsureReady: calling macgo.Start")
		if err := macgo.Start(cfg); err != nil {
			debugf("EnsureReady: macgo.Start failed: %v", err)
			setupErr = fmt.Errorf("macgo start: %w", err)
			return
		}
		debugf("EnsureReady: macgo.Start returned")
		if err := bindAX(); err != nil {
			debugf("EnsureReady: bindAX failed: %v", err)
			setupErr = err
			return
		}
		debugf("EnsureReady: bindAX returned")
	})
	return setupErr
}

func Bootstrap(timeout time.Duration) error {
	debugf("Bootstrap: start timeout=%s", timeout)
	if err := EnsureReady(); err != nil {
		debugf("Bootstrap: EnsureReady failed: %v", err)
		return err
	}
	if ui.IsTrusted() {
		debugf("Bootstrap: already trusted")
		return nil
	}
	debugf("Bootstrap: requesting accessibility permission")
	ui.RequestAccessibilityPermission()
	deadline := time.Now().Add(timeout)
	for {
		if ui.IsTrusted() {
			debugf("Bootstrap: trust granted")
			return nil
		}
		if timeout <= 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	debugf("Bootstrap: timed out waiting for trust")
	return fmt.Errorf("tcc-harness.app needs Accessibility permission; grant it in System Settings > Privacy & Security > Accessibility and retry")
}

func Inspect(appName string) ([]Prompt, error) {
	if err := Bootstrap(1500 * time.Millisecond); err != nil {
		return nil, err
	}

	var prompts []Prompt
	seen := make(map[string]bool)
	for _, process := range popupProcesses {
		pids, err := processPIDs(process)
		if err != nil {
			return nil, err
		}
		for _, pid := range pids {
			app := axCreateApplication(pid)
			if app == 0 {
				continue
			}
			windows := windowElements(app)
			if len(windows) == 0 {
				corefoundation.CFRelease(corefoundation.CFTypeRef(app))
				continue
			}
			for _, window := range windows {
				prompt := summarizePrompt(process, pid, window)
				if matchesPrompt(prompt, appName) && !seen[promptKey(prompt)] {
					seen[promptKey(prompt)] = true
					prompts = append(prompts, prompt)
				}
			}
			corefoundation.CFRelease(corefoundation.CFTypeRef(app))
		}
	}
	return prompts, nil
}

func promptKey(prompt Prompt) string {
	return strings.Join([]string{
		prompt.Process,
		strconv.Itoa(int(prompt.PID)),
		prompt.Role,
		prompt.Title,
		prompt.Description,
		prompt.Value,
		strings.Join(prompt.Texts, "\x1f"),
		strings.Join(prompt.Buttons, "\x1f"),
	}, "\x1e")
}

func Click(appName, buttonName string) error {
	if strings.TrimSpace(buttonName) == "" {
		return fmt.Errorf("button name is required")
	}
	if err := Bootstrap(1500 * time.Millisecond); err != nil {
		return err
	}

	appLower := strings.ToLower(strings.TrimSpace(appName))
	buttonLower := strings.ToLower(strings.TrimSpace(buttonName))
	for _, process := range popupProcesses {
		pids, err := processPIDs(process)
		if err != nil {
			return err
		}
		for _, pid := range pids {
			app := axCreateApplication(pid)
			if app == 0 {
				continue
			}
			windows := windowElements(app)
			for _, window := range windows {
				prompt := summarizePrompt(process, pid, window)
				if !matchesPrompt(prompt, appLower) {
					corefoundation.CFRelease(corefoundation.CFTypeRef(window))
					continue
				}
				button := findButton(window, buttonLower)
				if button == 0 {
					corefoundation.CFRelease(corefoundation.CFTypeRef(window))
					continue
				}
				action := ui.MkString("AXPress")
				errCode := axPerformAction(button, action)
				corefoundation.CFRelease(corefoundation.CFTypeRef(action))
				corefoundation.CFRelease(corefoundation.CFTypeRef(button))
				corefoundation.CFRelease(corefoundation.CFTypeRef(window))
				corefoundation.CFRelease(corefoundation.CFTypeRef(app))
				if errCode != kAXErrorSuccess {
					return fmt.Errorf("press %q in %s (pid %d): AX error %d", buttonName, process, pid, errCode)
				}
				return nil
			}
			corefoundation.CFRelease(corefoundation.CFTypeRef(app))
		}
	}
	return fmt.Errorf("no matching prompt with button %q for %q", buttonName, appName)
}

func bindAX() error {
	bindOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_GLOBAL)
		if err != nil {
			bindErr = fmt.Errorf("load ApplicationServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&axCreateApplication, lib, "AXUIElementCreateApplication")
		purego.RegisterLibFunc(&axCopyAttributeValue, lib, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&axPerformAction, lib, "AXUIElementPerformAction")
	})
	return bindErr
}

func processPIDs(name string) ([]int32, error) {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("pgrep %s: %w", name, err)
	}
	var pids []int32
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, int32(pid))
	}
	return pids, nil
}

func windowElements(app uintptr) []uintptr {
	seen := make(map[uintptr]bool)
	var windows []uintptr
	for _, attr := range []string{"AXWindows", "AXChildren", "AXSheets"} {
		for _, child := range childElements(app, attr) {
			role := stringAttribute(child, "AXRole")
			switch role {
			case "AXWindow", "AXSheet", "AXDialog":
				if seen[child] {
					corefoundation.CFRelease(corefoundation.CFTypeRef(child))
					continue
				}
				seen[child] = true
				windows = append(windows, child)
			default:
				corefoundation.CFRelease(corefoundation.CFTypeRef(child))
			}
		}
	}
	return windows
}

func summarizePrompt(process string, pid int32, window uintptr) Prompt {
	texts, buttons := collectTextsAndButtons(window)
	prompt := Prompt{
		Process:     process,
		PID:         pid,
		Role:        stringAttribute(window, "AXRole"),
		Title:       stringAttribute(window, "AXTitle"),
		Description: stringAttribute(window, "AXDescription"),
		Value:       stringAttribute(window, "AXValue"),
		Texts:       texts,
		Buttons:     buttons,
	}
	sort.Strings(prompt.Texts)
	sort.Strings(prompt.Buttons)
	return prompt
}

func collectTextsAndButtons(root uintptr) ([]string, []string) {
	queue := []uintptr{retain(root)}
	seen := make(map[uintptr]bool)
	texts := make(map[string]bool)
	buttons := make(map[string]bool)

	for len(queue) > 0 && len(seen) < 2000 {
		el := queue[0]
		queue = queue[1:]
		if el == 0 || seen[el] {
			release(el)
			continue
		}
		seen[el] = true

		role := strings.TrimSpace(stringAttribute(el, "AXRole"))
		for _, value := range []string{
			stringAttribute(el, "AXTitle"),
			stringAttribute(el, "AXDescription"),
			stringAttribute(el, "AXValue"),
		} {
			value = normalizeText(value)
			if value == "" {
				continue
			}
			texts[value] = true
			if role == "AXButton" {
				buttons[value] = true
			}
		}

		for _, attr := range []string{"AXChildren", "AXSheets"} {
			queue = append(queue, childElements(el, attr)...)
		}
		release(el)
	}

	return mapKeys(texts), mapKeys(buttons)
}

func findButton(root uintptr, buttonName string) uintptr {
	queue := []uintptr{retain(root)}
	seen := make(map[uintptr]bool)
	for len(queue) > 0 && len(seen) < 2000 {
		el := queue[0]
		queue = queue[1:]
		if el == 0 || seen[el] {
			release(el)
			continue
		}
		seen[el] = true
		role := strings.TrimSpace(stringAttribute(el, "AXRole"))
		if role == "AXButton" {
			for _, value := range []string{
				stringAttribute(el, "AXTitle"),
				stringAttribute(el, "AXDescription"),
				stringAttribute(el, "AXValue"),
			} {
				if containsFold(value, buttonName) {
					return el
				}
			}
		}
		for _, attr := range []string{"AXChildren", "AXSheets"} {
			queue = append(queue, childElements(el, attr)...)
		}
		release(el)
	}
	return 0
}

func childElements(el uintptr, attr string) []uintptr {
	val := copiedAttribute(el, attr)
	if val == 0 {
		return nil
	}
	defer release(val)

	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(val)) != corefoundation.CFArrayGetTypeID() {
		return nil
	}
	array := corefoundation.CFArrayRef(val)
	count := corefoundation.CFArrayGetCount(array)
	out := make([]uintptr, 0, count)
	for i := 0; i < count; i++ {
		child := uintptr(corefoundation.CFArrayGetValueAtIndex(array, i))
		if child == 0 {
			continue
		}
		out = append(out, retain(child))
	}
	return out
}

func copiedAttribute(el uintptr, attr string) uintptr {
	key := ui.MkString(attr)
	defer release(key)
	var val uintptr
	if axCopyAttributeValue(el, key, &val) != kAXErrorSuccess {
		return 0
	}
	return val
}

func stringAttribute(el uintptr, attr string) string {
	val := copiedAttribute(el, attr)
	if val == 0 {
		return ""
	}
	defer release(val)
	return cfString(val)
}

func cfString(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(ref)) == corefoundation.CFStringGetTypeID() {
		return cfStringToGo(corefoundation.CFStringRef(ref))
	}
	desc := corefoundation.CFCopyDescription(corefoundation.CFTypeRef(ref))
	if desc == 0 {
		return ""
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(desc))
	return cfStringToGo(desc)
}

func cfStringToGo(ref corefoundation.CFStringRef) string {
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

func matchesPrompt(prompt Prompt, appName string) bool {
	appName = strings.ToLower(strings.TrimSpace(appName))
	if appName == "" {
		return true
	}
	for _, value := range append([]string{prompt.Title, prompt.Description, prompt.Value}, prompt.Texts...) {
		if containsFold(value, appName) {
			return true
		}
	}
	return false
}

func containsFold(s, want string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	want = strings.ToLower(strings.TrimSpace(want))
	return want != "" && strings.Contains(s, want)
}

func normalizeText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

func debugf(format string, args ...any) {
	if os.Getenv("TCCPROMPT_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "tccprompt: "+format+"\n", args...)
}

func retain(ref uintptr) uintptr {
	if ref == 0 {
		return 0
	}
	return uintptr(corefoundation.CFRetain(corefoundation.CFTypeRef(ref)))
}

func release(ref uintptr) {
	if ref != 0 {
		corefoundation.CFRelease(corefoundation.CFTypeRef(ref))
	}
}

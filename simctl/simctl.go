package simctl

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type State string

const (
	StateBooted   State = "Booted"
	StateShutdown State = "Shutdown"
)

type Simulator struct {
	UDID    string `json:"udid"`
	Name    string `json:"name"`
	State   State  `json:"state"`
	Runtime string `json:"-"` // Populated manually from runtime map
	// simctl json has "devices" map where key is runtime string.
	IsAvailable bool `json:"isAvailable"`
}

// List returns all available simulators.
func List(ctx context.Context) ([]Simulator, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "list", "devices", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list failed: %w", err)
	}

	var result struct {
		Devices map[string][]Simulator `json:"devices"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse simctl output: %w", err)
	}

	var sims []Simulator
	for runtime, devices := range result.Devices {
		// filter out unavailable if needed? for now keep all.
		for _, dev := range devices {
			// Clean up runtime string if needed (it's often "com.apple.CoreSimulator.SimRuntime.iOS-17-2")
			// We'll just store it as is or simplified.
			dev.Runtime = runtime
			sims = append(sims, dev)
		}
	}
	return sims, nil
}

// ListApps lists installed applications for a device
func ListApps(ctx context.Context, udid string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "listapps", udid)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ListRunningApps lists running applications (processes ending in .app matching heuristic)
// Returns a list of application names (executable filenames)
func ListRunningApps(ctx context.Context, udid string) ([]string, error) {
	// launchctl list is too verbose. Use ps to find processes with .app in path.
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "spawn", udid, "/bin/ps", "-ax", "-o", "comm")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Filter output line by line
	lines := strings.Split(string(out), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, ".app/") {
			// Extract the executable name from the path
			// line contains the full path to the executable
			// e.g. /path/to/MyApp.app/MyApp
			parts := strings.Split(line, "/")
			if len(parts) > 0 {
				appName := parts[len(parts)-1]
				result = append(result, appName)
			}
		}
	}
	return result, nil
}

// Boot starts a simulator by UDID.
func Boot(ctx context.Context, udid string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "boot", udid)
	return cmd.Run()
}

// Shutdown stops a simulator by UDID.
func Shutdown(ctx context.Context, udid string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "shutdown", udid)
	return cmd.Run()
}

// InstallApp installs an .app bundle to a simulator.
func InstallApp(ctx context.Context, udid, appPath string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "install", udid, appPath)
	return cmd.Run()
}

// UninstallApp uninstalls an application by bundle ID.
func UninstallApp(ctx context.Context, udid, bundleID string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "uninstall", udid, bundleID)
	return cmd.Run()
}

// Launch starts an app on a simulator by bundle ID.
func Launch(_ context.Context, udid, bundleID string) error {
	// Use plain Command (not CommandContext) to avoid context cancellation killing the process
	cmd := exec.Command("/usr/bin/xcrun", "simctl", "launch", udid, bundleID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to launch app: %w, output: %s", err, string(out))
	}
	return nil
}

// LaunchApp is a wrapper around Launch to match the devicectl interface.
// It accepts optional arguments which are currently ignored for simctl basic launch.
func LaunchApp(ctx context.Context, udid, bundleID string, _ []string) error {
	return Launch(ctx, udid, bundleID)
}

// Terminate stops an app on a simulator by bundle ID.
func Terminate(ctx context.Context, udid, bundleID string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "terminate", udid, bundleID)
	return cmd.Run()
}

// SetAppearance sets the simulator appearance (light/dark).
func SetAppearance(ctx context.Context, udid, appearance string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "ui", udid, "appearance", appearance)
	return cmd.Run()
}

// GetOrientation returns the device orientation (portrait/landscape) by inspecting screenshot dimensions.
func GetOrientation(ctx context.Context, udid string) (string, error) {
	// Use temp file because stdout capture might send mixed output or fail
	f, err := os.CreateTemp("", "screenshot-*.png")
	if err != nil {
		return "unknown", err
	}
	f.Close()
	defer os.Remove(f.Name())

	// xcrun simctl io <udid> screenshot <file>
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "io", udid, "screenshot", f.Name())
	if err := cmd.Run(); err != nil {
		return "unknown", err
	}

	// Read file
	fileData, err := os.Open(f.Name())
	if err != nil {
		return "unknown", err
	}
	defer fileData.Close()

	cfg, _, err := image.DecodeConfig(fileData)
	if err != nil {
		return "unknown", fmt.Errorf("failed to decode screenshot: %v", err)
	}

	if cfg.Width > cfg.Height {
		return "landscape", nil
	}
	return "portrait", nil
}

// GetAppLogs captures recent logs for a process or subsystem
// udid: target simulator
// query: can be a bundle ID or process name. We'll search both predicate `process like "query" OR subsystem == "query"`
// duration: parsed duration string (e.g. "5m") for `log show --last`
func GetAppLogs(ctx context.Context, udid, query, duration string) (string, error) {
	if duration == "" {
		duration = "5m"
	}
	// xcrun simctl spawn booted log show --predicate '...' --last 5m
	// Constructing predicate to be flexible
	predicate := fmt.Sprintf(`process == "%s" OR subsystem == "%s"`, query, query)

	cmd := exec.CommandContext(ctx, "xcrun", "simctl", "spawn", udid, "log", "show", "--predicate", predicate, "--last", duration)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w\nOutput: %s", err, string(out))
	}
	return string(out), nil
}

// VideoRecording represents an active video recording process.
type VideoRecording struct {
	ID       string
	UDID     string
	FilePath string
	Cmd      *exec.Cmd
}

// Active recordings map
var activeRecordings = make(map[string]*VideoRecording)
var recordingCounter int

// StartVideoRecording starts recording video from a simulator.
// Returns a recording ID that can be used to stop the recording.
func StartVideoRecording(ctx context.Context, udid, outputPath, codec string) (string, error) {
	if codec == "" {
		codec = "hevc"
	}
	if outputPath == "" {
		outputPath = fmt.Sprintf("/tmp/simrecord_%d.mp4", recordingCounter)
	}

	cmd := exec.Command("/usr/bin/xcrun", "simctl", "io", udid, "recordVideo",
		"--codec="+codec, "--force", outputPath)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start recording: %w", err)
	}

	recordingCounter++
	id := fmt.Sprintf("rec_%d", recordingCounter)

	activeRecordings[id] = &VideoRecording{
		ID:       id,
		UDID:     udid,
		FilePath: outputPath,
		Cmd:      cmd,
	}

	return id, nil
}

// StopVideoRecording stops an active recording and returns the output file path.
func StopVideoRecording(id string) (string, error) {
	rec, ok := activeRecordings[id]
	if !ok {
		return "", fmt.Errorf("recording %s not found", id)
	}

	// Send interrupt signal to stop recording gracefully
	if rec.Cmd.Process != nil {
		rec.Cmd.Process.Signal(os.Interrupt)
		rec.Cmd.Wait() // Wait for process to finish writing
	}

	delete(activeRecordings, id)
	return rec.FilePath, nil
}

// ListActiveRecordings returns all active recording IDs.
func ListActiveRecordings() []string {
	ids := make([]string, 0, len(activeRecordings))
	for id := range activeRecordings {
		ids = append(ids, id)
	}
	return ids
}

// Screenshot captures a screenshot from the simulator.
func Screenshot(ctx context.Context, udid, outputPath, format string) error {
	if format == "" {
		format = "png"
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "io", udid, "screenshot",
		"--type="+format, outputPath)
	return cmd.Run()
}

// TriggerSimulatorAction triggers a hardware action via AppleScript.
// Supports: "home", "lock", "volume_up", "volume_down", "biometry_match", "biometry_fail"
func TriggerSimulatorAction(action string) error {
	var script string

	switch action {
	case "home":
		// Menu: Device -> Home (Shift-Cmd-H)
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Home" of menu "Device" of menu bar 1`
	case "lock":
		// Menu: Device -> Lock (Cmd-L)
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Lock" of menu "Device" of menu bar 1`
	case "volume_up":
		// Menu: Features -> Audio -> Volume Up (Cmd-Up) (Xcode 15+ structure varies)
		// Try "I/O" -> "Increase Volume" or "Features" -> "Audio" -> "Increase Volume"
		// Fallback to "Device" if needed. Best effort for generic.
		// Let's assume generic "I/O" > "Increase Volume" for modern Xcode.
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Increase Volume" of menu "I/O" of menu bar 1`
	case "volume_down":
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Decrease Volume" of menu "I/O" of menu bar 1`
	case "shake":
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Shake" of menu "Device" of menu bar 1`
	case "biometry_match":
		// Features -> Face ID -> Matching Face
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Matching Face" of menu "Face ID" of menu "Features" of menu bar 1`
	case "biometry_fail":
		// Features -> Face ID -> Non-matching Face
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Non-matching Face" of menu "Face ID" of menu "Features" of menu bar 1`
	case "biometry_enroll":
		script = `tell application "System Events" to tell process "Simulator" to click menu item "Enrolled" of menu "Face ID" of menu "Features" of menu bar 1`
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}

	// Wrapper to ensure Simulator is active?
	// Note: using 'ignoring application responses' might be needed if it blocks.
	fullScript := fmt.Sprintf(`
tell application "Simulator" to activate
tell application "System Events"
	tell process "Simulator"
		if exists menu bar 1 then
			%s
		end if
	end tell
end tell
`, script)

	cmd := exec.Command("osascript", "-e", fullScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("AppleScript failed: %w, output: %s", err, string(out))
	}
	return nil
}

// SetLocation sets the simulated location for a device.
func SetLocation(ctx context.Context, udid string, lat, lon float64) error {
	// xcrun simctl location <udid> set <lat,lon>
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "location", udid, "set", fmt.Sprintf("%f,%f", lat, lon))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set location: %w, output: %s", err, string(out))
	}
	return nil
}

// SetPrivacy grants or revokes permissions.
// action: "grant", "revoke", "reset"
// service: "all", "calendar", "contacts", "location", "location-always", "photos", etc.
// bundleID: target app
func SetPrivacy(ctx context.Context, udid, action, service, bundleID string) error {
	// xcrun simctl privacy <udid> <action> <service> <bundleID>
	args := []string{"simctl", "privacy", udid, action, service}
	if bundleID != "" {
		args = append(args, bundleID)
	}

	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set privacy: %w, output: %s", err, string(out))
	}
	return nil
}

// GetAppContainer gets the path to an app's container.
// containerType: "app", "data", "groups", "sile"
func GetAppContainer(ctx context.Context, udid, bundleID, containerType string) (string, error) {
	if containerType == "" {
		containerType = "data"
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "get_app_container", udid, bundleID, containerType)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get app container: %w, output: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// OpenURL opens a URL on the simulator.
func OpenURL(ctx context.Context, udid, url string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "openurl", udid, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to open URL: %w, output: %s", err, string(out))
	}
	return nil
}

// AddMedia adds photo/video files to the simulator's library.
func AddMedia(ctx context.Context, udid, path string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "simctl", "addmedia", udid, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add media: %w, output: %s", err, string(out))
	}
	return nil
}

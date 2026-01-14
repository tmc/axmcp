// Package devicectl provides wrappers around xcrun devicectl for physical device management.
package devicectl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// Device represents a physical iOS/macOS device.
type Device struct {
	Identifier   string            `json:"identifier"`
	Name         string            `json:"name"`
	DeviceType   string            `json:"deviceType"`
	Platform     string            `json:"platform"`
	OSVersion    string            `json:"osVersion"`
	ConnectionType string          `json:"connectionType"`
	State        string            `json:"state"`
	Extra        map[string]any    `json:"-"`
}

// DeviceListResult is the JSON output from devicectl list devices.
type DeviceListResult struct {
	Result struct {
		Devices []struct {
			Identifier           string `json:"identifier"`
			Name                 string `json:"deviceProperties.name"`
			DeviceType           string `json:"hardwareProperties.deviceType"`
			Platform             string `json:"hardwareProperties.platform"`
			OSVersionNumber      string `json:"deviceProperties.osVersionNumber"`
			ConnectionProperties struct {
				TransportType string `json:"transportType"`
			} `json:"connectionProperties"`
		} `json:"devices"`
	} `json:"result"`
}

// runDeviceCtl runs a devicectl command with JSON output.
func runDeviceCtl(ctx context.Context, args ...string) ([]byte, error) {
	// Create temp file for JSON output
	f, err := os.CreateTemp("", "devicectl-*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	jsonPath := f.Name()
	f.Close()
	defer os.Remove(jsonPath)

	// Add JSON output flag
	fullArgs := append(args, "--json-output", jsonPath)
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", append([]string{"devicectl"}, fullArgs...)...)

	// Run command - devicectl writes status to stdout, JSON to file
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("devicectl failed: %w\nOutput: %s", err, string(output))
	}

	// Read JSON output
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON output: %w", err)
	}

	return jsonData, nil
}

// ListDevices returns all physical devices known to CoreDevice.
func ListDevices(ctx context.Context) ([]Device, error) {
	data, err := runDeviceCtl(ctx, "list", "devices")
	if err != nil {
		return nil, err
	}

	// Parse the nested JSON structure
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse device list: %w", err)
	}

	result, ok := raw["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format: missing result")
	}

	devicesRaw, ok := result["devices"].([]any)
	if !ok {
		return []Device{}, nil // No devices
	}

	var devices []Device
	for _, d := range devicesRaw {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}

		dev := Device{
			Identifier: getString(dm, "identifier"),
			Extra:      dm,
		}

		// Extract nested properties
		if dp, ok := dm["deviceProperties"].(map[string]any); ok {
			dev.Name = getString(dp, "name")
			dev.OSVersion = getString(dp, "osVersionNumber")
		}
		if hp, ok := dm["hardwareProperties"].(map[string]any); ok {
			dev.DeviceType = getString(hp, "deviceType")
			dev.Platform = getString(hp, "platform")
		}
		if cp, ok := dm["connectionProperties"].(map[string]any); ok {
			dev.ConnectionType = getString(cp, "transportType")
		}
		if cs, ok := dm["connectionState"].(string); ok {
			dev.State = cs
		}

		devices = append(devices, dev)
	}

	return devices, nil
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// DeviceInfo returns detailed information about a specific device.
func DeviceInfo(ctx context.Context, identifier string) (map[string]any, error) {
	data, err := runDeviceCtl(ctx, "device", "info", "-d", identifier)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse device info: %w", err)
	}

	return result, nil
}

// InstallApp installs an application bundle on a physical device.
func InstallApp(ctx context.Context, identifier, appPath string) error {
	_, err := runDeviceCtl(ctx, "device", "install", "app", "-d", identifier, appPath)
	return err
}

// UninstallApp uninstalls an application from a physical device.
func UninstallApp(ctx context.Context, identifier, bundleID string) error {
	_, err := runDeviceCtl(ctx, "device", "uninstall", "app", "-d", identifier, bundleID)
	return err
}

// LaunchApp launches an application on a physical device.
func LaunchApp(ctx context.Context, identifier, bundleID string) error {
	_, err := runDeviceCtl(ctx, "device", "process", "launch", "-d", identifier, bundleID)
	return err
}

// TerminateApp terminates an application on a physical device.
func TerminateApp(ctx context.Context, identifier, bundleID string) error {
	_, err := runDeviceCtl(ctx, "device", "process", "terminate", "-d", identifier, bundleID)
	return err
}

// RebootDevice reboots a physical device.
func RebootDevice(ctx context.Context, identifier string) error {
	_, err := runDeviceCtl(ctx, "device", "reboot", "-d", identifier)
	return err
}

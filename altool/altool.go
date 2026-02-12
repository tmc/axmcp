// Package altool wraps xcrun altool for app upload and validation.
package altool

import (
	"context"
	"fmt"
	"os/exec"
)

// UploadApp uploads an .ipa or .pkg to App Store Connect via altool.
func UploadApp(ctx context.Context, filePath, apiKey, apiIssuer string) (string, error) {
	args := []string{"altool", "--upload-app", "-f", filePath, "-t", fileType(filePath)}
	args = append(args, authArgs(apiKey, apiIssuer)...)
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("altool upload: %w\n%s", err, out)
	}
	return string(out), nil
}

// ValidateApp validates an .ipa or .pkg before uploading.
func ValidateApp(ctx context.Context, filePath, apiKey, apiIssuer string) (string, error) {
	args := []string{"altool", "--validate-app", "-f", filePath, "-t", fileType(filePath)}
	args = append(args, authArgs(apiKey, apiIssuer)...)
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("altool validate: %w\n%s", err, out)
	}
	return string(out), nil
}

// ListApps lists apps for a provider via altool.
func ListApps(ctx context.Context, providerID, apiKey, apiIssuer string) (string, error) {
	args := []string{"altool", "--list-apps"}
	if providerID != "" {
		args = append(args, "--asc-provider", providerID)
	}
	args = append(args, authArgs(apiKey, apiIssuer)...)
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("altool list-apps: %w\n%s", err, out)
	}
	return string(out), nil
}

func authArgs(apiKey, apiIssuer string) []string {
	if apiKey == "" || apiIssuer == "" {
		return nil
	}
	return []string{"--apiKey", apiKey, "--apiIssuer", apiIssuer}
}

func fileType(path string) string {
	if len(path) > 4 && path[len(path)-4:] == ".pkg" {
		return "macos"
	}
	return "ios"
}

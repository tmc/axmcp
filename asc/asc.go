// Package asc wraps the asc CLI for App Store Connect API operations.
package asc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ascBin resolves the asc binary path.
func ascBin() string {
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, "go", "bin", "asc")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("asc"); err == nil {
		return p
	}
	return "asc"
}

// App represents an App Store Connect app.
type App struct {
	ID         string `json:"id"`
	BundleID   string `json:"bundleId"`
	Name       string `json:"name"`
	SKU        string `json:"sku"`
	PrimaryLocale string `json:"primaryLocale"`
}

// Build represents an App Store Connect build.
type Build struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	BuildNumber       string `json:"buildNumber"`
	ProcessingState   string `json:"processingState"`
	MinOSVersion      string `json:"minOsVersion"`
	UploadedDate      string `json:"uploadedDate"`
	ExpirationDate    string `json:"expirationDate"`
}

// BetaGroup represents a TestFlight beta group.
type BetaGroup struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	IsInternalGroup       bool   `json:"isInternalGroup"`
	PublicLinkEnabled     bool   `json:"publicLinkEnabled"`
	PublicLink            string `json:"publicLink,omitempty"`
	FeedbackEnabled       bool   `json:"feedbackEnabled"`
}

// Tester represents a TestFlight beta tester.
type Tester struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// AuthInfo represents authentication status information.
type AuthInfo struct {
	Authenticated bool   `json:"authenticated"`
	KeyID         string `json:"keyId,omitempty"`
	IssuerID      string `json:"issuerId,omitempty"`
	Output        string `json:"output"`
}

// run executes an asc command with JSON output and returns the raw bytes.
func run(ctx context.Context, args ...string) ([]byte, error) {
	args = append(args, "-o", "json")
	cmd := exec.CommandContext(ctx, ascBin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("asc %v: %w\n%s", args, err, out)
	}
	return out, nil
}

// runText executes an asc command and returns the raw text output.
func runText(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, ascBin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("asc %v: %w\n%s", args, err, out)
	}
	return string(out), nil
}

// ListApps returns all apps in the account.
func ListApps(ctx context.Context) ([]App, error) {
	out, err := run(ctx, "apps", "list")
	if err != nil {
		return nil, err
	}
	var apps []App
	if err := json.Unmarshal(out, &apps); err != nil {
		return nil, fmt.Errorf("parse apps: %w", err)
	}
	return apps, nil
}

// ListBuilds returns builds for an app, optionally filtered by app ID.
func ListBuilds(ctx context.Context, appID string) ([]Build, error) {
	args := []string{"builds", "list"}
	if appID != "" {
		args = append(args, "--app", appID)
	}
	out, err := run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var builds []Build
	if err := json.Unmarshal(out, &builds); err != nil {
		return nil, fmt.Errorf("parse builds: %w", err)
	}
	return builds, nil
}

// ListBetaGroups returns beta groups, optionally filtered by app ID.
func ListBetaGroups(ctx context.Context, appID string) ([]BetaGroup, error) {
	args := []string{"testers", "groups", "list"}
	if appID != "" {
		args = append(args, "--app", appID)
	}
	out, err := run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var groups []BetaGroup
	if err := json.Unmarshal(out, &groups); err != nil {
		return nil, fmt.Errorf("parse beta groups: %w", err)
	}
	return groups, nil
}

// CreateBetaGroup creates a new TestFlight beta group for an app.
func CreateBetaGroup(ctx context.Context, appID, name string) (*BetaGroup, error) {
	out, err := run(ctx, "testers", "groups", "create", "--app", appID, "--name", name)
	if err != nil {
		return nil, err
	}
	var group BetaGroup
	if err := json.Unmarshal(out, &group); err != nil {
		return nil, fmt.Errorf("parse beta group: %w", err)
	}
	return &group, nil
}

// AddTester adds a beta tester by email, optionally assigning to groups.
func AddTester(ctx context.Context, email, firstName, lastName string, groupIDs []string) error {
	args := []string{"testers", "add", "--email", email}
	if firstName != "" {
		args = append(args, "--first-name", firstName)
	}
	if lastName != "" {
		args = append(args, "--last-name", lastName)
	}
	for _, gid := range groupIDs {
		args = append(args, "--groups", gid)
	}
	_, err := runText(ctx, args...)
	return err
}

// InviteUser invites a user to App Store Connect with the given roles.
func InviteUser(ctx context.Context, email, firstName, lastName string, roles []string) error {
	args := []string{"users", "invite", "--email", email, "--first-name", firstName, "--last-name", lastName}
	for _, role := range roles {
		args = append(args, "--roles", role)
	}
	_, err := runText(ctx, args...)
	return err
}

// AuthStatus checks the current authentication status.
func AuthStatus(ctx context.Context) (*AuthInfo, error) {
	out, err := runText(ctx, "auth", "status")
	if err != nil {
		return &AuthInfo{
			Authenticated: false,
			Output:        fmt.Sprintf("auth check failed: %v", err),
		}, nil
	}
	return &AuthInfo{
		Authenticated: true,
		Output:        out,
	}, nil
}

package project

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Schemes returns available schemes via xcodebuild -list.
// It populates p.Schemes and returns it.
func (p *Project) GetSchemes(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "xcodebuild", "-list")
	if p.Type == TypeWorkspace {
		cmd.Args = append(cmd.Args, "-workspace", p.Path)
	} else {
		cmd.Args = append(cmd.Args, "-project", p.Path)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("xcodebuild -list failed: %w: %s", err, string(out))
	}

	schemes := parseSchemes(string(out))
	p.Schemes = schemes
	return schemes, nil
}

func parseSchemes(output string) []string {
	var schemes []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	inSchemesSection := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "Schemes:") {
			inSchemesSection = true
			continue
		}
		if inSchemesSection {
			if isXcodebuildListSection(line) {
				break
			}
			schemes = append(schemes, line)
		}
	}
	return schemes
}

func isXcodebuildListSection(line string) bool {
	switch line {
	case "Targets:", "Build Configurations:", "Schemes:":
		return true
	default:
		return strings.HasPrefix(line, "Information about ") && strings.HasSuffix(line, ":")
	}
}

type buildSettingEntry struct {
	BuildSettings map[string]string `json:"buildSettings"`
	Action        string            `json:"action"`
	Target        string            `json:"target"`
}

// BuildSettings returns build settings as key-value pairs.
func (p *Project) BuildSettings(ctx context.Context, scheme, config string) (map[string]string, error) {
	args := []string{"-showBuildSettings", "-json"}
	if p.Type == TypeWorkspace {
		args = append(args, "-workspace", p.Path)
	} else {
		args = append(args, "-project", p.Path)
	}
	if scheme != "" {
		args = append(args, "-scheme", scheme)
	}
	if config != "" {
		args = append(args, "-configuration", config)
	}

	cmd := exec.CommandContext(ctx, "xcodebuild", args...)

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("xcodebuild failed: %w: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}

	var entries []buildSettingEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse build settings json: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no build settings returned")
	}

	return entries[0].BuildSettings, nil
}
